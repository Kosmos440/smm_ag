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
	"smmagent/internal/postgres"
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
