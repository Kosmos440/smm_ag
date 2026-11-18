package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
	"smmagent/internal/news"
	"smmagent/internal/postgres"
	"smmagent/internal/publishing"
	"smmagent/internal/topic"
)

type scheduler struct {
	store           *postgres.Store
	classifier      topic.Classifier
	articleFetch    func(context.Context, model.Material) (model.Material, error)
	fetchers        map[model.SourceKind]ingest.Fetcher
	publishers      map[model.Target]publishing.Publisher
	collectInterval time.Duration
	tickInterval    time.Duration
	nextCollection  time.Time
}

func (s *scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.cycle(ctx); err != nil && ctx.Err() == nil {
			log.Printf("worker cycle: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *scheduler) cycle(ctx context.Context) error {
	release, err := s.store.AcquireWorker(ctx)
	if errors.Is(err, model.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	defer release()
	if err := s.store.MarkInterruptedPublications(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.store.ExpirePublications(ctx, now); err != nil {
		return err
	}
	channels, err := s.store.EnabledChannels(ctx)
	if err != nil {
		return err
	}
	if len(channels) == 0 {
		return nil
	}
	var collectErr error
	if !now.Before(s.nextCollection) {
		collectErr = s.collect(ctx)
		if collectErr == nil {
			s.nextCollection = time.Now().Add(s.collectInterval)
		}
	}
	for _, channel := range channels {
		now = time.Now().UTC()
		if err := s.evaluate(ctx, channel, now); err != nil && ctx.Err() == nil {
			log.Printf("evaluate channel %d: %v", channel.ID, err)
		}
		if channel.Website {
			s.deliver(ctx, channel, model.TargetWebsite, "")
		}
		if channel.TelegramChatID != "" {
			s.deliver(ctx, channel, model.TargetTelegram, channel.TelegramChatID)
		}
	}
	return collectErr
}

func (s *scheduler) collect(ctx context.Context) error {
	sources, err := s.store.VerifiedSources(ctx)
	if err != nil {
		return err
	}
	jobs := make(chan model.Source)
	var workers sync.WaitGroup
	for range min(4, len(sources)) {
		workers.Go(func() {
			for source := range jobs {
				s.collectSource(ctx, source)
			}
		})
	}
	defer workers.Wait()
	defer close(jobs)
	for _, source := range sources {
		select {
		case jobs <- source:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *scheduler) collectSource(ctx context.Context, source model.Source) {
	fetcher := s.fetchers[source.Kind]
	if fetcher == nil {
		return
	}
	fetchContext, cancel := context.WithTimeout(ctx, 25*time.Second)
	materials, err := fetcher.Fetch(fetchContext, source)
	cancel()
	fetchedAt := time.Now().UTC()
	message := ""
	if err != nil {
		// Source URLs may contain credentials in query parameters.
		message = "could not fetch or parse source"
	} else {
		for index := range materials {
			materials[index].FetchedAt = fetchedAt
		}
		if err = s.store.SaveMaterials(ctx, source.ID, materials); err != nil {
			message = "could not save source materials"
		}
	}
	if message != "" && ctx.Err() == nil {
		log.Printf("source %d: %s", source.ID, message)
	}
	if err := s.store.RecordPoll(ctx, source.ID, message == "", message, fetchedAt); err != nil && ctx.Err() == nil {
		log.Printf("record poll for source %d: %v", source.ID, err)
	}
}

func (s *scheduler) evaluate(ctx context.Context, channel model.Channel, now time.Time) error {
	evaluationCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	classifier := s.classifier
	classifier.Store = s.store
	since := now.Add(-time.Duration(channel.Settings.MaxAgeMinutes) * time.Minute)
	materials, err := s.store.UnprocessedMaterials(ctx, channel.ID, since, 100)
	if err != nil {
		return err
	}
	for _, item := range materials {
		if evaluationCtx.Err() != nil {
			break
		}
		material, decision, ready, err := s.classify(evaluationCtx, classifier, item, channel.Settings, time.Now().UTC())
		if evaluationCtx.Err() != nil && ctx.Err() == nil {
			break
		}
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		result := model.Evaluation{Reason: decision.Reason}
		if decision.Verdict == model.TopicRelevant {
			candidates, err := s.store.CandidateEvents(ctx, channel.ID, now.AddDate(0, 0, -30), 200)
			if err != nil {
				return err
			}
			result = news.EvaluateRelevant(material, item.Source, channel.Settings, candidates, now)
		}
		result.Topic = &decision
		result.Event.ChannelID = channel.ID
		if err := s.store.ApplyEvaluation(ctx, channel, material, result, time.Now().UTC()); err != nil {
			return fmt.Errorf("material %d: %w", item.Material.ID, err)
		}
	}
	return nil
}

func (s *scheduler) deliver(ctx context.Context, channel model.Channel, target model.Target, destination string) {
	if s.publishers[target] == nil {
		return
	}
	for range 20 {
		sent, err := s.deliverOne(ctx, channel, target, destination)
		if err != nil && ctx.Err() == nil {
			log.Printf("deliver channel %d (%s): %v", channel.ID, target, err)
		}
		if !sent || err != nil {
			return
		}
	}
}

func (s *scheduler) deliverOne(ctx context.Context, channel model.Channel, target model.Target, destination string) (bool, error) {
	now := time.Now().UTC()
	lastSent, err := s.store.LastSent(ctx, channel.ID, target, destination)
	if err != nil {
		return false, err
	}
	urgent, reserve := publishing.Window(channel.Settings, now, lastSent)
	if !urgent && !reserve {
		return false, nil
	}
	reserveBefore := now.Add(-time.Duration(channel.Settings.ReserveAfterMinutes) * time.Minute)
	post, err := s.store.ClaimPublication(ctx, channel.ID, target, destination, urgent, reserve, now, reserveBefore)
	if errors.Is(err, model.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	previous := ""
	if target == model.TargetTelegram {
		previous, err = s.store.PreviousExternalID(ctx, post)
		if err != nil {
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return false, errors.Join(err, s.store.FinishPublication(finishCtx, post.ID, model.PublicationPending, "", "", time.Now().UTC()))
		}
	}
	post.ChannelName = channel.Name
	id, err := s.publishers[target].Send(ctx, post, previous)
	state, message := model.PublicationSent, ""
	if err != nil {
		state, message = model.PublicationFailed, err.Error()
		var outcome *publishing.ResultError
		if errors.As(err, &outcome) && outcome.Uncertain {
			state = model.PublicationUncertain
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	finishErr := s.store.FinishPublication(finishCtx, post.ID, state, id, message, time.Now().UTC())
	return state == model.PublicationSent && finishErr == nil, errors.Join(err, finishErr)
}
