// Package routine is the format of saved routines: named sequences of focus sessions, such as a pomodoro (work 25 minutes, break 5,
// four times, then a long break). They live in routines.json next to config.json, as JSON that may have comments, filed under their
// names in an object so that each name can be there only once, and this package
// reads and checks that file and lays a routine out as the flat list of steps a run goes through. Running one is the worker's job.
package routine

import (
	"bytes"
	"encoding/json"
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

// Step is one step of a routine: a timed focus session, or a change of the baseline.
type Step struct {
	Kind   StepKind // StepSession unless it is a baseline step
	Name   string
	Level  int           // 0-99, as a session's threshold (or, for a baseline step, the baseline); 0 is a break with nothing held back
	Length time.Duration // zero for a baseline step, and for a step that lasts until a time of day
	// Until is "HH:MM" for a session that lasts until then instead of for a length. The time is worked out when the step starts, so a
	// step whose time has gone by is skipped.
	Until   string
	Note    string
	Alert   Alert
	Advance Advance
	// Call and Times are for a call step: the routine to run and how many times. They are gone once Expand has run.
	Call  string
	Times int
}

// Routine is a named sequence of steps: Steps run Repeat times, then Finish once.
type Routine struct {
	Name        string
	Description string
	// Repeat is how many times Steps run, or Forever to go round until the run is stopped.
	Repeat  int
	Steps   []Step
	Finish  []Step
	Waiting Waiting
	Summary []Where
}

// Forever as a routine's Repeat means going round until stopped.
const Forever = -1

// Planned is one step in the order a run goes through them.
type Planned struct {
	Step
	// Index is the position in the plan, from 0.
	Index int
	// Round is which repeat this step belongs to, from 1, and 0 for a step in Finish. Rounds is Repeat.
	Round, Rounds int
}

// Plan lays the routine out as a flat list: the steps Repeat times, then the finish steps. A routine that repeats forever is laid out
// as one round (Rounds is then 0) which the run goes round again, and has no finish steps.
func (r Routine) Plan() []Planned {
	var plan []Planned
	rounds := r.Repeat
	if rounds == Forever {
		rounds = 1
	}
	shown := r.Repeat
	if shown == Forever {
		shown = 0
	}
	for round := 1; round <= rounds; round++ {
		for _, s := range r.Steps {
			plan = append(plan, Planned{Step: s, Index: len(plan), Round: round, Rounds: shown})
		}
	}
	for _, s := range r.Finish {
		plan = append(plan, Planned{Step: s, Index: len(plan), Round: 0, Rounds: shown})
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

// Total is how long the routine takes if nothing is paused or waited for. Steps that last until a time of day are not counted (see
// Exact), and a routine that repeats forever counts one round.
func (r Routine) Total() time.Duration {
	var total time.Duration
	for _, p := range r.Plan() {
		total += p.Length
	}
	return total
}

// Exact says whether Total is the whole story: no step lasts until a time of day, and the routine ends by itself.
func (r Routine) Exact() bool {
	if r.Repeat == Forever {
		return false
	}
	for _, s := range slices.Concat(r.Steps, r.Finish) {
		if s.Until != "" {
			return false
		}
	}
	return true
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

// fileStep is a step as written: an object with its fields spelled out, or a string, which is the shh command that does it
// ("75% 25m work --alert banner", "baseline 30%").
type fileStep struct {
	Name     *string `json:"name"`
	Level    *string `json:"level"`
	Length   *string `json:"length"`
	Until    *string `json:"until"`
	Note     *string `json:"note"`
	Alert    *string `json:"alert"`
	Advance  *string `json:"advance"`
	Baseline *string `json:"baseline"`

	command    string // the string, when the step was written as one
	fromString bool
}

// UnmarshalJSON reads a step from either form. The decoder's key checking still sees the struct above for the object form.
func (f *fileStep) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '"' {
		f.fromString = true
		return json.Unmarshal(trimmed, &f.command)
	}
	type plain fileStep // without the methods, so this does not call itself
	return json.Unmarshal(data, (*plain)(f))
}

// fileRepeat is a routine's repeat as written: a number, or the word forever.
type fileRepeat struct{ n int }

func (f *fileRepeat) UnmarshalJSON(data []byte) error {
	var word string
	if json.Unmarshal(data, &word) == nil {
		if strings.EqualFold(word, "forever") {
			f.n = Forever
			return nil
		}
		return fmt.Errorf("repeat %q is not a number or forever", word)
	}
	return json.Unmarshal(data, &f.n)
}

type fileRoutine struct {
	Description *string     `json:"description"`
	Repeat      *fileRepeat `json:"repeat"`
	Steps       []fileStep  `json:"steps"`
	Finish      []fileStep  `json:"finish"`
	Waiting     *string     `json:"waiting"`
	Summary     []string    `json:"summary"`
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
func Parse(name string, raw []byte) ([]Routine, error) { return parse(name, raw, true) }

// parse reads the file; resolve says whether to check that every call step can be expanded, which needs the other routines to be in
// the text (Check leaves it out, since it looks at one routine on its own).
func parse(name string, raw []byte, resolve bool) ([]Routine, error) {
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
	if resolve {
		for _, r := range out {
			if _, err := Expand(out, r); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
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
)

const maxRepeat = 99

func build(fr fileRoutine, name string) (Routine, error) {
	key := routineKey(name)
	bad := func(field, format string, args ...any) error {
		return fmt.Errorf("%q: %s", key+"."+field, fmt.Sprintf(format, args...))
	}
	r := Routine{Repeat: 1, Waiting: WaitStay, Summary: []Where{WhereBanner, WhereUI}}
	// The name is the key it is filed under, and it is typed on the command line.
	if err := ValidName(name); err != nil {
		return r, fmt.Errorf("%q: %w", key, err)
	}
	r.Name = name
	if fr.Description != nil {
		r.Description = *fr.Description
	}
	if fr.Repeat != nil {
		if n := fr.Repeat.n; n != Forever && (n < 1 || n > maxRepeat) {
			return r, bad("repeat", "%d is not between 1 and %d (or forever)", n, maxRepeat)
		}
		r.Repeat = fr.Repeat.n
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
	if r.Repeat == Forever && len(r.Finish) > 0 {
		return r, bad("finish", "a routine that repeats forever never gets to its finish steps: take them out, or give repeat a number")
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
	return r, nil
}

func buildSteps(fs []fileStep, key string) ([]Step, error) {
	var out []Step
	for i, f := range fs {
		k := fmt.Sprintf("%s[%d]", key, i)
		bad := func(field, format string, args ...any) error {
			path := k
			if field != "" {
				path += "." + field
			}
			return fmt.Errorf("%q: %s", path, fmt.Sprintf(format, args...))
		}
		if f.fromString {
			s, err := ParseCommand(f.command)
			if err != nil {
				return nil, bad("", "%v", err)
			}
			out = append(out, s)
			continue
		}
		if f.Baseline != nil {
			if f.Name != nil || f.Level != nil || f.Length != nil || f.Until != nil || f.Note != nil || f.Alert != nil || f.Advance != nil {
				return nil, bad("baseline", "a baseline step sets the standing level and nothing else: it takes no other fields")
			}
			level, err := decide.ParseThreshold(*f.Baseline)
			if err != nil {
				return nil, bad("baseline", "%v", err)
			}
			out = append(out, Step{Kind: StepBaseline, Name: "baseline " + LevelWord(level), Level: level})
			continue
		}
		s := Step{Kind: StepSession, Level: 65, Alert: AlertNone, Advance: AdvanceAuto}
		if f.Name == nil || strings.TrimSpace(*f.Name) == "" {
			return nil, bad("name", "a step needs a name")
		}
		s.Name = strings.TrimSpace(*f.Name)
		if f.Level != nil {
			level, err := decide.ParseThreshold(*f.Level)
			if err != nil {
				return nil, bad("level", "%v", err)
			}
			s.Level = level
		}
		switch {
		case f.Length != nil && f.Until != nil:
			return nil, bad("until", "a step has a length or an end time, not both")
		case f.Until != nil:
			at, ok := NormalClock(*f.Until)
			if !ok {
				return nil, bad("until", "%q is not a time of day such as 11:00", *f.Until)
			}
			s.Until = at
		case f.Length == nil:
			return nil, bad("length", "a step needs a length such as 25m, or until a time such as 11:00")
		default:
			length, err := decide.ParseDuration(*f.Length)
			if err != nil {
				return nil, bad("length", "%v", err)
			}
			if length <= 0 {
				return nil, bad("length", "%q is not above zero: a step that never ends would never hand over", *f.Length)
			}
			s.Length = length
		}
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
        // A step is written as the shh command that would do it: a level, a length and a name, in any order. level: a percentage or a tier
        // (off, light, medium, deep, lockdown). --alert banner (or sound) announces the end of the step.
        "75% 25m work --alert banner",
        "off 5m break --alert banner"
      ],
      "finish": [
        "off 15m long break"
      ],
      // what focus does while a step waits for you: stay (keep the last level), next (the next step's level, timer held), baseline
      "waiting": "stay",
      // where the summary shows when the routine finishes: banner, menu, ui
      "summary": ["banner", "ui"]
    },
    "deep work": {
      "steps": [
        "baseline 30%",                         // set the standing level; it takes no time, and the routine goes straight on
        "deep 90m deep work --alert banner --ask", // --ask waits for you ("shh routine next") before the next step
        {"name": "rest", "level": "off", "length": "20m"}, // or spell a step out as an object: name, level, length (or until), note, alert, advance
        "baseline off"
      ]
    },
    "afternoon": {
      "steps": [
        "run pomodoro --times 2",            // another routine's steps, twice (a routine cannot call itself, directly or through others)
        "50% until 17:00 email and wrap up"  // lasts until a time of day instead of for a length; skipped if that time has already passed
      ]
    },
    "all day": {
      "repeat": "forever",                    // go round and round until stopped (so no finish steps)
      "steps": ["75% 25m work", "off 5m break"]
    }
  }
}
`

// Outline is the routine's steps on one line, each as the shh command that does it: "75% 25m work -> off 5m break, 4 times -> off
// 15m long break".
func (r Routine) Outline() string {
	part := func(steps []Step) string {
		var out []string
		for _, s := range steps {
			out = append(out, s.Command())
		}
		return strings.Join(out, " -> ")
	}
	line := part(r.Steps)
	switch {
	case r.Repeat == Forever:
		line += ", forever"
	case r.Repeat > 1:
		line += fmt.Sprintf(", %d times", r.Repeat)
	}
	if len(r.Finish) > 0 {
		line += " -> " + part(r.Finish)
	}
	return line
}

// ValidName says whether a routine can be called that.
func ValidName(name string) error {
	if name != strings.TrimSpace(name) || !validName.MatchString(name) {
		return fmt.Errorf("the routine name %q should start with a letter or digit and use only letters, digits, spaces, dots, dashes and underscores (it is typed on the command line)", name)
	}
	return nil
}
