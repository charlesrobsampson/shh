package tui

import (
	"errors"
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"os"
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

// ---- the editor ----

// editorRig is a routines rig whose save and remove act on a file text, as the real ones act on the file.
func editorRig(t *testing.T) (*rig, *string, *[]string) {
	t.Helper()
	r, _ := routinesRig(t)
	text := tuiRoutines
	var log []string
	r.m.cfg.Saved = func() ([]routine.Routine, error) { return routine.Parse("routines.json", []byte(text)) }
	dir := t.TempDir()
	path := dir + "/routines.json"
	_ = os.WriteFile(path, []byte(tuiRoutines), 0o600)
	r.m.cfg.Saved = func() ([]routine.Routine, error) { return routine.Load(path) }
	r.m.cfg.SaveRoutine = func(rt routine.Routine, replace bool) error {
		log = append(log, fmt.Sprintf("save %s replace=%v", rt.Name, replace))
		return routine.Save(path, rt, replace)
	}
	r.m.cfg.RemoveRoutine = func(name string) error {
		log = append(log, "remove "+name)
		return routine.Remove(path, name)
	}
	r.refresh()
	return r, &path, &log
}

func (r *rig) typeInto(text string) {
	r.t.Helper()
	for i := 0; i < 200 && r.m.prompt != nil && r.m.prompt.text != ""; i++ {
		r.press("backspace")
	}
	r.typeText(text)
	r.press("enter")
}

func TestEditingARoutineChangesAStepAndSavesInPlace(t *testing.T) {
	r, path, log := editorRig(t)
	r.press("e")
	if r.m.editor == nil || r.m.editor.original != "pomo" {
		t.Fatalf("e should open the editor on pomo: %+v", r.m.editor)
	}
	v := r.view()
	for _, want := range []string{"Editing pomo", "Name", "Description", "Repeat", "Waiting", "Summary", "75% 25m work --alert banner", "+", "add a step", "Finish", "off 15m long break"} {
		if !strings.Contains(v, want) {
			t.Errorf("the editor should show %q:\n%s", want, v)
		}
	}
	r.press("j", "j", "j", "j", "j", "enter") // the first step
	if r.m.prompt == nil || r.m.prompt.text != "75% 25m work --alert banner" {
		t.Fatalf("Enter on a step should open it for editing: %+v", r.m.prompt)
	}
	r.typeInto("90% 50m deep work --ask")
	if r.m.editor.draft.Steps[0].Length != 50*time.Minute || !r.m.editor.dirty {
		t.Errorf("draft = %+v", r.m.editor.draft.Steps[0])
	}
	r.press("s")
	if r.m.editor != nil {
		t.Fatalf("s should save and close: notice %q", r.m.notice)
	}
	if got := strings.Join(*log, ","); got != "save pomo replace=true" {
		t.Errorf("log = %s", got)
	}
	saved, _ := os.ReadFile(*path)
	if !strings.Contains(string(saved), "90% 50m deep work --ask") || !strings.Contains(string(saved), `"deep"`) {
		t.Errorf("file:\n%s", saved)
	}
	if !strings.Contains(r.m.messageLine(), "saved pomo") {
		t.Errorf("message = %q", r.m.messageLine())
	}
}

func TestANewRoutineIsBuiltFromRowsAndSaved(t *testing.T) {
	r, path, _ := editorRig(t)
	r.press("a")
	if r.m.editor == nil || r.m.editor.original != "" || !strings.Contains(r.view(), "New routine") {
		t.Fatalf("a should open a new routine:\n%s", r.view())
	}
	r.press("s")
	if r.m.editor == nil || !strings.Contains(r.m.notice, "name") {
		t.Errorf("saving with no name should say so: %q", r.m.notice)
	}
	r.press("enter") // Name (the cursor was moved there)
	r.typeInto("reading")
	r.press("j", "enter")
	r.typeInto("an evening")
	r.press("j", "enter")
	r.typeInto("3")
	r.press("j", "enter") // waiting cycles
	r.press("j", "enter") // summary cycles
	r.press("j", "a")     // add a step after the first
	r.typeInto("off 5m break --ask")
	r.press("s")
	if r.m.editor != nil {
		t.Fatalf("not saved: %q", r.m.notice)
	}
	rs, err := routine.Load(*path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := routine.Find(rs, "reading")
	if got.Description != "an evening" || got.Repeat != 3 || got.Waiting != routine.WaitNext || len(got.Steps) != 2 || got.Steps[1].Advance != routine.AdvanceAsk {
		t.Errorf("saved = %+v", got)
	}
	if got.Summary[0] != routine.WhereBanner || len(got.Summary) != 1 {
		t.Errorf("summary = %v", got.Summary)
	}
	if r.m.saved[r.m.routineCursor].Name != "reading" {
		t.Error("the new routine should be selected afterwards")
	}
}

func TestBadValuesStayInTheBoxWithTheReason(t *testing.T) {
	r, _, _ := editorRig(t)
	r.press("a", "enter") // name
	r.typeInto("-bad")
	if r.m.prompt == nil || r.m.prompt.err == "" {
		t.Fatal("a bad name should keep the box open")
	}
	r.press("esc")
	r.press("j", "j", "enter") // repeat
	r.typeInto("500")
	if r.m.prompt == nil || !strings.Contains(r.m.prompt.err, "1 to 99") {
		t.Errorf("err = %v", r.m.prompt)
	}
	r.press("esc")
	r.press("j", "j", "j", "enter") // the first step
	r.typeInto("75% work")
	if r.m.prompt == nil || !strings.Contains(r.m.prompt.err, "length") {
		t.Errorf("err = %v", r.m.prompt)
	}
	if r.m.editor.draft.Steps[0].Length != 25*time.Minute {
		t.Error("a refused value must not change the draft")
	}
}

func TestStepsCanBeMovedAndDeletedButNotTheLastOne(t *testing.T) {
	r, _, _ := editorRig(t)
	r.press("e", "j", "j", "j", "j", "j") // first step
	r.press("J")
	if r.m.editor.draft.Steps[1].Name != "work" || r.m.editor.draft.Steps[0].Name != "break" {
		t.Errorf("steps = %+v", r.m.editor.draft.Steps)
	}
	if row := r.m.editor.current(); row.kind != rowStep || row.index != 1 {
		t.Errorf("the cursor should go with the step: %+v", row)
	}
	r.press("K", "K")
	if r.m.editor.draft.Steps[0].Name != "work" {
		t.Errorf("K puts it back, and at the top does nothing: %+v", r.m.editor.draft.Steps)
	}
	r.press("d")
	if len(r.m.editor.draft.Steps) != 1 {
		t.Errorf("steps = %+v", r.m.editor.draft.Steps)
	}
	r.press("d")
	if len(r.m.editor.draft.Steps) != 1 || !strings.Contains(r.m.notice, "at least one step") {
		t.Errorf("the last step stays: %q", r.m.notice)
	}
}

func TestEscAsksTwiceWhenThereAreUnsavedChanges(t *testing.T) {
	r, path, log := editorRig(t)
	r.press("e")
	r.press("esc")
	if r.m.editor != nil {
		t.Error("with nothing changed, Esc closes at once")
	}
	r.press("e", "j", "j", "enter")
	r.typeInto("9")
	r.press("esc")
	if r.m.editor == nil || !strings.Contains(r.m.notice, "unsaved") {
		t.Errorf("the first Esc should warn: %q", r.m.notice)
	}
	r.press("esc")
	if r.m.editor != nil {
		t.Error("the second Esc discards")
	}
	saved, _ := os.ReadFile(*path)
	if string(saved) != tuiRoutines || len(*log) != 0 {
		t.Error("nothing should have been written")
	}
}

func TestRenamingSavesTheNewNameAndRemovesTheOld(t *testing.T) {
	r, path, log := editorRig(t)
	r.press("e", "enter")
	r.typeInto("pomodoro")
	r.press("s")
	if got := strings.Join(*log, ","); got != "save pomodoro replace=false,remove pomo" {
		t.Errorf("log = %s", got)
	}
	rs, _ := routine.Load(*path)
	if _, ok := routine.Find(rs, "pomo"); ok {
		t.Error("the old name should be gone")
	}
	if _, ok := routine.Find(rs, "pomodoro"); !ok {
		t.Error("the new name should be there")
	}
}

func TestChangingOnlyTheCaseOfANameReplacesInPlace(t *testing.T) {
	r, _, log := editorRig(t)
	r.press("e", "enter")
	r.typeInto("POMO")
	r.press("s")
	if got := strings.Join(*log, ","); got != "save POMO replace=true" {
		t.Errorf("log = %s", got)
	}
}

func TestACopyIsANewRoutineUnderAnotherName(t *testing.T) {
	r, path, _ := editorRig(t)
	r.press("c")
	if r.m.editor == nil || r.m.editor.original != "" || r.m.editor.draft.Name != "pomo copy" {
		t.Fatalf("editor = %+v", r.m.editor)
	}
	r.press("s")
	rs, _ := routine.Load(*path)
	if len(rs) != 3 {
		t.Errorf("rs = %d routines", len(rs))
	}
}

func TestSavingOverAnExistingNameIsRefused(t *testing.T) {
	r, _, _ := editorRig(t)
	r.press("a", "enter")
	r.typeInto("deep")
	r.press("s")
	if r.m.editor == nil || !strings.Contains(r.m.notice, "already exists") {
		t.Errorf("notice = %q", r.m.notice)
	}
}

func TestDeletingARoutineAsksFirst(t *testing.T) {
	r, path, log := editorRig(t)
	r.press("d")
	if r.m.prompt == nil || r.m.prompt.kind != promptConfirmDelete {
		t.Fatal("d should ask")
	}
	r.press("n")
	if len(*log) != 0 {
		t.Error("n keeps it")
	}
	r.press("d", "y")
	rs, _ := routine.Load(*path)
	if len(rs) != 1 || rs[0].Name != "deep" || !strings.Contains(r.m.messageLine(), "$ shh routine remove pomo") {
		t.Errorf("rs = %+v, message %q", rs, r.m.messageLine())
	}
}

func TestTheEditorFooterShowsTheCommandThatWouldSaveIt(t *testing.T) {
	r, _, _ := editorRig(t)
	r.press("e")
	line := r.m.messageLine()
	if !strings.Contains(line, "$ shh routine add pomo") {
		t.Errorf("message = %q", line)
	}
	if got := addCommand(r.m.editor.draft, true); !strings.HasSuffix(got, "--replace") || !strings.Contains(got, "-r 2") || !strings.Contains(got, "-f 'off 15m long break'") {
		t.Errorf("command = %s", got)
	}
}

func TestEditingKeysAreOffWithoutSaveSupport(t *testing.T) {
	r, _ := routinesRig(t)
	r.press("a")
	if r.m.editor != nil || !strings.Contains(r.m.notice, "not available") {
		t.Errorf("editor %v notice %q", r.m.editor, r.m.notice)
	}
}

// ---- editing text in a prompt ----

func openStepBox(t *testing.T) *rig {
	t.Helper()
	r, _, _ := editorRig(t)
	r.press("e", "j", "j", "j", "j", "j", "enter") // the first step of pomo: "75% 25m work --alert banner"
	if r.m.prompt == nil || r.m.prompt.text != "75% 25m work --alert banner" {
		t.Fatalf("prompt = %+v", r.m.prompt)
	}
	return r
}

func TestArrowKeysMoveTheCursorSoATypoCanBeFixedInPlace(t *testing.T) {
	r := openStepBox(t)
	r.press("home", "right", "right", "delete") // "75% ..." -> "75 ..."? no: after 2 rights the cursor is after "75"
	if got := r.m.prompt.text; got != "75 25m work --alert banner" {
		t.Fatalf("delete removed the wrong thing: %q", got)
	}
	r.typeText("%")
	if got := r.m.prompt.text; got != "75% 25m work --alert banner" {
		t.Errorf("typing goes in at the cursor: %q", got)
	}
	r.press("end")
	r.typeText(" --ask")
	if got := r.m.prompt.text; got != "75% 25m work --alert banner --ask" {
		t.Errorf("end goes to the end: %q", got)
	}
	r.press("left", "left", "left", "left", "backspace")
	if got := r.m.prompt.text; got != "75% 25m work --alert banner -ask" {
		// four lefts from the end sit after the first "-" of "--ask", and backspace removes it
		t.Errorf("backspace removes the character before the cursor: %q", got)
	}
}

func TestEditingInTheMiddleThenEnterSavesTheWholeText(t *testing.T) {
	r := openStepBox(t)
	r.press("home", "right", "right", "right", "right", "right", "right") // after "75% 25"
	r.press("backspace", "backspace")
	r.typeText("50")
	if got := r.m.prompt.text; got != "75% 50m work --alert banner" {
		t.Fatalf("text = %q", got)
	}
	r.press("enter")
	if r.m.editor.draft.Steps[0].Length != 50*time.Minute || r.m.editor.draft.Steps[0].Alert != routine.AlertBanner {
		t.Errorf("step = %+v", r.m.editor.draft.Steps[0])
	}
}

func TestCursorKeysStopAtTheEdgesAndNeverLoseText(t *testing.T) {
	r := openStepBox(t)
	want := r.m.prompt.text
	r.press("home", "left", "left", "backspace")
	if r.m.prompt.text != want {
		t.Errorf("left and backspace at the start change nothing: %q", r.m.prompt.text)
	}
	r.press("end", "right", "right", "delete")
	if r.m.prompt.text != want {
		t.Errorf("right and delete at the end change nothing: %q", r.m.prompt.text)
	}
}

func TestControlKeysDeleteWordsAndLines(t *testing.T) {
	r := openStepBox(t)
	r.press("ctrl+w")
	if got := r.m.prompt.text; got != "75% 25m work --alert " {
		t.Errorf("ctrl+w deletes the word before the cursor: %q", got)
	}
	r.press("ctrl+a", "ctrl+k")
	if r.m.prompt.text != "" {
		t.Errorf("ctrl+k deletes to the end: %q", r.m.prompt.text)
	}
	r.typeText("abc def")
	r.press("left", "left", "left", "ctrl+u")
	if got := r.m.prompt.text; got != "def" {
		t.Errorf("ctrl+u deletes to the start: %q", got)
	}
	r.press("end", "alt+left")
	r.typeText("X")
	if got := r.m.prompt.text; got != "Xdef" {
		t.Errorf("alt+left jumps a word back: %q", got)
	}
	r.press("alt+right")
	r.typeText("Y")
	if got := r.m.prompt.text; got != "XdefY" {
		t.Errorf("alt+right jumps a word forward: %q", got)
	}
}

func TestTheBoxShowsTheCursorInTheMiddleOfTheText(t *testing.T) {
	r := openStepBox(t)
	r.m.cursorOn = true
	r.press("home", "right", "right", "right")
	bar := r.m.promptBar()
	if !strings.Contains(bar, "75%") || !strings.Contains(bar, "25m work --alert banner") {
		t.Errorf("the whole text should still be there: %q", bar)
	}
	if strings.Contains(bar, "█") {
		t.Errorf("in the middle the cursor is the character under it, not an extra block: %q", bar)
	}
	r.press("end")
	r.m.cursorOn = true
	if !strings.Contains(r.m.promptBar(), "█") {
		t.Error("at the end the cursor is a block")
	}
}

func TestALongTextScrollsInsideTheBoxAndKeepsTheCursorInView(t *testing.T) {
	r := openStepBox(t)
	r.m.width = 50
	r.press("ctrl+u") // from the end, this clears the line
	r.typeText(strings.Repeat("abcdefghij ", 12) + "TAIL")
	bar := r.m.promptBar()
	if w := lipgloss.Width(bar); w > 50 {
		t.Errorf("the bar is %d wide on a 50 wide screen:\n%q", w, bar)
	}
	if !strings.Contains(bar, "TAIL") {
		t.Errorf("typing at the end should show the end: %q", bar)
	}
	r.press("home")
	bar = r.m.promptBar()
	if lipgloss.Width(bar) > 50 || !strings.Contains(bar, "abcdefghij") || strings.Contains(bar, "TAIL") {
		t.Errorf("at the start it should show the start: %q", bar)
	}
}

func TestArrowKeysAlsoWorkInTheOtherPrompts(t *testing.T) {
	r := newRig(t)
	r.press("f1", "s")
	r.typeText("75% 25m writng")
	r.press("left", "left")
	r.typeText("i")
	r.press("enter")
	if got := r.lastCall(); !strings.Contains(got, `note="writing"`) {
		t.Errorf("the start prompt should take the fixed text: %q", got)
	}
}
