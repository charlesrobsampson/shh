package routine_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
)

func TestACommandIsReadLikeShhFocusReadsIt(t *testing.T) {
	tests := []struct {
		text string
		want routine.Step
	}{
		{"75% 25m work", routine.Step{Kind: routine.StepSession, Name: "work", Note: "work", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"25m 75% work", routine.Step{Kind: routine.StepSession, Name: "work", Note: "work", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"off 5m break --alert banner", routine.Step{Kind: routine.StepSession, Name: "break", Note: "break", Level: 0, Length: 5 * time.Minute, Alert: routine.AlertBanner, Advance: routine.AdvanceAuto}},
		{"deep 1h30m deep work --ask", routine.Step{Kind: routine.StepSession, Name: "deep work", Note: "deep work", Level: 90, Length: 90 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAsk}},
		{"30m reading", routine.Step{Kind: routine.StepSession, Name: "reading", Note: "reading", Level: 65, Length: 30 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"light 10m", routine.Step{Kind: routine.StepSession, Name: "35% 10m", Level: 35, Length: 10 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"75% 25m work --alert=sound", routine.Step{Kind: routine.StepSession, Name: "work", Note: "work", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertSound, Advance: routine.AdvanceAuto}},
		{"75% 25m work -m 'write the report'", routine.Step{Kind: routine.StepSession, Name: "work", Note: "write the report", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{`75% 25m work --note "two words"`, routine.Step{Kind: routine.StepSession, Name: "work", Note: "two words", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"  75%   25m   spaced   out  ", routine.Step{Kind: routine.StepSession, Name: "spaced out", Note: "spaced out", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"shh 75% 25m work", routine.Step{Kind: routine.StepSession, Name: "work", Note: "work", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"shh focus 75% 25m work --alert banner", routine.Step{Kind: routine.StepSession, Name: "work", Note: "work", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertBanner, Advance: routine.AdvanceAuto}},
		{"shh f off 5m break", routine.Step{Kind: routine.StepSession, Name: "break", Note: "break", Level: 0, Length: 5 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"75% 25m work --alert none", routine.Step{Kind: routine.StepSession, Name: "work", Note: "work", Level: 75, Length: 25 * time.Minute, Alert: routine.AlertNone, Advance: routine.AdvanceAuto}},
		{"baseline 30%", routine.Step{Kind: routine.StepBaseline, Name: "baseline 30%", Level: 30}},
		{"baseline off", routine.Step{Kind: routine.StepBaseline, Name: "baseline off", Level: 0}},
		{"baseline deep", routine.Step{Kind: routine.StepBaseline, Name: "baseline 90%", Level: 90}},
		{"shh baseline 40%", routine.Step{Kind: routine.StepBaseline, Name: "baseline 40%", Level: 40}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got, err := routine.ParseCommand(tt.text)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestBadCommandsSayWhatIsWrong(t *testing.T) {
	tests := []struct{ text, want string }{
		{"", "an empty step"},
		{"   ", "an empty step"},
		{"shh", "an empty step"},
		{"75% work", "needs a length"},
		{"75%", "needs a length"},
		{"150% 25m work", "above 100"},
		{"75% 25m 30% work", "level is given twice"},
		{"75% 25m 10m work", "length is given twice"},
		{"75% 25m work --alert loud", "not one of none, sound, banner"},
		{"75% 25m work --alert", "needs a value"},
		{"75% 25m work -m", "needs a value"},
		{"75% 25m work --then off", "does not apply in a routine"},
		{"75% 25m work --end-routine", "does not apply in a routine"},
		{"75% 25m work --colour red", "not an option a routine step takes"},
		{"75% 25m work -x", "not an option"},
		{"75% 25m 'work", "never closed"},
		{`75% 25m "work`, "never closed"},
		{"baseline", "takes one level"},
		{"baseline 30% 40%", "takes one level"},
		{"baseline banana", "banana"},
		{"baseline 150%", "above 100"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			_, err := routine.ParseCommand(tt.text)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestACommandIsOnlyReadNeverRunThroughAShell(t *testing.T) {
	for _, text := range []string{"75% 25m work $(touch /tmp/pwned)", "75% 25m work; rm /", "75% 25m `whoami`", "75% 25m work && echo hi", "75% 25m work | cat > /tmp/x"} {
		s, err := routine.ParseCommand(text)
		if err != nil {
			t.Errorf("%q: %v", text, err)
			continue
		}
		// the odd characters are just part of the name
		if s.Length != 25*time.Minute || s.Level != 75 || s.Kind != routine.StepSession {
			t.Errorf("%q read as %+v", text, s)
		}
	}
}

func TestCommandWritesAStepBackAsTheCommandThatDoesIt(t *testing.T) {
	for _, text := range []string{
		"75% 25m work", "off 5m break --alert banner", "deep 90m deep work --alert banner --ask", "75% 25m work -m \"write the report\"",
		"baseline 30%", "baseline off", "75% 25m work --alert sound", "off 1h30m long rest", "35% 10m", "99% 90s sprint",
	} {
		first, err := routine.ParseCommand(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		written := first.Command()
		again, err := routine.ParseCommand(written)
		if err != nil {
			t.Errorf("%q was written as %q, which does not read back: %v", text, written, err)
			continue
		}
		if !reflect.DeepEqual(first, again) {
			t.Errorf("%q was written as %q, which reads back as %+v, not %+v", text, written, again, first)
		}
	}
}

func TestCommandShowsAnObjectStepAsItsCommand(t *testing.T) {
	rs := parse(t, one(`{"steps": [
		{"name": "block", "level": "deep", "length": "90m", "note": "writing", "alert": "banner", "advance": "ask"},
		{"name": "rest", "level": "off", "length": "20m"},
		{"baseline": "30%"}
	]}`))
	var got []string
	for _, s := range rs[0].Steps {
		got = append(got, s.Command())
	}
	want := []string{`90% 1h30m block -m writing --alert banner --ask`, "off 20m rest", "baseline 30%"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestAStepNamedLikeASettingIsWrittenSoItReadsBack(t *testing.T) {
	rs := parse(t, one(`{"steps": [{"name": "25m block", "level": "75%", "length": "25m"}]}`))
	cmd := rs[0].Steps[0].Command()
	again, err := routine.ParseCommand(cmd)
	if err != nil || again.Length != 25*time.Minute || again.Level != 75 {
		t.Errorf("%q reads back as %+v, %v: a name that starts like a length must not be taken for one", cmd, again, err)
	}
}

func TestLevelAndDurationWords(t *testing.T) {
	for level, want := range map[int]string{0: "off", 35: "35%", 99: "99%"} {
		if got := routine.LevelWord(level); got != want {
			t.Errorf("LevelWord(%d) = %q", level, got)
		}
	}
	for d, want := range map[time.Duration]string{25 * time.Minute: "25m", 90 * time.Minute: "1h30m", 90 * time.Second: "1m30s", 45 * time.Second: "45s", time.Hour: "1h"} {
		if got := routine.DurationWord(d); got != want {
			t.Errorf("DurationWord(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestStepsInAFileCanBeStringsObjectsOrBaselines(t *testing.T) {
	rs := parse(t, one(`{"repeat": 2, "steps": [
		"baseline 30%",
		"75% 25m work --alert banner",
		{"name": "break", "level": "off", "length": "5m"},
		{"baseline": "off"}
	], "finish": ["off 15m long break", {"baseline": "20%"}]}`))
	var kinds []string
	for _, p := range rs[0].Plan() {
		kinds = append(kinds, string(p.Kind)+":"+p.Name)
	}
	want := "baseline:baseline 30%,session:work,session:break,baseline:baseline off,baseline:baseline 30%,session:work,session:break,baseline:baseline off,session:long break,baseline:baseline 20%"
	if strings.Join(kinds, ",") != want {
		t.Errorf("plan = %v", kinds)
	}
	if got, want := rs[0].Total(), 2*30*time.Minute+15*time.Minute; got != want {
		t.Errorf("total = %v (baseline steps take no time), want %v", got, want)
	}
}

func TestAStringStepErrorNamesTheStepAndSaysWhat(t *testing.T) {
	_, err := routine.Parse("routines.json", []byte(one(`{"steps": ["75% 25m work", "75% work"]}`)))
	if err == nil || !strings.Contains(err.Error(), `"routines.r.steps[1]"`) || !strings.Contains(err.Error(), "needs a length") {
		t.Errorf("err = %v", err)
	}
	_, err = routine.Parse("routines.json", []byte(one(`{"steps": [""]}`)))
	if err == nil || !strings.Contains(err.Error(), `"routines.r.steps[0]"`) || !strings.Contains(err.Error(), "an empty step") {
		t.Errorf("an empty string step: %v", err)
	}
	_, err = routine.Parse("routines.json", []byte(one(`{"steps": [42]}`)))
	if err == nil {
		t.Error("a number is not a step")
	}
}

func TestABaselineObjectTakesOnlyTheBaseline(t *testing.T) {
	for _, extra := range []string{`"name": "x"`, `"level": "50%"`, `"length": "5m"`, `"note": "n"`, `"alert": "banner"`, `"advance": "ask"`} {
		_, err := routine.Parse("routines.json", []byte(one(`{"steps": [{"baseline": "30%", `+extra+`}]}`)))
		if err == nil || !strings.Contains(err.Error(), `"routines.r.steps[0].baseline"`) || !strings.Contains(err.Error(), "takes no other fields") {
			t.Errorf("with %s: err = %v", extra, err)
		}
	}
	if _, err := routine.Parse("routines.json", []byte(one(`{"steps": [{"baseline": "banana"}]}`))); err == nil || !strings.Contains(err.Error(), "banana") {
		t.Errorf("a bad baseline level: %v", err)
	}
}

func TestARoutineOfOnlyBaselineStepsIsAllowed(t *testing.T) {
	rs := parse(t, one(`{"steps": ["baseline 30%"]}`))
	if len(rs) != 1 || rs[0].Total() != 0 {
		t.Errorf("routines = %+v: setting the baseline is a fine routine on its own (a scheduled one, say)", rs)
	}
}

func TestMisspeltKeysInAnObjectStepAreStillCaught(t *testing.T) {
	_, err := routine.Parse("routines.json", []byte("{\n \"routines\": {\n \"r\": {\"steps\": [\"75% 25m work\", {\"name\": \"b\", \"lenght\": \"5m\"}]}\n }\n}\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "routines.json:3") {
		t.Errorf("err = %v, want an unknown key error at line 3 even though the step list mixes strings and objects", err)
	}
}
