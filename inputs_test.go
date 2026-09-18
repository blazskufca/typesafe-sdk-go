package typesafe

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestStateForms(t *testing.T) {
	clearEnv(t)
	type ticket struct {
		Subject string  `json:"subject"`
		Body    *string `json:"body"`
	}
	for _, tc := range []struct {
		name  string
		state Content
		want  any
	}{
		{"text", "I was charged twice.", "I was charged twice."},
		{"map", map[string]any{"document": "Hello 🌍"}, map[string]any{"document": "Hello 🌍"}},
		{"slice", []any{map[string]any{"message": "Classify"}, nil}, []any{map[string]any{"message": "Classify"}, nil}},
		{"struct", ticket{Subject: "Duplicate charge"}, map[string]any{"subject": "Duplicate charge", "body": nil}},
		{"nested nulls", map[string]any{"missing": nil, "items": []any{nil, map[string]any{"nested": nil}}},
			map[string]any{"missing": nil, "items": []any{nil, map[string]any{"nested": nil}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, rec := newTestClient(t, respondJSON(http.StatusOK, `{"model":"m","usage":{},"answers":{}}`, nil))
			if _, err := client.SystemOne(context.Background(), tc.state, noulQuestions()); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}
			if got := rec.Body(t, 0)["state"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("state = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRichAndNestedQuestionContent(t *testing.T) {
	clearEnv(t)
	instructions := []Content{"Read the message", map[string]any{"context": nil}}
	description := []Content{"Example", nil}
	client, rec := newTestClient(t, respondJSON(http.StatusOK, `{"model":"m","usage":{},"answers":{}}`, nil))

	_, err := client.SystemOne(context.Background(), "x", Questions{
		"yes":    Noul{Instructions: instructions, Criteria: &NoulCriteria{True: description}},
		"label":  Choice{Instructions: instructions, Criteria: map[string]Content{"a": description, "b": nil}},
		"rating": Score{Instructions: instructions, Criteria: []Content{description}},
	})
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	want := map[string]any{
		"yes": map[string]any{"type": "noul", "instructions": []any{"Read the message", map[string]any{"context": nil}},
			"criteria": map[string]any{"true": []any{"Example", nil}}},
		"label": map[string]any{"type": "choice", "instructions": []any{"Read the message", map[string]any{"context": nil}},
			"criteria": map[string]any{"a": []any{"Example", nil}, "b": nil}},
		"rating": map[string]any{"type": "score", "instructions": []any{"Read the message", map[string]any{"context": nil}},
			"criteria": []any{[]any{"Example", nil}}},
	}
	if got := rec.Body(t, 0)["questions"]; !reflect.DeepEqual(got, want) {
		t.Errorf("questions\ngot  %#v\nwant %#v", got, want)
	}
}

func TestOptionalNoulCriteria(t *testing.T) {
	for _, tc := range []struct {
		name     string
		criteria *NoulCriteria
		want     string
	}{
		{"absent", nil, `{"type":"noul","instructions":"Spam?"}`},
		{"empty", &NoulCriteria{}, `{"type":"noul","instructions":"Spam?","criteria":{}}`},
		{"true only", &NoulCriteria{True: "Yes"}, `{"type":"noul","instructions":"Spam?","criteria":{"true":"Yes"}}`},
		{"false only", &NoulCriteria{False: "No"}, `{"type":"noul","instructions":"Spam?","criteria":{"false":"No"}}`},
		{"both", &NoulCriteria{True: "Yes", False: "No"}, `{"type":"noul","instructions":"Spam?","criteria":{"true":"Yes","false":"No"}}`},
		{
			name:     "structured",
			criteria: &NoulCriteria{True: map[string]any{"summary": "Unsolicited"}},
			want:     `{"type":"noul","instructions":"Spam?","criteria":{"true":{"summary":"Unsolicited"}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(Noul{Instructions: "Spam?", Criteria: tc.criteria})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(encoded) != tc.want {
				t.Errorf("got  %s\nwant %s", encoded, tc.want)
			}
		})
	}
}

func TestSuppliedHTTPClientIsUsedAndLeftAlone(t *testing.T) {
	clearEnv(t)
	httpClient := &http.Client{}
	client, rec := newTestClient(t, respondJSON(http.StatusOK, `{"models": []}`, nil), WithHTTPClient(httpClient))
	if client.cfg.httpClient != httpClient {
		t.Fatal("the supplied HTTP client was replaced")
	}
	if _, err := client.Models.List(context.Background()); err != nil {
		t.Fatalf("Models.List: %v", err)
	}
	if rec.Count() != 1 {
		t.Errorf("sent %d requests, want 1", rec.Count())
	}
	// The SDK carries its settings on the request, never on the caller's client.
	if httpClient.Timeout != 0 || httpClient.Transport != nil || httpClient.CheckRedirect != nil {
		t.Errorf("the SDK mutated the supplied HTTP client: %+v", httpClient)
	}
}

func TestQuestionValuesAreNotMutated(t *testing.T) {
	clearEnv(t)
	criteria := []Content{"bad", "good"}
	raw := RawQuestion{"type": "score", "criteria": criteria}
	questions := Questions{"typed": Score{Criteria: criteria}, "raw": raw}

	client, _ := newTestClient(t, respondJSON(http.StatusOK, `{"model":"m","usage":{},"answers":{}}`, nil))
	if _, err := client.SystemOne(context.Background(), "x", questions); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	if !reflect.DeepEqual(criteria, []Content{"bad", "good"}) {
		t.Errorf("criteria = %#v", criteria)
	}
	if !reflect.DeepEqual(raw, RawQuestion{"type": "score", "criteria": criteria}) {
		t.Errorf("raw question = %#v", raw)
	}
}
