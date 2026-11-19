package publishing

import (
	"testing"
	"time"
	_ "time/tzdata"

	"smmagent/internal/model"
)

func TestUrgentBypassesVariableReserveWindow(t *testing.T) {
	settings := model.Settings{MinIntervalMinutes: 10, MaxIntervalMinutes: 25, ReserveAfterMinutes: 1}
	last := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	gaps := map[int]bool{}
	for i := 0; i < 64; i++ {
		last = last.Add(time.Second)
		opened := false
		for m := 0; m <= 25; m++ {
			urgent, reserve := Window(settings, last.Add(time.Duration(m)*time.Minute), last)
			if !urgent || (reserve && m < 10) || (!reserve && (opened || m == 25)) {
				t.Fatalf("invalid window at minute %d after %v", m, last)
			}
			if reserve && !opened {
				gaps[m] = true
			}
			opened = reserve
		}
	}
	if len(gaps) < 2 {
		t.Fatal("expected varying gaps across deliveries")
	}
}

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
