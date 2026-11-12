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
	"github.com/charlesrobsampson/shh/internal/guide"
	"github.com/charlesrobsampson/shh/internal/menubar"
	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

// Data is the part of the store the lists read and the actions change.
type Data interface {
	// The runs of routines, for the history on the Routines tab.
	session.RunStore
	ListNotifications(store.Filter) ([]store.Notification, error)
	SetRead(id int64, read bool) error
	SetDismissed(id int64, dismissed bool) error
	SetFlagged(id int64, flagged bool) error
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
	// Control handles the Focus tab's keys. Nil turns them off.
	Control Control
	// Saved lists the routines in the routines file; Routines runs them. Either nil turns the Routines tab's actions off.
	Saved    func() ([]routine.Routine, error)
	Routines RoutineControl
	// SaveRoutine writes a routine to the routines file (replacing the one of that name when asked); RemoveRoutine deletes one.
	// Either nil turns editing off.
	SaveRoutine   func(r routine.Routine, replace bool) error
	RemoveRoutine func(name string) error
	// Summarize writes (and keeps) a short summary of a notification, for s. Nil, or ErrNoSummarizer from it, means none is set up. It
	// runs off the UI's thread. SummarizerName is the backend's name and SummarizerRemote whether the text leaves this Mac (the status
	// bar says so then).
	Summarize        func(store.Notification) (string, error)
	SummarizerName   string
	SummarizerRemote bool
	// Trainer handles y, n, t and u. Nil turns training off.
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
	// StartTab is the tab to open on: focus, notifications, train or routines; empty for the usual one.
	StartTab string
	// HelpOnly opens the guide on its own, as shh guide does: closing it leaves shh. HelpSection (a section's slug) and HelpSearch
	// say where it opens and what it searches for first. Either of those also opens the help when HelpOnly is not set.
	HelpOnly                bool
	HelpSection, HelpSearch string
}

// Tab is one of the screens.
type Tab int

const (
	TabFocus Tab = iota
	TabNotifications
	TabTrain
	TabRoutines
	tabCount
)

func (t Tab) String() string {
	return [...]string{"Focus", "Notifications", "Train", "Routines"}[t]
}

// maxRows bounds how many rows a list loads: the newest ones, which are the ones anyone looks at.
const maxRows = 500

// tabKeys are the keys that jump straight to a tab: shifted digits, which leaves the digits themselves free for training labels
// (F1 to F3 work too).
var tabKeys = [tabCount]string{"!", "@", "#", "$"}

// Model is the terminal UI as a Bubble Tea model.
type Model struct {
	cfg           Config
	tab           Tab
	width, height int
	showHelp      bool
	helpScroll    int // how far down the help text is
	// helpQuery is the search made in the help, helpHits the lines where its matches start and helpAt which of them is shown (-1 for
	// none). helpJump and helpSearch are a section and a search to go to when the help is first drawn.
	helpQuery            guide.Query
	helpHits             []int
	helpAt               int
	helpJump, helpSearch string
	layoutLines          []guide.Line // the guide laid out for layoutWidth
	layoutWidth          int
	err                  string // a problem to show in the status bar, such as the database failing to read

	lists  [tabCount][]store.Notification // the rows of the Notifications and Train tabs
	cursor [tabCount]int                  // the selected row in each list
	offset [tabCount]int                  // the first row shown in each list

	status     session.StatusView
	haveStatus bool // whether the worker answered the last time it was asked

	// summaries holds the summaries made in this run (the stored ones come with the notification); summarizing is those being written.
	summaries   map[int64]string
	summarizing map[int64]bool
	// expandedID is the notification shown in full across the whole screen (e); 0 when none is. scroll is how far down its text is.
	expandedID int64
	scroll     int
	// show narrows both lists to the notifications shh held back or let through ("" for all): v goes through the choices.
	show store.Status
	// filter narrows both lists to notifications containing this text; totals is how many each list holds before it is applied.
	filter string
	totals [tabCount]int
	// focusCursor is the selected row of the Focus tab's menu; routineCursor the selected routine of the Routines tab.
	focusCursor, routineCursor int
	// saved is the routines last read from the file, and savedErr why they could not be.
	saved    []routine.Routine
	savedErr string
	// history is the list of past runs, open on the Routines tab; nil when it is not.
	history *historyView
	// trigForm is the trigger being changed on top of the editor; nil when none is.
	trigForm *triggerForm
	// editor is the routine being made or changed on the Routines tab; nil when none is.
	editor *routineEditor
	// afterRoutine is what to do once the user agrees to end the running routine, which was in the way of what they asked for.
	afterRoutine, afterContinue, afterQueue func()
	// continueWith, set just before run, is how to start the same session on top of a running routine, if that makes sense for it.
	continueWith, queueWith func(Control) (session.StatusView, error)
	// count is the number typed before a motion (3 in 3k), vim style; 0 when none is pending.
	count int
	// prompt is the label being typed after t; nil when there is none.
	prompt *prompt
	// cursorOn is whether the prompt's cursor is showing right now; blinkGen identifies the current blink timer; restartBlink asks
	// for a new one after the key that was just handled.
	cursorOn     bool
	blinkGen     int
	restartBlink bool
	// undoStack remembers what each training in this session did besides teaching the model, so undo can put that back too.
	undoStack []trainedEntry
	// noticeCmd is the shh command that does what the last action did, shown at the right of the notice. noticeHasCmd says the action
	// is one that has such a thing to show; noticeCmd is then empty when the command line has no equivalent.
	noticeCmd    string
	noticeHasCmd bool
	// notice is what the last action did or why it could not, shown in the status bar until the next key.
	notice string
}

// ErrNoSummarizer is what Config.Summarize returns when the backend is none.
var ErrNoSummarizer = errors.New("no summarizer is set up")

// summaryMsg is a summary that arrived, or the reason there is none.
type summaryMsg struct {
	id   int64
	text string
	err  error
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
	m := &Model{cfg: cfg, tab: TabNotifications, width: 100, height: 30, helpAt: -1}
	m.reload()
	for tab := Tab(0); tab < tabCount; tab++ {
		if strings.EqualFold(cfg.StartTab, tab.String()) {
			m.tab = tab
		}
	}
	if cfg.HelpOnly || cfg.HelpSection != "" || cfg.HelpSearch != "" {
		m.openGuide(cfg.HelpSection, cfg.HelpSearch)
	}
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
		rows, err := m.cfg.Data.ListNotifications(store.Filter{View: view, Limit: maxRows, Status: m.show})
		if err != nil {
			m.err = "cannot read notifications: " + err.Error()
			continue
		}
		m.totals[tab] = len(rows)
		if m.filter != "" {
			rows = filterRows(rows, m.filter)
		}
		if tab == TabTrain {
			sortMostInformativeFirst(rows)
		}
		m.lists[tab] = rows
		m.cursor[tab] = indexOf(rows, selected, m.cursor[tab])
		m.clamp(tab)
	}
	m.loadSaved()
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
	if m.listless() || m.cursor[m.tab] >= len(m.lists[m.tab]) {
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
	case summaryMsg:
		delete(m.summarizing, msg.id)
		switch {
		case errors.Is(msg.err, ErrNoSummarizer):
			m.notice = noSummarizerHint
		case msg.err != nil:
			m.notice = "could not summarize: " + oneLine(msg.err.Error())
		default:
			if m.summaries == nil {
				m.summaries = map[int64]string{}
			}
			m.summaries[msg.id] = msg.text
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

const noSummarizerHint = "no summarizer is set up: choose a backend in config.json (\"summarizer\": {\"backend\": \"ollama\"}), see shh guide config"

// summarize starts a summary of the selected notification, unless it has one already or one is on its way. Nothing about the notification
// changes but its summary: it is not marked read or trained.
func (m *Model) summarize() tea.Cmd {
	n, ok := m.Selected()
	if !ok {
		return nil
	}
	if m.cfg.Summarize == nil {
		m.notice = noSummarizerHint
		return nil
	}
	if n.Summary != "" || m.summaries[n.ID] != "" {
		m.notice = "already summarized (shown under the score)"
		return nil
	}
	if m.summarizing[n.ID] {
		return nil
	}
	if m.summarizing == nil {
		m.summarizing = map[int64]bool{}
	}
	m.summarizing[n.ID] = true
	m.notice = "summarizing…"
	summarize := m.cfg.Summarize
	return func() tea.Msg {
		text, err := summarize(n)
		return summaryMsg{id: n.ID, text: text, err: err}
	}
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.notice, m.noticeCmd, m.noticeHasCmd = "", "", false
	if m.prompt != nil {
		m.promptKey(msg)
		return m, m.takeBlink()
	}
	if m.showHelp {
		return m.helpKey(msg)
	}
	if m.history != nil {
		m.historyKey(msg.String(), 1)
		return m, m.takeBlink()
	}
	if m.trigForm != nil {
		m.triggerFormKey(msg.String())
		return m, m.takeBlink()
	}
	if m.editor != nil {
		m.editorKey(msg.String())
		return m, m.takeBlink()
	}
	// A number typed before a motion repeats it (6j, 3k) or says where to go (5G). Digits only build a count here; an exact label is
	// typed after t, when the prompt is open and digits go into it.
	key := msg.String()
	if digit, ok := countDigit(key); ok {
		if digit != 0 || m.count > 0 { // a lone 0 does nothing
			m.count = min(m.count*10+digit, maxCount)
		}
		return m, nil
	}
	count := m.count
	m.count = 0
	if key == "esc" && count > 0 {
		return m, nil // Esc drops a pending count before it does anything else
	}
	steps := max(1, count)

	// The Focus tab has no rows to act on, so its letters mean other things: start, baseline, level, pause, adjust, stop.
	if m.tab == TabFocus && m.focusKey(key, steps) {
		return m, m.takeBlink()
	}
	if m.tab == TabRoutines && m.routineKey(key) {
		return m, m.takeBlink()
	}

	if key == "s" && !m.listless() && m.tab != TabFocus && m.tab != TabRoutines {
		return m, tea.Batch(m.summarize(), m.takeBlink())
	}

	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.openGuide("", "")
	case "tab", "l", "right":
		m.goTo((m.tab + Tab(steps%int(tabCount))) % tabCount)
	case "shift+tab", "h", "left":
		m.goTo((m.tab + tabCount - Tab(steps%int(tabCount))) % tabCount)
	case "f1", "!":
		m.goTo(TabFocus)
	case "f2", "@":
		m.goTo(TabNotifications)
	case "f3", "#":
		m.goTo(TabTrain)
	case "f4", "$":
		m.goTo(TabRoutines)
	case "e":
		m.toggleExpand()
	case "esc":
		if m.expanded() {
			m.collapse()
		} else if m.filter != "" || m.show != "" {
			m.show = ""
			m.setFilter("")
		}
	case "v":
		m.cycleShow()
	case "/":
		if !m.listless() {
			m.openAsk(promptFilter, m.filter)
		}
	case "enter", "o":
		m.open()
	case "d", "x":
		m.dismiss()
	case "f":
		m.toggleFlag()
	case "y":
		m.quickTrain(true)
	case "n":
		m.quickTrain(false)
	case "t":
		m.openPrompt("")
	case "u":
		m.undoTraining()
	case "j", "down":
		m.move(steps)
	case "k", "up":
		m.move(-steps)
	case "g", "home":
		m.jumpTo(max(0, count-1)) // the top, or row N when a number came first
	case "G", "end":
		if count > 0 {
			m.jumpTo(count - 1) // 5G goes to row 5, as in vim
		} else {
			m.jumpTo(1 << 30) // the bottom: jumpTo keeps it inside the list or the text
		}
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

// toggleFlag flags the selected notification for review in the Train tab, or clears the flag.
func (m *Model) toggleFlag() {
	n, ok := m.Selected()
	if !ok {
		return
	}
	if err := m.cfg.Data.SetFlagged(n.ID, !n.IsFlagged); err != nil {
		m.notice = "could not change the flag: " + err.Error()
		return
	}
	if n.IsFlagged {
		m.notice = "flag cleared"
	} else {
		m.notice = "flagged: it is at the top of the Train tab for review"
	}
	m.reload()
	m.selectID(n.ID)
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

// promptKind says what a prompt is asking for, and so what Enter does with the text.
type promptKind int

const (
	promptLabel             promptKind = iota // an exact label for the selected notification
	promptStart                               // a new focus session: level, length, note
	promptBaseline                            // the standing level
	promptLevel                               // a new level for the session in force
	promptFilter                              // text to narrow the lists to
	promptConfirmStopAll                      // y or n: turn everything off, baseline included
	promptConfirmEndRoutine                   // y or n: end the running routine, which is in the way
	promptConfirmDelete                       // y or n: delete the selected saved routine
	promptEditField                           // a value in the routine editor
	promptHelpSearch                          // text to find in the guide
)

// prompt is a line being typed at the bottom of the screen.
type prompt struct {
	kind   promptKind
	text   string
	err    string
	target store.Notification // for a label: the notification it is for
	// For an editor field: what is being asked, how to answer, and which row it is for.
	label, help string
	row         editorRow
	// canContinue is set on the question about a routine in the way when the session can also go on top of it.
	canContinue bool
	// back is how many characters the cursor is from the end of the text: 0 types at the end, as a text set by the program does.
	back int
}

// editKey handles the keys that edit the text of a prompt: typing and deleting at the cursor, and moving it. It reports whether the
// key was one of them.
func (p *prompt) editKey(msg tea.KeyMsg) bool {
	r := []rune(p.text)
	back := max(0, min(p.back, len(r)))
	at := len(r) - back // where the cursor is, counting from the start
	set := func(runes []rune, newBack int) {
		p.text, p.back, p.err = string(runes), newBack, ""
	}
	switch msg.String() {
	case "left", "ctrl+b":
		p.back = min(back+1, len(r))
	case "right", "ctrl+f":
		p.back = max(back-1, 0)
	case "home", "ctrl+a":
		p.back = len(r)
	case "end", "ctrl+e":
		p.back = 0
	case "alt+left", "alt+b", "ctrl+left":
		p.back = len(r) - wordStart(r, at)
	case "alt+right", "alt+f", "ctrl+right":
		p.back = len(r) - wordEnd(r, at)
	case "backspace", "ctrl+h":
		if at > 0 {
			set(append(r[:at-1:at-1], r[at:]...), back)
		}
	case "delete", "ctrl+d":
		if back > 0 {
			set(append(r[:at:at], r[at+1:]...), back-1)
		}
	case "ctrl+w", "alt+backspace":
		if start := wordStart(r, at); start < at {
			set(append(r[:start:start], r[at:]...), back)
		}
	case "ctrl+u":
		set(r[at:], len(r)-at)
	case "ctrl+k":
		set(r[:at], 0)
	default:
		switch msg.Type {
		case tea.KeyRunes, tea.KeySpace:
			typed := msg.Runes
			if msg.Type == tea.KeySpace && len(typed) == 0 { // the terminal's space key carries the space itself
				typed = []rune{' '}
			}
			if len(r)+len(typed) <= promptLimit(p.kind) {
				set(append(append(r[:at:at], typed...), r[at:]...), back)
			}
		default:
			return false
		}
	}
	return true
}

// wordStart is where the word before offset at begins (or the start of the text), wordEnd where the word after it ends.
func wordStart(r []rune, at int) int {
	for at > 0 && r[at-1] == ' ' {
		at--
	}
	for at > 0 && r[at-1] != ' ' {
		at--
	}
	return at
}

func wordEnd(r []rune, at int) int {
	for at < len(r) && r[at] == ' ' {
		at++
	}
	for at < len(r) && r[at] != ' ' {
		at++
	}
	return at
}

// window cuts the text to what fits in room characters, keeping the cursor in view, and splits it around the cursor: what comes
// before it, the character under it ("" at the end of the text) and what follows.
func (p *prompt) window(room int) (before, under, after string) {
	// Each character keeps its place (a control character shows as a space), so the cursor's offset still points at the right one.
	r := []rune(p.text)
	for i, c := range r {
		if unicode.IsControl(c) {
			r[i] = ' '
		}
	}
	back := max(0, min(p.back, len(r)))
	at := len(r) - back
	room = max(3, room)
	start := 0
	if len(r)+1 > room { // the cursor needs a place too
		start = max(0, min(at-room+1, len(r)-room+1))
		if at < start {
			start = at
		}
	}
	end := min(len(r), start+room-1)
	at = max(start, min(at, end))
	before = string(r[start:at])
	if at < end {
		under = string(r[at])
		after = string(r[at+1 : end])
	}
	return before, under, after
}

// promptLimit is how much can be typed: a label is short, a focus line with a note is not.
func promptLimit(kind promptKind) int {
	switch kind {
	case promptLabel:
		return 12
	case promptEditField:
		return 160
	}
	return 80
}

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

// openPrompt (on t) starts typing an exact label (0 to 100, or a tier name) for the selected notification.
func (m *Model) openPrompt(initial string) {
	if n, ok := m.trainable(); ok {
		m.prompt = &prompt{text: initial, target: n}
		m.cursorOn, m.restartBlink = true, true
	}
}

// openAsk starts a prompt that is not about a notification (a focus line, a baseline, a level, a filter).
func (m *Model) openAsk(kind promptKind, initial string) {
	m.prompt = &prompt{kind: kind, text: initial}
	m.cursorOn, m.restartBlink = true, true
}

func (m *Model) promptKey(msg tea.KeyMsg) {
	p := m.prompt
	m.cursorOn, m.restartBlink = true, true // the cursor stays solid while typing, then starts blinking again
	if p.kind == promptConfirmStopAll || p.kind == promptConfirmEndRoutine || p.kind == promptConfirmDelete {
		// A yes or no question: one key answers it.
		switch msg.String() {
		case "y", "Y":
			m.prompt = nil
			switch p.kind {
			case promptConfirmEndRoutine:
				m.endRoutineThen()
			case promptConfirmDelete:
				m.deleteSelected()
			default:
				m.stop(true)
			}
		case "c", "C":
			if p.kind == promptConfirmEndRoutine && m.afterContinue != nil {
				m.prompt = nil
				m.continueRoutineThen()
			}
		case "q", "Q":
			if p.kind == promptConfirmEndRoutine && m.afterQueue != nil {
				m.prompt = nil
				m.queueRoutineThen()
			}
		case "n", "N", "esc", "ctrl+c":
			m.prompt = nil
			m.afterRoutine, m.afterContinue, m.afterQueue = nil, nil, nil
		}
		return
	}
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.prompt = nil
	case tea.KeyEnter:
		m.submitPrompt()
	default:
		p.editKey(msg)
	}
}

// submitPrompt does what Enter means for the prompt that is open. A text that cannot be used leaves the prompt open with the reason
// under it, and nothing is sent anywhere.
func (m *Model) submitPrompt() {
	p := m.prompt
	switch p.kind {
	case promptLabel:
		label, err := decide.ParseLabel(p.text)
		if err != nil {
			p.err = shortLabelError(p.text, err)
			return
		}
		m.prompt = nil
		m.train(session.TrainRequest{Label: &label}, p.target)
	case promptStart:
		m.startFocus(p)
	case promptBaseline:
		m.setBaseline(p)
	case promptLevel:
		m.changeLevel(p)
	case promptFilter:
		m.prompt = nil
		m.setFilter(strings.TrimSpace(p.text))
	case promptEditField:
		m.submitField(p)
	case promptHelpSearch:
		m.prompt = nil
		m.setHelpQuery(p.text)
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
	if m.listless() {
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
	if m.listless() {
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

// sortMostInformativeFirst orders the Train list: flagged notifications first, then the rest so the notifications a label would teach the model most come first: those whose
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
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].IsFlagged != rows[j].IsFlagged {
			return rows[i].IsFlagged // the ones the user flagged to review come first, whatever their score
		}
		return distance(rows[i]) < distance(rows[j])
	})
}

func abs[T int | float64](x T) T {
	if x < 0 {
		return -x
	}
	return x
}

// openApp opens an app by bundle id, the same way the alert banners do.
func openApp(bundleID string) error {
	return exec.Command("open", "-b", bundleID).Run()
}

// setFilter narrows the lists to notifications containing the text, or clears the filter when it is empty.
func (m *Model) setFilter(text string) {
	m.filter = text
	m.collapse()
	m.reload()
	if text == "" {
		m.notice = "filter cleared"
	} else {
		m.notice = fmt.Sprintf("showing only what contains %q (Esc clears the filter)", text)
	}
}

// showText names a status for the tab bar and messages.
func showText(s store.Status) string {
	switch s {
	case store.StatusBlocked:
		return "held back"
	case store.StatusAlerted:
		return "let through"
	}
	return "everything"
}

// cycleShow (v) goes through everything, only what shh held back and only what it let through, on the two notification tabs. Held back
// is the one to look at for notifications that should have reached you: they never made a banner, so this is the only place to say so.
func (m *Model) cycleShow() {
	if m.listless() {
		return
	}
	switch m.show {
	case "":
		m.show = store.StatusBlocked
	case store.StatusBlocked:
		m.show = store.StatusAlerted
	default:
		m.show = ""
	}
	m.collapse()
	m.reload()
	switch m.show {
	case store.StatusBlocked:
		m.notice = "showing only what shh held back: n on one that should have reached you (v goes on, Esc shows everything)"
	case store.StatusAlerted:
		m.notice = "showing only what shh let through (v goes on, Esc shows everything)"
	default:
		m.notice = "showing everything"
	}
}

// filterRows keeps the notifications whose sender, app, title, subtitle or message contains the text, ignoring case.
func filterRows(rows []store.Notification, text string) []store.Notification {
	needle := strings.ToLower(text)
	var out []store.Notification
	for _, n := range rows {
		for _, field := range []string{n.Sender, n.Source, n.Title, n.Subtitle, n.Body} {
			if strings.Contains(strings.ToLower(field), needle) {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// maxCount bounds a typed count to four digits.
const maxCount = 9999

// countDigit says whether the key is a digit and which.
func countDigit(key string) (int, bool) {
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		return int(key[0] - '0'), true
	}
	return 0, false
}

// jumpTo goes to a row (counting from 0), or, in the expanded view, to a line of the text.
func (m *Model) jumpTo(row int) {
	if m.tab == TabFocus {
		m.focusCursor = max(0, min(row, len(m.focusItems())-1))
		return
	}
	if m.tab == TabRoutines {
		m.routineCursor = max(0, min(row, len(m.saved)-1))
		return
	}
	if m.expanded() {
		m.scroll = max(0, row)
		m.scrollBy(0)
		return
	}
	m.cursor[m.tab] = row
	m.clamp(m.tab)
}

// move changes the selected row by delta, staying inside the list.
func (m *Model) move(delta int) {
	if m.tab == TabFocus {
		m.focusCursor = max(0, min(m.focusCursor+delta, len(m.focusItems())-1)) // the Focus tab's rows are its menu
		return
	}
	if m.tab == TabRoutines {
		m.routineCursor = max(0, min(m.routineCursor+delta, len(m.saved)-1))
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
	room := max(0, m.bodyHeight()-len(meta)-1) // one line is kept for the footer
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

func (m *Model) listWidth() int { return max(34, m.width*50/100) }

var (
	// Colours come only from the terminal's own palette (ANSI colours 0 to 15, bold, faint and reverse), never fixed RGB values, so the
	// UI takes on whatever theme the terminal is using, light or dark.
	blockedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true) // yellow in most themes: needs a look
	alertedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green: it got through
	unreadStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	lineNumberStyle = lipgloss.NewStyle().Faint(true)
	// flaggedStyle marks a notification the user flagged from its alert to look at again: magenta and bold in most themes.
	flaggedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	// inputStyle is the box a label is typed in. It is drawn in normal video inside the reversed bar, so it stands out from the text
	// around it whatever the theme, and in yellow and underlined so it reads as a place to type.
	inputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true).Underline(true)
	tabStyle    = lipgloss.NewStyle().Padding(0, 1)
	activeTab   = tabStyle.Bold(true).Reverse(true)
	dim         = lipgloss.NewStyle().Faint(true)
	bold        = lipgloss.NewStyle().Bold(true)
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
	case m.tab == TabRoutines:
		body = m.routinesView()
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
			if flagged := m.flaggedCount(); flagged > 0 {
				label += fmt.Sprintf(" (%d, %d ⚑)", len(m.lists[t]), flagged)
			} else {
				label += fmt.Sprintf(" (%d)", len(m.lists[t]))
			}
		}
		if t == m.tab {
			parts = append(parts, activeTab.Render(label))
		} else {
			parts = append(parts, tabStyle.Render(label))
		}
	}
	bar := strings.Join(parts, "")
	if m.show != "" {
		bar += dim.Render(" · " + showText(m.show) + " only")
	}
	if m.filter != "" {
		bar += dim.Render(" / " + oneLine(m.filter))
	}
	return fit(bar, m.width)
}

// flaggedCount is how many notifications on the Train list are flagged.
func (m *Model) flaggedCount() int {
	n := 0
	for _, row := range m.lists[TabTrain] {
		if row.IsFlagged {
			n++
		}
	}
	return n
}

func (m *Model) separator() string {
	return dim.Render(strings.TrimRight(strings.Repeat("│\n", m.bodyHeight()), "\n"))
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
		lines = append(lines, fit(m.lineNumber(i)+m.row(rows[i], w, i == m.cursor[m.tab]), w))
	}
	return pad(lines, m.bodyHeight(), w)
}

// Column widths of a list row: time, source, then the sender takes what is left, then the score and the blocked mark.
// gutterWidth is the room for the line numbers beside each row: three digits and a space.
const gutterWidth = 4

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
	return fmt.Sprintf("%*s   %-*s%-*s%-*s%*s  %s", gutterWidth, "", colTime, "time", colSource, "app", senderWidth(w), "from", colScore, "score", "held back")
}

func senderWidth(w int) int { return max(6, w-gutterWidth-3-colTime-colSource-colScore-2-colStatus) }

// row writes one notification as a line of the list, in the terminal's own colours. The selected row is drawn in reverse video, so
// each piece is styled with that already, instead of nesting styles inside a reversed line.
func (m *Model) row(n store.Notification, w int, selected bool) string {
	cell := func(style lipgloss.Style, text string) string { return style.Reverse(selected).Render(text) }
	plain := lipgloss.NewStyle().Bold(selected)
	if n.IsFlagged {
		plain = flaggedStyle // the whole row stands out, not just a mark in it
	}

	// Three marker columns: ">" for the selected row (as well as its highlight, for terminals without colour), "•" for one not yet
	// opened and "⚑" for one the user flagged to look at again.
	marker := " "
	if selected {
		marker = ">"
	}
	unread := " "
	if !n.IsRead {
		unread = "•"
	}
	flag, flagStyle := " ", plain
	if n.IsFlagged {
		flag, flagStyle = "⚑", flaggedStyle
	}
	status := strings.Repeat(" ", colStatus)
	statusStyle := plain
	if n.Status == store.StatusBlocked {
		status = fmt.Sprintf("%-*s", colStatus, blockedMark)
		statusStyle = blockedStyle
	}
	return cell(plain, marker) + cell(unreadStyle, unread) + cell(flagStyle, flag) +
		cell(plain, fmt.Sprintf("%-*s%-*s%-*s%*d  ", colTime, m.when(n.Timestamp), colSource, runewidth.Truncate(oneLine(n.Source), colSource-1, "…"),
			senderWidth(w), runewidth.Truncate(oneLine(n.Sender), senderWidth(w)-1, "…"), colScore, score(n.Score))) +
		cell(statusStyle, status)
}

// lineNumber is the gutter beside row i, as in vim with number and relativenumber both on: the cursor's row shows its own number
// (counting from 1) and every other row shows how far it is from the cursor, which is what a count like 6j needs.
func (m *Model) lineNumber(i int) string {
	cursor := m.cursor[m.tab]
	if i == cursor {
		return lineNumberStyle.Bold(true).Render(fmt.Sprintf("%-*d ", gutterWidth-1, min(i+1, 999)))
	}
	return lineNumberStyle.Render(fmt.Sprintf("%*d ", gutterWidth-1, min(abs(i-cursor), 999)))
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
	room := max(0, m.bodyHeight()-len(meta)) // on a very small terminal the top of the pane can fill it all
	lines := append([]string{}, meta...)
	switch {
	case expanded:
		room = max(0, room-1) // the footer
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

// trainQuestion is what the Train tab's detail pane shows under the score, on two lines to fit the pane: the question, with what shh
// did with this notification, and the keys that answer it, so none of it needs working out.
func trainQuestion(n store.Notification) []string {
	did := "shh held it back"
	if n.Status == store.StatusAlerted {
		did = "shh alerted you"
	}
	return []string{
		lipgloss.NewStyle().Bold(true).Render("Was this correct?") + dim.Render(" "+did),
		dim.Render("y yes · n no · t type a score"),
	}
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
	if m.tab == TabTrain && !n.IsTrained {
		for _, line := range trainQuestion(n) {
			meta = append(meta, " "+line)
		}
	}
	if n.IsFlagged {
		meta = append(meta, " "+flaggedStyle.Render("⚑ flagged for review")+dim.Render(" (f clears it; labelling it clears it too)"))
	}
	if n.Reason != "" {
		meta = append(meta, " "+dim.Render("why: "+oneLine(n.Reason)))
	}
	if text := firstNonEmpty(n.Summary, m.summaries[n.ID]); text != "" {
		meta = append(meta, " "+dim.Render("summary ("+m.cfg.SummarizerName+"): ")+oneLine(text))
	} else if m.summarizing[n.ID] {
		meta = append(meta, " "+dim.Render("summarizing…"))
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
	// "? help" is always shown. When the line is too narrow the counts go first, then the session text is shortened.
	right := "? help · q quit "
	if !m.listless() {
		right = "v view · " + right // all, only what was held back, only what was let through
	}
	room := max(0, m.width-lipgloss.Width(right))
	counts := fmt.Sprintf("%d notifications · %d to train", len(m.lists[TabNotifications]), len(m.lists[TabTrain]))
	if m.filter != "" {
		counts = fmt.Sprintf("%d of %d notifications · %d of %d to train", len(m.lists[TabNotifications]), m.totals[TabNotifications],
			len(m.lists[TabTrain]), m.totals[TabTrain])
	}
	if m.cfg.SummarizerRemote {
		counts += " · " + m.cfg.SummarizerName + " summaries: text leaves this Mac"
	}
	left := " " + m.sessionText() + "  │  " + counts
	if lipgloss.Width(left) > room {
		left = " " + m.sessionText()
	}
	left = fit(left, room)
	gap := max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return statusStyle.Render(left + strings.Repeat(" ", gap) + right)
}

// messageLine is the second line of the footer, where anything to say goes so that it is never squeezed into the state line: what
// the last action did, a problem, what is wrong with the label being typed, or, when there is nothing to report, the keys that
// apply here.
func (m *Model) messageLine() string {
	var text, right string
	var style lipgloss.Style
	switch {
	case m.count > 0 && m.prompt == nil:
		text, style = fmt.Sprintf("%d: j / k move that many rows, G goes to that row, Esc cancels", m.count), errStyle
	case m.prompt != nil && m.prompt.err != "":
		text, style = "✗ "+oneLine(m.prompt.err), errStyle
	case m.prompt != nil:
		text, style = m.prompt.hint(), dim
	case m.err != "":
		text, style = m.err, errStyle
	case m.notice != "":
		text, style = oneLine(m.notice), errStyle
		if m.noticeHasCmd {
			right = commandText(m.noticeCmd)
		}
	default:
		text, style = m.keyHints(), dim
		if items := m.focusItems(); m.tab == TabFocus && m.focusCursor < len(items) {
			right = commandText(items[m.focusCursor].cmd) // what the selected row is, as a command, so the menu teaches the command line
		}
		if m.trigForm != nil {
			right = "" // the form shows its trigger as words itself
		} else if m.editor != nil {
			right = commandText(addCommand(m.editor.draft, m.editor.original != ""))
		} else if m.tab == TabRoutines && m.routineCursor < len(m.saved) {
			right = commandText("shh routine run " + m.saved[m.routineCursor].Name)
		}
	}
	return style.Render(withRight(" "+text, right, m.width))
}

// withRight lays a line out with its main text at the left and a second piece, the equivalent command, flush to the right edge. The
// command is kept whole when it can be; the main text gives way first.
func withRight(left, right string, width int) string {
	if right == "" {
		return fit(left, width)
	}
	right += " "
	if lipgloss.Width(right)+4 > width {
		return fit(strings.TrimSpace(right), width)
	}
	left = fit(left, width-lipgloss.Width(right)-2)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	return left + strings.Repeat(" ", max(2, gap)) + right
}

// keyHints are the keys that work on the current tab, briefly and all of them: the help screen (?, shown at the right of the line above)
// explains each in full. The keys that work everywhere, ? and q, are at the right of the state line instead.
func (m *Model) keyHints() string {
	hints := m.tabHints()
	// s is in the hints only when there is a summarizer to use; the help always lists it.
	if m.cfg.Summarize != nil && m.cfg.SummarizerName != "" && m.cfg.SummarizerName != "none" && (m.tab == TabNotifications || m.tab == TabTrain) {
		hints = strings.Replace(hints, "e read · ", "e read · s summary · ", 1)
	}
	return hints
}

func (m *Model) tabHints() string {
	switch m.tab {
	case TabFocus:
		return "j/k select · Enter run it · or its key: s b f p + - x X · Tab switch"
	case TabRoutines:
		if m.history != nil {
			return "j/k select a run · R review what was flagged (the Train tab) · H or Esc back to the routines"
		}
		if m.trigForm != nil {
			return triggerFormHint(m.trigForm)
		}
		if m.editor != nil {
			return "j/k move · Enter change · a add · d delete · J/K reorder steps · s save · Esc cancel"
		}
		return "j/k select · Enter start · n next · b back · x stop · a new · e edit · c copy · d delete · H history"
	case TabTrain:
		if m.show == store.StatusBlocked {
			return "held back: n = it should have reached me · y = right call · t score · v next view · f flag · Esc all"
		}
		return "j/k move · / filter · y/n right/wrong · t score · u undo · f flag · e read · o open · d dismiss"
	}
	if m.show == store.StatusBlocked {
		return "held back: n = it should have reached me · y = right call · t score · v next view · f flag · Esc all"
	}
	return "j/k move · / filter · e read · o open · d dismiss · f flag · y/n right/wrong · t score · u undo"
}

// question is what the prompt asks, at the left of the bar.
func (p *prompt) question() string {
	switch p.kind {
	case promptStart:
		return " start focus: "
	case promptBaseline:
		return " baseline: "
	case promptLevel:
		return " change level: "
	case promptFilter:
		return " filter: "
	case promptConfirmStopAll:
		return " turn focus off completely, baseline included? "
	case promptConfirmEndRoutine:
		if p.canContinue {
			return " a routine is running: "
		}
		return " a routine is running. End it and do this? "
	case promptConfirmDelete:
		return " delete this routine? "
	case promptEditField:
		return " " + p.label + ": "
	case promptHelpSearch:
		return " search the guide: "
	}
	return fmt.Sprintf(" label for %s: ", oneLine(p.target.Sender))
}

// hint is the line under the bar while the prompt is open: how to answer it.
func (p *prompt) hint() string {
	switch p.kind {
	case promptStart:
		return "e.g. 75% 25m writing docs · then off at the end sets what follows · Enter starts · Esc cancels"
	case promptBaseline:
		return "a percentage like 30% or a tier; off clears the baseline · Enter sets · Esc cancels"
	case promptLevel:
		return "the new level for the session in force: a percentage or a tier · Enter changes it · Esc cancels"
	case promptFilter:
		return "text to find in the sender, app, title or message · Enter applies (empty clears) · Esc cancels"
	case promptConfirmStopAll:
		return "y turns it all off · n or Esc keeps it"
	case promptConfirmEndRoutine:
		if p.canContinue {
			return "y: end the routine and start this · c: interrupt it, it continues after this ends · q: start this after the routine ends · n: leave it"
		}
		return "y ends the routine and goes on · n or Esc leaves it running"
	case promptConfirmDelete:
		return "y deletes it from the routines file · n or Esc keeps it"
	case promptEditField:
		return p.help
	case promptHelpSearch:
		return "text to find (capitals make it exact) · Enter searches · n and N then go to the next and previous match · Esc cancels"
	}
	return "Enter saves · Esc cancels · a score from 0 to 100, or light, medium, deep or lockdown"
}

// promptBar is the state line while an exact label is being typed: the question in the bar's own reverse video, then the box the
// label is typed in (yellow, underlined, with a blinking block cursor). The hint and any error are on the line below.
func (m *Model) promptBar() string {
	p := m.prompt
	question := p.question()
	if p.kind == promptConfirmStopAll || p.kind == promptConfirmEndRoutine || p.kind == promptConfirmDelete {
		field := " y / n "
		question = fit(question, max(0, m.width-lipgloss.Width(field)))
		fill := strings.Repeat(" ", max(0, m.width-lipgloss.Width(question)-lipgloss.Width(field)))
		return statusStyle.Render(question) + inputStyle.Render(field) + statusStyle.Render(fill)
	}
	// The box takes what the question leaves it, at least the room that is left after a third of the line; a text longer than that
	// scrolls so the cursor stays in view.
	questionRoom := min(lipgloss.Width(question), m.width/3)
	before, under, after := p.window(m.width - questionRoom - 2)
	box := inputStyle.Render(" "+before) + inputStyle.Render(after+" ")
	switch {
	case under == "" && m.cursorOn:
		box = inputStyle.Render(" "+before) + inputStyle.Render("█") + inputStyle.Render(" ")
	case under == "":
		box = inputStyle.Render(" " + before + " ")
	case m.cursorOn:
		box = inputStyle.Render(" "+before) + inputStyle.Reverse(true).Render(under) + inputStyle.Render(after+" ")
	default:
		box = inputStyle.Render(" " + before + under + after + " ")
	}
	question = fit(question, max(0, m.width-lipgloss.Width(box)))
	fill := strings.Repeat(" ", max(0, m.width-lipgloss.Width(question)-lipgloss.Width(box)))
	return statusStyle.Render(question) + box + statusStyle.Render(fill)
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

func firstNonEmpty(texts ...string) string {
	for _, t := range texts {
		if t != "" {
			return t
		}
	}
	return ""
}
