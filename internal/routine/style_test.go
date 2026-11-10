package routine_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/routine"
)

func ip(n int) *int       { return &n }
func sp(s string) *string { return &s }

func TestAStepsBannerStyleIsWrittenAsFlagsAndReadBack(t *testing.T) {
	s, err := routine.ParseCommand("75% 25m work --alert banner --color 33 --position top-left --size large --sound Hero --banner-for 10s")
	if err != nil {
		t.Fatal(err)
	}
	want := config.StyleOverride{Color: ip(33), Position: sp("top-left"), Size: sp("large"), Sound: sp("Hero"), DurationSecs: ip(10)}
	if !reflect.DeepEqual(s.Style, want) {
		t.Errorf("style = %+v, want %+v", s.Style, want)
	}
	again, err := routine.ParseCommand(s.Command())
	if err != nil || !reflect.DeepEqual(again, s) {
		t.Errorf("%q reads back as %+v, %v", s.Command(), again, err)
	}
	if got := s.Command(); got != "75% 25m work --alert banner --color 33 --position top-left --size large --sound Hero --banner-for 10s" {
		t.Errorf("command = %q", got)
	}
	// until dismissed, and a sound name with a space
	s, err = routine.ParseCommand(`off 5m break --banner-for 0 --sound "Blow Hard"`)
	if err != nil || s.Style.DurationSecs == nil || *s.Style.DurationSecs != 0 || *s.Style.Sound != "Blow Hard" {
		t.Fatalf("%+v %v", s.Style, err)
	}
	if again, err := routine.ParseCommand(s.Command()); err != nil || !reflect.DeepEqual(again.Style, s.Style) {
		t.Errorf("%q reads back as %+v, %v", s.Command(), again.Style, err)
	}
}

func TestBadStyleFlagsSayWhatIsWrong(t *testing.T) {
	for text, want := range map[string]string{
		"75% 25m work --color red":       "not a number from 0 to 255",
		"75% 25m work --color 256":       "style.color",
		"75% 25m work --color":           "needs a value",
		"75% 25m work --position middle": "style.position",
		"75% 25m work --size huge":       "style.size",
		"75% 25m work --banner-for soon": "--banner-for",
		"75% 25m work --sound":           "needs a value",
	} {
		_, err := routine.ParseCommand(text)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want one containing %q", text, err, want)
		}
	}
	if _, err := routine.ParseCommand("75% 25m work --color 256"); err == nil || !strings.Contains(err.Error(), config.ColorChartURL) {
		t.Errorf("the error should point at the palette: %v", err)
	}
}

func TestStyleInTheFileAtTheRoutineAndStepLevel(t *testing.T) {
	rs := parse(t, `{"routines": {"r": {
	  "style": {"color": 33, "size": "large", "sound": "Hero"},
	  "steps": ["75% 25m work --color 196", {"name": "break", "level": "off", "length": "5m", "style": {"position": "bottom-left", "duration_secs": 8}}]}}}`)
	r := rs[0]
	if *r.Style.Color != 33 || *r.Style.Size != "large" || *r.Style.Sound != "Hero" || r.Style.Position != nil {
		t.Errorf("routine style = %+v", r.Style)
	}
	if *r.Steps[0].Style.Color != 196 || r.Steps[0].Style.Size != nil {
		t.Errorf("step 0 style = %+v", r.Steps[0].Style)
	}
	if *r.Steps[1].Style.Position != "bottom-left" || *r.Steps[1].Style.DurationSecs != 8 {
		t.Errorf("step 1 style = %+v", r.Steps[1].Style)
	}
}

func TestStyleProblemsInTheFileNameTheKey(t *testing.T) {
	for text, want := range map[string]string{
		`{"style": {"color": 300}, "steps": ["75% 25m x"]}`:                            `routines.r.style.color`,
		`{"style": {"color": "#ff0000"}, "steps": ["75% 25m x"]}`:                      `color`,
		`{"style": {"size": "huge"}, "steps": ["75% 25m x"]}`:                          `routines.r.style.size`,
		`{"style": {"buttons": [{"type": "dismiss"}]}, "steps": ["75% 25m x"]}`:        `routines.r.style.buttons`,
		`{"steps": [{"name": "x", "length": "5m", "style": {"position": "nowhere"}}]}`: `routines.r.steps[0].style.position`,
		`{"steps": [{"name": "x", "length": "5m", "style": {"buttons": []}}]}`:         `routines.r.steps[0].style.buttons`,
		`{"steps": ["75% 25m x --size huge"]}`:                                         `routines.r.steps[0]`,
		`{"style": {"colour": 3}, "steps": ["75% 25m x"]}`:                             `unknown key`,
	} {
		if _, err := routine.Parse("routines.json", []byte(one(text))); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want one containing %q", text, err, want)
		}
	}
}

func TestACalledRoutinesLookTravelsWithItsStepsUnderTheirOwn(t *testing.T) {
	rs := parse(t, `{"routines": {
	  "inner": {"style": {"color": 33, "size": "large"}, "steps": ["75% 5m a", "off 5m b --color 196"]},
	  "outer": {"style": {"color": 41}, "steps": ["run inner", "50% 5m c"]}}}`)
	outer, err := routine.Expand(rs, rs[1])
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := outer.Steps[0], outer.Steps[1], outer.Steps[2]
	if *a.Style.Color != 33 || *a.Style.Size != "large" {
		t.Errorf("a took the called routine's look: %+v", a.Style)
	}
	if *b.Style.Color != 196 || *b.Style.Size != "large" {
		t.Errorf("b keeps its own color over the called routine's: %+v", b.Style)
	}
	if !c.Style.IsZero() || *outer.Style.Color != 41 {
		t.Errorf("the outer routine's own steps carry only their own: %+v, routine %+v", c.Style, outer.Style)
	}
}

func TestStyleSurvivesSavingAndReading(t *testing.T) {
	p := tempFile(t, "")
	r := newRoutine(t, "styled", "75% 25m work --color 196 --banner-for 0", "off 5m break")
	r.Style = config.StyleOverride{Color: ip(33), Position: sp("bottom-center"), Sound: sp("Hero")}
	if err := routine.Save(p, r, false); err != nil {
		t.Fatal(err)
	}
	rs, err := routine.Load(p)
	if err != nil || len(rs) != 1 || !reflect.DeepEqual(rs[0].Style, r.Style) || !reflect.DeepEqual(rs[0].Steps[0].Style, r.Steps[0].Style) {
		t.Fatalf("%+v %v", rs, err)
	}
	text := read(t, p)
	if !strings.Contains(text, `"style": {`) || !strings.Contains(text, `"color": 33`) || !strings.Contains(text, "--color 196 --banner-for 0") {
		t.Errorf("file:\n%s", text)
	}
	plain := newRoutine(t, "plain", "75% 25m x")
	if err := routine.Save(p, plain, false); err != nil {
		t.Fatal(err)
	}
	if strings.Count(read(t, p), `"style"`) != 1 {
		t.Errorf("a routine with no style writes none:\n%s", read(t, p))
	}
}

func TestAHexColorInARoutineSaysWhatAColorIsNow(t *testing.T) {
	_, err := routine.Parse("routines.json", []byte(one(`{"style": {"color": "#ff0000"}, "steps": ["75% 25m x"]}`)))
	if err == nil || !strings.Contains(err.Error(), "not a #rrggbb code") {
		t.Errorf("err = %v", err)
	}
}
