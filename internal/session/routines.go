package session

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/store"
)

var (
	// ErrRoutineRunning means a routine is running, so the command was refused: a session started or stopped by hand would fight the
	// routine for the focus stack. End the routine first, or let it finish.
	ErrRoutineRunning = errors.New("a routine is running (end it with: shh routine stop)")
	// ErrNoRoutine means there is no routine run to act on.
	ErrNoRoutine = errors.New("no routine is running")
	// ErrUnknownRoutine means no saved routine has that name.
	ErrUnknownRoutine = errors.New("no saved routine has that name")
)

// RoutineLoader reads the saved routines. It is called each time one is needed, so edits to the file apply to the next run (and
// a run in progress follows the file's current steps).
type RoutineLoader func() ([]routine.Routine, error)

// routineRunner runs routines for the worker. It keeps no state of its own: which run is active, where it has got to and which focus
// level it is on are all in the store, so a restarted worker carries on from the same place. Each step is an ordinary timed focus
// session on the stack, so pause, +/- time, the menu bar and the status all treat it like any other.
type routineRunner struct {
	w  *Worker
	mu sync.Mutex
}

func newRoutineRunner(w *Worker) *routineRunner { return &routineRunner{w: w} }

// Start begins a saved routine at its first step.
func (r *routineRunner) Start(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, err := r.find(name)
	if err != nil {
		return err
	}
	plan := rt.Plan()
	sessions := 0 // the summary counts sessions: a baseline step is not one that is started or skipped
	for _, p := range plan {
		if p.Kind == routine.StepSession {
			sessions++
		}
	}
	id, err := r.w.cfg.Store.StartRoutineRun(rt.Name, r.w.cfg.Clock.Now(), sessions)
	if errors.Is(err, store.ErrRoutineActive) {
		return ErrRoutineRunning
	}
	if err != nil {
		return err
	}
	run, _, err := r.w.cfg.Store.ActiveRoutineRun()
	if err != nil {
		return err
	}
	_ = id // the run row is read back so the step starts from what was stored
	return r.beginStep(run, rt, plan, 0)
}

// Next moves a run on: when it is waiting, it starts the step that was waiting; otherwise it ends the current step early and goes
// straight to the next one, whatever the step says about asking.
func (r *routineRunner) Next() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, rt, plan, err := r.active()
	if err != nil {
		return err
	}
	if run.State == store.RoutineWaiting {
		return r.startWaiting(run, rt, plan)
	}
	if err := r.w.stack.CloseLayer(run.LevelID); err != nil {
		return err
	}
	return r.advance(run, rt, plan, run.Position, false)
}

// Back restarts the step before the one in focus: the previous step while one is running, the step that just ended while one is
// waiting. At the first step it restarts that step.
func (r *routineRunner) Back() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, rt, plan, err := r.active()
	if err != nil {
		return err
	}
	target := run.Position // a baseline step is not one to go back to: back to the session before it
	for i := run.Position - 1; i >= 0; i-- {
		if plan[i].Kind == routine.StepSession {
			target = i
			break
		}
	}
	return r.beginStep(run, rt, plan, target)
}

// Stop ends the run now, closing whatever focus level it has, and records what it did.
func (r *routineRunner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok, err := r.w.cfg.Store.ActiveRoutineRun()
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoRoutine
	}
	return r.finish(run, nil, "stopped")
}

// Tick is called regularly. It notices that the step in force has run out and moves on, and ends a run whose routine has vanished
// or whose focus level was ended by hand.
func (r *routineRunner) Tick() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok, err := r.w.cfg.Store.ActiveRoutineRun()
	if err != nil || !ok {
		return err
	}
	if _, err := r.w.stack.Status(); err != nil { // settles timers that have run out, closing their levels
		return err
	}
	rt, plan, err := r.load(run)
	if err != nil {
		if r.w.cfg.OnError != nil {
			r.w.cfg.OnError(fmt.Errorf("routine %q: %w; ending the run", run.Routine, err))
		}
		return r.finish(run, nil, "missing")
	}
	if run.LevelID == 0 {
		return nil // waiting with nothing in force (waiting: baseline); nothing can end by itself
	}
	level, err := r.w.cfg.Store.GetLevel(run.LevelID)
	if err != nil {
		return err
	}
	if level.EndTime == nil {
		return nil // still going, held or paused
	}
	if run.State == store.RoutineWaiting || level.ExpiresAt == nil || level.EndTime.Before(*level.ExpiresAt) {
		return r.finish(run, &rt, "stopped") // ended by hand, not by running out
	}
	return r.advance(run, rt, plan, run.Position, true)
}

// Active says whether a run is in progress.
func (r *routineRunner) Active() bool {
	_, ok, err := r.w.cfg.Store.ActiveRoutineRun()
	return ok && err == nil
}

// View describes the run in progress for clients, or nil when there is none.
func (r *routineRunner) View() *RoutineView {
	run, ok, err := r.w.cfg.Store.ActiveRoutineRun()
	if err != nil || !ok {
		return nil
	}
	rt, plan, err := r.load(run)
	if err != nil || run.Position >= len(plan) {
		return &RoutineView{Name: run.Routine, Position: run.Position, Total: run.StepsTotal, Waiting: run.State == store.RoutineWaiting, Started: run.StartedAt}
	}
	current := plan[run.Position]
	v := &RoutineView{
		Name: rt.Name, Position: run.Position, Total: len(plan), Step: current.Label(), Waiting: run.State == store.RoutineWaiting,
		Level: current.Level, LengthSecs: int(current.Length / time.Second), Started: run.StartedAt,
	}
	if v.Waiting && run.Position > 0 {
		v.Previous = plan[run.Position-1].Label()
	}
	if run.Position+1 < len(plan) {
		v.Next = plan[run.Position+1].Label()
	}
	return v
}

// Info is the answer to "routine status": the run in progress and the last one that ended.
func (r *routineRunner) Info() (RoutineInfo, error) {
	info := RoutineInfo{Active: r.View()}
	runs, err := r.w.cfg.Store.RoutineRuns(5)
	if err != nil {
		return info, err
	}
	for _, run := range runs {
		if run.EndedAt != nil {
			summary := r.summary(run)
			info.Last = &summary
			break
		}
	}
	return info, nil
}

// ---- moving between steps ----

// advance goes on from the step at pos, which has ended (naturally, or because it was skipped). natural says it ran its course, which is
// what its alert and its advance setting apply to.
func (r *routineRunner) advance(run store.RoutineRun, rt routine.Routine, plan []routine.Planned, pos int, natural bool) error {
	step := plan[pos]
	next := nextSession(plan, pos+1)
	last := next >= len(plan)
	if natural {
		r.announce(rt, plan, pos, next, last)
	}
	switch {
	case last:
		if _, err := r.applyBaselines(plan, pos+1); err != nil { // baseline steps after the last session still apply
			return err
		}
		return r.finish(run, &rt, "finished")
	case natural && step.Advance == routine.AdvanceAsk:
		if _, err := r.applyBaselines(plan, pos+1); err != nil {
			return err
		}
		return r.wait(run, rt, plan, pos, next)
	}
	return r.beginStep(run, rt, plan, pos+1)
}

// nextSession is the index of the first session step at or after from, or len(plan) if only baseline steps (or none) are left.
func nextSession(plan []routine.Planned, from int) int {
	for i := from; i < len(plan); i++ {
		if plan[i].Kind == routine.StepSession {
			return i
		}
	}
	return len(plan)
}

// applyBaselines sets the baseline for each baseline step from the given index on, up to the next session step, whose index it
// returns (len(plan) if there is none). A baseline step takes no time: it applies as the run reaches it.
func (r *routineRunner) applyBaselines(plan []routine.Planned, from int) (int, error) {
	for from < len(plan) && plan[from].Kind == routine.StepBaseline {
		if err := r.w.stack.SetBaseline(plan[from].Level); err != nil {
			return from, err
		}
		from++
	}
	return from, nil
}

// beginStep puts the step at pos in force as a timed session, ending whatever level the run had.
func (r *routineRunner) beginStep(run store.RoutineRun, rt routine.Routine, plan []routine.Planned, pos int) error {
	pos, err := r.applyBaselines(plan, pos)
	if err != nil {
		return err
	}
	if pos >= len(plan) { // nothing but baseline steps were left
		return r.finish(run, &rt, "finished")
	}
	if run.LevelID != 0 {
		if err := r.w.stack.CloseLayer(run.LevelID); err != nil {
			return err
		}
	}
	step := plan[pos]
	level, err := r.w.stack.Escalate(Escalation{Level: step.Level, Duration: step.Length, Note: stepNote(rt, step)})
	if err != nil {
		return err
	}
	now := r.w.cfg.Clock.Now()
	if err := r.w.cfg.Store.AddRoutineRunStep(run.ID, pos, step.Name, level, now); err != nil {
		return err
	}
	if err := r.w.cfg.Store.SetRoutineRunProgress(run.ID, pos, store.RoutineRunning, level); err != nil {
		return err
	}
	return r.w.syncSuppressor()
}

// wait stops after the step at pos, until the user starts the next one. What focus does meanwhile is the routine's waiting setting.
func (r *routineRunner) wait(run store.RoutineRun, rt routine.Routine, plan []routine.Planned, pos, nextPos int) error {
	finished, next := plan[pos], plan[nextPos]
	var level int64
	switch rt.Waiting {
	case routine.WaitStay:
		// Hold the level of the step that ended, with no timer, so walking away does not let notifications through.
		id, err := r.w.stack.Escalate(Escalation{Level: finished.Level, Note: fmt.Sprintf("%s · waiting to start %s", rt.Name, next.Label())})
		if err != nil {
			return err
		}
		level = id
	case routine.WaitNext:
		// The next step's level at once, with its countdown held until it is started.
		id, err := r.w.stack.Escalate(Escalation{Level: next.Level, Duration: next.Length, Note: stepNote(rt, next) + " · waiting to start"})
		if err != nil {
			return err
		}
		if err := r.w.stack.PauseLayer(id); err != nil {
			return err
		}
		if err := r.w.cfg.Store.AddRoutineRunStep(run.ID, nextPos, next.Name, id, r.w.cfg.Clock.Now()); err != nil {
			return err
		}
		level = id
	}
	if err := r.w.cfg.Store.SetRoutineRunProgress(run.ID, nextPos, store.RoutineWaiting, level); err != nil {
		return err
	}
	return r.w.syncSuppressor()
}

// startWaiting starts the step a run is waiting on.
func (r *routineRunner) startWaiting(run store.RoutineRun, rt routine.Routine, plan []routine.Planned) error {
	if rt.Waiting == routine.WaitNext && run.LevelID != 0 {
		if err := r.w.stack.ResumeLayer(run.LevelID); err != nil {
			return err
		}
		if err := r.w.cfg.Store.SetRoutineRunProgress(run.ID, run.Position, store.RoutineRunning, run.LevelID); err != nil {
			return err
		}
		return r.w.syncSuppressor()
	}
	return r.beginStep(run, rt, plan, run.Position) // closes the hold, if there is one
}

// finish ends the run: closes its level, totals what it did and records that, and shows the summary where the routine says.
func (r *routineRunner) finish(run store.RoutineRun, rt *routine.Routine, reason string) error {
	if run.LevelID != 0 {
		if err := r.w.stack.CloseLayer(run.LevelID); err != nil {
			return err
		}
	}
	now := r.w.cfg.Clock.Now()
	levels, err := r.w.cfg.Store.RoutineRunLevels(run.ID)
	if err != nil {
		return err
	}
	var held, let int
	for _, l := range levels {
		held += l.Blocked
		let += l.Allowed
	}
	elapsed := int(now.Sub(run.StartedAt) / time.Second)
	if err := r.w.cfg.Store.EndRoutineRun(run.ID, now, reason, held, let, elapsed); err != nil {
		return err
	}
	if err := r.w.syncSuppressor(); err != nil {
		return err
	}
	if rt != nil && reason == "finished" && containsWhere(rt.Summary, routine.WhereBanner) {
		runs, err := r.w.cfg.Store.RoutineRuns(1)
		if err == nil && len(runs) == 1 {
			r.show(alert.Alert{Kind: config.KindMessage, Source: "Routine", Title: rt.Name + " finished", Body: describeSummary(r.summary(runs[0]))})
		}
	}
	return nil
}

// announce says that the step at pos is over, if its alert asks for that. A banner says what comes next and how to carry on.
func (r *routineRunner) announce(rt routine.Routine, plan []routine.Planned, pos, nextPos int, last bool) {
	step := plan[pos]
	if step.Alert == routine.AlertSound && !last {
		r.w.playSound()
		return
	}
	if step.Alert != routine.AlertBanner || last { // the end of the routine has its summary instead
		return
	}
	next := plan[nextPos]
	body := fmt.Sprintf("Next: %s, %s at %s", next.Label(), formatLength(next.Length), levelText(next.Level))
	if step.Advance == routine.AdvanceAsk {
		body += ". Start it with: shh routine next"
	}
	r.show(alert.Alert{Kind: config.KindMessage, Source: "Routine", Title: step.Name + " is over", Body: body})
}

func (r *routineRunner) show(a alert.Alert) { r.w.say(a) }

// ---- loading ----

func (r *routineRunner) find(name string) (routine.Routine, error) {
	if r.w.cfg.Routines == nil {
		return routine.Routine{}, ErrUnknownRoutine
	}
	all, err := r.w.cfg.Routines()
	if err != nil {
		return routine.Routine{}, err
	}
	rt, ok := routine.Find(all, name)
	if !ok {
		return routine.Routine{}, fmt.Errorf("%w: %q", ErrUnknownRoutine, name)
	}
	return rt, nil
}

// load finds the routine of a run and its current plan. A routine that has been edited since the run started is followed as edited:
// the position stays, and a position past the end of a shorter plan finishes the run.
func (r *routineRunner) load(run store.RoutineRun) (routine.Routine, []routine.Planned, error) {
	rt, err := r.find(run.Routine)
	if err != nil {
		return routine.Routine{}, nil, err
	}
	plan := rt.Plan()
	if run.Position >= len(plan) {
		return rt, plan, fmt.Errorf("the routine now has only %d steps and the run was at step %d", len(plan), run.Position+1)
	}
	return rt, plan, nil
}

// active returns the run in progress with its routine.
func (r *routineRunner) active() (store.RoutineRun, routine.Routine, []routine.Planned, error) {
	run, ok, err := r.w.cfg.Store.ActiveRoutineRun()
	if err != nil {
		return run, routine.Routine{}, nil, err
	}
	if !ok {
		return run, routine.Routine{}, nil, ErrNoRoutine
	}
	rt, plan, err := r.load(run)
	if err != nil {
		return run, rt, plan, err
	}
	return run, rt, plan, nil
}

// ---- wording ----

func stepNote(rt routine.Routine, p routine.Planned) string {
	note := fmt.Sprintf("%s · %s", rt.Name, p.Label())
	if p.Note != "" {
		note += " · " + p.Note
	}
	return note
}

func containsWhere(list []routine.Where, w routine.Where) bool {
	for _, x := range list {
		if x == w {
			return true
		}
	}
	return false
}

func levelText(level int) string {
	if level == 0 {
		return "off"
	}
	return decide.FormatLevel(level)
}

func formatLength(d time.Duration) string { return decide.FormatRemaining(int(d / time.Second)) }

// summary describes a run that has ended: how many steps it started of how many, how long it took, and what it held back and let through.
func (r *routineRunner) summary(run store.RoutineRun) RoutineSummary {
	s := RoutineSummary{Name: run.Routine, Reason: run.EndReason, Total: run.StepsTotal, Started: run.StartedAt, Held: run.Blocked, Let: run.Allowed, ElapsedSecs: run.ActiveSecs}
	if run.EndedAt != nil {
		s.Ended = *run.EndedAt
	}
	if steps, err := r.w.cfg.Store.RoutineRunSteps(run.ID); err == nil {
		started := map[int]bool{}
		for _, st := range steps {
			started[st.Position] = true
		}
		s.Steps = len(started)
	}
	return s
}

// describeSummary writes a finished run's summary as one sentence, for the banner and the command line.
func describeSummary(s RoutineSummary) string {
	took := decide.FormatRemaining(s.ElapsedSecs)
	held := fmt.Sprintf("held back %d", s.Held)
	if s.Held == 1 {
		held = "held back 1 notification"
	} else {
		held += " notifications"
	}
	return fmt.Sprintf("%d steps in %s. %s, let through %d.", s.Steps, took, strings.ToUpper(held[:1])+held[1:], s.Let)
}
