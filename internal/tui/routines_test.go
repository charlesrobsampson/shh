package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/session"
)

// fakeRoutines records the routine commands and answers with whatever it has been given.
type fakeRoutines struct {
	calls []string
	err   error
	rig   *rig
	// onRun, when set, changes the status the worker will report after a run starts.
	onRun func()
}

func (f *fakeRoutines) answer(call string) (session.RoutineInfo, error) {
	f.calls = append(f.calls, call)
	return session.RoutineInfo{}, f.err
}
func (f *fakeRoutines) RoutineRun(name string) (session.RoutineInfo, error) {
	if f.onRun != nil && f.err == nil {
		f.onRun()
	}
	return f.answer("run " + name)
}
func (f *fakeRoutines) RoutineNext() (session.RoutineInfo, error) { return f.answer("next") }
func (f *fakeRoutines) RoutineBack() (session.RoutineInfo, error) { return f.answer("back") }
func (f *fakeRoutines) RoutineStop() (session.RoutineInfo, error) { return f.answer("stop") }

const tuiRoutines = `{"routines": {
  "pomo": {"description": "short and sweet", "repeat": 2, "steps": ["75% 25m work --alert banner", "off 5m break"], "finish": ["off 15m long break"]},
  "deep": {"steps": ["baseline 30%", "90% 90m deep work --ask"]}
}}`

func routinesRig(t *testing.T) (*rig, *fakeRoutines) {
	t.Helper()
	r := newRig(t)
	fr := &fakeRoutines{rig: r}
	r.m.cfg.Routines = fr
	r.m.cfg.Saved = func() ([]routine.Routine, error) { return routine.Parse("routines.json", []byte(tuiRoutines)) }
	r.worker.err = nil
	r.worker.view = session.StatusView{}
	r.refresh()
	r.press("f4")
	return r, fr
}

func TestTheRoutinesTabListsSavedRoutinesWithTheirStepsAsCommands(t *testing.T) {
	r, _ := routinesRig(t)
	v := r.view()
	for _, want := range []string{"No routine is running", "Saved routines", "pomo", "short and sweet", "75% 25m work --alert banner -> off 5m break, 2 times -> off 15m long break", "deep", "baseline 30% -> 90% 1h30m deep work --ask"} {
		if !strings.Contains(v, want) {
			t.Errorf("the Routines tab should show %q:\n%s", want, v)
		}
	}
	if !strings.Contains(r.m.messageLine(), "Enter start") || !strings.Contains(r.m.messageLine(), "$ shh routine run pomo") {
		t.Errorf("the footer should say which keys work and show the command for the selected routine: %q", r.m.messageLine())
	}
}

func TestTheRoutinesTabSaysWhenThereAreNoneOrTheFileIsBroken(t *testing.T) {
	r, _ := routinesRig(t)
	r.m.cfg.Saved = func() ([]routine.Routine, error) { return nil, nil }
	r.m.saved = nil
	r.refresh()
	if v := r.view(); !strings.Contains(v, "none yet") || !strings.Contains(v, "shh routine example") {
		t.Errorf("an empty file should say how to start:\n%s", v)
	}
	r.press("enter")
	if !strings.Contains(r.m.notice, "no saved routine") {
		t.Errorf("notice = %q", r.m.notice)
	}
	r.m.cfg.Saved = func() ([]routine.Routine, error) { return nil, errors.New("routines.json:3: unknown key") }
	r.refresh()
	if v := r.view(); !strings.Contains(v, "cannot read the routines file: routines.json:3: unknown key") {
		t.Errorf("a broken file should say why:\n%s", v)
	}
}

func TestABrokenFileKeepsTheLastGoodListOnScreen(t *testing.T) {
	r, _ := routinesRig(t)
	r.m.cfg.Saved = func() ([]routine.Routine, error) { return nil, errors.New("oops") }
	r.refresh()
	if v := r.view(); !strings.Contains(v, "pomo") || !strings.Contains(v, "oops") {
		t.Errorf("a typo mid-edit should not empty the list:\n%s", v)
	}
}

func TestEnterStartsTheSelectedRoutineAndShowsTheCommand(t *testing.T) {
	r, fr := routinesRig(t)
	r.press("j", "enter") // the file lists deep before pomo? whichever is second
	name := r.m.saved[1].Name
	if len(fr.calls) != 1 || fr.calls[0] != "run "+name {
		t.Fatalf("calls = %v, want a run of %s", fr.calls, name)
	}
	if !strings.Contains(r.m.messageLine(), "$ shh routine run "+name) {
		t.Errorf("the message line should show the equivalent command: %q", r.m.messageLine())
	}
	if r.ensured == 0 {
		t.Error("starting a routine needs a worker, so one should have been ensured")
	}
}

func TestNextBackAndStopSteerTheRunningRoutine(t *testing.T) {
	r, fr := routinesRig(t)
	r.worker.view = session.StatusView{Routine: &session.RoutineView{Name: "pomo", Position: 1, Total: 5, Step: "break (1/2)", Next: "work (2/2)", Level: 0, LengthSecs: 300}}
	r.refresh()
	v := r.view()
	if !strings.Contains(v, "pomo: break (1/2) (step 2 of 5), then work (2/2)") || !strings.Contains(v, "5m") {
		t.Errorf("the running routine should be described:\n%s", v)
	}
	r.press("n", "b", "x")
	if got := strings.Join(fr.calls, ","); got != "next,back,stop" {
		t.Errorf("calls = %s", got)
	}
}

func TestAWaitingRoutineSaysWhatItWaitsFor(t *testing.T) {
	r, _ := routinesRig(t)
	r.worker.view = session.StatusView{Routine: &session.RoutineView{Name: "pomo", Position: 2, Total: 5, Step: "work (2/2)", Previous: "break (1/2)", Waiting: true}}
	r.refresh()
	if v := r.view(); !strings.Contains(v, "waiting for you to start work (2/2)") || !strings.Contains(v, "press n") {
		t.Errorf("view:\n%s", v)
	}
}

func TestRoutineKeysSayWhatIsWrongInsteadOfFailingQuietly(t *testing.T) {
	r, fr := routinesRig(t)
	r.worker.err = session.ErrNotRunning
	r.refresh()
	r.press("n")
	if !strings.Contains(r.m.notice, "not running") || len(fr.calls) != 0 {
		t.Errorf("with no worker, n should say so and send nothing: %q %v", r.m.notice, fr.calls)
	}
	r.worker.err = nil
	r.refresh()
	fr.err = session.ErrNoRoutine
	r.press("n")
	if !strings.Contains(r.m.notice, "no routine is running") {
		t.Errorf("notice = %q", r.m.notice)
	}
	fr.err = session.ErrRoutineRunning
	r.press("enter")
	if !strings.Contains(r.m.notice, "already running") {
		t.Errorf("notice = %q", r.m.notice)
	}
	r.m.cfg.Routines = nil
	r.press("n")
	if !strings.Contains(r.m.notice, "not available") {
		t.Errorf("notice = %q", r.m.notice)
	}
}

func TestMotionKeysMoveBetweenRoutinesAndNotificationKeysDoNothingHere(t *testing.T) {
	r, fr := routinesRig(t)
	r.press("G")
	if r.m.routineCursor != 1 {
		t.Errorf("G should go to the last routine: %d", r.m.routineCursor)
	}
	r.press("g")
	if r.m.routineCursor != 0 {
		t.Errorf("g should go to the first: %d", r.m.routineCursor)
	}
	r.press("5", "j")
	if r.m.routineCursor != 1 {
		t.Errorf("a count should stay inside the list: %d", r.m.routineCursor)
	}
	r.press("/")
	if r.m.prompt != nil {
		t.Error("there is nothing to filter on this tab")
	}
	r.press("y", "t", "u", "e")
	if r.m.prompt != nil || len(r.trainer.requests) != 0 || len(fr.calls) != 0 {
		t.Errorf("training keys must do nothing here: prompt %v, trained %v", r.m.prompt, r.trainer.requests)
	}
}

func TestAManualSessionAsksBeforeEndingARunningRoutine(t *testing.T) {
	r, fr := routinesRig(t)
	r.press("f1")
	r.control.err = session.ErrRoutineRunning
	r.press("s")
	r.typeText("75% 25m writing")
	r.press("enter")
	if r.m.prompt == nil || r.m.prompt.kind != promptConfirmEndRoutine {
		t.Fatalf("a refused session should ask whether to end the routine: prompt %+v, notice %q", r.m.prompt, r.m.notice)
	}
	if !strings.Contains(r.view(), "a routine is running. End it and do this?") {
		t.Errorf("the question should be on screen:\n%s", r.view())
	}
	r.control.err = nil
	r.control.calls = nil
	r.press("y")
	if got := strings.Join(fr.calls, ","); got != "stop" {
		t.Errorf("yes should end the routine: %s", got)
	}
	if len(r.control.calls) != 1 || !strings.HasPrefix(r.control.calls[0], "focus 75 25m0s") {
		t.Errorf("and then do what was asked: %v", r.control.calls)
	}
	if r.m.prompt != nil {
		t.Error("the question should be closed")
	}
}

func TestAnsweringNoLeavesTheRoutineAndDoesNotStartTheSession(t *testing.T) {
	r, fr := routinesRig(t)
	r.press("f1")
	r.control.err = session.ErrRoutineRunning
	r.press("x")
	if r.m.prompt == nil {
		t.Fatal("x should ask")
	}
	r.control.calls = nil
	r.press("n")
	if r.m.prompt != nil || len(fr.calls) != 0 || r.m.afterRoutine != nil {
		t.Errorf("no should change nothing: prompt %v calls %v", r.m.prompt, fr.calls)
	}
	if len(r.control.calls) != 0 {
		t.Errorf("nothing more should be sent: %v", r.control.calls)
	}
}

func TestWithoutRoutineControlARefusedSessionJustSaysSo(t *testing.T) {
	r := newRig(t)
	r.worker.err = nil
	r.refresh()
	r.press("f1")
	r.control.err = session.ErrRoutineRunning
	r.press("x")
	if r.m.prompt != nil || !strings.Contains(r.m.notice, "routine is running") {
		t.Errorf("prompt %v notice %q", r.m.prompt, r.m.notice)
	}
}

func TestTheFocusTabShowsTheRunningRoutine(t *testing.T) {
	r, _ := routinesRig(t)
	r.worker.view = session.StatusView{Level: 75, Layers: []session.LayerView{{Kind: "session", Percent: 75, Timed: true, RemainingSecs: 600}},
		Routine: &session.RoutineView{Name: "pomo", Position: 0, Total: 5, Step: "work (1/2)", Next: "break (1/2)"}}
	r.refresh()
	r.press("f1")
	if v := r.view(); !strings.Contains(v, "Routine pomo: work (1/2) (step 1 of 5), then break (1/2)") {
		t.Errorf("the Focus tab should show the routine:\n%s", v)
	}
}

func TestTheDollarKeyJumpsToRoutinesAndTheTabBarListsIt(t *testing.T) {
	r, _ := routinesRig(t)
	r.press("f1", "$")
	if r.m.Tab() != TabRoutines {
		t.Errorf("tab = %v", r.m.Tab())
	}
	if !strings.Contains(r.m.tabBar(), "$ Routines") {
		t.Errorf("tab bar = %q", r.m.tabBar())
	}
	if !strings.Contains(fmt.Sprint(helpLines(100)), "Routines tab") {
		t.Error("the help should explain the Routines tab")
	}
	_ = time.Second
}
