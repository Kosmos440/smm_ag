package catalog

import (
	"smmagent/internal/ingest"
	"smmagent/internal/ingest/rss"
	"smmagent/internal/ingest/telegram"
	"smmagent/internal/ingest/website"
	"smmagent/internal/model"
)

// Fetchers are shared by scheduled ingestion and management previews.
func Fetchers() map[model.SourceKind]ingest.Fetcher {
	client := ingest.NewPublicClient()
	return map[model.SourceKind]ingest.Fetcher{
		model.SourceRSS:      rss.New(client),
		model.SourceWebsite:  website.New(client),
		model.SourceTelegram: telegram.New(client),
	}
}
