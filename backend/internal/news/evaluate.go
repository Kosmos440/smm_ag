package news

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"smmagent/internal/model"
)

// Evaluate classifies an individual source observation against the latest
// persisted event versions. The caller owns version assignment and persistence.
func Evaluate(material model.Material, source model.Source, settings model.Settings, candidates []model.Event, now time.Time) model.Evaluation {
	if reason := Eligibility(material, source, settings, now); reason != "" {
		return rejected(reason)
	}
	if !topicMatches(normalize(material.Title+" "+material.Body), settings) {
		return rejected("материал не соответствует теме")
	}
	return EvaluateRelevant(material, source, settings, candidates, now)
}

// Eligibility applies the editorial rules that precede topic classification.
func Eligibility(material model.Material, source model.Source, settings model.Settings, now time.Time) string {
	if source.Status != model.SourceVerified {
		return "источник не проверен"
	}
	if strings.TrimSpace(material.Title) == "" || material.URL == "" {
		return "нет заголовка или ссылки"
	}
	maxAge := time.Duration(settings.MaxAgeMinutes) * time.Minute
	if material.PublishedAt.IsZero() || material.PublishedAt.After(now) ||
		maxAge <= 0 || now.Sub(material.PublishedAt) > maxAge {
		return "дата материала неизвестна или новость несвежая"
	}
	if TopicBlocked(material, settings) {
		return "запрещённая тема"
	}
	return ""
}

func TopicBlocked(material model.Material, settings model.Settings) bool {
	return containsPhrase(normalize(material.Title+" "+material.Body), settings.BlockedTerms)
}

// EvaluateRelevant applies the event and publication rules after the topic check.
func EvaluateRelevant(material model.Material, source model.Source, settings model.Settings, candidates []model.Event, now time.Time) model.Evaluation {
	body, reason := PrepareBody(material.Title, material.Body)
	if reason != "" {
		return rejected(reason)
	}
	material.Body = body
	text := material.Title + " " + material.Body
	normalized := normalize(text)

	organization := strings.TrimSpace(source.Organization)
	if organization == "" {
		organization = strings.TrimSpace(source.Name)
	}
	if organization == "" {
		organization = material.Organization
	}
	if meeting, milestone := trilateralReport(material.Title, material.Body); meeting {
		event, found := recentTrilateralEvent(candidates, now)
		return evaluateTrilateral(material, organization, milestone, event, found, now)
	}
	facts := extractFacts(text, material.URL, organization)
	event, found := matchEvent(material, facts, candidates)
	if !source.Primary && needsPrimary(normalized, facts) {
		if found {
			return model.Evaluation{Event: event, Accepted: true, Reason: "нет первоисточника для новой версии"}
		}
		return rejected("для конкретных условий нужен первоисточник")
	}

	confirmation := source.Primary && !hasConfirmation(event.Facts)
	changedFacts := hasNewFact(event.Facts, facts)
	if found && !changedFacts && !confirmation {
		return model.Evaluation{Event: event, Accepted: true, Reason: "известные факты"}
	}

	if !found {
		event.FirstSeen = now
		event.Facts = []model.Fact{{Key: "report", Value: material.Title, URL: material.URL, Organization: organization}}
	}
	event.Title = material.Title
	event.Summary = shortSummary(material.Title, material.Body)
	event.UpdatedAt = now
	event.Facts = mergeFacts(event.Facts, facts)
	if confirmation {
		event.Facts = append(event.Facts, model.Fact{Key: "confirmation", Value: material.Title, URL: material.URL, Organization: organization})
	}
	priority, urgent, reason := rank(normalized, settings.Audience, source.Primary, facts, now)
	if found {
		reason = "существенное дополнение: " + reason
	}
	return model.Evaluation{Event: event, Accepted: true, Changed: true, Urgent: urgent, Priority: priority, Reason: reason}
}

func rejected(reason string) model.Evaluation {
	return model.Evaluation{Reason: reason}
}

func topicMatches(text string, settings model.Settings) bool {
	if containsPhrase(text, settings.Keywords) || containsPhrase(text, []string{settings.Topic}) {
		return true
	}
	if len(settings.Keywords) > 0 {
		return false
	}
	topic := titleTokens(settings.Topic)
	count := 0
	for word := range titleTokens(text) {
		if _, found := topic[word]; found {
			count++
		}
	}
	return len(topic) > 0 && count*2 >= len(topic)
}

func rank(text, audience string, primary bool, facts []model.Fact, now time.Time) (int, bool, string) {
	score := 30
	if primary {
		score += 10
	}
	if mentionsAudience(text, audience) {
		score += 10
	}
	requiresAction := containsPhrase(text, []string{
		"вступит в силу", "вступает в силу", "вступают в силу", "вступили в силу", "вступил в силу",
		"обязаны", "обязано", "срок подачи", "до окончания", "подача заявлений",
		"требуется", "необходимо", "deadline", "takes effect", "required", "must submit",
	})
	if requiresAction {
		score += 25
		if nearDate(facts, now) {
			score += 15
		}
	}
	highImpact := containsPhrase(text, []string{
		"штраф", "штрафы", "запрет", "запретили", "отзыв разрешений", "приостановка",
		"налоговая ставка", "ставка налога", "обязательная маркировка",
		"fine", "penalty", "mandatory", "suspension", "recall", "emergency", "tax rate",
	})
	if highImpact {
		score += 20
	}
	emergency := containsPhrase(text, []string{
		"чрезвычайная ситуация", "объявлена эвакуация", "критическая уязвимость",
		"security advisory", "evacuation order", "critical vulnerability",
	})
	if emergency {
		score += 35
	}
	score = min(100, score)
	if primary && score >= 75 {
		return score, true, "первоисточник сообщил о значимых последствиях"
	}
	return score, false, fmt.Sprintf("резерв, приоритет %d", score)
}

func nearDate(facts []model.Fact, now time.Time) bool {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, fact := range facts {
		if fact.Key != "date" {
			continue
		}
		date, err := parseFactDate(fact.Value, now)
		if err == nil && !date.Before(today) && !date.After(today.AddDate(0, 0, 3)) {
			return true
		}
	}
	return false
}

func mentionsAudience(text, audience string) bool {
	tokens := titleTokens(audience)
	return len(tokens) > 0 && containsPhrase(text, []string{audience})
}

func shortSummary(title, body string) string {
	body = strings.TrimSpace(body)
	for _, divider := range []string{"\n", " "} {
		if strings.HasPrefix(body, title+divider) {
			body = strings.TrimSpace(strings.TrimPrefix(body, title))
			break
		}
	}
	runes := []rune(body)
	if len(runes) <= 520 {
		return body
	}
	end := 520
	for i := end; i > 455; i-- {
		if unicode.IsSpace(runes[i-1]) {
			end = i - 1
			break
		}
	}
	return string(runes[:end]) + "…"
}

func normalize(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(text, "Ё", "Е"), "ё", "е")), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func containsPhrase(text string, phrases []string) bool {
	padded := " " + text + " "
	for _, phrase := range phrases {
		if normalized := normalize(phrase); normalized != "" && strings.Contains(padded, " "+normalized+" ") {
			return true
		}
	}
	return false
}
