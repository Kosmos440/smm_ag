package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"smmagent/internal/config"
	"smmagent/internal/ingest/catalog"
	"smmagent/internal/media"
	"smmagent/internal/model"
	"smmagent/internal/postgres"
	"smmagent/internal/publishing"
	telegramPublisher "smmagent/internal/publishing/telegram"
	websitePublisher "smmagent/internal/publishing/website"
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

	worker := scheduler{
		store:           store,
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
	worker.run(ctx)
	return nil
}
