package publishing

import (
	"testing"
	"time"
	_ "time/tzdata"

	"smmagent/internal/model"
)

func TestWindowQuietHoursAndReserve(t *testing.T) {
	start, end := 23, 7
	settings := model.Settings{
		TimeZone: "Europe/Moscow", QuietStartHour: &start, QuietEndHour: &end,
		ReserveAfterMinutes: 30, MinIntervalMinutes: 45,
	}
	now := time.Date(2026, 11, 17, 21, 0, 0, 0, time.UTC)
	if urgent, reserve := Window(settings, now, time.Time{}); urgent || reserve {
		t.Fatal("quiet hours must block both categories by default")
	}
	settings.UrgentOutsideHours = true
	if urgent, reserve := Window(settings, now, time.Time{}); !urgent || reserve {
		t.Fatal("the night override must apply only to urgent posts")
	}
	now = time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC)
	if urgent, reserve := Window(settings, now, now.Add(-44*time.Minute)); !urgent || reserve {
		t.Fatal("the reserve must respect both the gap and the minimum interval")
	}
	if urgent, reserve := Window(settings, now, now.Add(-45*time.Minute)); !urgent || !reserve {
		t.Fatal("the reserve must reopen when the configured interval elapses")
	}
}
