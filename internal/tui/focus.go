package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
)

// Control is what the Focus tab does to the worker: everything the command line can. *session.Client satisfies it.
type Control interface {
	Baseline(level int) (session.StatusView, error)
	Focus(level int, duration time.Duration, then *int, note string) (session.StatusView, error)
	FocusAfterRoutine(level int, duration time.Duration, then *int, note, alert string) (session.StatusView, error)
	SetLevel(level int) (session.StatusView, error)
	Stop(all bool) (session.StatusView, error)
	Pause() (session.StatusView, error)
	Resume() (session.StatusView, error)
	Adjust(delta time.Duration) (session.StatusView, error)
}

// adjustStep is how much + and - change the time left, as in the menu bar.
const adjustStep = 5 * time.Minute

// focusKey handles the keys that mean something only on the Focus tab. steps is the number typed before the key, at least 1. It
// reports whether the key was one of them.
func (m *Model) focusKey(key string, steps int) bool {
	switch key {
	case "s":
		m.openAsk(promptStart, "")
	case "b":
		m.openAsk(promptBaseline, "")
	case "f":
		if !m.inForce() {
			m.notice = "no focus session to change the level of: press s to start one or b to set a baseline"
			return true
		}
		m.openAsk(promptLevel, "")
	case "p", " ":
		m.togglePause()
	case "+", "=":
		m.adjust(time.Duration(steps) * adjustStep)
	case "-", "_":
		m.adjust(-time.Duration(steps) * adjustStep)
	case "x":
		m.stop(false)
	case "X":
		m.openAsk(promptConfirmStopAll, "")
	case "enter":
		m.runFocusItem(steps)
	default:
		return false
	}
	return true
}

// focusItem is one row of the Focus tab's menu.
type focusItem struct {
	key   string // the shortcut shown beside it
	label string
	desc  string
	// cmd is the shh command that does the same from a terminal, as an example; empty when there is none (the TUI and menu bar only).
	cmd string
	// why is empty when the row can be used now, and otherwise says why not.
	why string
	run func(steps int)
}

// focusItems is the menu, with each row's state worked out from what the worker last reported.
func (m *Model) focusItems() []focusItem {
	top, haveTop := m.topLayer()
	timed := haveTop && top.Timed
	notRunning := ""
	if !m.haveStatus {
		notRunning = "shh is not running"
	}
	needsSession := notRunning
	if needsSession == "" && !haveTop {
		needsSession = "focus is off"
	}
	needsTimer := needsSession
	if needsTimer == "" && !timed {
		needsTimer = "the session in force has no timer"
	}
	hasEscalation := false
	for _, l := range m.status.Layers {
		if l.Kind != "baseline" {
			hasEscalation = true
		}
	}
	needsEscalation := notRunning
	if needsEscalation == "" && !hasEscalation {
		needsEscalation = "no session is running (X turns off the baseline)"
	}
	pauseLabel := "Pause"
	if haveTop && top.Paused {
		pauseLabel = "Resume"
	}
	pauseCmd := "shh pause"
	if haveTop && top.Paused {
		pauseCmd = "shh resume"
	}
	return []focusItem{
		{"s", "Start a session…", "a level, a length and a note: 75% 25m writing docs", "shh 75% 25m writing docs", "", func(int) { m.openAsk(promptStart, "") }},
		{"b", "Set the baseline…", "the standing level when no session is running; off clears it", "shh baseline 30%", "", func(int) { m.openAsk(promptBaseline, "") }},
		{"f", "Change the level…", "of the session in force, without starting another", "", needsSession, func(int) { m.openAsk(promptLevel, "") }},
		{"p", pauseLabel, "freeze or restart the countdown of a timed session", pauseCmd, needsTimer, func(int) { m.togglePause() }},
		{"+", "Add 5 minutes", "to the time left (3 then Enter adds 15)", "", needsTimer, func(n int) { m.adjust(time.Duration(n) * adjustStep) }},
		{"-", "Take away 5 minutes", "from the time left", "", needsTimer, func(n int) { m.adjust(-time.Duration(n) * adjustStep) }},
		{"x", "Stop this session", "focus falls back to what is under it", "shh stop", needsEscalation, func(int) { m.stop(false) }},
		{"X", "Turn focus off…", "everything, baseline included (asks first)", "shh stop --all", needsSession, func(int) { m.openAsk(promptConfirmStopAll, "") }},
	}
}

// runFocusItem does the selected menu row, or says why it cannot be done now. steps is a number typed first, for the rows that repeat.
func (m *Model) runFocusItem(steps int) {
	items := m.focusItems()
	if m.focusCursor >= len(items) {
		return
	}
	item := items[m.focusCursor]
	if item.why != "" {
		m.notice = fmt.Sprintf("%s: %s", strings.TrimSuffix(item.label, "…"), item.why)
		return
	}
	item.run(steps)
}

// inForce says whether the worker reported a level in force at its last answer.
func (m *Model) inForce() bool { return m.haveStatus && len(m.status.Layers) > 0 }

// topLayer is the layer in force, the last one the worker lists.
func (m *Model) topLayer() (session.LayerView, bool) {
	if !m.inForce() {
		return session.LayerView{}, false
	}
	return m.status.Layers[len(m.status.Layers)-1], true
}

// run sends one command to the worker and takes its answer as the new status, so the page is right at once and not a second later.
// Starting something needs a worker, so one is started if none is running; the other commands only make sense for one that is.
func (m *Model) run(needsWorker bool, cmd string, do func(Control) (session.StatusView, error), done func(session.StatusView) string) bool {
	if m.cfg.Control == nil {
		m.notice = "focus control is not available here"
		return false
	}
	if needsWorker {
		if err := m.ensureWorker(); err != nil {
			m.notice = "cannot reach the worker: " + err.Error()
			return false
		}
	} else if !m.haveStatus {
		m.notice = "shh is not running, so there is no focus session to change"
		return false
	}
	cont := m.continueWith
	m.continueWith = nil
	view, err := do(m.cfg.Control)
	if errors.Is(err, session.ErrRoutineRunning) && m.cfg.Routines != nil {
		// A routine is in charge of focus: ask before ending it, and do what was asked if the answer is yes. A new session may also go
		// on top of it, and the routine carries on afterwards.
		var continuing func()
		if cont != nil {
			continuing = func() { m.run(needsWorker, cmd+" --continue-routine", cont, done) }
		}
		m.askToEndRoutine(func() { m.run(needsWorker, cmd, do, done) }, continuing)
		return false
	}
	if err != nil {
		m.notice = describeFocusError(err)
		return false
	}
	m.status, m.haveStatus = view, true
	m.notice, m.noticeCmd, m.noticeHasCmd = done(view), cmd, true // the command goes at the right of the message: the same thing, typed in a terminal
	return true
}

// describeFocusError says in words what a refused command means.
func describeFocusError(err error) string {
	switch {
	case errors.Is(err, session.ErrNothingToStop):
		return "no session to stop (X turns everything off, baseline included)"
	case errors.Is(err, session.ErrNoTimer):
		return "the session in force has no timer to pause or adjust"
	case errors.Is(err, session.ErrNotPaused):
		return "it is not paused"
	case errors.Is(err, session.ErrNoSession):
		return "no focus session to change"
	case errors.Is(err, session.ErrNotRunning):
		return "shh is not running"
	case errors.Is(err, session.ErrRoutineRunning):
		return "a routine is running: end it on the Routines tab ($) first"
	}
	return err.Error()
}

// startFocus begins a session on top of whatever is in force, from the line typed in the start prompt.
func (m *Model) startFocus(p *prompt) {
	level, length, then, note, err := parseStart(p.text)
	if err != nil {
		p.err = err.Error()
		return
	}
	m.prompt = nil
	m.continueWith = func(c Control) (session.StatusView, error) { return c.FocusAfterRoutine(level, length, then, note, "") }
	m.run(true, startCommand(level, length, then, note), func(c Control) (session.StatusView, error) { return c.Focus(level, length, then, note) },
		func(session.StatusView) string {
			text := "focus started at " + decide.FormatLevel(level)
			if length > 0 {
				text += " for " + decide.FormatRemaining(int(length/time.Second))
			}
			return text
		})
}

// parseStart reads the start prompt: a level, a length and a note in the shorthand `shh focus` takes (75% 25m writing docs), with
// "then <level>" at the end to say what the session falls back to. Nothing typed means medium until stopped.
func parseStart(text string) (level int, length time.Duration, then *int, note string, err error) {
	words := strings.Fields(text)
	if n := len(words); n >= 2 && words[n-2] == "then" {
		v, perr := decide.ParseThreshold(words[n-1])
		if perr != nil {
			return 0, 0, nil, "", fmt.Errorf("then: %w", shortError(perr))
		}
		then, words = &v, words[:n-2]
	}
	levelText, lengthText, rest, serr := decide.SplitFocusWords("", "", words)
	if serr != nil {
		return 0, 0, nil, "", serr
	}
	if levelText == "" {
		levelText = "medium"
	}
	if lengthText == "" {
		lengthText = "0"
	}
	if level, err = decide.ParseThreshold(levelText); err != nil {
		return 0, 0, nil, "", shortError(err)
	}
	if length, err = decide.ParseDuration(lengthText); err != nil {
		return 0, 0, nil, "", shortError(err)
	}
	return level, length, then, strings.Join(rest, " "), nil
}

// shortError trims a parser's explanation to fit under the prompt.
func shortError(err error) error {
	msg := err.Error()
	if len(msg) > 90 {
		msg = msg[:89] + "…"
	}
	return errors.New(msg)
}

// setBaseline sets or clears the standing level ("off" clears it).
func (m *Model) setBaseline(p *prompt) {
	level, err := decide.ParseThreshold(strings.TrimSpace(p.text))
	if err != nil {
		p.err = shortError(err).Error()
		return
	}
	m.prompt = nil
	m.run(true, "shh baseline "+levelWord(level), func(c Control) (session.StatusView, error) { return c.Baseline(level) },
		func(view session.StatusView) string {
			if level == 0 {
				return "baseline cleared"
			}
			text := "baseline set to " + decide.FormatLevel(level)
			if view.Level != level && len(view.Layers) > 0 {
				text += fmt.Sprintf("; the %d%% session in force keeps focus until it ends", view.Level)
			}
			return text
		})
}

// changeLevel gives the layer in force a new level without starting a new session.
func (m *Model) changeLevel(p *prompt) {
	level, err := decide.ParseThreshold(strings.TrimSpace(p.text))
	if err != nil {
		p.err = shortError(err).Error()
		return
	}
	m.prompt = nil
	m.run(false, "", func(c Control) (session.StatusView, error) { return c.SetLevel(level) },
		func(session.StatusView) string { return "level changed to " + decide.FormatLevel(level) })
}

// stop ends the session in force (all: and the baseline too).
func (m *Model) stop(all bool) {
	stopCmd := "shh stop"
	if all {
		stopCmd += " --all"
	}
	m.run(false, stopCmd, func(c Control) (session.StatusView, error) { return c.Stop(all) },
		func(view session.StatusView) string {
			switch {
			case all:
				return "focus is off"
			case view.Level == 0:
				return "session stopped: focus is off"
			}
			return "session stopped: back to " + decide.FormatLevel(view.Level)
		})
}

// togglePause freezes the countdown of a running timed session, or starts a frozen one again.
func (m *Model) togglePause() {
	top, ok := m.topLayer()
	if !ok || !top.Timed {
		m.notice = "nothing to pause: the session in force has no timer"
		return
	}
	if top.Paused {
		m.run(false, "shh resume", func(c Control) (session.StatusView, error) { return c.Resume() }, func(session.StatusView) string { return "resumed" })
		return
	}
	m.run(false, "shh pause", func(c Control) (session.StatusView, error) { return c.Pause() }, func(session.StatusView) string { return "paused" })
}

// adjust adds time to the session in force, or takes it away (the worker will not go below zero).
func (m *Model) adjust(delta time.Duration) {
	top, ok := m.topLayer()
	if !ok || !top.Timed {
		m.notice = "nothing to adjust: the session in force has no timer"
		return
	}
	m.run(false, "", func(c Control) (session.StatusView, error) { return c.Adjust(delta) },
		func(session.StatusView) string {
			sign := "+"
			if delta < 0 {
				sign, delta = "−", -delta
			}
			return fmt.Sprintf("time left %s%s", sign, decide.FormatRemaining(int(delta/time.Second)))
		})
}

// focusView is the Focus tab: what is in force and what lies beneath it, top first, with the time left, and under it the menu of
// everything that can be done.
func (m *Model) focusView() string {
	lines := []string{""}
	switch {
	case !m.haveStatus:
		lines = append(lines, "  "+bold.Render("shh is not running")+dim.Render(" · so no focus session is active"))
	case len(m.status.Layers) == 0:
		lines = append(lines, "  "+bold.Render("Focus is off")+dim.Render(" · nothing is held back; notifications reach you as usual"))
	default:
		top := m.status.Layers[len(m.status.Layers)-1]
		headline := "Focus is on at " + decide.FormatLevel(m.status.Level)
		if top.Paused {
			headline += " (paused)"
		}
		lines = append(lines, "  "+bold.Render(headline)+dim.Render(fmt.Sprintf(" · held back %d, let through %d since this session started", top.Blocked, top.Allowed)), "")
		for i := len(m.status.Layers) - 1; i >= 0; i-- {
			l := m.status.Layers[i]
			pointer, style := "  ", dim
			if i == len(m.status.Layers)-1 {
				pointer, style = "▶ ", bold
			}
			lines = append(lines, "  "+pointer+style.Render(layerRow(l, m.status.Layers[:i])))
			if l.Note != "" {
				lines = append(lines, "      "+dim.Render(fmt.Sprintf("%q", oneLine(l.Note))))
			}
		}
	}
	if m.haveStatus && m.status.Routine != nil {
		lines = append(lines, "", "  "+bold.Render("Routine ")+routineLine(*m.status.Routine)+dim.Render("  (the $ tab)"))
	}
	lines = append(lines, "", "  "+bold.Underline(true).Render("What to do")+dim.Render("  (j/k to select, Enter to do it, or press the key)"))

	// The menu gets whatever room is left; on a short terminal it scrolls to keep the selected row in view.
	items := m.focusItems()
	room := max(1, m.bodyHeight()-len(lines))
	first := 0
	if m.focusCursor >= room {
		first = m.focusCursor - room + 1
	}
	for i := first; i < min(len(items), first+room); i++ {
		lines = append(lines, m.menuRow(items[i], i == m.focusCursor))
	}
	return pad(lines, m.bodyHeight(), m.width)
}

// menuRow is one menu row: the cursor, its key, what it does and what it means. A row that cannot be used now is faint and says why
// where its explanation would be. The selected row is drawn in reverse video, piece by piece so no style is nested in another.
func (m *Model) menuRow(it focusItem, selected bool) string {
	cell := func(style lipgloss.Style, text string) string { return style.Reverse(selected).Render(text) }
	cursor := " "
	if selected {
		cursor = ">"
	}
	label, desc, keyStyle := bold, dim, bold
	text := it.desc
	if it.why != "" {
		label, keyStyle, desc = dim, dim, dim
		text = "not now: " + it.why
	}
	if selected {
		desc = lipgloss.NewStyle()
	}
	row := cell(bold, " "+cursor+" ") + cell(keyStyle, fmt.Sprintf("%-3s", it.key)) + cell(label, fmt.Sprintf("%-22s", it.label)) + cell(desc, " "+text)
	if gap := m.width - lipgloss.Width(row); selected && gap > 0 {
		row += cell(lipgloss.NewStyle(), strings.Repeat(" ", gap)) // the highlight runs the width of the screen, as in the lists
	}
	return row
}

// layerRow is one layer of the stack on one line: kind, level, and its time left or that it has none; below is the layers under it.
func layerRow(l session.LayerView, below []session.LayerView) string {
	text := fmt.Sprintf("%-10s %-14s", l.Kind, decide.FormatLevel(l.Percent))
	switch {
	case l.Paused:
		text += fmt.Sprintf(" paused with %s left", decide.FormatRemaining(l.RemainingSecs))
	case l.Timed:
		text += fmt.Sprintf(" %s left", decide.FormatRemaining(l.RemainingSecs))
	case l.Kind != "baseline":
		text += " until stopped"
	}
	if l.Kind != "baseline" {
		switch {
		case l.ThenLevel != nil:
			text += ", then " + decide.FormatLevel(*l.ThenLevel)
		case len(below) > 0:
			under := below[len(below)-1]
			text += fmt.Sprintf(", then back to %s %s", under.Kind, decide.FormatLevel(under.Percent))
		default:
			text += ", then off"
		}
	}
	return text
}

// startCommand is the shh command that starts the same session: the shorthand (shh 75% 25m writing docs) where it applies, and the
// full command (shh focus ... --then off) where the shorthand has no place for something, such as a level of off or a then level.
func startCommand(level int, length time.Duration, then *int, note string) string {
	parts := []string{"shh"}
	if then != nil || level == 0 {
		parts = append(parts, "focus")
	}
	parts = append(parts, levelWord(level))
	if length > 0 {
		parts = append(parts, durationWord(length))
	}
	if then != nil {
		parts = append(parts, "--then", levelWord(*then))
	}
	if note != "" {
		// The level always comes first, so a tier word after it is just a word of the note; a percentage or a length is not (it would be read
		// as a second setting), so a note starting with one is given with -m.
		if words := strings.Fields(note); decide.IsPercentWord(words[0]) || decide.IsDurationWord(words[0]) {
			parts = append(parts, "-m", fmt.Sprintf("%q", note))
		} else {
			parts = append(parts, note)
		}
	}
	return strings.Join(parts, " ")
}

// levelWord writes a level the way it is typed: 75%, or off for none.
func levelWord(level int) string {
	if level == 0 {
		return "off"
	}
	return fmt.Sprintf("%d%%", level)
}

// durationWord writes a length the way it is typed: 25m, 1h30m, 90s.
func durationWord(d time.Duration) string {
	var out string
	if h := d / time.Hour; h > 0 {
		out += fmt.Sprintf("%dh", h)
	}
	if m := d % time.Hour / time.Minute; m > 0 {
		out += fmt.Sprintf("%dm", m)
	}
	if sec := d % time.Minute / time.Second; sec > 0 {
		out += fmt.Sprintf("%ds", sec)
	}
	return out
}

// commandText is how the equivalent command is shown at the right of the message line: "$ shh pause", or, where the command line has
// nothing like it, a note saying so.
func commandText(cmd string) string {
	if cmd == "" {
		return "no shh command · TUI only"
	}
	return "$ " + cmd
}
