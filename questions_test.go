package typesafe

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"context"
)

func TestQuestionMarshalling(t *testing.T) {
	rich := map[string]any{"summary": "duplicated", "examples": []any{"charged twice"}}
	for _, tc := range []struct {
		name     string
		question Question
		want     string
	}{
		{"bare noul", Noul{}, `{"type":"noul"}`},
		{"noul", Noul{Instructions: "Spam?"}, `{"type":"noul","instructions":"Spam?"}`},
		{
			name:     "noul criteria",
			question: Noul{Criteria: &NoulCriteria{True: "advertising"}},
			want:     `{"type":"noul","criteria":{"true":"advertising"}}`,
		},
		{
			name:     "choice",
			question: Choice{Criteria: map[string]Content{"a": nil}},
			want:     `{"type":"choice","criteria":{"a":null}}`,
		},
		{
			name:     "score",
			question: Score{Instructions: "Risk?", Criteria: []Content{rich}},
			want:     `{"type":"score","instructions":"Risk?","criteria":[{"examples":["charged twice"],"summary":"duplicated"}]}`,
		},
		{
			name:     "raw passthrough",
			question: RawQuestion{"type": "noul", "instructions": "Spam?", "weight": 3},
			want:     `{"instructions":"Spam?","type":"noul","weight":3}`,
		},
		{
			name:     "empty instructions are kept",
			question: Noul{Instructions: ""},
			want:     `{"type":"noul","instructions":""}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.question)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(encoded) != tc.want {
				t.Errorf("got  %s\nwant %s", encoded, tc.want)
			}
		})
	}
}

func TestQuestionValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		questions Questions
		want      string
	}{
		{"empty", Questions{}, "at least one question"},
		{"nil", nil, "at least one question"},
		{"nil question", Questions{"q": nil}, `question "q" must not be nil`},
		{"empty score", Questions{"rating": Score{Criteria: []Content{}}}, `score question "rating" has no criteria`},
		{"nil choice criteria", Questions{"tone": Choice{}}, `choice question "tone" requires criteria`},
		{"raw without type", Questions{"invalid": RawQuestion{}}, `question "invalid" must have a nonempty string "type"`},
		{"raw with blank type", Questions{"invalid": RawQuestion{"type": ""}}, `question "invalid" must have a nonempty string "type"`},
		{"raw with wrong type", Questions{"invalid": RawQuestion{"type": 1}}, `question "invalid" must have a nonempty string "type"`},
		{"raw choice without criteria", Questions{"c": RawQuestion{"type": "choice"}}, `question "c" requires "criteria"`},
		{"raw score without criteria", Questions{"s": RawQuestion{"type": "score"}}, `question "s" requires "criteria"`},
		{"raw score with empty criteria", Questions{"s": RawQuestion{"type": "score", "criteria": []Content{}}}, `has no criteria`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateQuestions(tc.questions)
			if !errors.Is(err, ErrInvalidQuestions) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestValidQuestionsPassValidation(t *testing.T) {
	questions := Questions{
		"noul":   Noul{},
		"choice": Choice{Criteria: map[string]Content{"a": nil}},
		"score":  Score{Criteria: []Content{"bad"}},
		"raw":    RawQuestion{"type": "future", "nested": map[string]any{"k": nil}},
	}
	if err := validateQuestions(questions); err != nil {
		t.Errorf("validateQuestions: %v", err)
	}
}

func TestInvalidQuestionsNeverReachTheNetwork(t *testing.T) {
	clearEnv(t)
	client, rec := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("invalid questions reached the network")
	})
	_, err := client.SystemOne(context.Background(), "x", Questions{"rating": Score{}})
	if !errors.Is(err, ErrInvalidQuestions) {
		t.Errorf("err = %v, want ErrInvalidQuestions", err)
	}
	if rec.Count() != 0 {
		t.Errorf("sent %d requests", rec.Count())
	}
}

func TestRawQuestionSchemaIsLeftToTheAPI(t *testing.T) {
	clearEnv(t)
	// The SDK forwards unmodeled shapes untouched and lets the API judge them.
	client, rec := newTestClient(t, respondJSON(http.StatusUnprocessableEntity, `{"detail": "Invalid question"}`, nil))
	question := RawQuestion{"type": "choice", "criteria": []any{"invalid", "shape"}}

	_, err := client.SystemOne(context.Background(), "x", Questions{"q": question})
	if !errors.Is(err, ErrUnprocessableEntity) || !strings.Contains(err.Error(), "Invalid question") {
		t.Fatalf("err = %v", err)
	}
	sent := rec.Body(t, 0)["questions"].(map[string]any)["q"]
	want := map[string]any{"type": "choice", "criteria": []any{"invalid", "shape"}}
	if encoded, _ := json.Marshal(sent); string(encoded) != mustJSON(t, want) {
		t.Errorf("question on the wire = %s", encoded)
	}
}

// mustJSON encodes a value or fails the test.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(encoded)
}
