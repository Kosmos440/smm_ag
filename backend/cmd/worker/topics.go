package main

import (
	"context"
	"errors"
	"log"
	"time"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
	"smmagent/internal/news"
	"smmagent/internal/topic"
)

func (s *scheduler) classify(ctx context.Context, classifier topic.Classifier, item model.SourcedMaterial, settings model.Settings, now time.Time) (model.Material, model.TopicDecision, bool, error) {
	material := item.Material
	if reason := news.Eligibility(material, item.Source, settings, now); reason != "" {
		return material, eligibilityDecision(settings, reason), true, nil
	}
	body, reason := news.PrepareBody(material.Title, material.Body)
	// An absent RSS description can be read from the article. An explicit
	// headline copy is rejected before any article lookup or model call.
	if reason != "" && (item.Source.Kind != model.SourceRSS || material.Body != "") {
		return material, eligibilityDecision(settings, reason), true, nil
	}
	material.Body = body
	lead := ""
	var state string
	// RSS either uses the source text or the article shared by every channel.
	if item.Source.Kind == model.SourceRSS {
		article, err := s.store.Article(ctx, material.ID)
		if err != nil {
			return material, model.TopicDecision{}, false, err
		}
		state = article.State
		if state == "ready" {
			material.Body, reason = news.PrepareBody(material.Title, article.Body)
			lead, _ = news.PrepareBody(material.Title, article.Lead)
			media := append([]model.Media{}, material.Media...)
			seen := map[string]bool{}
			for _, entry := range media {
				seen[entry.URL] = true
			}
			for _, entry := range article.Media {
				if !seen[entry.URL] && len(media) < 10 {
					media = append(media, entry)
					seen[entry.URL] = true
				}
			}
			material.Media = media
		}
	}
	if reason != "" {
		if item.Source.Kind == model.SourceRSS && s.articleFetch != nil && (state == "" || state == "pending") {
			until := material.PublishedAt.Add(time.Duration(settings.MaxAgeMinutes) * time.Minute)
			err := s.store.QueueArticle(ctx, material.ID, until)
			return material, model.TopicDecision{}, false, err
		}
		return material, eligibilityDecision(settings, reason), true, nil
	}
	if news.TopicBlocked(material, settings) {
		return material, eligibilityDecision(settings, "запрещённая тема в тексте статьи"), true, nil
	}
	decision, err := classifier.Match(ctx, material, lead, settings)
	if err != nil {
		return material, decision, false, err
	}
	if decision.Verdict == model.TopicInsufficient && item.Source.Kind == model.SourceRSS &&
		topic.Short(material) && s.articleFetch != nil && (state == "" || state == "pending") {
		until := material.PublishedAt.Add(time.Duration(settings.MaxAgeMinutes) * time.Minute)
		err := s.store.QueueArticle(ctx, material.ID, until)
		return material, decision, false, err
	}
	decision = classifier.Resolve(ctx, material, settings, decision)
	if state == "unavailable" && topic.Short(material) && decision.Verdict == model.TopicInsufficient {
		decision.Reason = "страница статьи недоступна или не содержит достаточно текста"
	}
	return material, decision, true, nil
}

func eligibilityDecision(settings model.Settings, reason string) model.TopicDecision {
	return model.TopicDecision{Verdict: model.TopicIrrelevant, Method: "rules", Reason: reason,
		ProfileVersion: topic.ProfileVersion(settings)}
}

func (s *scheduler) readArticles(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		job, err := s.store.ClaimArticle(ctx)
		if err == nil {
			fetchContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			material, fetchErr := s.articleFetch(fetchContext, job.Material)
			cancel()
			if ctx.Err() != nil {
				return
			}
			lead := topic.Lead(material.Body)
			material = ingest.Normalize(material, model.Source{})
			if fetchErr != nil || topic.Short(material) {
				log.Printf("article %d unavailable on attempt %d", job.Material.ID, job.Attempt)
				material.Body, lead, material.Media = "", "", nil
			}
			err = s.store.FinishArticle(ctx, job, material.Body, lead, material.Media)
		}
		if err != nil && !errors.Is(err, model.ErrNotFound) && ctx.Err() == nil {
			log.Printf("article queue: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
