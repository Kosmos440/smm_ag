package channels

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
	"unicode/utf8"

	"smmagent/internal/ingest"
	"smmagent/internal/ingest/telegram"
	"smmagent/internal/model"
)

type ValidationError struct {
	Field   string
	Problem string
}

func (e *ValidationError) Error() string {
	return e.Field + " " + e.Problem + "."
}

func invalid(field, problem string) error {
	return &ValidationError{Field: field, Problem: problem}
}

var telegramName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

func (in ChannelInput) validate() (model.Channel, error) {
	if err := normalizeText("name", &in.Name, 120); err != nil {
		return model.Channel{}, err
	}
	in.TelegramChatID = strings.TrimSpace(in.TelegramChatID)
	if in.TelegramChatID != "" {
		if strings.HasPrefix(in.TelegramChatID, "@") {
			if !telegramName.MatchString(in.TelegramChatID[1:]) {
				return model.Channel{}, invalid("telegram_chat_id", "must be an @username or a numeric chat ID")
			}
			in.TelegramChatID = strings.ToLower(in.TelegramChatID)
		} else {
			id, err := strconv.ParseInt(in.TelegramChatID, 10, 64)
			if err != nil || id == 0 {
				return model.Channel{}, invalid("telegram_chat_id", "must be an @username or a numeric chat ID")
			}
			in.TelegramChatID = strconv.FormatInt(id, 10)
		}
	}
	if !in.Website && in.TelegramChatID == "" {
		return model.Channel{}, invalid("website", "or telegram_chat_id is required")
	}
	if err := normalizeSettings(&in.Settings); err != nil {
		return model.Channel{}, err
	}
	return model.Channel{
		Name: in.Name, TelegramChatID: in.TelegramChatID, Website: in.Website,
		Enabled: in.Enabled, Settings: in.Settings,
	}, nil
}

func (in SourceInput) validate() (model.Source, error) {
	if err := normalizeText("name", &in.Name, 120); err != nil {
		return model.Source{}, err
	}
	if err := normalizeText("organization", &in.Organization, 120); err != nil {
		return model.Source{}, err
	}
	in.URL = strings.TrimSpace(in.URL)
	if len(in.URL) > 2048 || strings.Contains(in.URL, "#") {
		return model.Source{}, invalid("url", "must have at most 2048 bytes and no fragment")
	}
	switch in.Kind {
	case model.SourceRSS, model.SourceWebsite:
		u, err := ingest.ParseURL(in.URL)
		if err != nil {
			return model.Source{}, invalid("url", "must be a public HTTP(S) URL on its default port without credentials")
		}
		u.Host = strings.ToLower(u.Host)
		in.URL = u.String()
	case model.SourceTelegram:
		preview, err := telegram.PreviewURL(in.URL)
		if err != nil {
			return model.Source{}, invalid("url", "must be a public https://t.me/s/channel URL")
		}
		in.URL = preview
	default:
		return model.Source{}, invalid("kind", "must be rss, website, or telegram")
	}
	if in.Status == "" {
		in.Status = model.SourceCandidate
	}
	switch in.Status {
	case model.SourceCandidate, model.SourceVerified, model.SourceDisabled:
	default:
		return model.Source{}, invalid("status", "must be candidate, verified, or disabled")
	}
	if err := normalizeTerms("topics", &in.Topics); err != nil {
		return model.Source{}, err
	}
	return model.Source{
		Name: in.Name, Organization: in.Organization, URL: in.URL, Kind: in.Kind,
		Status: in.Status, Topics: in.Topics, Primary: in.Primary,
	}, nil
}

func (in PresetInput) validate() (model.Preset, error) {
	if err := normalizeText("name", &in.Name, 120); err != nil {
		return model.Preset{}, err
	}
	if err := normalizeSettings(&in.Settings); err != nil {
		return model.Preset{}, err
	}
	return model.Preset{Name: in.Name, Settings: in.Settings}, nil
}

func normalizeSettings(s *model.Settings) error {
	if err := normalizeText("settings.topic", &s.Topic, 200); err != nil {
		return err
	}
	if err := normalizeText("settings.audience", &s.Audience, 300); err != nil {
		return err
	}
	if err := normalizeTerms("settings.keywords", &s.Keywords); err != nil {
		return err
	}
	if err := normalizeTerms("settings.blocked_terms", &s.BlockedTerms); err != nil {
		return err
	}
	if err := normalizeProfile(s.TopicProfile); err != nil {
		return err
	}
	s.TimeZone = strings.TrimSpace(s.TimeZone)
	if s.TimeZone == "" {
		s.TimeZone = "UTC"
	}
	if _, err := time.LoadLocation(s.TimeZone); err != nil || s.TimeZone == "Local" {
		return invalid("settings.time_zone", "must be an IANA time zone such as UTC or Europe/Moscow")
	}
	if s.MaxAgeMinutes == 0 {
		s.MaxAgeMinutes = 1440
	}
	const minutesInWeek = 7 * 24 * 60
	if s.MaxAgeMinutes < 1 || s.MaxAgeMinutes > 52*minutesInWeek {
		return invalid("settings.max_age_minutes", "must be between 1 and 524160")
	}
	if s.ReserveAfterMinutes == 0 {
		s.ReserveAfterMinutes = 30
	}
	if s.ReserveAfterMinutes < 1 || s.ReserveAfterMinutes > minutesInWeek {
		return invalid("settings.reserve_after_minutes", "must be between 1 and 10080")
	}
	if s.MinIntervalMinutes < 0 || s.MinIntervalMinutes > minutesInWeek {
		return invalid("settings.min_interval_minutes", "must be between 0 and 10080")
	}
	if (s.QuietStartHour == nil) != (s.QuietEndHour == nil) {
		return invalid("settings quiet hours", "must include both quiet_start_hour and quiet_end_hour")
	}
	if s.QuietStartHour != nil {
		start, end := *s.QuietStartHour, *s.QuietEndHour
		if start < 0 || start > 23 || end < 0 || end > 23 || start == end {
			return invalid("settings quiet hours", "must be distinct whole hours between 0 and 23")
		}
	}
	return nil
}

func normalizeProfile(profile *model.TopicProfile) error {
	if profile == nil {
		return nil
	}
	const field = "settings.topic_profile"
	profile.Description = strings.Join(strings.Fields(profile.Description), " ")
	if utf8.RuneCountInString(profile.Description) > 1000 || len(profile.Subtopics) > 16 {
		return invalid(field, "allows a description of up to 1000 characters and at most 16 subtopics")
	}
	names := map[string]bool{}
	for i := range profile.Subtopics {
		subtopic := &profile.Subtopics[i]
		if err := normalizeText(field+".subtopics.name", &subtopic.Name, 100); err != nil {
			return err
		}
		key := strings.ToLower(subtopic.Name)
		if names[key] {
			return invalid(field+".subtopics", "must have distinct names")
		}
		names[key] = true
		if err := normalizeTerms(field+".subtopics.keywords", &subtopic.Keywords); err != nil {
			return err
		}
		if len(subtopic.Keywords) > 15 {
			return invalid(field+".subtopics.keywords", "must contain at most 15 terms per subtopic")
		}
	}
	if err := normalizeTerms(field+".context_terms", &profile.ContextTerms); err != nil {
		return err
	}
	for _, examples := range [][]string{profile.PositiveExamples, profile.NegativeExamples} {
		if len(examples) > 12 {
			return invalid(field, "allows at most 12 examples of each kind")
		}
		for i := range examples {
			examples[i] = strings.Join(strings.Fields(examples[i]), " ")
			if err := normalizeText(field+".examples", &examples[i], 500); err != nil {
				return err
			}
		}
	}
	for _, threshold := range []*float64{profile.VectorAccept, profile.VectorReject} {
		if threshold != nil && (math.IsNaN(*threshold) || *threshold < -1 || *threshold > 1) {
			return invalid(field, "vector thresholds must be between -1 and 1")
		}
	}
	if profile.VectorAccept != nil && profile.VectorReject != nil && *profile.VectorReject >= *profile.VectorAccept {
		return invalid(field, "vector_reject must be less than vector_accept")
	}
	return nil
}

func normalizeText(field string, value *string, limit int) error {
	*value = strings.TrimSpace(*value)
	if *value == "" {
		return invalid(field, "is required")
	}
	if utf8.RuneCountInString(*value) > limit || strings.ContainsFunc(*value, unicode.IsControl) {
		return invalid(field, fmt.Sprintf("must contain at most %d characters and no control characters", limit))
	}
	return nil
}

func normalizeTerms(field string, terms *[]string) error {
	if len(*terms) > 50 {
		return invalid(field, "must contain at most 50 terms")
	}
	unique := make(map[string]struct{}, len(*terms))
	normalized := make([]string, 0, len(*terms))
	for _, term := range *terms {
		if err := normalizeText(field, &term, 100); err != nil {
			return err
		}
		key := strings.ToLower(term)
		if _, exists := unique[key]; !exists {
			unique[key] = struct{}{}
			normalized = append(normalized, term)
		}
	}
	*terms = normalized
	return nil
}
