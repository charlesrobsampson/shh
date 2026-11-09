package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
)

// renderStatus prints the stack of focus levels for people.
func renderStatus(w io.Writer, v session.StatusView, running bool) {
	defer func() {
		if v.Routine != nil {
			fmt.Fprintf(w, "Routine: %s\n", describeStep(*v.Routine))
		}
	}()
	if v.Level == 0 {
		suffix := ""
		if !running {
			suffix = " (shh is not running)"
		}
		fmt.Fprintf(w, "Focus is off%s\n", suffix)
		return
	}
	fmt.Fprintf(w, "Focus is on at %s\n", decide.FormatLevel(v.Level))
	for _, l := range v.Layers {
		var detail []string
		switch {
		case l.Paused:
			detail = append(detail, "paused with "+decide.FormatRemaining(l.RemainingSecs)+" left")
		case l.Timed:
			detail = append(detail, decide.FormatRemaining(l.RemainingSecs)+" left")
		case l.Kind != "baseline":
			detail = append(detail, "until stopped")
		}
		if l.ThenLevel != nil {
			detail = append(detail, "then "+decide.FormatLevel(*l.ThenLevel))
		}
		if l.Alert != "" {
			detail = append(detail, "alerts at the end ("+l.Alert+")")
		}
		line := fmt.Sprintf("  %-10s  %-14s", l.Kind, decide.FormatLevel(l.Percent))
		if len(detail) > 0 {
			line += "  " + strings.Join(detail, ", ")
		}
		if l.Note != "" {
			line += fmt.Sprintf("  %q", l.Note)
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}

// shortStatus is the one-line form for a prompt or status bar: "shh 38m · 90%", "shh 30%", "shh ⏸ 90%". Empty when focus is off.
func shortStatus(v session.StatusView, label string) string {
	if v.Level == 0 || len(v.Layers) == 0 {
		return ""
	}
	top := v.Layers[len(v.Layers)-1]
	switch {
	case top.Paused:
		return fmt.Sprintf("%s ⏸ %d%%", label, v.Level)
	case top.Timed:
		return fmt.Sprintf("%s %s · %d%%", label, decide.FormatRemaining(top.RemainingSecs), v.Level)
	}
	return fmt.Sprintf("%s %d%%", label, v.Level)
}

func printJSON(w io.Writer, v session.StatusView, running bool) error {
	out := struct {
		Running bool `json:"running"`
		session.StatusView
	}{running, v}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}
