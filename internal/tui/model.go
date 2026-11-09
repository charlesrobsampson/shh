package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/charlesrobsampson/shh/internal/decide"
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

// Trainer teaches the model from labelled notifications. The worker owns the model, so training goes through it: *session.Client
// satisfies this.
type Trainer interface {
	Train(session.TrainRequest) (session.TrainView, error)
	UndoTraining() (session.TrainView, error)
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
	// Trainer handles y, n, l and u. Nil turns training off.
	Trainer Trainer
	// Ensure makes sure a worker is running before a notification is trained or an undo is made (starting one if need be); nil means
	// do not check.
	Ensure func() error
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
	// prompt is the label being typed after t or a digit; nil when there is none.
	prompt *prompt
	// cursorOn is whether the prompt's cursor is showing right now; blinkGen identifies the current blink timer; restartBlink asks
	// for a new one after the key that was just handled.
	cursorOn     bool
	blinkGen     int
	restartBlink bool
	// undoStack remembers what each training in this session did besides teaching the model, so undo can put that back too.
	undoStack []trainedEntry
	// notice is what the last action did or why it could not, shown in the status bar until the next key.
	notice string
}

type tickMsg time.Time

// blinkMsg flips the prompt's cursor. gen says which prompt or keystroke started this blink timer, so a timer left over from an earlier
// one (or from a prompt that has closed) does nothing and ends.
type blinkMsg struct{ gen int }

// blinkInterval is how long the cursor stays on or off.
const blinkInterval = 500 * time.Millisecond

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
		if tab == TabTrain {
			sortMostInformativeFirst(rows)
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
	case blinkMsg:
		if m.prompt == nil || msg.gen != m.blinkGen {
			return m, nil // a timer from a prompt or keystroke that has gone
		}
		m.cursorOn = !m.cursorOn
		return m, m.blink()
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.notice = ""
	if m.prompt != nil {
		m.promptKey(msg)
		return m, m.takeBlink()
	}
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
	case "y":
		m.quickTrain(true)
	case "n":
		m.quickTrain(false)
	case "t":
		m.openPrompt("")
	case "u":
		m.undoTraining()
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.openPrompt(msg.String()) // a digit starts typing a label, so a score can be given with one hand
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
	return m, m.takeBlink()
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

// ---- training ----

// trainedEntry is what one training did to the lists besides the model: whether it also archived the row from Notifications.
type trainedEntry struct {
	id       int64
	archived bool
}

// prompt is the exact label being typed.
type prompt struct {
	text   string
	err    string
	target store.Notification
}

const maxLabelInput = 12

// quickTrain answers "did shh do the right thing?" for the selected notification: y says it did (a notification it held back should
// stay held back, one it alerted on should have been alerted), n says it did not. The row already shows what shh did, so the answer
// needs no working out. The worker's quick labels are absolute (y: it should have reached the user at the level it was decided at, n:
// it should have stayed muted), so the answer is turned into one of those using what shh did.
func (m *Model) quickTrain(right bool) {
	n, ok := m.trainable()
	if !ok {
		return
	}
	alerted := n.Status == store.StatusAlerted
	feedback := "n"
	if alerted == right {
		feedback = "y"
	}
	m.train(session.TrainRequest{Feedback: feedback}, n)
}

// trainable is the selected notification, when training is possible here: not on the Focus tab, and with a trainer.
func (m *Model) trainable() (store.Notification, bool) {
	n, ok := m.Selected()
	if !ok {
		return store.Notification{}, false
	}
	if m.cfg.Trainer == nil {
		m.notice = "training is not available here"
		return store.Notification{}, false
	}
	return n, true
}

// openPrompt (t, or a digit) starts typing an exact label (0 to 100, or a tier name) for the selected notification.
func (m *Model) openPrompt(initial string) {
	if n, ok := m.trainable(); ok {
		m.prompt = &prompt{text: initial, target: n}
		m.cursorOn, m.restartBlink = true, true
	}
}

func (m *Model) promptKey(msg tea.KeyMsg) {
	p := m.prompt
	m.cursorOn, m.restartBlink = true, true // the cursor stays solid while typing, then starts blinking again
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.prompt = nil
	case tea.KeyEnter:
		label, err := decide.ParseLabel(p.text)
		if err != nil {
			p.err = shortLabelError(p.text, err)
			return
		}
		m.prompt = nil
		m.train(session.TrainRequest{Label: &label}, p.target)
	case tea.KeyBackspace:
		if r := []rune(p.text); len(r) > 0 {
			p.text = string(r[:len(r)-1])
		}
		p.err = ""
	case tea.KeyRunes, tea.KeySpace:
		if len([]rune(p.text)) < maxLabelInput {
			p.text += string(msg.Runes)
			if msg.Type == tea.KeySpace {
				p.text += " "
			}
		}
		p.err = ""
	}
}

// shortLabelError words a bad label so it fits under the box: the parser's own message when it is short ("101" is above 100), a brief
// version of its long one otherwise.
func shortLabelError(text string, err error) string {
	if msg := err.Error(); len(msg) <= 40 {
		return msg
	}
	return fmt.Sprintf("%q is not a score: use 0-100, or light, medium, deep, lockdown", oneLine(text))
}

// train labels a notification through the worker. A notification trained from the Notifications tab is archived from it, as one
// trained on the Train tab leaves that list; either way the row disappears from the list it was trained in.
func (m *Model) train(req session.TrainRequest, n store.Notification) {
	if err := m.ensureWorker(); err != nil {
		m.notice = "cannot reach the worker to train: " + err.Error()
		return
	}
	req.NotificationID = n.ID
	out, err := m.cfg.Trainer.Train(req)
	switch {
	case errors.Is(err, session.ErrAlreadyTrained):
		m.notice = "already trained: press u to undo the last training first"
		return
	case err != nil:
		m.notice = "could not train: " + err.Error()
		return
	}
	entry := trainedEntry{id: n.ID}
	if m.tab == TabNotifications {
		if err := m.cfg.Data.SetDismissed(n.ID, true); err != nil {
			m.notice = "trained, but could not archive it: " + err.Error()
		} else {
			entry.archived = true
		}
	}
	m.undoStack = append(m.undoStack, entry)
	if m.notice == "" {
		m.notice = describeTraining(req, out, n.Status)
	}
	m.reload()
}

func describeTraining(req session.TrainRequest, out session.TrainView, did store.Status) string {
	what := "labelled"
	switch {
	case req.Label != nil:
		what = fmt.Sprintf("labelled %d", *req.Label)
	default:
		alerted := did == store.StatusAlerted
		shouldAlert := req.Feedback == "y"
		switch {
		case alerted && shouldAlert:
			what = "right call to alert you"
		case !alerted && !shouldAlert:
			what = "right call to hold it back"
		case shouldAlert:
			what = "should have alerted"
		default:
			what = "should have stayed muted"
		}
	}
	if !out.Updated {
		return fmt.Sprintf("%s: the model already agreed (score %d), nothing to change", what, out.Before)
	}
	return fmt.Sprintf("%s: score %d → %d  (u undoes)", what, out.Before, out.After)
}

// undoTraining reverses the most recent training exactly: the model's weights and the row's flags come back, and a row that the
// training archived from Notifications is put back there.
func (m *Model) undoTraining() {
	if m.tab == TabFocus {
		return
	}
	if m.cfg.Trainer == nil {
		m.notice = "training is not available here"
		return
	}
	if err := m.ensureWorker(); err != nil {
		m.notice = "cannot reach the worker to undo: " + err.Error()
		return
	}
	out, err := m.cfg.Trainer.UndoTraining()
	switch {
	case errors.Is(err, session.ErrNothingToUndo):
		m.notice = "nothing to undo"
		return
	case err != nil:
		m.notice = "could not undo: " + err.Error()
		return
	}
	if n := len(m.undoStack); n > 0 && m.undoStack[n-1].id == out.NotificationID {
		if m.undoStack[n-1].archived {
			if err := m.cfg.Data.SetDismissed(out.NotificationID, false); err != nil {
				m.notice = "undone, but could not put it back on Notifications: " + err.Error()
			}
		}
		m.undoStack = m.undoStack[:n-1]
	}
	if m.notice == "" {
		m.notice = "undid the last training"
	}
	m.reload()
	m.selectID(out.NotificationID)
}

// blink schedules the next flip of the cursor.
func (m *Model) blink() tea.Cmd {
	gen := m.blinkGen
	return tea.Tick(blinkInterval, func(time.Time) tea.Msg { return blinkMsg{gen: gen} })
}

// takeBlink starts a fresh blink timer if the key just handled asked for one, and retires the old one.
func (m *Model) takeBlink() tea.Cmd {
	if !m.restartBlink {
		return nil
	}
	m.restartBlink = false
	m.blinkGen++
	return m.blink()
}

// selectID moves the cursor to the notification with this id on the current tab, if it is there.
func (m *Model) selectID(id int64) {
	if m.tab == TabFocus {
		return
	}
	for i, n := range m.lists[m.tab] {
		if n.ID == id {
			m.cursor[m.tab] = i
			m.clamp(m.tab)
			return
		}
	}
}

func (m *Model) ensureWorker() error {
	if m.cfg.Ensure == nil {
		return nil
	}
	return m.cfg.Ensure()
}

// sortMostInformativeFirst orders the Train list so the notifications a label would teach the model most come first: those whose
// score sits close to one of the common thresholds, where being a little wrong changes what gets through. Ties keep the newest first.
func sortMostInformativeFirst(rows []store.Notification) {
	distance := func(n store.Notification) float64 {
		effective := decide.Decide(0, n.Score, n.Boost, false).Effective * 100
		best := 100.0
		for _, t := range decide.Tiers() {
			if t.Level == 0 {
				continue
			}
			best = min(best, abs(effective-float64(t.Level)))
		}
		return best
	}
	sort.SliceStable(rows, func(i, j int) bool { return distance(rows[i]) < distance(rows[j]) })
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
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
	statusBarHeight = 2 // the state line and the message line under it
)

// bodyHeight is the room between the tab bar and the status bar.
func (m *Model) bodyHeight() int { return max(3, m.height-tabBarHeight-statusBarHeight) }

// listHeight is how many rows the list shows: the body without the header line of the list.
func (m *Model) listHeight() int { return max(1, m.bodyHeight()-1) }

func (m *Model) listWidth() int { return max(30, m.width*45/100) }

var (
	// Colours come only from the terminal's own palette (ANSI colours 0 to 15, bold, faint and reverse), never fixed RGB values, so the
	// UI takes on whatever theme the terminal is using, light or dark.
	blockedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true) // yellow in most themes: needs a look
	alertedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green: it got through
	unreadStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	// inputStyle is the box a label is typed in. It is drawn in normal video inside the reversed bar, so it stands out from the text
	// around it whatever the theme, and in yellow and underlined so it reads as a place to type.
	inputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true).Underline(true)
	tabStyle    = lipgloss.NewStyle().Padding(0, 1)
	activeTab   = tabStyle.Bold(true).Reverse(true)
	dim         = lipgloss.NewStyle().Faint(true)
	statusStyle = lipgloss.NewStyle().Reverse(true)
	errStyle    = lipgloss.NewStyle().Bold(true)
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
		lines = append(lines, fit(m.row(rows[i], w, i == m.cursor[m.tab]), w))
	}
	return pad(lines, m.bodyHeight(), w)
}

// Column widths of a list row: time, source, then the sender takes what is left, then the score and the blocked mark.
const (
	colTime   = 6
	colSource = 9
	colScore  = 5
	colStatus = 10
)

// blockedMark is what a blocked notification shows in the last column; one that was alerted shows nothing. A blocked one never
// reached you, so it is the one worth a glance, and a blank row is one you already saw.
const blockedMark = "✕ blocked"

func listHeader(w int) string {
	return fmt.Sprintf("  %-*s%-*s%-*s%*s  %s", colTime, "time", colSource, "app", senderWidth(w), "from", colScore, "score", "held back")
}

func senderWidth(w int) int { return max(6, w-2-colTime-colSource-colScore-2-colStatus) }

// row writes one notification as a line of the list, in the terminal's own colours. The selected row is drawn in reverse video, so
// each piece is styled with that already, instead of nesting styles inside a reversed line.
func (m *Model) row(n store.Notification, w int, selected bool) string {
	cell := func(style lipgloss.Style, text string) string { return style.Reverse(selected).Render(text) }
	plain := lipgloss.NewStyle().Bold(selected)

	// Two marker columns: ">" for the selected row (as well as its highlight, for terminals without colour) and "•" for one not yet
	// opened.
	marker := " "
	if selected {
		marker = ">"
	}
	unread := " "
	if !n.IsRead {
		unread = "•"
	}
	status := strings.Repeat(" ", colStatus)
	statusStyle := plain
	if n.Status == store.StatusBlocked {
		status = fmt.Sprintf("%-*s", colStatus, blockedMark)
		statusStyle = blockedStyle
	}
	return cell(plain, marker) + cell(unreadStyle, unread) +
		cell(plain, fmt.Sprintf("%-*s%-*s%-*s%*d  ", colTime, m.when(n.Timestamp), colSource, runewidth.Truncate(oneLine(n.Source), colSource-1, "…"),
			senderWidth(w), runewidth.Truncate(oneLine(n.Sender), senderWidth(w)-1, "…"), colScore, score(n.Score))) +
		cell(statusStyle, status)
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
	statusText := alertedStyle.Render("alerted")
	if n.Status == store.StatusBlocked {
		statusText = blockedStyle.Render("blocked")
	}
	meta = append(meta, " "+dim.Render(fmt.Sprintf("%s · %s · ", oneLine(n.Source), n.Timestamp.In(m.cfg.Now().Location()).Format("Mon Jan 2 15:04")))+statusText)
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
	return m.stateLine() + "\n" + m.messageLine()
}

// stateLine is the first line of the footer: the focus session and what the lists hold, or, while a label is being typed, the
// question and the box it is typed in.
func (m *Model) stateLine() string {
	if m.prompt != nil {
		return m.promptBar()
	}
	right := "? help"
	counts := fmt.Sprintf("%d notifications · %d to train", len(m.lists[TabNotifications]), len(m.lists[TabTrain]))
	text := " " + m.sessionText() + "  │  " + counts
	gap := m.width - lipgloss.Width(text) - lipgloss.Width(right) - 1
	if gap < 1 {
		return statusStyle.Render(fit(text, m.width))
	}
	return statusStyle.Render(text + strings.Repeat(" ", gap) + right + " ")
}

// messageLine is the second line of the footer, where anything to say goes so that it is never squeezed into the state line: what
// the last action did, a problem, what is wrong with the label being typed, or, when there is nothing to report, the keys that
// apply here.
func (m *Model) messageLine() string {
	var text string
	var style lipgloss.Style
	switch {
	case m.prompt != nil && m.prompt.err != "":
		text, style = "✗ "+oneLine(m.prompt.err), errStyle
	case m.prompt != nil:
		text, style = "Enter saves · Esc cancels · a score from 0 to 100, or light, medium, deep or lockdown", dim
	case m.err != "":
		text, style = m.err, errStyle
	case m.notice != "":
		text, style = oneLine(m.notice), errStyle
	default:
		text, style = m.keyHints(), dim
	}
	return style.Render(fit(" "+text, m.width))
}

// keyHints are the keys worth knowing on the current tab.
func (m *Model) keyHints() string {
	switch m.tab {
	case TabFocus:
		return "! @ # or Tab switch tabs · ? help · q quit"
	case TabTrain:
		return "y shh got it right · n shh got it wrong · t type a score · u undo · e read · o open · ? help"
	}
	return "e read · o open its app · d dismiss · y right call · n wrong call · t score · u undo · ? help"
}

// promptBar is the state line while an exact label is being typed: the question in the bar's own reverse video, then the box the
// label is typed in (yellow, underlined, with a blinking block cursor). The hint and any error are on the line below.
func (m *Model) promptBar() string {
	p := m.prompt
	cursor := " "
	if m.cursorOn {
		cursor = "█"
	}
	question := fmt.Sprintf(" label for %s: ", oneLine(p.target.Sender))
	field := " " + oneLine(p.text) + cursor + " "
	// Make everything fit the line: the box always shows in full, the question gives way.
	fieldWidth := lipgloss.Width(field)
	question = fit(question, max(0, m.width-fieldWidth))
	fill := strings.Repeat(" ", max(0, m.width-lipgloss.Width(question)-fieldWidth))
	return statusStyle.Render(question) + inputStyle.Render(field) + statusStyle.Render(fill)
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
		{"Tab / Shift-Tab, l / h, ← / →", "next / previous tab"},
		{"! @ #  (Shift-1 2 3), F1 F2 F3", "Focus, Notifications, Train"},
		{"j / k, ↓ / ↑", "move down / up"},
		{"g / G, PgUp / PgDn", "first / last, page"},
		{"e", "read it all (j/k scroll); not marked read"},
		{"Enter, o", "open its app and mark it read"},
		{"d, x", "dismiss (it stays in Train)"},
		{"y / n", "did shh do the right thing? yes / no"},
		{"t, or a digit", "type an exact 0-100 score or a tier name"},
		{"u", "undo the last training"},
		{"?", "this help"},
		{"q, Ctrl-C", "quit"},
	}
	lines := []string{"", "  shh keys", ""}
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("  %-32s %s", k[0], k[1]))
	}
	lines = append(lines, "", "  • not opened yet    ✕ blocked: held back, never reached you (no mark: it was alerted)",
		"", dim.Render("  Press any key to go back."))
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
