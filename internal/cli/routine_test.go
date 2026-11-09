package cli_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

const lite = `{"routines": {
  "pomo": {"description": "short and sweet", "repeat": 2,
   "steps": [{"name": "work", "level": "75%", "length": "25m", "alert": "banner"}, {"name": "break", "level": "off", "length": "5m"}],
   "finish": [{"name": "long break", "level": "off", "length": "15m"}]},
  "ask": {"waiting": "stay", "steps": [{"name": "work", "level": "75%", "length": "25m", "advance": "ask"}, {"name": "break", "level": "30%", "length": "5m"}]}
}}`

// view is the status as a client sees it, which carries the routine.
func (r *rig) view() session.StatusView {
	r.t.Helper()
	v, err := session.NewClient(r.socket).Status()
	if err != nil {
		r.t.Fatal(err)
	}
	return v
}

func (r *rig) saveRoutines(text string) {
	r.t.Helper()
	if err := os.WriteFile(r.routines, []byte(text), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func TestRoutineListShowsEachRoutinesPlanOnOneLine(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines(lite)
	out := r.mustRun("routine", "list")
	for _, want := range []string{"pomo", "short and sweet", "75% 25m work --alert banner -> off 5m break, 2 times -> off 15m long break", "1h15m in all",
		"ask", "75% 25m work --ask -> 30% 5m break"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output is missing %q:\n%s", want, out)
		}
	}
}

func TestRoutineListWithNoFileSaysWhereToPutOneAndHowToStart(t *testing.T) {
	r := newRig(t, false)
	out := r.mustRun("routine", "list")
	if !strings.Contains(out, "No routines yet") || !strings.Contains(out, r.routines) || !strings.Contains(out, "shh routine example") {
		t.Errorf("output = %q", out)
	}
}

func TestRoutineListReportsAProblemFileByKey(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines(`{"routines": {"x": {"steps": [{"name": "a", "length": "5m", "level": "150%"}]}}}`)
	if code := r.run("routine", "list"); code != 1 || !strings.Contains(r.err.String(), "routines.x.steps[0].level") {
		t.Errorf("exit %d, stderr %q", code, r.err.String())
	}
}

func TestRoutineExamplePrintsAFileThatParses(t *testing.T) {
	r := newRig(t, false)
	out := r.mustRun("routine", "example")
	if out != routine.Example {
		t.Error("the example command should print exactly the example file")
	}
	if _, err := routine.Parse("example", []byte(out)); err != nil {
		t.Errorf("the example does not parse: %v", err)
	}
}

func TestRoutineRunStartsTheFirstStep(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	out := r.mustRun("routine", "run", "pomo")
	if strings.TrimSpace(out) != "Started pomo: work (1/2), step 1 of 5: 25m at 75%" {
		t.Errorf("output = %q, want the name once", out)
	}
	if st := r.view(); st.Level != 75 || st.Routine == nil || st.Routine.Name != "pomo" {
		t.Errorf("status = %+v", st)
	}
}

func TestRoutineRunStartsAWorkerWhenNoneIsRunning(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines(lite)
	r.env.Spawn = func() error { r.spawns++; r.serve(); return nil }
	r.mustRun("routine", "run", "ask")
	if r.spawns != 1 || r.view().Routine == nil {
		t.Errorf("spawns %d, routine %v", r.spawns, r.view().Routine)
	}
}

func TestRoutineRunWithAnUnknownNameListsTheSavedOnes(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	if code := r.run("routine", "run", "nope"); code != 1 || !strings.Contains(r.err.String(), `"nope"`) || !strings.Contains(r.err.String(), "ask, pomo") {
		t.Errorf("exit %d, stderr %q", code, r.err.String())
	}
	empty := newRig(t, true)
	if code := empty.run("routine", "run", "nope"); code != 1 || !strings.Contains(empty.err.String(), "no saved routines") {
		t.Errorf("with no file: exit %d, stderr %q", code, empty.err.String())
	}
}

func TestStartingASecondRoutineIsRefused(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	if code := r.run("routine", "run", "ask"); code != 1 || !strings.Contains(r.err.String(), "routine is running") {
		t.Errorf("exit %d, stderr %q", code, r.err.String())
	}
}

func TestRoutineStatusNextBackAndStopSayWhereTheRunIs(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "ask")
	if out := r.mustRun("routine", "status"); !strings.Contains(out, "ask: work, step 1 of 2: 25m at 75%") || !strings.Contains(out, "next: break") {
		t.Errorf("status = %q", out)
	}
	r.clock.Advance(25 * time.Minute)
	r.worker.Tick()
	out := r.mustRun("routine", "status")
	if !strings.Contains(out, "waiting to start break (5m at 30%)") || !strings.Contains(out, "shh routine next") {
		t.Errorf("waiting status = %q", out)
	}
	if out := r.mustRun("routine", "next"); !strings.Contains(out, "ask: break, step 2 of 2: 5m at 30%") {
		t.Errorf("next = %q", out)
	}
	if out := r.mustRun("routine", "back"); !strings.Contains(out, "ask: work, step 1 of 2: 25m at 75%") {
		t.Errorf("back = %q", out)
	}
	out = r.mustRun("routine", "stop")
	if !strings.Contains(out, "Routine ask stopped.") || !strings.Contains(out, "2 of 2 steps") {
		t.Errorf("stop = %q", out)
	}
	if out := r.mustRun("routine", "status"); !strings.Contains(out, "No routine is running.") || !strings.Contains(out, "Last run: ask stopped") {
		t.Errorf("status after = %q", out)
	}
}

func TestSkippingTheLastStepSaysTheRoutineFinished(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(`{"routines": {"one": {"steps": [{"name": "a", "level": "50%", "length": "30m"}]}}}`)
	r.mustRun("routine", "run", "one")
	if out := r.mustRun("routine", "next"); !strings.Contains(out, "Routine one finished.") || !strings.Contains(out, "1 of 1 steps") {
		t.Errorf("next on the last step = %q", out)
	}
}

func TestRoutineCommandsWithNothingRunningSayWhatTheyFound(t *testing.T) {
	r := newRig(t, true)
	for _, cmd := range []string{"next", "back", "stop"} {
		if out := r.mustRun("routine", cmd); !strings.Contains(out, "No routine is running.") {
			t.Errorf("%s = %q", cmd, out)
		}
	}
	gone := newRig(t, false)
	gone.env.Spawn = func() error { gone.spawns++; return nil }
	for _, cmd := range []string{"next", "back", "stop"} {
		if out := gone.mustRun("routine", cmd); !strings.Contains(out, "No routine is running.") {
			t.Errorf("%s with no worker = %q", cmd, out)
		}
	}
	if out := gone.mustRun("routine", "status"); !strings.Contains(out, "No routine is running (shh is not running).") {
		t.Errorf("status with no worker = %q", out)
	}
	if gone.spawns != 0 {
		t.Errorf("%d workers were started just to say there is no routine", gone.spawns)
	}
}

func TestTheFocusStatusShowsTheRoutine(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	if out := r.mustRun("status"); !strings.Contains(out, "Routine: pomo: work (1/2), step 1 of 5") {
		t.Errorf("status = %q", out)
	}
}

func TestAManualSessionDuringARoutineIsRefusedWithAWayForward(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	for _, args := range [][]string{{"focus", "90%"}, {"stop"}, {"stop", "--all"}} {
		r.err.Reset()
		if code := r.run(args...); code != 1 || !strings.Contains(r.err.String(), "routine is running") || !strings.Contains(r.err.String(), "--end-routine") {
			t.Errorf("%v: exit %d, stderr %q; want a refusal that names --end-routine", args, code, r.err.String())
		}
	}
	if st := r.view(); st.Level != 75 || st.Routine == nil {
		t.Errorf("the refusals changed something: %+v", st)
	}
}

func TestEndRoutineEndsItAndDoesWhatWasAsked(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	r.mustRun("focus", "90%", "--end-routine")
	if st := r.view(); st.Routine != nil || st.Level != 90 || len(st.Layers) != 1 {
		t.Errorf("status = %+v, want the routine ended and only the manual session", st)
	}
	r.mustRun("routine", "run", "pomo")
	r.mustRun("stop", "--end-routine")
	if st := r.view(); st.Routine != nil || st.Level == 75 {
		t.Errorf("stop --end-routine left %+v", st)
	}
}

func TestOnATerminalTheUserIsAskedBeforeARoutineIsEnded(t *testing.T) {
	for _, tt := range []struct {
		answer    string
		wantEnded bool
	}{{"y\n", true}, {"yes\n", true}, {"Y\n", true}, {"n\n", false}, {"\n", false}, {"", false}, {"maybe\n", false}} {
		r := newRig(t, true)
		r.saveRoutines(lite)
		r.mustRun("routine", "run", "pomo")
		r.env.IsTerminal = true
		r.env.Stdin = strings.NewReader(tt.answer)
		r.out.Reset()
		r.mustRun("focus", "90%")
		out := r.out.String()
		if !strings.Contains(out, "A routine is running. End it and start this session? [y/N]") {
			t.Errorf("%q: the question was not asked: %q", tt.answer, out)
		}
		st := r.view()
		if tt.wantEnded && (st.Routine != nil || st.Level != 90) {
			t.Errorf("%q: should have ended the routine and started the session: %+v", tt.answer, st)
		}
		if !tt.wantEnded && (st.Routine == nil || st.Level != 75 || !strings.Contains(out, "Left the routine running.")) {
			t.Errorf("%q: should have left it running: %+v / %q", tt.answer, st, out)
		}
	}
}

func TestStopAlsoAsksBeforeEndingARoutine(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	r.env.IsTerminal = true
	r.env.Stdin = strings.NewReader("y\n")
	r.mustRun("stop")
	if !strings.Contains(r.out.String(), "End it and stop? [y/N]") || r.view().Routine != nil {
		t.Errorf("out %q, routine %v", r.out.String(), r.view().Routine)
	}
}

func TestAnUnrelatedFailureIsNotMistakenForARoutine(t *testing.T) {
	r := newRig(t, true)
	if code := r.run("stop"); code != 1 {
		t.Errorf("stopping with nothing to stop: exit %d, stderr %q", code, r.err.String())
	}
	if strings.Contains(r.err.String(), "routine") {
		t.Errorf("the message mentions a routine that is not there: %q", r.err.String())
	}
}

func TestRoutineListKeepsTheOrderOfTheFile(t *testing.T) {
	r := newRig(t, false)
	step := `"steps": [{"name": "s", "length": "5m"}]`
	r.saveRoutines(`{"routines": {"zeta": {` + step + `}, "alpha": {` + step + `}, "mid": {` + step + `}}}`)
	out := r.mustRun("routine", "list")
	z, a, m := strings.Index(out, "zeta"), strings.Index(out, "alpha"), strings.Index(out, "mid")
	if !(z >= 0 && z < a && a < m) {
		t.Errorf("list should follow the file's order (zeta, alpha, mid):\n%s", out)
	}
}

func TestARoutineNameWrittenTwiceIsReportedByTheCommand(t *testing.T) {
	r := newRig(t, false)
	step := `"steps": [{"name": "s", "length": "5m"}]`
	r.saveRoutines("{\n\"routines\": {\n\"pomo\": {" + step + "},\n\"pomo\": {" + step + "}\n}\n}\n")
	if code := r.run("routine", "list"); code != 1 || !strings.Contains(r.err.String(), "duplicate key") || !strings.Contains(r.err.String(), "routines.json:4") {
		t.Errorf("exit %d, stderr %q", code, r.err.String())
	}
	if code := r.run("routine", "run", "pomo"); code == 0 {
		t.Error("a file with a duplicate name must not start anything")
	}
}

func TestRoutineNamesWithSpacesNeedNoQuotes(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(`{"routines": {"deep work": {"steps": [{"name": "block", "level": "90%", "length": "30m"}]}}}`)
	r.mustRun("routine", "run", "deep", "work")
	if v := r.view(); v.Routine == nil || v.Routine.Name != "deep work" {
		t.Errorf("view = %+v", v.Routine)
	}
}

func TestAWorkerThatDoesNotKnowARoutineThatIsInTheFileIsBlamedNotTheName(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines(lite)
	// A worker from before routines existed: it has no loader, so it knows no routine at all.
	old, err := session.NewWorker(session.Config{Store: r.store, Source: testkit.NewFakeSource(), Clock: r.clock, Rules: mustRules(t), Alerter: r.alerter, Suppressor: r.suppressor, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	r.worker = old
	r.serve()
	code := r.run("routine", "run", "pomo")
	msg := r.err.String()
	if code != 1 || !strings.Contains(msg, `"pomo" is in `) || !strings.Contains(msg, "does not know it") || !strings.Contains(msg, "older build") || strings.Contains(msg, "the saved ones are") {
		t.Errorf("exit %d, stderr %q: want it to say the worker is old, not list names as if the name were wrong", code, msg)
	}
	if code := r.run("routine", "run", "nope"); code != 1 || !strings.Contains(r.err.String(), "the saved ones are: ask, pomo") {
		t.Errorf("a name that is not in the file should still list the saved ones: %q", r.err.String())
	}
}

func mustRules(t *testing.T) rules.Rules {
	t.Helper()
	r, err := rules.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	return r
}
