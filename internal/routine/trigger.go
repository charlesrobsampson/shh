package routine

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
)

// WhenBusy says what a trigger does when its time comes and something is already running.
type WhenBusy string

const (
	// BusyAsk (the default) shows a banner with Start now and Skip.
	BusyAsk WhenBusy = "ask"
	// BusySkip leaves what is running alone and does not start.
	BusySkip WhenBusy = "skip"
	// BusyStart starts anyway: a routine that is running is ended, and a session of the user's stays underneath.
	BusyStart WhenBusy = "start"
)

// DefaultGrace is how late a trigger may still start: a start missed by longer than this (the worker was down, the Mac asleep) is
// skipped, never caught up the next day.
const DefaultGrace = 15 * time.Minute

// maxGrace bounds grace: a "missed" start hours late is a different day's business.
const maxGrace = 6 * time.Hour

// Trigger is one way a routine starts by itself: on some days of the week, at a time of day. A routine can have any number, so the
// same steps can start Monday to Thursday at 9 and Friday at 10.
type Trigger struct {
	// Days are the days it fires on, Monday first, each once.
	Days []time.Weekday
	// At is the start, "HH:MM" in local time.
	At string
	// Until is "HH:MM", a hard stop on the same day: whatever is running then is cut off. Empty for none.
	Until string
	// UntilAlert says how the cut-off is announced: none (the default), sound or banner.
	UntilAlert Alert
	// Reminder is how long before the start a banner says it is coming; zero for none.
	Reminder time.Duration
	// Ask shows a "Start? [Start] [Skip]" banner instead of starting by itself.
	Ask      bool
	WhenBusy WhenBusy
	// Grace is how late a start can still be made.
	Grace time.Duration
	// Disabled leaves the trigger in the file without acting on it.
	Disabled bool
}

var weekOrder = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

var dayNames = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
	"monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
	"saturday": time.Saturday, "sunday": time.Sunday,
}

// parseDays reads day words: names (mon, tuesday), ranges (mon-fri, which may wrap: fri-mon), the shorthands weekdays, weekends and
// daily, and commas between them. The result is Monday first, each day once.
func parseDays(words []string) ([]time.Weekday, error) {
	set := map[time.Weekday]bool{}
	for _, w := range words {
		for _, part := range strings.Split(w, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			switch {
			case part == "":
			case part == "daily" || part == "everyday":
				for _, d := range weekOrder {
					set[d] = true
				}
			case part == "weekdays":
				for _, d := range weekOrder[:5] {
					set[d] = true
				}
			case part == "weekends":
				set[time.Saturday], set[time.Sunday] = true, true
			case strings.Contains(part, "-"):
				from, to, _ := strings.Cut(part, "-")
				a, okA := dayNames[from]
				b, okB := dayNames[to]
				if !okA || !okB {
					return nil, fmt.Errorf("%q is not a range of days: use names such as mon-fri", part)
				}
				i := slices.Index(weekOrder, a)
				for {
					set[weekOrder[i]] = true
					if weekOrder[i] == b {
						break
					}
					i = (i + 1) % 7
				}
			default:
				d, ok := dayNames[part]
				if !ok {
					return nil, fmt.Errorf("%q is not a day: use mon, tue, wed, thu, fri, sat or sun, a range like mon-fri, or weekdays, weekends or daily", part)
				}
				set[d] = true
			}
		}
	}
	var out []time.Weekday
	for _, d := range weekOrder {
		if set[d] {
			out = append(out, d)
		}
	}
	return out, nil
}

// daysWord writes days the shortest way: daily, weekdays, weekends, or names with ranges for runs of three or more (mon-wed,fri).
func daysWord(days []time.Weekday) string {
	switch {
	case len(days) == 7:
		return "daily"
	case slices.Equal(days, weekOrder[:5]):
		return "weekdays"
	case slices.Equal(days, weekOrder[5:]):
		return "weekends"
	}
	var parts []string
	for i := 0; i < len(weekOrder); {
		if !slices.Contains(days, weekOrder[i]) {
			i++
			continue
		}
		j := i
		for j+1 < len(weekOrder) && slices.Contains(days, weekOrder[j+1]) {
			j++
		}
		name := func(d time.Weekday) string { return strings.ToLower(d.String()[:3]) }
		if j-i >= 2 {
			parts = append(parts, name(weekOrder[i])+"-"+name(weekOrder[j]))
		} else {
			for k := i; k <= j; k++ {
				parts = append(parts, name(weekOrder[k]))
			}
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// build checks the trigger as a whole and fills in what was left out.
func (t Trigger) check() error {
	if len(t.Days) == 0 {
		return errors.New("no days are left for it to start on")
	}
	if _, ok := NormalClock(t.At); !ok {
		return errors.New(`a trigger needs a start time such as "at 09:00"`)
	}
	if t.Until != "" {
		if _, ok := NormalClock(t.Until); !ok {
			return fmt.Errorf("until %q is not a time of day such as 12:00", t.Until)
		}
		if t.Until <= t.At {
			return fmt.Errorf("until %s is not after the start (%s): a trigger's stop time is on the same day", t.Until, t.At)
		}
	}
	if t.UntilAlert != "" && t.UntilAlert != AlertNone && t.Until == "" {
		return errors.New("an until alert needs an until time")
	}
	if t.Reminder < 0 || t.Reminder > 24*time.Hour {
		return fmt.Errorf("reminder %v is not between 0 and 24 hours", t.Reminder)
	}
	if t.Grace < 0 || t.Grace > maxGrace {
		return fmt.Errorf("grace %v is not between 0 and %v", t.Grace, maxGrace)
	}
	return nil
}

// ParseTrigger reads a trigger written as words, like a step:
//
//	weekdays at 09:00 until 12:00 --reminder 5m
//	mon-thu except wed at 09:00
//	fri at 10:00 --ask --when-busy skip
//
// The days come first (daily when left out), then optionally except and the days to leave out, then at and the start time, then
// optionally until and the stop time. Flags: --reminder 5m (a banner before the start), --ask (a Start/Skip banner instead of starting),
// --when-busy ask|skip|start, --grace 10m (how late a start can be made), --until-alert none|sound|banner, --disabled.
func ParseTrigger(text string) (Trigger, error) {
	words, err := splitCommand(text)
	if err != nil {
		return Trigger{}, err
	}
	if len(words) == 0 {
		return Trigger{}, errors.New(`an empty trigger: write something like "weekdays at 09:00"`)
	}
	t := Trigger{UntilAlert: AlertNone, WhenBusy: BusyAsk, Grace: DefaultGrace}
	var days, except []string
	excepting := false
	section := &days
	for i := 0; i < len(words); i++ {
		w := words[i]
		flag, value, hasValue := strings.Cut(w, "=")
		take := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(words) {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			i++
			return words[i], nil
		}
		switch strings.ToLower(w) {
		case "except":
			section, excepting = &except, true
			continue
		case "at", "until":
			if i+1 >= len(words) {
				return Trigger{}, fmt.Errorf("%s needs a time of day such as 09:00", w)
			}
			i++
			at, ok := NormalClock(words[i])
			if !ok {
				return Trigger{}, fmt.Errorf("%s needs a time of day such as 09:00, not %q", w, words[i])
			}
			if strings.EqualFold(w, "at") {
				t.At = at
			} else {
				t.Until = at
			}
			continue
		}
		switch {
		case flag == "--reminder" || flag == "--remind":
			v, err := take()
			if err != nil {
				return Trigger{}, err
			}
			if t.Reminder, err = decide.ParseDuration(v); err != nil {
				return Trigger{}, fmt.Errorf("--reminder: %w", err)
			}
		case flag == "--grace":
			v, err := take()
			if err != nil {
				return Trigger{}, err
			}
			if t.Grace, err = parseGrace(v); err != nil {
				return Trigger{}, fmt.Errorf("--grace: %w", err)
			}
		case flag == "--when-busy":
			v, err := take()
			if err != nil {
				return Trigger{}, err
			}
			if !slices.Contains([]WhenBusy{BusyAsk, BusySkip, BusyStart}, WhenBusy(v)) {
				return Trigger{}, fmt.Errorf("--when-busy %q is not one of ask, skip, start", v)
			}
			t.WhenBusy = WhenBusy(v)
		case flag == "--until-alert":
			v, err := take()
			if err != nil {
				return Trigger{}, err
			}
			if !slices.Contains([]Alert{AlertNone, AlertSound, AlertBanner}, Alert(v)) {
				return Trigger{}, fmt.Errorf("--until-alert %q is not one of none, sound, banner", v)
			}
			t.UntilAlert = Alert(v)
		case flag == "--ask" && !hasValue:
			t.Ask = true
		case flag == "--disabled" && !hasValue:
			t.Disabled = true
		case strings.HasPrefix(w, "-") && len(w) > 1:
			return Trigger{}, fmt.Errorf("%q is not an option a trigger takes (it takes --reminder, --ask, --when-busy, --grace, --until-alert and --disabled)", w)
		default:
			*section = append(*section, w)
		}
	}
	if t.Days, err = parseDays(days); err != nil {
		return Trigger{}, err
	}
	if len(days) == 0 {
		t.Days, _ = parseDays([]string{"daily"})
	}
	if excepting && len(except) == 0 {
		return Trigger{}, errors.New("except needs the days to leave out")
	}
	if t.Days, err = withoutDays(t.Days, except); err != nil {
		return Trigger{}, err
	}
	if err := t.check(); err != nil {
		return Trigger{}, err
	}
	return t, nil
}

// withoutDays takes the excepted days out of days.
func withoutDays(days []time.Weekday, except []string) ([]time.Weekday, error) {
	if len(except) == 0 {
		return days, nil
	}
	out, err := parseDays(except)
	if err != nil {
		return nil, fmt.Errorf("except: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("except needs the days to leave out")
	}
	var kept []time.Weekday
	for _, d := range days {
		if !slices.Contains(out, d) {
			kept = append(kept, d)
		}
	}
	return kept, nil
}

// parseGrace reads a grace period, which may be 0 (no lateness at all).
func parseGrace(v string) (time.Duration, error) {
	if v == "0" {
		return 0, nil
	}
	return decide.ParseDuration(v)
}

// Command writes the trigger as the words that ParseTrigger reads back.
func (t Trigger) Command() string {
	parts := []string{daysWord(t.Days), "at", t.At}
	if t.Until != "" {
		parts = append(parts, "until", t.Until)
	}
	if t.Reminder > 0 {
		parts = append(parts, "--reminder", DurationWord(t.Reminder))
	}
	if t.Ask {
		parts = append(parts, "--ask")
	}
	if t.WhenBusy != "" && t.WhenBusy != BusyAsk {
		parts = append(parts, "--when-busy", string(t.WhenBusy))
	}
	if t.Grace != DefaultGrace {
		g := "0"
		if t.Grace > 0 {
			g = DurationWord(t.Grace)
		}
		parts = append(parts, "--grace", g)
	}
	if t.UntilAlert != "" && t.UntilAlert != AlertNone {
		parts = append(parts, "--until-alert", string(t.UntilAlert))
	}
	if t.Disabled {
		parts = append(parts, "--disabled")
	}
	return strings.Join(parts, " ")
}

// clockOn is the moment HH:MM on the calendar day of date, in date's time zone. A time that does not exist that day (the clocks went
// forward over it) is taken as an hour later, the first moment after the gap, not an hour earlier as time.Date may choose.
func clockOn(date time.Time, hhmm string) time.Time {
	h, _ := strconv.Atoi(hhmm[:2])
	m, _ := strconv.Atoi(hhmm[3:])
	t := time.Date(date.Year(), date.Month(), date.Day(), h, m, 0, 0, date.Location())
	if t.Hour() != h && h < 23 {
		t = time.Date(date.Year(), date.Month(), date.Day(), h+1, m, 0, 0, date.Location())
	}
	return t
}

// StartOn is the trigger's start on the calendar day of date (in date's time zone), or false if it does not start that day.
func (t Trigger) StartOn(date time.Time) (time.Time, bool) {
	if !slices.Contains(t.Days, date.Weekday()) {
		return time.Time{}, false
	}
	return clockOn(date, t.At), true
}

// EndFor is the hard stop for a start, or false if the trigger has none.
func (t Trigger) EndFor(start time.Time) (time.Time, bool) {
	if t.Until == "" {
		return time.Time{}, false
	}
	return clockOn(start, t.Until), true
}

// ReminderFor is when the reminder banner for a start is due, or false if the trigger has none.
func (t Trigger) ReminderFor(start time.Time) (time.Time, bool) {
	if t.Reminder <= 0 {
		return time.Time{}, false
	}
	return start.Add(-t.Reminder), true
}

// NextStart is the first start strictly after the given moment, in that moment's time zone, or false for a trigger with no days.
func (t Trigger) NextStart(after time.Time) (time.Time, bool) {
	for i := 0; i <= 7; i++ {
		day := after.AddDate(0, 0, i)
		if start, ok := t.StartOn(day); ok && start.After(after) {
			return start, true
		}
	}
	return time.Time{}, false
}

// NextStart is the first start of any enabled trigger after the given moment, and which trigger it is.
func (r Routine) NextStart(after time.Time) (time.Time, int, bool) {
	var best time.Time
	index := -1
	for i, t := range r.Triggers {
		if t.Disabled {
			continue
		}
		if s, ok := t.NextStart(after); ok && (index < 0 || s.Before(best)) {
			best, index = s, i
		}
	}
	return best, index, index >= 0
}

// buildTrigger turns a trigger as written (a string or an object) into a Trigger, checked.
func buildTrigger(f fileTrigger) (Trigger, error) {
	if f.fromString {
		return ParseTrigger(f.command)
	}
	t := Trigger{UntilAlert: AlertNone, WhenBusy: BusyAsk, Grace: DefaultGrace}
	var err error
	if len(f.Days) == 0 {
		f.Days = fileDays{"daily"}
	}
	if t.Days, err = parseDays(f.Days); err != nil {
		return Trigger{}, fmt.Errorf("days: %w", err)
	}
	if t.Days, err = withoutDays(t.Days, f.Except); err != nil {
		return Trigger{}, err
	}
	if f.At != nil {
		at, ok := NormalClock(*f.At)
		if !ok {
			return Trigger{}, fmt.Errorf("at %q is not a time of day such as 09:00", *f.At)
		}
		t.At = at
	}
	if f.Until != nil {
		until, ok := NormalClock(*f.Until)
		if !ok {
			return Trigger{}, fmt.Errorf("until %q is not a time of day such as 12:00", *f.Until)
		}
		t.Until = until
	}
	if f.UntilAlert != nil {
		a := Alert(*f.UntilAlert)
		if !slices.Contains([]Alert{AlertNone, AlertSound, AlertBanner}, a) {
			return Trigger{}, fmt.Errorf("until_alert %q is not one of none, sound, banner", *f.UntilAlert)
		}
		t.UntilAlert = a
	}
	if f.Reminder != nil {
		if t.Reminder, err = decide.ParseDuration(*f.Reminder); err != nil {
			return Trigger{}, fmt.Errorf("reminder: %w", err)
		}
	}
	if f.Ask != nil {
		t.Ask = *f.Ask
	}
	if f.WhenBusy != nil {
		b := WhenBusy(*f.WhenBusy)
		if !slices.Contains([]WhenBusy{BusyAsk, BusySkip, BusyStart}, b) {
			return Trigger{}, fmt.Errorf("when_busy %q is not one of ask, skip, start", *f.WhenBusy)
		}
		t.WhenBusy = b
	}
	if f.Grace != nil {
		if t.Grace, err = parseGrace(*f.Grace); err != nil {
			return Trigger{}, fmt.Errorf("grace: %w", err)
		}
	}
	if f.Enabled != nil {
		t.Disabled = !*f.Enabled
	}
	if err := t.check(); err != nil {
		return Trigger{}, err
	}
	return t, nil
}
