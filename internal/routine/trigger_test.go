package routine_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
)

func days(d ...time.Weekday) []time.Weekday { return d }

var weekdays = days(time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday)

func TestATriggerIsReadFromWords(t *testing.T) {
	for text, want := range map[string]routine.Trigger{
		"weekdays at 09:00":           {Days: weekdays, At: "09:00"},
		"at 9:30":                     {Days: days(1, 2, 3, 4, 5, 6, 0), At: "09:30"},
		"daily at 07:00 until 08:00":  {Days: days(1, 2, 3, 4, 5, 6, 0), At: "07:00", Until: "08:00"},
		"mon-thu except wed at 09:00": {Days: days(1, 2, 4), At: "09:00"},
		"weekdays except fri at 09:00 until 12:00 --reminder 5m":        {Days: days(1, 2, 3, 4), At: "09:00", Until: "12:00", Reminder: 5 * time.Minute},
		"fri at 10:00 --ask --when-busy skip":                           {Days: days(5), At: "10:00", Ask: true, WhenBusy: routine.BusySkip},
		"sat,sun at 10:00 --grace 30m":                                  {Days: days(6, 0), At: "10:00", Grace: 30 * time.Minute},
		"weekends at 10:00 --grace 0":                                   {Days: days(6, 0), At: "10:00", Grace: 0},
		"mon, wed ,fri at 10:00":                                        {Days: days(1, 3, 5), At: "10:00"},
		"Monday Wednesday at 10:00":                                     {Days: days(1, 3), At: "10:00"},
		"fri-mon at 10:00":                                              {Days: days(1, 5, 6, 0), At: "10:00"},
		"weekdays at 09:00 until 12:00 --until-alert banner --disabled": {Days: weekdays, At: "09:00", Until: "12:00", UntilAlert: routine.AlertBanner, Disabled: true},
		"weekdays at 09:00 --remind=10m":                                {Days: weekdays, At: "09:00", Reminder: 10 * time.Minute},
	} {
		got, err := routine.ParseTrigger(text)
		if err != nil {
			t.Errorf("%q: %v", text, err)
			continue
		}
		// fill in the defaults the want side leaves out
		if want.UntilAlert == "" {
			want.UntilAlert = routine.AlertNone
		}
		if want.WhenBusy == "" {
			want.WhenBusy = routine.BusyAsk
		}
		if want.Grace == 0 && !strings.Contains(text, "--grace") {
			want.Grace = routine.DefaultGrace
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q:\ngot  %+v\nwant %+v", text, got, want)
		}
	}
}

func TestBadTriggersSaySoPlainly(t *testing.T) {
	for text, want := range map[string]string{
		"":                                       "empty trigger",
		"weekdays":                               "needs a start time",
		"weekdays at":                            "needs a time of day",
		"weekdays at 9am":                        "needs a time of day",
		"funday at 09:00":                        "not a day",
		"mon-xyz at 09:00":                       "not a range of days",
		"weekdays at 09:00 until 08:00":          "not after the start",
		"weekdays at 09:00 until 09:00":          "not after the start",
		"weekdays at 09:00 until":                "needs a time of day",
		"weekdays except weekdays at 09:00":      "no days are left",
		"weekdays except at 09:00":               "needs the days to leave out",
		"weekdays at 09:00 --grace 7h":           "grace",
		"weekdays at 09:00 --grace soon":         "grace",
		"weekdays at 09:00 --when-busy now":      "not one of ask, skip, start",
		"weekdays at 09:00 --until-alert x":      "not one of none, sound, banner",
		"weekdays at 09:00 --until-alert banner": "needs an until time",
		"weekdays at 09:00 --reminder":           "needs a value",
		"weekdays at 09:00 --reminder xyz":       "reminder",
		"weekdays at 09:00 --colour red":         "not an option a trigger takes",
		"weekdays at 09:00 'oops":                "never closed",
	} {
		if _, err := routine.ParseTrigger(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want one containing %q", text, err, want)
		}
	}
}

func TestATriggerIsWrittenBackAsTheWordsThatReadBackTheSame(t *testing.T) {
	for _, text := range []string{
		"weekdays at 09:00", "daily at 07:00 until 08:00", "mon,wed,fri at 10:00", "mon-wed,fri at 10:00", "tue,thu,sun at 08:15", "weekends at 10:00 --grace 0",
		"weekdays at 09:00 until 12:00 --reminder 5m --ask --when-busy start --grace 30m --until-alert sound --disabled", "fri-mon at 10:00", "sat at 10:00 --reminder 1h30m",
	} {
		first, err := routine.ParseTrigger(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		again, err := routine.ParseTrigger(first.Command())
		if err != nil || !reflect.DeepEqual(again, first) {
			t.Errorf("%q was written as %q, which reads back as %+v, %v", text, first.Command(), again, err)
		}
	}
	for text, want := range map[string]string{
		"mon,tue,wed,thu,fri at 09:00": "weekdays at 09:00", "sat,sun at 10:00": "weekends at 10:00", "mon,tue,wed,thu,fri,sat,sun at 10:00": "daily at 10:00",
		"mon,tue,wed at 09:00": "mon-wed at 09:00", "mon,tue at 09:00": "mon,tue at 09:00", "fri-mon at 10:00": "mon,fri-sun at 10:00",
	} {
		got, err := routine.ParseTrigger(text)
		if err != nil || got.Command() != want {
			t.Errorf("%q is written %q, want %q (%v)", text, got.Command(), want, err)
		}
	}
}

func TestTriggersInTheFileAreStringsOrObjects(t *testing.T) {
	rs := parse(t, one(`{"steps": ["75% 25m work"], "triggers": [
		"weekdays except fri at 09:00 until 12:00",
		{"days": ["fri"], "at": "9:00", "until": "11:00", "until_alert": "banner", "reminder": "10m", "ask": true, "when_busy": "skip", "grace": "5m"},
		{"days": "weekends", "except": "sun", "at": "10:00", "enabled": false},
		{"at": "07:00"}
	]}`))
	got := rs[0].Triggers
	if len(got) != 4 {
		t.Fatalf("triggers = %+v", got)
	}
	if got[0].Command() != "mon-thu at 09:00 until 12:00" {
		t.Errorf("0: %q", got[0].Command())
	}
	if got[1].Command() != "fri at 09:00 until 11:00 --reminder 10m --ask --when-busy skip --grace 5m --until-alert banner" {
		t.Errorf("1: %q", got[1].Command())
	}
	if got[2].Command() != "sat at 10:00 --disabled" || !got[2].Disabled {
		t.Errorf("2: %q", got[2].Command())
	}
	if got[3].Command() != "daily at 07:00" {
		t.Errorf("a trigger with no days is daily: %q", got[3].Command())
	}
}

func TestATriggerProblemNamesTheKey(t *testing.T) {
	for text, want := range map[string]string{
		`{"steps": ["75% 25m x"], "triggers": ["weekdays"]}`:                            `routines.r.triggers[0]`,
		`{"steps": ["75% 25m x"], "triggers": ["weekdays at 09:00", {"days": "mon"}]}`:  `routines.r.triggers[1]`,
		`{"steps": ["75% 25m x"], "triggers": [{"days": "xday", "at": "09:00"}]}`:       "not a day",
		`{"steps": ["75% 25m x"], "triggers": [{"at": "09:00", "until": "08:00"}]}`:     "not after the start",
		`{"steps": ["75% 25m x"], "triggers": [{"at": "09:00", "when_busy": "never"}]}`: "when_busy",
		`{"steps": ["75% 25m x"], "triggers": [{"at": "9am"}]}`:                         "not a time of day",
		`{"steps": ["75% 25m x"], "triggers": [{"at": "09:00", "grace": "1d"}]}`:        "grace",
	} {
		if _, err := routine.Parse("routines.json", []byte(one(text))); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want one containing %q", text, err, want)
		}
	}
	_, err := routine.Parse("routines.json", []byte("{\n \"routines\": {\n \"r\": {\"steps\": [\"75% 25m x\"], \"triggers\": [{\"at\": \"09:00\", \"dayz\": \"mon\"}]}\n }\n}\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "routines.json:3") {
		t.Errorf("a misspelt trigger key should be caught at its line: %v", err)
	}
}

func TestTheOldScheduleKeyIsGone(t *testing.T) {
	_, err := routine.Parse("routines.json", []byte(one(`{"steps": ["75% 25m x"], "schedule": [{"days": ["mon"], "at": "09:00"}]}`)))
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("err = %v: triggers replaced it", err)
	}
}

func mustTrigger(t *testing.T, text string) routine.Trigger {
	t.Helper()
	tr, err := routine.ParseTrigger(text)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestNextStartIsTheFirstMatchingDayAfterNow(t *testing.T) {
	loc := time.UTC
	trig := mustTrigger(t, "mon,wed at 09:00 until 12:00")
	// 2026-11-09 is a Monday
	at := func(day, hh, mm int) time.Time { return time.Date(2026, 11, day, hh, mm, 0, 0, loc) }
	for _, tt := range []struct{ now, want time.Time }{
		{at(9, 8, 0), at(9, 9, 0)},    // earlier the same day
		{at(9, 9, 0), at(11, 9, 0)},   // exactly at the start: the next one (strictly after)
		{at(9, 9, 1), at(11, 9, 0)},   // just after
		{at(11, 13, 0), at(16, 9, 0)}, // wednesday afternoon: next monday
		{at(14, 0, 0), at(16, 9, 0)},  // the weekend
	} {
		got, ok := trig.NextStart(tt.now)
		if !ok || !got.Equal(tt.want) {
			t.Errorf("after %v: %v %v, want %v", tt.now, got, ok, tt.want)
		}
	}
	start := at(9, 9, 0)
	if end, ok := trig.EndFor(start); !ok || !end.Equal(at(9, 12, 0)) {
		t.Errorf("end = %v %v", end, ok)
	}
	if _, ok := mustTrigger(t, "mon at 09:00").EndFor(start); ok {
		t.Error("no until, no end")
	}
	rem := mustTrigger(t, "mon at 09:00 --reminder 10m")
	if r, ok := rem.ReminderFor(start); !ok || !r.Equal(at(9, 8, 50)) {
		t.Errorf("reminder = %v %v", r, ok)
	}
}

func TestStartsFollowTheWallClockAcrossDaylightSavingChanges(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data")
	}
	daily := mustTrigger(t, "daily at 09:00")
	// the clocks went forward on 2026-03-08 and back on 2026-11-01: 09:00 stays 09:00 on the wall clock
	for _, day := range []time.Time{time.Date(2026, 3, 7, 12, 0, 0, 0, ny), time.Date(2026, 10, 31, 12, 0, 0, 0, ny)} {
		first, _ := daily.NextStart(day)
		second, _ := daily.NextStart(first)
		if first.Hour() != 9 || second.Hour() != 9 || second.Sub(first) == 24*time.Hour && second.Day() == first.Day() {
			t.Errorf("%v then %v", first, second)
		}
		if second.Day() != first.Day()+1 && second.Day() != 1 {
			t.Errorf("the next day: %v then %v", first, second)
		}
	}
	gap := mustTrigger(t, "daily at 02:30") // does not exist on 2026-03-08
	got, _ := gap.NextStart(time.Date(2026, 3, 7, 12, 0, 0, 0, ny))
	if got.Day() != 8 || got.Hour() < 2 || got.Hour() > 3 {
		t.Errorf("a start that falls in the gap should still happen that day: %v", got)
	}
}

func TestARoutinesNextStartIsTheEarliestOfItsEnabledTriggers(t *testing.T) {
	rs := parse(t, one(`{"steps": ["75% 25m x"], "triggers": ["fri at 10:00", "mon at 09:00 --disabled", "weekdays at 09:00 until 12:00 --disabled", "wed at 08:00"]}`))
	now := time.Date(2026, 11, 9, 7, 0, 0, 0, time.UTC) // monday morning
	start, i, ok := rs[0].NextStart(now)
	if !ok || i != 3 || !start.Equal(time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("start %v trigger %d %v: the disabled ones should not count", start, i, ok)
	}
	none := parse(t, one(`{"steps": ["75% 25m x"], "triggers": ["mon at 09:00 --disabled"]}`))
	if _, _, ok := none[0].NextStart(now); ok {
		t.Error("only disabled triggers: nothing starts")
	}
	if _, _, ok := parse(t, one(`{"steps": ["75% 25m x"]}`))[0].NextStart(now); ok {
		t.Error("no triggers: nothing starts by itself")
	}
}

func TestTriggersSurviveSavingAndReading(t *testing.T) {
	p := tempFile(t, "")
	r := newRoutine(t, "morning", "75% 25m work")
	r.Triggers = []routine.Trigger{mustTrigger(t, "weekdays except fri at 09:00 until 12:00 --reminder 5m"), mustTrigger(t, "fri at 10:00 --ask --disabled")}
	if err := routine.Save(p, r, false); err != nil {
		t.Fatal(err)
	}
	rs, err := routine.Load(p)
	if err != nil || len(rs) != 1 || !reflect.DeepEqual(rs[0].Triggers, r.Triggers) {
		t.Fatalf("%+v %v", rs, err)
	}
	text := read(t, p)
	if !strings.Contains(text, `"mon-thu at 09:00 until 12:00 --reminder 5m"`) || !strings.Contains(text, `"fri at 10:00 --ask --disabled"`) {
		t.Errorf("file:\n%s", text)
	}
}

func TestStartWhenSaysTodayTomorrowTheWeekdayOrTheDate(t *testing.T) {
	now := time.Date(2026, 11, 9, 7, 0, 0, 0, time.UTC) // monday
	at := func(day, hh, mm int) time.Time { return time.Date(2026, 11, day, hh, mm, 0, 0, time.UTC) }
	for _, tt := range []struct {
		start time.Time
		want  string
	}{
		{at(9, 9, 0), "today 09:00"}, {at(10, 9, 5), "tomorrow 09:05"}, {at(11, 17, 30), "Wed 17:30"}, {at(15, 8, 0), "Sun 08:00"}, {at(16, 9, 0), "Mon 16 Nov 09:00"},
	} {
		if got := routine.StartWhen(tt.start, now); got != tt.want {
			t.Errorf("StartWhen(%v) = %q, want %q", tt.start, got, tt.want)
		}
	}
	late := time.Date(2026, 11, 9, 23, 30, 0, 0, time.UTC)
	if got := routine.StartWhen(time.Date(2026, 11, 10, 0, 30, 0, 0, time.UTC), late); got != "tomorrow 00:30" {
		t.Errorf("an hour later but the next calendar day is tomorrow: %q", got)
	}
}

func TestParseGraceTakesZeroOrALengthUpToSixHours(t *testing.T) {
	for text, want := range map[string]time.Duration{"0": 0, "10m": 10 * time.Minute, "6h": 6 * time.Hour, " 5m ": 5 * time.Minute} {
		if got, err := routine.ParseGrace(text); err != nil || got != want {
			t.Errorf("%q: %v %v", text, got, err)
		}
	}
	for _, text := range []string{"7h", "soon", ""} {
		if _, err := routine.ParseGrace(text); err == nil {
			t.Errorf("%q should be refused", text)
		}
	}
}
