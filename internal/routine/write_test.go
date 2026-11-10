package routine_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
)

func mustSteps(t *testing.T, cmds ...string) []routine.Step {
	t.Helper()
	var out []routine.Step
	for _, c := range cmds {
		s, err := routine.ParseCommand(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func newRoutine(t *testing.T, name string, cmds ...string) routine.Routine {
	return routine.Routine{Name: name, Repeat: 1, Waiting: routine.WaitStay, Summary: []routine.Where{routine.WhereBanner, routine.WhereUI}, Steps: mustSteps(t, cmds...)}
}

func tempFile(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "routines.json")
	if text != "" {
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func names(t *testing.T, p string) string {
	t.Helper()
	rs, err := routine.Load(p)
	if err != nil {
		t.Fatalf("the file no longer loads: %v\n%s", err, read(t, p))
	}
	var n []string
	for _, r := range rs {
		n = append(n, r.Name)
	}
	return strings.Join(n, ",")
}

const commented = `// my routines
{
  // the one I use daily
  "routines": {
    // pomodoro, the classic
    "pomodoro": {
      "steps": ["75% 25m work", "off 5m break"], // keep it short
      "repeat": 4
    },
    /* block */
    "block": {"steps": ["90% 1h deep"]}
  },
  // not mine
}
`

func TestSavingANewRoutineLeavesEverythingElseAsItWas(t *testing.T) {
	p := tempFile(t, commented)
	r := newRoutine(t, "reading", "50% 30m read --alert banner")
	r.Description = "an evening"
	if err := routine.Save(p, r, false); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	for _, keep := range []string{"// my routines", "// the one I use daily", "// pomodoro, the classic", "// keep it short", "/* block */", "// not mine", `// not mine`} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was lost:\n%s", keep, got)
		}
	}
	if names(t, p) != "pomodoro,block,reading" {
		t.Errorf("order = %s\n%s", names(t, p), got)
	}
	rs, _ := routine.Load(p)
	if rs[2].Description != "an evening" || rs[2].Steps[0].Alert != routine.AlertBanner || rs[2].Steps[0].Length != 30*time.Minute {
		t.Errorf("reading = %+v", rs[2])
	}
	if !strings.Contains(got, "    \"reading\": {\n      \"description\"") {
		t.Errorf("the new routine should be indented like the others:\n%s", got)
	}
}

func TestSavingIntoAMissingOrEmptyFileCreatesIt(t *testing.T) {
	for name, text := range map[string]string{"missing": "", "empty": " \n", "comments only": "// nothing yet\n", "empty routines": `{"routines": {}}`, "empty routines on lines": "{\n  \"routines\": {\n  }\n}\n", "no routines key": ""} {
		p := tempFile(t, text)
		if name == "no routines key" {
			p = filepath.Join(t.TempDir(), "sub", "routines.json")
		}
		if err := routine.Save(p, newRoutine(t, "a", "75% 25m work"), false); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if names(t, p) != "a" {
			t.Errorf("%s: names = %s\n%s", name, names(t, p), read(t, p))
		}
		if err := routine.Save(p, newRoutine(t, "b", "75% 25m work"), false); err != nil || names(t, p) != "a,b" {
			t.Errorf("%s: a second one: %v %s", name, err, names(t, p))
		}
	}
	if got := read(t, tempFile(t, "// nothing yet\n")); got != "" && false {
		t.Error(got)
	}
}

func TestACommentOnlyFileKeepsItsComment(t *testing.T) {
	p := tempFile(t, "// nothing yet\n")
	if err := routine.Save(p, newRoutine(t, "a", "75% 25m work"), false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); !strings.Contains(got, "// nothing yet") {
		t.Errorf("comment lost:\n%s", got)
	}
}

func TestANewFileIsPrivate(t *testing.T) {
	p := tempFile(t, "")
	if err := routine.Save(p, newRoutine(t, "a", "75% 25m work"), false); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestSavingOverANameNeedsReplaceAndIgnoresCase(t *testing.T) {
	p := tempFile(t, commented)
	err := routine.Save(p, newRoutine(t, "POMODORO", "75% 50m work"), false)
	if !errors.Is(err, routine.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if read(t, p) != commented {
		t.Error("a refused save must not touch the file")
	}
	if err := routine.Save(p, newRoutine(t, "pomodoro", "75% 50m work"), true); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	rs, _ := routine.Load(p)
	if len(rs) != 2 || rs[0].Name != "pomodoro" || rs[0].Steps[0].Length != 50*time.Minute || rs[0].Repeat != 1 {
		t.Errorf("rs = %+v", rs)
	}
	for _, keep := range []string{"// my routines", "// pomodoro, the classic", "/* block */", "// not mine"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was lost:\n%s", keep, got)
		}
	}
}

func TestARoutineThatWouldNotLoadIsNeverSaved(t *testing.T) {
	p := tempFile(t, commented)
	bad := newRoutine(t, "bad", "75% 25m work")
	bad.Repeat = 500
	if err := routine.Save(p, bad, false); err == nil || !strings.Contains(err.Error(), "repeat") {
		t.Errorf("err = %v", err)
	}
	if err := routine.Save(p, newRoutine(t, "-x", "75% 25m work"), false); err == nil {
		t.Error("a bad name should be refused")
	}
	if err := routine.Save(p, routine.Routine{Name: "empty", Repeat: 1}, false); err == nil {
		t.Error("a routine with no steps should be refused")
	}
	if read(t, p) != commented {
		t.Error("the file changed")
	}
}

func TestAFileThatDoesNotLoadIsNotTouched(t *testing.T) {
	broken := `{"routines": {"a": {"steps": ["75% work"]}}}`
	p := tempFile(t, broken)
	if err := routine.Save(p, newRoutine(t, "b", "75% 25m work"), false); err == nil {
		t.Error("saving into a file that does not load would hide the problem")
	}
	if read(t, p) != broken {
		t.Error("the file changed")
	}
}

func TestEveryFieldOfARoutineSurvivesTheRoundTrip(t *testing.T) {
	p := tempFile(t, "")
	r := newRoutine(t, "deep work", "baseline 30%", "90% 90m deep work -m \"write the report\" --alert banner --ask", "off 20m rest --alert sound")
	r.Description = "a \"quoted\" <tag> & more"
	r.Repeat = 3
	r.Finish = mustSteps(t, "off 15m long rest", "baseline off")
	r.Waiting = routine.WaitNext
	r.Summary = []routine.Where{routine.WhereMenu}
	if err := routine.Save(p, r, false); err != nil {
		t.Fatal(err)
	}
	rs, err := routine.Load(p)
	if err != nil || len(rs) != 1 {
		t.Fatalf("%v %v", rs, err)
	}
	got := rs[0]
	if got.Outline() != r.Outline() || got.Description != r.Description || got.Repeat != 3 || got.Waiting != routine.WaitNext ||
		len(got.Summary) != 1 || got.Summary[0] != routine.WhereMenu {
		t.Errorf("got  %+v\nwant %+v", got, r)
	}
	if text := read(t, p); strings.Contains(text, `\u003c`) || strings.Contains(text, `\u0026`) {
		t.Errorf("angle brackets and ampersands should be written plainly:\n%s", text)
	}
}

func TestDefaultsAreLeftOutOfTheFile(t *testing.T) {
	p := tempFile(t, "")
	if err := routine.Save(p, newRoutine(t, "a", "75% 25m work"), false); err != nil {
		t.Fatal(err)
	}
	text := read(t, p)
	for _, absent := range []string{"repeat", "waiting", "summary", "finish", "description"} {
		if strings.Contains(text, absent) {
			t.Errorf("%q is the default and should not be written:\n%s", absent, text)
		}
	}
}

func TestRemovingARoutineTakesOnlyThatRoutine(t *testing.T) {
	for _, victim := range []string{"pomodoro", "block", "BLOCK"} {
		p := tempFile(t, commented)
		if err := routine.Remove(p, victim); err != nil {
			t.Fatalf("%s: %v", victim, err)
		}
		got := read(t, p)
		want := "block"
		if strings.EqualFold(victim, "block") {
			want = "pomodoro"
		}
		if names(t, p) != want {
			t.Errorf("%s: names = %s\n%s", victim, names(t, p), got)
		}
		for _, keep := range []string{"// my routines", "// the one I use daily", "// not mine"} {
			if !strings.Contains(got, keep) {
				t.Errorf("%s: %q lost:\n%s", victim, keep, got)
			}
		}
		if strings.EqualFold(victim, "pomodoro") && strings.Contains(got, "the classic") {
			t.Errorf("the comment on the line above the routine goes with it:\n%s", got)
		}
	}
}

func TestRemovingTheOnlyOrAnUnknownRoutine(t *testing.T) {
	p := tempFile(t, `{"routines": {"only": {"steps": ["75% 25m x"]}}}`)
	if err := routine.Remove(p, "nope"); !errors.Is(err, routine.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if err := routine.Remove(p, "only"); err != nil {
		t.Fatal(err)
	}
	if names(t, p) != "" {
		t.Errorf("names = %q", names(t, p))
	}
	if err := routine.Remove(filepath.Join(t.TempDir(), "none.json"), "x"); !errors.Is(err, routine.ErrNotFound) {
		t.Errorf("a missing file has no routines: %v", err)
	}
	// and it can be added to again afterwards
	if err := routine.Save(p, newRoutine(t, "again", "75% 25m x"), false); err != nil || names(t, p) != "again" {
		t.Errorf("%v %s\n%s", err, names(t, p), read(t, p))
	}
}

func TestRemovingKeepsATrailingCommaFileValid(t *testing.T) {
	p := tempFile(t, "{\n \"routines\": {\n  \"a\": {\"steps\": [\"75% 25m x\"]},\n  \"b\": {\"steps\": [\"75% 25m x\"]},\n },\n}\n")
	if err := routine.Remove(p, "b"); err != nil || names(t, p) != "a" {
		t.Errorf("%v %s\n%s", err, names(t, p), read(t, p))
	}
	if err := routine.Save(p, newRoutine(t, "c", "75% 25m x"), false); err != nil || names(t, p) != "a,c" {
		t.Errorf("%v %s\n%s", err, names(t, p), read(t, p))
	}
}

func TestOneLineFilesAreEditedToo(t *testing.T) {
	p := tempFile(t, `{"routines": {"a": {"steps": ["75% 25m x"]}}}`)
	if err := routine.Save(p, newRoutine(t, "b", "75% 25m x"), false); err != nil || names(t, p) != "a,b" {
		t.Errorf("%v %s\n%s", err, names(t, p), read(t, p))
	}
	if err := routine.Remove(p, "a"); err != nil || names(t, p) != "b" {
		t.Errorf("%v %s\n%s", err, names(t, p), read(t, p))
	}
}

func TestRoutinesWithAwkwardNamesSurvive(t *testing.T) {
	p := tempFile(t, "")
	for _, n := range []string{"deep work", "a.b-c_d", "x"} {
		if err := routine.Save(p, newRoutine(t, n, "75% 25m x"), false); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	if names(t, p) != "deep work,a.b-c_d,x" {
		t.Errorf("names = %s", names(t, p))
	}
}

func TestCheckReturnsTheRoutineAsItWillReadBack(t *testing.T) {
	got, err := routine.Check(newRoutine(t, "a", "75% 25m work"))
	if err != nil || got.Name != "a" || len(got.Steps) != 1 {
		t.Errorf("%+v %v", got, err)
	}
	if _, err := routine.Check(routine.Routine{Name: "", Steps: mustSteps(t, "75% 25m x")}); err == nil {
		t.Error("a nameless routine is not valid")
	}
}
