package store_test

import (
	"errors"
	"testing"
	"time"

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

func TestCalendarEventsRoundTripAndDedupeOnTitleAndStart(t *testing.T) {
	s := open(t)
	notification := insert(t, s, sample(nil))
	start := t0.Add(time.Hour)
	event := store.CalendarEvent{Title: "Weekly Sync", Start: start, AlertAt: start.Add(-time.Minute), SourceNotificationID: notification}

	id, inserted, err := s.AddCalendarEvent(event)
	if err != nil || !inserted || id == 0 {
		t.Fatalf("AddCalendarEvent = %d, %v, %v", id, inserted, err)
	}
	got, err := s.GetCalendarEvent(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Weekly Sync" || !got.Start.Equal(start) || !got.AlertAt.Equal(start.Add(-time.Minute)) || got.Fired || got.SourceNotificationID != notification {
		t.Errorf("event = %+v", got)
	}

	tests := []struct {
		name         string
		mutate       func(*store.CalendarEvent)
		wantInserted bool
	}{
		{"the same meeting again", func(e *store.CalendarEvent) {}, false},
		{"the same meeting with a different alert time", func(e *store.CalendarEvent) { e.AlertAt = e.AlertAt.Add(time.Minute) }, false},
		{"the same title at another time", func(e *store.CalendarEvent) {
			e.Start = e.Start.Add(24 * time.Hour)
			e.AlertAt = e.AlertAt.Add(24 * time.Hour)
		}, true},
		{"another meeting at the same time", func(e *store.CalendarEvent) { e.Title = "Design review" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			again := event
			tt.mutate(&again)
			if _, inserted, err := s.AddCalendarEvent(again); err != nil || inserted != tt.wantInserted {
				t.Fatalf("inserted=%v err=%v, want inserted=%v", inserted, err, tt.wantInserted)
			}
		})
	}
}

func TestPendingCalendarEventsAreOrderedAndExcludeFiredOnes(t *testing.T) {
	s := open(t)
	add := func(title string, minutes int) int64 {
		start := t0.Add(time.Duration(minutes) * time.Minute)
		id, _, err := s.AddCalendarEvent(store.CalendarEvent{Title: title, Start: start, AlertAt: start.Add(-time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	late, early, middle := add("late", 90), add("early", 10), add("middle", 40)
	if _, err := s.MarkCalendarEventFired(middle); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingCalendarEvents()
	if err != nil || len(pending) != 2 || pending[0].ID != early || pending[1].ID != late {
		t.Fatalf("pending = %+v, %v; want early then late, without the fired middle one", pending, err)
	}
}

func TestAnEventIsMarkedFiredExactlyOnce(t *testing.T) {
	s := open(t)
	id, _, _ := s.AddCalendarEvent(store.CalendarEvent{Title: "Sync", Start: t0, AlertAt: t0})
	first, err := s.MarkCalendarEventFired(id)
	second, err2 := s.MarkCalendarEventFired(id)
	if err != nil || err2 != nil || !first || second {
		t.Fatalf("first=%v (%v) second=%v (%v), want true then false", first, err, second, err2)
	}
	if got, _ := s.GetCalendarEvent(id); !got.Fired {
		t.Error("the event is not recorded as fired")
	}
	if _, err := s.MarkCalendarEventFired(999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown event: err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetCalendarEvent(999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown event: err = %v, want ErrNotFound", err)
	}
}
