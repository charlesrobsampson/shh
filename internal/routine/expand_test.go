package routine_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
)

func TestAnUntilStepIsReadAndWrittenBack(t *testing.T) {
	s, err := routine.ParseCommand("90% until 11:00 deep work --alert banner")
	if err != nil {
		t.Fatal(err)
	}
	if s.Until != "11:00" || s.Length != 0 || s.Name != "deep work" || s.Level != 90 || s.Alert != routine.AlertBanner {
		t.Errorf("step = %+v", s)
	}
	if got := s.Command(); got != "90% until 11:00 deep work --alert banner" {
		t.Errorf("command = %q", got)
	}
	for text, want := range map[string]string{"50% until 9:05 email": "09:05", "off until 17:30": "17:30", "50% UNTIL 8:00 early": "08:00"} {
		got, err := routine.ParseCommand(text)
		if err != nil || got.Until != want {
			t.Errorf("%q: %+v %v, want until %s", text, got, err, want)
		}
	}
	if got, _ := routine.ParseCommand("off until 17:30"); got.Name != "off until 17:30" {
		t.Errorf("a step with no name is called by what it is: %q", got.Name)
	}
}

func TestUntilAndALengthTogetherOrABadTimeAreErrors(t *testing.T) {
	for text, want := range map[string]string{
		"75% 25m until 11:00 work": "not both",
		"75% until work":           "needs a length",
		"75% until 25:00 work":     "needs a length",
	} {
		if _, err := routine.ParseCommand(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want one containing %q", text, err, want)
		}
	}
}

func TestTheWordUntilInANameIsJustAWord(t *testing.T) {
	s, err := routine.ParseCommand("75% 25m wait until noon")
	if err != nil || s.Until != "" || s.Name != "wait until noon" {
		t.Errorf("%+v %v", s, err)
	}
	// a name that would read as an end time is written with -m, and reads back the same
	named := routine.Step{Kind: routine.StepSession, Name: "work until 11:00", Note: "work until 11:00", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}
	again, err := routine.ParseCommand(named.Command())
	if err != nil || again.Until != "" || again.Length != 25*time.Minute {
		t.Errorf("%q read back as %+v %v", named.Command(), again, err)
	}
}

func TestAnUntilStepInAnObjectOrAString(t *testing.T) {
	rs := parse(t, one(`{"steps": ["50% until 12:00 email", {"name": "wrap", "level": "30%", "until": "17:00"}]}`))
	if rs[0].Steps[0].Until != "12:00" || rs[0].Steps[1].Until != "17:00" || rs[0].Steps[1].Length != 0 {
		t.Errorf("steps = %+v", rs[0].Steps)
	}
	if rs[0].Exact() {
		t.Error("a routine with a step that ends at a time of day has no exact total")
	}
	for _, bad := range []string{`{"name": "x", "length": "5m", "until": "17:00"}`, `{"name": "x", "until": "5pm"}`} {
		if _, err := routine.Parse("f", []byte(one(`{"steps": [`+bad+`]}`))); err == nil || !strings.Contains(err.Error(), "routines.r.steps[0].until") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
}

func TestACallStepIsReadAndWrittenBack(t *testing.T) {
	for text, want := range map[string]routine.Step{
		"run pomodoro":                 {Kind: routine.StepCall, Name: "pomodoro", Call: "pomodoro", Times: 1, Alert: routine.AlertNone, Advance: routine.AdvanceAuto},
		"run pomodoro --times 2 --ask": {Kind: routine.StepCall, Name: "pomodoro", Call: "pomodoro", Times: 2, Alert: routine.AlertNone, Advance: routine.AdvanceAsk},
		"run deep work --alert banner": {Kind: routine.StepCall, Name: "deep work", Call: "deep work", Times: 1, Alert: routine.AlertBanner, Advance: routine.AdvanceAuto},
		`run "deep work" --times=3`:    {Kind: routine.StepCall, Name: "deep work", Call: "deep work", Times: 3, Alert: routine.AlertNone, Advance: routine.AdvanceAuto},
		"shh run pomodoro":             {Kind: routine.StepCall, Name: "pomodoro", Call: "pomodoro", Times: 1, Alert: routine.AlertNone, Advance: routine.AdvanceAuto},
	} {
		got, err := routine.ParseCommand(text)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %+v, %v\nwant %+v", text, got, err, want)
			continue
		}
		again, err := routine.ParseCommand(got.Command())
		if err != nil || !reflect.DeepEqual(again, got) {
			t.Errorf("%q was written as %q, which reads back as %+v %v", text, got.Command(), again, err)
		}
	}
	for text, want := range map[string]string{"run": "needs the name", "run x --times 0": "1 to 99", "run x --times": "needs a value", "run x -m hi": "not an option run takes"} {
		if _, err := routine.ParseCommand(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", text, err, want)
		}
	}
}

const calls = `{"routines": {
  "pomodoro": {"repeat": 2, "steps": ["75% 25m work --alert banner", "off 5m break"], "finish": ["off 15m long break"]},
  "day": {"steps": ["run pomodoro --ask", "50% until 17:00 email"]},
  "twice": {"steps": ["run pomodoro --times 2 --alert sound"]},
  "nest": {"steps": ["run day", "baseline 30%"]}
}}`

func TestACallIsReplacedByTheStepsItStandsFor(t *testing.T) {
	rs := parse(t, calls)
	day, err := routine.Expand(rs, rs[1])
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range day.Steps {
		names = append(names, s.Name)
	}
	want := []string{"pomodoro › work (1/2)", "pomodoro › break (1/2)", "pomodoro › work (2/2)", "pomodoro › break (2/2)", "pomodoro › long break", "email"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %q\nwant    %q", names, want)
	}
	if last := day.Steps[4]; last.Advance != routine.AdvanceAsk {
		t.Errorf("--ask on the call goes on the last session of what it stands for: %+v", last)
	}
	if day.Steps[0].Advance != routine.AdvanceAuto || day.Steps[0].Alert != routine.AlertBanner {
		t.Errorf("the called routine's own step settings are kept: %+v", day.Steps[0])
	}
	if len(day.Plan()) != 6 || day.Total() != 2*(25+5)*time.Minute+15*time.Minute {
		t.Errorf("plan %d, total %v", len(day.Plan()), day.Total())
	}
}

func TestTimesRepeatsTheCallAndAlertGoesOnItsLastStep(t *testing.T) {
	rs := parse(t, calls)
	twice, err := routine.Expand(rs, rs[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(twice.Steps) != 10 {
		t.Fatalf("steps = %d, want 5 twice", len(twice.Steps))
	}
	if twice.Steps[4].Alert != routine.AlertNone || twice.Steps[9].Alert != routine.AlertSound {
		t.Errorf("--alert applies when the whole call ends: %v then %v", twice.Steps[4].Alert, twice.Steps[9].Alert)
	}
}

func TestACallInsideACallExpandsAllTheWayDown(t *testing.T) {
	rs := parse(t, calls)
	nest, err := routine.Expand(rs, rs[3])
	if err != nil {
		t.Fatal(err)
	}
	if got := nest.Steps[0].Name; got != "day › pomodoro › work (1/2)" {
		t.Errorf("name = %q", got)
	}
	if len(nest.Steps) != 7 || nest.Steps[6].Kind != routine.StepBaseline {
		t.Errorf("steps = %d, last %+v", len(nest.Steps), nest.Steps[len(nest.Steps)-1])
	}
}

func TestAFileWithACallToNothingOrACycleIsRefusedWithThePath(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"missing":   {`{"routines": {"a": {"steps": ["run nothing"]}}}`, `routines.a.steps[0]": run "nothing": no routine is called that`},
		"itself":    {`{"routines": {"a": {"steps": ["75% 5m x", "run a"]}}}`, "forever: a → a"},
		"two":       {`{"routines": {"a": {"steps": ["run b"]}, "b": {"steps": ["run a"]}}}`, "a → b → a"},
		"three":     {`{"routines": {"a": {"steps": ["run b"]}, "b": {"steps": ["run c"]}, "c": {"steps": ["75% 5m x", "run A"]}}}`, "a → b → c → a"},
		"in finish": {`{"routines": {"a": {"steps": ["75% 5m x"], "finish": ["run a"]}}}`, "a → a"},
		"forever":   {`{"routines": {"a": {"steps": ["run b"]}, "b": {"repeat": "forever", "steps": ["75% 5m x"]}}}`, "repeats forever"},
	} {
		_, err := routine.Parse("routines.json", []byte(tc.text))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", name, err, tc.want)
		}
	}
}

func TestTooDeepANestingIsRefused(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"routines": {`)
	for i := 0; i < 8; i++ {
		next := `"75% 5m x"`
		if i < 7 {
			next = `"run r` + string(rune('1'+i)) + `"`
		}
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"r` + string(rune('0'+i)) + `": {"steps": [` + next + `]}`)
	}
	b.WriteString("}}")
	_, err := routine.Parse("routines.json", []byte(b.String()))
	if err == nil || !strings.Contains(err.Error(), "more than 5 deep") {
		t.Errorf("err = %v", err)
	}
}

func TestACallFindsRoutinesIgnoringCase(t *testing.T) {
	rs := parse(t, `{"routines": {"Pomo": {"steps": ["75% 5m x"]}, "b": {"steps": ["run POMO"]}}}`)
	if _, err := routine.Expand(rs, rs[1]); err != nil {
		t.Error(err)
	}
}

func TestRepeatForeverIsAWordInTheFileAndOnePassInThePlan(t *testing.T) {
	rs := parse(t, one(`{"repeat": "forever", "steps": ["75% 25m work", "off 5m break"]}`))
	if rs[0].Repeat != routine.Forever || rs[0].Exact() || len(rs[0].Plan()) != 2 {
		t.Errorf("routine = %+v, plan %d", rs[0], len(rs[0].Plan()))
	}
	if got := rs[0].Plan()[0].Label(); got != "work" {
		t.Errorf("label = %q: with no end to the rounds there is no (n/N)", got)
	}
	if got := rs[0].Outline(); got != "75% 25m work -> off 5m break, forever" {
		t.Errorf("outline = %q", got)
	}
	for text, want := range map[string]string{
		`{"repeat": "sometimes", "steps": ["75% 5m x"]}`:                       "forever",
		`{"repeat": "forever", "steps": ["75% 5m x"], "finish": ["off 5m y"]}`: "finish steps",
		`{"repeat": 0, "steps": ["75% 5m x"]}`:                                 "between 1 and 99 (or forever)",
	} {
		if _, err := routine.Parse("f", []byte(one(text))); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", text, err, want)
		}
	}
}

func TestForeverSurvivesSavingAndReading(t *testing.T) {
	p := tempFile(t, "")
	r := newRoutine(t, "loop", "75% 25m work", "run other --times 2", "50% until 17:00 email")
	r.Repeat = routine.Forever
	other := newRoutine(t, "other", "off 5m b")
	if err := routine.Save(p, other, false); err != nil {
		t.Fatal(err)
	}
	if err := routine.Save(p, r, false); err != nil {
		t.Fatal(err)
	}
	rs, err := routine.Load(p)
	if err != nil || len(rs) != 2 || rs[1].Repeat != routine.Forever || rs[1].Outline() != r.Outline() {
		t.Fatalf("%v %v", rs, err)
	}
	if !strings.Contains(read(t, p), `"repeat": "forever"`) {
		t.Errorf("file:\n%s", read(t, p))
	}
}

func TestSavingARoutineThatCallsAMissingOneOrMakesACycleIsRefused(t *testing.T) {
	p := tempFile(t, `{"routines": {"a": {"steps": ["75% 5m x"]}}}`)
	before := read(t, p)
	if err := routine.Save(p, newRoutine(t, "b", "run nothing"), false); err == nil || !strings.Contains(err.Error(), "no routine is called that") {
		t.Errorf("err = %v", err)
	}
	if err := routine.Save(p, newRoutine(t, "a", "75% 5m x", "run a"), true); err == nil || !strings.Contains(err.Error(), "a → a") {
		t.Errorf("err = %v", err)
	}
	if read(t, p) != before {
		t.Error("the file changed")
	}
}

func TestRemovingARoutineAnotherOneCallsIsRefused(t *testing.T) {
	p := tempFile(t, `{"routines": {"a": {"steps": ["75% 5m x"]}, "b": {"steps": ["run a"]}}}`)
	before := read(t, p)
	if err := routine.Remove(p, "a"); err == nil || !strings.Contains(err.Error(), "no routine is called that") {
		t.Errorf("err = %v: deleting a routine that is called would break the file", err)
	}
	if read(t, p) != before {
		t.Error("the file changed")
	}
}
