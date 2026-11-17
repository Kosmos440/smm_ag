package publishing

import (
	"context"
	"time"

	"smmagent/internal/model"
)

// Publisher delivers one prepared revision and returns its destination-specific ID.
type Publisher interface {
	Send(ctx context.Context, post model.Publication, previousID string) (string, error)
}

type ResultError struct {
	Message   string
	Uncertain bool
}

func (e *ResultError) Error() string { return e.Message }

// Window specifies whether urgent posts or reserve posts can be sent at this tick.
func Window(settings model.Settings, now, lastSent time.Time) (urgent, reserve bool) {
	location := time.UTC
	if settings.TimeZone != "" {
		if configured, err := time.LoadLocation(settings.TimeZone); err == nil {
			location = configured
		}
	}
	quiet := false
	if settings.QuietStartHour != nil && settings.QuietEndHour != nil {
		start, end := *settings.QuietStartHour, *settings.QuietEndHour
		hour := now.In(location).Hour()
		if start < end {
			quiet = hour >= start && hour < end
		} else {
			quiet = hour >= start || hour < end
		}
	}
	waitMinutes := max(settings.ReserveAfterMinutes, settings.MinIntervalMinutes)
	return !quiet || settings.UrgentOutsideHours,
		!quiet && (lastSent.IsZero() || !now.Before(lastSent.Add(time.Duration(waitMinutes)*time.Minute)))
}
