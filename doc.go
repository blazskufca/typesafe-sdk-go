// Package typesafe is a community Go SDK for the TypeSafe AI API
// (https://typesafe.ai).
//
// It is unofficial: not published, endorsed, or supported by TypeSafe AI. It is
// a port of the official Python SDK
// (https://github.com/typesafe-ai/typesafe-sdk-python).
//
// Set TYPESAFE_API_KEY in the environment — or in a .env file, see [WithDotenv] —
// then create a client and ask questions about a piece of state:
//
//	client, err := typesafe.New()
//	if err != nil {
//		return err
//	}
//	resp, err := client.SystemOne(ctx,
//		map[string]any{"document": "I was charged twice. Please fix this ASAP."},
//		typesafe.Questions{
//			"category": typesafe.Choice{
//				Instructions: "What is this ticket about?",
//				Criteria:     map[string]any{"billing": nil, "technical": nil, "other": nil},
//			},
//		},
//	)
//	if err != nil {
//		return err
//	}
//	fmt.Println(resp.Choices()["category"].Choice)
//
// Every call takes a [context.Context] and blocks; run calls in goroutines for
// concurrency. A [Client] is safe for concurrent use.
//
// Client-wide settings and per-call overrides share one [Option] vocabulary, so
// [WithModel], [WithTimeout], [WithHeader], [WithRetryPolicy] and friends work
// both in [New] and in any request method.
//
// Errors are inspected with [errors.Is] against the sentinels ([ErrRateLimit],
// [ErrNotFound], [ErrTimeout], ...) and with [errors.As] against [*APIError],
// [*ConnectionError], [*TimeoutError] and [*ResponseValidationError].
package typesafe
