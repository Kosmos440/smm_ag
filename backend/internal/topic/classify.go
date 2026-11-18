// Package topic classifies news before event matching.
package topic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"smmagent/internal/model"
)

type Store interface {
	RussianMatches(context.Context, string, string, string, []string) ([][3]bool, error)
	Embedding(context.Context, string, []byte) ([]float32, error)
	SaveEmbedding(context.Context, string, []byte, []float32) error
}

type Classifier struct {
	Store      Store
	Embeddings *Provider
	LLM        *Provider
}

func ProfileVersion(settings model.Settings) string {
	data, _ := json.Marshal(settings)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

type term struct {
	text, subtopic string
}

func (c Classifier) Match(ctx context.Context, item model.Material, lead string, settings model.Settings) (model.TopicDecision, error) {
	decision := model.TopicDecision{Verdict: model.TopicInsufficient, Method: "keywords",
		Reason: "нет однозначного тематического совпадения", ProfileVersion: ProfileVersion(settings)}
	var rules []term
	weak := map[string]bool{}
	for _, keyword := range append([]string{settings.Topic}, settings.Keywords...) {
		rules = append(rules, term{text: keyword})
	}
	if profile := settings.TopicProfile; profile != nil {
		for _, subtopic := range profile.Subtopics {
			for _, keyword := range subtopic.Keywords {
				rules = append(rules, term{text: keyword, subtopic: subtopic.Name})
			}
		}
		for _, keyword := range profile.ContextTerms {
			rules = append(rules, term{text: keyword})
			weak[normalize(keyword)] = true
		}
	}
	terms := make([]string, len(rules))
	for index, rule := range rules {
		terms[index] = normalize(rule.text)
	}
	if lead == "" {
		lead = Lead(item.Body)
	}
	title, first, body := normalize(item.Title), normalize(lead), normalize(item.Body)
	matches, err := c.Store.RussianMatches(ctx, title, first, body, terms)
	if err != nil {
		return decision, err
	}
	for index, rule := range rules {
		key := terms[index]
		if key == "" {
			continue
		}
		exact := contains(title, key) || contains(first, key)
		beginning := exact || matches[index][0] || matches[index][1]
		if beginning && !weak[key] {
			decision.Verdict, decision.Evidence, decision.Subtopic = model.TopicRelevant, rule.text, rule.subtopic
			decision.Reason = "тематический ключ в заголовке или начале текста"
			if !exact {
				decision.Method = "word_forms"
			}
			return decision, nil
		}
		if beginning || matches[index][2] || contains(body, key) {
			decision.Evidence = rule.text
			decision.Reason = "контекстное совпадение или совпадение вне начала статьи"
		}
	}
	return decision, nil
}

func Short(item model.Material) bool {
	n := normalize(item.Body)
	return n == normalize(item.Title) || utf8.RuneCountInString(n) < 80
}

func Lead(body string) string {
	return truncate(strings.TrimSpace(strings.SplitN(strings.TrimSpace(body), "\n", 2)[0]), 400)
}

func truncate(value string, limit int) string {
	for index := range value {
		if limit == 0 {
			return value[:index]
		}
		limit--
	}
	return value
}

func normalize(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(value), "ё", "е"), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func contains(text, term string) bool {
	return strings.Contains(" "+text+" ", " "+term+" ")
}
