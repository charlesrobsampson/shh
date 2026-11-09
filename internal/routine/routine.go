// Package routine is the format of saved routines: named sequences of focus sessions, such as a pomodoro (work 25 minutes, break 5,
// four times, then a long break). They live in routines.json next to config.json, as JSON that may have comments, filed under their
// names in an object so that each name can be there only once, and this package
// reads and checks that file and lays a routine out as the flat list of steps a run goes through. Running one is the worker's job.
package routine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/jsonc"
)

// FileName is the routines file, in the configuration directory.
const FileName = "routines.json"

// Alert says how the end of a step is announced.
type Alert string

const (
	AlertNone   Alert = "none"
	AlertSound  Alert = "sound"
	AlertBanner Alert = "banner"
)

// Advance says how a step hands over to the next: straight away, or when the user says so.
type Advance string

const (
	AdvanceAuto Advance = "auto"
	AdvanceAsk  Advance = "ask"
)

// Waiting says what focus does while a routine waits for the user to start the next step.
type Waiting string

const (
	// WaitStay keeps the level of the step that just ended (the default: walking away does not let notifications through).
	WaitStay Waiting = "stay"
	// WaitNext puts the next step's level in force at once, with its countdown held until the user starts it.
	WaitNext Waiting = "next"
	// WaitBaseline falls back to whatever is beneath: the baseline, or nothing.
	WaitBaseline Waiting = "baseline"
)

// Where a finished routine's summary is shown.
type Where string

const (
	WhereBanner Where = "banner"
	WhereMenu   Where = "menu"
	WhereUI     Where = "ui"
)

// Step is one session of a routine.
type Step struct {
	Name    string
	Level   int // 0-99, as a session's threshold; 0 is a break with nothing held back
	Length  time.Duration
	Note    string
	Alert   Alert
	Advance Advance
}

// Schedule says when a routine starts by itself. Reserved: it is read and checked now so files written today keep working, and the
// worker will act on it in a later phase.
type Schedule struct {
	Days []time.Weekday
	At   string // "15:04"
}

// Routine is a named sequence of steps: Steps run Repeat times, then Finish once.
type Routine struct {
	Name        string
	Description string
	Repeat      int
	Steps       []Step
	Finish      []Step
	Waiting     Waiting
	Summary     []Where
	Schedule    []Schedule
}

// Planned is one step in the order a run goes through them.
type Planned struct {
	Step
	// Index is the position in the plan, from 0.
	Index int
	// Round is which repeat this step belongs to, from 1, and 0 for a step in Finish. Rounds is Repeat.
	Round, Rounds int
}

// Plan lays the routine out as a flat list: the steps Repeat times, then the finish steps.
func (r Routine) Plan() []Planned {
	var plan []Planned
	for round := 1; round <= r.Repeat; round++ {
		for _, s := range r.Steps {
			plan = append(plan, Planned{Step: s, Index: len(plan), Round: round, Rounds: r.Repeat})
		}
	}
	for _, s := range r.Finish {
		plan = append(plan, Planned{Step: s, Index: len(plan), Round: 0, Rounds: r.Repeat})
	}
	return plan
}

// Label is what a step is called on screen: "work (2/4)" for the second of four rounds, "long break" for a finish step, and just the
// name when the routine does not repeat.
func (p Planned) Label() string {
	if p.Round == 0 || p.Rounds <= 1 {
		return p.Name
	}
	return fmt.Sprintf("%s (%d/%d)", p.Name, p.Round, p.Rounds)
}

// Total is how long the routine takes if nothing is paused or waited for.
func (r Routine) Total() time.Duration {
	var total time.Duration
	for _, p := range r.Plan() {
		total += p.Length
	}
	return total
}

// Find returns the routine with that name, ignoring case.
func Find(routines []Routine, name string) (Routine, bool) {
	for _, r := range routines {
		if strings.EqualFold(r.Name, strings.TrimSpace(name)) {
			return r, true
		}
	}
	return Routine{}, false
}

// ---- the file ----

type fileStep struct {
	Name    *string `json:"name"`
	Level   *string `json:"level"`
	Length  *string `json:"length"`
	Note    *string `json:"note"`
	Alert   *string `json:"alert"`
	Advance *string `json:"advance"`
}

type fileSchedule struct {
	Days []string `json:"days"`
	At   *string  `json:"at"`
}

type fileRoutine struct {
	Description *string        `json:"description"`
	Repeat      *int           `json:"repeat"`
	Steps       []fileStep     `json:"steps"`
	Finish      []fileStep     `json:"finish"`
	Waiting     *string        `json:"waiting"`
	Summary     []string       `json:"summary"`
	Schedule    []fileSchedule `json:"schedule"`
}

// file is routines.json: an object of routines keyed by name, so a name can only be there once (the decoder rejects a repeated key) and
// a routine is found by its name directly.
type file struct {
	Routines map[string]fileRoutine `json:"routines"`
}

// Load reads the routines file. A missing file means no routines. A problem in the file is an error that names the file, the line
// where it can be told, and the key (for example routines[0].steps[1].level), and no routines are returned: a half-read file would
// start the wrong thing.
func Load(path string) ([]Routine, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(path, raw)
}

// Parse reads routines from the text of a routines file; name is shown in errors. The routines come back in the order they are in the
// file.
func Parse(name string, raw []byte) ([]Routine, error) {
	var f file
	if err := jsonc.Decode(name, raw, &f); err != nil {
		return nil, err
	}
	order, err := jsonc.Keys(name, raw, "routines")
	if err != nil {
		return nil, err
	}
	var out []Routine
	seen := map[string]string{} // lower-cased name -> the name as written
	for _, key := range order {
		r, err := build(f.Routines[key], key)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		lower := strings.ToLower(key)
		if first, dup := seen[lower]; dup {
			return nil, fmt.Errorf("%s: %q: the routine name %q is the same as %q apart from case, and names are found ignoring case", name, routineKey(key), key, first)
		}
		seen[lower] = key
		out = append(out, r)
	}
	return out, nil
}

var simpleKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// routineKey is how the routine called name is written in error messages: routines.pomodoro, or routines['deep work'] when the name
// has characters that would be ambiguous in a dotted path (single quotes, so the key reads cleanly inside the message's own quotes).
func routineKey(name string) string {
	if simpleKey.MatchString(name) {
		return "routines." + name
	}
	return "routines['" + name + "']"
}

var (
	validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]*$`)
	clock     = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	weekdays  = map[string]time.Weekday{
		"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
	}
)

const maxRepeat = 99

func build(fr fileRoutine, name string) (Routine, error) {
	key := routineKey(name)
	bad := func(field, format string, args ...any) error {
		return fmt.Errorf("%q: %s", key+"."+field, fmt.Sprintf(format, args...))
	}
	r := Routine{Repeat: 1, Waiting: WaitStay, Summary: []Where{WhereBanner, WhereUI}}
	// The name is the key it is filed under, and it is typed on the command line.
	if name != strings.TrimSpace(name) || !validName.MatchString(name) {
		return r, fmt.Errorf("%q: the routine name %q should start with a letter or digit and use only letters, digits, spaces, dots, dashes and underscores (it is typed on the command line)", key, name)
	}
	r.Name = name
	if fr.Description != nil {
		r.Description = *fr.Description
	}
	if fr.Repeat != nil {
		if *fr.Repeat < 1 || *fr.Repeat > maxRepeat {
			return r, bad("repeat", "%d is not between 1 and %d", *fr.Repeat, maxRepeat)
		}
		r.Repeat = *fr.Repeat
	}
	if len(fr.Steps) == 0 {
		return r, bad("steps", "a routine needs at least one step")
	}
	var err error
	if r.Steps, err = buildSteps(fr.Steps, key+".steps"); err != nil {
		return r, err
	}
	if r.Finish, err = buildSteps(fr.Finish, key+".finish"); err != nil {
		return r, err
	}
	if fr.Waiting != nil {
		w := Waiting(*fr.Waiting)
		if !slices.Contains([]Waiting{WaitStay, WaitNext, WaitBaseline}, w) {
			return r, bad("waiting", "%q is not one of stay, next, baseline", *fr.Waiting)
		}
		r.Waiting = w
	}
	if fr.Summary != nil {
		r.Summary = nil
		for i, s := range fr.Summary {
			w := Where(s)
			if !slices.Contains([]Where{WhereBanner, WhereMenu, WhereUI}, w) {
				return r, bad(fmt.Sprintf("summary[%d]", i), "%q is not one of banner, menu, ui", s)
			}
			if !slices.Contains(r.Summary, w) {
				r.Summary = append(r.Summary, w)
			}
		}
	}
	for i, fs := range fr.Schedule {
		sch, err := buildSchedule(fs)
		if err != nil {
			return r, bad(fmt.Sprintf("schedule[%d]", i), "%v", err)
		}
		r.Schedule = append(r.Schedule, sch)
	}
	return r, nil
}

func buildSteps(fs []fileStep, key string) ([]Step, error) {
	var out []Step
	seen := map[string]bool{}
	for i, f := range fs {
		k := fmt.Sprintf("%s[%d]", key, i)
		bad := func(field, format string, args ...any) error {
			return fmt.Errorf("%q: %s", k+"."+field, fmt.Sprintf(format, args...))
		}
		s := Step{Level: 65, Alert: AlertNone, Advance: AdvanceAuto}
		if f.Name == nil || strings.TrimSpace(*f.Name) == "" {
			return nil, bad("name", "a step needs a name")
		}
		s.Name = strings.TrimSpace(*f.Name)
		if seen[strings.ToLower(s.Name)] {
			return nil, bad("name", "the step name %q is used twice in this list", s.Name)
		}
		seen[strings.ToLower(s.Name)] = true
		if f.Level != nil {
			level, err := decide.ParseThreshold(*f.Level)
			if err != nil {
				return nil, bad("level", "%v", err)
			}
			s.Level = level
		}
		if f.Length == nil {
			return nil, bad("length", "a step needs a length such as 25m")
		}
		length, err := decide.ParseDuration(*f.Length)
		if err != nil {
			return nil, bad("length", "%v", err)
		}
		if length <= 0 {
			return nil, bad("length", "%q is not above zero: a step that never ends would never hand over", *f.Length)
		}
		s.Length = length
		if f.Note != nil {
			s.Note = *f.Note
		}
		if f.Alert != nil {
			a := Alert(*f.Alert)
			if !slices.Contains([]Alert{AlertNone, AlertSound, AlertBanner}, a) {
				return nil, bad("alert", "%q is not one of none, sound, banner", *f.Alert)
			}
			s.Alert = a
		}
		if f.Advance != nil {
			a := Advance(*f.Advance)
			if !slices.Contains([]Advance{AdvanceAuto, AdvanceAsk}, a) {
				return nil, bad("advance", "%q is not one of auto, ask", *f.Advance)
			}
			s.Advance = a
		}
		out = append(out, s)
	}
	return out, nil
}

func buildSchedule(f fileSchedule) (Schedule, error) {
	var s Schedule
	if f.At == nil || !clock.MatchString(*f.At) {
		return s, errors.New(`"at" must be a time of day such as "09:00"`)
	}
	s.At = *f.At
	if len(f.Days) == 0 {
		return s, errors.New(`"days" must list at least one day, such as ["mon", "tue"]`)
	}
	for _, d := range f.Days {
		day, ok := weekdays[strings.ToLower(d)]
		if !ok {
			return s, fmt.Errorf("%q is not a day: use mon, tue, wed, thu, fri, sat or sun", d)
		}
		if !slices.Contains(s.Days, day) {
			s.Days = append(s.Days, day)
		}
	}
	return s, nil
}

// DefaultPath is routines.json in the given configuration directory.
func DefaultPath(dir string) string { return filepath.Join(dir, FileName) }

// Example is a routines file to start from: a pomodoro and a deep work block, with every option shown and explained.
const Example = `// Routines: named sequences of focus sessions, filed under their names. Run one with: shh routine run pomodoro
{
  "routines": {
    "pomodoro": {
      "description": "25 minutes on, 5 off, four times, then a long break",
      "repeat": 4, // the steps below run this many times, then the finish steps once
      "steps": [
        // level: a percentage or a tier (off, light, medium, deep, lockdown); length: how long the step lasts
        {"name": "work", "level": "75%", "length": "25m", "note": "focus", "alert": "banner"},
        // alert: none, sound or banner when the step ends; advance: auto starts the next step, ask waits for you
        {"name": "break", "level": "off", "length": "5m", "alert": "banner", "advance": "auto"}
      ],
      "finish": [
        {"name": "long break", "level": "off", "length": "15m"}
      ],
      // what focus does while a step waits for you: stay (keep the last level), next (the next step's level, timer held), baseline
      "waiting": "stay",
      // where the summary shows when the routine finishes: banner, menu, ui
      "summary": ["banner", "ui"]
      // "schedule": [{"days": ["mon", "tue", "wed", "thu", "fri"], "at": "09:00"}] // reserved: not acted on yet
    },
    "deep work": {
      "steps": [
        {"name": "block", "level": "deep", "length": "90m", "note": "deep work", "alert": "banner", "advance": "ask"},
        {"name": "rest", "level": "off", "length": "20m"}
      ]
    }
  }
}
`
