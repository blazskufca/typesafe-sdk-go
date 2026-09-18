package typesafe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSystemOneRoundTrip(t *testing.T) {
	clearEnv(t)
	client, rec := newTestClient(t, respondJSON(http.StatusOK, resultBody, map[string]string{requestIDHeader: "req-42"}))

	resp, err := client.SystemOne(context.Background(),
		map[string]any{"document": "Hello 🌍"},
		Questions{
			"spam":    Noul{Instructions: "Spam?"},
			"tone":    Choice{Instructions: "Tone?", Criteria: map[string]Content{"friendly": nil, "hostile": nil}},
			"quality": Score{Instructions: "Quality?", Criteria: []Content{"bad", "ok", "great"}},
		},
	)
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}

	request := rec.Requests()[0]
	if request.Method != http.MethodPost || request.URL.Path != systemOnePath {
		t.Errorf("got %s %s, want POST %s", request.Method, request.URL.Path, systemOnePath)
	}
	want := map[string]any{
		"state": map[string]any{"document": "Hello 🌍"},
		"model": "jev-latest",
		"questions": map[string]any{
			"spam": map[string]any{"type": "noul", "instructions": "Spam?"},
			"tone": map[string]any{"type": "choice", "instructions": "Tone?",
				"criteria": map[string]any{"friendly": nil, "hostile": nil}},
			"quality": map[string]any{"type": "score", "instructions": "Quality?",
				"criteria": []any{"bad", "ok", "great"}},
		},
	}
	if got := rec.Body(t, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("request body\ngot  %#v\nwant %#v", got, want)
	}

	if resp.Model != "jev-latest" {
		t.Errorf("Model = %q, want jev-latest", resp.Model)
	}
	if resp.Usage != (Usage{InputTokens: 12, OutputTokens: 3}) {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.RequestID != "req-42" || resp.Status != http.StatusOK {
		t.Errorf("metadata = %q %d", resp.RequestID, resp.Status)
	}
	if len(resp.Answers) != 3 {
		t.Fatalf("Answers = %v", resp.Answers)
	}
	if got := resp.Nouls()["spam"].Noul; got != 0.98 {
		t.Errorf("noul = %v, want 0.98", got)
	}
	tone := resp.Choices()["tone"]
	if tone.Choice != "friendly" || tone.Confidence != 0.9 || tone.Probabilities["hostile"] != 0.1 {
		t.Errorf("choice = %+v", tone)
	}
	quality := resp.Scores()["quality"]
	if quality.Score != 1.7 || quality.Confidence != 0.8 {
		t.Errorf("score = %+v", quality)
	}
	if !reflect.DeepEqual(quality.Legend, map[int]Content{0: "bad", 1: "ok", 2: "great"}) {
		t.Errorf("legend = %#v", quality.Legend)
	}
	if !reflect.DeepEqual(quality.Probabilities, map[int]float64{0: 0.1, 1: 0.1, 2: 0.8}) {
		t.Errorf("probabilities = %#v", quality.Probabilities)
	}
	if string(resp.RawBody) == "" {
		t.Error("RawBody is empty")
	}
}

func TestTypedAnswerAccessors(t *testing.T) {
	clearEnv(t)
	client, _ := newTestClient(t, respondJSON(http.StatusOK, resultBody, nil))
	resp, err := client.SystemOne(context.Background(), "x", noulQuestions())
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}

	tone, ok := resp.As[*ChoiceAnswer]("tone")
	if !ok || tone.Choice != "friendly" {
		t.Errorf("As[*ChoiceAnswer](tone) = %+v, %v", tone, ok)
	}
	if _, ok := resp.As[*NoulAnswer]("tone"); ok {
		t.Error("As[*NoulAnswer](tone) matched a choice answer")
	}
	if _, ok := resp.As[*NoulAnswer]("absent"); ok {
		t.Error("As[*NoulAnswer](absent) matched a missing answer")
	}
	if got := AnswersOf[*ScoreAnswer](resp); len(got) != 1 || got["quality"].Score != 1.7 {
		t.Errorf("AnswersOf[*ScoreAnswer] = %+v", got)
	}
	if answer, ok := resp.Answers["spam"].(*NoulAnswer); !ok || answer.Type() != "noul" {
		t.Errorf("Answers[spam] = %#v", resp.Answers["spam"])
	}
}

func TestSystemOneInto(t *testing.T) {
	clearEnv(t)
	type answers struct {
		Spam NoulAnswer   `json:"spam"`
		Tone ChoiceAnswer `json:"tone"`
	}
	type triage struct {
		Model   string  `json:"model"`
		Answers answers `json:"answers"`
		ResponseMetadata
	}

	client, _ := newTestClient(t, respondJSON(http.StatusOK, resultBody, map[string]string{requestIDHeader: "req-into"}))
	result, err := SystemOneInto[triage](context.Background(), client, "x", noulQuestions())
	if err != nil {
		t.Fatalf("SystemOneInto: %v", err)
	}
	if result.Answers.Spam.Noul != 0.98 || result.Answers.Tone.Choice != "friendly" {
		t.Errorf("answers = %+v", result.Answers)
	}
	if result.RequestID != "req-into" {
		t.Errorf("RequestID = %q", result.RequestID)
	}

	method, err := client.Into[triage](context.Background(), "x", noulQuestions())
	if err != nil {
		t.Fatalf("Into: %v", err)
	}
	if method.Answers.Spam.Noul != result.Answers.Spam.Noul {
		t.Errorf("Into disagrees with SystemOneInto: %+v", method.Answers)
	}
}

func TestModelsList(t *testing.T) {
	clearEnv(t)
	body := `{"models": [{"name": "jev-latest", "description": "Fast model", "release_date": "2026-08-01", "context_window": 128000}]}`
	client, rec := newTestClient(t, respondJSON(http.StatusOK, body, nil))

	resp, err := client.Models.List(context.Background())
	if err != nil {
		t.Fatalf("Models.List: %v", err)
	}
	request := rec.Requests()[0]
	if request.Method != http.MethodGet || request.URL.Path != modelsPath {
		t.Errorf("got %s %s, want GET %s", request.Method, request.URL.Path, modelsPath)
	}
	if len(rec.Body(t, 0)) != 0 {
		t.Error("GET request carried a body")
	}
	want := []ModelMetadata{{Name: "jev-latest", Description: "Fast model", ReleaseDate: "2026-08-01"}}
	if !reflect.DeepEqual(resp.Models, want) {
		t.Errorf("Models = %+v, want %+v", resp.Models, want)
	}
	// Unmodeled fields are dropped from the card but stay in the raw body.
	if !strings.Contains(string(resp.RawBody), "context_window") {
		t.Error("RawBody lost the unmodeled field")
	}
}

func TestConfigResolution(t *testing.T) {
	for _, tc := range []struct {
		name       string
		env        map[string]string
		opts       []Option
		key, model string
	}{
		{name: "defaults", key: "test-key", model: DefaultModel},
		{
			name:  "environment",
			env:   map[string]string{APIKeyEnv: "  env-key  ", DefaultModelEnv: "  env-model  "},
			key:   "env-key",
			model: "env-model",
		},
		{
			name:  "options win",
			env:   map[string]string{APIKeyEnv: "env-key", DefaultModelEnv: "env-model"},
			opts:  []Option{WithAPIKey("code-key"), WithModel("code-model")},
			key:   "code-key",
			model: "code-model",
		},
		{
			name:  "blank environment is ignored",
			env:   map[string]string{DefaultModelEnv: " \t "},
			key:   "test-key",
			model: DefaultModel,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			client, rec := newTestClient(t, respondJSON(http.StatusOK, resultBody, nil), tc.opts...)
			if _, err := client.SystemOne(context.Background(), "x", noulQuestions()); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if got := rec.Requests()[0].Header.Get(authorizationHeader); got != "Bearer "+tc.key {
				t.Errorf("Authorization = %q, want Bearer %s", got, tc.key)
			}
			if got := rec.Body(t, 0)["model"]; got != tc.model {
				t.Errorf("model = %v, want %v", got, tc.model)
			}
		})
	}
}

func TestMissingAPIKey(t *testing.T) {
	clearEnv(t)
	for _, key := range []string{"", " \t\n "} {
		t.Setenv(APIKeyEnv, key)
		_, err := New()
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), APIKeyEnv) {
			t.Errorf("New() with key %q: %v", key, err)
		}
	}
}

func TestBaseURLTrailingSlashesTrimmed(t *testing.T) {
	clearEnv(t)
	for _, source := range []struct {
		name   string
		build  func() (*Client, error)
		expect string
	}{
		{
			name:   "option",
			build:  func() (*Client, error) { return New(WithAPIKey("k"), WithBaseURL("https://example.test/prefix///")) },
			expect: "https://example.test/prefix",
		},
		{
			name: "environment",
			build: func() (*Client, error) {
				t.Setenv(BaseURLEnv, "  https://env.test///  ")
				return New(WithAPIKey("k"))
			},
			expect: "https://env.test",
		},
	} {
		t.Run(source.name, func(t *testing.T) {
			client, err := source.build()
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if client.cfg.baseURL != source.expect {
				t.Errorf("baseURL = %q, want %q", client.cfg.baseURL, source.expect)
			}
		})
	}
}

func TestInvalidOptions(t *testing.T) {
	clearEnv(t)
	t.Setenv(APIKeyEnv, "test-key")
	for name, opt := range map[string]Option{
		"empty key":        WithAPIKey("  "),
		"empty base URL":   WithBaseURL(" "),
		"empty model":      WithModel(""),
		"zero timeout":     WithTimeout(0),
		"negative timeout": WithTimeout(-time.Second),
		"nil HTTP client":  WithHTTPClient(nil),
		"nil logger":       WithLogger(nil),
		"bad retries":      WithRetryPolicy(RetryPolicy{MaxRetries: -1}),
		"bad jitter":       WithRetryPolicy(RetryPolicy{BackoffJitter: 1.5}),
		"bad backoff":      WithRetryPolicy(RetryPolicy{BackoffInitial: -time.Second}),
		"bad budget":       WithRetryPolicy(RetryPolicy{Timeout: -time.Second}),
	} {
		if _, err := New(opt); !errors.Is(err, ErrConfig) {
			t.Errorf("New(%s) = %v, want ErrConfig", name, err)
		}
	}
}

func TestPerCallOptionsDoNotLeak(t *testing.T) {
	clearEnv(t)
	client, rec := newTestClient(t, respondJSON(http.StatusOK, resultBody, nil),
		WithModel("client-model"), WithHeader("X-Team", "default"))

	ctx := context.Background()
	if _, err := client.SystemOne(ctx, "x", noulQuestions(),
		WithModel("call-model"), WithHeader("X-Team", "call"), WithExtraBody(map[string]any{"beam_width": 4})); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if _, err := client.SystemOne(ctx, "x", noulQuestions()); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}

	first, second := rec.Body(t, 0), rec.Body(t, 1)
	if first["model"] != "call-model" || first["beam_width"] != float64(4) {
		t.Errorf("first body = %v", first)
	}
	if second["model"] != "client-model" {
		t.Errorf("second body = %v", second)
	}
	if _, present := second["beam_width"]; present {
		t.Error("extra body leaked into a later call")
	}
	requests := rec.Requests()
	if got := requests[0].Header.Get("X-Team"); got != "call" {
		t.Errorf("first X-Team = %q", got)
	}
	if got := requests[1].Header.Get("X-Team"); got != "default" {
		t.Errorf("second X-Team = %q", got)
	}
}

func TestExtraBodyOverridesModeledFields(t *testing.T) {
	clearEnv(t)
	client, rec := newTestClient(t, respondJSON(http.StatusOK, resultBody, nil))
	_, err := client.SystemOne(context.Background(), "hi", noulQuestions(),
		WithModel("call-model"),
		WithExtraBody(map[string]any{"model": "override-model", "nullable": nil}))
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	body := rec.Body(t, 0)
	if body["model"] != "override-model" {
		t.Errorf("model = %v, want override-model", body["model"])
	}
	if value, present := body["nullable"]; !present || value != nil {
		t.Errorf("nullable = %v, present = %v", value, present)
	}
}

func TestUnencodableBodyFailsBeforeNetwork(t *testing.T) {
	clearEnv(t)
	client, rec := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("an unencodable body reached the network")
	})
	_, err := client.SystemOne(context.Background(), "x", noulQuestions(),
		WithExtraBody(map[string]any{"bad": make(chan int)}))
	if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "could not be encoded") {
		t.Errorf("err = %v, want an encoding failure", err)
	}
	if rec.Count() != 0 {
		t.Error("a request was sent")
	}
}

func TestSDKHeadersCannotBeOverridden(t *testing.T) {
	clearEnv(t)
	protected := http.Header{
		authorizationHeader: []string{"injected-secret"},
		acceptHeader:        []string{"text/plain"},
		userAgentHeader:     []string{"wrong"},
		sdkHeader:           []string{"wrong"},
		runtimeHeader:       []string{"wrong"},
		contentTypeHeader:   []string{"text/plain"},
		retryCountHeader:    []string{"99"},
	}
	client, rec := newTestClient(t, respondJSON(http.StatusOK, resultBody, nil),
		WithHeaders(protected), WithHeader("X-Default", "kept"))

	if _, err := client.SystemOne(context.Background(), "x", noulQuestions(), WithHeaders(protected)); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	header := rec.Requests()[0].Header
	for name, want := range map[string]string{
		authorizationHeader: "Bearer test-key",
		acceptHeader:        jsonContentType,
		contentTypeHeader:   jsonContentType,
		userAgentHeader:     userAgent,
		sdkHeader:           userAgent,
		runtimeHeader:       runtimeDescription,
		"X-Default":         "kept",
	} {
		if got := header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := header.Get(retryCountHeader); got != "" {
		t.Errorf("%s = %q on the first attempt", retryCountHeader, got)
	}
}

func TestPerAttemptTimeout(t *testing.T) {
	clearEnv(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}, WithTimeout(50*time.Millisecond))

	_, err := client.Models.List(context.Background())
	var timeout *TimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("err = %v, want *TimeoutError", err)
	}
	if timeout.Duration != 50*time.Millisecond || !timeout.Timeout() {
		t.Errorf("timeout = %+v", timeout)
	}
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, ErrConnection) {
		t.Errorf("errors.Is: timeout=%v connection=%v", errors.Is(err, ErrTimeout), errors.Is(err, ErrConnection))
	}
}

func TestContextCancellationIsNotRetried(t *testing.T) {
	clearEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	client, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}, WithRetryPolicy(DefaultRetryPolicy()))

	_, err := client.Models.List(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if rec.Count() != 1 {
		t.Errorf("sent %d requests, want 1", rec.Count())
	}
}

func TestConnectionError(t *testing.T) {
	clearEnv(t)
	client, err := New(WithAPIKey("k"), WithBaseURL("http://127.0.0.1:1"), WithRetryPolicy(RetryPolicy{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Models.List(context.Background())
	var connection *ConnectionError
	if !errors.As(err, &connection) || !errors.Is(err, ErrConnection) {
		t.Fatalf("err = %v, want *ConnectionError", err)
	}
	if !strings.Contains(connection.Endpoint, modelsPath) {
		t.Errorf("Endpoint = %q", connection.Endpoint)
	}
}

func TestErrorStatusMapping(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct {
		status   int
		sentinel error
	}{
		{http.StatusBadRequest, ErrBadRequest},
		{http.StatusUnauthorized, ErrAuthentication},
		{http.StatusForbidden, ErrPermissionDenied},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusUnprocessableEntity, ErrUnprocessableEntity},
		{http.StatusTooManyRequests, ErrRateLimit},
		{http.StatusInternalServerError, ErrInternalServer},
		{http.StatusServiceUnavailable, ErrInternalServer},
		{http.StatusConflict, ErrAPI},
		{http.StatusFound, ErrAPI},
	} {
		body := `{"detail": {"message": "Server explanation"}}`
		client, _ := newTestClient(t, respondJSON(tc.status, body, map[string]string{
			requestIDHeader: "req_123", retryAfterMSHeader: "125",
		}))
		_, err := client.Models.List(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("status %d: err = %v, want *APIError", tc.status, err)
		}
		if !errors.Is(err, tc.sentinel) || !errors.Is(err, ErrAPI) {
			t.Errorf("status %d did not match its sentinel", tc.status)
		}
		if apiErr.Status != tc.status || apiErr.RequestID() != "req_123" {
			t.Errorf("status %d: %+v", tc.status, apiErr)
		}
		if apiErr.Message != "Server explanation" {
			t.Errorf("status %d: message = %q", tc.status, apiErr.Message)
		}
		want := fmt.Sprintf("%s: %d Server explanation (request_id=req_123)", apiErr.Endpoint, tc.status)
		if apiErr.Error() != want {
			t.Errorf("Error() = %q, want %q", apiErr.Error(), want)
		}
		if wait, ok := apiErr.RetryAfter(); !ok || wait != 125*time.Millisecond {
			t.Errorf("RetryAfter() = %v, %v", wait, ok)
		}
	}
}

func TestEndpointOmitsCredentialsAndQuery(t *testing.T) {
	clearEnv(t)
	target := mustParseURL(t, "https://user:password@example.test/v1/models?token=secret#fragment")
	if got := requestEndpoint(http.MethodGet, target); got != "GET https://example.test/v1/models" {
		t.Errorf("requestEndpoint = %q", got)
	}
}

// mustParseURL parses a URL or fails the test.
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %q: %v", raw, err)
	}
	return parsed
}
