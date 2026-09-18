package typesafe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// retryPolicy returns a deterministic policy: no jitter, so the delays a test
// observes are exactly the ones the backoff computes.
func retryPolicy(maxRetries int) RetryPolicy {
	policy := DefaultRetryPolicy()
	policy.MaxRetries = maxRetries
	policy.BackoffJitter = 0
	return policy
}

func TestRetriesUntilSuccess(t *testing.T) {
	clearEnv(t)
	clock := newClock()
	attempts := 0
	client, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			respondJSON(http.StatusServiceUnavailable, `{"message": "temporarily unavailable"}`, nil)(w, r)
			return
		}
		respondJSON(http.StatusOK, `{"models": []}`, nil)(w, r)
	}, WithRetryPolicy(retryPolicy(2)), withClock(clock))

	resp, err := client.Models.List(context.Background())
	if err != nil {
		t.Fatalf("Models.List: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Errorf("Models = %+v", resp.Models)
	}
	if rec.Count() != 3 {
		t.Errorf("sent %d requests, want 3", rec.Count())
	}
	var counts []string
	for _, request := range rec.Requests() {
		counts = append(counts, request.Header.Get(retryCountHeader))
	}
	if want := []string{"", "1", "2"}; !equalStrings(counts, want) {
		t.Errorf("%s values = %q, want %q", retryCountHeader, counts, want)
	}
	if want := []time.Duration{500 * time.Millisecond, time.Second}; !equalDurations(clock.Delays(), want) {
		t.Errorf("delays = %v, want %v", clock.Delays(), want)
	}
}

func TestRetriesExhausted(t *testing.T) {
	clearEnv(t)
	client, rec := newTestClient(t, respondJSON(http.StatusServiceUnavailable, `{"message": "down"}`, nil),
		WithRetryPolicy(retryPolicy(1)), withClock(newClock()))

	_, err := client.Models.List(context.Background())
	if !errors.Is(err, ErrInternalServer) {
		t.Fatalf("err = %v", err)
	}
	if rec.Count() != 2 {
		t.Errorf("sent %d requests, want 2", rec.Count())
	}
}

func TestNonRetryableStatus(t *testing.T) {
	clearEnv(t)
	client, rec := newTestClient(t, respondJSON(http.StatusBadRequest, `{"message": "nope"}`, nil),
		WithRetryPolicy(retryPolicy(3)), withClock(newClock()))

	if _, err := client.Models.List(context.Background()); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v", err)
	}
	if rec.Count() != 1 {
		t.Errorf("sent %d requests, want 1", rec.Count())
	}
}

func TestRetryAfterHeaderIsHonored(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct {
		name   string
		header map[string]string
		want   time.Duration
	}{
		{"milliseconds", map[string]string{retryAfterMSHeader: "125"}, 125 * time.Millisecond},
		{"seconds", map[string]string{retryAfterHeader: "2"}, 2 * time.Second},
		{"negative seconds fall back to backoff", map[string]string{retryAfterHeader: "-1"}, 500 * time.Millisecond},
		{"no header falls back to backoff", nil, 500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			client, _ := newTestClient(t, respondJSON(http.StatusTooManyRequests, `{"message": "slow down"}`, tc.header),
				WithRetryPolicy(retryPolicy(1)), withClock(clock))

			if _, err := client.Models.List(context.Background()); !errors.Is(err, ErrRateLimit) {
				t.Fatalf("err = %v", err)
			}
			if delays := clock.Delays(); len(delays) != 1 || delays[0] != tc.want {
				t.Errorf("delays = %v, want [%v]", delays, tc.want)
			}
		})
	}
}

func TestRetryAfterIgnoredWhenDisabled(t *testing.T) {
	clearEnv(t)
	clock := newClock()
	policy := retryPolicy(1)
	policy.RespectRetryAfter = false
	client, _ := newTestClient(t, respondJSON(http.StatusTooManyRequests, `{}`, map[string]string{retryAfterHeader: "30"}),
		WithRetryPolicy(policy), withClock(clock))

	if _, err := client.Models.List(context.Background()); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("err = %v", err)
	}
	if delays := clock.Delays(); len(delays) != 1 || delays[0] != 500*time.Millisecond {
		t.Errorf("delays = %v, want [500ms]", delays)
	}
}

func TestRetryBudget(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct {
		name     string
		budget   time.Duration
		spend    time.Duration
		delay    string
		attempts int
	}{
		{"budget stops a long wait", time.Second, 0, "60", 1},
		{"budget allows a short wait", 10 * time.Second, 0, "1", 3},
		{"attempts spend the budget", 3 * time.Second, 750 * time.Millisecond, "1", 2},
		{"no budget", 0, time.Hour, "1", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			policy := retryPolicy(2)
			policy.Timeout = tc.budget
			client, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				clock.Advance(tc.spend)
				respondJSON(http.StatusTooManyRequests, `{"message": "slow down"}`, map[string]string{retryAfterHeader: tc.delay})(w, r)
			}, WithRetryPolicy(policy), withClock(clock))

			if _, err := client.Models.List(context.Background()); !errors.Is(err, ErrRateLimit) {
				t.Fatalf("err = %v", err)
			}
			if rec.Count() != tc.attempts {
				t.Errorf("sent %d requests, want %d", rec.Count(), tc.attempts)
			}
		})
	}
}

func TestConnectionErrorsAreRetried(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct {
		name     string
		retry    bool
		attempts int
	}{
		{"retried", true, 3},
		{"not retried", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				// Hang up mid-response so the client sees a transport failure.
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Fatal("the test server does not support hijacking")
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Fatalf("Hijack: %v", err)
				}
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\ntruncated")
				conn.Close()
			})
			policy := retryPolicy(2)
			policy.RetryConnectionErrors = tc.retry
			client, _ := newTestClient(t, handler, WithRetryPolicy(policy), withClock(newClock()))

			_, err := client.Models.List(context.Background())
			if !errors.Is(err, ErrConnection) {
				t.Fatalf("err = %v, want ErrConnection", err)
			}
			if attempts != tc.attempts {
				t.Errorf("made %d attempts, want %d", attempts, tc.attempts)
			}
		})
	}
}

func TestCustomRetryPredicate(t *testing.T) {
	clearEnv(t)
	policy := retryPolicy(1)
	policy.RetryStatuses = nil
	policy.Retry = func(err error) bool { return errors.Is(err, ErrNotFound) }
	client, rec := newTestClient(t, respondJSON(http.StatusNotFound, `{}`, nil),
		WithRetryPolicy(policy), withClock(newClock()))

	if _, err := client.Models.List(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if rec.Count() != 2 {
		t.Errorf("sent %d requests, want 2", rec.Count())
	}
}

func TestZeroRetryPolicyDisablesRetries(t *testing.T) {
	clearEnv(t)
	client, rec := newTestClient(t, respondJSON(http.StatusServiceUnavailable, `{}`, nil), WithRetryPolicy(RetryPolicy{}))
	if _, err := client.Models.List(context.Background()); !errors.Is(err, ErrInternalServer) {
		t.Fatalf("err = %v", err)
	}
	if rec.Count() != 1 {
		t.Errorf("sent %d requests, want 1", rec.Count())
	}
}

func TestBackoff(t *testing.T) {
	initial, maximum := 500*time.Millisecond, 5*time.Second
	for attempt, want := range map[int]time.Duration{1: 500 * time.Millisecond, 2: time.Second, 3: 2 * time.Second, 4: 4 * time.Second, 5: maximum, 9: maximum} {
		if got := backoff(attempt, initial, maximum, 0); got != want {
			t.Errorf("backoff(%d) = %v, want %v", attempt, got, want)
		}
	}
	for _, disabled := range []struct{ initial, maximum time.Duration }{{0, maximum}, {initial, 0}, {0, 0}} {
		if got := backoff(1, disabled.initial, disabled.maximum, 0.25); got != 0 {
			t.Errorf("backoff with initial=%v max=%v = %v, want 0", disabled.initial, disabled.maximum, got)
		}
	}
	// Jitter only ever shortens a delay, and never past its share of it.
	for attempt := 1; attempt <= 6; attempt++ {
		full := backoff(attempt, initial, maximum, 0)
		for range 50 {
			got := backoff(attempt, initial, maximum, 0.25)
			if got > full || got < time.Duration(float64(full)*0.75) {
				t.Fatalf("backoff(%d) with jitter = %v, want within 25%% of %v", attempt, got, full)
			}
		}
	}
	// A huge attempt number must not overflow into a negative delay.
	if got := backoff(4096, initial, maximum, 0); got != maximum {
		t.Errorf("backoff(4096) = %v, want %v", got, maximum)
	}
}

func TestRetryPolicyValidation(t *testing.T) {
	for name, policy := range map[string]RetryPolicy{
		"negative retries": {MaxRetries: -1},
		"negative initial": {BackoffInitial: -time.Second},
		"negative maximum": {BackoffMax: -time.Second},
		"jitter below 0":   {BackoffJitter: -0.1},
		"jitter above 1":   {BackoffJitter: 1.1},
		"negative budget":  {Timeout: -time.Second},
	} {
		if err := policy.validate(); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: validate() = %v, want ErrConfig", name, err)
		}
	}
	if err := DefaultRetryPolicy().validate(); err != nil {
		t.Errorf("DefaultRetryPolicy().validate() = %v", err)
	}
}

func TestRetryPolicyIsCopiedPerCall(t *testing.T) {
	clearEnv(t)
	client, _ := newTestClient(t, respondJSON(http.StatusOK, `{"models": []}`, nil))
	policy := DefaultRetryPolicy()
	if _, err := client.Models.List(context.Background(), WithRetryPolicy(policy)); err != nil {
		t.Fatalf("Models.List: %v", err)
	}
	// Mutating the caller's slice must not reach into the client's copy.
	policy.RetryStatuses[0] = 999
	if client.cfg.retry.MaxRetries != 0 {
		t.Errorf("the per-call policy leaked into the client: %+v", client.cfg.retry)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestDefaultRetryStatuses(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct {
		status   int
		attempts int
	}{
		{408, 3}, {429, 3}, {500, 3}, {503, 3}, {599, 3},
		{400, 1}, {401, 1}, {403, 1}, {404, 1}, {409, 1}, {422, 1}, {302, 1},
	} {
		t.Run(http.StatusText(tc.status)+"/"+itoa(tc.status), func(t *testing.T) {
			client, rec := newTestClient(t,
				respondJSON(tc.status, `{"message": "failed"}`, map[string]string{retryAfterMSHeader: "0"}),
				WithRetryPolicy(retryPolicy(2)), withClock(newClock()))

			_, err := client.Models.List(context.Background())
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tc.status {
				t.Fatalf("err = %v", err)
			}
			if rec.Count() != tc.attempts {
				t.Errorf("sent %d requests, want %d", rec.Count(), tc.attempts)
			}
			var counts []string
			for _, request := range rec.Requests() {
				counts = append(counts, request.Header.Get(retryCountHeader))
			}
			if want := []string{"", "1", "2"}[:tc.attempts]; !equalStrings(counts, want) {
				t.Errorf("%s values = %q, want %q", retryCountHeader, counts, want)
			}
		})
	}
}

func TestEachCallGetsAFreshBudget(t *testing.T) {
	clearEnv(t)
	clock := newClock()
	policy := retryPolicy(2)
	policy.Timeout = 3 * time.Second
	client, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		clock.Advance(time.Second)
		respondJSON(http.StatusTooManyRequests, `{"message": "slow down"}`, map[string]string{retryAfterHeader: "0.5"})(w, r)
	}, WithRetryPolicy(policy), withClock(clock))

	for call := range 2 {
		before := rec.Count()
		if _, err := client.Models.List(context.Background()); !errors.Is(err, ErrRateLimit) {
			t.Fatalf("call %d: err = %v", call, err)
		}
		// 1s + 0.5s + 1s + 0.5s = 3s reaches the budget before a third attempt.
		if attempts := rec.Count() - before; attempts != 2 {
			t.Errorf("call %d made %d attempts, want 2", call, attempts)
		}
	}
}

func TestPerCallRetryPolicyOverride(t *testing.T) {
	clearEnv(t)
	clientPolicy := retryPolicy(0)
	callPolicy := retryPolicy(2)
	callPolicy.RetryStatuses = []int{http.StatusConflict}

	client, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusTooManyRequests
		if r.Header.Get("X-Call") == "override" {
			status = http.StatusConflict
		}
		respondJSON(status, `{"message": "failed"}`, map[string]string{retryAfterMSHeader: "0"})(w, r)
	}, WithRetryPolicy(clientPolicy), withClock(newClock()))

	for _, tc := range []struct {
		name     string
		opts     []Option
		attempts int
	}{
		{"override", []Option{WithHeader("X-Call", "override"), WithRetryPolicy(callPolicy)}, 3},
		{"inherited", nil, 1},
		{"override again", []Option{WithHeader("X-Call", "override"), WithRetryPolicy(callPolicy)}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := rec.Count()
			if _, err := client.SystemOne(context.Background(), "hello", noulQuestions(), tc.opts...); err == nil {
				t.Fatal("SystemOne succeeded, want an API error")
			}
			if attempts := rec.Count() - before; attempts != tc.attempts {
				t.Errorf("made %d attempts, want %d", attempts, tc.attempts)
			}
		})
	}
}

func TestConnectionErrorRecovers(t *testing.T) {
	clearEnv(t)
	clock := newClock()
	attempts := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("the test server does not support hijacking")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("Hijack: %v", err)
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\ntruncated")
			conn.Close()
			return
		}
		respondJSON(http.StatusOK, `{"models": []}`, nil)(w, r)
	}, WithRetryPolicy(mustJitteredPolicy()), withClock(clock))

	resp, err := client.Models.List(context.Background())
	if err != nil {
		t.Fatalf("Models.List: %v", err)
	}
	if len(resp.Models) != 0 || attempts != 3 {
		t.Errorf("Models = %v after %d attempts", resp.Models, attempts)
	}
	delays := clock.Delays()
	if len(delays) != 2 {
		t.Fatalf("delays = %v, want two", delays)
	}
	if delays[0] < 375*time.Millisecond || delays[0] > 500*time.Millisecond {
		t.Errorf("first delay = %v, want between 375ms and 500ms", delays[0])
	}
	if delays[1] < 750*time.Millisecond || delays[1] > time.Second {
		t.Errorf("second delay = %v, want between 750ms and 1s", delays[1])
	}
}

// mustJitteredPolicy returns the default policy, jitter included.
func mustJitteredPolicy() RetryPolicy {
	policy := DefaultRetryPolicy()
	policy.MaxRetries = 2
	return policy
}

func itoa(value int) string { return strconv.Itoa(value) }
