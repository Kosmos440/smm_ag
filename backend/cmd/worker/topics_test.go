package main

import (
	"testing"
	"time"

	"smmagent/internal/model"
	"smmagent/internal/topic"
)

func TestHeadlineCopyRejectedBeforeArticleAndModels(t *testing.T) {
	now := time.Now().UTC()
	for _, kind := range []model.SourceKind{model.SourceRSS, model.SourceWebsite, model.SourceTelegram} {
		item := model.SourcedMaterial{
			Source: model.Source{Kind: kind, Status: model.SourceVerified},
			Material: model.Material{Title: "Решение принято", Body: "РЕШЕНИЕ принято. Читать далее",
				URL: "https://example.org/item", PublishedAt: now},
		}
		// No store or model provider is needed for this decision.
		_, decision, ready, err := (&scheduler{}).classify(t.Context(), topic.Classifier{}, item, model.Settings{MaxAgeMinutes: 60}, now)
		if err != nil || !ready || decision.Verdict != model.TopicIrrelevant || decision.Reason != "текст повторяет заголовок" {
			t.Fatalf("%s: %+v, ready=%v, err=%v", kind, decision, ready, err)
		}
	}
}
