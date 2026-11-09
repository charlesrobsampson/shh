package tui

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/charlesrobsampson/shh/internal/menubar"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

// Data is the part of the store the lists read and the actions change.
type Data interface {
	ListNotifications(store.Filter) ([]store.Notification, error)
	SetRead(id int64, read bool) error
	SetDismissed(id int64, dismissed bool) error
}

// Worker is the part of the worker's client the status bar reads. An error (the usual one being session.ErrNotRunning) just
// means there is no session information to show: the UI is useful for review and training without a worker.
type Worker interface {
	Status() (session.StatusView, error)
}

// Config is everything the UI needs from the outside world.
type Config struct {
	Data   Data
	Worker Worker
	// Label starts the session text in the status bar, for example "shh".
	Label string
	// Open opens the app with the given bundle id, for Enter and o; `open -b` when nil.
	Open func(bundleID string) error
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Refresh is how often the lists and status are re-read; one second when zero.
	Refresh time.Duration
}

// Tab is one of the three screens.
type Tab int

const (
	TabFocus Tab = iota
	TabNotifications
	TabTrain
	tabCount
)

func (t Tab) String() string {
	return [...]string{"Focus", "Notifications", "Train"}[t]
}

// maxRows bounds how many rows a list loads: the newest ones, which are the ones anyone looks at.
const maxRows = 500

// tabKeys are the keys that jump straight to a tab: shifted digits, which leaves the digits themselves free for training labels
// (F1 to F3 work too).
var tabKeys = [tabCount]string{"!", "@", "#"}

// Model is the terminal UI as a Bubble Tea model.
type Model struct {
	cfg           Config
	tab           Tab
	width, height int
	showHelp      bool
	err           string // a problem to show in the status bar, such as the database failing to read

	lists  [tabCount][]store.Notification // the rows of the Notifications and Train tabs
	cursor [tabCount]int                  // the selected row in each list
	offset [tabCount]int                  // the first row shown in each list

	status     session.StatusView
	haveStatus bool // whether the worker answered the last time it was asked

	// expandedID is the notification shown in full across the whole screen (e); 0 when none is. scroll is how far down its text is.
	expandedID int64
	scroll     int
	// notice is what the last action did or why it could not, shown in the status bar until the next key.
	notice string
}

type tickMsg time.Time

// New builds the UI and reads its data once, so the first frame is not empty.
func New(cfg Config) *Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Open == nil {
		cfg.Open = openApp
	}
	if cfg.Refresh == 0 {
		cfg.Refresh = time.Second
	}
	if cfg.Label == "" {
		cfg.Label = "shh"
	}
	m := &Model{cfg: cfg, tab: TabNotifications, width: 100, height: 30}
	m.reload()
	return m
}

func (m *Model) Init() tea.Cmd { return m.tick() }

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.cfg.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// reload re-reads both lists and the session status, keeping each cursor on the same notification when it is still there.
func (m *Model) reload() {
	m.err = ""
	for _, tab := range []Tab{TabNotifications, TabTrain} {
		var selected int64
		if rows := m.lists[tab]; m.cursor[tab] < len(rows) {
			selected = rows[m.cursor[tab]].ID
		}
		view := store.ViewInbox
		if tab == TabTrain {
			view = store.ViewTrain
		}
		rows, err := m.cfg.Data.ListNotifications(store.Filter{View: view, Limit: maxRows})
		if err != nil {
			m.err = "cannot read notifications: " + err.Error()
			continue
		}
		m.lists[tab] = rows
		m.cursor[tab] = indexOf(rows, selected, m.cursor[tab])
		m.clamp(tab)
	}
	if m.cfg.Worker != nil {
		st, err := m.cfg.Worker.Status()
		m.status, m.haveStatus = st, err == nil
	}
}

// indexOf finds id in rows, falling back to the old position (kept in range) so the cursor stays put when a row has gone.
func indexOf(rows []store.Notification, id int64, fallback int) int {
	for i, r := range rows {
		if r.ID == id {
			return i
		}
	}
	return fallback
}

func (m *Model) clamp(tab Tab) {
	rows := len(m.lists[tab])
	m.cursor[tab] = max(0, min(m.cursor[tab], rows-1))
	visible := m.listHeight()
	switch {
	case m.cursor[tab] < m.offset[tab]:
		m.offset[tab] = m.cursor[tab]
	case m.cursor[tab] >= m.offset[tab]+visible:
		m.offset[tab] = m.cursor[tab] - visible + 1
	}
	m.offset[tab] = max(0, min(m.offset[tab], max(0, rows-visible)))
}

// Tab is the tab being shown.
func (m *Model) Tab() Tab { return m.tab }

// Selected is the notification under the cursor on the current tab, if any.
func (m *Model) Selected() (store.Notification, bool) {
	if m.tab == TabFocus || m.cursor[m.tab] >= len(m.lists[m.tab]) {
		return store.Notification{}, false
	}
	return m.lists[m.tab][m.cursor[m.tab]], true
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for _, tab := range []Tab{TabNotifications, TabTrain} {
			m.clamp(tab)
		}
	case tickMsg:
		m.reload()
		return m, m.tick()
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.notice = ""
	if m.showHelp {
		// Any key closes the help, except the ones that quit.
		if k := msg.String(); k == "ctrl+c" || k == "q" {
			return m, tea.Quit
		}
		m.showHelp = false
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.showHelp = true
	case "tab", "l", "right":
		m.goTo((m.tab + 1) % tabCount)
	case "shift+tab", "h", "left":
		m.goTo((m.tab + tabCount - 1) % tabCount)
	case "f1", "!":
		m.goTo(TabFocus)
	case "f2", "@":
		m.goTo(TabNotifications)
	case "f3", "#":
		m.goTo(TabTrain)
	case "e":
		m.toggleExpand()
	case "esc":
		m.collapse()
	case "enter", "o":
		m.open()
	case "d", "x":
		m.dismiss()
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.move(-len(m.lists[m.tab]))
	case "G", "end":
		m.move(len(m.lists[m.tab]))
	case "pgdown", "ctrl+d":
		m.move(m.listHeight())
	case "pgup", "ctrl+u":
		m.move(-m.listHeight())
	}
	return m, nil
}

// goTo switches tab, closing any expanded notification.
func (m *Model) goTo(tab Tab) {
	m.tab = tab
	m.collapse()
}

func (m *Model) collapse() { m.expandedID, m.scroll = 0, 0 }

// expanded says whether the selected notification is the one shown in full.
func (m *Model) expanded() bool {
	n, ok := m.Selected()
	return ok && m.expandedID != 0 && n.ID == m.expandedID
}

// toggleExpand shows the selected notification across the whole screen, or puts it back. Reading a notification here does not mark
// it read: only opening it in its own app does (plan.md section 8).
func (m *Model) toggleExpand() {
	n, ok := m.Selected()
	switch {
	case !ok:
	case m.expanded():
		m.collapse()
	default:
		m.expandedID, m.scroll = n.ID, 0
	}
}

// open opens the selected notification's app and, only if that worked, marks it read.
func (m *Model) open() {
	n, ok := m.Selected()
	switch {
	case !ok:
		return
	case n.BundleID == "":
		m.notice = "nothing to open: this notification does not say which app it came from"
		return
	}
	if err := m.cfg.Open(n.BundleID); err != nil {
		m.notice = fmt.Sprintf("could not open %s: %v", oneLine(n.Source), err)
		return
	}
	if err := m.cfg.Data.SetRead(n.ID, true); err != nil {
		m.notice = "opened, but could not mark it read: " + err.Error()
		return
	}
	m.notice = "opened " + oneLine(n.Source)
	m.reload()
}

// dismiss takes the selected notification off the Notifications list. It stays on the Train list: dismissing is not training.
func (m *Model) dismiss() {
	n, ok := m.Selected()
	if !ok {
		return
	}
	if err := m.cfg.Data.SetDismissed(n.ID, true); err != nil {
		m.notice = "could not dismiss: " + err.Error()
		return
	}
	m.notice = "dismissed"
	m.reload()
}

// openApp opens an app by bundle id, the same way the alert banners do.
func openApp(bundleID string) error {
	return exec.Command("open", "-b", bundleID).Run()
}

// move changes the selected row by delta, staying inside the list.
func (m *Model) move(delta int) {
	if m.tab == TabFocus {
		return
	}
	if m.expanded() {
		m.scrollBy(delta)
		return
	}
	m.cursor[m.tab] += delta
	m.clamp(m.tab)
}

// scrollBy moves the text of the expanded notification up or down, staying inside it.
func (m *Model) scrollBy(delta int) {
	n, ok := m.Selected()
	if !ok {
		return
	}
	meta, body := m.detailParts(n, m.width)
	room := m.bodyHeight() - len(meta) - 1 // one line is kept for the footer
	m.scroll = max(0, min(m.scroll+delta, len(body)-room))
}

// ---- layout ----

const (
	tabBarHeight    = 1
	statusBarHeight = 1
)

// bodyHeight is the room between the tab bar and the status bar.
func (m *Model) bodyHeight() int { return max(3, m.height-tabBarHeight-statusBarHeight) }

// listHeight is how many rows the list shows: the body without the header line of the list.
func (m *Model) listHeight() int { return max(1, m.bodyHeight()-1) }

func (m *Model) listWidth() int { return max(30, m.width*45/100) }

var (
	tabStyle      = lipgloss.NewStyle().Padding(0, 1)
	activeTab     = tabStyle.Bold(true).Reverse(true)
	dim           = lipgloss.NewStyle().Faint(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
	statusStyle   = lipgloss.NewStyle().Reverse(true)
	errStyle      = lipgloss.NewStyle().Bold(true)
)

func (m *Model) View() string {
	if m.showHelp {
		return m.helpView()
	}
	body := m.focusView()
	switch {
	case m.tab == TabFocus:
	case m.expanded():
		body = m.detailView()
	default:
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.listView(), m.separator(), m.detailView())
	}
	return strings.Join([]string{m.tabBar(), body, m.statusBar()}, "\n")
}

func (m *Model) tabBar() string {
	var parts []string
	for t := Tab(0); t < tabCount; t++ {
		label := fmt.Sprintf("%s %s", tabKeys[t], t)
		if t == TabNotifications && len(m.lists[t]) > 0 {
			label += fmt.Sprintf(" (%d)", len(m.lists[t]))
		}
		if t == TabTrain && len(m.lists[t]) > 0 {
			label += fmt.Sprintf(" (%d)", len(m.lists[t]))
		}
		if t == m.tab {
			parts = append(parts, activeTab.Render(label))
		} else {
			parts = append(parts, tabStyle.Render(label))
		}
	}
	return fit(strings.Join(parts, ""), m.width)
}

func (m *Model) separator() string {
	return dim.Render(strings.TrimRight(strings.Repeat("│\n", m.bodyHeight()), "\n"))
}

// focusView is the placeholder for the Focus tab, which task 34 builds.
func (m *Model) focusView() string {
	lines := []string{"", "  Focus", ""}
	switch {
	case !m.haveStatus:
		lines = append(lines, "  shh is not running, so no focus session is active.")
	case m.status.Level == 0:
		lines = append(lines, "  Focus is off.")
	default:
		for _, l := range m.status.Layers {
			lines = append(lines, "  "+layerLine(l))
		}
	}
	lines = append(lines, "", dim.Render("  Starting, stopping and adjusting sessions from here is coming; use the shh command for now."))
	return pad(lines, m.bodyHeight(), m.width)
}

func layerLine(l session.LayerView) string {
	view := menubar.Build(session.StatusView{Level: l.Percent, Layers: []session.LayerView{l}}, "")
	return view.Items[0].Title
}

// listView is the left column: a header and the rows of the current tab.
func (m *Model) listView() string {
	w := m.listWidth()
	rows := m.lists[m.tab]
	header := dim.Render(fit(listHeader(w), w))
	lines := []string{header}
	if len(rows) == 0 {
		empty := "Nothing here yet."
		if m.tab == TabTrain {
			empty = "Nothing left to train."
		}
		lines = append(lines, dim.Render(fit(empty, w)))
	}
	end := min(len(rows), m.offset[m.tab]+m.listHeight())
	for i := m.offset[m.tab]; i < end; i++ {
		line := fit(m.row(rows[i], w, i == m.cursor[m.tab]), w)
		if i == m.cursor[m.tab] {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return pad(lines, m.bodyHeight(), w)
}

// Column widths of a list row: time, source, then the sender takes what is left, then the score and alerted or blocked.
const (
	colTime   = 6
	colSource = 9
	colScore  = 4
	colStatus = 8
)

func listHeader(w int) string {
	return fmt.Sprintf("   %-*s%-*s%-*s%*s %s", colTime, "time", colSource, "app", senderWidth(w), "from", colScore, "score", "status")
}

func senderWidth(w int) int { return max(6, w-3-colTime-colSource-colScore-1-colStatus) }

// row writes one notification as a line of the list.
func (m *Model) row(n store.Notification, w int, selected bool) string {
	// Three marker columns: ">" for the selected row (as well as its highlight, for terminals without colour), "!" for alerted and
	// "•" for one not yet opened.
	marker := " "
	if selected {
		marker = ">"
	}
	if n.Status == store.StatusAlerted {
		marker += "!"
	} else {
		marker += " "
	}
	if n.IsRead {
		marker += " "
	} else {
		marker += "•"
	}
	return fmt.Sprintf("%s%-*s%-*s%-*s%*d %s", marker, colTime, m.when(n.Timestamp), colSource, runewidth.Truncate(oneLine(n.Source), colSource-1, "…"),
		senderWidth(w), runewidth.Truncate(oneLine(n.Sender), senderWidth(w)-1, "…"), colScore, score(n.Score), n.Status)
}

// when is the time of day for today's rows and the date for older ones.
func (m *Model) when(t time.Time) string {
	now := m.cfg.Now()
	local := t.In(now.Location())
	if local.YearDay() == now.YearDay() && local.Year() == now.Year() {
		return local.Format("15:04")
	}
	return local.Format("Jan 2")
}

// score is the model's base score on the 0 to 100 scale people see.
func score(s float64) int { return int(s*100 + 0.5) }

// detailView is the right column, or the whole screen when a notification is expanded: everything known about the selected
// notification. Collapsed, the body is cut to fit the pane; expanded, all of it can be scrolled through.
func (m *Model) detailView() string {
	w := m.width - m.listWidth() - 1
	expanded := m.expanded()
	if expanded {
		w = m.width
	}
	n, ok := m.Selected()
	if !ok {
		return pad([]string{dim.Render(fit(" Select a notification to see its details.", w))}, m.bodyHeight(), w)
	}
	meta, body := m.detailParts(n, w)
	room := m.bodyHeight() - len(meta)
	lines := append([]string{}, meta...)
	switch {
	case expanded:
		room-- // the footer
		first := max(0, min(m.scroll, len(body)-room))
		last := min(len(body), first+room)
		lines = append(lines, body[first:last]...)
		for len(lines) < m.bodyHeight()-1 {
			lines = append(lines, "")
		}
		lines = append(lines, dim.Render(fmt.Sprintf(" lines %d-%d of %d · j/k scroll · e or esc closes · o opens the app", first+1, last, len(body))))
	case len(body) > room:
		keep := max(0, room-1)
		lines = append(lines, body[:keep]...)
		lines = append(lines, dim.Render(fmt.Sprintf(" … %d more lines (e to read it all)", len(body)-keep)))
	default:
		lines = append(lines, body...)
	}
	return pad(lines, m.bodyHeight(), w)
}

// detailParts splits the detail pane into its fixed top (who, when, why) and the wrapped body text, for a pane w cells wide.
func (m *Model) detailParts(n store.Notification, w int) (meta, body []string) {
	title := oneLine(n.Sender)
	if t := oneLine(n.Title); t != "" && t != title {
		title += " — " + t
	}
	meta = append(meta, " "+lipgloss.NewStyle().Bold(true).Width(w-2).Render(title))
	meta = append(meta, " "+dim.Render(fmt.Sprintf("%s · %s · %s", oneLine(n.Source), n.Timestamp.In(m.cfg.Now().Location()).Format("Mon Jan 2 15:04"), n.Status)))
	decision := fmt.Sprintf("score %d", score(n.Score))
	if n.Boost > 0 {
		decision += fmt.Sprintf(" +%d boost", score(n.Boost))
	}
	if n.ThresholdAtDecision != nil {
		decision += fmt.Sprintf(" · focus was %d%%", *n.ThresholdAtDecision)
	}
	meta = append(meta, " "+decision)
	if n.Reason != "" {
		meta = append(meta, " "+dim.Render("why: "+oneLine(n.Reason)))
	}
	meta = append(meta, "")
	for _, line := range strings.Split(lipgloss.NewStyle().Width(w-2).Render(multiLine(n.Body)), "\n") {
		body = append(body, " "+line)
	}
	return meta, body
}

// statusBar is the line along the bottom: the focus session, then what the lists hold, then the way to the help.
func (m *Model) statusBar() string {
	left := m.sessionText()
	right := "? help"
	switch {
	case m.err != "":
		left = errStyle.Render(m.err)
	case m.notice != "":
		left = errStyle.Render(oneLine(m.notice))
	}
	counts := fmt.Sprintf("%d notifications · %d to train", len(m.lists[TabNotifications]), len(m.lists[TabTrain]))
	text := " " + left + "  │  " + counts
	gap := m.width - lipgloss.Width(text) - lipgloss.Width(right) - 1
	if gap < 1 {
		return statusStyle.Render(fit(text, m.width))
	}
	return statusStyle.Render(text + strings.Repeat(" ", gap) + right + " ")
}

// sessionText is what the focus session looks like in one line: "shh 28m · 65%", with the counts of what it has held back.
func (m *Model) sessionText() string {
	switch {
	case !m.haveStatus:
		return "shh is not running"
	case m.status.Level == 0:
		return "focus off"
	}
	title := strings.ReplaceAll(menubar.Build(m.status, m.cfg.Label).Title, " ", " ")
	top := m.status.Layers[len(m.status.Layers)-1]
	return fmt.Sprintf("%s  (%d held back, %d let through)", title, top.Blocked, top.Allowed)
}

func (m *Model) helpView() string {
	keys := [][2]string{
		{"Tab / Shift-Tab, h / l", "next / previous tab"},
		{"! @ #  (Shift-1 2 3), F1 F2 F3", "Focus, Notifications, Train"},
		{"j / k, ↓ / ↑", "move down / up"},
		{"g / G, PgUp / PgDn", "first / last, page"},
		{"e", "read it all (j/k scroll); not marked read"},
		{"Enter, o", "open its app and mark it read"},
		{"d, x", "dismiss (it stays in Train)"},
		{"?", "this help"},
		{"q, Ctrl-C", "quit"},
	}
	lines := []string{"", "  shh keys", ""}
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("  %-32s %s", k[0], k[1]))
	}
	lines = append(lines, "", dim.Render("  Press any key to go back."))
	return pad(lines, m.height, m.width)
}

// ---- small helpers ----

// fit cuts s to w cells, leaving styling escape sequences intact.
func fit(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// pad returns exactly height lines of at most width cells, so columns joined side by side line up.
func pad(lines []string, height, width int) string {
	out := make([]string, 0, height)
	for _, l := range lines {
		// A line with a newline inside would be two lines on screen, and one line too many scrolls the tab bar off the top.
		for _, part := range strings.Split(l, "\n") {
			if len(out) < height {
				out = append(out, part)
			}
		}
	}
	for len(out) < height {
		out = append(out, "")
	}
	for i, l := range out {
		l = fit(l, width)
		if gap := width - lipgloss.Width(l); gap > 0 {
			l += strings.Repeat(" ", gap)
		}
		out[i] = l
	}
	return strings.Join(out, "\n")
}

// Run shows the UI until the user quits.
func Run(cfg Config) error {
	_, err := tea.NewProgram(New(cfg), tea.WithAltScreen()).Run()
	return err
}

// Text from notifications is untrusted and arbitrary: it can hold newlines, tabs, escape sequences that move the cursor or set the
// window title, and other control characters. Drawn as it is, any of them breaks the layout, so everything that comes from a
// notification passes through one of these two before it is drawn.

var (
	oscSequence = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)?`)
	csiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	escOther    = regexp.MustCompile(`\x1b.?`)
)

func stripEscapes(s string) string {
	s = oscSequence.ReplaceAllString(s, "")
	s = csiSequence.ReplaceAllString(s, "")
	s = escOther.ReplaceAllString(s, "")
	return invisibleJoiners.Replace(s)
}

// invisibleJoiners are zero-width characters that glue emoji together (👩‍💻) or pick their emoji or text form (✔️). Terminals and
// width tables disagree about how wide the result is, and a line that is wider on screen than the layout thinks wraps and pushes
// the tab bar off the top. Dropped, each part is drawn on its own with a width everyone agrees on.
var invisibleJoiners = strings.NewReplacer("\u200d", "", "\ufe0f", "", "\ufe0e", "", "\u200b", "", "\u2060", "", "\ufeff", "")

// oneLine makes s fit on a single line: every run of whitespace and control characters becomes one space.
func oneLine(s string) string {
	var b strings.Builder
	space := false
	for _, r := range stripEscapes(s) {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// multiLine keeps the line breaks of s but removes anything else that would break the layout: tabs become spaces, carriage returns
// and other control characters go.
func multiLine(s string) string {
	var b strings.Builder
	for _, r := range stripEscapes(s) {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("    ")
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
