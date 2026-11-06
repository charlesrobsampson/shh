package store_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

func baseline(percent int) store.Level {
	return store.Level{Kind: store.LevelBaseline, Percent: percent, StartTime: t0}
}

func timed(percent int, minutes int, at time.Time) store.Level {
	expires := at.Add(time.Duration(minutes) * time.Minute)
	return store.Level{Kind: store.LevelEscalation, Percent: percent, StartTime: at, TargetMinutes: minutes, ExpiresAt: &expires}
}

func indefinite(percent int, at time.Time) store.Level {
	return store.Level{Kind: store.LevelEscalation, Percent: percent, StartTime: at}
}

func push(t *testing.T, s *store.Store, l store.Level) int64 {
	t.Helper()
	id, err := s.PushLevel(l)
	if err != nil {
		t.Fatalf("PushLevel: %v", err)
	}
	return id
}

func activeIDs(t *testing.T, s *store.Store) []int64 {
	t.Helper()
	levels, err := s.ActiveLevels()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int64, len(levels))
	for i, l := range levels {
		out[i] = l.ID
	}
	return out
}

func sameInts(got []int64, want ...int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestActiveLevelsListTheStackBottomFirst(t *testing.T) {
	s := open(t)
	base := push(t, s, baseline(30))
	first := push(t, s, timed(90, 50, t0))
	second := push(t, s, indefinite(75, t0.Add(time.Minute)))

	if got := activeIDs(t, s); !sameInts(got, base, first, second) {
		t.Fatalf("stack = %v, want [%d %d %d]", got, base, first, second)
	}
	levels, _ := s.ActiveLevels()
	if levels[1].ParentID != base || levels[2].ParentID != first || levels[0].ParentID != 0 {
		t.Errorf("parents = %d %d %d, want 0, baseline, first escalation", levels[0].ParentID, levels[1].ParentID, levels[2].ParentID)
	}
}

func TestANewBaselineStaysAtTheBottom(t *testing.T) {
	s := open(t)
	oldBase := push(t, s, baseline(30))
	escalation := push(t, s, timed(90, 50, t0))
	if err := s.CloseLevel(oldBase, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	newBase := push(t, s, store.Level{Kind: store.LevelBaseline, Percent: 40, StartTime: t0.Add(2 * time.Minute)})

	if got := activeIDs(t, s); !sameInts(got, newBase, escalation) {
		t.Fatalf("stack = %v, want the new baseline below the escalation", got)
	}
}

func TestOnlyOneBaselineCanBeActive(t *testing.T) {
	s := open(t)
	push(t, s, baseline(30))
	if _, err := s.PushLevel(baseline(40)); err == nil {
		t.Fatal("a second active baseline was accepted")
	}
}

func TestPushValidatesTheLevel(t *testing.T) {
	expires := t0.Add(time.Hour)
	tests := []struct {
		name  string
		level store.Level
	}{
		{"percent 100", store.Level{Kind: store.LevelBaseline, Percent: 100, StartTime: t0}},
		{"negative percent", store.Level{Kind: store.LevelBaseline, Percent: -1, StartTime: t0}},
		{"unknown kind", store.Level{Kind: "sideways", Percent: 30, StartTime: t0}},
		{"missing start time", store.Level{Kind: store.LevelBaseline, Percent: 30}},
		{"timed without an expiry", store.Level{Kind: store.LevelEscalation, Percent: 30, StartTime: t0, TargetMinutes: 60}},
		{"expiry without a duration", store.Level{Kind: store.LevelEscalation, Percent: 30, StartTime: t0, ExpiresAt: &expires}},
		{"then level 100", store.Level{Kind: store.LevelEscalation, Percent: 30, StartTime: t0, ThenLevel: ptr(100)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := open(t).PushLevel(tt.level); err == nil {
				t.Fatal("PushLevel succeeded, want an error")
			}
		})
	}
}

func TestPushKeepsEveryField(t *testing.T) {
	s := open(t)
	expires := t0.Add(50 * time.Minute)
	id := push(t, s, store.Level{
		Kind: store.LevelEscalation, Percent: 90, StartTime: t0, TargetMinutes: 50, ExpiresAt: &expires,
		ThenLevel: ptr(0), Note: "ship the release",
	})
	got, err := s.GetLevel(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != store.LevelEscalation || got.Percent != 90 || got.TargetMinutes != 50 || got.Note != "ship the release" {
		t.Errorf("level = %+v", got)
	}
	if got.ThenLevel == nil || *got.ThenLevel != 0 {
		t.Errorf("ThenLevel = %v, want 0 (off)", got.ThenLevel)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) || !got.StartTime.Equal(t0) {
		t.Errorf("times = start %v expires %v", got.StartTime, got.ExpiresAt)
	}
	if got.EndTime != nil || got.PausedAt != nil || got.RemainingSecs != nil || got.Blocked != 0 || got.Allowed != 0 {
		t.Errorf("a new level must be running with zero counters: %+v", got)
	}
}

func TestPopClosesOnlyTheTopEscalation(t *testing.T) {
	s := open(t)
	base := push(t, s, baseline(30))
	first := push(t, s, timed(90, 50, t0))
	second := push(t, s, indefinite(75, t0))

	popped, err := s.PopLevel(t0.Add(5 * time.Minute))
	if err != nil || popped.ID != second {
		t.Fatalf("PopLevel = %+v, %v; want level %d", popped, err, second)
	}
	if got := activeIDs(t, s); !sameInts(got, base, first) {
		t.Fatalf("stack after one pop = %v, want [%d %d]", got, base, first)
	}
	if _, err := s.PopLevel(t0.Add(6 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PopLevel(t0.Add(7 * time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("popping with only the baseline left: err = %v, want ErrNotFound", err)
	}
	if got := activeIDs(t, s); !sameInts(got, base) {
		t.Fatalf("the baseline must survive pops: %v", got)
	}
}

func TestCloseSetsTheEndTimeAndKeepsHistory(t *testing.T) {
	s := open(t)
	base := push(t, s, baseline(30))
	escalation := push(t, s, timed(90, 50, t0))
	end := t0.Add(50 * time.Minute)

	if err := s.CloseLevel(escalation, end); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetLevel(escalation)
	if err != nil || got.EndTime == nil || !got.EndTime.Equal(end) {
		t.Fatalf("closed level = %+v, %v; want EndTime %v", got, err, end)
	}
	if activeIDs(t, s)[0] != base || len(activeIDs(t, s)) != 1 {
		t.Fatalf("stack = %v, want only the baseline", activeIDs(t, s))
	}
	if err := s.CloseLevel(escalation, end); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("closing twice: err = %v, want ErrNotFound", err)
	}
}

func TestAnExpiredLevelBelowTheTopCanBeClosed(t *testing.T) {
	s := open(t)
	push(t, s, baseline(30))
	lower := push(t, s, timed(60, 10, t0))
	top := push(t, s, timed(90, 60, t0))
	s.CloseLevel(lower, t0.Add(10*time.Minute))

	levels, _ := s.ActiveLevels()
	if levels[len(levels)-1].ID != top {
		t.Fatalf("top = %d, want %d", levels[len(levels)-1].ID, top)
	}
}

func TestPauseStoresTheRemainingTimeAndResumeRestartsIt(t *testing.T) {
	s := open(t)
	id := push(t, s, timed(90, 50, t0))

	pauseAt := t0.Add(20 * time.Minute)
	if err := s.PauseLevel(id, pauseAt); err != nil {
		t.Fatal(err)
	}
	paused, _ := s.GetLevel(id)
	if paused.ExpiresAt != nil || paused.PausedAt == nil || !paused.PausedAt.Equal(pauseAt) || paused.RemainingSecs == nil || *paused.RemainingSecs != 30*60 {
		t.Fatalf("paused level = %+v, want 1800s remaining and no expiry", paused)
	}

	resumeAt := t0.Add(45 * time.Minute)
	if err := s.ResumeLevel(id, resumeAt); err != nil {
		t.Fatal(err)
	}
	resumed, _ := s.GetLevel(id)
	if want := resumeAt.Add(30 * time.Minute); resumed.ExpiresAt == nil || !resumed.ExpiresAt.Equal(want) ||
		resumed.PausedAt != nil || resumed.RemainingSecs != nil {
		t.Fatalf("resumed level = %+v, want expiry %v and no pause fields", resumed, want)
	}
}

func TestPauseRoundsRemainingTimeUpAndNeverGoesNegative(t *testing.T) {
	tests := []struct {
		name    string
		pauseAt time.Duration
		want    int
	}{
		{"fraction of a second is rounded up", 50*time.Minute - 1500*time.Millisecond, 2},
		{"after the deadline there is nothing left", 51 * time.Minute, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t)
			id := push(t, s, timed(90, 50, t0))
			if err := s.PauseLevel(id, t0.Add(tt.pauseAt)); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetLevel(id)
			if got.RemainingSecs == nil || *got.RemainingSecs != tt.want {
				t.Fatalf("RemainingSecs = %v, want %d", got.RemainingSecs, tt.want)
			}
		})
	}
}

func TestPauseAndResumeRejectNonsense(t *testing.T) {
	s := open(t)
	forever := push(t, s, indefinite(75, t0))
	running := push(t, s, timed(90, 50, t0))
	closed := push(t, s, timed(60, 10, t0))
	s.CloseLevel(closed, t0)
	paused := push(t, s, timed(40, 10, t0))
	s.PauseLevel(paused, t0)

	tests := []struct {
		name string
		do   func() error
	}{
		{"pause an indefinite level", func() error { return s.PauseLevel(forever, t0) }},
		{"pause a paused level", func() error { return s.PauseLevel(paused, t0) }},
		{"pause a closed level", func() error { return s.PauseLevel(closed, t0) }},
		{"pause an unknown level", func() error { return s.PauseLevel(999, t0) }},
		{"resume a running level", func() error { return s.ResumeLevel(running, t0) }},
		{"resume a closed level", func() error { return s.ResumeLevel(closed, t0) }},
		{"resume an unknown level", func() error { return s.ResumeLevel(999, t0) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.do(); err == nil {
				t.Fatal("succeeded, want an error")
			}
		})
	}
}

func TestThenLevelCanBeSetAndCleared(t *testing.T) {
	s := open(t)
	id := push(t, s, timed(90, 50, t0))

	if err := s.SetThenLevel(id, ptr(35)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetLevel(id); got.ThenLevel == nil || *got.ThenLevel != 35 {
		t.Fatalf("ThenLevel = %v, want 35", got.ThenLevel)
	}
	if err := s.SetThenLevel(id, ptr(0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetLevel(id); got.ThenLevel == nil || *got.ThenLevel != 0 {
		t.Fatalf("ThenLevel = %v, want 0 (off, distinct from unset)", got.ThenLevel)
	}
	if err := s.SetThenLevel(id, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetLevel(id); got.ThenLevel != nil {
		t.Fatalf("ThenLevel = %v, want nil (revert to the layer beneath)", got.ThenLevel)
	}
	if err := s.SetThenLevel(id, ptr(100)); err == nil {
		t.Error("then level 100 accepted")
	}
	if err := s.SetThenLevel(999, ptr(10)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown level: err = %v, want ErrNotFound", err)
	}
}

func TestCountersAccumulate(t *testing.T) {
	s := open(t)
	id := push(t, s, baseline(30))
	for _, step := range [][2]int{{1, 0}, {2, 1}, {0, 3}} {
		if err := s.AddCounts(id, step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GetLevel(id)
	if got.Blocked != 3 || got.Allowed != 4 {
		t.Fatalf("counters = blocked %d allowed %d, want 3 and 4", got.Blocked, got.Allowed)
	}
	s.CloseLevel(id, t0)
	if got, _ := s.GetLevel(id); got.Blocked != 3 || got.Allowed != 4 {
		t.Errorf("closing must keep the counters: %+v", got)
	}
	if err := s.AddCounts(999, 1, 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown level: err = %v, want ErrNotFound", err)
	}
}

func TestReopeningTheDatabaseRestoresTheStack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	base := push(t, first, store.Level{Kind: store.LevelBaseline, Percent: 30, StartTime: t0, Note: "all day"})
	escalation := push(t, first, store.Level{
		Kind: store.LevelEscalation, Percent: 90, StartTime: t0, TargetMinutes: 50,
		ExpiresAt: ptr(t0.Add(50 * time.Minute)), ThenLevel: ptr(0),
	})
	first.PauseLevel(escalation, t0.Add(10*time.Minute))
	first.AddCounts(escalation, 2, 1)
	first.Close()

	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	levels, err := second.ActiveLevels()
	if err != nil || len(levels) != 2 || levels[0].ID != base || levels[1].ID != escalation {
		t.Fatalf("stack after reopen = %+v, %v", levels, err)
	}
	top := levels[1]
	if top.RemainingSecs == nil || *top.RemainingSecs != 40*60 || top.PausedAt == nil || top.ThenLevel == nil || *top.ThenLevel != 0 ||
		top.Blocked != 2 || top.Allowed != 1 || levels[0].Note != "all day" {
		t.Fatalf("restored levels lost state: %+v", levels)
	}
}

func TestUnknownLevelReportsNotFound(t *testing.T) {
	if _, err := open(t).GetLevel(99); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSetExpiresAtMovesARunningDeadline(t *testing.T) {
	s := open(t)
	id := push(t, s, timed(90, 50, t0))
	moved := t0.Add(80 * time.Minute)
	if err := s.SetExpiresAt(id, moved); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetLevel(id); got.ExpiresAt == nil || !got.ExpiresAt.Equal(moved) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, moved)
	}
	s.CloseLevel(id, t0)
	if err := s.SetExpiresAt(id, moved); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("closed level: err = %v, want ErrNotFound", err)
	}
	if err := s.SetExpiresAt(999, moved); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown level: err = %v, want ErrNotFound", err)
	}
}

func TestSetRemainingSecsChangesAPausedLevel(t *testing.T) {
	s := open(t)
	id := push(t, s, timed(90, 50, t0))
	s.PauseLevel(id, t0)
	if err := s.SetRemainingSecs(id, 123); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetLevel(id); got.RemainingSecs == nil || *got.RemainingSecs != 123 {
		t.Fatalf("RemainingSecs = %v, want 123", got.RemainingSecs)
	}
	if err := s.SetRemainingSecs(id, -1); err == nil {
		t.Error("negative remaining time accepted")
	}
}

func TestSetPercentChangesTheLevel(t *testing.T) {
	s := open(t)
	id := push(t, s, baseline(30))
	if err := s.SetPercent(id, 75); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetLevel(id); got.Percent != 75 {
		t.Fatalf("Percent = %d, want 75", got.Percent)
	}
	if err := s.SetPercent(id, 100); err == nil {
		t.Error("percent 100 accepted")
	}
	if err := s.SetPercent(999, 10); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown level: err = %v, want ErrNotFound", err)
	}
}
