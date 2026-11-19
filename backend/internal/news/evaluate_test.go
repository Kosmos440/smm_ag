package news

import (
	"testing"
	"time"

	"smmagent/internal/model"
)

var (
	testNow = time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)
	policy  = model.Settings{
		Topic: "регулирование торговли", Audience: "малый бизнес",
		Keywords: []string{"маркировка", "маркировки"}, BlockedTerms: []string{"реклама"}, MaxAgeMinutes: 60,
	}
	official = model.Source{Organization: "Регулятор", Status: model.SourceVerified, Primary: true}
	media    = model.Source{Organization: "Редакция", Status: model.SourceVerified}
)

func TestOfficialReleaseDuplicateAndCorrection(t *testing.T) {
	material := model.Material{
		Title: "Новые правила маркировки вступают в силу 19 ноября 2026 года",
		Body:  "Обязательная маркировка вводится для продавцов. Документ № 73.",
		URL:   "https://example.org/rules-73", PublishedAt: testNow.Add(-time.Minute),
	}
	first := Evaluate(material, official, policy, nil, testNow)
	if !first.Accepted || !first.Changed || !first.Urgent || !containsFact(first.Event.Facts, "date", "2026-11-19") {
		t.Fatalf("expected urgent official release: %#v", first)
	}
	existing := first.Event
	existing.ID = 21
	existing.Version = 4

	duplicate := material
	duplicate.URL = "https://other.example.org/report"
	result := Evaluate(duplicate, media, policy, []model.Event{existing}, testNow)
	if !result.Accepted || result.Changed || result.Event.ID != existing.ID {
		t.Fatalf("expected a duplicate attached to event: %#v", result)
	}

	correction := material
	correction.Title = "Новые правила маркировки вступают в силу 20 ноября 2026 года"
	correction.URL = "https://example.org/rules-73-correction"
	result = Evaluate(correction, media, policy, []model.Event{existing}, testNow)
	if !result.Accepted || result.Changed || result.Event.ID != existing.ID {
		t.Fatalf("expected no unconfirmed correction: %#v", result)
	}
	result = Evaluate(correction, official, policy, []model.Event{existing}, testNow)
	if !result.Accepted || !result.Changed || result.Event.ID != existing.ID || result.Event.Version != 4 ||
		!containsFact(result.Event.Facts, "date", "2026-11-20") || containsFact(result.Event.Facts, "date", "2026-11-19") ||
		!containsFact(existing.Facts, "date", "2026-11-19") {
		t.Fatalf("expected distinct updated event with original version: %#v", result)
	}

	different := correction
	different.Body = "Обязательная маркировка вводится для продавцов. Документ № 547."
	different.URL = "https://example.org/rules-547"
	result = Evaluate(different, official, policy, []model.Event{existing}, testNow)
	if !result.Accepted || !result.Changed || result.Event.ID != 0 {
		t.Fatalf("expected a separate event for distinct documents: %#v", result)
	}
}

func TestRejectsUnverifiedStaleBlockedAndOffTopic(t *testing.T) {
	fresh := testNow.Add(-time.Minute)
	for _, tc := range []struct {
		name, title string
		published   time.Time
		source      model.Source
	}{
		{"unverified", "Новости маркировки", fresh, model.Source{Status: model.SourceCandidate}},
		{"stale", "Новости маркировки", testNow.Add(-2 * time.Hour), official},
		{"undated", "Новости маркировки", time.Time{}, official},
		{"future", "Новости маркировки", testNow.Add(time.Hour), official},
		{"blocked", "Реклама на складе маркировки", fresh, official},
		{"off topic", "Прогноз погоды в городе", fresh, official},
		{"secondary conditions", "Срок маркировки перенесли на 20 ноября", fresh, media},
	} {
		t.Run(tc.name, func(t *testing.T) {
			material := model.Material{URL: "https://example.org/item", Title: tc.title, PublishedAt: tc.published}
			if result := Evaluate(material, tc.source, policy, nil, testNow); result.Accepted || result.Changed {
				t.Fatalf("unexpected acceptance: %#v", result)
			}
		})
	}
}

func TestRoutineSecondaryCoverageAndOfficialConfirmation(t *testing.T) {
	material := model.Material{
		URL: "https://example.org/workshop", Title: "Инструкции по маркировке подготовят к семинару",
		Body: "На встрече разберут типовые ошибки.", PublishedAt: testNow.Add(-time.Minute),
	}
	localPolicy := policy
	localPolicy.Keywords = []string{"маркировке"}
	ordinary := Evaluate(material, media, localPolicy, nil, testNow)
	if !ordinary.Accepted || !ordinary.Changed || ordinary.Urgent {
		t.Fatalf("expected routine article in reserve: %#v", ordinary)
	}
	existing := ordinary.Event
	existing.ID = 17
	material.URL = "https://public.example.org/workshop"
	confirmed := Evaluate(material, official, localPolicy, []model.Event{existing}, testNow)
	if !confirmed.Accepted || !confirmed.Changed || confirmed.Event.ID != 17 || !hasConfirmation(confirmed.Event.Facts) {
		t.Fatalf("expected confirmation on existing event: %#v", confirmed)
	}
}

func TestTrilateralMilestonesAreUrgentUpdates(t *testing.T) {
	material := model.Material{Title: "Готовится встреча лидеров РФ, США и КНР",
		Body: "Помощник президента рассказал о подготовке.", URL: "https://example.org/upcoming"}
	preview := EvaluateRelevant(material, media, model.Settings{}, nil, testNow)
	if !preview.Changed || preview.Urgent {
		t.Fatalf("a scheduled meeting belongs in reserve: %+v", preview)
	}
	existing := preview.Event
	existing.ID = 58
	material.Title = "Завершилась трёхсторонняя встреча лидеров России, США и Китая"
	material.URL = "https://example.org/held"
	finished := EvaluateRelevant(material, media, model.Settings{}, []model.Event{existing}, testNow)
	if !finished.Changed || !finished.Urgent || finished.Event.ID != existing.ID ||
		!containsFact(finished.Event.Facts, meetingMilestoneKey, "meeting_held") {
		t.Fatalf("meeting completion should update the same event urgently: %+v", finished)
	}
	material.Title = "Трамп заявил о результатах по итогам переговоров с лидерами России и Китая"
	material.URL = "https://example.org/statement"
	statement := EvaluateRelevant(material, media, model.Settings{}, []model.Event{finished.Event}, testNow)
	if !statement.Changed || !statement.Urgent || statement.Event.ID != existing.ID ||
		!containsFact(statement.Event.Facts, meetingMilestoneKey, "meeting_held") {
		t.Fatalf("the first leader statement should preserve the prior milestone: %+v", statement)
	}
	material.URL = "https://other.example.org/statement"
	repeat := EvaluateRelevant(material, media, model.Settings{}, []model.Event{statement.Event}, testNow)
	if !repeat.Accepted || repeat.Changed || repeat.Urgent {
		t.Fatalf("repeat coverage must not produce another post: %+v", repeat)
	}
	material.Title = "По итогам встречи России, США и Китая может быть опубликовано совместное заявление"
	if result := EvaluateRelevant(material, media, model.Settings{}, []model.Event{statement.Event}, testNow); result.Changed || result.Urgent {
		t.Fatalf("an expected statement is not a published outcome: %+v", result)
	}
	material.Title = "Кремль опубликовал текст декларации, принятой по итогам трехсторонней встречи"
	material.Body = "Россия, США и Китай приняли совместную декларацию по итогам переговоров."
	material.URL = "https://example.org/declaration"
	published := EvaluateRelevant(material, media, model.Settings{}, []model.Event{statement.Event}, testNow)
	if !published.Changed || !published.Urgent || published.Event.ID != existing.ID ||
		!containsFact(published.Event.Facts, meetingMilestoneKey, "joint_statement") {
		t.Fatalf("the published declaration should be a new milestone: %+v", published)
	}
}
