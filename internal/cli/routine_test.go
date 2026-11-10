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
	}{{"y\n", true}, {"yes\n", true}, {"Y\n", true}, {"e\n", true}, {"n\n", false}, {"\n", false}, {"", false}, {"maybe\n", false}} {
		r := newRig(t, true)
		r.saveRoutines(lite)
		r.mustRun("routine", "run", "pomo")
		r.env.IsTerminal = true
		r.env.Stdin = strings.NewReader(tt.answer)
		r.out.Reset()
		r.mustRun("focus", "90%")
		out := r.out.String()
		if !strings.Contains(out, "Choose [e/c/q/N]:") {
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

func TestRoutineAddSavesARoutineFromCommandsAndListShowsIt(t *testing.T) {
	r := newRig(t, false)
	out := r.mustRun("routine", "add", "pomo", "75% 25m work --alert banner", "off 5m break", "-r", "4", "-d", "the classic",
		"--finish", "off 15m long break", "--waiting", "next")
	for _, want := range []string{"Saved pomo", r.routines, "75% 25m work --alert banner -> off 5m break, 4 times -> off 15m long break", "shh routine run pomo"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	rs, err := routine.Load(r.routines)
	if err != nil || len(rs) != 1 {
		t.Fatalf("%v %v", rs, err)
	}
	if got := rs[0]; got.Description != "the classic" || got.Repeat != 4 || got.Waiting != routine.WaitNext || len(got.Finish) != 1 || len(got.Steps) != 2 {
		t.Errorf("saved = %+v", got)
	}
	if list := r.mustRun("routine", "list"); !strings.Contains(list, "pomo") {
		t.Errorf("list:\n%s", list)
	}
}

func TestRoutineAddKeepsTheRestOfTheFileAndRefusesAnExistingNameWithoutReplace(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines("// mine\n" + `{"routines": {"a": {"steps": ["75% 25m x"]}}}` + "\n")
	r.mustRun("routine", "add", "b", "baseline 30%", "50% 10m y --ask", "--summary", "menu,ui")
	text, _ := os.ReadFile(r.routines)
	if !strings.Contains(string(text), "// mine") {
		t.Errorf("a comment was lost:\n%s", text)
	}
	if code := r.run("routine", "add", "A", "75% 5m z"); code == 0 || !strings.Contains(r.err.String(), "--replace") {
		t.Errorf("code %d, stderr %q: an existing name needs --replace", code, r.err.String())
	}
	r.mustRun("routine", "add", "A", "75% 5m z", "--replace")
	rs, _ := routine.Load(r.routines)
	if len(rs) != 2 || rs[0].Steps[0].Length != 5*time.Minute {
		t.Errorf("rs = %+v", rs)
	}
	if rs[1].Summary[0] != routine.WhereMenu {
		t.Errorf("summary = %v", rs[1].Summary)
	}
}

func TestRoutineAddSaysWhichStepIsWrong(t *testing.T) {
	r := newRig(t, false)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"routine", "add", "x", "75% 25m ok", "75% oops"}, `step 2 ("75% oops")`},
		{[]string{"routine", "add", "x"}, "at least one step"},
		{[]string{"routine", "add", "x", "75% 25m a", "--finish", "off"}, "--finish step 1"},
		{[]string{"routine", "add", "x", "75% 25m a", "-r", "500"}, "repeat"},
		{[]string{"routine", "add", ".bad", "75% 25m a"}, "name"},
		{[]string{"routine", "add", "x", "75% 25m a", "--waiting", "sideways"}, "sideways"},
	} {
		if code := r.run(tt.args...); code == 0 || !strings.Contains(r.err.String(), tt.want) {
			t.Errorf("%v: code %d, stderr %q, want it to say %q", tt.args, code, r.err.String(), tt.want)
		}
	}
	if _, err := os.Stat(r.routines); err == nil {
		t.Error("nothing should have been written")
	}
}

func TestRoutineRemoveNeedsYesWhenItCannotAsk(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines(lite)
	if code := r.run("routine", "remove", "ask"); code == 0 || !strings.Contains(r.err.String(), "--yes") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	if out := r.mustRun("routine", "rm", "ASK", "--yes"); !strings.Contains(out, "Deleted ask") {
		t.Errorf("out = %q", out)
	}
	rs, _ := routine.Load(r.routines)
	if len(rs) != 1 || rs[0].Name != "pomo" {
		t.Errorf("rs = %+v", rs)
	}
	if code := r.run("routine", "remove", "nope", "-y"); code == 0 || !strings.Contains(r.err.String(), "no saved routine") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestAnswerCContinuesTheRoutineAfterTheNewSession(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	r.env.IsTerminal = true
	r.env.Stdin = strings.NewReader("c\n")
	r.mustRun("focus", "90%", "20m", "a call")
	st := r.view()
	if st.Routine == nil || !st.Routine.Interrupted || st.Level != 90 || len(st.Layers) != 2 {
		t.Fatalf("status = %+v, want the routine held with the session on top", st)
	}
	if out := r.mustRun("routine", "status"); !strings.Contains(out, "held while your own session runs") {
		t.Errorf("status:\n%s", out)
	}
}

func TestContinueRoutineFlagNeedsNoQuestionAndConflictsWithEndRoutine(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	r.mustRun("focus", "90%", "--continue-routine")
	if st := r.view(); st.Routine == nil || !st.Routine.Interrupted {
		t.Errorf("status = %+v", st)
	}
	if code := r.run("focus", "90%", "--end-routine", "--continue-routine"); code == 0 || !strings.Contains(r.err.String(), "three different answers") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	// not a terminal and no flag: the refusal names both ways out
	r2 := newRig(t, true)
	r2.saveRoutines(lite)
	r2.mustRun("routine", "run", "pomo")
	if code := r2.run("focus", "90%"); code == 0 || !strings.Contains(r2.err.String(), "--continue-routine") || !strings.Contains(r2.err.String(), "--end-routine") {
		t.Errorf("code %d stderr %q", code, r2.err.String())
	}
}

func TestTheQuestionSaysThatContinuingInterruptsTheRoutine(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	r.env.IsTerminal = true
	r.env.Stdin = strings.NewReader("\n")
	r.mustRun("focus", "90%")
	for _, want := range []string{"[e] end it, and start this session", "[c] interrupt it: start this session now; the routine waits and carries on when this session ends",
		"[q] queue this session: it starts when the routine ends", "[N] leave it as it is"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("the question should say %q:\n%s", want, r.out.String())
		}
	}
}

func TestAnswerQQueuesTheSessionForWhenTheRoutineEnds(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	r.env.IsTerminal = true
	r.env.Stdin = strings.NewReader("q\n")
	r.mustRun("focus", "90%", "30m", "deep block")
	if !strings.Contains(r.out.String(), "Queued: this session starts when the routine ends") {
		t.Errorf("out = %q", r.out.String())
	}
	st := r.view()
	if st.Routine == nil || st.Routine.Queued != "90% (deep) for 30m, \"deep block\"" || st.Level != 75 || len(st.Layers) != 1 {
		t.Fatalf("status = %+v: nothing should start now", st)
	}
	if out := r.mustRun("routine", "status"); !strings.Contains(out, "Queued for when it ends: 90% (deep) for 30m") {
		t.Errorf("status:\n%s", out)
	}
	r.mustRun("routine", "stop")
	st = r.view()
	if st.Routine != nil || len(st.Layers) != 1 || st.Level != 90 || st.Layers[0].Note != "deep block" {
		t.Errorf("status = %+v: the queued session should start when the routine ends", st)
	}
}

func TestAfterRoutineFlagQueuesAndUnqueueDropsIt(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	r.mustRun("focus", "90%", "30m", "--after-routine")
	r.mustRun("focus", "50%", "10m", "--after-routine") // replaces it
	if st := r.view(); st.Routine == nil || !strings.HasPrefix(st.Routine.Queued, "50% for 10m") {
		t.Errorf("status = %+v: a second queue replaces the first", st.Routine)
	}
	if out := r.mustRun("routine", "unqueue"); !strings.Contains(out, "Dropped the session that was queued") {
		t.Errorf("out = %q", out)
	}
	r.mustRun("routine", "stop")
	if st := r.view(); st.Level != 0 || len(st.Layers) != 0 {
		t.Errorf("status = %+v: nothing should start after an unqueue", st)
	}
	if out := r.mustRun("routine", "unqueue"); !strings.Contains(out, "No routine is running") {
		t.Errorf("out = %q", out)
	}
}

func TestNonTerminalRefusalNamesAllThreeFlags(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(lite)
	r.mustRun("routine", "run", "pomo")
	if code := r.run("focus", "90%"); code == 0 || !strings.Contains(r.err.String(), "--after-routine") || !strings.Contains(r.err.String(), "carries on when this session ends") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestRoutineAddTakesForeverAndCallsAndUntilSteps(t *testing.T) {
	r := newRig(t, false)
	r.mustRun("routine", "add", "base", "75% 25m work", "off 5m break")
	out := r.mustRun("routine", "add", "day", "run base --times 2", "50% until 17:00 email", "-r", "forever")
	if !strings.Contains(out, "run base --times 2 -> 50% until 17:00 email, forever") || !strings.Contains(out, "(until stopped)") {
		t.Errorf("out:\n%s", out)
	}
	rs, err := routine.Load(r.routines)
	if err != nil || len(rs) != 2 || rs[1].Repeat != routine.Forever {
		t.Fatalf("%v %v", rs, err)
	}
	if code := r.run("routine", "add", "x", "75% 25m a", "-r", "often"); code == 0 || !strings.Contains(r.err.String(), "not a number or forever") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	if code := r.run("routine", "add", "y", "run nothing"); code == 0 || !strings.Contains(r.err.String(), "no routine is called that") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	if code := r.run("routine", "add", "z", "75% 25m a", "--finish", "off 5m b", "-r", "forever"); code == 0 || !strings.Contains(r.err.String(), "finish steps") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestListSaysHowLongARoutineTakesIncludingCallsAndSetTimes(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines(`{"routines": {
	  "base": {"repeat": 2, "steps": ["75% 25m work", "off 5m break"]},
	  "twice": {"steps": ["run base --times 2"]},
	  "timed": {"steps": ["75% 25m work", "50% until 17:00 email"]},
	  "onlytimed": {"steps": ["50% until 17:00 email"]},
	  "loop": {"repeat": "forever", "steps": ["75% 25m work"]}}}`)
	out := r.mustRun("routine", "list")
	for _, want := range []string{"(1h in all)", "(2h in all)", "(at least 25m in all)", "(ends at set times)", "(until stopped)"} {
		if !strings.Contains(out, want) {
			t.Errorf("list is missing %q:\n%s", want, out)
		}
	}
}

func TestRoutineAddTakesTriggersAndListShowsWhenItStartsNext(t *testing.T) {
	r := newRig(t, false)
	r.env.Now = func() time.Time { return time.Date(2026, 11, 9, 7, 0, 0, 0, time.Local) } // a monday morning
	out := r.mustRun("routine", "add", "morning", "75% 25m work", "-t", "weekdays except fri at 09:00 until 12:00 --reminder 5m", "-t", "fri at 10:00 --ask")
	for _, want := range []string{"starts by itself:", "mon-thu at 09:00 until 12:00 --reminder 5m", "fri at 10:00 --ask", "next start: today 09:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("add output is missing %q:\n%s", want, out)
		}
	}
	rs, err := routine.Load(r.routines)
	if err != nil || len(rs) != 1 || len(rs[0].Triggers) != 2 {
		t.Fatalf("%v %v", rs, err)
	}
	r.env.Now = func() time.Time { return time.Date(2026, 11, 9, 12, 30, 0, 0, time.Local) }
	if list := r.mustRun("routine", "list"); !strings.Contains(list, "next start: tomorrow 09:00") {
		t.Errorf("list:\n%s", list)
	}
	r.env.Now = func() time.Time { return time.Date(2026, 11, 12, 12, 30, 0, 0, time.Local) } // thursday afternoon
	if list := r.mustRun("routine", "list"); !strings.Contains(list, "next start: tomorrow 10:00") {
		t.Errorf("list:\n%s", list)
	}
	r.env.Now = func() time.Time { return time.Date(2026, 11, 13, 12, 30, 0, 0, time.Local) } // friday afternoon
	if list := r.mustRun("routine", "list"); !strings.Contains(list, "next start: Mon 09:00") {
		t.Errorf("list:\n%s", list)
	}
	r.env.Now = func() time.Time { return time.Date(2026, 11, 1, 12, 30, 0, 0, time.Local) } // sunday: monday is tomorrow
	if list := r.mustRun("routine", "list"); !strings.Contains(list, "next start: tomorrow 09:00") {
		t.Errorf("list:\n%s", list)
	}
}

func TestListSaysWhenEveryTriggerIsDisabledAndStaysQuietForRoutinesWithNone(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines(`{"routines": {"off": {"steps": ["75% 25m x"], "triggers": ["mon at 09:00 --disabled"]}, "plain": {"steps": ["75% 25m x"]}}}`)
	list := r.mustRun("routine", "list")
	if !strings.Contains(list, "next start: none (every trigger is disabled)") || strings.Count(list, "next start") != 1 || strings.Count(list, "starts by itself") != 1 {
		t.Errorf("list:\n%s", list)
	}
}

func TestRoutineAddSaysWhichTriggerIsWrong(t *testing.T) {
	r := newRig(t, false)
	if code := r.run("routine", "add", "x", "75% 25m a", "-t", "weekdays at 09:00", "-t", "weekdays"); code == 0 || !strings.Contains(r.err.String(), `--trigger 2 ("weekdays")`) || !strings.Contains(r.err.String(), "needs a start time") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	if _, err := os.Stat(r.routines); err == nil {
		t.Error("nothing should be written")
	}
}

func TestGuidePrintsASectionOrSearchesItWhenNotOnATerminal(t *testing.T) {
	r := newRig(t, false)
	out := r.mustRun("guide", "steps")
	if !strings.Contains(out, "Writing steps") || !strings.Contains(out, "until 11:00") || strings.Contains(out, "Moving around") {
		t.Errorf("one section:\n%s", out)
	}
	out = r.mustRun("guide", "-s", "queue")
	if !strings.Contains(out, "unqueue") || strings.Contains(out, "Reading\n===") {
		t.Errorf("search:\n%s", out)
	}
	out = r.mustRun("guide", "routine-commands", "--search", "remove")
	if !strings.Contains(out, "shh routine remove") || strings.Contains(out, "shh routine example prints") {
		t.Errorf("a section and a search:\n%s", out)
	}
	if out = r.mustRun("guide", "--list"); !strings.Contains(out, "routine-files") || !strings.Contains(out, "during-a-run") {
		t.Errorf("list:\n%s", out)
	}
	if out = r.mustRun("guide"); !strings.Contains(out, "Moving around") || !strings.Contains(out, "Routine commands") {
		t.Errorf("everything:\n%s", out[:200])
	}
}

func TestGuideSaysWhenASectionIsUnknownOrAmbiguousOrASearchFindsNothing(t *testing.T) {
	r := newRig(t, false)
	if code := r.run("guide", "nonsense"); code != 2 || !strings.Contains(r.err.String(), "routine-files") {
		t.Errorf("code %d stderr %q: an unknown section lists the sections", code, r.err.String())
	}
	if code := r.run("guide", "routine"); code != 2 {
		t.Errorf("code %d: routine starts several sections", code)
	}
	if code := r.run("guide", "-s", "zzzzqqqq"); code != 1 || !strings.Contains(r.err.String(), "nothing in the guide matches") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestGuideOpensTheUIOnATerminalAndPrintsWithPrint(t *testing.T) {
	r := newRig(t, false)
	var gotSection, gotSearch string
	opened := 0
	r.env.IsTerminal = true
	r.env.Guide = func(section, search string) error { opened++; gotSection, gotSearch = section, search; return nil }
	r.mustRun("guide", "routine-f", "-s", "comments")
	if opened != 1 || gotSection != "routine-files" || gotSearch != "comments" {
		t.Errorf("opened %d at %q searching %q", opened, gotSection, gotSearch)
	}
	r.mustRun("guide")
	if opened != 2 || gotSection != "" || gotSearch != "" {
		t.Errorf("opened %d at %q searching %q", opened, gotSection, gotSearch)
	}
	if out := r.mustRun("guide", "steps", "--print"); opened != 2 || !strings.Contains(out, "Writing steps") {
		t.Errorf("--print should print: opened %d\n%s", opened, out)
	}
}

func TestSkipLeavesOutTheNextStartAndStatusShowsWhatIsUpcoming(t *testing.T) {
	r := newRig(t, true)
	r.env.Now = func() time.Time { return time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC) }
	r.saveRoutines(`{"routines": {"morning": {"steps": ["75% 25m work"], "triggers": ["daily at 09:30", "weekdays at 17:00"]}, "plain": {"steps": ["75% 5m x"]}}}`)
	out := r.mustRun("routine", "status")
	if !strings.Contains(out, "Starts by itself:") || !strings.Contains(out, "morning  today 09:30  (daily at 09:30)") || strings.Contains(out, "plain  ") {
		t.Errorf("status:\n%s", out)
	}
	out = r.mustRun("routine", "skip", "morning")
	if !strings.Contains(out, "Skipped the next start of morning (today 09:30)") || !strings.Contains(out, "The one after is today 17:00") || !strings.Contains(out, "shh routine skip --undo morning") {
		t.Errorf("skip:\n%s", out)
	}
	if out = r.mustRun("routine", "status"); !strings.Contains(out, "SKIPPED, then today 17:00") {
		t.Errorf("status:\n%s", out)
	}
	if out = r.mustRun("routine", "skip", "--undo", "morning"); !strings.Contains(out, "back on: today 09:30") {
		t.Errorf("undo:\n%s", out)
	}
	if code := r.run("routine", "skip", "--undo", "morning"); code == 0 || !strings.Contains(r.err.String(), "is not skipped") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestSkipSaysWhenARoutineHasNoTriggerOrDoesNotExist(t *testing.T) {
	r := newRig(t, true)
	r.saveRoutines(`{"routines": {"plain": {"steps": ["75% 5m x"]}}}`)
	if code := r.run("routine", "skip", "plain"); code == 0 || !strings.Contains(r.err.String(), "does not start by itself") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	if code := r.run("routine", "skip", "nothing"); code == 0 || !strings.Contains(r.err.String(), "no saved routine is called") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestListWarnsThatTriggersNeedAWorkerWhenThereIsNone(t *testing.T) {
	r := newRig(t, false)
	r.saveRoutines(`{"routines": {"morning": {"steps": ["75% 25m work"], "triggers": ["daily at 09:30"]}, "off": {"steps": ["75% 25m work"], "triggers": ["daily at 09:30 --disabled"]}}}`)
	if out := r.mustRun("routine", "list"); !strings.Contains(out, "the shh worker is not running, so triggers will not start anything yet") || !strings.Contains(out, "shh worker start") {
		t.Errorf("list:\n%s", out)
	}
	r.saveRoutines(`{"routines": {"off": {"steps": ["75% 25m work"], "triggers": ["daily at 09:30 --disabled"]}, "plain": {"steps": ["75% 25m work"]}}}`)
	if out := r.mustRun("routine", "list"); strings.Contains(out, "worker is not running") {
		t.Errorf("no enabled trigger, nothing to warn about:\n%s", out)
	}
}

func TestStatusSaysAnUntilCutOffInWords(t *testing.T) {
	if got := session.ReasonText("until"); got != "was cut off at its end time" {
		t.Errorf("ReasonText = %q", got)
	}
	if session.ReasonText("finished") != "finished" || !strings.Contains(session.ReasonText("missing"), "gone from the file") {
		t.Error("the other reasons")
	}
}

func TestRoutineAddTakesABannerStyleAndListShowsIt(t *testing.T) {
	r := newRig(t, false)
	out := r.mustRun("routine", "add", "styled", "75% 25m work --alert banner --color 196", "--style=--color 33 --size large --sound Hero")
	if !strings.Contains(out, "banners: --color 33 --size large --sound Hero") || !strings.Contains(out, "--color 196") {
		t.Errorf("add output:\n%s", out)
	}
	rs, err := routine.Load(r.routines)
	if err != nil || *rs[0].Style.Color != 33 || *rs[0].Style.Size != "large" || *rs[0].Steps[0].Style.Color != 196 {
		t.Fatalf("%+v %v", rs, err)
	}
	if code := r.run("routine", "add", "bad", "75% 25m x", "--style=--color purple"); code == 0 || !strings.Contains(r.err.String(), "--style:") || !strings.Contains(r.err.String(), "0 to 255") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	if code := r.run("routine", "add", "bad", "75% 25m x", "--style=--colour 3"); code == 0 || !strings.Contains(r.err.String(), "not a style flag") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	if code := r.run("routine", "add", "bad", "75% 25m x", "--style=--size huge"); code == 0 || !strings.Contains(r.err.String(), "style.size") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestAlertColorsShowsThePaletteOrOneColor(t *testing.T) {
	r := newRig(t, false)
	out := r.mustRun("alert", "colors")
	if !strings.Contains(out, "https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit") || !strings.Contains(out, "208  #ff8700") || strings.Count(out, "\n") < 250 {
		t.Errorf("plain list:\n%s", out[:300])
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("with no terminal there must be no escape sequences")
	}
	r.env.IsTerminal = true
	out = r.mustRun("alert", "colors")
	for _, want := range []string{"\x1b[48;5;208m", "\x1b[48;5;0m", "\x1b[48;5;255m", "Standard (0-7)", "Colors (16-231)", "Greys (232-255)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the palette is missing %q", strings.ReplaceAll(want, "\x1b", "ESC"))
		}
	}
	if out = r.mustRun("alert", "colors", "208"); !strings.Contains(out, "208 is #ff8700") || !strings.Contains(out, "\x1b[48;5;208m") {
		t.Errorf("one color:\n%s", out)
	}
	if code := r.run("alert", "colors", "300"); code != 2 || !strings.Contains(r.err.String(), "0 to 255") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}
