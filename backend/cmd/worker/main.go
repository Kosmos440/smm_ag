package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"smmagent/internal/config"
	"smmagent/internal/ingest"
	"smmagent/internal/ingest/catalog"
	ingestWebsite "smmagent/internal/ingest/website"
	"smmagent/internal/media"
	"smmagent/internal/model"
	"smmagent/internal/postgres"
	"smmagent/internal/publishing"
	telegramPublisher "smmagent/internal/publishing/telegram"
	websitePublisher "smmagent/internal/publishing/website"
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

	if len(os.Args) > 1 && os.Args[1] == "events-replay" {
		return replayEventCatalog(ctx, store, os.Args[2:])
	}
	embeddings, llm, err := topic.ConfiguredProviders()
	if err != nil {
		return err
	}
	worker := scheduler{
		store:           store,
		classifier:      topic.Classifier{Store: store, Embeddings: embeddings, LLM: llm},
		articleFetch:    ingestWebsite.New(ingest.NewPublicClient()).FetchArticle,
		fetchers:        catalog.Fetchers(),
		publishers:      map[model.Target]publishing.Publisher{model.TargetWebsite: websitePublisher.Publisher{}},
		collectInterval: cfg.CollectInterval,
		tickInterval:    cfg.TickInterval,
	}
	if cfg.TelegramToken != "" {
		publisher := telegramPublisher.New(cfg.TelegramToken)
		if os.Getenv("S3_ENDPOINT") != "" {
			publisher.TempDir = os.Getenv("MEDIA_TEMP_DIR")
			publisher.Media = media.New(os.Getenv("S3_ENDPOINT"), os.Getenv("S3_BUCKET"),
				os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"), os.Getenv("MEDIA_TEMP_DIR"))
		}
		worker.publishers[model.TargetTelegram] = publisher
	}
	log.Println("News worker started")
	var background sync.WaitGroup
	background.Go(func() { worker.readArticles(ctx) })
	if os.Getenv("EVENT_CATALOG_SHADOW") == "1" {
		background.Go(func() { followEventCatalog(ctx, store, cfg.CollectInterval) })
	}
	worker.run(ctx)
	background.Wait()
	return nil
}
