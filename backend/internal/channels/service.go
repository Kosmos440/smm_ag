// Package channels exposes the management operations shared by the HTTP
// transport and future control interfaces.
package channels

import (
	"context"
	"errors"
	"time"

	"smmagent/internal/model"
)

type Queries interface {
	ListChannels(context.Context, int, int) ([]model.Channel, error)
	GetChannel(context.Context, int64) (model.Channel, error)
	ListSources(context.Context, int, int) ([]model.Source, error)
	GetSource(context.Context, int64) (model.Source, error)
	ListPresets(context.Context, int, int) ([]model.Preset, error)
	GetPreset(context.Context, int64) (model.Preset, error)
	ListEvents(context.Context, int64, int, int) ([]model.Event, error)
	ListTopics(context.Context, int64, int, int) ([]model.TopicRecord, error)
	ListPublications(context.Context, int64, int, int) ([]model.Publication, error)
	ListReview(context.Context, int, int) ([]model.Publication, error)
	ListSiteNews(context.Context, int64, int, int) ([]model.Publication, error)
}

type Store interface {
	Queries
	CreateChannel(context.Context, model.Channel) (model.Channel, error)
	UpdateChannel(context.Context, int64, model.Channel) (model.Channel, error)
	CreateSource(context.Context, model.Source) (model.Source, error)
	UpdateSource(context.Context, int64, model.Source) (model.Source, error)
	CreatePreset(context.Context, model.Preset) (model.Preset, error)
	UpdatePreset(context.Context, int64, model.Preset) (model.Preset, error)
	DecideReview(context.Context, int64, bool) (model.Publication, error)
	ReviewTopic(context.Context, int64, int64, model.TopicVerdict) error
	SaveTopicPreview(context.Context, int64, int64, model.TopicDecision) error
}

type Service struct {
	Queries
	store           Store
	probe           SourceProbe
	generateFilters FilterGenerator
	previewTopic    TopicPreview
}

func New(store Store) *Service {
	return &Service{Queries: store, store: store}
}

type ChannelInput struct {
	Name           string         `json:"name"`
	TelegramChatID string         `json:"telegram_chat_id"`
	Website        bool           `json:"website"`
	Enabled        bool           `json:"enabled"`
	Settings       model.Settings `json:"settings"`
}

type SourceInput struct {
	Name         string             `json:"name"`
	Organization string             `json:"organization"`
	URL          string             `json:"url"`
	Kind         model.SourceKind   `json:"kind"`
	Status       model.SourceStatus `json:"status"`
	Topics       []string           `json:"topics"`
	Primary      bool               `json:"primary"`
}

type PresetInput struct {
	Name     string         `json:"name"`
	Settings model.Settings `json:"settings"`
}

type SitePost struct {
	ID          int64     `json:"id"`
	ChannelID   int64     `json:"channel_id"`
	EventID     int64     `json:"event_id"`
	Version     int       `json:"version"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	SourceURL   string    `json:"source_url"`
	PublishedAt time.Time `json:"published_at"`
}

func (s *Service) CreateChannel(ctx context.Context, input ChannelInput) (model.Channel, error) {
	channel, err := input.validate()
	if err != nil {
		return model.Channel{}, err
	}
	return s.store.CreateChannel(ctx, channel)
}

func (s *Service) UpdateChannel(ctx context.Context, id int64, input ChannelInput) (model.Channel, error) {
	channel, err := input.validate()
	if err != nil {
		return model.Channel{}, err
	}
	return s.store.UpdateChannel(ctx, id, channel)
}

func (s *Service) CreateSource(ctx context.Context, input SourceInput) (model.Source, error) {
	source, err := input.validate()
	if err != nil {
		return model.Source{}, err
	}
	return s.store.CreateSource(ctx, source)
}

func (s *Service) UpdateSource(ctx context.Context, id int64, input SourceInput) (model.Source, error) {
	source, err := input.validate()
	if err != nil {
		return model.Source{}, err
	}
	return s.store.UpdateSource(ctx, id, source)
}

func (s *Service) CreatePreset(ctx context.Context, input PresetInput) (model.Preset, error) {
	preset, err := input.validate()
	if err != nil {
		return model.Preset{}, err
	}
	return s.store.CreatePreset(ctx, preset)
}

func (s *Service) UpdatePreset(ctx context.Context, id int64, input PresetInput) (model.Preset, error) {
	preset, err := input.validate()
	if err != nil {
		return model.Preset{}, err
	}
	return s.store.UpdatePreset(ctx, id, preset)
}

func (s *Service) Review(ctx context.Context, id int64, decision string) (model.Publication, error) {
	switch decision {
	case "approve":
		return s.store.DecideReview(ctx, id, true)
	case "reject":
		return s.store.DecideReview(ctx, id, false)
	default:
		return model.Publication{}, invalid("decision", "must be approve or reject")
	}
}

func (s *Service) SiteNews(ctx context.Context, channelID int64, limit, offset int) ([]SitePost, error) {
	publications, err := s.store.ListSiteNews(ctx, channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	posts := make([]SitePost, 0, len(publications))
	for _, publication := range publications {
		if publication.State != model.PublicationSent || publication.Target != model.TargetWebsite {
			return nil, errors.New("site news query returned an unpublished or non-website item")
		}
		posts = append(posts, SitePost{
			ID: publication.ID, ChannelID: publication.ChannelID, EventID: publication.EventID,
			Version: publication.Version, Title: publication.Title, Body: publication.Body,
			SourceURL: publication.SourceURL, PublishedAt: publication.SentAt,
		})
	}
	return posts, nil
}
