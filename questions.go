package typesafe

import (
	"encoding/json"
	"sort"
)

// Content is any value that encodes to JSON: text, a struct, a map, or a slice.
// Instructions and criteria accept all of them, so a question can carry as much
// structure as it needs.
type Content = any

// Questions are the questions of one request, keyed by the names their answers
// are returned under.
type Questions = map[string]Question

// A Question is a [Noul], a [Choice], a [Score], or a [RawQuestion]. The
// interface is closed: it exists to group the question kinds the API accepts.
type Question interface {
	// validate reports whether the question is well-formed, naming it in the error.
	validate(name string) error
}

// Noul is a yes/no question, with optional descriptions of either outcome.
//
// See the noul primitive (https://docs.typesafe.ai/primitives/noul) for details.
type Noul struct {
	// Instructions is the question to ask; optional.
	Instructions Content `json:"instructions,omitzero"`
	// Criteria describes the yes and no outcomes; optional.
	Criteria *NoulCriteria `json:"criteria,omitzero"`
}

// NoulCriteria describes the outcomes of a [Noul]. A nil field leaves that
// outcome undescribed.
type NoulCriteria struct {
	// True describes what counts as a yes answer.
	True Content `json:"true,omitzero"`
	// False describes what counts as a no answer.
	False Content `json:"false,omitzero"`
}

// MarshalJSON implements [json.Marshaler], tagging the question as a noul.
func (q Noul) MarshalJSON() ([]byte, error) {
	type noul Noul
	return json.Marshal(struct {
		Type string `json:"type"`
		noul
	}{Type: "noul", noul: noul(q)})
}

func (q Noul) validate(string) error { return nil }

// Choice is a question that selects between named alternatives.
//
// See the choice primitive (https://docs.typesafe.ai/primitives/choice) for details.
type Choice struct {
	// Instructions is the question to ask; optional.
	Instructions Content `json:"instructions,omitzero"`
	// Criteria maps each label to its description, or to nil to leave the label
	// undescribed. It is required.
	Criteria map[string]Content `json:"criteria"`
}

// MarshalJSON implements [json.Marshaler], tagging the question as a choice.
func (q Choice) MarshalJSON() ([]byte, error) {
	type choice Choice
	return json.Marshal(struct {
		Type string `json:"type"`
		choice
	}{Type: "choice", choice: choice(q)})
}

func (q Choice) validate(name string) error {
	if q.Criteria == nil {
		return questionErrorf("choice question %q requires criteria", name)
	}
	return nil
}

// Score is a question that rates state against an ordered rubric.
//
// See the score primitive (https://docs.typesafe.ai/primitives/score) for details.
type Score struct {
	// Instructions is the question to ask; optional.
	Instructions Content `json:"instructions,omitzero"`
	// Criteria is a nonempty, ordered list of descriptions, one per score starting
	// at zero.
	Criteria []Content `json:"criteria"`
}

// MarshalJSON implements [json.Marshaler], tagging the question as a score.
func (q Score) MarshalJSON() ([]byte, error) {
	type score Score
	return json.Marshal(struct {
		Type string `json:"type"`
		score
	}{Type: "score", score: score(q)})
}

func (q Score) validate(name string) error {
	if len(q.Criteria) == 0 {
		return questionErrorf("score question %q has no criteria; at least one score is required", name)
	}
	return nil
}

// RawQuestion is a question written as its wire form. It is the escape hatch for
// question fields the API supports before this SDK models them: the SDK only
// checks that it carries a nonempty "type", and leaves the rest to the API.
//
//	typesafe.RawQuestion{"type": "noul", "instructions": "Spam?", "weight": 3}
type RawQuestion map[string]Content

func (q RawQuestion) validate(name string) error {
	kind, _ := q["type"].(string)
	if kind == "" {
		return questionErrorf("question %q must have a nonempty string %q", name, "type")
	}
	if kind == "choice" || kind == "score" {
		criteria, present := q["criteria"]
		if !present {
			return questionErrorf("question %q requires %q", name, "criteria")
		}
		if kind == "score" {
			if list, ok := criteria.([]Content); ok && len(list) == 0 {
				return questionErrorf("score question %q has no criteria; at least one score is required", name)
			}
		}
	}
	return nil
}

// validateQuestions rejects an empty or malformed question set before a request
// reaches the network. Names are checked in sorted order so that a given set
// always fails on the same question.
func validateQuestions(questions Questions) error {
	if len(questions) == 0 {
		return questionErrorf("at least one question is required")
	}
	names := make([]string, 0, len(questions))
	for name := range questions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		question := questions[name]
		if question == nil {
			return questionErrorf("question %q must not be nil", name)
		}
		if err := question.validate(name); err != nil {
			return err
		}
	}
	return nil
}
