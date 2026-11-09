package session_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

var t0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

type rig struct {
	stack *session.Stack
	clock *testkit.FakeClock
	store *store.Store
}

func newRig(t *testing.T) rig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	clock := testkit.NewFakeClock(t0)
	return rig{session.NewStack(s, clock), clock, s}
}

func ptr[T any](v T) *T { return &v }

func (r rig) mustEscalate(t *testing.T, e session.Escalation) {
	t.Helper()
	if _, err := r.stack.Escalate(e); err != nil {
		t.Fatalf("Escalate(%+v): %v", e, err)
	}
}

func (r rig) status(t *testing.T) session.Status {
	t.Helper()
	st, err := r.stack.Status()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (r rig) level(t *testing.T) int {
	t.Helper()
	return r.status(t).Level
}

func percents(st session.Status) []int {
	out := make([]int, len(st.Layers))
	for i, l := range st.Layers {
		out[i] = l.Percent
	}
	return out
}

func sameInts(got []int, want ...int) bool {
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

func TestNothingActiveMeansLevelZero(t *testing.T) {
	r := newRig(t)
	if st := r.status(t); st.Level != 0 || len(st.Layers) != 0 {
		t.Fatalf("empty stack = %+v, want level 0 and no layers", st)
	}
}

func TestBaseline(t *testing.T) {
	r := newRig(t)
	if err := r.stack.SetBaseline(30); err != nil {
		t.Fatal(err)
	}
	if got := r.level(t); got != 30 {
		t.Fatalf("level = %d, want 30", got)
	}
	if err := r.stack.SetBaseline(45); err != nil {
		t.Fatal(err)
	}
	if st := r.status(t); st.Level != 45 || !sameInts(percents(st), 45) {
		t.Fatalf("replacing the baseline: %+v, want a single layer at 45", percents(st))
	}
	if err := r.stack.SetBaseline(0); err != nil {
		t.Fatal(err)
	}
	if st := r.status(t); st.Level != 0 || len(st.Layers) != 0 {
		t.Fatalf("baseline off: %+v, want nothing active", percents(st))
	}
	if err := r.stack.SetBaseline(0); err != nil {
		t.Errorf("turning off a baseline that is already off: %v", err)
	}
}

func TestBaselineKeepsTheEscalationOnTop(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: time.Hour})
	r.stack.SetBaseline(40)
	if st := r.status(t); st.Level != 90 || !sameInts(percents(st), 40, 90) {
		t.Fatalf("stack = %v level %d, want baseline 40 under the escalation at 90", percents(st), st.Level)
	}
}

func TestEscalationRevertsToTheBaselineWhenItsTimerEnds(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 50 * time.Minute})

	tests := []struct {
		advance time.Duration
		want    int
	}{
		{0, 90},
		{49 * time.Minute, 90},
		{time.Minute, 30}, // exactly at +50m
		{3 * time.Hour, 30},
	}
	for _, tt := range tests {
		r.clock.Advance(tt.advance)
		if got := r.level(t); got != tt.want {
			t.Fatalf("after +%v the level is %d, want %d", tt.advance, got, tt.want)
		}
	}
}

func TestEscalationBelowTheBaselineWinsWhileActive(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(65)
	r.mustEscalate(t, session.Escalation{Level: 10, Duration: 10 * time.Minute})
	if got := r.level(t); got != 10 {
		t.Fatalf("level = %d, want the lower escalation (10)", got)
	}
	r.clock.Advance(10 * time.Minute)
	if got := r.level(t); got != 65 {
		t.Fatalf("level after it ends = %d, want 65", got)
	}
}

func TestThenOverridesWhatHappensWhenTheTimerEnds(t *testing.T) {
	tests := []struct {
		name       string
		then       *int
		wantLevel  int
		wantLayers []int
	}{
		{"default falls back to the baseline", nil, 30, []int{30}},
		{"then off clears the baseline too", ptr(0), 0, nil},
		{"then a level replaces the baseline", ptr(65), 65, []int{65}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.stack.SetBaseline(30)
			r.mustEscalate(t, session.Escalation{Level: 90, Duration: 50 * time.Minute, Then: tt.then})
			r.clock.Advance(50 * time.Minute)
			st := r.status(t)
			if st.Level != tt.wantLevel || !sameInts(percents(st), tt.wantLayers...) {
				t.Fatalf("level %d layers %v, want level %d layers %v", st.Level, percents(st), tt.wantLevel, tt.wantLayers)
			}
		})
	}
}

func TestThenIsIgnoredWhenStoppedByHand(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 50 * time.Minute, Then: ptr(0)})
	if err := r.stack.Stop(); err != nil {
		t.Fatal(err)
	}
	if st := r.status(t); st.Level != 30 || !sameInts(percents(st), 30) {
		t.Fatalf("after Stop: level %d layers %v, want the baseline back", st.Level, percents(st))
	}
}

func TestIndefiniteEscalationNeverExpires(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 75})
	r.clock.Advance(1000 * time.Hour)
	if got := r.level(t); got != 75 {
		t.Fatalf("level = %d, want 75", got)
	}
}

func TestExpiredLayersBelowTheTopLeaveTheTopAlone(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 60, Duration: 10 * time.Minute})
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 60 * time.Minute})
	r.clock.Advance(20 * time.Minute)
	if st := r.status(t); st.Level != 90 || !sameInts(percents(st), 30, 90) {
		t.Fatalf("level %d layers %v, want the 10-minute layer gone and 90 still on top", st.Level, percents(st))
	}
}

func TestSeveralExpiriesAreSettledInOrderAtTheirOwnDeadlines(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 60, Duration: 10 * time.Minute})
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 20 * time.Minute})
	r.clock.Advance(time.Hour)
	if st := r.status(t); st.Level != 30 || !sameInts(percents(st), 30) {
		t.Fatalf("level %d layers %v, want only the baseline", st.Level, percents(st))
	}
	levels, _ := r.store.ActiveLevels()
	if len(levels) != 1 {
		t.Fatalf("active levels = %d, want 1", len(levels))
	}
	closed, err := r.store.GetLevel(levels[0].ID + 1)
	if err != nil || closed.EndTime == nil || !closed.EndTime.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("first escalation = %+v, %v; want it closed at its own deadline, +10m", closed, err)
	}
}

func TestPauseFreezesTheRemainingTime(t *testing.T) {
	r := newRig(t)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 50 * time.Minute})
	r.clock.Advance(20 * time.Minute)
	if err := r.stack.Pause(); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(2 * time.Hour)

	st := r.status(t)
	top := st.Layers[len(st.Layers)-1]
	if st.Level != 90 || !top.Paused || top.Remaining != 30*time.Minute {
		t.Fatalf("paused: level %d paused=%v remaining=%v, want 90, true, 30m (focus stays on while the timer is paused)", st.Level, top.Paused, top.Remaining)
	}

	if err := r.stack.Resume(); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(29 * time.Minute)
	if got := r.level(t); got != 90 {
		t.Fatalf("29 minutes after resuming: level %d, want 90", got)
	}
	r.clock.Advance(time.Minute)
	if got := r.level(t); got != 0 {
		t.Fatalf("30 minutes after resuming: level %d, want 0", got)
	}
}

func TestPauseTargetsTheTopTimedLayer(t *testing.T) {
	r := newRig(t)
	r.mustEscalate(t, session.Escalation{Level: 60, Duration: time.Hour})
	r.mustEscalate(t, session.Escalation{Level: 90}) // indefinite, on top
	if err := r.stack.Pause(); err != nil {
		t.Fatalf("Pause with an indefinite layer on top of a timed one: %v", err)
	}
	st := r.status(t)
	if !st.Layers[0].Paused || st.Layers[1].Paused {
		t.Fatalf("paused flags = %v %v, want only the timed layer paused", st.Layers[0].Paused, st.Layers[1].Paused)
	}
}

func TestPauseAndResumeNeedSomethingToAct(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	if err := r.stack.Pause(); !errors.Is(err, session.ErrNoTimer) {
		t.Errorf("Pause with only a baseline: err = %v, want ErrNoTimer", err)
	}
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: time.Hour})
	if err := r.stack.Resume(); !errors.Is(err, session.ErrNotPaused) {
		t.Errorf("Resume of a running timer: err = %v, want ErrNotPaused", err)
	}
	r.stack.Pause()
	if err := r.stack.Pause(); !errors.Is(err, session.ErrNoTimer) {
		t.Errorf("second Pause: err = %v, want ErrNoTimer (nothing running to pause)", err)
	}
}

func TestAdjust(t *testing.T) {
	tests := []struct {
		name     string
		adjust   time.Duration
		advance  time.Duration
		wantOn   bool
		describe string
	}{
		{"extend keeps it alive past the original end", 5 * time.Minute, 14 * time.Minute, true, "+14m of 15m"},
		{"extend ends at the new time", 5 * time.Minute, 15 * time.Minute, false, "+15m of 15m"},
		{"shorten ends earlier", -5 * time.Minute, 5 * time.Minute, false, "+5m of 5m"},
		{"shorten but not yet", -5 * time.Minute, 4 * time.Minute, true, "+4m of 5m"},
		{"shortening past the end expires it now", -30 * time.Minute, 0, false, "immediately"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.mustEscalate(t, session.Escalation{Level: 90, Duration: 10 * time.Minute})
			if err := r.stack.Adjust(tt.adjust); err != nil {
				t.Fatal(err)
			}
			r.clock.Advance(tt.advance)
			if on := r.level(t) == 90; on != tt.wantOn {
				t.Fatalf("%s: active = %v, want %v", tt.describe, on, tt.wantOn)
			}
		})
	}

	t.Run("a paused layer's remaining time changes", func(t *testing.T) {
		r := newRig(t)
		r.mustEscalate(t, session.Escalation{Level: 90, Duration: 10 * time.Minute})
		r.stack.Pause()
		if err := r.stack.Adjust(5 * time.Minute); err != nil {
			t.Fatal(err)
		}
		st := r.status(t)
		if got := st.Layers[0].Remaining; got != 15*time.Minute || !st.Layers[0].Paused {
			t.Fatalf("remaining = %v paused=%v, want 15m and still paused", got, st.Layers[0].Paused)
		}
		r.stack.Adjust(-time.Hour)
		if got := r.status(t).Layers[0].Remaining; got != 0 {
			t.Fatalf("remaining after a big cut = %v, want 0 (never negative)", got)
		}
	})
	t.Run("nothing to adjust", func(t *testing.T) {
		r := newRig(t)
		r.stack.SetBaseline(30)
		if err := r.stack.Adjust(time.Minute); !errors.Is(err, session.ErrNoTimer) {
			t.Fatalf("err = %v, want ErrNoTimer", err)
		}
	})
}

func TestStopPopsOnlyTheTopEscalation(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: time.Hour})
	r.mustEscalate(t, session.Escalation{Level: 75})

	steps := []struct {
		wantLevel int
		wantErr   error
	}{{90, nil}, {30, nil}, {30, session.ErrNothingToStop}}
	for i, step := range steps {
		err := r.stack.Stop()
		if !errors.Is(err, step.wantErr) {
			t.Fatalf("Stop #%d: err = %v, want %v", i+1, err, step.wantErr)
		}
		if got := r.level(t); got != step.wantLevel {
			t.Fatalf("after Stop #%d: level %d, want %d", i+1, got, step.wantLevel)
		}
	}
}

func TestStopAllClearsEverythingIncludingTheBaseline(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: time.Hour})
	if err := r.stack.StopAll(); err != nil {
		t.Fatal(err)
	}
	if st := r.status(t); st.Level != 0 || len(st.Layers) != 0 {
		t.Fatalf("after StopAll: %v level %d", percents(st), st.Level)
	}
	if err := r.stack.StopAll(); err != nil {
		t.Errorf("StopAll on an empty stack: %v", err)
	}
}

func TestSetLevelChangesTheTopLayer(t *testing.T) {
	r := newRig(t)
	if err := r.stack.SetLevel(50); !errors.Is(err, session.ErrNoSession) {
		t.Fatalf("SetLevel with nothing active: err = %v, want ErrNoSession", err)
	}
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: time.Hour})
	if err := r.stack.SetLevel(75); err != nil {
		t.Fatal(err)
	}
	if st := r.status(t); st.Level != 75 || !sameInts(percents(st), 30, 75) {
		t.Fatalf("layers %v level %d, want only the top layer changed", percents(st), st.Level)
	}
	r.stack.Stop()
	if err := r.stack.SetLevel(40); err != nil {
		t.Fatal(err)
	}
	if got := r.level(t); got != 40 {
		t.Fatalf("after changing the baseline: %d, want 40", got)
	}
	if err := r.stack.SetLevel(100); err == nil {
		t.Error("SetLevel(100) accepted; levels stop at 99")
	}
}

func TestInvalidInputIsRejected(t *testing.T) {
	r := newRig(t)
	tests := []struct {
		name string
		err  error
	}{
		{"baseline above 99", r.stack.SetBaseline(100)},
		{"negative baseline", r.stack.SetBaseline(-1)},
		{"escalation above 99", func() error { _, err := r.stack.Escalate(session.Escalation{Level: 100}); return err }()},
		{"negative duration", func() error {
			_, err := r.stack.Escalate(session.Escalation{Level: 50, Duration: -time.Minute})
			return err
		}()},
		{"then above 99", func() error {
			_, err := r.stack.Escalate(session.Escalation{Level: 50, Then: ptr(100)})
			return err
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatal("accepted, want an error")
			}
		})
	}
	if st := r.status(t); len(st.Layers) != 0 {
		t.Errorf("rejected input left layers behind: %v", percents(st))
	}
}

func TestStatusReportsTimeLeftAndTheNote(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 50 * time.Minute, Note: "release prep"})
	r.mustEscalate(t, session.Escalation{Level: 75})
	r.clock.Advance(10 * time.Minute)

	st := r.status(t)
	base, timed, forever := st.Layers[0], st.Layers[1], st.Layers[2]
	if base.Timed || forever.Timed {
		t.Errorf("baseline timed=%v, indefinite timed=%v, want both false", base.Timed, forever.Timed)
	}
	if !timed.Timed || timed.Remaining != 40*time.Minute || timed.Note != "release prep" {
		t.Errorf("timed layer = timed:%v remaining:%v note:%q, want true, 40m, the note", timed.Timed, timed.Remaining, timed.Note)
	}
}

func TestNextExpiryIsTheEarliestRunningTimer(t *testing.T) {
	r := newRig(t)
	if _, ok, err := r.stack.NextExpiry(); err != nil || ok {
		t.Fatalf("empty stack: ok=%v err=%v, want no expiry", ok, err)
	}
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 75}) // indefinite, ignored
	r.mustEscalate(t, session.Escalation{Level: 60, Duration: 30 * time.Minute})
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 10 * time.Minute})

	at, ok, err := r.stack.NextExpiry()
	if err != nil || !ok || !at.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("NextExpiry = %v, %v, %v; want +10m", at, ok, err)
	}
	r.stack.Pause() // pauses the top timed layer (the 10-minute one)
	at, ok, _ = r.stack.NextExpiry()
	if !ok || !at.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("NextExpiry with the top paused = %v, %v; want +30m", at, ok)
	}
}

func TestStateSurvivesAnEngineRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	clock := testkit.NewFakeClock(t0)
	stack := session.NewStack(first, clock)
	stack.SetBaseline(30)
	if _, err := stack.Escalate(session.Escalation{Level: 90, Duration: 50 * time.Minute, Then: ptr(0), Note: "deep work"}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(20 * time.Minute)
	first.Close()

	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	restarted := session.NewStack(second, testkit.NewFakeClock(t0.Add(20*time.Minute)))
	st, err := restarted.Status()
	if err != nil || st.Level != 90 || len(st.Layers) != 2 || st.Layers[1].Remaining != 30*time.Minute || st.Layers[1].Note != "deep work" {
		t.Fatalf("restarted status = %+v, %v", st, err)
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAnUntimedSessionReplacesAnUntimedOneOnTopOfIt(t *testing.T) {
	r := newRig(t)
	r.stack.SetBaseline(30)
	r.mustEscalate(t, session.Escalation{Level: 65, Note: "first"})
	r.mustEscalate(t, session.Escalation{Level: 40})
	r.mustEscalate(t, session.Escalation{Level: 0})
	r.mustEscalate(t, session.Escalation{Level: 75})
	st := r.status(t)
	if got := percents(st); !equalIntSlices(got, []int{30, 75}) || st.Level != 75 {
		t.Fatalf("layers = %v, level %d, want the baseline and just the newest session: repeated untimed sessions replace each other", got, st.Level)
	}
	if err := r.stack.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := r.level(t); got != 30 {
		t.Errorf("level after stopping = %d, want the baseline: there is no pile of old sessions to fall back to", got)
	}
}

func TestATimedSessionStillStacksOnAnUntimedOne(t *testing.T) {
	r := newRig(t)
	r.mustEscalate(t, session.Escalation{Level: 65})
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 25 * time.Minute})
	if got := percents(r.status(t)); !equalIntSlices(got, []int{65, 90}) {
		t.Fatalf("layers = %v, want both: the timed session falls back to the untimed one", got)
	}
	r.clock.Advance(26 * time.Minute)
	if got := r.level(t); got != 65 {
		t.Errorf("level after the timer = %d, want 65", got)
	}
}

func TestAnUntimedSessionStacksOnATimedOne(t *testing.T) {
	r := newRig(t)
	r.mustEscalate(t, session.Escalation{Level: 90, Duration: 25 * time.Minute})
	r.mustEscalate(t, session.Escalation{Level: 40})
	if got := percents(r.status(t)); !equalIntSlices(got, []int{90, 40}) {
		t.Errorf("layers = %v: a timed session is not replaced, it comes back when the newer one stops", got)
	}
}

func TestReplacingKeepsTheReplacedSessionInTheHistory(t *testing.T) {
	r := newRig(t)
	r.mustEscalate(t, session.Escalation{Level: 65})
	r.mustEscalate(t, session.Escalation{Level: 40})
	levels, err := r.store.ActiveLevels()
	if err != nil || len(levels) != 1 {
		t.Fatalf("active = %d, %v", len(levels), err)
	}
	old, err := r.store.GetLevel(levels[0].ID - 1)
	if err != nil || old.EndTime == nil {
		t.Errorf("the replaced session = %+v, %v; want it kept with an end time, not deleted", old, err)
	}
}
