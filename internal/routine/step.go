package routine

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/decide"
)

// StepKind says what a step does.
type StepKind string

const (
	// StepSession is a timed focus session: it takes its length, then the routine moves on.
	StepSession StepKind = "session"
	// StepBaseline sets the standing level. It takes no time: the routine applies it when it gets there and goes straight on.
	StepBaseline StepKind = "baseline"
	// StepCall runs another routine in this one's place. Reading the file turns each call into the steps it stands for (see Expand), so
	// a run never meets one.
	StepCall StepKind = "call"
)

// clockWord is a time of day as typed after "until": 9:00, 09:00, 17:30.
var clockWord = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):([0-5][0-9])$`)

// untilClock finds "until 11:00" inside a name, which would be read as the end time of the step.
var untilClock = regexp.MustCompile(`(?i)\buntil\s+([01]?[0-9]|2[0-3]):[0-5][0-9]\b`)

// NormalClock writes a time of day as HH:MM, or says it is not one.
func NormalClock(text string) (string, bool) {
	m := clockWord.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	hour, _ := strconv.Atoi(m[1])
	return fmt.Sprintf("%02d:%s", hour, m[2]), true
}

// ParseCommand reads a step written as the shh command that would do it:
//
//	75% 25m work --alert banner     a 25 minute session at 75%, called work, announced when it ends
//	off 5m break --ask              a break that waits for you before the next step
//	baseline 30%                    set the baseline to 30% (baseline off clears it)
//	90% until 11:00 deep work       a session that lasts until 11:00 (a step that starts after that is skipped)
//	run pomodoro --times 2          the steps of another routine, twice
//
// It takes what `shh focus` takes (a level, a length and a note, in any order, with the same shorthand) plus --ask, which only means
// something in a routine. A leading "shh" or "shh focus" is allowed, so a line can be pasted from a terminal. The words that are not a
// level or a length name the step and are its note; -m gives a different note. Nothing is run through a shell: the line is only read.
func ParseCommand(text string) (Step, error) {
	words, err := splitCommand(text)
	if err != nil {
		return Step{}, err
	}
	if len(words) > 0 && words[0] == "shh" {
		words = words[1:]
	}
	if len(words) > 0 && (words[0] == "focus" || words[0] == "f") {
		words = words[1:]
	}
	if len(words) == 0 {
		return Step{}, errors.New("an empty step: write a command such as 75% 25m work, or baseline 30%")
	}
	if words[0] == "baseline" {
		return parseBaseline(words[1:])
	}
	if words[0] == "run" {
		return parseCall(words[1:])
	}

	s := Step{Kind: StepSession, Alert: AlertNone, Advance: AdvanceAuto}
	var positional []string
	var noteFlag *string
	for i := 0; i < len(words); i++ {
		w := words[i]
		flag, value, hasValue := strings.Cut(w, "=")
		take := func() (string, error) { // the value of a flag: after =, or the next word
			if hasValue {
				return value, nil
			}
			if i+1 >= len(words) {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			i++
			return words[i], nil
		}
		switch {
		case flag == "--alert":
			v, err := take()
			if err != nil {
				return Step{}, err
			}
			if !slices.Contains([]Alert{AlertNone, AlertSound, AlertBanner}, Alert(v)) {
				return Step{}, fmt.Errorf("--alert %q is not one of none, sound, banner", v)
			}
			s.Alert = Alert(v)
		case flag == "--ask" && !hasValue:
			s.Advance = AdvanceAsk
		case flag == "--color" || flag == "--text-color" || flag == "--position" || flag == "--size" || flag == "--sound" || flag == "--banner-for":
			v, err := take()
			if err != nil {
				return Step{}, err
			}
			if err := setStyleFlag(&s.Style, flag, v); err != nil {
				return Step{}, err
			}
		case flag == "-m" || flag == "--note":
			v, err := take()
			if err != nil {
				return Step{}, err
			}
			noteFlag = &v
		case flag == "--then" || flag == "--end-routine" || flag == "--clear":
			return Step{}, fmt.Errorf("%s does not apply in a routine (the routine decides what comes next)", flag)
		case strings.HasPrefix(w, "-") && len(w) > 1:
			return Step{}, fmt.Errorf("%q is not an option a routine step takes (it takes --alert, --ask, -m and the banner style flags --color, --text-color, --position, --size, --sound and --banner-for)", w)
		default:
			positional = append(positional, w)
		}
	}
	// "until 11:00" stands where a length would. It is swapped for a length word that cannot be typed (so that "off until 11:00 break"
	// still reads off as the level, which it only does beside another setting), and swapped back out after the rest is read.
	const untilStandIn = "99999h"
	for i := 0; i < len(positional); i++ {
		if strings.EqualFold(positional[i], "until") && i+1 < len(positional) {
			if at, ok := NormalClock(positional[i+1]); ok { // otherwise it is just a word of the name
				s.Until = at
				positional = slices.Replace(positional, i, i+2, untilStandIn)
				break
			}
		}
	}
	levelText, lengthText, rest, err := decide.SplitFocusWords("", "", positional)
	if err != nil {
		if s.Until != "" && strings.Contains(err.Error(), untilStandIn) {
			return Step{}, errors.New("a step has a length or an end time (until ...), not both")
		}
		return Step{}, err
	}
	if lengthText == untilStandIn {
		lengthText = ""
	}
	if levelText == "" {
		levelText = "medium"
	}
	if s.Level, err = decide.ParseThreshold(levelText); err != nil {
		return Step{}, err
	}
	if lengthText == "" && s.Until == "" {
		return Step{}, errors.New("a step needs a length such as 25m, or an end time such as until 11:00")
	}
	if lengthText != "" {
		if s.Length, err = decide.ParseDuration(lengthText); err != nil {
			return Step{}, err
		}
		if s.Length <= 0 {
			return Step{}, fmt.Errorf("%q is not above zero: a step that never ends would never hand over", lengthText)
		}
	}
	s.Name = strings.Join(rest, " ")
	s.Note = s.Name
	if s.Name == "" {
		s.Name = s.defaultName() // nothing to call it by: say what it is
	}
	if noteFlag != nil {
		s.Note = *noteFlag
	}
	if err := checkStyle(s.Style, "style"); err != nil {
		return Step{}, err
	}
	return s, nil
}

// setStyleFlag reads one of the banner style flags of a step into its style: --color 208 (a code from the 8-bit ANSI palette),
// --position top-left, --size large, --sound Hero, --banner-for 10s (0 for until dismissed).
func setStyleFlag(o *config.StyleOverride, flag, value string) error {
	switch flag {
	case "--color", "--text-color":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s %q is not a number from 0 to 255 (the 8-bit ANSI palette: shh alert colors shows it)", flag, value)
		}
		if flag == "--color" {
			o.Color = &n
		} else {
			o.TextColor = &n
		}
	case "--position":
		o.Position = &value
	case "--size":
		o.Size = &value
	case "--sound":
		o.Sound = &value
	case "--banner-for":
		d, err := decide.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("--banner-for: %w", err)
		}
		secs := int(d / time.Second)
		o.DurationSecs = &secs
	}
	return nil
}

// ParseStyle reads a banner style written as the flags a step takes: "--color 33 --size large --position bottom-left --sound Hero
// --banner-for 10s". Nothing at all is no change.
func ParseStyle(text string) (config.StyleOverride, error) {
	words, err := splitCommand(text)
	if err != nil {
		return config.StyleOverride{}, err
	}
	var o config.StyleOverride
	for i := 0; i < len(words); i++ {
		flag, value, hasValue := strings.Cut(words[i], "=")
		if !slices.Contains([]string{"--color", "--text-color", "--position", "--size", "--sound", "--banner-for"}, flag) {
			return o, fmt.Errorf("%q is not a style flag: use --color, --text-color, --position, --size, --sound and --banner-for", words[i])
		}
		if !hasValue {
			if i+1 >= len(words) {
				return o, fmt.Errorf("%s needs a value", flag)
			}
			i++
			value = words[i]
		}
		if err := setStyleFlag(&o, flag, value); err != nil {
			return o, err
		}
	}
	if err := checkStyle(o, "style"); err != nil {
		return o, err
	}
	return o, nil
}

// StyleWords writes a style as the flags ParseStyle reads back ("" for no change).
func StyleWords(o config.StyleOverride) string { return strings.Join(styleFlags(o), " ") }

// styleFlags writes a step's style as the flags that ParseCommand reads back.
func styleFlags(o config.StyleOverride) []string {
	var out []string
	if o.Color != nil {
		out = append(out, "--color", strconv.Itoa(*o.Color))
	}
	if o.TextColor != nil {
		out = append(out, "--text-color", strconv.Itoa(*o.TextColor))
	}
	if o.Position != nil {
		out = append(out, "--position", *o.Position)
	}
	if o.Size != nil {
		out = append(out, "--size", *o.Size)
	}
	if o.Sound != nil {
		out = append(out, "--sound", quoteWord(*o.Sound))
	}
	if o.DurationSecs != nil {
		w := "0"
		if *o.DurationSecs > 0 {
			w = DurationWord(time.Duration(*o.DurationSecs) * time.Second)
		}
		out = append(out, "--banner-for", w)
	}
	return out
}

// parseCall reads what follows the word run: a routine's name (several words, so a name with spaces needs no quotes) and the flags
// --times N, --ask and --alert. --ask and --alert apply when the called routine ends.
func parseCall(words []string) (Step, error) {
	s := Step{Kind: StepCall, Times: 1, Alert: AlertNone, Advance: AdvanceAuto}
	var name []string
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
		switch {
		case flag == "--times":
			v, err := take()
			if err != nil {
				return Step{}, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > maxRepeat {
				return Step{}, fmt.Errorf("--times %q is not a number from 1 to %d", v, maxRepeat)
			}
			s.Times = n
		case flag == "--alert":
			v, err := take()
			if err != nil {
				return Step{}, err
			}
			if !slices.Contains([]Alert{AlertNone, AlertSound, AlertBanner}, Alert(v)) {
				return Step{}, fmt.Errorf("--alert %q is not one of none, sound, banner", v)
			}
			s.Alert = Alert(v)
		case flag == "--ask" && !hasValue:
			s.Advance = AdvanceAsk
		case strings.HasPrefix(w, "-") && len(w) > 1:
			return Step{}, fmt.Errorf("%q is not an option run takes (it takes --times, --alert and --ask)", w)
		default:
			name = append(name, w)
		}
	}
	if len(name) == 0 {
		return Step{}, errors.New("run needs the name of a routine, such as: run pomodoro")
	}
	s.Call = strings.Join(name, " ")
	s.Name = s.Call
	return s, nil
}

// parseBaseline reads what follows the word baseline: exactly one level.
func parseBaseline(args []string) (Step, error) {
	if len(args) != 1 {
		return Step{}, errors.New("baseline takes one level, such as: baseline 30%  (baseline off clears it)")
	}
	level, err := decide.ParseThreshold(args[0])
	if err != nil {
		return Step{}, err
	}
	return Step{Kind: StepBaseline, Name: "baseline " + LevelWord(level), Level: level}, nil
}

// splitCommand splits a line into words the way a shell would for ordinary use: on spaces, with double or single quotes keeping a
// phrase together and a backslash taking the next character literally outside single quotes. It only splits; nothing is expanded or run.
func splitCommand(text string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote != 0:
			switch {
			case c == quote:
				quote = 0
			case c == '\\' && quote == '"' && i+1 < len(runes):
				i++
				cur.WriteRune(runes[i])
			default:
				cur.WriteRune(c)
			}
		case c == '"' || c == '\'':
			quote, inWord = c, true
		case c == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("a quote is opened with %c and never closed", quote)
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// Command writes the step as the command that does it, for showing in lists and the editor: what ParseCommand reads back.
func (s Step) Command() string {
	if s.Kind == StepBaseline {
		return "baseline " + LevelWord(s.Level)
	}
	if s.Kind == StepCall {
		parts := []string{"run", quoteWord(s.Call)}
		if s.Times > 1 {
			parts = append(parts, "--times", strconv.Itoa(s.Times))
		}
		if s.Alert != AlertNone && s.Alert != "" {
			parts = append(parts, "--alert", string(s.Alert))
		}
		if s.Advance == AdvanceAsk {
			parts = append(parts, "--ask")
		}
		return strings.Join(parts, " ")
	}
	parts := []string{LevelWord(s.Level), s.lengthWord()}
	defaultName := s.defaultName()
	note := s.Note
	// The name is the words of the command. A name that starts like a level or a length would be read as one, so it goes in -m instead.
	if s.Name != "" && s.Name != defaultName {
		if first := strings.Fields(s.Name)[0]; decide.IsPercentWord(first) || decide.IsDurationWord(first) || untilClock.MatchString(s.Name) {
			note = s.Name
		} else {
			parts = append(parts, s.Name)
			if note == s.Name {
				note = ""
			} else if note == "" {
				note = "" // an object step with a name and no note: the command gives the name; the note stays empty on reading back as the name
			}
		}
	}
	if note != "" && note != s.Name {
		parts = append(parts, "-m", quoteWord(note))
	}
	if s.Alert != AlertNone && s.Alert != "" {
		parts = append(parts, "--alert", string(s.Alert))
	}
	if s.Advance == AdvanceAsk {
		parts = append(parts, "--ask")
	}
	parts = append(parts, styleFlags(s.Style)...)
	return strings.Join(parts, " ")
}

// lengthWord is how a step's length is typed: 25m, or until 11:00.
func (s Step) lengthWord() string {
	if s.Until != "" {
		return "until " + s.Until
	}
	return DurationWord(s.Length)
}

// defaultName is what a step with no name is called: what it is.
func (s Step) defaultName() string { return LevelWord(s.Level) + " " + s.lengthWord() }

// quoteWord wraps a phrase in double quotes when it has spaces or quotes in it.
func quoteWord(w string) string {
	if w == "" || strings.ContainsAny(w, " \t\"'\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(w) + `"`
	}
	return w
}

// LevelWord writes a level the way it is typed: 75%, or off for none.
func LevelWord(level int) string {
	if level == 0 {
		return "off"
	}
	return fmt.Sprintf("%d%%", level)
}

// DurationWord writes a length the way it is typed: 25m, 1h30m, 90s.
func DurationWord(d time.Duration) string {
	var out string
	if h := d / time.Hour; h > 0 {
		out += fmt.Sprintf("%dh", h)
	}
	if m := d % time.Hour / time.Minute; m > 0 {
		out += fmt.Sprintf("%dm", m)
	}
	if s := d % time.Minute / time.Second; s > 0 {
		out += fmt.Sprintf("%ds", s)
	}
	return out
}
