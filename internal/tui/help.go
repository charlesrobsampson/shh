package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/charlesrobsampson/shh/internal/guide"
)

var (
	matchStyle   = lipgloss.NewStyle().Reverse(true)
	currentStyle = lipgloss.NewStyle().Reverse(true).Bold(true).Underline(true)
)

// helpLayout is the guide laid out for the screen's width.
func (m *Model) helpLayout() []guide.Line {
	if m.layoutLines == nil || m.layoutWidth != m.width { // laying it out is not free, and it is asked for on every key and every draw
		m.layoutLines, m.layoutWidth = guide.Layout(guide.Sections, m.width), m.width
	}
	return m.layoutLines
}

// helpLines is the guide as plain lines for a width.
func helpLines(width int) []string {
	var out []string
	for _, l := range guide.Layout(guide.Sections, width) {
		out = append(out, l.Pad())
	}
	return out
}

// openGuide shows the help, optionally at a section and with a search already made.
func (m *Model) openGuide(section, search string) {
	m.showHelp, m.helpScroll = true, 0
	m.helpJump, m.helpSearch = section, search
	m.helpQuery, m.helpHits, m.helpAt = guide.Query{}, nil, -1
}

// setHelpQuery searches the guide, and goes to the first match at or below the top of the screen (or the first of all).
func (m *Model) setHelpQuery(text string) {
	m.helpQuery = guide.NewQuery(strings.TrimSpace(text))
	lines := m.helpLayout()
	m.helpHits, m.helpAt = guide.Hits(guide.Sections, lines, m.helpQuery), -1
	if len(m.helpHits) == 0 {
		return
	}
	m.helpAt = 0
	for i, h := range m.helpHits {
		if h >= m.helpScroll {
			m.helpAt = i
			break
		}
	}
	m.helpGoTo(m.helpAt)
}

// helpGoTo scrolls so that match i is near the top, with a line of what comes before it.
func (m *Model) helpGoTo(i int) {
	if len(m.helpHits) == 0 {
		return
	}
	m.helpAt = (i + len(m.helpHits)) % len(m.helpHits)
	m.helpScroll = max(0, m.helpHits[m.helpAt]-1)
}

// closeHelp leaves the help: back to the tabs, or out of shh for a guide opened on its own.
func (m *Model) closeHelp() tea.Cmd {
	if m.cfg.HelpOnly {
		return tea.Quit
	}
	m.showHelp = false
	return nil
}

// helpKey handles a key while the help is showing.
func (m *Model) helpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := max(1, m.height-2)
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		if !m.helpQuery.Empty() {
			m.helpQuery, m.helpHits, m.helpAt = guide.Query{}, nil, -1
			return m, nil
		}
		return m, m.closeHelp()
	case "?":
		return m, m.closeHelp()
	case "/":
		m.openAsk(promptHelpSearch, "")
	case "n":
		m.helpGoTo(m.helpAt + 1)
	case "N":
		m.helpGoTo(m.helpAt - 1)
	case "j", "down":
		m.helpScroll++
	case "k", "up":
		m.helpScroll--
	case "pgdown", "ctrl+d", " ":
		m.helpScroll += page
	case "pgup", "ctrl+u":
		m.helpScroll -= page
	case "g", "home":
		m.helpScroll = 0
	case "G", "end":
		m.helpScroll = 1 << 30
	}
	return m, nil
}

// styledHelpLine draws one line of the guide, with the matches of the search marked.
func (m *Model) styledHelpLine(l guide.Line, currentTopic int, currentLine bool) string {
	mark := func(s string, current bool) string {
		spans := m.helpQuery.Spans(s)
		var b strings.Builder
		for i, part := range spans {
			switch {
			case part == "":
			case i%2 == 1 && current:
				b.WriteString(currentStyle.Render(part))
			case i%2 == 1:
				b.WriteString(matchStyle.Render(part))
			default:
				b.WriteString(part)
			}
		}
		return b.String()
	}
	current := l.Topic >= 0 && l.Topic == currentTopic
	switch l.Kind {
	case guide.LineHeading:
		return " " + bold.Render(l.Text)
	case guide.LineSection:
		return " " + bold.Underline(true).Render(mark(l.Text, currentLine))
	case guide.LineRow:
		keys := fmt.Sprintf("%-*s", guide.KeyWidth, l.Keys)
		if l.Keys != "" {
			keys = mark(l.Keys, current) + strings.Repeat(" ", max(0, guide.KeyWidth-lipgloss.Width(l.Keys)))
		}
		return "  " + keys + " " + mark(l.Text, current)
	}
	return ""
}

func (m *Model) helpView() string {
	lines := m.helpLayout()
	// A guide opened at a section, or with a search, goes there on its first draw: where the lines are depends on the width, which is
	// only known now.
	if m.helpJump != "" {
		if at := guide.SectionStart(lines, m.helpJump); at >= 0 {
			m.helpScroll = max(0, at-1)
		}
		m.helpJump = ""
	}
	if m.helpSearch != "" {
		search := m.helpSearch
		m.helpSearch = ""
		m.setHelpQuery(search)
	}
	room := max(1, m.height-1) // one line is kept for the footer
	m.helpScroll = max(0, min(m.helpScroll, len(lines)-room))
	last := min(len(lines), m.helpScroll+room)
	currentTopic, currentLine := -1, -1
	if m.helpAt >= 0 && m.helpAt < len(m.helpHits) {
		currentLine = m.helpHits[m.helpAt]
		currentTopic = lines[currentLine].Topic
	}
	visible := make([]string, 0, room)
	for i := m.helpScroll; i < last; i++ {
		visible = append(visible, m.styledHelpLine(lines[i], currentTopic, i == currentLine))
	}
	for len(visible) < room {
		visible = append(visible, "")
	}
	return pad(append(visible, m.helpFooter(m.helpScroll+1, last, len(lines))), m.height, m.width)
}

// helpFooter is the line under the guide: how to search, or where the search is.
func (m *Model) helpFooter(first, last, total int) string {
	if m.prompt != nil {
		return m.promptBar()
	}
	closing := "Esc closes"
	if m.cfg.HelpOnly {
		closing = "Esc or q quits"
	}
	where := fmt.Sprintf("lines %d-%d of %d", first, last, total)
	switch {
	case m.helpQuery.Empty():
		return dim.Render(fmt.Sprintf(" / search · j/k scroll · g/G top/bottom · %s · %s", closing, where))
	case len(m.helpHits) == 0:
		return errStyle.Render(fmt.Sprintf(" no match for %q · / searches again · Esc clears", m.helpQuery.Text()))
	}
	return dim.Render(fmt.Sprintf(" /%s · match %d of %d · n/N next and previous · Esc clears · %s", m.helpQuery.Text(), m.helpAt+1, len(m.helpHits), where))
}
