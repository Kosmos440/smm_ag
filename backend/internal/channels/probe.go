package channels

import (
	"context"
	"errors"
	"time"

	"smmagent/internal/model"
)

var (
	ErrProbeUnavailable = errors.New("source probe unavailable")
	ErrProbeFailed      = errors.New("source probe failed")
)

type SourceProbe func(context.Context, model.Source) ([]model.Material, error)

type SourcePreview struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at,omitzero"`
}

func (s *Service) WithSourceProbe(probe SourceProbe) *Service {
	configured := *s
	configured.probe = probe
	return &configured
}

func (s *Service) ProbeSource(ctx context.Context, id int64) ([]SourcePreview, error) {
	source, err := s.store.GetSource(ctx, id)
	if err != nil {
		return nil, err
	}
	if source.Status != model.SourceCandidate && source.Status != model.SourceVerified {
		return nil, model.ErrConflict
	}
	if s.probe == nil {
		return nil, ErrProbeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	materials, err := s.probe(ctx, source)
	if err != nil {
		return nil, ErrProbeFailed
	}
	const limit = 5
	previews := make([]SourcePreview, 0, min(limit, len(materials)))
	for _, material := range materials {
		if len(material.URL) > 2048 || len(material.Title) > 4096 || material.URL == "" || material.Title == "" {
			continue
		}
		previews = append(previews, SourcePreview{
			Title: material.Title, URL: material.URL, PublishedAt: material.PublishedAt,
		})
		if len(previews) == limit {
			break
		}
	}
	return previews, nil
}
