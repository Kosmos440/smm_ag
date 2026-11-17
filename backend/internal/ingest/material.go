package ingest

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"smmagent/internal/model"
)

const MaxItems = 100

type Fetcher interface {
	Fetch(context.Context, model.Source) ([]model.Material, error)
}

func Normalize(material model.Material, source model.Source) model.Material {
	material.SourceID = source.ID
	material.Organization = strings.TrimSpace(source.Organization)
	if material.Organization == "" {
		material.Organization = strings.TrimSpace(source.Name)
	}
	material.Primary = source.Primary
	material.Title = truncate(strings.Join(strings.Fields(material.Title), " "), 240)
	material.Body = truncate(strings.Join(strings.Fields(material.Body), " "), 12000)
	material.ExternalID = truncate(strings.TrimSpace(material.ExternalID), 512)
	if material.ExternalID == "" {
		material.ExternalID = material.URL
	}
	return material
}

func truncate(text string, maxRunes int) string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxRunes]))
}

func ParseDate(value string) time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC850,
		"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
		"Mon, 02 Jan 2006 15:04 -0700", "02 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05-0700", "2006-01-02T15:04:05", "2006-01-02 15:04:05", time.DateOnly,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
