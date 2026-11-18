package model

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("resource conflict")
)

type SourceKind string

const (
	SourceRSS      SourceKind = "rss"
	SourceWebsite  SourceKind = "website"
	SourceTelegram SourceKind = "telegram"
)

type SourceStatus string

const (
	SourceCandidate SourceStatus = "candidate"
	SourceVerified  SourceStatus = "verified"
	SourceDisabled  SourceStatus = "disabled"
)

type Source struct {
	ID           int64        `json:"id"`
	Name         string       `json:"name"`
	Organization string       `json:"organization"`
	URL          string       `json:"url"`
	Kind         SourceKind   `json:"kind"`
	Status       SourceStatus `json:"status"`
	Topics       []string     `json:"topics"`
	Primary      bool         `json:"primary"`
	LastSuccess  time.Time    `json:"last_success,omitzero"`
	LastError    string       `json:"last_error,omitempty"`
}

type Material struct {
	Media        []Media
	ID           int64
	SourceID     int64
	ExternalID   string
	URL          string
	Title        string
	Body         string
	Organization string
	Primary      bool
	PublishedAt  time.Time
	FetchedAt    time.Time
}

// Media identifies an original attachment exposed by the source.
type Media struct {
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

type SourcedMaterial struct {
	Material Material
	Source   Source
}

type Settings struct {
	Topic               string   `json:"topic"`
	Audience            string   `json:"audience"`
	Keywords            []string `json:"keywords"`
	BlockedTerms        []string `json:"blocked_terms"`
	MaxAgeMinutes       int      `json:"max_age_minutes"`
	ReserveAfterMinutes int      `json:"reserve_after_minutes"`
	MinIntervalMinutes  int      `json:"min_interval_minutes"`
	TimeZone            string   `json:"time_zone"`
	QuietStartHour      *int     `json:"quiet_start_hour,omitempty"`
	QuietEndHour        *int     `json:"quiet_end_hour,omitempty"`
	UrgentOutsideHours  bool     `json:"urgent_outside_hours"`
	RequireApproval     bool     `json:"require_approval"`
}

type Channel struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	TelegramChatID string   `json:"telegram_chat_id,omitempty"`
	Website        bool     `json:"website"`
	Enabled        bool     `json:"enabled"`
	Settings       Settings `json:"settings"`
}

type Preset struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Settings Settings `json:"settings"`
}

type Fact struct {
	Key          string `json:"key"`
	Value        string `json:"value"`
	URL          string `json:"url"`
	Organization string `json:"organization"`
}

type Event struct {
	ID        int64     `json:"id"`
	ChannelID int64     `json:"channel_id"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Facts     []Fact    `json:"facts"`
	Version   int       `json:"version"`
	FirstSeen time.Time `json:"first_seen"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Evaluation struct {
	Event    Event
	Accepted bool
	Changed  bool
	Urgent   bool
	Priority int
	Reason   string
}

type Target string

const (
	TargetTelegram Target = "telegram"
	TargetWebsite  Target = "website"
)

type PublicationState string

const (
	PublicationReview    PublicationState = "review"
	PublicationPending   PublicationState = "pending"
	PublicationSending   PublicationState = "sending"
	PublicationSent      PublicationState = "sent"
	PublicationRejected  PublicationState = "rejected"
	PublicationExpired   PublicationState = "expired"
	PublicationUncertain PublicationState = "uncertain"
	PublicationFailed    PublicationState = "failed"
)

type Publication struct {
	Media       []Media          `json:"-"`
	ChannelName string           `json:"-"`
	ChannelURL  string           `json:"-"`
	ID          int64            `json:"id"`
	ChannelID   int64            `json:"channel_id"`
	EventID     int64            `json:"event_id"`
	Version     int              `json:"version"`
	Target      Target           `json:"target"`
	Destination string           `json:"destination,omitempty"`
	State       PublicationState `json:"state"`
	Priority    int              `json:"priority"`
	Urgent      bool             `json:"urgent"`
	Title       string           `json:"title"`
	Body        string           `json:"body"`
	SourceURL   string           `json:"source_url"`
	Reason      string           `json:"reason"`
	ExternalID  string           `json:"external_id,omitempty"`
	LastError   string           `json:"last_error,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	ExpiresAt   time.Time        `json:"expires_at"`
	SentAt      time.Time        `json:"sent_at,omitzero"`
}
