package routine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
)

// StepKind says what a step does.
type StepKind string

const (
	// StepSession is a timed focus session: it takes its length, then the routine moves on.
	StepSession StepKind = "session"
	// StepBaseline sets the standing level. It takes no time: the routine applies it when it gets there and goes straight on.
	StepBaseline StepKind = "baseline"
)

// ParseCommand reads a step written as the shh command that would do it:
//
//	75% 25m work --alert banner     a 25 minute session at 75%, called work, announced when it ends
//	off 5m break --ask              a break that waits for you before the next step
//	baseline 30%                    set the baseline to 30% (baseline off clears it)
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
		case flag == "-m" || flag == "--note":
			v, err := take()
			if err != nil {
				return Step{}, err
			}
			noteFlag = &v
		case flag == "--then" || flag == "--end-routine" || flag == "--clear":
			return Step{}, fmt.Errorf("%s does not apply in a routine (the routine decides what comes next)", flag)
		case strings.HasPrefix(w, "-") && len(w) > 1:
			return Step{}, fmt.Errorf("%q is not an option a routine step takes (it takes --alert, --ask and -m)", w)
		default:
			positional = append(positional, w)
		}
	}
	levelText, lengthText, rest, err := decide.SplitFocusWords("", "", positional)
	if err != nil {
		return Step{}, err
	}
	if levelText == "" {
		levelText = "medium"
	}
	if s.Level, err = decide.ParseThreshold(levelText); err != nil {
		return Step{}, err
	}
	if lengthText == "" {
		return Step{}, errors.New("a step needs a length such as 25m")
	}
	if s.Length, err = decide.ParseDuration(lengthText); err != nil {
		return Step{}, err
	}
	if s.Length <= 0 {
		return Step{}, fmt.Errorf("%q is not above zero: a step that never ends would never hand over", lengthText)
	}
	s.Name = strings.Join(rest, " ")
	s.Note = s.Name
	if s.Name == "" {
		s.Name = LevelWord(s.Level) + " " + DurationWord(s.Length) // nothing to call it by: say what it is
	}
	if noteFlag != nil {
		s.Note = *noteFlag
	}
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
	parts := []string{LevelWord(s.Level), DurationWord(s.Length)}
	defaultName := LevelWord(s.Level) + " " + DurationWord(s.Length)
	note := s.Note
	// The name is the words of the command. A name that starts like a level or a length would be read as one, so it goes in -m instead.
	if s.Name != "" && s.Name != defaultName {
		if first := strings.Fields(s.Name)[0]; decide.IsPercentWord(first) || decide.IsDurationWord(first) {
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
	return strings.Join(parts, " ")
}

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
