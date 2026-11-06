package store_test

import (
	"testing"

	"github.com/charlesrobsampson/shh/internal/store"
)

func TestUnparsedCalendarRowsAreRecordedAndListedNewestFirst(t *testing.T) {
	s := open(t)
	id := insert(t, s, sample(nil))
	entries := []store.UnparsedCalendar{
		{NotificationID: id, Category: "OLAlertsNotificationReminders", Title: "Sync", Subtitle: "Teams Meeting", RawText: "sometime tomorrow-ish"},
		{Title: "No notification", RawText: "after lunch"},
	}
	for _, e := range entries {
		if err := s.AddUnparsedCalendar(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.UnparsedCalendar()
	if err != nil || len(got) != 2 {
		t.Fatalf("UnparsedCalendar = %+v, %v", got, err)
	}
	if got[0].Title != "No notification" || got[0].NotificationID != 0 || got[0].ID == 0 || got[0].CreatedAt.IsZero() {
		t.Errorf("newest = %+v, want the second entry first, with no notification", got[0])
	}
	older := got[1]
	if older.NotificationID != id || older.Category != "OLAlertsNotificationReminders" || older.Subtitle != "Teams Meeting" || older.RawText != "sometime tomorrow-ish" {
		t.Errorf("older = %+v", older)
	}
}

func TestUnparsedCalendarRequiresTheText(t *testing.T) {
	if err := open(t).AddUnparsedCalendar(store.UnparsedCalendar{Title: "x"}); err == nil {
		t.Fatal("an entry with no raw text was accepted")
	}
}
