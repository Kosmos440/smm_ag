package channels

import (
	"context"
	"errors"
	"smmagent/internal/model"
	"strings"
	"time"
)

var (
	ErrFilterGeneratorUnavailable = errors.New("filter generator unavailable")
	ErrFilterGenerationFailed     = errors.New("filter generation failed")
)

type FilterRequest struct {
	Topic      string `json:"topic"`
	Audience   string `json:"audience"`
	Exclusions string `json:"exclusions"`
}

type FilterSuggestion struct {
	TopicProfile *model.TopicProfile `json:"topic_profile,omitempty"`
	Keywords     []string            `json:"keywords"`
	BlockedTerms []string            `json:"blocked_terms"`
}

type FilterGenerator func(context.Context, FilterRequest) (FilterSuggestion, error)

func (s *Service) WithFilterGenerator(generate FilterGenerator) *Service {
	configured := *s
	configured.generateFilters = generate
	return &configured
}

func (s *Service) SuggestFilters(ctx context.Context, input FilterRequest) (FilterSuggestion, error) {
	if err := normalizeText("topic", &input.Topic, 200); err != nil {
		return FilterSuggestion{}, err
	}
	if err := normalizeText("audience", &input.Audience, 300); err != nil {
		return FilterSuggestion{}, err
	}
	input.Exclusions = strings.Join(strings.Fields(input.Exclusions), " ")
	if input.Exclusions != "" {
		if err := normalizeText("exclusions", &input.Exclusions, 1000); err != nil {
			return FilterSuggestion{}, err
		}
	}
	if s.generateFilters == nil {
		return FilterSuggestion{}, ErrFilterGeneratorUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := s.generateFilters(ctx, input)
	if err != nil || len(result.Keywords) == 0 {
		return FilterSuggestion{}, ErrFilterGenerationFailed
	}
	if input.Exclusions == "" {
		result.BlockedTerms = nil
	}
	if normalizeTerms("keywords", &result.Keywords) != nil || normalizeTerms("blocked_terms", &result.BlockedTerms) != nil {
		return FilterSuggestion{}, ErrFilterGenerationFailed
	}
	if result.TopicProfile != nil {
		result.TopicProfile.UseVectors, result.TopicProfile.UseLLM = nil, nil
		result.TopicProfile.VectorAccept, result.TopicProfile.VectorReject = nil, nil
		if normalizeProfile(result.TopicProfile) != nil {
			return FilterSuggestion{}, ErrFilterGenerationFailed
		}
	}
	seen := make(map[string]bool, len(result.Keywords))
	for _, keyword := range result.Keywords {
		if strings.Contains(keyword, ",") {
			return FilterSuggestion{}, ErrFilterGenerationFailed
		}
		seen[strings.ToLower(keyword)] = true
	}
	for _, term := range result.BlockedTerms {
		if strings.Contains(term, ",") || seen[strings.ToLower(term)] {
			return FilterSuggestion{}, ErrFilterGenerationFailed
		}
	}
	return result, nil
}
