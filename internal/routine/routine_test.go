package routine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
)

func parse(t *testing.T, text string) []routine.Routine {
	t.Helper()
	rs, err := routine.Parse("routines.json", []byte(text))
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, text)
	}
	return rs
}

func TestTheExampleFileParsesAndHasWhatItClaims(t *testing.T) {
	rs := parse(t, routine.Example)
	if len(rs) != 2 {
		t.Fatalf("%d routines, want the pomodoro and the deep work block", len(rs))
	}
	p, ok := routine.Find(rs, "POMODORO")
	if !ok {
		t.Fatal("the pomodoro is missing (names are found ignoring case)")
	}
	if p.Repeat != 4 || len(p.Steps) != 2 || len(p.Finish) != 1 || p.Waiting != routine.WaitStay {
		t.Errorf("pomodoro = %+v", p)
	}
	if work := p.Steps[0]; work.Level != 75 || work.Length != 25*time.Minute || work.Alert != routine.AlertBanner || work.Note != "focus" {
		t.Errorf("work = %+v", work)
	}
	if deep := rs[1]; deep.Steps[0].Level != 90 || deep.Steps[0].Advance != routine.AdvanceAsk || deep.Repeat != 1 {
		t.Errorf("deep work = %+v", deep)
	}
}

func TestDefaultsApplyWhenAFieldIsLeftOut(t *testing.T) {
	rs := parse(t, `{"routines": [{"name": "r", "steps": [{"name": "a", "length": "10m"}]}]}`)
	r := rs[0]
	if r.Repeat != 1 || r.Waiting != routine.WaitStay || len(r.Finish) != 0 || r.Description != "" {
		t.Errorf("routine defaults = %+v", r)
	}
	if got := r.Summary; len(got) != 2 || got[0] != routine.WhereBanner || got[1] != routine.WhereUI {
		t.Errorf("summary default = %v, want banner and ui", got)
	}
	if s := r.Steps[0]; s.Level != 65 || s.Alert != routine.AlertNone || s.Advance != routine.AdvanceAuto || s.Note != "" {
		t.Errorf("step defaults = %+v, want medium, no alert, automatic hand-over", s)
	}
}

func TestPlanRepeatsTheStepsThenRunsFinishOnce(t *testing.T) {
	rs := parse(t, `{"routines": [{"name": "p", "repeat": 3, "steps": [{"name": "work", "length": "25m"}, {"name": "break", "length": "5m"}],
		"finish": [{"name": "long break", "length": "15m"}]}]}`)
	plan := rs[0].Plan()
	var names []string
	for _, p := range plan {
		names = append(names, p.Label())
	}
	want := "work (1/3),break (1/3),work (2/3),break (2/3),work (3/3),break (3/3),long break"
	if strings.Join(names, ",") != want {
		t.Errorf("plan = %v, want %s", names, want)
	}
	for i, p := range plan {
		if p.Index != i {
			t.Errorf("step %d has index %d", i, p.Index)
		}
	}
	if last := plan[len(plan)-1]; last.Round != 0 || last.Rounds != 3 {
		t.Errorf("the finish step has round %d of %d, want round 0 (not part of a round)", last.Round, last.Rounds)
	}
	if got, want := rs[0].Total(), 3*30*time.Minute+15*time.Minute; got != want {
		t.Errorf("total = %v, want %v", got, want)
	}
}

func TestARoutineThatDoesNotRepeatLabelsStepsByNameAlone(t *testing.T) {
	rs := parse(t, `{"routines": [{"name": "p", "steps": [{"name": "block", "length": "1h"}]}]}`)
	if got := rs[0].Plan()[0].Label(); got != "block" {
		t.Errorf("label = %q", got)
	}
}

func TestCommentsAndTrailingCommasAreFine(t *testing.T) {
	rs := parse(t, "// my routines\n{\n  \"routines\": [\n    {\"name\": \"a\", /* steps follow */ \"steps\": [{\"name\": \"s\", \"length\": \"5m\",},],},\n  ],\n}\n")
	if len(rs) != 1 || rs[0].Steps[0].Length != 5*time.Minute {
		t.Errorf("routines = %+v", rs)
	}
}

func TestAnEmptyFileMeansNoRoutinesAndAMissingOneToo(t *testing.T) {
	if rs := parse(t, ""); len(rs) != 0 {
		t.Errorf("an empty file gave %v", rs)
	}
	rs, err := routine.Load(filepath.Join(t.TempDir(), "routines.json"))
	if err != nil || rs != nil {
		t.Errorf("a missing file gave %v, %v; want no routines and no error", rs, err)
	}
}

func TestLoadReadsTheFileAndNamesItInErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routines.json")
	must(t, os.WriteFile(path, []byte(routine.Example), 0o600))
	rs, err := routine.Load(path)
	if err != nil || len(rs) != 2 {
		t.Fatalf("Load = %d routines, %v", len(rs), err)
	}
	must(t, os.WriteFile(path, []byte(`{"routines": [{"name": "x", "steps": []}]}`), 0o600))
	if _, err := routine.Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want one naming the file %s", err, path)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestProblemsNameTheKeyThatIsWrong(t *testing.T) {
	step := `{"name": "a", "length": "5m"}`
	tests := []struct {
		name    string
		content string
		wantKey string
	}{
		{"no name", `{"routines": [{"steps": [` + step + `]}]}`, "routines[0].name"},
		{"a blank name", `{"routines": [{"name": "  ", "steps": [` + step + `]}]}`, "routines[0].name"},
		{"a name that cannot be typed", `{"routines": [{"name": "-x", "steps": [` + step + `]}]}`, "routines[0].name"},
		{"the same name twice", `{"routines": [{"name": "a", "steps": [` + step + `]}, {"name": "A", "steps": [` + step + `]}]}`, "routines[1].name"},
		{"no steps", `{"routines": [{"name": "a"}]}`, "routines[0].steps"},
		{"repeat of zero", `{"routines": [{"name": "a", "repeat": 0, "steps": [` + step + `]}]}`, "routines[0].repeat"},
		{"too many repeats", `{"routines": [{"name": "a", "repeat": 100, "steps": [` + step + `]}]}`, "routines[0].repeat"},
		{"a step with no name", `{"routines": [{"name": "a", "steps": [{"length": "5m"}]}]}`, "routines[0].steps[0].name"},
		{"a step with no length", `{"routines": [{"name": "a", "steps": [{"name": "s"}]}]}`, "routines[0].steps[0].length"},
		{"a zero length", `{"routines": [{"name": "a", "steps": [{"name": "s", "length": "0"}]}]}`, "routines[0].steps[0].length"},
		{"a length with no unit", `{"routines": [{"name": "a", "steps": [{"name": "s", "length": "25"}]}]}`, "routines[0].steps[0].length"},
		{"a bad level", `{"routines": [{"name": "a", "steps": [{"name": "s", "length": "5m", "level": "150%"}]}]}`, "routines[0].steps[0].level"},
		{"a level that is a bare number", `{"routines": [{"name": "a", "steps": [{"name": "s", "length": "5m", "level": "75"}]}]}`, "routines[0].steps[0].level"},
		{"a bad alert", `{"routines": [{"name": "a", "steps": [{"name": "s", "length": "5m", "alert": "loud"}]}]}`, "routines[0].steps[0].alert"},
		{"a bad advance", `{"routines": [{"name": "a", "steps": [{"name": "s", "length": "5m", "advance": "never"}]}]}`, "routines[0].steps[0].advance"},
		{"the same step name twice", `{"routines": [{"name": "a", "steps": [` + step + `, ` + step + `]}]}`, "routines[0].steps[1].name"},
		{"a bad finish step", `{"routines": [{"name": "a", "steps": [` + step + `], "finish": [{"name": "f"}]}]}`, "routines[0].finish[0].length"},
		{"a bad waiting", `{"routines": [{"name": "a", "waiting": "sleep", "steps": [` + step + `]}]}`, "routines[0].waiting"},
		{"a bad summary place", `{"routines": [{"name": "a", "summary": ["ui", "email"], "steps": [` + step + `]}]}`, "routines[0].summary[1]"},
		{"a schedule with no time", `{"routines": [{"name": "a", "steps": [` + step + `], "schedule": [{"days": ["mon"]}]}]}`, "routines[0].schedule[0]"},
		{"a schedule with a bad time", `{"routines": [{"name": "a", "steps": [` + step + `], "schedule": [{"days": ["mon"], "at": "9am"}]}]}`, "routines[0].schedule[0]"},
		{"a schedule with a bad day", `{"routines": [{"name": "a", "steps": [` + step + `], "schedule": [{"days": ["funday"], "at": "09:00"}]}]}`, "routines[0].schedule[0]"},
		{"a schedule with no days", `{"routines": [{"name": "a", "steps": [` + step + `], "schedule": [{"at": "09:00"}]}]}`, "routines[0].schedule[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := routine.Parse("routines.json", []byte(tt.content))
			if err == nil {
				t.Fatal("Parse succeeded, want an error")
			}
			if !strings.Contains(err.Error(), `"`+tt.wantKey) {
				t.Errorf("error %q does not name the key %q", err, tt.wantKey)
			}
		})
	}
}

func TestUnknownKeysAndBrokenJSONAreErrorsWithTheirLine(t *testing.T) {
	_, err := routine.Parse("routines.json", []byte("{\n  \"routines\": [\n    {\"name\": \"a\", \"colour\": \"red\", \"steps\": []}\n  ]\n}\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "routines.json:3") {
		t.Errorf("err = %v, want an unknown key error pointing at line 3", err)
	}
	if _, err := routine.Parse("routines.json", []byte(`{"routines": [`)); err == nil {
		t.Error("broken JSON was accepted")
	}
	if _, err := routine.Parse("routines.json", []byte(`{"routines": [{"name": "a", "steps": [{"name": "s", "length": "5m", "lenght": "1m"}]}]}`)); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("a misspelt step key should be reported: %v", err)
	}
}

func TestAScheduleIsReadAndKept(t *testing.T) {
	rs := parse(t, `{"routines": [{"name": "a", "steps": [{"name": "s", "length": "5m"}],
		"schedule": [{"days": ["Mon", "wed", "mon"], "at": "09:30"}, {"days": ["sat"], "at": "20:00"}]}]}`)
	s := rs[0].Schedule
	if len(s) != 2 || s[0].At != "09:30" || len(s[0].Days) != 2 || s[0].Days[0] != time.Monday || s[0].Days[1] != time.Wednesday || s[1].Days[0] != time.Saturday {
		t.Errorf("schedule = %+v, want mon and wed (once each) at 09:30 and saturday at 20:00", s)
	}
}

func TestWaitingAndSummaryOptionsAreRead(t *testing.T) {
	for _, w := range []routine.Waiting{routine.WaitStay, routine.WaitNext, routine.WaitBaseline} {
		rs := parse(t, `{"routines": [{"name": "a", "waiting": "`+string(w)+`", "summary": ["menu", "menu", "ui"], "steps": [{"name": "s", "length": "5m"}]}]}`)
		if rs[0].Waiting != w {
			t.Errorf("waiting = %q, want %q", rs[0].Waiting, w)
		}
		if got := rs[0].Summary; len(got) != 2 || got[0] != routine.WhereMenu || got[1] != routine.WhereUI {
			t.Errorf("summary = %v, want menu and ui (a repeat is dropped)", got)
		}
	}
	rs := parse(t, `{"routines": [{"name": "a", "summary": [], "steps": [{"name": "s", "length": "5m"}]}]}`)
	if len(rs[0].Summary) != 0 {
		t.Errorf("an empty summary list means no summary anywhere, got %v", rs[0].Summary)
	}
}
