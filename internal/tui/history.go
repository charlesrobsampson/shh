package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
)

// historyRuns is how many past runs the history reads.
const historyRuns = 100

// historyView is the list of past runs of routines, and the notifications flagged during the selected one.
type historyView struct {
	runs    []session.RunLog
	cursor  int
	flagged []session.FlaggedItem // of the selected run: what is left to review
	err     string
}

// openHistory reads the runs and shows them in place of the routines list.
func (m *Model) openHistory() {
	m.history = &historyView{}
	m.reloadHistory()
}

// reloadHistory reads the runs again, keeping the cursor on the same run.
func (m *Model) reloadHistory() {
	h := m.history
	if h == nil {
		return
	}
	var selected int64
	if h.cursor < len(h.runs) {
		selected = h.runs[h.cursor].ID
	}
	runs, err := session.RunLogs(m.cfg.Data, "", historyRuns, m.cfg.Now())
	if err != nil {
		h.err = "cannot read the runs: " + err.Error()
		return
	}
	h.err, h.runs = "", runs
	for i, r := range runs {
		if r.ID == selected {
			h.cursor = i
		}
	}
	h.cursor = max(0, min(h.cursor, len(runs)-1))
	h.flagged = nil
	if h.cursor < len(runs) {
		if items, err := session.RunFlagged(m.cfg.Data, runs[h.cursor].ID, m.cfg.Now()); err == nil {
			h.flagged = items
		}
	}
}

// historyKey handles every key while the history is open.
func (m *Model) historyKey(key string, steps int) {
	h := m.history
	switch key {
	case "esc", "H", "q":
		m.history = nil
		return
	case "ctrl+c":
		m.history = nil
	case "j", "down":
		h.cursor = min(h.cursor+steps, len(h.runs)-1)
	case "k", "up":
		h.cursor = max(h.cursor-steps, 0)
	case "g", "home":
		h.cursor = 0
	case "G", "end":
		h.cursor = max(0, len(h.runs)-1)
	case "R", "t", "enter":
		m.history = nil
		m.goTo(TabTrain) // flagged notifications lead the Train list
		m.notice = "flagged notifications are at the top of the Train tab"
		return
	}
	h.cursor = max(0, min(h.cursor, len(h.runs)-1))
	m.reloadHistory()
}

// historyView draws the runs, newest first, and under them what is flagged and still to review for the selected one.
func (m *Model) historyView() string {
	h := m.history
	lines := []string{"", "  " + bold.Render("Runs of routines") + dim.Render("  (newest first)"), ""}
	if h.err != "" {
		lines = append(lines, "  "+errStyle.Render("✗ "+oneLine(h.err)))
	}
	if len(h.runs) == 0 && h.err == "" {
		lines = append(lines, "  "+dim.Render("no routine has run yet"))
	}
	detail := m.historyDetail()
	room := max(1, m.bodyHeight()-len(lines)-len(detail)-1)
	first := 0
	if h.cursor >= room {
		first = h.cursor - room + 1
	}
	for i := first; i < min(len(h.runs), first+room); i++ {
		lines = append(lines, m.historyRow(h.runs[i], i == h.cursor))
	}
	if len(detail) > 0 {
		lines = append(lines, "")
		lines = append(lines, detail...)
	}
	return pad(lines, m.bodyHeight(), m.width)
}

// historyRow is one run: when, which routine, how it ended, how long, what it held back and what is flagged.
func (m *Model) historyRow(l session.RunLog, selected bool) string {
	state := l.Reason
	switch {
	case l.Ended == nil:
		state = "running"
	case l.Reason == "until":
		state = "cut off"
	case l.Reason == "missing":
		state = "ended"
	}
	text := fmt.Sprintf(" %s %-10s %-14s %-9s %-7s held %d, let %d", map[bool]string{true: ">", false: " "}[selected], l.Started.In(m.cfg.Now().Location()).Format("Mon 15:04"),
		l.Routine, state, decide.FormatRemaining(int(l.Elapsed/time.Second)), l.Held, l.Let)
	switch {
	case l.Flagged > 0 && l.ToReview == 0:
		text += fmt.Sprintf("  flagged %d (reviewed)", l.Flagged)
	case l.Flagged > 0:
		text += fmt.Sprintf("  ⚑ %d to review", l.ToReview)
	}
	style := lipgloss.NewStyle().Reverse(selected)
	row := style.Render(text)
	if gap := m.width - lipgloss.Width(row); selected && gap > 0 {
		row += style.Render(strings.Repeat(" ", gap))
	}
	return row
}

// historyDetail says what the selected run did and lists the notifications flagged during it that wait for review.
func (m *Model) historyDetail() []string {
	h := m.history
	if h.cursor >= len(h.runs) {
		return nil
	}
	l := h.runs[h.cursor]
	out := []string{"  " + bold.Render(fmt.Sprintf("Run %d: %s", l.ID, l.Routine)) + dim.Render(fmt.Sprintf("  %d of %d steps in %s", l.Steps, l.Total, decide.FormatRemaining(int(l.Elapsed/time.Second))))}
	switch {
	case len(h.flagged) > 0:
		out = append(out, "  "+dim.Render(fmt.Sprintf("flagged during it and still to review (%d); R opens the Train tab, where they come first:", len(h.flagged))))
		for i, it := range h.flagged {
			if i == 3 {
				out = append(out, "  "+dim.Render(fmt.Sprintf("    … and %d more", len(h.flagged)-3)))
				break
			}
			out = append(out, "    "+oneLine(it.At.In(m.cfg.Now().Location()).Format("15:04")+"  "+flaggedBrief(it)))
		}
	case l.Flagged > 0:
		out = append(out, "  "+dim.Render(fmt.Sprintf("%d flagged during it, all reviewed since", l.Flagged)))
	default:
		out = append(out, "  "+dim.Render("nothing was flagged during it"))
	}
	return out
}

// flaggedBrief is a flagged notification in a few words: "Teams · Ana · the subject".
func flaggedBrief(it session.FlaggedItem) string {
	parts := []string{it.Source}
	if it.Sender != "" {
		parts = append(parts, it.Sender)
	}
	if it.Title != "" && it.Title != it.Sender {
		parts = append(parts, it.Title)
	}
	return strings.Join(parts, " · ")
}
