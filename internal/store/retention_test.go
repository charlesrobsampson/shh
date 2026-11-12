package store_test

import (
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

func TestPurgeExpiredKeepsWhatIsStillToReviewForLonger(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	threshold := 65
	n := 0
	add := func(name string, status store.Status, ageDays int, flagged, trained bool) int64 {
		n++
		id, _, err := s.InsertNotification(store.Notification{Source: "Teams", Sender: name, Title: name, Body: name, Timestamp: now.Add(-time.Duration(ageDays) * day),
			Status: status, SourceRecID: int64(n), ThresholdAtDecision: &threshold})
		if err != nil {
			t.Fatal(err)
		}
		if flagged {
			s.SetFlagged(id, true)
		}
		if trained {
			s.Train(id, store.Training{Feedback: "y"})
		}
		return id
	}
	fresh := add("fresh", store.StatusBlocked, 5, false, false)
	oldAlerted := add("old alerted, never reviewed", store.StatusAlerted, 40, false, false)
	oldBlocked := add("old blocked, untrained", store.StatusBlocked, 40, false, false)
	oldTrained := add("old blocked, trained", store.StatusBlocked, 40, false, true)
	oldFlagged := add("old alerted but flagged", store.StatusAlerted, 40, true, false)
	ancient := add("ancient blocked, untrained", store.StatusBlocked, 100, false, false)
	r := store.Retention{TTL: 30 * day, KeepUntrained: 90 * day}

	if got, _ := s.CountExpired(now, r); got != 3 {
		t.Errorf("CountExpired = %d, want 3", got)
	}
	if got, err := s.PurgeExpired(now, r); err != nil || got != 3 {
		t.Fatalf("PurgeExpired = %d, %v", got, err)
	}
	for id, want := range map[int64]bool{fresh: true, oldAlerted: false, oldBlocked: true, oldTrained: false, oldFlagged: true, ancient: false} {
		if _, err := s.GetNotification(id); (err == nil) != want {
			t.Errorf("notification %d kept = %v, want %v", id, err == nil, want)
		}
	}
}

func TestPurgeExpiredWithNoExtensionTakesEverythingPastTTL(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	threshold := 65
	s.InsertNotification(store.Notification{Source: "T", Sender: "a", Body: "x", Timestamp: now.Add(-48 * time.Hour), Status: store.StatusBlocked, SourceRecID: 1, ThresholdAtDecision: &threshold})
	if got, _ := s.PurgeExpired(now, store.Retention{TTL: 24 * time.Hour}); got != 1 {
		t.Errorf("purged %d", got)
	}
}

func TestPurgeAllAndUnreviewedBeforeAndAutoTrainedMark(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	threshold := 65
	ids := map[string]int64{}
	for i, c := range []struct {
		name    string
		age     time.Duration
		flagged bool
	}{{"old", 40 * 24 * time.Hour, false}, {"old flagged", 40 * 24 * time.Hour, true}, {"new", time.Hour, false}} {
		id, _, _ := s.InsertNotification(store.Notification{Source: "T", Sender: c.name, Body: c.name, Timestamp: now.Add(-c.age), Status: store.StatusBlocked, SourceRecID: int64(i + 1), ThresholdAtDecision: &threshold})
		if c.flagged {
			s.SetFlagged(id, true)
		}
		ids[c.name] = id
	}
	rows, err := s.UnreviewedBefore(now.Add(-30*24*time.Hour), 0)
	if err != nil || len(rows) != 1 || rows[0].ID != ids["old"] {
		t.Fatalf("UnreviewedBefore = %+v, %v (a flagged one is for the user to review; a new one is not due)", rows, err)
	}
	s.Train(ids["old"], store.Training{Feedback: "n"})
	if err := s.MarkAutoTrained(ids["old"]); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.GetNotification(ids["old"]); !n.AutoTrained || !n.IsTrained {
		t.Errorf("%+v", n)
	}
	s.Untrain(ids["old"])
	if n, _ := s.GetNotification(ids["old"]); n.AutoTrained {
		t.Error("untraining takes the auto mark away")
	}
	s.AddUnparsedCalendar(store.UnparsedCalendar{RawText: "x"})
	if got, err := s.PurgeAll(); err != nil || got != 3 {
		t.Errorf("PurgeAll = %d, %v", got, err)
	}
	if left, _ := s.CountAll(); left != 0 {
		t.Errorf("%d left", left)
	}
	if rows, _ := s.UnparsedCalendar(); len(rows) != 0 {
		t.Error("the unreadable calendar entries go with the notifications")
	}
}
