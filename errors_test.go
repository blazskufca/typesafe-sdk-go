package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestErrorMessageExtraction(t *testing.T) {
	long := strings.Repeat("x", 201)
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"empty body", "", "status code (no body)"},
		{"null body", "null", "status code (no body)"},
		{"empty list", "[]", "[]"},
		{"number", "42", "42"},
		{"plain text", "plain text", "plain text"},
		{"long plain text is kept whole", long, long},
		{"long JSON is truncated", `{"unknown":"` + long + `"}`, `{"unknown":"` + strings.Repeat("x", 188) + "…"},
		{"error wins", `{"error":"error","message":"message","detail":"detail"}`, "error"},
		{"nested error", `{"error":{"message":"nested error"},"message":"message"}`, "nested error"},
		{"message", `{"message":"message","detail":"detail"}`, "message"},
		{"detail", `{"detail":"detail"}`, "detail"},
		{"nested detail", `{"detail":{"message":"nested detail"}}`, "nested detail"},
		{
			name: "validation detail",
			body: `{"detail":[{"loc":["body","questions","q","score","criteria",0],"msg":"Invalid"},{"msg":"Missing"},{}]}`,
			want: "questions.q.score.criteria.0: Invalid; Missing",
		},
		{"empty error falls back to the body", `{"error":"","message":"ignored"}`, `{"error":"","message":"ignored"}`},
		{"unusable detail falls back to the body", `{"detail":[null,42,{"msg":4}]}`, `{"detail":[null,42,{"msg":4}]}`},
		{"unmodeled body", `{"unexpected":true}`, `{"unexpected":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorMessage(decodeBody([]byte(tc.body))); got != tc.want {
				t.Errorf("errorMessage(%s)\ngot  %q\nwant %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestErrorMessageOnInvalidUTF8(t *testing.T) {
	got := errorMessage(decodeBody([]byte("not JSON: \xff")))
	if got != "not JSON: \uFFFD" {
		t.Errorf("errorMessage = %q", got)
	}
}

func TestErrorMessagesOverTheWire(t *testing.T) {
	clearEnv(t)
	client, _ := newTestClient(t, respondJSON(http.StatusBadRequest, `{"detail": "Invalid request"}`, nil))
	_, err := client.Models.List(context.Background())
	if err == nil || !strings.HasSuffix(err.Error(), "/v1/models: 400 Invalid request") {
		t.Errorf("err = %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	for _, tc := range []struct {
		name   string
		header http.Header
		want   time.Duration
		ok     bool
	}{
		{"milliseconds", http.Header{retryAfterMSHeader: {"125"}}, 125 * time.Millisecond, true},
		{"seconds", http.Header{retryAfterHeader: {"2"}}, 2 * time.Second, true},
		{"fractional seconds", http.Header{retryAfterHeader: {"0.5"}}, 500 * time.Millisecond, true},
		{"milliseconds win", http.Header{retryAfterMSHeader: {"125"}, retryAfterHeader: {"30"}}, 125 * time.Millisecond, true},
		{"empty value", http.Header{retryAfterMSHeader: {""}}, 0, true},
		{"negative seconds", http.Header{retryAfterHeader: {"-1"}}, 0, false},
		{"garbage", http.Header{retryAfterHeader: {"soon"}}, 0, false},
		{"absent", http.Header{}, 0, false},
		{"unparseable milliseconds fall through", http.Header{retryAfterMSHeader: {"NaN"}, retryAfterHeader: {"1.5"}}, 1500 * time.Millisecond, true},
		{"garbage milliseconds fall through", http.Header{retryAfterMSHeader: {"bad"}, retryAfterHeader: {"2"}}, 2 * time.Second, true},
		{"negative milliseconds fall through", http.Header{retryAfterMSHeader: {"-1"}, retryAfterHeader: {"2"}}, 2 * time.Second, true},
		{"infinite milliseconds", http.Header{retryAfterMSHeader: {"inf"}}, 0, false},
		{"seconds too large to represent", http.Header{retryAfterHeader: {"1e308"}}, 0, false},
		{"zero milliseconds win over a long wait", http.Header{retryAfterMSHeader: {"0"}, retryAfterHeader: {"50"}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseRetryAfter(tc.header)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Errorf("parseRetryAfter = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}

	got, ok := parseRetryAfter(http.Header{retryAfterHeader: {future}})
	if !ok || got < 80*time.Second || got > 90*time.Second {
		t.Errorf("parseRetryAfter(HTTP date) = %v, %v", got, ok)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got, ok := parseRetryAfter(http.Header{retryAfterHeader: {past}}); !ok || got != 0 {
		t.Errorf("parseRetryAfter(past date) = %v, %v; want 0, true", got, ok)
	}
}

func TestSecretHeadersAreRedactedEverywhere(t *testing.T) {
	clearEnv(t)
	// Every credential-bearing name, on the way out and on the way back, at every
	// status the SDK logs.
	for _, header := range []string{
		"Authorization", "Proxy-Authorization", "X-Api-Key", "Api-Key", "Cookie",
		"Set-Cookie", "X-Access-Token", "X-Client-Secret", "X-MiXeD-ToKeN",
	} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusTooManyRequests} {
			t.Run(header+"/"+http.StatusText(status), func(t *testing.T) {
				var logs bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
				body := `{"models": []}`
				if status != http.StatusOK {
					body = `{"message": "failure"}`
				}
				policy := retryPolicy(2)
				client, rec := newTestClient(t,
					respondJSON(status, body, map[string]string{header: "response-credential", "X-Visible": "response-visible"}),
					WithAPIKey("auth-credential"), WithLogger(logger), withClock(newClock()),
					WithRetryPolicy(policy),
					WithHeaders(http.Header{header: {"request-credential"}, "X-Visible": {"request-visible"}}),
				)

				_, err := client.Models.List(context.Background())
				if (err != nil) != (status != http.StatusOK) {
					t.Fatalf("err = %v for status %d", err, status)
				}
				attempts := 1
				if status == http.StatusTooManyRequests {
					attempts = 3
				}
				if rec.Count() != attempts {
					t.Errorf("sent %d requests, want %d", rec.Count(), attempts)
				}

				output := logs.String()
				for _, secret := range []string{"auth-credential", "request-credential", "response-credential"} {
					if strings.Contains(output, secret) {
						t.Errorf("the log leaked %q:\n%s", secret, output)
					}
				}
				for _, want := range []string{"request-visible", "response-visible", "***"} {
					if !strings.Contains(output, want) {
						t.Errorf("the log is missing %q:\n%s", want, output)
					}
				}
				if status == http.StatusTooManyRequests && !strings.Contains(output, "retrying") {
					t.Errorf("retries were not logged:\n%s", output)
				}
			})
		}
	}
}

func TestLogLevelControlsOutput(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct {
		level  slog.Level
		levels map[slog.Level]bool
	}{
		{slog.LevelDebug, map[slog.Level]bool{slog.LevelDebug: true, slog.LevelInfo: true}},
		{slog.LevelInfo, map[slog.Level]bool{slog.LevelInfo: true}},
		{slog.LevelWarn, map[slog.Level]bool{}},
	} {
		t.Run(tc.level.String(), func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: tc.level}))
			client, _ := newTestClient(t, respondJSON(http.StatusOK, `{"models": []}`, nil), WithLogger(logger))
			if _, err := client.Models.List(context.Background()); err != nil {
				t.Fatalf("Models.List: %v", err)
			}
			output := logs.String()
			for level, want := range map[slog.Level]bool{slog.LevelDebug: tc.levels[slog.LevelDebug], slog.LevelInfo: tc.levels[slog.LevelInfo]} {
				if got := strings.Contains(output, "level="+level.String()); got != want {
					t.Errorf("%v records present = %v, want %v:\n%s", level, got, want, output)
				}
			}
			if !tc.levels[slog.LevelDebug] && strings.Contains(output, "headers=") {
				t.Errorf("wire detail logged above debug:\n%s", output)
			}
			if tc.levels[slog.LevelInfo] && !strings.Contains(output, "/v1/models") {
				t.Errorf("the response summary is missing the endpoint:\n%s", output)
			}
		})
	}
}

func TestSecretsAreRedactedFromLogs(t *testing.T) {
	clearEnv(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client, _ := newTestClient(t,
		respondJSON(http.StatusOK, resultBody, map[string]string{
			"Set-Cookie": "response-secret", requestIDHeader: "req_log",
		}),
		WithLogger(logger),
		WithHeaders(http.Header{
			"X-Api-Key":     {"key-secret"},
			"Cookie":        {"cookie-secret"},
			"X-Auth-Token":  {"token-secret"},
			"X-Team-Secret": {"other-secret"},
			"X-Team":        {"kept"},
		}),
	)

	if _, err := client.SystemOne(context.Background(), "hello", noulQuestions()); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	output := logs.String()
	for _, secret := range []string{"test-key", "key-secret", "cookie-secret", "token-secret", "other-secret", "response-secret"} {
		if strings.Contains(output, secret) {
			t.Errorf("the log leaked %q:\n%s", secret, output)
		}
	}
	for _, want := range []string{"req_log", "hello", "kept", "***"} {
		if !strings.Contains(output, want) {
			t.Errorf("the log is missing %q:\n%s", want, output)
		}
	}
}

func TestLoggerIsSilentByDefault(t *testing.T) {
	clearEnv(t)
	client, _ := newTestClient(t, respondJSON(http.StatusOK, resultBody, nil))
	if _, err := client.SystemOne(context.Background(), "x", noulQuestions()); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if client.cfg.logger.Enabled(context.Background(), slog.LevelError) {
		t.Error("the default logger is not silent")
	}
}

func TestLogLevelFromEnvironment(t *testing.T) {
	clearEnv(t)
	for level, enabled := range map[string]slog.Level{"debug": slog.LevelDebug, "warn": slog.LevelWarn} {
		t.Setenv(LogLevelEnv, level)
		logger := levelLogger(level)
		if !logger.Enabled(context.Background(), enabled) {
			t.Errorf("%s logger does not log at %v", level, enabled)
		}
		if enabled > slog.LevelDebug && logger.Enabled(context.Background(), slog.LevelDebug) {
			t.Errorf("%s logger logs at debug", level)
		}
	}
	for _, level := range []string{"off", "", "nonsense"} {
		t.Setenv(LogLevelEnv, level)
		if levelLogger(level).Enabled(context.Background(), slog.LevelError) {
			t.Errorf("%q logger is not silent", level)
		}
	}
}

func TestAPIErrorCarriesTheDecodedBody(t *testing.T) {
	clearEnv(t)
	client, _ := newTestClient(t, respondJSON(http.StatusTooManyRequests, `{"message": "Too many requests"}`,
		map[string]string{requestIDHeader: "req-context"}))

	_, err := client.SystemOne(context.Background(), "hello", noulQuestions())
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err = %#v, want *APIError", err)
	}
	body, _ := json.Marshal(apiErr.Body)
	if string(body) != `{"message":"Too many requests"}` {
		t.Errorf("Body = %s", body)
	}
	if !strings.HasPrefix(apiErr.Endpoint, "POST http://127.0.0.1") || !strings.HasSuffix(apiErr.Endpoint, systemOnePath) {
		t.Errorf("Endpoint = %q", apiErr.Endpoint)
	}
	if !strings.Contains(apiErr.Error(), "429 Too many requests (request_id=req-context)") {
		t.Errorf("Error() = %q", apiErr.Error())
	}
	if strings.Contains(apiErr.Error(), "test-key") {
		t.Error("the error leaked the API key")
	}
}
