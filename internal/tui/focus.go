package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
)

// Control is what the Focus tab does to the worker: everything the command line can. *session.Client satisfies it.
type Control interface {
	Baseline(level int) (session.StatusView, error)
	Focus(level int, duration time.Duration, then *int, note string) (session.StatusView, error)
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
	default:
		return false
	}
	return true
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
func (m *Model) run(needsWorker bool, do func(Control) (session.StatusView, error), done func(session.StatusView) string) bool {
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
	view, err := do(m.cfg.Control)
	if err != nil {
		m.notice = describeFocusError(err)
		return false
	}
	m.status, m.haveStatus = view, true
	m.notice = done(view)
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
	m.run(true, func(c Control) (session.StatusView, error) { return c.Focus(level, length, then, note) },
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
	m.run(true, func(c Control) (session.StatusView, error) { return c.Baseline(level) },
		func(view session.StatusView) string {
			if level == 0 {
				return "baseline cleared"
			}
			text := "baseline set to " + decide.FormatLevel(level)
			if view.Level != level && len(view.Layers) > 0 {
				text += fmt.Sprintf("; the session in force keeps focus at %s until it ends", decide.FormatLevel(view.Level))
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
	m.run(false, func(c Control) (session.StatusView, error) { return c.SetLevel(level) },
		func(session.StatusView) string { return "level changed to " + decide.FormatLevel(level) })
}

// stop ends the session in force (all: and the baseline too).
func (m *Model) stop(all bool) {
	m.run(false, func(c Control) (session.StatusView, error) { return c.Stop(all) },
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
		m.run(false, func(c Control) (session.StatusView, error) { return c.Resume() }, func(session.StatusView) string { return "resumed" })
		return
	}
	m.run(false, func(c Control) (session.StatusView, error) { return c.Pause() }, func(session.StatusView) string { return "paused" })
}

// adjust adds time to the session in force, or takes it away (the worker will not go below zero).
func (m *Model) adjust(delta time.Duration) {
	top, ok := m.topLayer()
	if !ok || !top.Timed {
		m.notice = "nothing to adjust: the session in force has no timer"
		return
	}
	m.run(false, func(c Control) (session.StatusView, error) { return c.Adjust(delta) },
		func(session.StatusView) string {
			sign := "+"
			if delta < 0 {
				sign, delta = "−", -delta
			}
			return fmt.Sprintf("time left %s%s", sign, decide.FormatRemaining(int(delta/time.Second)))
		})
}

// focusView is the Focus tab: what is in force and what lies beneath it, top first, with the time left.
func (m *Model) focusView() string {
	lines := []string{""}
	switch {
	case !m.haveStatus:
		lines = append(lines,
			"  "+bold.Render("shh is not running"),
			"",
			"  so no focus session is active. Press s to start one (a worker is started for you), or b to set a baseline.")
	case len(m.status.Layers) == 0:
		lines = append(lines,
			"  "+bold.Render("Focus is off"),
			"",
			"  Nothing is held back: notifications reach you as usual. Press s to start a session or b to set a standing level.")
	default:
		top := m.status.Layers[len(m.status.Layers)-1]
		headline := "Focus is on at " + decide.FormatLevel(m.status.Level)
		if top.Paused {
			headline += " (paused)"
		}
		lines = append(lines, "  "+bold.Render(headline),
			"  "+dim.Render(fmt.Sprintf("held back %d · let through %d since this session started", top.Blocked, top.Allowed)), "")
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
		lines = append(lines, "", "  "+dim.Render("▶ is what is in force; the rest is what it falls back to as sessions end."))
	}
	return pad(lines, m.bodyHeight(), m.width)
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
