package main

import (
	"testing"
	"time"

	"smmagent/internal/model"
)

func TestEventCatalogReplay(t *testing.T) {
	store := pipelineStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	source, err := store.CreateSource(ctx, model.Source{
		Name: "Test", Organization: "Test", URL: "https://example.org/feed",
		Kind: model.SourceRSS, Status: model.SourceVerified,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Reports are intentionally written with different titles and inflections.
	reports := []model.Material{
		{Title: "Жалобу BSW с требованием пересчитать голоса на выборах в Бундестаг 2025 года отклонили"},
		{Title: "КС ФРГ отклонил жалобу BSW с требованием пересчитать голоса на выборах 2025 года"},
		{Title: "Суд ФРГ отверг жалобу BSW с требованием пересчитать голоса на выборах"},
		{Title: "Запрет на продажу вейпов на 5 лет одобрили в Питере", Body: "Депутаты ЗакСа приняли законопроект во втором чтении."},
		{Title: "В Петербурге с марта 2027 года запретят розничную продажу вейпов", Body: "Депутаты приняли закон в третьем, окончательном чтении."},
		{Title: "В Москве с марта 2027 года запретят розничную продажу вейпов", Body: "Депутаты приняли закон в третьем чтении."},
		{Title: "Запрет на продажу вейпов приняли в Питере во втором чтении"},
	}
	before, err := store.CreateChannel(ctx, model.Channel{
		Name: "First", Website: true, Enabled: true,
		Settings: model.Settings{Topic: "новости", Audience: "читатели", MaxAgeMinutes: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateChannel(ctx, model.Channel{
		Name: "Second", Website: true, Enabled: true, Settings: before.Settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range reports {
		report := &reports[i]
		report.URL = source.URL + "/" + string(rune('a'+i))
		report.FetchedAt = now.Add(time.Duration(i) * time.Second)
		report.PublishedAt = now
	}
	if err := store.SaveMaterials(ctx, source.ID, reports); err != nil {
		t.Fatal(err)
	}
	items, err := store.UnprocessedMaterials(ctx, before.ID, now.Add(-time.Minute), 100)
	if err != nil || len(items) != len(reports) {
		t.Fatalf("saved materials: %d, %v", len(items), err)
	}
	for i, item := range items {
		channel := before
		if i%2 == 1 {
			channel = other
		}
		evaluation := model.Evaluation{
			Reason: "excluded by keywords",
			Topic:  &model.TopicDecision{Verdict: model.TopicRelevant, Method: "fixture"},
		}
		if i == 3 {
			evaluation.Topic.Verdict = model.TopicInsufficient
		}
		if err := store.ApplyEvaluation(ctx, channel, item.Material, evaluation, item.Material.FetchedAt); err != nil {
			t.Fatal(err)
		}
		if i == 3 {
			if err := store.SaveTopicPreview(ctx, channel.ID, item.Material.ID, model.TopicDecision{Verdict: model.TopicRelevant}); err != nil {
				t.Fatal(err)
			}
		}
		if i == 0 {
			if err := store.ApplyEvaluation(ctx, other, item.Material, evaluation, item.Material.FetchedAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	scope := "archive/test"
	then := now.Add(time.Minute)
	if n, err := drainEventCatalog(ctx, store, scope, 0, now.Add(-time.Second), then); err != nil || n != len(reports) {
		t.Fatalf("first replay: %d (%v)", n, err)
	}
	assignments, err := store.CatalogAssignments(ctx, scope, nil)
	if err != nil || len(assignments) != len(reports) {
		t.Fatalf("assignments: %+v (%v)", assignments, err)
	}
	for _, item := range assignments[:3] {
		if item.EventID != assignments[0].EventID || item.Version != 1 {
			t.Fatalf("court reports must share an event: %+v", assignments[:3])
		}
	}
	second, final, moscow, late := assignments[3], assignments[4], assignments[5], assignments[6]
	if second.EventID != final.EventID || second.Reading != 2 || final.Reading != 3 || final.Version != 2 ||
		final.Relation != "update" || moscow.EventID == final.EventID ||
		late.EventID != second.EventID || late.Version != 1 || late.Relation != "repeat" {
		t.Fatalf("reading or jurisdiction mismatch: %+v", assignments[3:])
	}
	if n, err := drainEventCatalog(ctx, store, scope, 0, now.Add(-time.Second), then); err != nil || n != 0 {
		t.Fatalf("second replay: %d (%v)", n, err)
	}
	if records, err := store.ListPublications(ctx, before.ID, 10, 0); err != nil || len(records) != 0 {
		t.Fatalf("shadow writes must stay out of the publication queue: %+v (%v)", records, err)
	}
	if records, err := store.CatalogAssignments(ctx, "shadow", []int64{}); err != nil || len(records) != 0 {
		t.Fatalf("archive and live scope must be independent: %+v (%v)", records, err)
	}
}
