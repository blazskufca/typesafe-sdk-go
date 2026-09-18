package typesafe

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ResponseMetadata carries the HTTP details of the response a value was decoded
// from. Embed it in a custom response type to have [Client.SystemOneInto] fill it
// in:
//
//	type Triage struct {
//		Answers TriageAnswers `json:"answers"`
//		typesafe.ResponseMetadata `json:"-"`
//	}
type ResponseMetadata struct {
	// RequestID is the x-typesafe-request-id response header, or "" if absent.
	RequestID string
	// Status is the HTTP status code.
	Status int
	// Header holds the response headers.
	Header http.Header
	// RawBody is the undecoded response body, including any fields this SDK version
	// does not model.
	RawBody []byte
}

// setMetadata lets the transport fill in metadata through an interface, so that
// any type embedding ResponseMetadata receives it.
func (m *ResponseMetadata) setMetadata(meta ResponseMetadata) { *m = meta }

// metadataSetter is satisfied by every type that embeds [ResponseMetadata].
type metadataSetter interface {
	setMetadata(ResponseMetadata)
}

// SystemOneResponse holds the answers to one System One request.
//
// See System One (https://docs.typesafe.ai/concepts/system-one) for details.
type SystemOneResponse struct {
	// Model is the model that answered the request, which may differ from the alias
	// that was requested.
	Model string `json:"model"`
	// Usage reports the token counts of the request.
	Usage Usage `json:"usage"`
	// Answers holds every answer, keyed by question name.
	Answers map[string]Answer `json:"answers"`

	ResponseMetadata `json:"-"`

	// unrecognized names the answers skipped for having a type this SDK version
	// does not model, so the transport can report them.
	unrecognized map[string]string
}

// Nouls returns the yes/no answers, keyed by question name.
func (r *SystemOneResponse) Nouls() map[string]*NoulAnswer { return AnswersOf[*NoulAnswer](r) }

// Choices returns the choice answers, keyed by question name.
func (r *SystemOneResponse) Choices() map[string]*ChoiceAnswer { return AnswersOf[*ChoiceAnswer](r) }

// Scores returns the score answers, keyed by question name.
func (r *SystemOneResponse) Scores() map[string]*ScoreAnswer { return AnswersOf[*ScoreAnswer](r) }

// AnswersOf returns every answer of one kind, keyed by question name.
//
//	scores := typesafe.AnswersOf[*typesafe.ScoreAnswer](resp)
func AnswersOf[T Answer](r *SystemOneResponse) map[string]T {
	selected := make(map[string]T)
	for name, answer := range r.Answers {
		if typed, ok := answer.(T); ok {
			selected[name] = typed
		}
	}
	return selected
}

// As returns the answer to the named question when it has the requested type.
// It reports false when the question went unanswered or was answered with a
// different kind:
//
//	tone, ok := resp.As[*typesafe.ChoiceAnswer]("tone")
func (r *SystemOneResponse) As[T Answer](name string) (T, bool) {
	answer, ok := r.Answers[name].(T)
	return answer, ok
}

// UnmarshalJSON implements [json.Unmarshaler], decoding each answer into the type
// its "type" field names. An answer of a type this SDK version does not model is
// skipped rather than failing the whole response; it stays available in
// [ResponseMetadata.RawBody].
func (r *SystemOneResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		Model   *string                    `json:"model"`
		Usage   *Usage                     `json:"usage"`
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := decodeJSON(data, &wire, ""); err != nil {
		return err
	}
	if wire.Model == nil {
		return invalidField("model", nil)
	}
	if wire.Usage == nil {
		return invalidField("usage", nil)
	}
	r.Model, r.Usage = *wire.Model, *wire.Usage
	r.Answers = make(map[string]Answer, len(wire.Answers))
	r.unrecognized = nil
	for name, raw := range wire.Answers {
		answer, err := decodeAnswer(raw, fieldPath("answers", name))
		if err != nil {
			return err
		}
		if answer == nil {
			if r.unrecognized == nil {
				r.unrecognized = make(map[string]string)
			}
			var tagged struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &tagged)
			r.unrecognized[name] = tagged.Type
			continue
		}
		r.Answers[name] = answer
	}
	return nil
}

// ModelMetadata describes one model available to the account.
type ModelMetadata struct {
	// Name is the model name or alias accepted by [WithModel].
	Name string `json:"name"`
	// Description is a human-readable description of the model.
	Description string `json:"description"`
	// ReleaseDate is the model's release date, formatted as YYYY-MM-DD.
	ReleaseDate string `json:"release_date"`
}

// ListModelsResponse holds the models available to the account.
type ListModelsResponse struct {
	// Models holds the available models.
	Models []ModelMetadata `json:"models"`

	ResponseMetadata `json:"-"`
}

// UnmarshalJSON implements [json.Unmarshaler], reporting which model card is
// incomplete when one is.
func (r *ListModelsResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		Models *[]struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
			ReleaseDate *string `json:"release_date"`
		} `json:"models"`
	}
	if err := decodeJSON(data, &wire, ""); err != nil {
		return err
	}
	if wire.Models == nil {
		return invalidField("models", nil)
	}
	r.Models = make([]ModelMetadata, 0, len(*wire.Models))
	for index, card := range *wire.Models {
		path := fmt.Sprintf("models[%d]", index)
		switch {
		case card.Name == nil:
			return invalidField(fieldPath(path, "name"), nil)
		case card.Description == nil:
			return invalidField(fieldPath(path, "description"), nil)
		case card.ReleaseDate == nil:
			return invalidField(fieldPath(path, "release_date"), nil)
		}
		r.Models = append(r.Models, ModelMetadata{Name: *card.Name, Description: *card.Description, ReleaseDate: *card.ReleaseDate})
	}
	return nil
}
