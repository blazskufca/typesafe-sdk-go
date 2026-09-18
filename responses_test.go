package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestResponseValidationFieldPaths(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct {
		name string
		body string
		path string
	}{
		{"missing model", `{"usage": {}, "answers": {}}`, "model"},
		{"missing usage", `{"model": "test", "answers": {}}`, "usage"},
		{"missing noul", `{"model":"test","usage":{},"answers":{"n":{"type":"noul"}}}`, "answers.n.noul"},
		{"noul is a string", `{"model":"test","usage":{},"answers":{"n":{"type":"noul","noul":"0.5"}}}`, "answers.n.noul"},
		{
			name: "missing confidence",
			body: `{"model":"test","usage":{},"answers":{"c":{"type":"choice","choice":"a","probabilities":{}}}}`,
			path: "answers.c.confidence",
		},
		{
			name: "missing choice",
			body: `{"model":"test","usage":{},"answers":{"c":{"type":"choice","confidence":0.5,"probabilities":{}}}}`,
			path: "answers.c.choice",
		},
		{
			name: "legend is a list",
			body: `{"model":"test","usage":{},"answers":{"s":{"type":"score","score":1,"confidence":1,"legend":[],"probabilities":{}}}}`,
			path: "answers.s.legend",
		},
		{
			name: "legend key is not a score",
			body: `{"model":"test","usage":{},"answers":{"s":{"type":"score","score":1,"confidence":1,"legend":{"x":"bad"},"probabilities":{}}}}`,
			path: "answers.s.legend.x",
		},
		{"answer is not an object", `{"model":"test","usage":{},"answers":{"c":"not-an-object"}}`, "answers.c.type"},
		{"answer without a type", `{"model":"test","usage":{},"answers":{"c":{"noul":0.5}}}`, "answers.c.type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newTestClient(t, respondJSON(http.StatusOK, tc.body, map[string]string{requestIDHeader: "req-123"}))
			_, err := client.SystemOne(context.Background(), "x", noulQuestions())

			var invalid *ResponseValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want *ResponseValidationError", err)
			}
			if invalid.FieldPath != tc.path {
				t.Errorf("FieldPath = %q, want %q", invalid.FieldPath, tc.path)
			}
			if !errors.Is(err, ErrInvalidResponse) {
				t.Error("does not match ErrInvalidResponse")
			}
			if invalid.Status != http.StatusOK || invalid.RequestID() != "req-123" {
				t.Errorf("metadata = %d %q", invalid.Status, invalid.RequestID())
			}
			want := "invalid response data at \"" + tc.path + "\" (request_id=req-123)"
			if !strings.HasSuffix(invalid.Error(), want) {
				t.Errorf("Error() = %q, want it to end with %q", invalid.Error(), want)
			}
		})
	}
}

func TestModelsValidationFieldPaths(t *testing.T) {
	clearEnv(t)
	card := `{"name": "test", "description": "Test model", "release_date": "2026-09-14"}`
	for _, tc := range []struct {
		name string
		body string
		path string
	}{
		{"missing name", `{"models": [` + card + `, {"description": "d", "release_date": "r"}]}`, "models[1].name"},
		{"missing description", `{"models": [` + card + `, {"name": "n", "release_date": "r"}]}`, "models[1].description"},
		{"missing release date", `{"models": [` + card + `, {"name": "n", "description": "d"}]}`, "models[1].release_date"},
		{"missing models", `{}`, "models"},
		{"null body", `null`, "models"},
		{"models is a string", `{"models": "bad"}`, "models"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newTestClient(t, respondJSON(http.StatusOK, tc.body, nil))
			_, err := client.Models.List(context.Background())

			var invalid *ResponseValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want *ResponseValidationError", err)
			}
			if invalid.FieldPath != tc.path {
				t.Errorf("FieldPath = %q, want %q", invalid.FieldPath, tc.path)
			}
		})
	}
}

func TestUnknownAnswerTypeIsSkipped(t *testing.T) {
	clearEnv(t)
	body := `{
		"model": "test",
		"usage": {"input_tokens": 1, "output_tokens": 1},
		"answers": {
			"spam": {"type": "noul", "noul": 0.9},
			"mystery": {"type": "aurora", "value": 3}
		}
	}`
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client, _ := newTestClient(t, respondJSON(http.StatusOK, body, nil), WithLogger(logger))

	resp, err := client.SystemOne(context.Background(), "x", noulQuestions())
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if len(resp.Answers) != 1 || resp.Nouls()["spam"].Noul != 0.9 {
		t.Errorf("Answers = %v", resp.Answers)
	}
	if !strings.Contains(logs.String(), "aurora") {
		t.Errorf("the skipped answer was not logged: %s", logs.String())
	}
	// The unmodeled answer stays reachable in the raw body.
	if !strings.Contains(string(resp.RawBody), "aurora") {
		t.Error("RawBody lost the unmodeled answer")
	}
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	clearEnv(t)
	body := `{
		"model": "test",
		"usage": {"input_tokens": 1, "output_tokens": 1, "reasoning_tokens": 9},
		"answers": {"spam": {"type": "noul", "noul": 0.9, "explanation": "spammy"}}
	}`
	client, _ := newTestClient(t, respondJSON(http.StatusOK, body, nil))
	resp, err := client.SystemOne(context.Background(), "x", noulQuestions())
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if resp.Nouls()["spam"].Noul != 0.9 {
		t.Errorf("answers = %v", resp.Answers)
	}
	if resp.Usage != (Usage{InputTokens: 1, OutputTokens: 1}) {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestResponseRoundTripsToJSON(t *testing.T) {
	var resp SystemOneResponse
	if err := json.Unmarshal([]byte(resultBody), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := json.Marshal(&resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got, want any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(resultBody), &want); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the payload\ngot  %v\nwant %v", got, want)
	}
	// HTTP metadata is runtime state, never part of the serialized payload.
	if strings.Contains(string(encoded), "RequestID") || strings.Contains(string(encoded), "RawBody") {
		t.Errorf("metadata leaked into the payload: %s", encoded)
	}
}

func TestNestedJSONIsPreserved(t *testing.T) {
	clearEnv(t)
	body := `{
		"model": "test",
		"usage": {"input_tokens": 1, "output_tokens": 1},
		"answers": {"risk": {"type": "score", "score": 0, "confidence": 1,
			"legend": {"0": {"examples": ["a", {"note": null}]}}, "probabilities": {"0": 1}}}
	}`
	client, _ := newTestClient(t, respondJSON(http.StatusOK, body, nil))
	resp, err := client.SystemOne(context.Background(), "x", noulQuestions())
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	legend, ok := resp.Scores()["risk"].Legend[0].(map[string]any)
	if !ok {
		t.Fatalf("legend[0] = %#v", resp.Scores()["risk"].Legend[0])
	}
	want := []any{"a", map[string]any{"note": nil}}
	if !reflect.DeepEqual(legend["examples"], want) {
		t.Errorf("examples = %#v, want %#v", legend["examples"], want)
	}
}

func TestAPIErrorsSurviveCustomResponseTypes(t *testing.T) {
	clearEnv(t)
	type known struct {
		Model string `json:"model"`
	}
	client, _ := newTestClient(t, respondJSON(http.StatusBadRequest, `{"detail": "Invalid request"}`,
		map[string]string{requestIDHeader: "req-error"}))

	_, err := SystemOneInto[known](context.Background(), client, "x", noulQuestions())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want a 400 *APIError", err)
	}
	if apiErr.RequestID() != "req-error" || apiErr.Message != "Invalid request" {
		t.Errorf("error = %+v", apiErr)
	}
}

func TestCustomResponseValidation(t *testing.T) {
	clearEnv(t)
	type answers struct {
		Spam NoulAnswer `json:"spam"`
	}
	type known struct {
		Model   string  `json:"model"`
		Answers answers `json:"answers"`
	}
	client, _ := newTestClient(t, respondJSON(http.StatusOK,
		`{"model": "test", "answers": {"spam": {"type": "noul", "noul": "not a number"}}}`, nil))

	_, err := SystemOneInto[known](context.Background(), client, "x", noulQuestions())
	var invalid *ResponseValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want *ResponseValidationError", err)
	}
	if invalid.FieldPath != "answers.spam.noul" {
		t.Errorf("FieldPath = %q", invalid.FieldPath)
	}
}

func TestEmptyAnswersDecode(t *testing.T) {
	clearEnv(t)
	client, _ := newTestClient(t, respondJSON(http.StatusOK, `{"model":"test","usage":{}}`, nil))
	resp, err := client.SystemOne(context.Background(), "x", noulQuestions())
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if len(resp.Answers) != 0 || len(resp.Nouls()) != 0 {
		t.Errorf("Answers = %v", resp.Answers)
	}
}
