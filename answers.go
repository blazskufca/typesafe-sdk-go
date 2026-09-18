package typesafe

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// An Answer is a [*NoulAnswer], a [*ChoiceAnswer], or a [*ScoreAnswer]. The
// interface is closed: it exists to group the answer kinds the API returns.
//
// Type-switch on it, or reach for the typed views on [SystemOneResponse]:
//
//	switch answer := resp.Answers["tone"].(type) {
//	case *typesafe.ChoiceAnswer:
//		fmt.Println(answer.Choice)
//	}
type Answer interface {
	// Type reports the wire discriminator: "noul", "choice", or "score".
	Type() string
	// isAnswer keeps the interface closed to the answer kinds this SDK models.
	isAnswer()
}

// NoulAnswer is a yes/no answer.
//
// See the noul primitive (https://docs.typesafe.ai/primitives/noul) for details.
type NoulAnswer struct {
	// Noul is the probability that the answer is yes, from 0 to 1. Values near 0.5
	// mean the model is unsure.
	Noul float64 `json:"noul"`
}

// Type reports the wire discriminator.
func (a NoulAnswer) Type() string { return "noul" }

func (a NoulAnswer) isAnswer() {}

// MarshalJSON implements [json.Marshaler], tagging the answer as a noul.
func (a NoulAnswer) MarshalJSON() ([]byte, error) {
	type noul NoulAnswer
	return json.Marshal(struct {
		Type string `json:"type"`
		noul
	}{Type: "noul", noul: noul(a)})
}

// ChoiceAnswer is a selected label with the probabilities of every alternative.
//
// See the choice primitive (https://docs.typesafe.ai/primitives/choice) for details.
type ChoiceAnswer struct {
	// Choice is the label with the highest probability.
	Choice string `json:"choice"`
	// Confidence is the confidence in the selected label, from 0 to 1.
	Confidence float64 `json:"confidence"`
	// Probabilities holds the probability of each label, summing to about 1.
	Probabilities map[string]float64 `json:"probabilities"`
}

// Type reports the wire discriminator.
func (a ChoiceAnswer) Type() string { return "choice" }

func (a ChoiceAnswer) isAnswer() {}

// MarshalJSON implements [json.Marshaler], tagging the answer as a choice.
func (a ChoiceAnswer) MarshalJSON() ([]byte, error) {
	type choice ChoiceAnswer
	return json.Marshal(struct {
		Type string `json:"type"`
		choice
	}{Type: "choice", choice: choice(a)})
}

// ScoreAnswer is a probability-weighted score with the rubric it was read from.
//
// See the score primitive (https://docs.typesafe.ai/primitives/score) for details.
type ScoreAnswer struct {
	// Score is the expected score: the probability-weighted average of the levels.
	Score float64 `json:"score"`
	// Confidence is the confidence in the score, from 0 to 1.
	Confidence float64 `json:"confidence"`
	// Legend maps each score level to the criterion it was given for.
	Legend map[int]Content `json:"legend"`
	// Probabilities holds the probability of each score level, summing to about 1.
	Probabilities map[int]float64 `json:"probabilities"`
}

// Type reports the wire discriminator.
func (a ScoreAnswer) Type() string { return "score" }

func (a ScoreAnswer) isAnswer() {}

// MarshalJSON implements [json.Marshaler], tagging the answer as a score.
func (a ScoreAnswer) MarshalJSON() ([]byte, error) {
	type score ScoreAnswer
	return json.Marshal(struct {
		Type string `json:"type"`
		score
	}{Type: "score", score: score(a)})
}

// Usage reports the token counts of a request. A count the API did not report
// reads as zero.
type Usage struct {
	// InputTokens is the number of billable input tokens used to evaluate the request.
	InputTokens int `json:"input_tokens"`
	// OutputTokens is the number of output tokens used to answer the questions.
	OutputTokens int `json:"output_tokens"`
}

// fieldError names the response field that could not be decoded. The transport
// turns it into a [*ResponseValidationError] once it knows the response.
type fieldError struct {
	path string
	err  error
}

func (e *fieldError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("invalid response data at %q", e.path)
	}
	return fmt.Sprintf("invalid response data at %q: %v", e.path, e.err)
}

func (e *fieldError) Unwrap() error { return e.err }

// invalidField reports a field that is missing, or present with the wrong shape.
func invalidField(path string, err error) error { return &fieldError{path: path, err: err} }

// fieldPath joins a parent path with a child field.
func fieldPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// decodeJSON decodes strictly, and reports which field went wrong relative to path.
func decodeJSON(data []byte, target any, path string) error {
	if err := json.Unmarshal(data, target); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field != "" {
			return invalidField(fieldPath(path, typeErr.Field), err)
		}
		return invalidField(path, err)
	}
	return nil
}

// decodeAnswer builds the answer named by its "type" discriminator. A type this
// SDK version does not model yields a nil answer and no error, so that a future
// API addition does not fail the whole response.
func decodeAnswer(raw json.RawMessage, path string) (Answer, error) {
	var tagged struct {
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(raw, &tagged); err != nil || tagged.Type == nil || *tagged.Type == "" {
		return nil, invalidField(fieldPath(path, "type"), err)
	}
	switch *tagged.Type {
	case "noul":
		return decodeNoulAnswer(raw, path)
	case "choice":
		return decodeChoiceAnswer(raw, path)
	case "score":
		return decodeScoreAnswer(raw, path)
	default:
		return nil, nil
	}
}

func decodeNoulAnswer(raw json.RawMessage, path string) (Answer, error) {
	var wire struct {
		Noul *float64 `json:"noul"`
	}
	if err := decodeJSON(raw, &wire, path); err != nil {
		return nil, err
	}
	if wire.Noul == nil {
		return nil, invalidField(fieldPath(path, "noul"), nil)
	}
	return &NoulAnswer{Noul: *wire.Noul}, nil
}

func decodeChoiceAnswer(raw json.RawMessage, path string) (Answer, error) {
	var wire struct {
		Choice        *string             `json:"choice"`
		Confidence    *float64            `json:"confidence"`
		Probabilities *map[string]float64 `json:"probabilities"`
	}
	if err := decodeJSON(raw, &wire, path); err != nil {
		return nil, err
	}
	switch {
	case wire.Choice == nil:
		return nil, invalidField(fieldPath(path, "choice"), nil)
	case wire.Confidence == nil:
		return nil, invalidField(fieldPath(path, "confidence"), nil)
	case wire.Probabilities == nil:
		return nil, invalidField(fieldPath(path, "probabilities"), nil)
	}
	return &ChoiceAnswer{Choice: *wire.Choice, Confidence: *wire.Confidence, Probabilities: *wire.Probabilities}, nil
}

func decodeScoreAnswer(raw json.RawMessage, path string) (Answer, error) {
	var wire struct {
		Score         *float64                   `json:"score"`
		Confidence    *float64                   `json:"confidence"`
		Legend        map[string]json.RawMessage `json:"legend"`
		Probabilities map[string]json.RawMessage `json:"probabilities"`
	}
	if err := decodeJSON(raw, &wire, path); err != nil {
		return nil, err
	}
	switch {
	case wire.Score == nil:
		return nil, invalidField(fieldPath(path, "score"), nil)
	case wire.Confidence == nil:
		return nil, invalidField(fieldPath(path, "confidence"), nil)
	case wire.Legend == nil:
		return nil, invalidField(fieldPath(path, "legend"), nil)
	case wire.Probabilities == nil:
		return nil, invalidField(fieldPath(path, "probabilities"), nil)
	}
	answer := &ScoreAnswer{Score: *wire.Score, Confidence: *wire.Confidence}
	var err error
	if answer.Legend, err = decodeScoreMap[Content](wire.Legend, fieldPath(path, "legend")); err != nil {
		return nil, err
	}
	if answer.Probabilities, err = decodeScoreMap[float64](wire.Probabilities, fieldPath(path, "probabilities")); err != nil {
		return nil, err
	}
	return answer, nil
}

// decodeScoreMap converts a JSON object keyed by score level into a map keyed by
// the integer level, naming the offending key when one is not a score.
func decodeScoreMap[V any](raw map[string]json.RawMessage, path string) (map[int]V, error) {
	decoded := make(map[int]V, len(raw))
	for key, value := range raw {
		level, err := strconv.Atoi(key)
		if err != nil {
			return nil, invalidField(fieldPath(path, key), err)
		}
		var item V
		if err := decodeJSON(value, &item, fieldPath(path, key)); err != nil {
			return nil, err
		}
		decoded[level] = item
	}
	return decoded, nil
}
