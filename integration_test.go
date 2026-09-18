package typesafe_test

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blazskufca/typesafe-sdk-go"
)

// liveClient returns a client for the real API, skipping the test when no key is
// configured. Run these with: go test -run Live ./...
func liveClient(t *testing.T) *typesafe.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping live API test in short mode")
	}
	if strings.TrimSpace(os.Getenv(typesafe.APIKeyEnv)) == "" {
		t.Skipf("%s is not set", typesafe.APIKeyEnv)
	}
	client, err := typesafe.New(typesafe.WithTimeout(120 * time.Second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestLiveModels(t *testing.T) {
	resp, err := liveClient(t).Models.List(context.Background())
	if err != nil {
		t.Fatalf("Models.List: %v", err)
	}
	if len(resp.Models) == 0 {
		t.Fatal("no models available")
	}
	for _, model := range resp.Models {
		if model.Name == "" || model.Description == "" || model.ReleaseDate == "" {
			t.Errorf("incomplete model card: %+v", model)
		}
	}
}

func TestLiveSystemOne(t *testing.T) {
	client := liveClient(t)
	state := map[string]any{
		"subject": "Charged twice this month",
		"body":    "I see two charges of $49. I only have one account. Please fix this ASAP.",
	}
	resp, err := client.SystemOne(context.Background(), state, typesafe.Questions{
		"billing": typesafe.Noul{
			Instructions: "Is this ticket about billing?",
			Criteria: &typesafe.NoulCriteria{
				True: map[string]any{"meaning": "Payments or invoices", "examples": []any{"charged twice"}},
			},
		},
		"tone": typesafe.Choice{
			Instructions: "What is the customer's tone?",
			Criteria:     map[string]typesafe.Content{"calm": nil, "frustrated": nil, "angry": nil},
		},
		"urgency": typesafe.Score{
			Instructions: "How urgent is this ticket?",
			Criteria:     []typesafe.Content{"can wait", "this week", "today"},
		},
	})
	if err != nil {
		t.Fatalf("SystemOne: %v", err)
	}

	if resp.Model == "" || resp.RequestID == "" {
		t.Errorf("model = %q, request ID = %q", resp.Model, resp.RequestID)
	}
	if resp.Usage.InputTokens < 0 || resp.Usage.OutputTokens < 0 {
		t.Errorf("usage = %+v", resp.Usage)
	}

	billing := resp.Nouls()["billing"]
	if billing == nil || billing.Noul < 0 || billing.Noul > 1 {
		t.Errorf("billing = %+v", billing)
	}
	tone := resp.Choices()["tone"]
	if tone == nil {
		t.Fatal("no tone answer")
	}
	if _, known := map[string]bool{"calm": true, "frustrated": true, "angry": true}[tone.Choice]; !known {
		t.Errorf("tone = %q", tone.Choice)
	}
	if total := sum(tone.Probabilities); math.Abs(total-1) > 0.1 {
		t.Errorf("tone probabilities sum to %v", total)
	}
	urgency := resp.Scores()["urgency"]
	if urgency == nil {
		t.Fatal("no urgency answer")
	}
	if urgency.Score < 0 || urgency.Score > 2 {
		t.Errorf("urgency = %v", urgency.Score)
	}
	want := map[int]typesafe.Content{0: "can wait", 1: "this week", 2: "today"}
	for level, description := range want {
		if urgency.Legend[level] != description {
			t.Errorf("legend[%d] = %v, want %v", level, urgency.Legend[level], description)
		}
	}
	if total := sum(urgency.Probabilities); math.Abs(total-1) > 0.1 {
		t.Errorf("urgency probabilities sum to %v", total)
	}
}

func TestLiveSystemOneInto(t *testing.T) {
	client := liveClient(t)
	type answers struct {
		Billing typesafe.NoulAnswer   `json:"billing"`
		Tone    typesafe.ChoiceAnswer `json:"tone"`
		Urgency typesafe.ScoreAnswer  `json:"urgency"`
	}
	type triage struct {
		Model   string  `json:"model"`
		Answers answers `json:"answers"`
		typesafe.ResponseMetadata
	}

	state := map[string]any{"subject": "Charged twice this month", "body": "I see two charges of $49. Please fix this ASAP."}
	result, err := typesafe.SystemOneInto[triage](context.Background(), client, state, typesafe.Questions{
		"billing": typesafe.Noul{Instructions: "Is this ticket about billing?"},
		"tone": typesafe.Choice{
			Instructions: "What is the customer's tone?",
			Criteria:     map[string]typesafe.Content{"calm": nil, "frustrated": nil, "angry": nil},
		},
		"urgency": typesafe.Score{
			Instructions: "How urgent is this ticket?",
			Criteria:     []typesafe.Content{"can wait", "this week", "today"},
		},
	})
	if err != nil {
		t.Fatalf("SystemOneInto: %v", err)
	}
	if result.Answers.Billing.Noul < 0 || result.Answers.Billing.Noul > 1 {
		t.Errorf("billing = %+v", result.Answers.Billing)
	}
	if result.Answers.Urgency.Score < 0 || result.Answers.Urgency.Score > 2 {
		t.Errorf("urgency = %+v", result.Answers.Urgency)
	}
	if result.RequestID == "" {
		t.Error("no request ID")
	}
}

// sum totals the values of a probability map.
func sum[K comparable](values map[K]float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return total
}
