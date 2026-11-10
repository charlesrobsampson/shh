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

// one wraps a single routine's body as a whole file, filed under the name r.
func one(body string) string { return `{"routines": {"r": ` + body + `}}` }

func TestTheExampleFileParsesAndHasWhatItClaims(t *testing.T) {
	rs := parse(t, routine.Example)
	if len(rs) != 4 {
		t.Fatalf("%d routines, want the pomodoro, deep work, afternoon and all day", len(rs))
	}
	if rs[0].Name != "pomodoro" || rs[1].Name != "deep work" || rs[2].Name != "afternoon" || rs[3].Name != "all day" {
		t.Errorf("names = %q, %q, %q, %q, want them in the order they are in the file", rs[0].Name, rs[1].Name, rs[2].Name, rs[3].Name)
	}
	if rs[3].Repeat != routine.Forever || rs[2].Steps[0].Kind != routine.StepCall || rs[2].Steps[1].Until != "17:00" {
		t.Errorf("the example should show forever, a call and an until step: %+v %+v", rs[3], rs[2])
	}
	p, ok := routine.Find(rs, "POMODORO")
	if !ok {
		t.Fatal("the pomodoro is missing (names are found ignoring case)")
	}
	if p.Repeat != 4 || len(p.Steps) != 2 || len(p.Finish) != 1 || p.Waiting != routine.WaitStay {
		t.Errorf("pomodoro = %+v", p)
	}
	if work := p.Steps[0]; work.Level != 75 || work.Length != 25*time.Minute || work.Alert != routine.AlertBanner || work.Name != "work" {
		t.Errorf("work = %+v", work)
	}
	deep := rs[1]
	if len(deep.Steps) != 4 || deep.Repeat != 1 {
		t.Fatalf("deep work = %+v", deep)
	}
	if b := deep.Steps[0]; b.Kind != routine.StepBaseline || b.Level != 30 {
		t.Errorf("the first deep work step should set the baseline to 30%%: %+v", b)
	}
	if block := deep.Steps[1]; block.Kind != routine.StepSession || block.Level != 90 || block.Length != 90*time.Minute || block.Advance != routine.AdvanceAsk || block.Name != "deep work" {
		t.Errorf("block = %+v", block)
	}
	if rest := deep.Steps[2]; rest.Name != "rest" || rest.Level != 0 || rest.Length != 20*time.Minute {
		t.Errorf("an object step in the same list should read the same way: %+v", rest)
	}
	if last := deep.Steps[3]; last.Kind != routine.StepBaseline || last.Level != 0 {
		t.Errorf("the last step should clear the baseline: %+v", last)
	}
}

func TestRoutinesComeBackInTheOrderTheyAreInTheFile(t *testing.T) {
	step := `"steps": [{"name": "s", "length": "5m"}]`
	rs := parse(t, `{"routines": {"zeta": {`+step+`}, "alpha": {`+step+`}, "mid": {`+step+`}}}`)
	var names []string
	for _, r := range rs {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "zeta,alpha,mid" {
		t.Errorf("names = %v, want zeta,alpha,mid (file order, not sorted: it is the order people wrote them and see them)", names)
	}
}

func TestARoutineNameCanOnlyBeThereOnce(t *testing.T) {
	step := `"steps": [{"name": "s", "length": "5m"}]`
	_, err := routine.Parse("routines.json", []byte("{\n \"routines\": {\n  \"pomo\": {"+step+"},\n  \"pomo\": {"+step+"}\n }\n}\n"))
	if err == nil || !strings.Contains(err.Error(), "duplicate key") || !strings.Contains(err.Error(), "routines.json:4") {
		t.Errorf("err = %v, want a duplicate key error at line 4: the second would silently replace the first", err)
	}
	_, err = routine.Parse("routines.json", []byte(`{"routines": {"Pomo": {`+step+`}, "pomo": {`+step+`}}}`))
	if err == nil || !strings.Contains(err.Error(), `"routines.pomo"`) || !strings.Contains(err.Error(), "apart from case") {
		t.Errorf("err = %v, want names that differ only in case to be refused (they are found ignoring case)", err)
	}
}

func TestDefaultsApplyWhenAFieldIsLeftOut(t *testing.T) {
	rs := parse(t, one(`{"steps": [{"name": "a", "length": "10m"}]}`))
	r := rs[0]
	if r.Name != "r" || r.Repeat != 1 || r.Waiting != routine.WaitStay || len(r.Finish) != 0 || r.Description != "" {
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
	rs := parse(t, one(`{"repeat": 3, "steps": [{"name": "work", "length": "25m"}, {"name": "break", "length": "5m"}],
		"finish": [{"name": "long break", "length": "15m"}]}`))
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
	rs := parse(t, one(`{"steps": [{"name": "block", "length": "1h"}]}`))
	if got := rs[0].Plan()[0].Label(); got != "block" {
		t.Errorf("label = %q", got)
	}
}

func TestCommentsAndTrailingCommasAreFine(t *testing.T) {
	rs := parse(t, "// my routines\n{\n  \"routines\": {\n    \"a\": {/* steps follow */ \"steps\": [{\"name\": \"s\", \"length\": \"5m\",},],},\n  },\n}\n")
	if len(rs) != 1 || rs[0].Steps[0].Length != 5*time.Minute {
		t.Errorf("routines = %+v", rs)
	}
}

func TestAnEmptyFileMeansNoRoutinesAndAMissingOneToo(t *testing.T) {
	if rs := parse(t, ""); len(rs) != 0 {
		t.Errorf("an empty file gave %v", rs)
	}
	if rs := parse(t, `{"routines": {}}`); len(rs) != 0 {
		t.Errorf("an empty object gave %v", rs)
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
	if err != nil || len(rs) != 4 {
		t.Fatalf("Load = %d routines, %v", len(rs), err)
	}
	must(t, os.WriteFile(path, []byte(one(`{"steps": []}`)), 0o600))
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
		{"a name that cannot be typed", `{"routines": {"-x": {"steps": [` + step + `]}}}`, "routines.-x"},
		{"a blank name", `{"routines": {" ": {"steps": [` + step + `]}}}`, `routines[' ']`},
		{"a name with a trailing space", `{"routines": {"a ": {"steps": [` + step + `]}}}`, `routines['a ']`},
		{"an empty name", `{"routines": {"": {"steps": [` + step + `]}}}`, `routines['']`},
		{"no steps", `{"routines": {"a": {}}}`, "routines.a.steps"},
		{"an empty steps list", `{"routines": {"a": {"steps": []}}}`, "routines.a.steps"},
		{"repeat of zero", `{"routines": {"a": {"repeat": 0, "steps": [` + step + `]}}}`, "routines.a.repeat"},
		{"too many repeats", `{"routines": {"a": {"repeat": 100, "steps": [` + step + `]}}}`, "routines.a.repeat"},
		{"a step with no name", `{"routines": {"a": {"steps": [{"length": "5m"}]}}}`, "routines.a.steps[0].name"},
		{"a step with no length", `{"routines": {"a": {"steps": [{"name": "s"}]}}}`, "routines.a.steps[0].length"},
		{"a zero length", `{"routines": {"a": {"steps": [{"name": "s", "length": "0"}]}}}`, "routines.a.steps[0].length"},
		{"a length with no unit", `{"routines": {"a": {"steps": [{"name": "s", "length": "25"}]}}}`, "routines.a.steps[0].length"},
		{"a bad level", `{"routines": {"a": {"steps": [{"name": "s", "length": "5m", "level": "150%"}]}}}`, "routines.a.steps[0].level"},
		{"a level that is a bare number", `{"routines": {"a": {"steps": [{"name": "s", "length": "5m", "level": "75"}]}}}`, "routines.a.steps[0].level"},
		{"a bad alert", `{"routines": {"a": {"steps": [{"name": "s", "length": "5m", "alert": "loud"}]}}}`, "routines.a.steps[0].alert"},
		{"a bad advance", `{"routines": {"a": {"steps": [{"name": "s", "length": "5m", "advance": "never"}]}}}`, "routines.a.steps[0].advance"},
		{"a bad finish step", `{"routines": {"a": {"steps": [` + step + `], "finish": [{"name": "f"}]}}}`, "routines.a.finish[0].length"},
		{"a bad waiting", `{"routines": {"a": {"waiting": "sleep", "steps": [` + step + `]}}}`, "routines.a.waiting"},
		{"a bad summary place", `{"routines": {"a": {"summary": ["ui", "email"], "steps": [` + step + `]}}}`, "routines.a.summary[1]"},
		{"a problem in a routine with a spaced name", `{"routines": {"deep work": {"steps": [{"name": "s"}]}}}`, `routines['deep work'].steps[0].length`},
		{"a second routine's problem", `{"routines": {"ok": {"steps": [` + step + `]}, "bad": {"repeat": 0, "steps": [` + step + `]}}}`, "routines.bad.repeat"},
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

func TestOneProblemRoutineMeansNoRoutinesAreReturned(t *testing.T) {
	good := `{"steps": [{"name": "a", "length": "5m"}]}`
	rs, err := routine.Parse("routines.json", []byte(`{"routines": {"good": `+good+`, "bad": {"steps": []}}}`))
	if err == nil || rs != nil {
		t.Errorf("got %v, %v: a half-read file would start the wrong thing, so there should be no routines and an error", rs, err)
	}
}

func TestUnknownKeysAndBrokenJSONAreErrorsWithTheirLine(t *testing.T) {
	_, err := routine.Parse("routines.json", []byte("{\n  \"routines\": {\n    \"a\": {\"colour\": \"red\", \"steps\": []}\n  }\n}\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "routines.json:3") {
		t.Errorf("err = %v, want an unknown key error pointing at line 3", err)
	}
	if _, err := routine.Parse("routines.json", []byte(`{"routines": {`)); err == nil {
		t.Error("broken JSON was accepted")
	}
	if _, err := routine.Parse("routines.json", []byte(one(`{"steps": [{"name": "s", "length": "5m", "lenght": "1m"}]}`))); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("a misspelt step key should be reported: %v", err)
	}
	if _, err := routine.Parse("routines.json", []byte(`{"routines": {"a": {"name": "a", "steps": [{"name": "s", "length": "5m"}]}}}`)); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("a name field is no longer part of a routine (the key is the name): %v", err)
	}
	if _, err := routine.Parse("routines.json", []byte(`{"routines": [{"name": "a"}]}`)); err == nil {
		t.Error("the old list form should be refused, not half understood")
	}
}

func TestWaitingAndSummaryOptionsAreRead(t *testing.T) {
	for _, w := range []routine.Waiting{routine.WaitStay, routine.WaitNext, routine.WaitBaseline} {
		rs := parse(t, one(`{"waiting": "`+string(w)+`", "summary": ["menu", "menu", "ui"], "steps": [{"name": "s", "length": "5m"}]}`))
		if rs[0].Waiting != w {
			t.Errorf("waiting = %q, want %q", rs[0].Waiting, w)
		}
		if got := rs[0].Summary; len(got) != 2 || got[0] != routine.WhereMenu || got[1] != routine.WhereUI {
			t.Errorf("summary = %v, want menu and ui (a repeat is dropped)", got)
		}
	}
	rs := parse(t, one(`{"summary": [], "steps": [{"name": "s", "length": "5m"}]}`))
	if len(rs[0].Summary) != 0 {
		t.Errorf("an empty summary list means no summary anywhere, got %v", rs[0].Summary)
	}
}

func TestTheSameStepNameTwiceInAListIsFine(t *testing.T) {
	rs := parse(t, `{"routines": {"a": {"steps": ["75% 25m work", "off 5m break", "75% 25m work", "off 5m break"]}}}`)
	if len(rs[0].Steps) != 4 {
		t.Errorf("steps = %+v: a routine may have two breaks of the same name (it is the routine's name that must be unique)", rs[0].Steps)
	}
}
