package model

import "time"

type TopicProfile struct {
	Description      string     `json:"description,omitempty"`
	Subtopics        []Subtopic `json:"subtopics,omitempty"`
	ContextTerms     []string   `json:"context_terms,omitempty"`
	PositiveExamples []string   `json:"positive_examples,omitempty"`
	NegativeExamples []string   `json:"negative_examples,omitempty"`
	UseVectors       bool       `json:"use_vectors,omitempty"`
	UseLLM           bool       `json:"use_llm,omitempty"`
	VectorAccept     *float64   `json:"vector_accept,omitempty"`
	VectorReject     *float64   `json:"vector_reject,omitempty"`
}

type Subtopic struct {
	Name     string   `json:"name"`
	Keywords []string `json:"keywords,omitempty"`
}

type TopicVerdict string

const (
	TopicRelevant     TopicVerdict = "relevant"
	TopicIrrelevant   TopicVerdict = "irrelevant"
	TopicInsufficient TopicVerdict = "insufficient"
)

type TopicDecision struct {
	Verdict        TopicVerdict `json:"verdict"`
	Method         string       `json:"method"`
	Reason         string       `json:"reason"`
	Evidence       string       `json:"evidence,omitempty"`
	Subtopic       string       `json:"subtopic,omitempty"`
	Score          *float64     `json:"score,omitempty"`
	ProfileVersion string       `json:"profile_version"`
}

type TopicRecord struct {
	MaterialID  int64          `json:"material_id"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Decision    *TopicDecision `json:"decision"`
	Preview     *TopicDecision `json:"preview"`
	Editor      *TopicVerdict  `json:"editor_verdict"`
	EvaluatedAt time.Time      `json:"evaluated_at"`
}
