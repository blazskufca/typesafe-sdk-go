package typesafe

import (
	"context"
	"maps"
	"net/http"
)

// SystemOne answers named questions about text or structured state.
//
// state is anything that encodes to JSON: a string, a struct, a map, or a slice.
// questions is a nonempty set of [Noul], [Choice], [Score], or [RawQuestion]
// values, keyed by the names their answers come back under.
//
//	resp, err := client.SystemOne(ctx, "I was charged twice. Please help.", typesafe.Questions{
//		"billing": typesafe.Noul{Instructions: "Is this about billing?"},
//		"tone": typesafe.Choice{
//			Instructions: "What is the tone?",
//			Criteria:     map[string]typesafe.Content{"calm": nil, "angry": nil},
//		},
//	})
//	if err != nil {
//		return err
//	}
//	fmt.Println(resp.Nouls()["billing"].Noul, resp.Choices()["tone"].Choice)
//
// See System One (https://docs.typesafe.ai/concepts/system-one) for details.
func (c *Client) SystemOne(ctx context.Context, state Content, questions Questions, opts ...Option) (*SystemOneResponse, error) {
	cfg, body, err := c.prepareSystemOne(state, questions, opts)
	if err != nil {
		return nil, err
	}
	return send[SystemOneResponse](ctx, cfg, http.MethodPost, systemOnePath, body)
}

// SystemOneInto answers named questions and decodes the response into T, a struct
// describing the JSON body. Declare the answers you asked for and get them back
// typed, with no map lookups or type assertions:
//
//	type Triage struct {
//		Answers struct {
//			Billing typesafe.NoulAnswer   `json:"billing"`
//			Tone    typesafe.ChoiceAnswer `json:"tone"`
//		} `json:"answers"`
//		typesafe.ResponseMetadata `json:"-"`
//	}
//
//	triage, err := typesafe.SystemOneInto[Triage](ctx, client, state, questions)
//	if err != nil {
//		return err
//	}
//	fmt.Println(triage.Answers.Tone.Choice, triage.RequestID)
//
// Embedding [ResponseMetadata] is optional; when T embeds it, the HTTP details of
// the response are filled in. Unknown fields are ignored, so a T that models only
// part of the body is fine.
func SystemOneInto[T any](ctx context.Context, c *Client, state Content, questions Questions, opts ...Option) (*T, error) {
	cfg, body, err := c.prepareSystemOne(state, questions, opts)
	if err != nil {
		return nil, err
	}
	return send[T](ctx, cfg, http.MethodPost, systemOnePath, body)
}

// Into answers named questions and decodes the response into T. It is the method
// form of [SystemOneInto]:
//
//	triage, err := client.Into[Triage](ctx, state, questions)
func (c *Client) Into[T any](ctx context.Context, state Content, questions Questions, opts ...Option) (*T, error) {
	return SystemOneInto[T](ctx, c, state, questions, opts...)
}

// prepareSystemOne resolves the call's configuration and encodes its request body,
// rejecting invalid questions before anything reaches the network.
func (c *Client) prepareSystemOne(state Content, questions Questions, opts []Option) (config, []byte, error) {
	cfg, err := c.callConfig(opts)
	if err != nil {
		return config{}, nil, err
	}
	if err := validateQuestions(questions); err != nil {
		return config{}, nil, err
	}
	body := map[string]any{
		"state":     state,
		"model":     cfg.model,
		"questions": questions,
	}
	// Last write wins, so an extra field may replace a modeled one.
	maps.Copy(body, cfg.extraBody)
	encoded, err := encodeBody(body)
	if err != nil {
		return config{}, nil, err
	}
	return cfg, encoded, nil
}
