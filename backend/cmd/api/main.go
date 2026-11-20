package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"smmagent/internal/channels"
	"smmagent/internal/config"
	"smmagent/internal/httpapi"
	"smmagent/internal/ingest/catalog"
	"smmagent/internal/model"
	"smmagent/internal/news"
	"smmagent/internal/postgres"
	"smmagent/internal/topic"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	store, err := postgres.Open(startup, cfg.DatabaseURL)
	if err == nil {
		defer store.Close()
		err = store.Migrate(startup)
	}
	cancel()
	if err != nil {
		return err
	}

	fetchers := catalog.Fetchers()
	service := channels.New(store).WithSourceProbe(func(ctx context.Context, source model.Source) ([]model.Material, error) {
		fetcher := fetchers[source.Kind]
		if fetcher == nil {
			return nil, errors.New("unsupported source kind")
		}
		return fetcher.Fetch(ctx, source)
	})
	embeddings, llm, err := topic.ConfiguredProviders()
	if err != nil {
		return err
	}
	classifier := topic.Classifier{Store: store, Embeddings: embeddings, LLM: llm}
	service = service.WithTopicPreview(func(ctx context.Context, channel model.Channel, id int64) (model.TopicDecision, error) {
		material, err := store.TopicMaterial(ctx, channel.ID, id)
		if err != nil {
			return model.TopicDecision{}, err
		}
		body, reason := news.PrepareBody(material.Title, material.Body)
		if reason != "" && material.Body != "" {
			return model.TopicDecision{Verdict: model.TopicIrrelevant, Method: "rules",
				Reason: reason, ProfileVersion: topic.ProfileVersion(channel.Settings)}, nil
		}
		material.Body = body
		article, err := store.Article(ctx, id)
		if err != nil {
			return model.TopicDecision{}, err
		}
		blocked := news.TopicBlocked(material, channel.Settings)
		if article.State == "ready" {
			material.Body, reason = news.PrepareBody(material.Title, article.Body)
			article.Lead, _ = news.PrepareBody(material.Title, article.Lead)
		}
		if blocked || news.TopicBlocked(material, channel.Settings) {
			return model.TopicDecision{Verdict: model.TopicIrrelevant, Method: "rules",
				Reason: "запрещённая тема", ProfileVersion: topic.ProfileVersion(channel.Settings)}, nil
		}
		if reason != "" {
			return model.TopicDecision{Verdict: model.TopicInsufficient, Method: "rules",
				Reason: reason, ProfileVersion: topic.ProfileVersion(channel.Settings)}, nil
		}
		decision, err := classifier.Match(ctx, material, article.Lead, channel.Settings)
		if err != nil {
			return model.TopicDecision{}, err
		}
		return classifier.Resolve(ctx, material, channel.Settings, decision), nil
	})
	if endpoint := os.Getenv("LLM_FILTERS_URL"); endpoint != "" {
		generate, err := channels.ChatFilterGenerator(endpoint, os.Getenv("LLM_FILTERS_MODEL"), os.Getenv("LLM_FILTERS_API_KEY"))
		if err != nil {
			return err
		}
		service = service.WithFilterGenerator(generate)
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(service, store.Ready),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdown := make(chan error, 1)
	go func() {
		<-ctx.Done()
		grace, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdown <- server.Shutdown(grace)
	}()
	log.Printf("HTTP API listening on %s", cfg.HTTPAddr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-shutdown
}
