package typesafe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// resultBody is the System One response the tests answer with, covering all three
// answer kinds.
const resultBody = `{
	"model": "jev-latest",
	"usage": {"input_tokens": 12, "output_tokens": 3},
	"answers": {
		"spam": {"type": "noul", "noul": 0.98},
		"tone": {"type": "choice", "choice": "friendly", "confidence": 0.9,
		         "probabilities": {"friendly": 0.9, "hostile": 0.1}},
		"quality": {"type": "score", "score": 1.7, "confidence": 0.8,
		            "legend": {"0": "bad", "1": "ok", "2": "great"},
		            "probabilities": {"0": 0.1, "1": 0.1, "2": 0.8}}
	}
}`

// noulQuestions is the smallest valid question set.
func noulQuestions() Questions {
	return Questions{"spam": Noul{Instructions: "Spam?"}}
}

// recorder captures what the SDK put on the wire.
type recorder struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
}

// Requests returns the requests received so far.
func (r *recorder) Requests() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*http.Request(nil), r.requests...)
}

// Body returns the body of the nth request.
func (r *recorder) Body(t *testing.T, n int) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if n >= len(r.bodies) {
		t.Fatalf("no request %d; got %d requests", n, len(r.bodies))
	}
	if len(r.bodies[n]) == 0 {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(r.bodies[n], &decoded); err != nil {
		t.Fatalf("decoding request %d: %v", n, err)
	}
	return decoded
}

// Count returns the number of requests received.
func (r *recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// newTestClient starts a server running handler and returns a client pointed at
// it, with retries off unless an option turns them back on.
func newTestClient(t *testing.T, handler http.HandlerFunc, opts ...Option) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.requests = append(rec.requests, r.Clone(context.Background()))
		rec.bodies = append(rec.bodies, body)
		rec.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	// The key comes from the environment when a test put one there, so that
	// resolution tests see what they configured.
	base := []Option{WithBaseURL(server.URL), WithRetryPolicy(RetryPolicy{})}
	if strings.TrimSpace(os.Getenv(APIKeyEnv)) == "" {
		base = append(base, WithAPIKey("test-key"))
	}
	client, err := New(append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client, rec
}

// respondJSON writes a JSON body with the given status and headers.
func respondJSON(status int, body string, header map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for name, value := range header {
			w.Header().Set(name, value)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// clock is a controllable time source for the retry loop: it never really waits,
// it just moves the clock forward and records how far.
type clock struct {
	mu     sync.Mutex
	now    time.Time
	delays []time.Duration
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.delays = append(c.delays, d)
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

func (c *clock) Delays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

// withClock drives the retry loop from a test clock.
func withClock(c *clock) Option {
	return func(cfg *config) error {
		cfg.now, cfg.sleep = c.Now, c.Sleep
		return nil
	}
}

// clearEnv blanks the SDK's environment variables for the duration of a test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{APIKeyEnv, BaseURLEnv, DefaultModelEnv, LogLevelEnv} {
		t.Setenv(name, "")
	}
}
