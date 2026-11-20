package channels

import (
	"context"
	"time"

	"smmagent/internal/model"
)

type TopicPreview func(context.Context, model.Channel, int64) (model.TopicDecision, error)

func (s *Service) WithTopicPreview(preview TopicPreview) *Service {
	configured := *s
	configured.previewTopic = preview
	return &configured
}

func (s *Service) PreviewTopic(ctx context.Context, channelID, materialID int64) (model.TopicDecision, error) {
	channel, err := s.GetChannel(ctx, channelID)
	if err != nil {
		return model.TopicDecision{}, err
	}
	if s.previewTopic == nil {
		return model.TopicDecision{}, ErrFilterGeneratorUnavailable
	}
	previewCtx, cancel := context.WithTimeout(ctx, 14*time.Second)
	defer cancel()
	decision, err := s.previewTopic(previewCtx, channel, materialID)
	if err != nil {
		return model.TopicDecision{}, err
	}
	return decision, s.store.SaveTopicPreview(ctx, channelID, materialID, decision)
}

func (s *Service) ReviewTopic(ctx context.Context, channelID, materialID int64, verdict model.TopicVerdict) error {
	switch verdict {
	case model.TopicRelevant, model.TopicIrrelevant, model.TopicInsufficient:
		return s.store.ReviewTopic(ctx, channelID, materialID, verdict)
	default:
		return invalid("verdict", "must be relevant, irrelevant, or insufficient")
	}
}
