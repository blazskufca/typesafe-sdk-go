package typesafe_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/blazskufca/typesafe-sdk-go"
)

func ExampleNew() {
	// TYPESAFE_API_KEY supplies the key when WithAPIKey does not.
	client, err := typesafe.New(typesafe.WithModel("jev-latest"))
	if err != nil {
		log.Fatal(err)
	}

	resp, err := client.SystemOne(context.Background(),
		map[string]any{"document": "I was charged twice. Please fix this ASAP."},
		typesafe.Questions{
			"category": typesafe.Choice{
				Instructions: "What is this ticket about?",
				Criteria:     map[string]typesafe.Content{"billing": nil, "technical": nil, "other": nil},
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resp.Choices()["category"].Choice)
}

func ExampleClient_SystemOne() {
	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}

	resp, err := client.SystemOne(context.Background(), "I was charged twice. Please help.", typesafe.Questions{
		"billing": typesafe.Noul{Instructions: "Is this about billing?"},
		"tone": typesafe.Choice{
			Instructions: "What is the tone?",
			Criteria:     map[string]typesafe.Content{"calm": nil, "angry": nil},
		},
		"urgency": typesafe.Score{
			Instructions: "How urgent is this?",
			Criteria:     []typesafe.Content{"can wait", "this week", "today"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("billing: %.2f\n", resp.Nouls()["billing"].Noul)
	fmt.Printf("tone: %s\n", resp.Choices()["tone"].Choice)
	fmt.Printf("urgency: %.1f of %d\n", resp.Scores()["urgency"].Score, len(resp.Scores()["urgency"].Legend)-1)
}

// Answers can also be reached by kind, without a map lookup per question.
func ExampleSystemOneResponse_As() {
	var resp *typesafe.SystemOneResponse // from client.SystemOne

	if tone, ok := resp.As[*typesafe.ChoiceAnswer]("tone"); ok {
		fmt.Println(tone.Choice, tone.Probabilities)
	}
	for name, score := range typesafe.AnswersOf[*typesafe.ScoreAnswer](resp) {
		fmt.Println(name, score.Score, score.Legend)
	}
}

// A struct describing the response body turns the answers into typed fields.
func ExampleSystemOneInto() {
	type answers struct {
		Billing typesafe.NoulAnswer   `json:"billing"`
		Tone    typesafe.ChoiceAnswer `json:"tone"`
	}
	type triage struct {
		Model   string  `json:"model"`
		Answers answers `json:"answers"`
		typesafe.ResponseMetadata
	}

	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}
	result, err := typesafe.SystemOneInto[triage](context.Background(), client, "I was charged twice.", typesafe.Questions{
		"billing": typesafe.Noul{Instructions: "Is this about billing?"},
		"tone":    typesafe.Choice{Criteria: map[string]typesafe.Content{"calm": nil, "angry": nil}},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Answers.Tone.Choice, result.RequestID)
}

func ExampleClient_Models() {
	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}
	resp, err := client.Models.List(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, model := range resp.Models {
		fmt.Printf("%s (%s): %s\n", model.Name, model.ReleaseDate, model.Description)
	}
}

// Errors carry the status, the server's explanation, and the request ID.
func ExampleAPIError() {
	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}
	_, err = client.SystemOne(context.Background(), "state", typesafe.Questions{
		"billing": typesafe.Noul{},
	})

	var apiErr *typesafe.APIError
	switch {
	case errors.As(err, &apiErr):
		if wait, ok := apiErr.RetryAfter(); ok && errors.Is(err, typesafe.ErrRateLimit) {
			fmt.Println("rate limited, retry after", wait)
			break
		}
		fmt.Println(apiErr.Status, apiErr.Message, apiErr.RequestID())
	case errors.Is(err, typesafe.ErrTimeout):
		fmt.Println("timed out")
	case err != nil:
		fmt.Println(err)
	}
}

func ExampleWithRetryPolicy() {
	policy := typesafe.DefaultRetryPolicy()
	policy.MaxRetries = 5
	policy.BackoffMax = 10 * time.Second

	client, err := typesafe.New(
		typesafe.WithRetryPolicy(policy),
		typesafe.WithTimeout(30*time.Second),
		typesafe.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))),
	)
	if err != nil {
		log.Fatal(err)
	}

	// A call may override any of it, for that call only.
	_, err = client.Models.List(context.Background(), typesafe.WithRetryPolicy(typesafe.RetryPolicy{}))
	if err != nil {
		log.Fatal(err)
	}
}
