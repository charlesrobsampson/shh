package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

func TestARoutineRunIsStoredWithItsProgress(t *testing.T) {
	s := open(t)
	if _, ok, err := s.ActiveRoutineRun(); ok || err != nil {
		t.Fatalf("a new store has an active run: %v %v", ok, err)
	}
	id, err := s.StartRoutineRun("pomodoro", t0, 9)
	if err != nil {
		t.Fatal(err)
	}
	run, ok, err := s.ActiveRoutineRun()
	if err != nil || !ok {
		t.Fatalf("ActiveRoutineRun = %v %v", ok, err)
	}
	if run.ID != id || run.Routine != "pomodoro" || run.StepsTotal != 9 || run.Position != 0 || run.State != store.RoutineRunning ||
		!run.StartedAt.Equal(t0) || run.EndedAt != nil || run.LevelID != 0 {
		t.Errorf("run = %+v", run)
	}
	level, err := s.PushLevel(store.Level{Kind: store.LevelEscalation, Percent: 75, StartTime: t0})
	must(t, err)
	must(t, s.SetRoutineRunProgress(id, 3, store.RoutineWaiting, level))
	run, _, _ = s.ActiveRoutineRun()
	if run.Position != 3 || run.State != store.RoutineWaiting || run.LevelID != level {
		t.Errorf("after progress = %+v", run)
	}
	must(t, s.SetRoutineRunProgress(id, 4, store.RoutineRunning, 0))
	if run, _, _ = s.ActiveRoutineRun(); run.LevelID != 0 {
		t.Errorf("a level of 0 should store as none, got %d", run.LevelID)
	}
}

func TestOnlyOneRoutineRunsAtATime(t *testing.T) {
	s := open(t)
	id, err := s.StartRoutineRun("a", t0, 2)
	must(t, err)
	if _, err := s.StartRoutineRun("b", t0, 2); !errors.Is(err, store.ErrRoutineActive) {
		t.Fatalf("a second start: err = %v, want ErrRoutineActive", err)
	}
	must(t, s.EndRoutineRun(id, t0.Add(time.Hour), "finished", 5, 2, 3600))
	if _, ok, _ := s.ActiveRoutineRun(); ok {
		t.Error("an ended run is still active")
	}
	if _, err := s.StartRoutineRun("b", t0.Add(2*time.Hour), 2); err != nil {
		t.Errorf("a new run after the last one ended: %v", err)
	}
}

func TestEndingARunKeepsItsTotalsAndReason(t *testing.T) {
	s := open(t)
	id, _ := s.StartRoutineRun("pomodoro", t0, 3)
	must(t, s.EndRoutineRun(id, t0.Add(90*time.Minute), "finished", 12, 3, 5400))
	runs, err := s.RoutineRuns(0)
	if err != nil || len(runs) != 1 {
		t.Fatalf("RoutineRuns = %v, %v", runs, err)
	}
	r := runs[0]
	if r.EndReason != "finished" || r.Blocked != 12 || r.Allowed != 3 || r.ActiveSecs != 5400 || r.EndedAt == nil || !r.EndedAt.Equal(t0.Add(90*time.Minute)) || r.LevelID != 0 {
		t.Errorf("ended run = %+v", r)
	}
	if err := s.EndRoutineRun(id, t0, "stopped", 0, 0, 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ending an ended run again: err = %v, want ErrNotFound (it must not be rewritten)", err)
	}
	if err := s.SetRoutineRunProgress(id, 1, store.RoutineRunning, 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("moving an ended run: err = %v, want ErrNotFound", err)
	}
}

func TestRunsAreListedNewestFirstWithALimit(t *testing.T) {
	s := open(t)
	for i, name := range []string{"a", "b", "c"} {
		id, err := s.StartRoutineRun(name, t0.Add(time.Duration(i)*time.Hour), 1)
		must(t, err)
		must(t, s.EndRoutineRun(id, t0.Add(time.Duration(i)*time.Hour+time.Minute), "finished", 0, 0, 60))
	}
	runs, _ := s.RoutineRuns(2)
	if len(runs) != 2 || runs[0].Routine != "c" || runs[1].Routine != "b" {
		t.Errorf("runs = %+v, want c then b", runs)
	}
}

func TestStepsAndTheirLevelsAreKeptSoTotalsCanBeAddedUp(t *testing.T) {
	s := open(t)
	run, _ := s.StartRoutineRun("pomodoro", t0, 2)
	work, err := s.PushLevel(store.Level{Kind: store.LevelEscalation, Percent: 75, StartTime: t0, TargetMinutes: 25, ExpiresAt: ptr(t0.Add(25 * time.Minute))})
	must(t, err)
	must(t, s.AddCounts(work, 4, 1))
	must(t, s.CloseLevel(work, t0.Add(25*time.Minute)))
	rest, err := s.PushLevel(store.Level{Kind: store.LevelEscalation, Percent: 0, StartTime: t0.Add(25 * time.Minute)})
	must(t, err)
	must(t, s.AddRoutineRunStep(run, 0, "work", work, t0))
	must(t, s.AddRoutineRunStep(run, 1, "break", rest, t0.Add(25*time.Minute)))
	must(t, s.AddRoutineRunStep(run, 2, "no level", 0, t0.Add(30*time.Minute)))

	steps, err := s.RoutineRunSteps(run)
	if err != nil || len(steps) != 3 || steps[0].Name != "work" || steps[0].LevelID != work || steps[2].LevelID != 0 || !steps[1].StartedAt.Equal(t0.Add(25*time.Minute)) {
		t.Fatalf("steps = %+v, %v", steps, err)
	}
	levels, err := s.RoutineRunLevels(run)
	if err != nil || len(levels) != 2 || levels[0].ID != work || levels[0].Blocked != 4 || levels[0].Allowed != 1 || levels[1].ID != rest {
		t.Errorf("levels = %+v, %v", levels, err)
	}
	if levels, _ := s.RoutineRunLevels(9999); len(levels) != 0 {
		t.Errorf("a run with no steps has levels %v", levels)
	}
}
