// Package menubar shows shh's state in the macOS menu bar: a status item with the time left and the focus level, and a small menu
// to pause, adjust or stop the session. The item itself is drawn by a JXA helper (statusitem.js, docs/spikes/04-menubar.md); this
// package decides what it says and what its buttons do.
package menubar

import (
	"fmt"
	"strings"

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

func items(v session.StatusView) []Item {
	var out []Item
	if v.Level == 0 || len(v.Layers) == 0 {
		out = append(out, Item{Title: "Focus is off"})
	}
	for _, l := range v.Layers {
		out = append(out, Item{Title: describe(l)})
	}
	if len(out) > 0 {
		out = append(out, Item{Separator: true})
	}

	var top *session.LayerView
	hasEscalation := false
	if n := len(v.Layers); n > 0 {
		top = &v.Layers[n-1]
		hasEscalation = top.Kind != "baseline"
	}
	if top != nil && top.Timed {
		if top.Paused {
			out = append(out, Item{ID: IDResume, Title: "Resume"})
		} else {
			out = append(out, Item{ID: IDPause, Title: "Pause"})
		}
		out = append(out, Item{ID: IDPlus, Title: "+5m"}, Item{ID: IDMinus, Title: "−5m"})
	}
	if hasEscalation {
		out = append(out, Item{ID: IDStop, Title: "Stop this session"})
	}
	if top != nil {
		out = append(out, Item{ID: IDOff, Title: "Turn focus off"}, Item{Separator: true})
	}
	return append(out, Item{ID: IDOpen, Title: "Open shh…"})
}

// describe is one heading per layer: "baseline 30%", "escalation 75% · 12m left · then 30%".
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
	if l.ThenLevel != nil {
		parts = append(parts, "then "+decide.FormatLevel(*l.ThenLevel))
	}
	if l.Note != "" {
		parts = append(parts, fmt.Sprintf("%q", l.Note))
	}
	return strings.Join(parts, " · ")
}
