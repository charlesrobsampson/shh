// Package menubar shows shh's state in the macOS menu bar: a status item with the time left and the focus level, and a small menu
// to pause, adjust or stop the session. The item itself is drawn by a JXA helper (statusitem.js, docs/spikes/04-menubar.md); this
// package decides what it says and what its buttons do.
package menubar

import (
	"fmt"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
)

// Menu item ids; the helper reports a click by id.
const (
	IDPause  = "pause"
	IDResume = "resume"
	IDPlus   = "plus"
	IDMinus  = "minus"
	IDStop   = "stop"
	IDOff    = "off"
	IDOpen   = "open"
	// A routine run has its own items: while one runs, the worker refuses to stop the session it has in force by hand.
	IDRoutineNext = "routine_next"
	IDRoutineBack = "routine_back"
	IDRoutineStop = "routine_stop"
)

// View is everything the helper draws: the text next to the icon and the menu under it.
type View struct {
	Title string `json:"title"`
	Items []Item `json:"items"`
}

// Item is one menu line. A line with no id is a heading and is greyed out; Separator draws a rule instead of text.
type Item struct {
	ID        string `json:"id,omitempty"`
	Title     string `json:"title,omitempty"`
	Separator bool   `json:"separator,omitempty"`
}

// lastRoutineFor is how long after a routine ends its summary stays in the menu, for a routine that asks for it there.
const lastRoutineFor = time.Hour

// figureSpace is as wide as a digit, so padding with it keeps the title the same width as the digits change.
const figureSpace = " "

// Build turns the worker's status into what the menu bar shows.
//
// The title is the label, the time left and the effective level: "shh 28m · 65%", "shh 30%" for a level with no timer,
// "shh ⏸ 65%" while paused, and just "shh" when focus is off. Single digit counts are padded with figure spaces so the width does not
// jump while the seconds tick down (docs/spikes/04-menubar.md).
func Build(v session.StatusView, label string) View {
	return View{Title: title(v, label), Items: items(v)}
}

func title(v session.StatusView, label string) string {
	if r := v.Routine; r != nil {
		return routineTitle(v, label, *r)
	}
	if v.Level == 0 || len(v.Layers) == 0 {
		return label
	}
	top := v.Layers[len(v.Layers)-1]
	switch {
	case top.Paused:
		return fmt.Sprintf("%s ⏸ %d%%", label, v.Level)
	case top.Timed:
		left := decide.FormatRemaining(top.RemainingSecs)
		if pad := 3 - len([]rune(left)); pad > 0 {
			left = strings.Repeat(figureSpace, pad) + left
		}
		return fmt.Sprintf("%s %s · %d%%", label, left, v.Level)
	}
	return fmt.Sprintf("%s %d%%", label, v.Level)
}

// routineTitle is the title while a routine runs: "shh pomodoro · work (2/4) · 14m", the time being that left in the step, or
// "shh pomodoro · break (2/4)?" while the run waits for the user to start that step.
func routineTitle(v session.StatusView, label string, r session.RoutineView) string {
	text := fmt.Sprintf("%s %s · %s", label, r.Name, r.Step)
	if r.Waiting {
		return text + " ?"
	}
	if len(v.Layers) == 0 {
		return text
	}
	top := v.Layers[len(v.Layers)-1]
	switch {
	case top.Paused:
		return text + " · ⏸"
	case top.Timed:
		return text + " · " + decide.FormatRemaining(top.RemainingSecs)
	}
	return text
}

func items(v session.StatusView) []Item {
	var out []Item
	var top *session.LayerView
	if n := len(v.Layers); n > 0 {
		top = &v.Layers[n-1]
	}
	// Only what is in force: the top layer and, for an escalation, where it falls back to. Layers it covers are not listed one by
	// one, which keeps the menu short and stops it reading like a log.
	if r := v.Routine; r != nil {
		out = append(out, Item{Title: fmt.Sprintf("Routine %s · step %d of %d", r.Name, r.Position+1, r.Total)})
		if r.Queued != "" {
			out = append(out, Item{Title: "Queued for when it ends: " + r.Queued})
		}
	}
	if l := v.LastRoutine; l != nil && v.Routine == nil && l.Shows("menu") && !v.Now.IsZero() && v.Now.Sub(l.Ended) < lastRoutineFor {
		out = append(out, Item{Title: "Last routine: " + l.Line()}, Item{Separator: true})
	}
	if top == nil {
		out = append(out, Item{Title: "Focus is off"})
	} else {
		out = append(out, Item{Title: describe(*top)})
		if top.Kind != "baseline" {
			out = append(out, Item{Title: fallback(v.Layers)})
		}
	}
	if len(out) > 0 {
		out = append(out, Item{Separator: true})
	}

	if top != nil && top.Timed {
		if top.Paused {
			out = append(out, Item{ID: IDResume, Title: "Resume"})
		} else {
			out = append(out, Item{ID: IDPause, Title: "Pause"})
		}
		out = append(out, Item{ID: IDPlus, Title: "+5m"}, Item{ID: IDMinus, Title: "−5m"})
	}
	if r := v.Routine; r != nil {
		next := "Skip to the next step"
		if r.Waiting {
			next = "Start " + r.Step
		}
		out = append(out, Item{ID: IDRoutineNext, Title: next}, Item{ID: IDRoutineBack, Title: "Back a step"}, Item{ID: IDRoutineStop, Title: "Stop routine " + r.Name}, Item{Separator: true})
		return append(out, Item{ID: IDOpen, Title: "Open shh…"})
	}
	if top != nil && top.Kind != "baseline" {
		out = append(out, Item{ID: IDStop, Title: "Stop this session"})
	}
	if top != nil {
		out = append(out, Item{ID: IDOff, Title: "Turn focus off"}, Item{Separator: true})
	}
	return append(out, Item{ID: IDOpen, Title: "Open shh…"})
}

// describe is the heading for a layer: "baseline 30%", "escalation 75% · 12m left · "deep work"".
func describe(l session.LayerView) string {
	parts := []string{fmt.Sprintf("%s %s", l.Kind, decide.FormatLevel(l.Percent))}
	switch {
	case l.Paused:
		parts = append(parts, "paused, "+decide.FormatRemaining(l.RemainingSecs)+" left")
	case l.Timed:
		parts = append(parts, decide.FormatRemaining(l.RemainingSecs)+" left")
	case l.Kind != "baseline":
		parts = append(parts, "until stopped")
	}
	if l.Note != "" {
		parts = append(parts, fmt.Sprintf("%q", l.Note))
	}
	return strings.Join(parts, " · ")
}

// fallback says what focus goes back to when the top escalation ends: its own "then" level, else the layer beneath, else off.
func fallback(layers []session.LayerView) string {
	top := layers[len(layers)-1]
	switch {
	case top.ThenLevel != nil:
		return "then " + decide.FormatLevel(*top.ThenLevel)
	case len(layers) > 1:
		below := layers[len(layers)-2]
		return fmt.Sprintf("then back to %s %s", below.Kind, decide.FormatLevel(below.Percent))
	}
	return "then off"
}
