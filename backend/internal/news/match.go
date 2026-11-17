package news

import (
	"strings"
	"unicode"

	"smmagent/internal/model"
)

var stopWords = map[string]bool{
	"или": true, "для": true, "при": true, "как": true, "что": true, "это": true,
	"его": true, "она": true, "они": true, "над": true, "под": true, "про": true,
	"после": true, "перед": true, "снова": true, "уже": true, "еще": true, "теперь": true,
	"новый": true, "новые": true, "новая": true, "новое": true, "обновление": true,
	"and": true, "the": true, "with": true, "for": true, "from": true, "will": true,
	"new": true, "update": true,
}

func titleTokens(title string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, word := range strings.Fields(normalize(title)) {
		if len([]rune(word)) < 3 || stopWords[word] || months[word] != "" {
			continue
		}
		if strings.IndexFunc(word, unicode.IsLetter) == -1 {
			continue
		}
		tokens[word] = struct{}{}
	}
	return tokens
}

func matchEvent(material model.Material, facts []model.Fact, candidates []model.Event) (model.Event, bool) {
	title := normalize(material.Title)
	tokens := titleTokens(title)
	var best model.Event
	bestScore := 0
	for _, candidate := range candidates {
		if conflicts(candidate.Facts, facts) {
			continue
		}
		score := 0
		for _, fact := range candidate.Facts {
			if fact.URL != "" && fact.URL == material.URL {
				score = 1000
				break
			}
			if fact.Key == "document" && containsFact(facts, fact.Key, fact.Value) {
				score = 800
			}
		}
		candidateTitle := normalize(candidate.Title)
		if score == 0 && title == candidateTitle && len(tokens) >= 3 {
			score = 600
		}
		if score == 0 {
			score = titleOverlap(tokens, titleTokens(candidateTitle))
		}
		if score > bestScore || score > 0 && score == bestScore && candidate.UpdatedAt.After(best.UpdatedAt) {
			best = candidate
			bestScore = score
		}
	}
	return best, bestScore != 0
}

func titleOverlap(first, second map[string]struct{}) int {
	common := 0
	for word := range first {
		if _, found := second[word]; found {
			common++
		}
	}
	if common < 4 || common*5 < min(len(first), len(second))*4 {
		return 0
	}
	// Demand most words across the two titles to match, including when
	// the shorter headline is a substring of a longer one.
	union := len(first) + len(second) - common
	if common*3 < union*2 {
		return 0
	}
	return common * 100 / union
}

func conflicts(existing, findings []model.Fact) bool {
	existingDocument := false
	for _, fact := range existing {
		if fact.Key == "document" {
			existingDocument = true
			if containsFact(findings, fact.Key, fact.Value) {
				return false
			}
		}
	}
	if !existingDocument {
		return false
	}
	for _, fact := range findings {
		if fact.Key == "document" {
			return true
		}
	}
	return false
}
