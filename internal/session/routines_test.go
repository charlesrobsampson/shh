package session_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

// pomodoroLite is a short routine: two rounds of work then break, and a long break at the end.
const pomodoroLite = `{"routines": {"pomo": {
  "repeat": 2,
  "steps": [
    {"name": "work", "level": "75%", "length": "25m", "note": "focus", "alert": "banner"},
    {"name": "break", "level": "off", "length": "5m", "alert": "banner"}
  ],
  "finish": [{"name": "long break", "level": "off", "length": "15m"}]
}}}`

// routineRig is a worker rig whose routines come from a text that tests can change.
type routineRig struct {
	*workerRig
	text *string
}

func newRoutineRig(t *testing.T, text string) *routineRig {
	t.Helper()
	rr := &routineRig{text: &text}
	rr.workerRig = newWorkerRig(t, func(c *session.Config) {
		c.Location = time.UTC
		c.Routines = func() ([]routine.Routine, error) { return routine.Parse("routines.json", []byte(*rr.text)) }
	})
	return rr
}

// restart builds a second worker over the same store, clock and fakes, as a worker restarting would.
func (r *routineRig) restart() {
	r.t.Helper()
	r.worker = r.build(func(c *session.Config) {
		c.Location = time.UTC
		c.Routines = func() ([]routine.Routine, error) { return routine.Parse("routines.json", []byte(*r.text)) }
	})
}

func (r *routineRig) start(name string) {
	r.t.Helper()
	if err := r.worker.RoutineStart(name); err != nil {
		r.t.Fatalf("RoutineStart(%q): %v", name, err)
	}
}

// pass moves the clock on and lets the worker notice, as its regular tick does.
func (r *routineRig) pass(d time.Duration) {
	r.t.Helper()
	r.clock.Advance(d)
	r.tick()
}

func (r *routineRig) status() session.Status {
	r.t.Helper()
	st, err := r.worker.Status()
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

func (r *routineRig) top() session.Layer {
	r.t.Helper()
	st := r.status()
	if len(st.Layers) == 0 {
		r.t.Fatal("nothing is in force")
	}
	return st.Layers[len(st.Layers)-1]
}

func (r *routineRig) info() session.RoutineInfo {
	r.t.Helper()
	info, err := r.worker.RoutineInfo()
	if err != nil {
		r.t.Fatal(err)
	}
	return info
}

func (r *routineRig) shown() []string {
	var titles []string
	for _, a := range r.alerter.Shown() {
		titles = append(titles, a.Title+" | "+a.Body)
	}
	return titles
}

func TestStartingARoutineBeginsItsFirstStepAsATimedSession(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("POMO") // names are found ignoring case
	top := r.top()
	if top.Percent != 75 || !top.Timed || top.Remaining != 25*time.Minute {
		t.Errorf("top = %+v, want a 25 minute session at 75%%", top)
	}
	if want := "pomo · work (1/2) · focus"; top.Note != want {
		t.Errorf("note = %q, want %q", top.Note, want)
	}
	v := r.info().Active
	if v == nil || v.Name != "pomo" || v.Position != 0 || v.Total != 5 || v.Step != "work (1/2)" || v.Next != "break (1/2)" || v.Waiting || v.Level != 75 || v.LengthSecs != 1500 {
		t.Errorf("view = %+v", v)
	}
	if !r.suppressor.Running() {
		t.Error("the banner suppressor should run while a step with focus is in force")
	}
}

func TestARoutineGoesThroughEveryStepFinishingOnTheLongBreak(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	type want struct {
		step  string
		level int
		left  time.Duration
		after time.Duration // how long this step lasts before the next begins
	}
	for _, w := range []want{
		{"work (1/2)", 75, 25 * time.Minute, 25 * time.Minute},
		{"break (1/2)", 0, 5 * time.Minute, 5 * time.Minute},
		{"work (2/2)", 75, 25 * time.Minute, 25 * time.Minute},
		{"break (2/2)", 0, 5 * time.Minute, 5 * time.Minute},
		{"long break", 0, 15 * time.Minute, 15 * time.Minute},
	} {
		v := r.info().Active
		if v == nil || v.Step != w.step {
			t.Fatalf("expected to be at %q, got %+v", w.step, v)
		}
		top := r.top()
		if top.Percent != w.level || top.Remaining != w.left {
			t.Errorf("%s: level %d with %v left, want %d with %v", w.step, top.Percent, top.Remaining, w.level, w.left)
		}
		r.pass(w.after)
	}
	if r.info().Active != nil {
		t.Error("the run should have finished")
	}
	if len(r.status().Layers) != 0 {
		t.Errorf("nothing should be in force after the last step: %+v", r.status().Layers)
	}
	if r.suppressor.Running() {
		t.Error("the suppressor should stop when the routine is over")
	}
}

func TestStepAlertsAnnounceWhatComesNext(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(25 * time.Minute)
	shown := r.shown()
	if len(shown) != 1 || !strings.HasPrefix(shown[0], "work is over | Next: break (1/2), 5m at off") {
		t.Fatalf("alerts after work = %v", shown)
	}
	if a := r.alerter.Shown()[0]; a.Source != "Routine" {
		t.Errorf("source = %q, want Routine so the banner header says where it came from", a.Source)
	}
	r.pass(5 * time.Minute)
	if got := r.shown(); len(got) != 2 || !strings.HasPrefix(got[1], "break is over | Next: work (2/2), 25m at 75%") {
		t.Errorf("alerts after break = %v", got)
	}
}

func TestOnlyStepsThatAskForABannerAnnounceTheirEnd(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(25 * time.Minute) // work: banner
	r.pass(5 * time.Minute)  // break: banner
	r.pass(25 * time.Minute) // work again
	r.pass(5 * time.Minute)  // break again
	before := len(r.alerter.Shown())
	r.pass(15 * time.Minute) // the long break has no alert, and it is the last step
	for _, a := range r.alerter.Shown()[before:] {
		if strings.Contains(a.Title, "long break") {
			t.Errorf("the long break has no alert but showed %q", a.Title)
		}
	}
}

func TestAFinishedRoutineShowsASummaryOfWhatItHeldBack(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"r": {"steps": [{"name": "work", "level": "75%", "length": "10m"}]}}}`)
	r.start("r")
	r.postAndTick(teams, "Ana", "lunch menu for friday")     // held back
	r.postAndTick(teams, "Ops", "prod outage, need you now") // let through
	r.pass(10 * time.Minute)
	info := r.info()
	if info.Active != nil || info.Last == nil {
		t.Fatalf("info = %+v", info)
	}
	l := info.Last
	if l.Name != "r" || l.Reason != "finished" || l.Steps != 1 || l.Total != 1 || l.Held != 1 || l.Let != 1 || l.ElapsedSecs < 600 {
		t.Errorf("summary = %+v", l)
	}
	shown := r.shown()
	if got := shown[len(shown)-1]; !strings.HasPrefix(got, "r finished | 1 steps in 10m. Held back 1 notification, let through 1.") {
		t.Errorf("summary banner = %q", got)
	}
}

func TestTheSummaryBannerFollowsTheRoutinesSummarySetting(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"quiet": {"summary": ["ui"], "steps": [{"name": "work", "length": "10m"}]}}}`)
	r.start("quiet")
	r.pass(10 * time.Minute)
	if got := r.shown(); len(got) != 0 {
		t.Errorf("a routine whose summary is ui only showed a banner: %v", got)
	}
	if r.info().Last == nil {
		t.Error("the summary should still be recorded for the UI")
	}
}

func TestStartingNeedsAKnownRoutineAndOnlyOneRunsAtATime(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	if err := r.worker.RoutineStart("nope"); !errors.Is(err, session.ErrUnknownRoutine) {
		t.Errorf("unknown routine: err = %v", err)
	}
	r.start("pomo")
	if err := r.worker.RoutineStart("pomo"); !errors.Is(err, session.ErrRoutineRunning) {
		t.Errorf("a second start: err = %v, want ErrRoutineRunning", err)
	}
}

func TestRoutineCommandsWithNoRunSayNoRoutineIsRunning(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	for name, do := range map[string]func() error{"next": r.worker.RoutineNext, "back": r.worker.RoutineBack, "stop": r.worker.RoutineStop} {
		if err := do(); !errors.Is(err, session.ErrNoRoutine) {
			t.Errorf("%s with no run: err = %v, want ErrNoRoutine", name, err)
		}
	}
	if r.info().Active != nil || r.info().Last != nil {
		t.Errorf("info = %+v, want nothing", r.info())
	}
}

func TestARoutineWithNoLoaderOrABrokenFileCannotStart(t *testing.T) {
	r := newWorkerRig(t)
	if err := r.worker.RoutineStart("pomo"); !errors.Is(err, session.ErrUnknownRoutine) {
		t.Errorf("no loader: err = %v", err)
	}
	rr := newRoutineRig(t, `{"routines": {"x": {"steps": []}}}`)
	if err := rr.worker.RoutineStart("x"); err == nil || !strings.Contains(err.Error(), "routines.x.steps") {
		t.Errorf("a broken file: err = %v, want the file's problem", err)
	}
}

// askRoutine has a work step that waits for the user before the break, in the given waiting mode.
func askRoutine(waiting string) string {
	return `{"routines": {"ask": {"waiting": "` + waiting + `",
	  "steps": [
	    {"name": "work", "level": "75%", "length": "25m", "advance": "ask", "alert": "banner"},
	    {"name": "break", "level": "30%", "length": "5m"}
	  ]}}}`
}

func TestAStepThatAsksWaitsForTheUserAndStayKeepsTheLevelUntimed(t *testing.T) {
	r := newRoutineRig(t, askRoutine("stay"))
	r.start("ask")
	r.pass(25 * time.Minute)
	v := r.info().Active
	if v == nil || !v.Waiting || v.Step != "break" || v.Previous != "work" || v.Position != 1 {
		t.Fatalf("after work the run should be waiting to start the break: %+v", v)
	}
	top := r.top()
	if top.Percent != 75 || top.Timed {
		t.Errorf("top = %+v, want the work level held at 75%% with no timer", top)
	}
	if !strings.Contains(top.Note, "waiting to start break") {
		t.Errorf("note = %q", top.Note)
	}
	r.pass(2 * time.Hour) // however long the user takes, focus does not drop and nothing starts
	if v := r.info().Active; v == nil || !v.Waiting || r.top().Percent != 75 {
		t.Errorf("a long wait changed something: %+v, level %d", v, r.top().Percent)
	}
	if !r.suppressor.Running() {
		t.Error("notifications must stay held back while waiting in stay mode")
	}
	if got := r.shown(); len(got) != 1 || !strings.Contains(got[0], "shh routine next") {
		t.Errorf("the banner should say how to carry on: %v", got)
	}
	if err := r.worker.RoutineNext(); err != nil {
		t.Fatal(err)
	}
	top = r.top()
	if top.Percent != 30 || !top.Timed || top.Remaining != 5*time.Minute || len(r.status().Layers) != 1 {
		t.Errorf("after next: %+v, layers %d; want only the 5 minute break at 30%%", top, len(r.status().Layers))
	}
	if v := r.info().Active; v.Waiting || v.Step != "break" {
		t.Errorf("view = %+v", v)
	}
}

func TestWaitingInNextModePutsTheNextLevelInForceWithItsTimerHeld(t *testing.T) {
	r := newRoutineRig(t, askRoutine("next"))
	r.start("ask")
	r.pass(25 * time.Minute)
	top := r.top()
	if top.Percent != 30 || !top.Paused || top.Remaining != 5*time.Minute {
		t.Fatalf("top = %+v, want the break's level in force with its 5 minutes held", top)
	}
	r.pass(time.Hour)
	if top := r.top(); !top.Paused || top.Remaining != 5*time.Minute {
		t.Errorf("a held timer ran: %+v", top)
	}
	must(t, r.worker.RoutineNext())
	top = r.top()
	if top.Paused || top.Percent != 30 || top.Remaining != 5*time.Minute || len(r.status().Layers) != 1 {
		t.Errorf("after next: %+v, layers %d; want the same break session running", top, len(r.status().Layers))
	}
	r.pass(5 * time.Minute)
	if r.info().Active != nil {
		t.Error("the break was the last step, so the run should be over")
	}
}

func TestWaitingInBaselineModeFallsBackToWhatIsBeneath(t *testing.T) {
	r := newRoutineRig(t, askRoutine("baseline"))
	must(t, r.worker.SetBaseline(20))
	r.start("ask")
	r.pass(25 * time.Minute)
	if top := r.top(); top.Percent != 20 || top.Kind != "baseline" {
		t.Errorf("top = %+v, want the baseline", top.Level)
	}
	v := r.info().Active
	if v == nil || !v.Waiting {
		t.Fatalf("view = %+v", v)
	}
	r.pass(time.Hour)
	must(t, r.worker.RoutineNext())
	if top := r.top(); top.Percent != 30 || !top.Timed {
		t.Errorf("after next: %+v", top)
	}
}

func TestWaitingInBaselineModeWithNothingInForceKeepsTheWorkerAlive(t *testing.T) {
	r := newRoutineRig(t, askRoutine("baseline"))
	r.start("ask")
	r.pass(25 * time.Minute)
	if len(r.status().Layers) != 0 {
		t.Fatalf("layers = %+v, want nothing in force", r.status().Layers)
	}
	if !r.worker.RoutineActive() {
		t.Error("a waiting routine must count as active, or an idle worker would exit and lose it")
	}
	must(t, r.worker.RoutineStop())
	if r.worker.RoutineActive() {
		t.Error("a stopped routine is not active")
	}
}

func TestWaitingDefaultsToStay(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"d": {"steps": [{"name": "a", "level": "60%", "length": "5m", "advance": "ask"}, {"name": "b", "length": "5m"}]}}}`)
	r.start("d")
	r.pass(5 * time.Minute)
	if top := r.top(); top.Percent != 60 || top.Timed {
		t.Errorf("top = %+v, want the level held (stay is the default)", top)
	}
}

func TestNextOnARunningStepSkipsStraightToTheNextWithoutWaitingOrAnnouncing(t *testing.T) {
	r := newRoutineRig(t, askRoutine("stay"))
	r.start("ask")
	r.pass(10 * time.Minute)
	must(t, r.worker.RoutineNext()) // work asks for confirmation when it runs out, but a skip goes straight on
	v := r.info().Active
	if v == nil || v.Waiting || v.Step != "break" {
		t.Fatalf("view = %+v", v)
	}
	if top := r.top(); top.Percent != 30 || top.Remaining != 5*time.Minute || len(r.status().Layers) != 1 {
		t.Errorf("top = %+v, layers %d", top, len(r.status().Layers))
	}
	if got := r.shown(); len(got) != 0 {
		t.Errorf("a skip should not announce the end of the step: %v", got)
	}
}

func TestSkippingTheLastStepFinishesTheRun(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"one": {"steps": [{"name": "a", "level": "50%", "length": "30m"}]}}}`)
	r.start("one")
	r.pass(time.Minute)
	must(t, r.worker.RoutineNext())
	info := r.info()
	if info.Active != nil || info.Last == nil || info.Last.Reason != "finished" {
		t.Errorf("info = %+v", info)
	}
	if len(r.status().Layers) != 0 {
		t.Errorf("layers = %+v", r.status().Layers)
	}
}

func TestBackRestartsThePreviousStep(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(25 * time.Minute) // now in break (1/2)
	r.pass(time.Minute)
	must(t, r.worker.RoutineBack())
	v := r.info().Active
	if v == nil || v.Step != "work (1/2)" || v.Position != 0 {
		t.Fatalf("view = %+v, want work again", v)
	}
	if top := r.top(); top.Percent != 75 || top.Remaining != 25*time.Minute || len(r.status().Layers) != 1 {
		t.Errorf("top = %+v, layers %d; want a fresh 25 minutes", top, len(r.status().Layers))
	}
	must(t, r.worker.RoutineBack()) // at the first step it restarts that step
	if v := r.info().Active; v.Position != 0 || r.top().Remaining != 25*time.Minute {
		t.Errorf("back at the start: %+v, %v left", v, r.top().Remaining)
	}
}

func TestBackWhileWaitingRestartsTheStepThatJustEnded(t *testing.T) {
	r := newRoutineRig(t, askRoutine("stay"))
	r.start("ask")
	r.pass(25 * time.Minute) // waiting to start the break
	must(t, r.worker.RoutineBack())
	v := r.info().Active
	if v == nil || v.Waiting || v.Step != "work" {
		t.Fatalf("view = %+v, want work running again", v)
	}
	if top := r.top(); top.Percent != 75 || top.Remaining != 25*time.Minute || len(r.status().Layers) != 1 {
		t.Errorf("top = %+v, layers %d: the hold should be gone and work restarted", top, len(r.status().Layers))
	}
}

func TestStopEndsTheRunClosesItsLevelAndRecordsASummary(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.postAndTick(teams, "Ana", "lunch menu for friday")
	r.pass(3 * time.Minute)
	must(t, r.worker.RoutineStop())
	if r.worker.RoutineActive() || len(r.status().Layers) != 0 || r.suppressor.Running() {
		t.Errorf("active %v layers %+v suppressor %v", r.worker.RoutineActive(), r.status().Layers, r.suppressor.Running())
	}
	l := r.info().Last
	if l == nil || l.Reason != "stopped" || l.Steps != 1 || l.Total != 5 || l.Held != 1 || l.ElapsedSecs < 180 {
		t.Errorf("summary = %+v", l)
	}
	if got := r.shown(); len(got) != 0 {
		t.Errorf("stopping by hand needs no banner: %v", got)
	}
}

func TestSessionsStartedOrStoppedByHandAreRefusedWhileARoutineRuns(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	if _, err := r.worker.Escalate(session.Escalation{Level: 90}); !errors.Is(err, session.ErrRoutineRunning) {
		t.Errorf("a manual session: err = %v, want ErrRoutineRunning", err)
	}
	if err := r.worker.Stop(); !errors.Is(err, session.ErrRoutineRunning) {
		t.Errorf("stop: err = %v", err)
	}
	if err := r.worker.StopAll(); !errors.Is(err, session.ErrRoutineRunning) {
		t.Errorf("stop all: err = %v", err)
	}
	if v := r.info().Active; v == nil || r.top().Percent != 75 || len(r.status().Layers) != 1 {
		t.Error("a refused command must change nothing")
	}
	must(t, r.worker.RoutineStop())
	if _, err := r.worker.Escalate(session.Escalation{Level: 90}); err != nil {
		t.Errorf("after the routine ended a manual session is fine: %v", err)
	}
}

func TestBaselinePauseAndAdjustStillWorkDuringARoutine(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	must(t, r.worker.SetBaseline(20))
	must(t, r.worker.Pause())
	r.pass(time.Hour)
	if v := r.info().Active; v == nil || v.Position != 0 || r.top().Remaining != 25*time.Minute || !r.top().Paused {
		t.Fatalf("a paused step should not run out: %+v, %+v", v, r.top())
	}
	must(t, r.worker.Resume())
	must(t, r.worker.Adjust(5*time.Minute))
	r.pass(25 * time.Minute)
	if v := r.info().Active; v == nil || v.Position != 0 {
		t.Errorf("work was given 5 more minutes, so it should not have ended yet: %+v", v)
	}
	r.pass(5 * time.Minute)
	if v := r.info().Active; v == nil || v.Position != 1 {
		t.Errorf("work should end at its adjusted time: %+v", v)
	}
}

func TestARestartedWorkerCarriesOnMidStep(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(10 * time.Minute)
	r.restart()
	r.tick()
	v := r.info().Active
	if v == nil || v.Step != "work (1/2)" || r.top().Remaining != 15*time.Minute {
		t.Fatalf("after a restart: %+v, %v left", v, r.top().Remaining)
	}
	r.pass(15 * time.Minute)
	if v := r.info().Active; v == nil || v.Step != "break (1/2)" {
		t.Errorf("the restarted worker should move on at the right time: %+v", v)
	}
}

func TestARestartedWorkerCarriesOnWhileWaiting(t *testing.T) {
	r := newRoutineRig(t, askRoutine("stay"))
	r.start("ask")
	r.pass(25 * time.Minute)
	r.restart()
	r.tick()
	if v := r.info().Active; v == nil || !v.Waiting || r.top().Percent != 75 {
		t.Fatalf("a waiting run should survive a restart: %+v", v)
	}
	must(t, r.worker.RoutineNext())
	if v := r.info().Active; v == nil || v.Step != "break" || v.Waiting {
		t.Errorf("view = %+v", v)
	}
}

func TestAStepThatRanOutWhileTheWorkerWasDownMovesOnWhenItReturns(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.clock.Advance(26 * time.Minute) // the worker is down through the end of work
	r.restart()
	r.tick()
	if v := r.info().Active; v == nil || v.Step != "break (1/2)" {
		t.Errorf("view = %+v, want the break to have started on the first tick", v)
	}
}

func TestARoutineThatVanishesFromTheFileEndsTheRunWithANote(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	*r.text = `{"routines": {}}`
	r.pass(time.Minute)
	if r.info().Active != nil {
		t.Error("the run should have ended")
	}
	if l := r.info().Last; l == nil || l.Reason != "missing" {
		t.Errorf("last = %+v", l)
	}
	if len(r.errs) == 0 || !strings.Contains(r.errs[len(r.errs)-1].Error(), "pomo") {
		t.Errorf("the worker should say why: %v", r.errs)
	}
	if len(r.status().Layers) != 0 {
		t.Errorf("its level should be closed: %+v", r.status().Layers)
	}
}

func TestEditingARoutineDuringARunFollowsTheNewSteps(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	*r.text = strings.Replace(pomodoroLite, `"length": "5m"`, `"length": "10m"`, 1)
	r.pass(25 * time.Minute)
	if top := r.top(); top.Remaining != 10*time.Minute {
		t.Errorf("the break should take the edited length, got %v", top.Remaining)
	}
	*r.text = `{"routines": {"pomo": {"steps": [{"name": "only", "length": "5m"}]}}}`
	r.pass(time.Minute)
	if r.info().Active != nil {
		t.Error("a run whose position is past the end of the edited routine should end")
	}
}

func TestAStepEndedByHandEndsTheRunAsStopped(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	must(t, r.store.CloseLevel(r.top().ID, r.clock.Now().Add(time.Minute))) // closed early, not by running out
	r.pass(2 * time.Minute)
	if l := r.info().Last; r.info().Active != nil || l == nil || l.Reason != "stopped" {
		t.Errorf("info = %+v", r.info())
	}
}

func TestTheSuppressorFollowsEachStepsLevel(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	if !r.suppressor.Running() {
		t.Fatal("work holds notifications back")
	}
	r.pass(25 * time.Minute)
	if r.suppressor.Running() {
		t.Error("the break is at off, so banners should be left alone")
	}
	r.pass(5 * time.Minute)
	if !r.suppressor.Running() {
		t.Error("the next work step should hold them back again")
	}
}

func TestAStepStartsAtTheTickThatNoticedTheEndSoLengthsAreWholeSteps(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.clock.Advance(25*time.Minute + 1500*time.Millisecond)
	r.tick()
	if top := r.top(); top.Remaining != 5*time.Minute {
		t.Errorf("the break should get its full length from when it began, got %v", top.Remaining)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// baseline returns the percent of the baseline layer, or 0 if there is none.
func (r *routineRig) baseline() int {
	r.t.Helper()
	for _, l := range r.status().Layers {
		if l.Kind == store.LevelBaseline {
			return l.Percent
		}
	}
	return 0
}

const baselineRoutine = `{"routines": {"day": {"steps": [
  "baseline 30%",
  "75% 25m work",
  "baseline 50%",
  "off 5m break --ask",
  "baseline off",
  "75% 10m wrap up",
  "baseline 20%"
]}}}`

func TestBaselineStepsApplyWhenTheRunReachesThemAndTakeNoTime(t *testing.T) {
	r := newRoutineRig(t, baselineRoutine)
	r.start("day")
	if got := r.baseline(); got != 30 {
		t.Errorf("a baseline step at the start should apply at once: baseline = %d", got)
	}
	if v := r.info().Active; v == nil || v.Step != "work" || v.Position != 1 || r.top().Percent != 75 {
		t.Errorf("the run should be on the first session, past the baseline step: %+v", v)
	}
	r.pass(25 * time.Minute)
	if got := r.baseline(); got != 50 {
		t.Errorf("the baseline step after work should have applied: baseline = %d", got)
	}
	if v := r.info().Active; v == nil || v.Step != "break" || r.top().Percent != 0 || !r.top().Timed {
		t.Errorf("after work comes the break: %+v", v)
	}
	r.pass(5 * time.Minute) // the break asks: baseline off applies, then the run waits on the last session
	if got := r.baseline(); got != 0 {
		t.Errorf("baseline off should apply before waiting: baseline = %d", got)
	}
	v := r.info().Active
	if v == nil || !v.Waiting || v.Step != "wrap up" || v.Position != 5 {
		t.Fatalf("the run should wait to start wrap up: %+v", v)
	}
	if err := r.worker.RoutineNext(); err != nil {
		t.Fatal(err)
	}
	r.pass(10 * time.Minute)
	if r.info().Active != nil {
		t.Error("the run should be over")
	}
	if got := r.baseline(); got != 20 {
		t.Errorf("a baseline step after the last session still applies before the run ends: baseline = %d", got)
	}
}

func TestARoutineOfOnlyBaselineStepsSetsThemAndEndsAtOnce(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"set": {"steps": ["baseline 30%", "baseline 45%"]}}}`)
	r.start("set")
	if r.info().Active != nil {
		t.Error("with no sessions the run is over as soon as it starts")
	}
	if got := r.baseline(); got != 45 {
		t.Errorf("baseline = %d, want the last one, 45", got)
	}
}

func TestBackSkipsOverBaselineStepsToTheSessionBefore(t *testing.T) {
	r := newRoutineRig(t, baselineRoutine)
	r.start("day")
	r.pass(25 * time.Minute) // now on the break, position 3
	if err := r.worker.RoutineBack(); err != nil {
		t.Fatal(err)
	}
	if v := r.info().Active; v == nil || v.Step != "work" || r.top().Percent != 75 {
		t.Errorf("back from the break should restart work, not stop on the baseline step: %+v", v)
	}
}

func TestANextOnASessionBeforeABaselineStepAppliesTheBaseline(t *testing.T) {
	r := newRoutineRig(t, baselineRoutine)
	r.start("day")
	if err := r.worker.RoutineNext(); err != nil { // skip work
		t.Fatal(err)
	}
	if got := r.baseline(); got != 50 {
		t.Errorf("skipping work should still apply the baseline step after it: baseline = %d", got)
	}
}

func TestTheBannerOfAStepThatAsksCarriesAStartNextButton(t *testing.T) {
	r := newRoutineRig(t, askRoutine("stay"))
	r.start("ask")
	r.pass(25 * time.Minute)
	shown := r.alerter.Shown()
	if len(shown) != 1 || len(shown[0].Actions) != 1 || shown[0].Actions[0].ID != "next" || shown[0].Actions[0].Label != "Start next" || shown[0].ActionToken == "" {
		t.Fatalf("shown = %+v", shown)
	}
	if err := r.worker.RoutineAction(shown[0].ActionToken, "next"); err != nil {
		t.Fatal(err)
	}
	if v := r.info().Active; v == nil || v.Waiting || v.Step != "break" || r.top().Percent != 30 {
		t.Errorf("the button should start the break: %+v", v)
	}
}

func TestTheBannerOfAStepThatMovedOnItselfCarriesASkipButton(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(25 * time.Minute) // work ends, its banner says the break began
	shown := r.alerter.Shown()
	if len(shown) != 1 || len(shown[0].Actions) != 1 || shown[0].Actions[0].ID != "skip" {
		t.Fatalf("shown = %+v", shown)
	}
	if err := r.worker.RoutineAction(shown[0].ActionToken, "skip"); err != nil {
		t.Fatal(err)
	}
	if v := r.info().Active; v == nil || v.Step != "work (2/2)" {
		t.Errorf("skip should end the break that had begun: %+v", v)
	}
}

func TestABannerButtonLeftOnScreenCannotActOnALaterStep(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(25 * time.Minute)
	token := r.alerter.Shown()[0].ActionToken
	r.pass(5 * time.Minute) // the break ends by itself and the second work begins
	if err := r.worker.RoutineAction(token, "skip"); err != nil {
		t.Fatal(err)
	}
	if v := r.info().Active; v == nil || v.Step != "work (2/2)" || r.top().Remaining < 24*time.Minute {
		t.Errorf("an old banner's button must do nothing: %+v, %v left", v, r.top().Remaining)
	}
	for _, bad := range []string{"", "nonsense", "1:x", "999:0"} {
		if err := r.worker.RoutineAction(bad, "skip"); err != nil {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	if err := r.worker.RoutineAction(token, "unknown-button"); err != nil {
		t.Error(err)
	}
	r.worker.RoutineStop()
	if err := r.worker.RoutineAction(token, "skip"); err != nil {
		t.Errorf("a button after the run ended: %v", err)
	}
}

func TestTheSummaryCarriesWhereTheRoutineWantsItShown(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"quiet": {"summary": ["menu", "ui"], "steps": ["off 10m x"]}, "loud": {"steps": ["off 10m x"]}}}`)
	r.start("quiet")
	r.pass(10 * time.Minute)
	last := r.worker.RoutineLast()
	if last == nil || last.Name != "quiet" || !last.Shows("menu") || !last.Shows("ui") || last.Shows("banner") {
		t.Fatalf("last = %+v", last)
	}
	if last.Line() != "quiet finished: 1 steps in 10m. Held back 0 notifications, let through 0." {
		t.Errorf("line = %q", last.Line())
	}
	r.start("loud")
	if r.worker.RoutineLast().Name != "quiet" {
		t.Error("while a run is going, the last summary is still the one that ended")
	}
	r.pass(10 * time.Minute)
	if l := r.worker.RoutineLast(); l.Name != "loud" || !l.Shows("banner") || !l.Shows("ui") || l.Shows("menu") {
		t.Errorf("defaults: %+v", l)
	}
}

func TestNoRunHasEndedMeansNoLastSummary(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	if r.worker.RoutineLast() != nil {
		t.Error("nothing has run")
	}
	r.start("pomo")
	if r.worker.RoutineLast() != nil {
		t.Error("a run that has not ended has no summary")
	}
}

// ---- a session of the user's own on top of a routine ("continue after") ----

func (r *routineRig) interrupt(d time.Duration) {
	r.t.Helper()
	if _, err := r.worker.Escalate(session.Escalation{Level: 90, Duration: d, Note: "a call", ContinueRoutine: true}); err != nil {
		r.t.Fatal(err)
	}
}

func TestAUserSessionOnTopHoldsTheStepAndItCarriesOnAfterwards(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(10 * time.Minute) // 15 minutes of work left
	r.interrupt(20 * time.Minute)
	if v := r.info().Active; v == nil || !v.Interrupted || v.Step != "work (1/2)" {
		t.Fatalf("view = %+v", v)
	}
	if top := r.top(); top.Percent != 90 || top.Remaining != 20*time.Minute {
		t.Errorf("top = %+v", top)
	}
	r.pass(20 * time.Minute) // the user's session runs out; the step must not have used any of its time
	v := r.info().Active
	if v == nil || v.Interrupted || v.Step != "work (1/2)" {
		t.Fatalf("after the session the routine should carry on in the same step: %+v", v)
	}
	if top := r.top(); top.Percent != 75 || top.Paused || top.Remaining != 15*time.Minute {
		t.Errorf("top = %+v, want the work step resumed with its 15 minutes", top)
	}
	r.pass(15 * time.Minute)
	if v := r.info().Active; v == nil || v.Step != "break (1/2)" {
		t.Errorf("then the routine goes on as usual: %+v", v)
	}
}

func TestStopEndsTheUsersSessionAndTheRoutineResumes(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(5 * time.Minute)
	r.interrupt(0) // until stopped
	if top := r.top(); top.Percent != 90 || top.Timed {
		t.Fatalf("top = %+v", top)
	}
	if err := r.worker.Stop(); err != nil {
		t.Fatalf("stop should end the user's own session: %v", err)
	}
	r.pass(time.Second)
	if v := r.info().Active; v == nil || v.Interrupted || r.top().Percent != 75 || r.top().Remaining != 20*time.Minute {
		t.Errorf("view %+v, top %+v: the routine should carry on with its 20 minutes", v, r.top())
	}
	if err := r.worker.Stop(); !errors.Is(err, session.ErrRoutineRunning) {
		t.Errorf("with nothing of the user's on top, stop is refused again: %v", err)
	}
}

func TestSeveralUserSessionsOnTopAreAllWaitedFor(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.interrupt(10 * time.Minute)
	r.interrupt(30 * time.Minute)
	r.pass(10 * time.Minute) // the first one has run out, but the second is still on top
	if v := r.info().Active; v == nil || !v.Interrupted {
		t.Fatalf("a session is still on top: %+v", v)
	}
	r.pass(20 * time.Minute)
	if v := r.info().Active; v == nil || v.Interrupted || r.top().Percent != 75 {
		t.Errorf("view %+v, top %+v", v, r.top())
	}
}

func TestNextWhileInterruptedEndsTheUsersSessionAndMovesOn(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.interrupt(30 * time.Minute)
	if err := r.worker.RoutineNext(); err != nil {
		t.Fatal(err)
	}
	v := r.info().Active
	if v == nil || v.Interrupted || v.Step != "break (1/2)" || len(r.status().Layers) != 1 {
		t.Errorf("view %+v, layers %d: the user's session should be gone and the break begun", v, len(r.status().Layers))
	}
}

func TestBackWhileInterruptedRestartsTheStepWithoutTheUsersSession(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(25 * time.Minute) // on the break
	r.interrupt(30 * time.Minute)
	if err := r.worker.RoutineBack(); err != nil {
		t.Fatal(err)
	}
	if v := r.info().Active; v == nil || v.Interrupted || v.Step != "work (1/2)" || len(r.status().Layers) != 1 {
		t.Errorf("view %+v layers %d", v, len(r.status().Layers))
	}
}

func TestStoppingTheRoutineWhileInterruptedLeavesTheUsersSession(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.interrupt(30 * time.Minute)
	if err := r.worker.RoutineStop(); err != nil {
		t.Fatal(err)
	}
	if r.info().Active != nil {
		t.Error("the run should have ended")
	}
	if st := r.status(); len(st.Layers) != 1 || st.Layers[0].Percent != 90 {
		t.Errorf("layers = %+v, want the user's session to carry on alone", st.Layers)
	}
}

func TestAUserSessionOnAWaitingRoutineDoesNotReplaceItsHold(t *testing.T) {
	r := newRoutineRig(t, askRoutine("stay"))
	r.start("ask")
	r.pass(25 * time.Minute) // waiting, with the untimed hold in force
	r.interrupt(0)           // untimed too: it must not replace the hold
	if st := r.status(); len(st.Layers) != 2 {
		t.Fatalf("layers = %d, want the hold and the user's session", len(st.Layers))
	}
	if v := r.info().Active; v == nil || !v.Waiting {
		t.Fatalf("view = %+v", v)
	}
	if err := r.worker.Stop(); err != nil {
		t.Fatal(err)
	}
	r.pass(time.Second)
	if v := r.info().Active; v == nil || !v.Waiting || v.Interrupted || r.top().Percent != 75 {
		t.Errorf("the routine should still be waiting on its hold: %+v, top %+v", v, r.top())
	}
}

func TestAnInterruptionSurvivesAWorkerRestart(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(5 * time.Minute)
	r.interrupt(10 * time.Minute)
	r.restart()
	if v := r.info().Active; v == nil || !v.Interrupted {
		t.Fatalf("view = %+v", v)
	}
	r.pass(10 * time.Minute)
	if v := r.info().Active; v == nil || v.Interrupted || r.top().Remaining != 20*time.Minute {
		t.Errorf("view %+v, top %+v", v, r.top())
	}
}

func TestWithoutContinueRoutineASessionIsStillRefused(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	if _, err := r.worker.Escalate(session.Escalation{Level: 90, Duration: time.Minute}); !errors.Is(err, session.ErrRoutineRunning) {
		t.Errorf("err = %v", err)
	}
	if v := r.info().Active; v.Interrupted {
		t.Error("a refused session must not leave the routine marked as interrupted")
	}
}

// ---- a session queued for when the routine ends ----

func (r *routineRig) queue(level int, d time.Duration, note string) {
	r.t.Helper()
	if _, err := r.worker.Escalate(session.Escalation{Level: level, Duration: d, Note: note, QueueAfterRoutine: true}); err != nil {
		r.t.Fatal(err)
	}
}

func TestAQueuedSessionStartsWhenTheRoutineFinishes(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"two": {"steps": ["75% 10m a", "off 5m b"]}}}`)
	r.start("two")
	r.queue(90, 30*time.Minute, "deep block")
	if st := r.status(); len(st.Layers) != 1 || st.Layers[0].Percent != 75 {
		t.Fatalf("queueing must not start anything now: %+v", st.Layers)
	}
	if v := r.info().Active; v == nil || v.Queued != `90% (deep) for 30m, "deep block"` {
		t.Fatalf("view = %+v", v)
	}
	r.pass(10 * time.Minute)
	r.pass(5 * time.Minute) // the routine is over
	if r.info().Active != nil {
		t.Fatal("the routine should have finished")
	}
	top := r.top()
	if top.Percent != 90 || top.Remaining != 30*time.Minute || top.Note != "deep block" {
		t.Errorf("top = %+v, want the queued session", top)
	}
	if !r.suppressor.Running() {
		t.Error("the queued session holds notifications back")
	}
	r.pass(30 * time.Minute)
	if len(r.status().Layers) != 0 {
		t.Error("and it ends as any session does")
	}
}

func TestAQueuedSessionStartsWhenTheRoutineIsStoppedToo(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.queue(90, 0, "until I stop")
	if err := r.worker.RoutineStop(); err != nil {
		t.Fatal(err)
	}
	if top := r.top(); top.Percent != 90 || top.Timed || len(r.status().Layers) != 1 {
		t.Errorf("top = %+v", top)
	}
}

func TestAQueuedSessionStartsWhenTheRoutineGoesMissing(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.queue(50, 10*time.Minute, "later")
	*r.text = `{"routines": {}}`
	r.pass(time.Second)
	if r.info().Active != nil || r.top().Percent != 50 {
		t.Errorf("a routine that vanished still ends the run, and the queue starts")
	}
}

func TestQueuingAgainReplacesTheQueuedSessionAndUnqueueDropsIt(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.queue(90, 30*time.Minute, "first")
	r.queue(50, 10*time.Minute, "second")
	if v := r.info().Active; !strings.Contains(v.Queued, "second") || strings.Contains(v.Queued, "first") {
		t.Errorf("queued = %q", v.Queued)
	}
	if err := r.worker.RoutineUnqueue(); err != nil {
		t.Fatal(err)
	}
	if r.info().Active.Queued != "" {
		t.Error("unqueue should drop it")
	}
	r.worker.RoutineStop()
	if len(r.status().Layers) != 0 {
		t.Errorf("nothing should start: %+v", r.status().Layers)
	}
	if err := r.worker.RoutineUnqueue(); !errors.Is(err, session.ErrNoRoutine) {
		t.Errorf("err = %v", err)
	}
}

func TestABadQueuedSessionIsRefusedNowNotWhenItsTimeComes(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	for _, e := range []session.Escalation{
		{Level: 150, QueueAfterRoutine: true},
		{Level: 50, Duration: -time.Minute, QueueAfterRoutine: true},
		{Level: 50, Alert: "banner", QueueAfterRoutine: true}, // an alert needs a length
		{Level: 50, Alert: "loud", Duration: time.Minute, QueueAfterRoutine: true},
	} {
		if _, err := r.worker.Escalate(e); !errors.Is(err, session.ErrInvalid) {
			t.Errorf("%+v: err = %v", e, err)
		}
	}
	if r.info().Active.Queued != "" {
		t.Error("nothing bad should have been kept")
	}
}

func TestAQueuedSessionSurvivesARestartAndKeepsItsAlert(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"one": {"steps": ["off 10m a"]}}}`)
	r.start("one")
	if _, err := r.worker.Escalate(session.Escalation{Level: 90, Duration: 20 * time.Minute, Alert: "banner", QueueAfterRoutine: true}); err != nil {
		t.Fatal(err)
	}
	r.restart()
	r.pass(10 * time.Minute)
	r.pass(20 * time.Minute) // the queued session ends and announces itself
	got := r.shown()
	found := false
	for _, g := range got {
		found = found || strings.Contains(g, "Focus session over")
	}
	if !found {
		t.Errorf("the queued session's own alert should be given: %v", got)
	}
}

func TestQueueingWithNoRoutineRunningStartsTheSessionAsUsual(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	if _, err := r.worker.Escalate(session.Escalation{Level: 90, Duration: time.Minute, QueueAfterRoutine: true}); err != nil {
		t.Fatal(err)
	}
	if r.top().Percent != 90 {
		t.Error("with no routine there is nothing to wait for")
	}
}

// ---- steps that end at a time of day, calls, and going round forever (the rig's clock starts at 09:00 UTC) ----

func TestAnUntilStepLastsUntilThatTimeAndTheNextStepFollowsOnTime(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"day": {"steps": ["90% until 10:30 deep", "off until 10:45 break", "50% until 12:00 email"]}}}`)
	r.start("day")
	if top := r.top(); top.Percent != 90 || top.Remaining != 90*time.Minute {
		t.Fatalf("top = %+v, want 90 minutes (09:00 to 10:30)", top)
	}
	if v := r.info().Active; v.LengthSecs != 90*60 {
		t.Errorf("view length = %d", v.LengthSecs)
	}
	r.pass(90 * time.Minute)
	if top := r.top(); top.Percent != 0 || top.Remaining != 15*time.Minute {
		t.Errorf("top = %+v, want the 15 minute break", top)
	}
	r.pass(15 * time.Minute)
	if top := r.top(); top.Percent != 50 || top.Remaining != 75*time.Minute {
		t.Errorf("top = %+v", top)
	}
}

func TestAStepWhoseTimeHasGoneByIsSkippedSoARunStartedLateJoinsInProgress(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"day": {"steps": ["90% until 09:30 deep", "off until 09:45 break", "50% until 12:00 email"]}}}`)
	r.clock.Advance(40 * time.Minute) // 09:40
	r.start("day")
	if v := r.info().Active; v == nil || v.Step != "break" || r.top().Remaining != 5*time.Minute {
		t.Errorf("view %+v, top %+v: it should join in the break", v, r.top())
	}
}

func TestARoutineWhoseStepsHaveAllGoneByEndsAtOnce(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"early": {"steps": ["90% until 08:00 deep", "baseline 30%"]}}}`)
	r.start("early")
	if r.info().Active != nil {
		t.Error("nothing is left to do: the run is over")
	}
	if got := r.baseline(); got != 30 {
		t.Errorf("baseline = %d: the baseline step after the skipped one still applies", got)
	}
}

func TestAnUntilStepReadsTheClockInTheWorkersTimeZone(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"x": {"steps": ["75% until 09:30 a"]}}}`)
	r.start("x")
	if r.top().Remaining != 30*time.Minute {
		t.Errorf("remaining = %v", r.top().Remaining)
	}
}

func TestWaitingNextHoldsNothingForAnUntilStepAndWorksItOutWhenItStarts(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"w": {"waiting": "next", "steps": ["75% 10m a --ask", "50% until 12:00 b"]}}}`)
	r.start("w")
	r.pass(10 * time.Minute) // 09:10, waiting
	v := r.info().Active
	if v == nil || !v.Waiting || r.top().Percent != 75 || r.top().Timed {
		t.Fatalf("view %+v top %+v: an until step cannot hold a countdown, so the hold stays at the level that ended", v, r.top())
	}
	r.pass(20 * time.Minute) // 09:30
	if err := r.worker.RoutineNext(); err != nil {
		t.Fatal(err)
	}
	if top := r.top(); top.Percent != 50 || top.Remaining != 150*time.Minute {
		t.Errorf("top = %+v, want 09:30 to 12:00", top)
	}
}

func TestACallRunsTheCalledRoutinesStepsInPlace(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {
	  "inner": {"repeat": 2, "steps": ["75% 10m a", "off 5m b"]},
	  "outer": {"steps": ["run inner", "50% 20m c"]}}}`)
	r.start("outer")
	var steps []string
	for i := 0; i < 5; i++ {
		v := r.info().Active
		if v == nil {
			t.Fatalf("the run ended early after %v", steps)
		}
		steps = append(steps, v.Step)
		r.pass(time.Duration(r.top().Remaining))
	}
	want := "inner › a (1/2),inner › b (1/2),inner › a (2/2),inner › b (2/2),c"
	if got := strings.Join(steps, ","); got != want {
		t.Errorf("steps = %s\nwant    %s", got, want)
	}
	if r.info().Active != nil {
		t.Error("the run should be over")
	}
}

func TestARoutineThatRepeatsForeverGoesRoundAgainUntilStopped(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"loop": {"repeat": "forever", "steps": ["75% 10m work", "off 5m break"]}}}`)
	r.start("loop")
	var seen []string
	for i := 0; i < 7; i++ {
		v := r.info().Active
		if v == nil {
			t.Fatalf("a routine that repeats forever ended after %v", seen)
		}
		seen = append(seen, v.Step)
		r.pass(r.top().Remaining)
	}
	if got := strings.Join(seen, ","); got != "work,break,work,break,work,break,work" {
		t.Errorf("seen = %s", got)
	}
	if v := r.info().Active; v == nil || v.Position != 1 || v.Total != 2 {
		t.Errorf("view = %+v: the position stays inside one round", v)
	}
	if err := r.worker.RoutineStop(); err != nil {
		t.Fatal(err)
	}
}

func TestForeverWithAskWaitsAtTheEndOfEachRound(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"loop": {"repeat": "forever", "steps": ["75% 10m work", "off 5m break --ask"]}}}`)
	r.start("loop")
	r.pass(10 * time.Minute)
	r.pass(5 * time.Minute) // the break ends and asks
	v := r.info().Active
	if v == nil || !v.Waiting || v.Step != "work" || v.Position != 0 {
		t.Fatalf("view = %+v: it should wait to start the next round", v)
	}
	if err := r.worker.RoutineNext(); err != nil {
		t.Fatal(err)
	}
	if v := r.info().Active; v.Waiting || v.Step != "work" {
		t.Errorf("view = %+v", v)
	}
}

func TestForeverWithEveryStepGoneByEndsInsteadOfSpinning(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"loop": {"repeat": "forever", "steps": ["75% until 08:00 work"]}}}`)
	r.start("loop")
	if r.info().Active != nil {
		t.Error("a round with nothing that can start ends the run")
	}
}

func TestACalledRoutineEditedMidRunIsFollowedAsEdited(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"inner": {"steps": ["75% 10m a"]}, "outer": {"steps": ["run inner", "50% 20m c"]}}}`)
	r.start("outer")
	*r.text = `{"routines": {"inner": {"steps": ["75% 10m a", "off 5m extra"]}, "outer": {"steps": ["run inner", "50% 20m c"]}}}`
	r.pass(10 * time.Minute)
	if v := r.info().Active; v == nil || v.Step != "inner › extra" {
		t.Errorf("view = %+v", v)
	}
}
