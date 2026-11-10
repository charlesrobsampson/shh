package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

var now = time.Date(2026, 11, 9, 14, 30, 0, 0, time.UTC)

func init() {
	lipgloss.SetColorProfile(termenv.Ascii) // plain text, so views can be compared as strings
}

type fakeWorker struct {
	view session.StatusView
	err  error
}

func (w *fakeWorker) Status() (session.StatusView, error) { return w.view, w.err }

// fakeTrainer stands in for the worker's training: it records what it was asked and answers as programmed.
type fakeTrainer struct {
	requests  []session.TrainRequest
	undos     int
	err       error
	undoErr   error
	outcome   session.TrainView
	lastTrain int64
	// onTrain and onUndo stand in for the worker's side effects on the stored row, which happen before it answers.
	onTrain func(session.TrainRequest)
	onUndo  func(id int64)
}

func (f *fakeTrainer) Train(req session.TrainRequest) (session.TrainView, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return session.TrainView{}, f.err
	}
	out := f.outcome
	out.NotificationID = req.NotificationID
	f.lastTrain = req.NotificationID
	if f.onTrain != nil {
		f.onTrain(req)
	}
	return out, nil
}

func (f *fakeTrainer) UndoTraining() (session.TrainView, error) {
	f.undos++
	if f.undoErr != nil {
		return session.TrainView{}, f.undoErr
	}
	if f.onUndo != nil {
		f.onUndo(f.lastTrain)
	}
	return session.TrainView{NotificationID: f.lastTrain}, nil
}

// fakeControl stands in for the worker's focus commands: it records each call as text and applies it to a status it keeps, as the
// worker would, answering with that status. err, if set, is returned by every call.
type fakeControl struct {
	calls  []string
	status session.StatusView
	err    error
}

func (f *fakeControl) answer(call string) (session.StatusView, error) {
	f.calls = append(f.calls, call)
	if f.err != nil {
		return session.StatusView{}, f.err
	}
	return f.status, nil
}

func (f *fakeControl) Baseline(level int) (session.StatusView, error) {
	return f.answer(fmt.Sprintf("baseline %d", level))
}
func (f *fakeControl) Focus(level int, d time.Duration, then *int, note string) (session.StatusView, error) {
	t := "-"
	if then != nil {
		t = fmt.Sprint(*then)
	}
	return f.answer(fmt.Sprintf("focus %d %v then=%s note=%q", level, d, t, note))
}
func (f *fakeControl) SetLevel(level int) (session.StatusView, error) {
	return f.answer(fmt.Sprintf("level %d", level))
}
func (f *fakeControl) Stop(all bool) (session.StatusView, error) {
	return f.answer(fmt.Sprintf("stop all=%v", all))
}
func (f *fakeControl) Pause() (session.StatusView, error)  { return f.answer("pause") }
func (f *fakeControl) Resume() (session.StatusView, error) { return f.answer("resume") }
func (f *fakeControl) Adjust(d time.Duration) (session.StatusView, error) {
	return f.answer("adjust " + d.String())
}

type rig struct {
	t       *testing.T
	store   *store.Store
	worker  *fakeWorker
	m       *Model
	next    int64
	opened  []string
	openErr error
	trainer *fakeTrainer
	control *fakeControl
	ensured int
	ensureE error
}

func newRig(t *testing.T) *rig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := &rig{t: t, store: s, worker: &fakeWorker{err: session.ErrNotRunning}}
	r.trainer = &fakeTrainer{outcome: session.TrainView{Before: 42, After: 71, Updated: true}}
	r.trainer.onTrain = func(req session.TrainRequest) {
		_ = s.Train(req.NotificationID, store.Training{Feedback: req.Feedback, Label: req.Label})
	}
	r.trainer.onUndo = func(id int64) { _ = s.Untrain(id) }
	r.control = &fakeControl{}
	r.m = New(Config{Data: s, Worker: r.worker, Now: func() time.Time { return now }, Trainer: r.trainer, Control: r.control,
		Ensure: func() error { r.ensured++; return r.ensureE },
		Open: func(bundle string) error {
			r.opened = append(r.opened, bundle)
			return r.openErr
		}})
	return r
}

// add stores a notification, minutes before now, and returns its id.
func (r *rig) add(sender, body string, minutesAgo int, mut ...func(*store.Notification)) int64 {
	r.t.Helper()
	r.next++
	threshold := 65
	n := store.Notification{
		Source: "Teams", Sender: sender, Title: sender, Body: body, Timestamp: now.Add(-time.Duration(minutesAgo) * time.Minute),
		Status: store.StatusBlocked, Score: 0.42, Boost: 0.1, Reason: "keyword: down", BundleID: "com.microsoft.teams2",
		SourceRecID: r.next, ThresholdAtDecision: &threshold,
	}
	for _, f := range mut {
		f(&n)
	}
	id, inserted, err := r.store.InsertNotification(n)
	if err != nil || !inserted {
		r.t.Fatalf("insert: %v %v", inserted, err)
	}
	return id
}

func (r *rig) refresh() { r.m.Update(tickMsg(now)) }

func (r *rig) press(keys ...string) {
	r.t.Helper()
	for _, k := range keys {
		r.m.Update(key(k))
	}
}

func key(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "f1":
		return tea.KeyMsg{Type: tea.KeyF1}
	case "f2":
		return tea.KeyMsg{Type: tea.KeyF2}
	case "f3":
		return tea.KeyMsg{Type: tea.KeyF3}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	case "alt+left":
		return tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	case "alt+right":
		return tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func (r *rig) view() string { return r.m.View() }

func (r *rig) selectedSender() string {
	n, ok := r.m.Selected()
	if !ok {
		return ""
	}
	return n.Sender
}

func TestTabsSwitchWithTabShiftTabAndFunctionKeys(t *testing.T) {
	r := newRig(t)
	if r.m.Tab() != TabNotifications {
		t.Fatalf("first tab = %v, want Notifications: that is what you open shh to see", r.m.Tab())
	}
	for _, step := range []struct {
		keys []string
		want Tab
	}{
		{[]string{"tab"}, TabTrain},
		{[]string{"tab"}, TabRoutines},
		{[]string{"tab"}, TabFocus},
		{[]string{"tab"}, TabNotifications},
		{[]string{"shift+tab"}, TabFocus},
		{[]string{"shift+tab"}, TabRoutines},
		{[]string{"f1"}, TabFocus},
		{[]string{"f3"}, TabTrain},
		{[]string{"f4"}, TabRoutines},
		{[]string{"f2"}, TabNotifications},
		{[]string{"right"}, TabTrain},
		{[]string{"left"}, TabNotifications},
		{[]string{"l"}, TabTrain},
		{[]string{"h"}, TabNotifications},
		{[]string{"h"}, TabFocus},
		{[]string{"h"}, TabRoutines},
		{[]string{"l"}, TabFocus},
		{[]string{"l"}, TabNotifications},
	} {
		r.press(step.keys...)
		if r.m.Tab() != step.want {
			t.Fatalf("after %v the tab is %v, want %v", step.keys, r.m.Tab(), step.want)
		}
	}
	for _, name := range []string{"Focus", "Notifications", "Train", "Routines"} {
		if !strings.Contains(r.view(), name) {
			t.Errorf("the tab bar does not show %q:\n%s", name, r.view())
		}
	}
}

func TestDigitKeysAreLeftFreeForTrainingLabels(t *testing.T) {
	r := newRig(t)
	r.press("1", "2", "3")
	if r.m.Tab() != TabNotifications {
		t.Errorf("a digit changed the tab to %v: digits are reserved for labels", r.m.Tab())
	}
}

func TestNotificationsShowsOnlyWhatIsNotDismissedAndTrainOnlyWhatIsUntrained(t *testing.T) {
	r := newRig(t)
	plain := r.add("Plain", "x", 1)
	dismissed := r.add("Dismissed", "x", 2)
	trained := r.add("Trained", "x", 3)
	both := r.add("Both", "x", 4)
	must(t, r.store.SetDismissed(dismissed, true))
	must(t, r.store.Train(trained, store.Training{Feedback: "y"}))
	must(t, r.store.SetDismissed(both, true))
	must(t, r.store.Train(both, store.Training{Feedback: "n"}))
	_ = plain
	r.refresh()

	r.press("f2")
	got := r.view()
	if !strings.Contains(got, "Plain") || !strings.Contains(got, "Trained") || strings.Contains(got, "Dismissed") || strings.Contains(got, "Both") {
		t.Errorf("Notifications should list Plain and Trained only:\n%s", got)
	}
	r.press("f3")
	got = r.view()
	if !strings.Contains(got, "Plain") || !strings.Contains(got, "Dismissed") || strings.Contains(got, "Trained") || strings.Contains(got, "Both") {
		t.Errorf("Train should list Plain and Dismissed only (rows hidden from one list stay in the other):\n%s", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRowsShowTimeSourceSenderScoreAndWhetherTheyWereAlerted(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 5, func(n *store.Notification) { n.Score = 0.77; n.Status = store.StatusAlerted })
	r.add("Bo", "y", 60*24*3, func(n *store.Notification) { n.Source = "Outlook" })
	r.refresh()
	view := r.view()
	for _, want := range []string{"14:25", "Teams", "Ana", "77", "alerted", "Nov 6", "Outlook", "Bo", "42", "blocked"} {
		if !strings.Contains(view, want) {
			t.Errorf("list is missing %q:\n%s", want, view)
		}
	}
}

func TestNavigationStaysInsideTheList(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 5; i++ {
		r.add(fmt.Sprintf("Sender%d", i), "x", i+1)
	}
	r.refresh()
	r.press("k", "k", "up")
	if got := r.selectedSender(); got != "Sender0" {
		t.Errorf("moving up from the top selected %q, want to stay on Sender0", got)
	}
	r.press("j", "j", "down")
	if got := r.selectedSender(); got != "Sender3" {
		t.Errorf("selected %q after three moves down, want Sender3", got)
	}
	r.press("j", "j", "j", "j", "j")
	if got := r.selectedSender(); got != "Sender4" {
		t.Errorf("moving past the end selected %q, want to stay on Sender4", got)
	}
	r.press("g")
	if got := r.selectedSender(); got != "Sender0" {
		t.Errorf("g selected %q, want the first row", got)
	}
	r.press("G")
	if got := r.selectedSender(); got != "Sender4" {
		t.Errorf("G selected %q, want the last row", got)
	}
}

func TestTheListScrollsToKeepTheSelectedRowInView(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 40; i++ {
		r.add(fmt.Sprintf("Sender%02d", i), "x", i+1)
	}
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	r.refresh()
	for i := 0; i < 25; i++ {
		r.press("j")
	}
	view := r.view()
	if !strings.Contains(view, "Sender25") {
		t.Errorf("the selected row Sender25 is not on screen:\n%s", view)
	}
	if strings.Contains(view, "Sender00") {
		t.Errorf("the top of the list should have scrolled away:\n%s", view)
	}
	if h := lipgloss.Height(view); h > 12 {
		t.Errorf("the view is %d lines tall in a 12 line terminal", h)
	}
}

func TestEveryLineOfTheViewFitsTheTerminal(t *testing.T) {
	r := newRig(t)
	r.add("A very long sender name indeed", strings.Repeat("a long message body ", 40), 1)
	for _, size := range [][2]int{{80, 24}, {60, 15}, {120, 40}} {
		r.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, tab := range []string{"f1", "f2", "f3"} {
			r.press(tab)
			view := r.view()
			if h := lipgloss.Height(view); h != size[1] {
				t.Errorf("%dx%d %s: view is %d lines tall", size[0], size[1], tab, h)
			}
			for i, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w > size[0] {
					t.Errorf("%dx%d %s: line %d is %d wide: %q", size[0], size[1], tab, i, w, line)
				}
			}
		}
	}
}

func TestTheDetailPaneShowsTheSelectedNotification(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "the server is down again", 1, func(n *store.Notification) {
		n.Reason = "keyword: down"
		n.Boost = 0.15
		n.Score = 0.5
		n.Status = store.StatusAlerted
		n.Title = "Incident"
	})
	r.refresh()
	view := r.view()
	for _, want := range []string{"Ana — Incident", "the server is down again", "score 50", "+15 boost", "focus was 65%", "why: keyword: down", "alerted"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail pane is missing %q:\n%s", want, view)
		}
	}
	r.press("f1")
	if strings.Contains(r.view(), "the server is down again") {
		t.Error("the Focus tab should not show notification details")
	}
}

func TestTheStatusBarShowsTheSessionAndTheTimeLeft(t *testing.T) {
	r := newRig(t)
	if got := r.view(); !strings.Contains(got, "shh is not running") {
		t.Errorf("with no worker the status bar should say so:\n%s", got)
	}
	r.worker.err = nil
	r.worker.view = session.StatusView{Level: 65, Layers: []session.LayerView{
		{Kind: "baseline", Percent: 30},
		{Kind: "escalation", Percent: 65, Timed: true, RemainingSecs: 28 * 60, Blocked: 7, Allowed: 2},
	}}
	r.refresh()
	got := r.view()
	if !strings.Contains(got, "shh 28m · 65%") || !strings.Contains(got, "7 held back, 2 let through") {
		t.Errorf("status bar should show the time left, level and counts:\n%s", got)
	}
	r.worker.view = session.StatusView{}
	r.refresh()
	if got := r.view(); !strings.Contains(got, "focus off") {
		t.Errorf("status bar should say focus is off:\n%s", got)
	}
	r.worker.err = errors.New("boom")
	r.refresh()
	if got := r.view(); !strings.Contains(got, "shh is not running") {
		t.Errorf("a worker that cannot be asked counts as not running:\n%s", got)
	}
}

func TestTheStatusBarCountsWhatTheListsHold(t *testing.T) {
	r := newRig(t)
	a := r.add("A", "x", 1)
	r.add("B", "x", 2)
	must(t, r.store.Train(a, store.Training{Feedback: "y"}))
	r.refresh()
	if got := r.view(); !strings.Contains(got, "2 notifications · 1 to train") {
		t.Errorf("counts are wrong:\n%s", got)
	}
}

func TestTheFocusTabShowsTheStackOrSaysThereIsNone(t *testing.T) {
	r := newRig(t)
	r.press("f1")
	if got := r.view(); !strings.Contains(got, "shh is not running") {
		t.Errorf("Focus tab without a worker:\n%s", got)
	}
	r.worker.err = nil
	r.worker.view = session.StatusView{Level: 65, Layers: []session.LayerView{
		{Kind: "baseline", Percent: 30},
		{Kind: "escalation", Percent: 65, Timed: true, RemainingSecs: 600},
	}}
	r.refresh()
	got := r.view()
	for _, want := range []string{"Focus is on at 65% (medium)", "▶", "escalation", "10m left", "then back to baseline 30%", "baseline"} {
		if !strings.Contains(got, want) {
			t.Errorf("Focus tab should show %q:\n%s", want, got)
		}
	}
}

func TestTheSelectionFollowsTheSameNotificationWhenNewOnesArrive(t *testing.T) {
	r := newRig(t)
	r.add("Old1", "x", 10)
	r.add("Old2", "x", 11)
	r.refresh()
	r.press("j")
	if r.selectedSender() != "Old2" {
		t.Fatalf("selected %q", r.selectedSender())
	}
	r.add("Fresh", "x", 0)
	r.refresh()
	if got := r.selectedSender(); got != "Old2" {
		t.Errorf("selected %q after a new row arrived above, want the cursor to stay on Old2", got)
	}
}

func TestWhenTheSelectedRowDisappearsTheCursorStaysInRange(t *testing.T) {
	r := newRig(t)
	a := r.add("A", "x", 1)
	b := r.add("B", "x", 2)
	r.refresh()
	r.press("G")
	must(t, r.store.SetDismissed(b, true))
	r.refresh()
	if got := r.selectedSender(); got != "A" {
		t.Errorf("selected %q, want A: the last row went away", got)
	}
	must(t, r.store.SetDismissed(a, true))
	r.refresh()
	if _, ok := r.m.Selected(); ok {
		t.Error("a selection exists in an empty list")
	}
	if got := r.view(); !strings.Contains(got, "Nothing here yet") {
		t.Errorf("an empty list should say so:\n%s", got)
	}
	r.press("j", "k", "G", "g") // must not panic on an empty list
}

func TestHelpOpensJKScrollItAndAnyOtherKeyCloses(t *testing.T) {
	r := newRig(t)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	r.press("?")
	if got := r.view(); !strings.Contains(got, "shh keys") || !strings.Contains(got, "Moving around") {
		t.Errorf("help:\n%s", got)
	}
	r.press("j", "j", "j")
	if r.m.helpScroll != 3 || !r.m.showHelp {
		t.Errorf("scroll %d open %v: j should scroll the help, not close it", r.m.helpScroll, r.m.showHelp)
	}
	r.press("k")
	if r.m.helpScroll != 2 {
		t.Errorf("scroll = %d after k", r.m.helpScroll)
	}
	r.press("G")
	if got := r.view(); !strings.Contains(got, "Quit.") {
		t.Errorf("G should scroll to the end of the help:\n%s", got)
	}
	r.press("g")
	if r.m.helpScroll != 0 {
		t.Errorf("g should scroll back to the top, scroll = %d", r.m.helpScroll)
	}
	r.press("x")
	if got := r.view(); strings.Contains(got, "shh keys") || r.m.showHelp {
		t.Errorf("another key should close the help:\n%s", got)
	}
	r.press("?")
	if r.m.helpScroll != 0 {
		t.Errorf("the help should open at the top, not where it was left (scroll %d)", r.m.helpScroll)
	}
}

func TestQuitKeysQuit(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		r := newRig(t)
		_, cmd := r.m.Update(key(k))
		if cmd == nil || fmt.Sprintf("%T", cmd()) != "tea.QuitMsg" {
			t.Errorf("%s did not quit", k)
		}
	}
	r := newRig(t)
	r.press("?")
	if _, cmd := r.m.Update(key("q")); cmd == nil {
		t.Error("q should quit even from the help screen")
	}
}

func TestARefreshTickReloadsAndSchedulesTheNext(t *testing.T) {
	r := newRig(t)
	r.add("Late", "x", 1)
	_, cmd := r.m.Update(tickMsg(now))
	if cmd == nil {
		t.Error("no next tick was scheduled")
	}
	if !strings.Contains(r.view(), "Late") {
		t.Error("the tick did not reload the lists")
	}
}

func TestADatabaseErrorIsShownNotFatal(t *testing.T) {
	r := newRig(t)
	r.store.Close()
	r.refresh()
	if got := r.view(); !strings.Contains(got, "cannot read notifications") {
		t.Errorf("the status bar should report the problem:\n%s", got)
	}
}

func TestTheUIWorksWithNoWorkerConfigured(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := New(Config{Data: s})
	if got := m.View(); !strings.Contains(got, "Notifications") {
		t.Errorf("view:\n%s", got)
	}
}

func TestTheSelectedRowIsMarkedAndOnlyBlockedRowsCarryTheBlockedMark(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1, func(n *store.Notification) { n.Status = store.StatusAlerted })
	r.add("Bo", "x", 2)
	r.refresh()
	// Only the list column (left of the separator) holds rows; the detail pane to its right repeats the sender's name.
	find := func(name string) string {
		for _, l := range strings.Split(r.view(), "\n") {
			left, _, found := strings.Cut(l, "│")
			if found && strings.Contains(left, name) && strings.Contains(left, "Teams") {
				return left
			}
		}
		return ""
	}
	// Each row starts with the line-number gutter; the markers follow it.
	afterGutter := func(l string) string { return l[min(len(l), gutterWidth):] }
	ana, bo := afterGutter(find("Ana")), afterGutter(find("Bo"))
	if !strings.HasPrefix(ana, ">") || strings.HasPrefix(bo, ">") {
		t.Errorf("the selected row should start with >: %q / %q", ana, bo)
	}
	if strings.Contains(ana, "✕") || strings.Contains(ana, "blocked") {
		t.Errorf("an alerted row should show nothing in the status column: %q", ana)
	}
	if !strings.Contains(bo, "✕ blocked") {
		t.Errorf("a blocked row should carry the mark: %q", bo)
	}
	r.press("j")
	if ana, bo = afterGutter(find("Ana")), afterGutter(find("Bo")); strings.HasPrefix(ana, ">") || !strings.HasPrefix(bo, ">") {
		t.Errorf("the > should follow the cursor: %q / %q", ana, bo)
	}
	if got := r.view(); !strings.Contains(got, "held back") {
		t.Errorf("the status column needs a header:\n%s", got)
	}
}

func TestTheDetailPaneNamesTheStatusInFull(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1, func(n *store.Notification) { n.Status = store.StatusAlerted })
	r.refresh()
	if got := r.view(); !strings.Contains(got, "alerted") {
		t.Errorf("detail pane:\n%s", got)
	}
	r.add("Bo", "x", 0)
	r.refresh()
	if got := r.view(); !strings.Contains(got, "blocked") {
		t.Errorf("detail pane:\n%s", got)
	}
}

func TestColoursComeOnlyFromTheTerminalsOwnPalette(t *testing.T) {
	// With colour on, the escape codes must be the basic 16 colours (30 to 37, 90 to 97) that every terminal theme redefines, and never
	// 256-colour or true colour codes, which would ignore the theme.
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	r := newRig(t)
	r.add("Ana", "x", 1, func(n *store.Notification) { n.Status = store.StatusAlerted })
	r.add("Bo", "x", 2)
	r.worker.err = nil
	r.worker.view = session.StatusView{Level: 65, Layers: []session.LayerView{{Kind: "escalation", Percent: 65}}}
	r.refresh()
	for _, tab := range []string{"f1", "f2", "f3"} {
		r.press(tab)
		view := r.view()
		if strings.Contains(view, "38;2;") || strings.Contains(view, "38;5;") || strings.Contains(view, "48;2;") || strings.Contains(view, "48;5;") {
			t.Errorf("%s: the view uses a fixed colour instead of the terminal's palette: %q", tab, view)
		}
	}
	r.press("f2")
	view := r.view()
	if !strings.Contains(view, "33m") {
		t.Errorf("the blocked mark should be coloured (ANSI yellow):\n%q", view)
	}
	if !strings.Contains(view, "7m") && !strings.Contains(view, ";7;") && !strings.Contains(view, "[7") {
		t.Errorf("the selected row and active tab should use reverse video (theme-proof):\n%q", view)
	}
}

func TestAwkwardTextNeverMakesTheViewTallerThanTheTerminal(t *testing.T) {
	// A view one line too tall pushes the tab bar off the top of the screen.
	r := newRig(t)
	r.add("Line\nBreak", "multi\nline\r\nbody\twith\ttabs", 1, func(n *store.Notification) {
		n.Title = "Title\nwith\nbreaks"
		n.Source = "Te\tams"
		n.Reason = "why\nnot"
	})
	r.add("Escape\x1b[31mred\x1b[0m", "bell\x07 and \x1b]0;title\x07 and null\x00 and del\x7f", 2)
	r.add("Emoji 🎉👩‍💻 ✔️", strings.Repeat("emoji 🎉 and wide 漢字 text ", 20), 3)
	r.refresh()
	for _, size := range [][2]int{{80, 24}, {100, 14}, {60, 10}} {
		r.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, tab := range []string{"f2", "f3"} {
			r.press(tab)
			for i := 0; i < 3; i++ {
				view := r.view()
				if h := lipgloss.Height(view); h != size[1] {
					t.Errorf("%dx%d %s row %d: the view is %d lines tall:\n%s", size[0], size[1], tab, i, h, view)
				}
				if strings.ContainsAny(view, "\x1b\x07\x00\x7f\t\r") {
					t.Errorf("%dx%d %s row %d: the view contains control characters: %q", size[0], size[1], tab, i, view)
				}
				if !strings.HasPrefix(view, " ! Focus") && !strings.Contains(strings.SplitN(view, "\n", 2)[0], "Notifications") {
					t.Errorf("the first line is not the tab bar: %q", strings.SplitN(view, "\n", 2)[0])
				}
				r.press("j")
			}
		}
	}
}

func TestShiftedDigitsJumpStraightToATab(t *testing.T) {
	r := newRig(t)
	for _, step := range []struct {
		key  string
		want Tab
	}{{"!", TabFocus}, {"#", TabTrain}, {"@", TabNotifications}, {"!", TabFocus}, {"@", TabNotifications}} {
		r.press(step.key)
		if r.m.Tab() != step.want {
			t.Fatalf("after %q the tab is %v, want %v", step.key, r.m.Tab(), step.want)
		}
	}
	got := r.view()
	for _, want := range []string{"! Focus", "@ Notifications", "# Train"} {
		if !strings.Contains(got, want) {
			t.Errorf("the tab bar should show the key for each tab (%q):\n%s", want, got)
		}
	}
	r.press("?")
	if !strings.Contains(r.view(), "! @ #") {
		t.Errorf("help does not mention the shifted digits:\n%s", r.view())
	}
}

func TestEmojiJoinersAreDroppedSoWidthsAreSafe(t *testing.T) {
	r := newRig(t)
	r.add("Dev 👩\u200d💻 ✔\ufe0f", "text with a zero\u200bwidth space and a joiner 👨\u200d👩\u200d👧", 1)
	r.refresh()
	if got := r.view(); strings.ContainsAny(got, "\u200d\ufe0f\u200b") {
		t.Errorf("invisible joiners reached the screen: %q", got)
	}
}

func (r *rig) row(id int64) store.Notification {
	r.t.Helper()
	n, err := r.store.GetNotification(id)
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

const longText = "line one\nline two\nline three\nline four\nline five\nline six\nline seven\nline eight\nline nine\nline ten\n" +
	"line eleven\nline twelve\nline thirteen\nline fourteen\nline fifteen\nline sixteen\nline seventeen\nline eighteen\nline nineteen\nTHE END"

func TestExpandingNeverMarksTheNotificationRead(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", longText, 1)
	r.refresh()
	r.press("e")
	if !r.m.expanded() {
		t.Fatal("e did not expand the notification")
	}
	r.press("j", "j", "e")
	if r.m.expanded() {
		t.Error("a second e should collapse it")
	}
	r.press("e", "esc")
	if r.m.expanded() {
		t.Error("esc should collapse it")
	}
	if r.row(id).IsRead {
		t.Error("expanding and collapsing marked the notification read: only opening it does")
	}
	if len(r.opened) != 0 {
		t.Errorf("expanding opened an app: %v", r.opened)
	}
}

func TestEnterAndOOpenTheAppAndMarkItRead(t *testing.T) {
	for _, k := range []string{"enter", "o"} {
		r := newRig(t)
		id := r.add("Ana", "hello", 1)
		other := r.add("Bo", "hi", 2)
		r.refresh()
		if !strings.Contains(r.view(), "•") {
			t.Fatalf("%s: unread rows should be marked", k)
		}
		r.press(k)
		if len(r.opened) != 1 || r.opened[0] != "com.microsoft.teams2" {
			t.Fatalf("%s: opened %v, want the notification's app", k, r.opened)
		}
		if !r.row(id).IsRead || r.row(other).IsRead {
			t.Errorf("%s: read flags are %v and %v, want only the opened one read", k, r.row(id).IsRead, r.row(other).IsRead)
		}
		if got := r.view(); strings.Count(got, "•") != 1 {
			t.Errorf("%s: want one unread marker left (Bo), got view:\n%s", k, got)
		}
		if !strings.Contains(r.view(), "opened Teams") {
			t.Errorf("%s: the status bar should say what happened:\n%s", k, r.view())
		}
		r.press("j")
		if strings.Contains(r.view(), "opened Teams") {
			t.Errorf("%s: the notice should go at the next key", k)
		}
	}
}

func TestAFailedOpenDoesNotMarkItRead(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "hello", 1)
	r.refresh()
	r.openErr = errors.New("no such app")
	r.press("o")
	if r.row(id).IsRead {
		t.Error("marked read although the app did not open")
	}
	if got := r.view(); !strings.Contains(got, "could not open Teams: no such app") {
		t.Errorf("the failure should show in the status bar:\n%s", got)
	}
}

func TestANotificationWithNoAppCannotBeOpenedOrMarkedRead(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "hello", 1, func(n *store.Notification) { n.BundleID = "" })
	r.refresh()
	r.press("enter")
	if len(r.opened) != 0 || r.row(id).IsRead {
		t.Errorf("opened %v, read %v: with no app there is nothing to open", r.opened, r.row(id).IsRead)
	}
	if got := r.view(); !strings.Contains(got, "nothing to open") {
		t.Errorf("status bar:\n%s", got)
	}
}

func TestDismissingHidesFromNotificationsButNotFromTrain(t *testing.T) {
	for _, k := range []string{"d", "x"} {
		r := newRig(t)
		gone := r.add("Gone", "x", 1)
		stay := r.add("Stay", "x", 2)
		r.refresh()
		r.press(k)
		if !r.row(gone).IsDismissedInbox || r.row(stay).IsDismissedInbox {
			t.Fatalf("%s: dismissed flags wrong", k)
		}
		if got := r.view(); strings.Contains(got, "Gone") || !strings.Contains(got, "Stay") {
			t.Errorf("%s: Notifications should show Stay only:\n%s", k, got)
		}
		if r.selectedSender() != "Stay" {
			t.Errorf("%s: the cursor is on %q, want it on the row that took the dismissed one's place", k, r.selectedSender())
		}
		r.press("f3")
		if got := r.view(); !strings.Contains(got, "Gone") || !strings.Contains(got, "Stay") {
			t.Errorf("%s: Train should still list both (dismissing is not training):\n%s", k, got)
		}
		if r.row(gone).IsTrained {
			t.Errorf("%s: dismissing trained the notification", k)
		}
	}
}

func TestDismissingFromTheTrainTabKeepsTheRowThereToo(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "d")
	if !r.row(id).IsDismissedInbox {
		t.Error("d on the Train tab should dismiss it from Notifications")
	}
	if !strings.Contains(r.view(), "Ana") {
		t.Errorf("the row should stay on the Train tab:\n%s", r.view())
	}
	r.press("f2")
	if strings.Contains(r.view(), "Ana") {
		t.Errorf("it should be gone from Notifications:\n%s", r.view())
	}
}

func TestALongMessageIsCutToThePaneWithAHintAndFullyReadableExpanded(t *testing.T) {
	r := newRig(t)
	r.add("Ana", longText, 1)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	r.refresh()
	view := r.view()
	if h := lipgloss.Height(view); h != 14 {
		t.Errorf("the collapsed view is %d lines tall in a 14 line terminal", h)
	}
	if strings.Contains(view, "THE END") || !strings.Contains(view, "line one") {
		t.Errorf("the collapsed preview should show the start and cut the end:\n%s", view)
	}
	if !strings.Contains(view, "more lines (e to read it all)") {
		t.Errorf("a cut preview should say so:\n%s", view)
	}
	r.press("e")
	view = r.view()
	if h := lipgloss.Height(view); h != 14 {
		t.Errorf("the expanded view is %d lines tall", h)
	}
	if strings.Contains(view, "from  score") {
		t.Errorf("expanded should replace the list with the full-width message:\n%s", view)
	}
	for i := 0; i < 20; i++ {
		r.press("j")
	}
	view = r.view()
	if !strings.Contains(view, "THE END") {
		t.Errorf("scrolling down should reach the end of the message:\n%s", view)
	}
	if strings.Contains(view, "line one") {
		t.Errorf("the start should have scrolled off:\n%s", view)
	}
	if !strings.Contains(view, "of 20") {
		t.Errorf("the footer should say where we are:\n%s", view)
	}
	for i := 0; i < 30; i++ {
		r.press("k")
	}
	if !strings.Contains(r.view(), "line one") {
		t.Errorf("scrolling back up should return to the start:\n%s", r.view())
	}
}

func TestAShortMessageShowsInFullWithNoHint(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "just this", 1)
	r.refresh()
	if got := r.view(); !strings.Contains(got, "just this") || strings.Contains(got, "more lines") {
		t.Errorf("view:\n%s", got)
	}
}

func TestExpandedViewGoesAwayWithTheTabOrTheRow(t *testing.T) {
	r := newRig(t)
	a := r.add("Ana", longText, 1)
	r.add("Bo", "x", 2)
	r.refresh()
	r.press("e", "f3")
	if r.m.expanded() {
		t.Error("changing tab should collapse")
	}
	r.press("f2", "e")
	must(t, r.store.SetDismissed(a, true))
	r.refresh()
	if r.m.expanded() {
		t.Error("when the expanded notification goes away, so does the expanded view")
	}
}

func TestTheNewKeysDoNothingOnTheFocusTabOrAnEmptyList(t *testing.T) {
	r := newRig(t)
	r.press("e", "o", "d", "x", "enter", "esc") // empty list
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f1", "e", "o", "d", "x")
	if len(r.opened) != 0 {
		t.Errorf("opened %v from the Focus tab", r.opened)
	}
	r.press("f2")
	if !strings.Contains(r.view(), "Ana") {
		t.Error("the Focus tab dismissed something")
	}
}

func TestWhenTheDatabaseRefusesAnActionTheStatusBarSaysSo(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.store.Close()
	r.press("d")
	if got := r.view(); !strings.Contains(got, "could not dismiss") {
		t.Errorf("status bar:\n%s", got)
	}
}

func senders(r *rig) []string {
	var out []string
	for _, n := range r.m.lists[r.m.tab] {
		out = append(out, n.Sender)
	}
	return out
}

func TestYAndNAnswerWhetherShhDidTheRightThing(t *testing.T) {
	// The worker's quick labels are absolute (y: it should have reached me, n: it should have stayed muted). y and n in the UI are
	// relative to what shh did, so each answer maps to one of them depending on the row.
	tests := []struct {
		name     string
		status   store.Status
		key      string
		feedback string
		notice   string
	}{
		{"right to hold a blocked one back", store.StatusBlocked, "y", "n", "right call to hold it back"},
		{"wrong to hold a blocked one back", store.StatusBlocked, "n", "y", "should have alerted"},
		{"right to alert", store.StatusAlerted, "y", "y", "right call to alert you"},
		{"wrong to alert", store.StatusAlerted, "n", "n", "should have stayed muted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			first := r.add("Ana", "x", 1, func(n *store.Notification) { n.Score = 0.60; n.Status = tt.status })
			r.add("Bo", "x", 2, func(n *store.Notification) { n.Score = 0.70 })
			r.refresh()
			r.press("f3")
			if r.selectedSender() != "Ana" {
				t.Fatalf("setup: selected %q", r.selectedSender())
			}
			r.press(tt.key)
			if len(r.trainer.requests) != 1 {
				t.Fatalf("%d training requests, want 1", len(r.trainer.requests))
			}
			if got := r.trainer.requests[0]; got.NotificationID != first || got.Feedback != tt.feedback || got.Label != nil {
				t.Errorf("request = %+v, want the worker label %q", got, tt.feedback)
			}
			if r.ensured != 1 {
				t.Errorf("the worker was ensured %d times, want once before training", r.ensured)
			}
			if got := r.messageLine(); !strings.Contains(got, tt.notice) {
				t.Errorf("the message line should say %q: %q", tt.notice, got)
			}
		})
	}
}

func TestTrainedRowsLeaveTheListInTheRealStoreToo(t *testing.T) {
	// The fake trainer does not touch the store, so stand in for the worker: mark the row trained as the real one does.
	r := newRig(t)
	a := r.add("Ana", "x", 1)
	r.add("Bo", "x", 2)
	r.refresh()
	r.press("f3", "y")
	must(t, r.store.Train(a, store.Training{Feedback: "y"})) // what the worker does
	r.refresh()
	if got := senders(r); len(got) != 1 || got[0] != "Bo" {
		t.Errorf("Train list = %v, want Bo only", got)
	}
	if r.selectedSender() != "Bo" {
		t.Errorf("the cursor is on %q, want the next row so labelling can carry on", r.selectedSender())
	}
	if got := r.view(); !strings.Contains(got, "1 to train") {
		t.Errorf("the count should follow:\n%s", got)
	}
}

func TestTheNoticeReportsHowTheScoreMoved(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "n") // blocked, and wrong to have held it back
	if got := r.view(); !strings.Contains(got, "should have alerted: score 42 → 71") {
		t.Errorf("status bar:\n%s", got)
	}
	r.add("Bo", "x", 2)
	r.refresh()
	r.trainer.outcome = session.TrainView{Before: 30, After: 30}
	r.press("y") // blocked, and right
	if got := r.view(); !strings.Contains(got, "right call to hold it back: the model already agreed (score 30), nothing to change") {
		t.Errorf("status bar:\n%s", got)
	}
}

func TestTTypesAnExactLabelAndEnterSavesIt(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "t")
	if r.m.prompt == nil {
		t.Fatal("t did not open the label prompt")
	}
	if got := r.view(); !strings.Contains(got, "label for Ana") || !strings.Contains(got, "Enter saves") {
		t.Errorf("the prompt should replace the status bar:\n%s", got)
	}
	r.press("7", "5", "5", "backspace", "enter")
	if len(r.trainer.requests) != 1 {
		t.Fatalf("%d requests, want 1", len(r.trainer.requests))
	}
	got := r.trainer.requests[0]
	if got.NotificationID != id || got.Label == nil || *got.Label != 75 || got.Feedback != "" {
		t.Errorf("request = %+v, want an exact label of 75 (the 5 typed twice, one deleted)", got)
	}
	if r.m.prompt != nil {
		t.Error("the prompt should close after saving")
	}
}

func TestDigitsOnlyTypeIntoThePromptOnceItIsOpen(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "9")
	if r.m.prompt != nil {
		t.Fatalf("a digit opened the label prompt: it should only build a count now (%+v)", r.m.prompt)
	}
	r.press("esc", "t", "9", "0", "enter")
	if got := r.trainer.requests; len(got) != 1 || got[0].Label == nil || *got[0].Label != 90 {
		t.Errorf("requests = %+v, want an exact label of 90 typed after t", got)
	}
}

func TestTierNamesAndOneHundredAreValidLabels(t *testing.T) {
	for text, want := range map[string]int{"light": 35, "medium": 65, "deep": 90, "lockdown": 99, "100": 100, "0": 0, "75%": 75} {
		r := newRig(t)
		r.add("Ana", "x", 1)
		r.refresh()
		r.press("f3", "t")
		for _, ch := range text {
			r.press(string(ch))
		}
		r.press("enter")
		if got := r.trainer.requests; len(got) != 1 || got[0].Label == nil || *got[0].Label != want {
			t.Errorf("%q: requests = %+v, want an exact label of %d", text, got, want)
		}
	}
}

func TestAnInvalidLabelShowsAnErrorSendsNothingAndKeepsThePromptOpen(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	for _, bad := range []string{"abc", "101", "-5", "", "7.5"} {
		r.press("f3", "t")
		for _, ch := range bad {
			r.press(string(ch))
		}
		r.press("enter")
		if len(r.trainer.requests) != 0 {
			t.Fatalf("%q was sent to the worker: %+v", bad, r.trainer.requests)
		}
		if r.m.prompt == nil {
			t.Fatalf("%q: the prompt closed on an invalid label", bad)
		}
		if got := r.view(); !strings.Contains(got, "✗") {
			t.Errorf("%q: the prompt should say what is wrong:\n%s", bad, got)
		}
		// typing again clears the complaint, and Esc leaves without training
		r.press("x")
		if r.m.prompt.err != "" {
			t.Errorf("%q: the error should clear when typing resumes", bad)
		}
		r.press("esc")
		if r.m.prompt != nil {
			t.Errorf("%q: Esc should close the prompt", bad)
		}
	}
}

func TestEscCancelsThePromptWithoutTraining(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "t", "5", "0", "esc")
	if len(r.trainer.requests) != 0 || r.m.prompt != nil || r.row(id).IsTrained {
		t.Errorf("requests %v, prompt %v: Esc should cancel", r.trainer.requests, r.m.prompt)
	}
}

func TestWhileTypingAKeyIsTextNotACommand(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "t", "q", "d", "y", "j", "?")
	if r.m.prompt == nil || r.m.prompt.text != "qdyj?" {
		t.Errorf("prompt = %+v, want the keys typed as text", r.m.prompt)
	}
	if len(r.trainer.requests) != 0 {
		t.Errorf("a typed y trained something: %+v", r.trainer.requests)
	}
}

func TestTrainingFromNotificationsArchivesTheRowButTrainingFromTrainDoesNot(t *testing.T) {
	r := newRig(t)
	a := r.add("Ana", "x", 1)
	b := r.add("Bo", "x", 2)
	r.refresh()
	r.press("f2", "y") // trains Ana from Notifications
	if !r.row(a).IsDismissedInbox {
		t.Error("training from Notifications should archive the row from it")
	}
	r.press("f3")
	r.m.cursor[TabTrain] = 1 // Bo, wherever the ordering put it
	if n, _ := r.m.Selected(); n.ID != b {
		r.m.cursor[TabTrain] = 0
	}
	r.press("y")
	if r.row(b).IsDismissedInbox {
		t.Error("training from Train must leave Notifications alone")
	}
}

func TestUndoPutsBackTheArchivedRowAndSelectsIt(t *testing.T) {
	r := newRig(t)
	a := r.add("Ana", "x", 1)
	r.add("Bo", "x", 2)
	r.refresh()
	r.press("f2", "y")
	must(t, r.store.Train(a, store.Training{Feedback: "y"})) // the worker's part
	r.refresh()
	if got := senders(r); len(got) != 1 {
		t.Fatalf("setup: Notifications = %v", got)
	}
	r.press("u")
	if r.trainer.undos != 1 {
		t.Fatalf("undo calls = %d, want 1", r.trainer.undos)
	}
	must(t, r.store.Untrain(a)) // the worker's part
	r.refresh()
	if r.row(a).IsDismissedInbox {
		t.Error("undo should put the row back on Notifications")
	}
	r.press("u") // nothing more of ours on the stack, but the fake still answers
	r.trainer.undoErr = session.ErrNothingToUndo
	r.press("u")
	if got := r.view(); !strings.Contains(got, "nothing to undo") {
		t.Errorf("status bar:\n%s", got)
	}
}

func TestUndoSaysSoAndSelectsTheRowThatCameBack(t *testing.T) {
	r := newRig(t)
	r.add("Zed", "x", 5, func(n *store.Notification) { n.Score = 0.2; n.Boost = 0 })
	a := r.add("Ana", "x", 1, func(n *store.Notification) { n.Score = 0.65; n.Boost = 0 })
	r.refresh()
	r.press("f3")
	if r.selectedSender() != "Ana" {
		t.Fatalf("setup: the most informative row (score near 65) should be first, got %q", r.selectedSender())
	}
	r.press("y")
	must(t, r.store.Train(a, store.Training{Feedback: "y"}))
	r.refresh()
	if r.selectedSender() != "Zed" {
		t.Fatalf("setup: selected %q", r.selectedSender())
	}
	r.press("u")
	must(t, r.store.Untrain(a))
	r.refresh()
	if got := r.view(); !strings.Contains(got, "undid the last training") {
		t.Errorf("status bar:\n%s", got)
	}
	if r.selectedSender() != "Ana" {
		t.Errorf("selected %q after the undo, want the row that came back", r.selectedSender())
	}
}

func TestTheWorkerIsEnsuredBeforeTrainingAndAFailureStopsIt(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.ensureE = errors.New("cannot start")
	r.press("f3", "y")
	if len(r.trainer.requests) != 0 {
		t.Errorf("trained without a worker: %+v", r.trainer.requests)
	}
	if got := r.view(); !strings.Contains(got, "cannot reach the worker to train: cannot start") {
		t.Errorf("status bar:\n%s", got)
	}
	r.press("u")
	if r.trainer.undos != 0 {
		t.Error("undid without a worker")
	}
}

func TestTrainingFailuresAreShownAndChangeNothing(t *testing.T) {
	r := newRig(t)
	a := r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3")
	r.trainer.err = session.ErrAlreadyTrained
	r.press("y")
	if got := r.view(); !strings.Contains(got, "already trained") {
		t.Errorf("status bar:\n%s", got)
	}
	r.trainer.err = errors.New("disk full")
	r.press("n")
	if got := r.view(); !strings.Contains(got, "could not train: disk full") {
		t.Errorf("status bar:\n%s", got)
	}
	if r.row(a).IsDismissedInbox || len(r.m.undoStack) != 0 {
		t.Error("a failed training must not archive the row or leave anything to undo")
	}
}

func TestWithoutATrainerTrainingKeysSayTheyAreUnavailable(t *testing.T) {
	s, err := store.Open(":memory:")
	must(t, err)
	defer s.Close()
	m := New(Config{Data: s, Now: func() time.Time { return now }})
	r := &rig{t: t, store: s, m: m}
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "y")
	if got := r.view(); !strings.Contains(got, "training is not available") {
		t.Errorf("status bar:\n%s", got)
	}
	r.press("t", "u", "5")
	if m.prompt != nil {
		t.Error("a prompt opened with no trainer to send it to")
	}
}

func TestTrainingKeysDoNothingOnTheFocusTabOrAnEmptyList(t *testing.T) {
	r := newRig(t)
	r.press("y", "n", "t", "5", "esc") // empty list: nothing to label (u still works: it undoes the last training)
	if len(r.trainer.requests) != 0 || r.m.prompt != nil {
		t.Errorf("requests %v prompt %v on an empty list", r.trainer.requests, r.m.prompt)
	}
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f1", "y", "n", "t", "u", "5")
	if len(r.trainer.requests) != 0 || r.trainer.undos != 0 || r.m.prompt != nil {
		t.Errorf("requests %v undos %d prompt %v from the Focus tab", r.trainer.requests, r.trainer.undos, r.m.prompt)
	}
}

func TestTheTrainListPutsScoresNearTheCommonThresholdsFirst(t *testing.T) {
	r := newRig(t)
	r.add("far-low", "x", 6, func(n *store.Notification) { n.Score = 0.10; n.Boost = 0 })
	r.add("mid", "x", 5, func(n *store.Notification) { n.Score = 0.50; n.Boost = 0 })
	r.add("near-90", "x", 4, func(n *store.Notification) { n.Score = 0.88; n.Boost = 0 })
	r.add("near-35-old", "x", 3, func(n *store.Notification) { n.Score = 0.36; n.Boost = 0 })
	r.add("near-65-new", "x", 1, func(n *store.Notification) { n.Score = 0.64; n.Boost = 0 })
	r.refresh()
	r.press("f3")
	want := []string{"near-65-new", "near-35-old", "near-90", "mid", "far-low"}
	got := senders(r)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Train order = %v, want %v: closest to a common threshold first, the newest first among equals", got, want)
	}
	r.press("f2")
	wantNewest := []string{"near-65-new", "near-35-old", "near-90", "mid", "far-low"}
	if got := senders(r); strings.Join(got, ",") != strings.Join(wantNewest, ",") {
		t.Errorf("Notifications should keep its newest-first order, got %v", got)
	}
}

func TestTheBoostCountsWhenRankingBecauseThatIsWhatWasCompared(t *testing.T) {
	r := newRig(t)
	r.add("lifted", "x", 3, func(n *store.Notification) { n.Score = 0.50; n.Boost = 0.14 }) // decided at 64
	r.add("plain-60", "x", 2, func(n *store.Notification) { n.Score = 0.58; n.Boost = 0 })
	r.refresh()
	r.press("f3")
	if got := r.selectedSender(); got != "lifted" {
		t.Errorf("first = %q, want the row whose boosted score (64) sits next to 65", got)
	}
}

// helpText gathers everything the help shows, scrolling through it.
func helpText(r *rig) string {
	r.m.helpScroll = 0
	var all []string
	for i := 0; i < 200; i++ {
		all = append(all, strings.Join(strings.Fields(r.view()), " "))
		before := r.m.helpScroll
		r.press("j")
		if r.m.helpScroll == before {
			break
		}
	}
	return strings.Join(all, " ")
}

func TestHelpExplainsEveryKeyInSentences(t *testing.T) {
	r := newRig(t)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 80}) // tall enough that every sentence is whole in one view
	r.press("?")
	text := helpText(r)
	r.press("x")
	// and a small terminal can still reach all of it by scrolling
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	r.press("?")
	small := helpText(r)
	r.press("x")
	for _, want := range []string{"Moving around", "Reading", "Training", "What the marks mean", "Other", "Quit."} {
		if !strings.Contains(small, want) {
			t.Errorf("scrolling a small terminal never reaches %q", want)
		}
	}
	for _, want := range []string{
		"6j goes down six rows", "3k up three", "how far it is from the cursor", "5G",
		"does not mark the notification as read", "not marked", "dismissing is not training", "Flag button on an alert banner",
		"did shh do the right thing", "✕ blocked", "no mark means it alerted you", "pushing the score further", "digits and letters are typed into it",
		"Undo the last training exactly", "closest to a common threshold", "Not opened yet", "Flagged for review", "Quit.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the help never says %q", want)
		}
	}
}

func TestHelpFitsEveryTerminalSize(t *testing.T) {
	r := newRig(t)
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 12}, {40, 8}} {
		r.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		r.press("?")
		for i := 0; i < 80; i++ {
			view := r.view()
			if h := lipgloss.Height(view); h != size[1] {
				t.Fatalf("%dx%d: the help is %d lines tall", size[0], size[1], h)
			}
			for n, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w > size[0] {
					t.Fatalf("%dx%d: help line %d is %d wide: %q", size[0], size[1], n, w, line)
				}
			}
			before := r.m.helpScroll
			r.press("j")
			if r.m.helpScroll == before {
				break
			}
		}
		r.press("x")
	}
}

func TestTheFooterHintsAreBriefButCoverEveryKeyOnThePage(t *testing.T) {
	r := newRig(t)
	for _, tt := range []struct {
		tab  string
		want []string
	}{
		{"f2", []string{"j/k move", "/ filter", "e read", "o open", "d dismiss", "f flag", "y/n right/wrong", "t score", "u undo"}},
		{"f3", []string{"j/k move", "/ filter", "y/n right/wrong", "t score", "u undo", "f flag", "e read", "o open", "d dismiss"}},
		{"f1", []string{"j/k select", "Enter run it", "s b f p + - x X", "Tab switch"}},
	} {
		r.press(tt.tab)
		hints := r.messageLine()
		for _, want := range tt.want {
			if !strings.Contains(hints, want) {
				t.Errorf("%s: the hints %q do not mention %q", tt.tab, hints, want)
			}
		}
		if strings.Contains(hints, "6j") || strings.Contains(hints, "3k") || strings.Contains(hints, "5G") {
			t.Errorf("%s: the hints should stay basic; counts are explained in the help: %q", tt.tab, hints)
		}
		if lipgloss.Width(hints) > 100 {
			t.Errorf("%s: the hints are %d wide, more than fits a 100 column terminal: %q", tt.tab, lipgloss.Width(hints), hints)
		}
		if state := r.promptBar(); !strings.Contains(state, "? help") || !strings.Contains(state, "q quit") {
			t.Errorf("%s: the keys that work everywhere belong at the right of the state line above the hints: %q", tt.tab, state)
		}
	}
}

func TestOnANarrowTerminalTheStateLineStillPointsToTheHelpAndTheHintsAreCutCleanly(t *testing.T) {
	r := newRig(t)
	r.m.Update(tea.WindowSizeMsg{Width: 50, Height: 12})
	r.press("f3")
	if state := r.promptBar(); !strings.Contains(state, "? help") {
		t.Errorf("state line on 50 columns: %q", state)
	}
	if got := r.messageLine(); lipgloss.Width(got) > 50 || !strings.Contains(got, "j/k move") {
		t.Errorf("hints on a 50 column terminal: %q", got)
	}
}

// promptBar is the footer's first line (the state line, which holds the label being typed) and messageLine its second.
func (r *rig) promptBar() string {
	lines := strings.Split(r.view(), "\n")
	return lines[len(lines)-2]
}

func (r *rig) messageLine() string {
	lines := strings.Split(r.view(), "\n")
	return lines[len(lines)-1]
}

func TestOpeningThePromptStartsABlinkingCursor(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3")
	_, cmd := r.m.Update(key("t"))
	if cmd == nil {
		t.Fatal("opening the prompt should start the blink timer")
	}
	if bar := r.promptBar(); !strings.Contains(bar, "█") {
		t.Errorf("the cursor should be showing as soon as the prompt opens: %q", bar)
	}
	gen := r.m.blinkGen
	r.m.Update(blinkMsg{gen: gen})
	if bar := r.promptBar(); strings.Contains(bar, "█") {
		t.Errorf("after a blink the cursor should be off: %q", bar)
	}
	_, cmd = r.m.Update(blinkMsg{gen: gen})
	if cmd == nil {
		t.Error("a blink should schedule the next one")
	}
	if bar := r.promptBar(); !strings.Contains(bar, "█") {
		t.Errorf("after another blink the cursor should be back: %q", bar)
	}
}

func TestTheCursorStaysSolidWhileTypingAndRestartsItsTimer(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "t")
	r.m.Update(blinkMsg{gen: r.m.blinkGen}) // off
	old := r.m.blinkGen
	_, cmd := r.m.Update(key("7"))
	if !strings.Contains(r.promptBar(), "7█") {
		t.Errorf("typing should show the cursor right after the text: %q", r.promptBar())
	}
	if cmd == nil || r.m.blinkGen == old {
		t.Errorf("a keystroke should restart the blink timer (cmd %v, generation %d -> %d)", cmd != nil, old, r.m.blinkGen)
	}
	// The timer that was running before the keystroke must not flip the cursor again.
	r.m.Update(blinkMsg{gen: old})
	if !strings.Contains(r.promptBar(), "█") {
		t.Error("a stale blink timer flipped the cursor")
	}
}

func TestBlinkTimersEndWhenThePromptCloses(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "t")
	gen := r.m.blinkGen
	r.press("esc")
	if _, cmd := r.m.Update(blinkMsg{gen: gen}); cmd != nil {
		t.Error("a blink after the prompt closed should end the timer, not schedule another")
	}
	if _, cmd := r.m.Update(blinkMsg{gen: gen - 1}); cmd != nil {
		t.Error("a stale blink should end quietly")
	}
}

func TestTheTypingBoxIsSeparateFromTheQuestionAndAlwaysFits(t *testing.T) {
	r := newRig(t)
	r.add("Ana with a rather long name indeed", "x", 1)
	r.refresh()
	for _, width := range []int{120, 80, 50, 30} {
		r.m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		r.press("f3", "t", "7", "5")
		bar := r.promptBar()
		if w := lipgloss.Width(bar); w != width {
			t.Errorf("width %d: the prompt bar is %d wide", width, w)
		}
		if !strings.Contains(bar, " 75█ ") {
			t.Errorf("width %d: the typed text should sit in its own padded box: %q", width, bar)
		}
		if msg := r.messageLine(); lipgloss.Width(msg) > width || !strings.Contains(msg, "Enter saves") {
			t.Errorf("width %d: the hint belongs on the line below, within the terminal's width: %q", width, msg)
		}
		r.press("esc")
	}
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	r.press("t", "5", "x", "enter")
	if bar := r.promptBar(); !strings.Contains(bar, "5x█") {
		t.Errorf("an invalid label keeps its text in the box: %q", bar)
	}
	if msg := r.messageLine(); !strings.Contains(msg, "✗") || !strings.Contains(msg, "is not a score: use 0-100, or light, medium, deep, lockdown") {
		t.Errorf("what is wrong with the label goes on the line below the box: %q", msg)
	}
	if bar := r.promptBar(); strings.Contains(bar, "✗") || strings.Contains(bar, "Enter saves") {
		t.Errorf("the box line should hold only the question and the box: %q", bar)
	}
}

func TestTheTypingBoxHasItsOwnPaletteColourAndNoFixedColours(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "t", "7", "5")
	bar := r.promptBar()
	if !strings.Contains(bar, "33;4m") && !strings.Contains(bar, "33m") {
		t.Errorf("the typing box should be yellow (ANSI 33): %q", bar)
	}
	if !strings.Contains(bar, ";4m") && !strings.Contains(bar, ";4;") {
		t.Errorf("the typing box should be underlined: %q", bar)
	}
	// The typed text is inside the coloured part, not the reversed part.
	if !strings.Contains(bar, "7\x1b") {
		t.Errorf("the typed text should be inside the styled box: %q", bar)
	}
	if !strings.Contains(bar, "7m") && !strings.Contains(bar, ";7;") && !strings.Contains(bar, "[7") {
		t.Errorf("the rest of the bar should stay in reverse video so the box contrasts with it: %q", bar)
	}
	if strings.Contains(bar, "38;2;") || strings.Contains(bar, "38;5;") || strings.Contains(bar, "48;") {
		t.Errorf("fixed colours would ignore the terminal's theme: %q", bar)
	}
}

func TestTheFooterIsTwoLinesAndTheMessageLineCarriesAnythingToSay(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	lines := strings.Split(r.view(), "\n")
	if h := len(lines); h != 30 {
		t.Fatalf("the view is %d lines, want the full terminal height", h)
	}
	if !strings.Contains(lines[len(lines)-2], "notifications") || !strings.Contains(lines[len(lines)-2], "? help") {
		t.Errorf("the first footer line should be the state: %q", lines[len(lines)-2])
	}
	if got := r.messageLine(); !strings.Contains(got, "e read") || !strings.Contains(got, "y/n right/wrong") {
		t.Errorf("with nothing to report the message line should list the keys: %q", got)
	}
	r.press("f3")
	if got := r.messageLine(); !strings.Contains(got, "y/n right/wrong") || !strings.Contains(got, "t score") {
		t.Errorf("the Train tab has its own hints: %q", got)
	}
	r.press("f1")
	if got := r.messageLine(); !strings.Contains(got, "j/k select") || !strings.Contains(got, "Tab switch") {
		t.Errorf("the Focus tab has its own hints: %q", got)
	}
	r.press("f2", "o")
	if got := r.messageLine(); !strings.Contains(got, "opened Teams") {
		t.Errorf("the result of an action goes on the message line: %q", got)
	}
	if got := r.promptBar(); strings.Contains(got, "opened Teams") {
		t.Errorf("the state line should not carry the message: %q", got)
	}
	r.press("j")
	if got := r.messageLine(); strings.Contains(got, "opened Teams") || !strings.Contains(got, "e read") {
		t.Errorf("the message goes at the next key and the hints return: %q", got)
	}
}

func TestAProblemReadingTheDatabaseShowsOnTheMessageLine(t *testing.T) {
	r := newRig(t)
	r.store.Close()
	r.refresh()
	if got := r.messageLine(); !strings.Contains(got, "cannot read notifications") {
		t.Errorf("message line: %q", got)
	}
	if got := r.promptBar(); !strings.Contains(got, "notifications") || strings.Contains(got, "cannot read") {
		t.Errorf("the state line should keep showing the state: %q", got)
	}
}

func TestLabelErrorsFitAnEightyColumnTerminalWithoutBeingCut(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	r.refresh()
	for _, bad := range []string{"abc", "101", "-5", "7.5", "lots of words"} {
		r.press("f3", "t")
		for _, ch := range bad {
			r.press(string(ch))
		}
		r.press("enter")
		msg := r.messageLine()
		if strings.Contains(msg, "…") || lipgloss.Width(msg) > 80 {
			t.Errorf("%q: the error is cut off or too wide: %q", bad, msg)
		}
		r.press("esc")
	}
}

func flagged(n *store.Notification) { n.IsFlagged = true }

func TestFlaggedNotificationsLeadTheTrainListWhateverTheirScore(t *testing.T) {
	r := newRig(t)
	r.add("near-65", "x", 1, func(n *store.Notification) { n.Score = 0.65; n.Boost = 0 })
	far := r.add("far-flagged", "x", 3, func(n *store.Notification) { n.Score = 0.05; n.Boost = 0 })
	r.add("near-35", "x", 2, func(n *store.Notification) { n.Score = 0.35; n.Boost = 0 })
	closer := r.add("closer-flagged", "x", 4, func(n *store.Notification) { n.Score = 0.20; n.Boost = 0 })
	must(t, r.store.SetFlagged(far, true))
	must(t, r.store.SetFlagged(closer, true))
	r.refresh()
	r.press("f3")
	want := []string{"closer-flagged", "far-flagged", "near-65", "near-35"}
	if got := senders(r); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Train order = %v, want %v: flagged first (the usual most-informative order among them), then the rest", got, want)
	}
	r.press("f2")
	if first := senders(r)[0]; first != "near-65" {
		t.Errorf("Notifications keeps newest first and does not reorder by flag, got %q first", first)
	}
}

func mustList(t *testing.T, r *rig) []store.Notification {
	t.Helper()
	rows, err := r.store.ListNotifications(store.Filter{})
	must(t, err)
	return rows
}

func TestAFlaggedRowShowsAMarkTheDetailPaneAndACountInTheTab(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "x", 1)
	r.add("Bo", "x", 2)
	must(t, r.store.SetFlagged(id, true))
	r.refresh()
	view := r.view()
	if !strings.Contains(view, "(2, 1 ⚑)") {
		t.Errorf("the Train tab should count the flagged ones:\n%s", view)
	}
	r.press("f3")
	view = r.view()
	if !strings.Contains(view, "⚑") || !strings.Contains(view, "flagged for review") {
		t.Errorf("the flagged row and its detail should say so:\n%s", view)
	}
	rows := 0
	for _, l := range strings.Split(view, "\n") {
		if left, _, ok := strings.Cut(l, "│"); ok && strings.Contains(left, "Teams") && strings.Contains(left, "⚑") {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("%d rows carry the flag mark, want exactly the flagged one:\n%s", rows, view)
	}
}

func TestFTogglesTheFlagAndKeepsTheCursorOnTheSameRow(t *testing.T) {
	for _, tab := range []string{"f2", "f3"} {
		r := newRig(t)
		r.add("Ana", "x", 1, func(n *store.Notification) { n.Score = 0.10; n.Boost = 0 })
		b := r.add("Bo", "x", 2, func(n *store.Notification) { n.Score = 0.64; n.Boost = 0 })
		r.refresh()
		r.press(tab)
		r.m.selectID(b)
		r.press("f")
		if !r.row(b).IsFlagged {
			t.Fatalf("%s: f did not flag the selected row", tab)
		}
		if got := r.messageLine(); !strings.Contains(got, "flagged") {
			t.Errorf("%s: message line = %q", tab, got)
		}
		if r.selectedSender() != "Bo" {
			t.Errorf("%s: the cursor moved to %q when the row was flagged; it should stay on Bo even though Train re-sorted", tab, r.selectedSender())
		}
		r.press("f")
		if r.row(b).IsFlagged {
			t.Errorf("%s: a second f should clear the flag", tab)
		}
		if got := r.messageLine(); !strings.Contains(got, "flag cleared") {
			t.Errorf("%s: message line = %q", tab, got)
		}
	}
}

func TestLabellingAFlaggedRowClearsItsFlagAndTheCount(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "x", 1)
	r.add("Bo", "x", 2)
	must(t, r.store.SetFlagged(id, true))
	r.refresh()
	r.press("f3")
	if r.selectedSender() != "Ana" {
		t.Fatalf("setup: selected %q", r.selectedSender())
	}
	r.press("y") // the fake worker marks it trained, which clears the flag as the real store does
	r.refresh()
	if r.row(id).IsFlagged {
		t.Error("training left the flag on")
	}
	if got := r.view(); strings.Contains(got, "⚑") {
		t.Errorf("no flagged rows are left, so no flag mark or count should show:\n%s", got)
	}
}

func TestFlaggedRowsAreHighlightedInThePaletteColourAndOthersAreNot(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	r := newRig(t)
	id := r.add("Ana", "x", 1, func(n *store.Notification) { n.Status = store.StatusAlerted })
	r.add("Bo", "x", 2, func(n *store.Notification) { n.Status = store.StatusAlerted })
	must(t, r.store.SetFlagged(id, true))
	r.refresh()
	r.press("f3", "j") // the cursor on the plain row, so the flagged one is not reversed
	var flaggedLine, plainLine string
	for _, l := range strings.Split(r.view(), "\n") {
		left, _, ok := strings.Cut(l, "│")
		switch {
		case ok && strings.Contains(left, "Ana"):
			flaggedLine = left
		case ok && strings.Contains(left, "Bo"):
			plainLine = left
		}
	}
	if !strings.Contains(flaggedLine, "35m") {
		t.Errorf("the flagged row should be in the palette's magenta (ANSI 35): %q", flaggedLine)
	}
	if strings.Contains(plainLine, "35m") {
		t.Errorf("an unflagged row should not be magenta: %q", plainLine)
	}
	if strings.Contains(flaggedLine, "38;2;") || strings.Contains(flaggedLine, "38;5;") {
		t.Errorf("fixed colours would ignore the theme: %q", flaggedLine)
	}
}

func TestFDoesNothingOnTheFocusTabAnEmptyListOrADatabaseError(t *testing.T) {
	r := newRig(t)
	r.press("f") // empty list
	id := r.add("Ana", "x", 1)
	r.refresh()
	r.press("f1", "f")
	if r.row(id).IsFlagged {
		t.Error("f flagged something from the Focus tab")
	}
	r.press("f2")
	r.store.Close()
	r.press("f")
	if got := r.messageLine(); !strings.Contains(got, "could not change the flag") {
		t.Errorf("message line = %q", got)
	}
}

func TestHelpAndHintsMentionTheFlag(t *testing.T) {
	r := newRig(t)
	if got := r.messageLine(); !strings.Contains(got, "f flag") {
		t.Errorf("Notifications hints: %q", got)
	}
	r.press("f3")
	if got := r.messageLine(); !strings.Contains(got, "f flag") {
		t.Errorf("Train hints: %q", got)
	}
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 80})
	r.press("?")
	if got := helpText(r); !strings.Contains(got, "Flag it to look at again later") {
		t.Errorf("help:\n%s", got)
	}
}

// gutterNumbers reads the line numbers beside the rows of the list, top to bottom.
func gutterNumbers(r *rig) []int {
	var out []int
	for _, l := range strings.Split(r.view(), "\n") {
		left, _, ok := strings.Cut(l, "│")
		if !ok || !strings.Contains(left, "Teams") {
			continue
		}
		var n int
		fmt.Sscanf(strings.TrimSpace(left[:gutterWidth]), "%d", &n)
		out = append(out, n)
	}
	return out
}

func fiveRows(t *testing.T) *rig {
	r := newRig(t)
	for i := 0; i < 5; i++ {
		r.add(fmt.Sprintf("Row%d", i), "x", i+1)
	}
	r.refresh()
	return r
}

func TestTheGutterShowsDistancesFromTheCursorAndItsOwnNumberOnTheCursorRow(t *testing.T) {
	r := fiveRows(t)
	if got := gutterNumbers(r); fmt.Sprint(got) != fmt.Sprint([]int{1, 1, 2, 3, 4}) {
		t.Errorf("gutter with the cursor on the first row = %v, want its own number 1 then distances 1 2 3 4", got)
	}
	r.press("j", "j")
	if got := gutterNumbers(r); fmt.Sprint(got) != fmt.Sprint([]int{2, 1, 3, 1, 2}) {
		t.Errorf("gutter with the cursor on the third row = %v, want distances 2 1, the cursor's own number 3, then 1 2", got)
	}
	r.press("f3")
	if got := gutterNumbers(r); len(got) != 5 || got[0] != 1 {
		t.Errorf("the Train tab has the gutter too: %v", got)
	}
}

func TestTheGutterStaysRightAfterTheListScrolls(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 40; i++ {
		r.add(fmt.Sprintf("Row%02d", i), "x", i+1)
	}
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	r.refresh()
	r.press("2", "5", "j")
	got := gutterNumbers(r)
	cursor := -1
	for i, n := range got {
		if n == 26 {
			cursor = i
		}
	}
	if cursor < 0 {
		t.Fatalf("the cursor row (26) is not on screen with its own number: %v", got)
	}
	for i, n := range got {
		if i != cursor && n != abs(i-cursor) {
			t.Errorf("row at screen line %d shows %d, want its distance %d from the cursor", i, n, abs(i-cursor))
		}
	}
}

func TestACountRepeatsJAndK(t *testing.T) {
	r := fiveRows(t)
	r.press("3", "j")
	if r.selectedSender() != "Row3" {
		t.Errorf("3j selected %q, want Row3", r.selectedSender())
	}
	r.press("2", "k")
	if r.selectedSender() != "Row1" {
		t.Errorf("2k selected %q, want Row1", r.selectedSender())
	}
	r.press("9", "j")
	if r.selectedSender() != "Row4" {
		t.Errorf("9j selected %q, want to stop at the last row", r.selectedSender())
	}
	r.press("9", "k")
	if r.selectedSender() != "Row0" {
		t.Errorf("9k selected %q, want to stop at the first row", r.selectedSender())
	}
	r.press("2", "down", "1", "up")
	if r.selectedSender() != "Row1" {
		t.Errorf("2 down then 1 up selected %q, want Row1: counts work with the arrow keys", r.selectedSender())
	}
}

func TestCountsCanHaveSeveralDigitsAndAZeroNeedsADigitBeforeIt(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 30; i++ {
		r.add(fmt.Sprintf("Row%02d", i), "x", i+1)
	}
	r.refresh()
	r.press("1", "2", "j")
	if r.selectedSender() != "Row12" {
		t.Errorf("12j selected %q", r.selectedSender())
	}
	r.press("1", "0", "j")
	if r.selectedSender() != "Row22" {
		t.Errorf("10j selected %q: the 0 continues the count", r.selectedSender())
	}
	r.press("0", "j")
	if r.selectedSender() != "Row23" {
		t.Errorf("0 then j selected %q: a lone 0 does nothing, so j moves one row", r.selectedSender())
	}
	if r.m.count != 0 {
		t.Errorf("count = %d after a motion, want it used up", r.m.count)
	}
	for i := 0; i < 8; i++ {
		r.press("9")
	}
	if r.m.count != maxCount {
		t.Errorf("count = %d after many digits, want it capped at %d", r.m.count, maxCount)
	}
}

func TestNGGoesToThatRow(t *testing.T) {
	r := fiveRows(t)
	r.press("4", "G")
	if r.selectedSender() != "Row3" {
		t.Errorf("4G selected %q, want the fourth row", r.selectedSender())
	}
	r.press("1", "g")
	if r.selectedSender() != "Row0" {
		t.Errorf("1g selected %q, want the first row", r.selectedSender())
	}
	r.press("9", "9", "G")
	if r.selectedSender() != "Row4" {
		t.Errorf("99G selected %q, want the last row", r.selectedSender())
	}
	r.press("g")
	if r.selectedSender() != "Row0" {
		t.Errorf("g selected %q, want the top", r.selectedSender())
	}
	r.press("G")
	if r.selectedSender() != "Row4" {
		t.Errorf("G selected %q, want the bottom", r.selectedSender())
	}
}

func TestAnyOtherKeyOrEscDropsAPendingCount(t *testing.T) {
	r := fiveRows(t)
	r.press("3", "esc")
	if r.m.count != 0 {
		t.Error("Esc should drop the count")
	}
	r.press("j")
	if r.selectedSender() != "Row1" {
		t.Errorf("selected %q, want a plain one-row j after the count was dropped", r.selectedSender())
	}
	r.press("3", "d") // a key that is not a motion uses up the count without repeating
	if r.m.count != 0 {
		t.Error("a non-motion key should drop the count")
	}
	if len(r.trainer.requests) != 0 {
		t.Error("a count must not repeat training")
	}
	r.press("2", "f3", "j")
	if r.m.Tab() != TabTrain || r.m.cursor[TabTrain] != 1 {
		t.Errorf("tab %v cursor %d: switching tabs drops the count so j moves one row", r.m.Tab(), r.m.cursor[TabTrain])
	}
}

func TestAPendingCountIsShownOnTheMessageLine(t *testing.T) {
	r := fiveRows(t)
	r.press("1", "4")
	if got := r.messageLine(); !strings.Contains(got, "14") || !strings.Contains(got, "Esc cancels") {
		t.Errorf("message line = %q, want the pending count and how to cancel it", got)
	}
	r.press("esc")
	if got := r.messageLine(); strings.Contains(got, "Esc cancels") {
		t.Errorf("the count message should go with the count: %q", got)
	}
}

func TestDigitsOnTheFocusTabBuildACountAndNeverOpenAPrompt(t *testing.T) {
	r := fiveRows(t)
	r.press("f1", "5", "9")
	if r.m.count != 59 || r.m.prompt != nil {
		t.Errorf("count %d prompt %v on the Focus tab, want a count (for 3+ and 2-) and no prompt", r.m.count, r.m.prompt)
	}
}

func TestACountScrollsTheExpandedMessageAndNGJumpsToALine(t *testing.T) {
	r := newRig(t)
	r.add("Ana", longText, 1)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	r.refresh()
	r.press("e", "5", "j")
	if r.m.scroll != 5 {
		t.Errorf("scroll = %d after 5j, want 5", r.m.scroll)
	}
	r.press("2", "k")
	if r.m.scroll != 3 {
		t.Errorf("scroll = %d after 2k, want 3", r.m.scroll)
	}
	r.press("9", "G")
	if r.m.scroll != 8 {
		t.Errorf("scroll = %d after 9G, want line 9 at the top (8)", r.m.scroll)
	}
	r.press("g")
	if r.m.scroll != 0 {
		t.Errorf("scroll = %d after g", r.m.scroll)
	}
}

func TestTheGutterNeverMakesALineWiderThanTheTerminal(t *testing.T) {
	r := fiveRows(t)
	for _, size := range [][2]int{{80, 24}, {60, 10}, {40, 12}} {
		r.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, tab := range []string{"f2", "f3"} {
			r.press(tab)
			view := r.view()
			if h := lipgloss.Height(view); h != size[1] {
				t.Errorf("%dx%d %s: %d lines tall", size[0], size[1], tab, h)
			}
			for i, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w > size[0] {
					t.Errorf("%dx%d %s: line %d is %d wide: %q", size[0], size[1], tab, i, w, line)
				}
			}
		}
	}
}

func TestTheTrainTabAsksWhetherEachCallWasCorrectSayingWhatShhDid(t *testing.T) {
	r := newRig(t)
	r.add("Held", "x", 2)
	r.add("Alerted", "x", 1, func(n *store.Notification) { n.Status = store.StatusAlerted })
	r.refresh()
	r.press("f3")
	for _, tt := range []struct{ sender, did string }{{"Alerted", "shh alerted you"}, {"Held", "shh held it back"}} {
		r.m.selectID(idOf(t, r, tt.sender))
		view := r.view()
		if !strings.Contains(view, "Was this correct?") || !strings.Contains(view, tt.did) {
			t.Errorf("%s: the detail pane should ask %q and say %q:\n%s", tt.sender, "Was this correct?", tt.did, view)
		}
		if !strings.Contains(view, "y yes · n no · t type a score") {
			t.Errorf("%s: the question should say which keys answer it:\n%s", tt.sender, view)
		}
		// it comes straight after the score line
		lines := strings.Split(view, "\n")
		for i, l := range lines {
			if strings.Contains(l, "score 42") && (!strings.Contains(lines[i+1], "Was this correct?") || !strings.Contains(lines[i+2], "y yes")) {
				t.Errorf("%s: the question and its keys should be right under the score line, got %q / %q", tt.sender, lines[i+1], lines[i+2])
			}
		}
	}
}

func idOf(t *testing.T, r *rig, sender string) int64 {
	t.Helper()
	for _, n := range r.m.lists[r.m.tab] {
		if n.Sender == sender {
			return n.ID
		}
	}
	t.Fatalf("no %q on the list", sender)
	return 0
}

func TestTheQuestionIsOnlyOnTheTrainTab(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	if got := r.view(); strings.Contains(got, "Was this correct?") {
		t.Errorf("the Notifications tab is for reading, not asking:\n%s", got)
	}
	r.press("f3")
	if got := r.view(); !strings.Contains(got, "Was this correct?") {
		t.Errorf("the Train tab should ask:\n%s", got)
	}
	r.press("f1")
	if got := r.view(); strings.Contains(got, "Was this correct?") {
		t.Errorf("the Focus tab should not ask:\n%s", got)
	}
}

func TestTheQuestionDoesNotBreakTheLayoutAtSmallSizes(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "a long message "+strings.Repeat("with words ", 30), 1)
	r.refresh()
	r.press("f3")
	for _, size := range [][2]int{{120, 30}, {80, 20}, {60, 12}, {40, 10}} {
		r.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := r.view()
		if h := lipgloss.Height(view); h != size[1] {
			t.Errorf("%dx%d: %d lines tall", size[0], size[1], h)
		}
		for n, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > size[0] {
				t.Errorf("%dx%d: line %d is %d wide", size[0], size[1], n, w)
			}
		}
	}
	r.press("e") // expanded too
	if got := r.view(); !strings.Contains(got, "Was this correct?") {
		t.Errorf("the expanded view should keep the question:\n%s", got)
	}
}

// ---- the Focus tab ----

var timedSession = session.StatusView{Level: 65, Layers: []session.LayerView{
	{Kind: "baseline", Percent: 30},
	{Kind: "escalation", Percent: 65, Timed: true, RemainingSecs: 25 * 60, Note: "writing docs"},
}}

// onFocus opens the rig on the Focus tab with the worker reporting the given status (nil: no worker running).
func onFocus(t *testing.T, status *session.StatusView) *rig {
	r := newRig(t)
	if status != nil {
		r.worker.err, r.worker.view = nil, *status
		r.control.status = *status
	}
	r.refresh()
	r.press("f1")
	return r
}

func (r *rig) typeText(text string) {
	for _, ch := range text {
		if ch == ' ' {
			r.m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			continue
		}
		r.press(string(ch))
	}
}

func (r *rig) lastCall() string {
	if len(r.control.calls) == 0 {
		return ""
	}
	return r.control.calls[len(r.control.calls)-1]
}

func TestTheFocusTabShowsTheStackWithTimeLeftNoteAndWhereItFallsBackTo(t *testing.T) {
	r := onFocus(t, &timedSession)
	view := r.view()
	for _, want := range []string{"Focus is on at 65% (medium)", "▶ escalation", "25m left", `"writing docs"`, "then back to baseline 30%", "baseline"} {
		if !strings.Contains(view, want) {
			t.Errorf("Focus tab is missing %q:\n%s", want, view)
		}
	}
	paused := timedSession
	paused.Layers = []session.LayerView{{Kind: "escalation", Percent: 90, Timed: true, Paused: true, RemainingSecs: 600, ThenLevel: intp(0)}}
	paused.Level = 90
	r.worker.view = paused
	r.refresh()
	view = r.view()
	for _, want := range []string{"(paused)", "paused with 10m left", "then off"} {
		if !strings.Contains(view, want) {
			t.Errorf("paused session: missing %q:\n%s", want, view)
		}
	}
}

func intp(n int) *int { return &n }

func TestTheFocusTabSaysWhatToDoWhenOffOrWhenNothingIsRunning(t *testing.T) {
	r := onFocus(t, nil)
	if got := r.view(); !strings.Contains(got, "shh is not running") || !strings.Contains(got, "What to do") || !strings.Contains(got, "Start a session…") {
		t.Errorf("no worker:\n%s", got)
	}
	off := session.StatusView{}
	r.worker.err, r.worker.view = nil, off
	r.refresh()
	if got := r.view(); !strings.Contains(got, "Focus is off") || !strings.Contains(got, "What to do") {
		t.Errorf("focus off:\n%s", got)
	}
}

func TestSStartsASessionFromWhatIsTypedAndTheViewUpdatesAtOnce(t *testing.T) {
	r := onFocus(t, &session.StatusView{})
	r.control.status = session.StatusView{Level: 75, Layers: []session.LayerView{{Kind: "escalation", Percent: 75, Timed: true, RemainingSecs: 25 * 60, Note: "writing docs"}}}
	r.press("s")
	if r.m.prompt == nil || r.m.prompt.kind != promptStart {
		t.Fatal("s did not open the start prompt")
	}
	if bar := r.promptBar(); !strings.Contains(bar, "start focus:") {
		t.Errorf("the bar should say what is being asked: %q", bar)
	}
	if got := r.messageLine(); !strings.Contains(got, "75% 25m writing docs") || !strings.Contains(got, "then off") {
		t.Errorf("the hint should show an example and mention then: %q", got)
	}
	r.typeText("75% 25m writing docs")
	r.press("enter")
	if got := r.lastCall(); got != `focus 75 25m0s then=- note="writing docs"` {
		t.Errorf("worker call = %q", got)
	}
	view := r.view()
	if !strings.Contains(view, "Focus is on at 75%") || !strings.Contains(view, "25m left") {
		t.Errorf("the page should show the new session at once, without waiting for the next refresh:\n%s", view)
	}
	if got := r.messageLine(); !strings.Contains(got, "focus started at 75% for 25m") {
		t.Errorf("message line = %q", got)
	}
	if r.m.prompt != nil {
		t.Error("the prompt should close once the session has started")
	}
}

func TestTheStartLineIsReadLikeTheCommandLine(t *testing.T) {
	tests := []struct {
		text      string
		wantLevel int
		wantLen   time.Duration
		wantThen  string
		wantNote  string
	}{
		{"", 65, 0, "-", ""},
		{"30%", 30, 0, "-", ""},
		{"25m", 65, 25 * time.Minute, "-", ""},
		{"deep 5m", 90, 5 * time.Minute, "-", ""},
		{"5m deep reading", 65, 5 * time.Minute, "-", "deep reading"}, // a tier word only counts as a level alone or before another setting
		{"deep work", 65, 0, "-", "deep work"},
		{"75% 25m writing docs then off", 75, 25 * time.Minute, "0", "writing docs"},
		{"light 1h30m then 40%", 35, 90 * time.Minute, "40", ""},
		{"  60%   10m   spaced   out  ", 60, 10 * time.Minute, "-", "spaced out"},
		{"off 15m", 0, 15 * time.Minute, "-", ""},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			level, length, then, note, err := parseStart(tt.text)
			if err != nil {
				t.Fatal(err)
			}
			thenText := "-"
			if then != nil {
				thenText = fmt.Sprint(*then)
			}
			if level != tt.wantLevel || length != tt.wantLen || thenText != tt.wantThen || note != tt.wantNote {
				t.Errorf("got level %d length %v then %s note %q; want %d %v %s %q", level, length, thenText, note, tt.wantLevel, tt.wantLen, tt.wantThen, tt.wantNote)
			}
		})
	}
	for _, bad := range []string{"150%", "30% 40%", "5m 10m", "75% then banana", "deep then 150%"} {
		if _, _, _, _, err := parseStart(bad); err == nil {
			t.Errorf("%q should not be accepted", bad)
		}
	}
}

func TestABadStartLineShowsTheReasonKeepsThePromptAndSendsNothing(t *testing.T) {
	r := onFocus(t, &session.StatusView{})
	r.press("s")
	r.typeText("150%")
	r.press("enter")
	if len(r.control.calls) != 0 {
		t.Errorf("a bad line was sent: %v", r.control.calls)
	}
	if r.m.prompt == nil || r.m.prompt.err == "" {
		t.Fatal("the prompt should stay open with the reason")
	}
	if got := r.messageLine(); !strings.Contains(got, "✗") || !strings.Contains(got, "150%") {
		t.Errorf("message line = %q, want the reason", got)
	}
	r.press("backspace")
	if r.m.prompt.err != "" {
		t.Error("typing again should clear the complaint")
	}
	r.press("esc")
	if r.m.prompt != nil || len(r.control.calls) != 0 {
		t.Error("Esc should cancel without sending anything")
	}
}

func TestStartingASessionStartsAWorkerWhenNoneIsRunning(t *testing.T) {
	r := onFocus(t, nil)
	r.control.status = session.StatusView{Level: 65, Layers: []session.LayerView{{Kind: "escalation", Percent: 65}}}
	r.press("s", "enter")
	if r.ensured != 1 {
		t.Errorf("the worker was ensured %d times, want once before starting", r.ensured)
	}
	if got := r.lastCall(); !strings.HasPrefix(got, "focus 65 0s") {
		t.Errorf("worker call = %q, want medium until stopped (nothing typed)", got)
	}
	if got := r.view(); !strings.Contains(got, "Focus is on at 65%") {
		t.Errorf("the page should show the session:\n%s", got)
	}
}

func TestIfAWorkerCannotBeStartedNothingIsSent(t *testing.T) {
	r := onFocus(t, nil)
	r.ensureE = errors.New("cannot start")
	r.press("s", "3", "0", "%", "enter")
	if len(r.control.calls) != 0 {
		t.Errorf("sent %v without a worker", r.control.calls)
	}
	if got := r.messageLine(); !strings.Contains(got, "cannot reach the worker: cannot start") {
		t.Errorf("message line = %q", got)
	}
}

func TestBSetsOrClearsTheBaseline(t *testing.T) {
	r := onFocus(t, &session.StatusView{})
	r.control.status = session.StatusView{Level: 30, Layers: []session.LayerView{{Kind: "baseline", Percent: 30}}}
	r.press("b")
	if bar := r.promptBar(); !strings.Contains(bar, "baseline:") {
		t.Errorf("bar = %q", bar)
	}
	r.typeText("light")
	r.press("enter")
	if got := r.lastCall(); got != "baseline 35" {
		t.Errorf("worker call = %q, want a baseline at the light tier", got)
	}
	r.control.status = session.StatusView{}
	r.press("b")
	r.typeText("off")
	r.press("enter")
	if got := r.lastCall(); got != "baseline 0" {
		t.Errorf("worker call = %q", got)
	}
	if got := r.messageLine(); !strings.Contains(got, "baseline cleared") {
		t.Errorf("message line = %q", got)
	}
}

func TestSettingABaselineUnderARunningSessionSaysWhichLevelStaysInForce(t *testing.T) {
	r := onFocus(t, &timedSession)
	r.press("b")
	r.typeText("30%")
	r.press("enter")
	if got := r.messageLine(); !strings.Contains(got, "baseline set to 30%") || !strings.Contains(got, "the 65% session in force keeps focus until it ends") {
		t.Errorf("message line = %q", got)
	}
}

func TestABadBaselineChangesNothing(t *testing.T) {
	r := onFocus(t, &session.StatusView{})
	r.press("b")
	r.typeText("banana")
	r.press("enter")
	if len(r.control.calls) != 0 || r.m.prompt == nil || r.m.prompt.err == "" {
		t.Errorf("calls %v prompt %+v, want the prompt kept open with a reason and nothing sent", r.control.calls, r.m.prompt)
	}
}

func TestFChangesTheLevelOfTheSessionInForce(t *testing.T) {
	r := onFocus(t, &timedSession)
	r.press("f")
	if r.m.prompt == nil || r.m.prompt.kind != promptLevel {
		t.Fatal("f did not open the level prompt")
	}
	r.typeText("deep")
	r.press("enter")
	if got := r.lastCall(); got != "level 90" {
		t.Errorf("worker call = %q", got)
	}
	if got := r.messageLine(); !strings.Contains(got, "level changed to 90% (deep)") {
		t.Errorf("message line = %q", got)
	}
}

func TestFWithNothingInForceSaysSoInsteadOfAsking(t *testing.T) {
	for _, status := range []*session.StatusView{nil, {}} {
		r := onFocus(t, status)
		r.press("f")
		if r.m.prompt != nil {
			t.Error("f opened a prompt with no session to change")
		}
		if got := r.messageLine(); !strings.Contains(got, "no focus session to change the level of") {
			t.Errorf("message line = %q", got)
		}
	}
}

func TestPAndSpacePauseAndResume(t *testing.T) {
	for _, k := range []string{"p", " "} {
		r := onFocus(t, &timedSession)
		r.press(k)
		if got := r.lastCall(); got != "pause" {
			t.Fatalf("%q on a running timer sent %q, want pause", k, got)
		}
		paused := timedSession
		paused.Layers = []session.LayerView{timedSession.Layers[0], timedSession.Layers[1]}
		paused.Layers[1].Paused = true
		r.control.status = paused
		r.press("p") // the status from the pause answer is what the page now believes
		r.press(k)
		if got := r.lastCall(); got != "resume" {
			t.Errorf("%q on a paused timer sent %q, want resume", k, got)
		}
	}
}

func TestPauseAndAdjustNeedATimedSessionAndARunningWorker(t *testing.T) {
	untimed := session.StatusView{Level: 65, Layers: []session.LayerView{{Kind: "escalation", Percent: 65}}}
	for name, status := range map[string]*session.StatusView{"untimed": &untimed, "no worker": nil, "focus off": {}} {
		r := onFocus(t, status)
		r.press("p", "+", "-")
		if len(r.control.calls) != 0 {
			t.Errorf("%s: sent %v", name, r.control.calls)
		}
		if r.ensured != 0 {
			t.Errorf("%s: a worker was started just to pause or adjust", name)
		}
		if got := r.messageLine(); !strings.Contains(got, "no timer") {
			t.Errorf("%s: message line = %q", name, got)
		}
	}
}

func TestPlusAndMinusAdjustTheTimeLeftByFiveMinutesAndCountsRepeatThat(t *testing.T) {
	r := onFocus(t, &timedSession)
	r.press("+")
	if got := r.lastCall(); got != "adjust 5m0s" {
		t.Errorf("+ sent %q", got)
	}
	r.press("-")
	if got := r.lastCall(); got != "adjust -5m0s" {
		t.Errorf("- sent %q", got)
	}
	r.press("3", "+")
	if got := r.lastCall(); got != "adjust 15m0s" {
		t.Errorf("3+ sent %q, want fifteen minutes", got)
	}
	r.press("2", "-")
	if got := r.lastCall(); got != "adjust -10m0s" {
		t.Errorf("2- sent %q", got)
	}
	if got := r.messageLine(); !strings.Contains(got, "time left −10m") {
		t.Errorf("message line = %q", got)
	}
	r.press("=")
	if got := r.lastCall(); got != "adjust 5m0s" {
		t.Errorf("= (plus without shift) sent %q", got)
	}
}

func TestXStopsTheSessionAndCapitalXAsksBeforeTurningEverythingOff(t *testing.T) {
	r := onFocus(t, &timedSession)
	r.control.status = session.StatusView{Level: 30, Layers: []session.LayerView{{Kind: "baseline", Percent: 30}}}
	r.press("x")
	if got := r.lastCall(); got != "stop all=false" {
		t.Errorf("x sent %q", got)
	}
	if got := r.messageLine(); !strings.Contains(got, "session stopped: back to 30%") {
		t.Errorf("message line = %q", got)
	}
	if got := r.view(); !strings.Contains(got, "Focus is on at 30%") {
		t.Errorf("the page should show what is left:\n%s", got)
	}

	calls := len(r.control.calls)
	r.press("X")
	if r.m.prompt == nil || r.m.prompt.kind != promptConfirmStopAll {
		t.Fatal("X should ask first")
	}
	if bar := r.promptBar(); !strings.Contains(bar, "baseline included?") {
		t.Errorf("the question should say what it will do: %q", bar)
	}
	r.press("j", "q", "x") // other keys do nothing and are not typed
	if r.m.prompt == nil || len(r.control.calls) != calls {
		t.Error("only y, n and Esc answer the question")
	}
	r.press("n")
	if r.m.prompt != nil || len(r.control.calls) != calls {
		t.Error("n should keep everything")
	}
	r.press("X", "esc")
	if r.m.prompt != nil || len(r.control.calls) != calls {
		t.Error("Esc should keep everything")
	}
	r.control.status = session.StatusView{}
	r.press("X", "y")
	if got := r.lastCall(); got != "stop all=true" {
		t.Errorf("y sent %q", got)
	}
	if got := r.view(); !strings.Contains(got, "Focus is off") {
		t.Errorf("the page should show that focus is off:\n%s", got)
	}
}

func TestRefusedCommandsAreExplainedInWords(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{
		{session.ErrNothingToStop, "no session to stop (X turns everything off"},
		{session.ErrNoTimer, "no timer to pause or adjust"},
		{session.ErrNotPaused, "not paused"},
		{session.ErrNoSession, "no focus session to change"},
		{session.ErrNotRunning, "shh is not running"},
		{errors.New("something odd"), "something odd"},
	} {
		r := onFocus(t, &timedSession)
		r.control.err = tt.err
		r.press("x")
		if got := r.messageLine(); !strings.Contains(got, tt.want) {
			t.Errorf("%v: message line = %q, want %q", tt.err, got, tt.want)
		}
		if got := r.view(); !strings.Contains(got, "Focus is on at 65%") {
			t.Errorf("%v: a refused command must leave the page as it was", tt.err)
		}
	}
}

func TestWithoutAControlTheFocusKeysSayTheyAreUnavailable(t *testing.T) {
	s, err := store.Open(":memory:")
	must(t, err)
	defer s.Close()
	m := New(Config{Data: s, Worker: &fakeWorker{view: timedSession}, Now: func() time.Time { return now }})
	r := &rig{t: t, store: s, m: m}
	r.refresh()
	r.press("f1", "x", "p", "+")
	if got := r.messageLine(); !strings.Contains(got, "focus control is not available") {
		t.Errorf("message line = %q", got)
	}
	r.press("s", "enter")
	if got := r.messageLine(); !strings.Contains(got, "focus control is not available") {
		t.Errorf("message line = %q", got)
	}
}

func TestFocusLettersKeepTheirOtherMeaningOnTheOtherTabs(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "x", 1)
	r.refresh()
	r.press("f2", "x")
	if !r.row(id).IsDismissedInbox {
		t.Error("x should still dismiss on the Notifications tab")
	}
	r.press("f3", "f")
	if !r.row(id).IsFlagged {
		t.Error("f should still flag on the Train tab")
	}
	r.press("s", "b", "p", "+", "-")
	if len(r.control.calls) != 0 || r.m.prompt != nil {
		t.Errorf("Focus keys acted on another tab: calls %v prompt %v", r.control.calls, r.m.prompt)
	}
}

func TestTheFocusPageIsDocumentedInTheHelp(t *testing.T) {
	r := newRig(t)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 80})
	r.press("?")
	got := helpText(r)
	for _, want := range []string{"Focus tab", "Start a session on top of what is in force", "75% 25m writing docs", "then off", "Pause the countdown",
		"A number first repeats it: 3+ adds fifteen", "Turn focus off completely, baseline included", "Change the level of the session in force"} {
		if !strings.Contains(got, want) {
			t.Errorf("the help never says %q", want)
		}
	}
}

// ---- the filter ----

func filterRig(t *testing.T) *rig {
	r := newRig(t)
	r.add("Ana Gomez", "the server is down", 1)
	r.add("Bo", "lunch on friday", 2, func(n *store.Notification) { n.Source = "Outlook"; n.Title = "Invitation" })
	r.add("Cy", "ANA asked about you", 3)
	r.add("Di", "nothing to see", 4, func(n *store.Notification) { n.Subtitle = "re: Ana's budget" })
	r.refresh()
	return r
}

func TestSlashFiltersBothListsByTextIgnoringCase(t *testing.T) {
	r := filterRig(t)
	r.press("/")
	if r.m.prompt == nil || r.m.prompt.kind != promptFilter {
		t.Fatal("/ did not open the filter prompt")
	}
	if bar := r.promptBar(); !strings.Contains(bar, "filter:") {
		t.Errorf("bar = %q", bar)
	}
	r.typeText("ana")
	r.press("enter")
	if got := senders(r); strings.Join(got, ",") != "Ana Gomez,Cy,Di" {
		t.Errorf("Notifications after filtering for ana = %v, want sender, message and subtitle matches (any case)", got)
	}
	r.press("f3")
	if got := senders(r); len(got) != 3 {
		t.Errorf("the Train list should be filtered too: %v", got)
	}
	view := r.view()
	if !strings.Contains(view, "/ ana") {
		t.Errorf("the filter should show beside the tabs:\n%s", view)
	}
	if !strings.Contains(view, "3 of 4 notifications · 3 of 4 to train") {
		t.Errorf("the counts should say how many are shown of how many:\n%s", view)
	}
}

func TestTheFilterAlsoMatchesTheAppAndTheTitle(t *testing.T) {
	r := filterRig(t)
	r.press("/")
	r.typeText("outlook")
	r.press("enter")
	if got := senders(r); strings.Join(got, ",") != "Bo" {
		t.Errorf("filtering for the app name = %v", got)
	}
	r.press("/")
	for i := 0; i < 7; i++ {
		r.press("backspace")
	}
	r.typeText("invitation")
	r.press("enter")
	if got := senders(r); strings.Join(got, ",") != "Bo" {
		t.Errorf("filtering for the title = %v", got)
	}
}

func TestEscAndAnEmptyFilterClearIt(t *testing.T) {
	r := filterRig(t)
	r.press("/")
	r.typeText("ana")
	r.press("enter", "esc")
	if r.m.filter != "" || len(senders(r)) != 4 {
		t.Errorf("Esc should clear the filter: %q, %v", r.m.filter, senders(r))
	}
	if got := r.messageLine(); !strings.Contains(got, "filter cleared") {
		t.Errorf("message line = %q", got)
	}
	r.press("/")
	r.typeText("bo")
	r.press("enter", "/")
	for i := 0; i < 2; i++ {
		r.press("backspace")
	}
	r.press("enter")
	if r.m.filter != "" || len(senders(r)) != 4 {
		t.Errorf("an empty filter should clear it: %q, %v", r.m.filter, senders(r))
	}
}

func TestTheFilterSurvivesRefreshesAndTheFilterPromptStartsFromIt(t *testing.T) {
	r := filterRig(t)
	r.press("/")
	r.typeText("ana")
	r.press("enter")
	r.add("Ed", "another ana mention", 0)
	r.add("Fy", "unrelated", 0)
	r.refresh()
	if got := senders(r); strings.Join(got, ",") != "Ed,Ana Gomez,Cy,Di" {
		t.Errorf("after a refresh = %v, want the new matching row in and the unrelated one out", got)
	}
	r.press("/")
	if r.m.prompt.text != "ana" {
		t.Errorf("the prompt should start from the current filter so it can be edited, got %q", r.m.prompt.text)
	}
	r.press("esc")
	if r.m.filter != "ana" {
		t.Error("cancelling the prompt should keep the filter")
	}
}

func TestEscClosesAnExpandedMessageBeforeItClearsTheFilter(t *testing.T) {
	r := filterRig(t)
	r.press("/")
	r.typeText("ana")
	r.press("enter", "e")
	if !r.m.expanded() {
		t.Fatal("setup")
	}
	r.press("esc")
	if r.m.expanded() || r.m.filter != "ana" {
		t.Errorf("first Esc: expanded %v filter %q, want the message closed and the filter kept", r.m.expanded(), r.m.filter)
	}
	r.press("esc")
	if r.m.filter != "" {
		t.Error("the next Esc should clear the filter")
	}
}

func TestSlashDoesNothingOnTheFocusTabAndAFilterWithNoMatchShowsAnEmptyList(t *testing.T) {
	r := filterRig(t)
	r.press("f1", "/")
	if r.m.prompt != nil {
		t.Error("/ opened a prompt on the Focus tab")
	}
	r.press("f2", "/")
	r.typeText("zzz")
	r.press("enter")
	if got := r.view(); !strings.Contains(got, "Nothing here yet") || !strings.Contains(got, "0 of 4 notifications") {
		t.Errorf("a filter with no match:\n%s", got)
	}
	r.press("j", "k", "y", "e", "o") // nothing selected: must not panic or act
	if len(r.trainer.requests) != 0 {
		t.Error("training with nothing selected")
	}
}

func TestEveryPromptsHintFitsAHundredColumnsWithoutBeingCut(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.worker.err, r.worker.view = nil, timedSession
	r.refresh()
	for _, open := range []struct {
		tab  string
		keys []string
	}{
		{"f1", []string{"s"}}, {"f1", []string{"b"}}, {"f1", []string{"f"}}, {"f1", []string{"X"}},
		{"f2", []string{"/"}}, {"f2", []string{"t"}},
	} {
		r.press(open.tab)
		r.press(open.keys...)
		if r.m.prompt == nil {
			t.Fatalf("%v did not open a prompt", open.keys)
		}
		hint := r.messageLine()
		if strings.Contains(hint, "…") || lipgloss.Width(hint) > 100 {
			t.Errorf("%v: the hint is cut off or too wide (%d): %q", open.keys, lipgloss.Width(hint), hint)
		}
		r.press("esc")
		if r.m.prompt != nil {
			r.press("n") // the yes or no question closes on n
		}
	}
}

// ---- the Focus tab's menu ----

// menuLine finds the menu row for a label on the Focus page.
func menuLine(r *rig, label string) string {
	for _, l := range strings.Split(r.view(), "\n") {
		if strings.Contains(l, label) {
			return l
		}
	}
	return ""
}

func TestTheFocusPageHasAMenuOfEverythingYouCanDoWithTheirKeysAndExplanations(t *testing.T) {
	r := onFocus(t, &timedSession)
	view := r.view()
	for _, want := range []string{
		"What to do", "Start a session…", "Set the baseline…", "Change the level…", "Pause", "Add 5 minutes", "Take away 5 minutes",
		"Stop this session", "Turn focus off…",
		"a level, a length and a note", "the standing level when no session is running", "freeze or restart the countdown",
		"focus falls back to what is under it", "everything, baseline included (asks first)",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the menu is missing %q:\n%s", want, view)
		}
	}
	for _, label := range []string{"Start a session…", "Set the baseline…", "Change the level…", "Pause", "Add 5 minutes", "Take away 5 minutes", "Stop this session", "Turn focus off…"} {
		if line := menuLine(r, label); !strings.Contains(line, " s ") && !strings.Contains(line, " b ") && !strings.Contains(line, " f ") &&
			!strings.Contains(line, " p ") && !strings.Contains(line, " + ") && !strings.Contains(line, " - ") && !strings.Contains(line, " x ") && !strings.Contains(line, " X ") {
			t.Errorf("%q has no key shown beside it: %q", label, line)
		}
	}
}

func TestJKSelectMenuRowsAndCountsAndGWork(t *testing.T) {
	r := onFocus(t, &timedSession)
	if line := menuLine(r, "Start a session…"); !strings.Contains(line, ">") {
		t.Errorf("the first row should be selected to begin with: %q", line)
	}
	r.press("j")
	if line := menuLine(r, "Set the baseline…"); !strings.Contains(line, ">") {
		t.Errorf("j should select the second row: %q", line)
	}
	r.press("3", "j")
	if line := menuLine(r, "Add 5 minutes"); !strings.Contains(line, ">") {
		t.Errorf("3j should go down three rows to Add 5 minutes: %q", line)
	}
	r.press("k")
	if line := menuLine(r, "Pause"); !strings.Contains(line, ">") {
		t.Errorf("k should go back up: %q", line)
	}
	r.press("G")
	if line := menuLine(r, "Turn focus off…"); !strings.Contains(line, ">") {
		t.Errorf("G should select the last row: %q", line)
	}
	r.press("9", "j")
	if r.m.focusCursor != len(r.m.focusItems())-1 {
		t.Errorf("moving past the end should stop at the last row, cursor %d", r.m.focusCursor)
	}
	r.press("g")
	if r.m.focusCursor != 0 {
		t.Errorf("g should select the first row, cursor %d", r.m.focusCursor)
	}
	r.press("up", "k")
	if r.m.focusCursor != 0 {
		t.Errorf("moving past the top should stop at the first row, cursor %d", r.m.focusCursor)
	}
	r.press("down")
	if r.m.focusCursor != 1 {
		t.Errorf("the down arrow should move too, cursor %d", r.m.focusCursor)
	}
}

func TestEnterRunsTheSelectedRowTheSameAsItsKey(t *testing.T) {
	type step struct {
		down  int
		check func(r *rig)
		name  string
	}
	steps := []step{
		{0, func(r *rig) {
			if r.m.prompt == nil || r.m.prompt.kind != promptStart {
				t.Error("Start a session… should open the start prompt")
			}
		}, "start"},
		{1, func(r *rig) {
			if r.m.prompt == nil || r.m.prompt.kind != promptBaseline {
				t.Error("Set the baseline… should open the baseline prompt")
			}
		}, "baseline"},
		{2, func(r *rig) {
			if r.m.prompt == nil || r.m.prompt.kind != promptLevel {
				t.Error("Change the level… should open the level prompt")
			}
		}, "level"},
		{3, func(r *rig) {
			if got := r.lastCall(); got != "pause" {
				t.Errorf("Pause sent %q", got)
			}
		}, "pause"},
		{4, func(r *rig) {
			if got := r.lastCall(); got != "adjust 5m0s" {
				t.Errorf("Add 5 minutes sent %q", got)
			}
		}, "add"},
		{5, func(r *rig) {
			if got := r.lastCall(); got != "adjust -5m0s" {
				t.Errorf("Take away 5 minutes sent %q", got)
			}
		}, "take away"},
		{6, func(r *rig) {
			if got := r.lastCall(); got != "stop all=false" {
				t.Errorf("Stop this session sent %q", got)
			}
		}, "stop"},
		{7, func(r *rig) {
			if r.m.prompt == nil || r.m.prompt.kind != promptConfirmStopAll {
				t.Error("Turn focus off… should ask first")
			}
		}, "stop all"},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			r := onFocus(t, &timedSession)
			r.press("g")
			for i := 0; i < st.down; i++ {
				r.press("j")
			}
			r.press("enter")
			st.check(r)
		})
	}
}

func TestACountBeforeEnterRepeatsTheTimeRows(t *testing.T) {
	r := onFocus(t, &timedSession)
	r.press("4", "j") // Add 5 minutes
	r.press("3", "enter")
	if got := r.lastCall(); got != "adjust 15m0s" {
		t.Errorf("3 then Enter on Add 5 minutes sent %q, want fifteen minutes", got)
	}
	r.press("j", "2", "enter")
	if got := r.lastCall(); got != "adjust -10m0s" {
		t.Errorf("2 then Enter on Take away sent %q, want ten minutes off", got)
	}
}

func TestRowsThatDoNotApplyAreDimmedWithTheReasonAndEnterExplainsInsteadOfActing(t *testing.T) {
	untimed := session.StatusView{Level: 65, Layers: []session.LayerView{{Kind: "escalation", Percent: 65}}}
	baselineOnly := session.StatusView{Level: 30, Layers: []session.LayerView{{Kind: "baseline", Percent: 30}}}
	tests := []struct {
		name   string
		status *session.StatusView
		row    string
		reason string
	}{
		{"no timer to pause", &untimed, "Pause", "the session in force has no timer"},
		{"no timer to add to", &untimed, "Add 5 minutes", "the session in force has no timer"},
		{"focus off: no level to change", &session.StatusView{}, "Change the level…", "focus is off"},
		{"no worker: nothing to stop", nil, "Stop this session", "shh is not running"},
		{"only a baseline: no session to stop", &baselineOnly, "Stop this session", "no session is running"},
		{"no worker: nothing to turn off", nil, "Turn focus off…", "shh is not running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := onFocus(t, tt.status)
			if line := menuLine(r, tt.row); !strings.Contains(line, "not now: "+tt.reason) {
				t.Errorf("%q should say why it is unavailable (%q): %q", tt.row, tt.reason, line)
			}
			// select it and press Enter
			for i, it := range r.m.focusItems() {
				if strings.HasPrefix(it.label, strings.TrimSuffix(tt.row, "…")) {
					r.m.focusCursor = i
				}
			}
			before := len(r.control.calls)
			r.press("enter")
			if len(r.control.calls) != before || r.m.prompt != nil {
				t.Errorf("Enter on an unavailable row did something: calls %v prompt %v", r.control.calls, r.m.prompt)
			}
			if got := r.messageLine(); !strings.Contains(got, tt.reason) {
				t.Errorf("Enter should explain: %q", got)
			}
		})
	}
}

func TestTheRowsThatAlwaysApplyAreAvailableWithNoWorker(t *testing.T) {
	r := onFocus(t, nil)
	for _, label := range []string{"Start a session…", "Set the baseline…"} {
		if line := menuLine(r, label); strings.Contains(line, "not now") {
			t.Errorf("%q should work with no worker (one is started): %q", label, line)
		}
	}
}

func TestThePauseRowBecomesResumeWhileAtimerIsPaused(t *testing.T) {
	r := onFocus(t, &timedSession)
	if menuLine(r, "Pause") == "" || strings.Contains(r.view(), "Resume") {
		t.Error("a running timer should offer Pause")
	}
	paused := timedSession
	paused.Layers = []session.LayerView{timedSession.Layers[0], timedSession.Layers[1]}
	paused.Layers[1].Paused = true
	r.worker.view = paused
	r.refresh()
	if !strings.Contains(r.view(), "Resume") {
		t.Errorf("a paused timer should offer Resume:\n%s", r.view())
	}
}

func TestTheShortcutKeysStillWorkWithoutSelectingTheRow(t *testing.T) {
	r := onFocus(t, &timedSession)
	r.press("G", "+") // the cursor is far from Add 5 minutes
	if got := r.lastCall(); got != "adjust 5m0s" {
		t.Errorf("+ sent %q with another row selected", got)
	}
	if r.m.focusCursor != len(r.m.focusItems())-1 {
		t.Error("using a shortcut should not move the selection")
	}
}

func TestTheMenuKeepsTheSelectedRowInViewOnAShortTerminal(t *testing.T) {
	r := onFocus(t, &timedSession)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 14}) // the stack takes most of what there is
	r.press("G")
	view := r.view()
	if !strings.Contains(view, "Turn focus off…") || !strings.Contains(view, ">") {
		t.Errorf("the selected last row should be on screen:\n%s", view)
	}
	if h := lipgloss.Height(view); h != 14 {
		t.Errorf("%d lines tall", h)
	}
	r.press("g")
	if v := r.view(); !strings.Contains(v, "Start a session…") {
		t.Errorf("the first row should come back into view:\n%s", v)
	}
}

func TestTheFocusPageFitsEverySize(t *testing.T) {
	for _, status := range []*session.StatusView{&timedSession, nil, {}} {
		r := onFocus(t, status)
		for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 12}, {40, 9}} {
			r.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, key := range []string{"g", "G"} {
				r.press(key)
				view := r.view()
				if h := lipgloss.Height(view); h != size[1] {
					t.Errorf("%dx%d: %d lines tall", size[0], size[1], h)
				}
				for n, line := range strings.Split(view, "\n") {
					if w := lipgloss.Width(line); w > size[0] {
						t.Errorf("%dx%d: line %d is %d wide: %q", size[0], size[1], n, w, line)
					}
				}
			}
		}
	}
}

func TestTheHelpExplainsTheMenu(t *testing.T) {
	r := newRig(t)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 80})
	r.press("?")
	got := helpText(r)
	for _, want := range []string{"Select a menu row and run it with Enter", "dimmed, with the reason", "3 then Enter on the menu row does the same", "a menu of everything you can do"} {
		if !strings.Contains(got, want) {
			t.Errorf("the help never says %q", want)
		}
	}
}

// rightPart is what is flush against the right edge of the message line: the text after the last run of two spaces.
func rightPart(line string) string {
	line = strings.TrimRight(line, " ")
	if i := strings.LastIndex(line, "  "); i >= 0 {
		return strings.TrimSpace(line[i:])
	}
	return strings.TrimSpace(line)
}

func TestAfterAnActionTheMessageLineShowsTheEquivalentCommandAtTheRight(t *testing.T) {
	tests := []struct {
		name string
		do   func(r *rig)
		msg  string
		cmd  string
	}{
		{"start", func(r *rig) { r.press("s"); r.typeText("75% 25m writing docs"); r.press("enter") }, "focus started at 75% for 25m", "$ shh 75% 25m writing docs"},
		{"start with then", func(r *rig) { r.press("s"); r.typeText("40% 10m then off"); r.press("enter") }, "focus started at 40%", "$ shh focus 40% 10m --then off"},
		{"start with nothing typed", func(r *rig) { r.press("s", "enter") }, "focus started at 65% (medium)", "$ shh 65%"},
		{"a note that looks like a setting", func(r *rig) { r.press("s"); r.typeText("50% 50% done"); r.press("enter") }, "", ""},
		{"baseline", func(r *rig) { r.press("b"); r.typeText("30%"); r.press("enter") }, "baseline set to 30%", "$ shh baseline 30%"},
		{"baseline off", func(r *rig) { r.press("b"); r.typeText("off"); r.press("enter") }, "baseline cleared", "$ shh baseline off"},
		{"pause", func(r *rig) { r.press("p") }, "paused", "$ shh pause"},
		{"stop", func(r *rig) { r.press("x") }, "session stopped", "$ shh stop"},
		{"stop all", func(r *rig) { r.press("X", "y") }, "focus is off", "$ shh stop --all"},
		{"change level", func(r *rig) { r.press("f"); r.typeText("deep"); r.press("enter") }, "level changed to 90% (deep)", "no shh command · TUI only"},
		{"add time", func(r *rig) { r.press("+") }, "time left +5m", "no shh command · TUI only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := onFocus(t, &timedSession)
			tt.do(r)
			line := r.messageLine()
			if tt.msg == "" {
				return // exercised by TestAStartNoteThatLooksLikeASettingIsQuoted
			}
			if !strings.Contains(line, tt.msg) {
				t.Errorf("message line %q should say %q", line, tt.msg)
			}
			if got := rightPart(line); got != tt.cmd {
				t.Errorf("right part = %q, want %q (line %q)", got, tt.cmd, line)
			}
			if i, j := strings.Index(line, tt.msg), strings.Index(line, tt.cmd); i < 0 || j < i {
				t.Errorf("the command should come after the message, at the right: %q", line)
			}
			if lipgloss.Width(line) > 100 {
				t.Errorf("the line is %d wide: %q", lipgloss.Width(line), line)
			}
		})
	}
}

func TestStartCommandsAreTheOnesTheCommandLineWouldTake(t *testing.T) {
	then := func(n int) *int { return &n }
	tests := []struct {
		level  int
		length time.Duration
		then   *int
		note   string
		want   string
	}{
		{65, 0, nil, "", "shh 65%"},
		{75, 25 * time.Minute, nil, "", "shh 75% 25m"},
		{75, 90 * time.Minute, nil, "writing docs", "shh 75% 1h30m writing docs"},
		{30, 90 * time.Second, nil, "", "shh 30% 1m30s"},
		{40, 10 * time.Minute, then(0), "", "shh focus 40% 10m --then off"},
		{40, 0, then(30), "x y", "shh focus 40% --then 30% x y"},
		{0, 25 * time.Minute, nil, "", "shh focus off 25m"},
		{75, 0, nil, "50% done", `shh 75% -m "50% done"`},
		{75, 0, nil, "deep work", "shh 75% deep work"},
		{75, 0, nil, "25m break", `shh 75% -m "25m break"`},
	}
	for _, tt := range tests {
		if got := startCommand(tt.level, tt.length, tt.then, tt.note); got != tt.want {
			t.Errorf("startCommand(%d, %v, %v, %q) = %q, want %q", tt.level, tt.length, tt.then, tt.note, got, tt.want)
		}
	}
}

func TestTheSelectedMenuRowShowsItsCommandAtTheRightBeforeAnythingIsRun(t *testing.T) {
	r := onFocus(t, &timedSession)
	want := map[string]string{
		"Start a session…": "$ shh 75% 25m writing docs", "Set the baseline…": "$ shh baseline 30%", "Change the level…": "no shh command · TUI only",
		"Pause": "$ shh pause", "Add 5 minutes": "no shh command · TUI only", "Take away 5 minutes": "no shh command · TUI only",
		"Stop this session": "$ shh stop", "Turn focus off…": "$ shh stop --all",
	}
	r.press("g")
	for _, it := range r.m.focusItems() {
		line := r.messageLine()
		if got := rightPart(line); got != want[it.label] {
			t.Errorf("%q selected: right part = %q, want %q (line %q)", it.label, got, want[it.label], line)
		}
		if !strings.Contains(line, "j/k select") {
			t.Errorf("%q selected: the hints should stay at the left: %q", it.label, line)
		}
		r.press("j")
	}
}

func TestPausedRowOffersTheResumeCommand(t *testing.T) {
	paused := timedSession
	paused.Layers = []session.LayerView{timedSession.Layers[0], timedSession.Layers[1]}
	paused.Layers[1].Paused = true
	r := onFocus(t, &paused)
	r.m.focusCursor = 3
	if got := rightPart(r.messageLine()); got != "$ shh resume" {
		t.Errorf("right part = %q, want the resume command while paused", got)
	}
}

func TestACommandIsNotShownForActionsThatAreNotFocusCommands(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "y")
	if got := r.messageLine(); strings.Contains(got, "$ shh") || strings.Contains(got, "TUI only") {
		t.Errorf("training is not a shh focus command and should show none: %q", got)
	}
	r.press("f1")
	r.press("f2", "o")
	if got := r.messageLine(); strings.Contains(got, "$ shh") {
		t.Errorf("opening an app should show no command: %q", got)
	}
}

func TestTheCommandStaysWholeOnANarrowTerminalAndTheMessageGivesWay(t *testing.T) {
	r := onFocus(t, &timedSession)
	r.m.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	r.press("p")
	line := r.messageLine()
	if lipgloss.Width(line) > 60 || !strings.Contains(line, "$ shh pause") {
		t.Errorf("on 60 columns the command should stay whole: %q", line)
	}
	r.m.Update(tea.WindowSizeMsg{Width: 24, Height: 14})
	r.press("x")
	if line := r.messageLine(); lipgloss.Width(line) > 24 {
		t.Errorf("on 24 columns the line must still fit: %q", line)
	}
}

func TestTheHelpMentionsTheCommandsShownAtTheRight(t *testing.T) {
	r := newRig(t)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 80})
	r.press("?")
	got := helpText(r)
	for _, want := range []string{"equivalent shh command", "at its right", "TUI only"} {
		if !strings.Contains(got, want) {
			t.Errorf("the help never says %q", want)
		}
	}
}

// readBack interprets a shown start command the way the shh command line does: the flags it has, then the shorthand rules.
func readBack(t *testing.T, cmd string) (level int, length time.Duration, then *int, note string) {
	t.Helper()
	words := strings.Fields(cmd)
	if len(words) < 2 || words[0] != "shh" {
		t.Fatalf("%q does not start with shh", cmd)
	}
	words = words[1:]
	if words[0] == "focus" {
		words = words[1:]
	}
	var flagNote string
	var rest []string
	for i := 0; i < len(words); i++ {
		switch words[i] {
		case "--then":
			v, err := decide.ParseThreshold(words[i+1])
			if err != nil {
				t.Fatalf("%q: --then %s: %v", cmd, words[i+1], err)
			}
			then = &v
			i++
		case "-m":
			// the rest of the line is the quoted note
			flagNote = strings.Trim(strings.Join(words[i+1:], " "), `"`)
			i = len(words)
		default:
			rest = append(rest, words[i])
		}
	}
	levelText, lengthText, remaining, err := decide.SplitFocusWords("", "", rest)
	if err != nil {
		t.Fatalf("%q: %v", cmd, err)
	}
	if levelText == "" {
		levelText = "medium"
	}
	if lengthText == "" {
		lengthText = "0"
	}
	if level, err = decide.ParseThreshold(levelText); err != nil {
		t.Fatalf("%q: %v", cmd, err)
	}
	if length, err = decide.ParseDuration(lengthText); err != nil {
		t.Fatalf("%q: %v", cmd, err)
	}
	note = strings.Join(remaining, " ")
	if flagNote != "" {
		note = flagNote
	}
	return level, length, then, note
}

func TestEveryShownStartCommandMeansWhatWasStarted(t *testing.T) {
	for _, text := range []string{
		"", "30%", "25m", "deep 5m", "5m deep reading", "deep work", "75% 25m writing docs then off", "light 1h30m then 40%",
		"60% 10m spaced out", "off 15m", "99% 90s",
	} {
		level, length, then, note, err := parseStart(text)
		if err != nil {
			t.Errorf("%q: %v", text, err)
			continue
		}
		gotLevel, gotLength, gotThen, gotNote := readBack(t, startCommand(level, length, then, note))
		thenText := func(p *int) string {
			if p == nil {
				return "-"
			}
			return fmt.Sprint(*p)
		}
		if gotLevel != level || gotLength != length || thenText(gotThen) != thenText(then) || gotNote != note {
			t.Errorf("%q: shown as %q, which reads back as level %d length %v then %s note %q; started level %d length %v then %s note %q",
				text, startCommand(level, length, then, note), gotLevel, gotLength, thenText(gotThen), gotNote, level, length, thenText(then), note)
		}
	}
}

func TestANoteThatStartsLikeASettingIsRejectedByTheStartPromptSinceTheCommandLineWouldReadItAsOne(t *testing.T) {
	for _, text := range []string{"75% 25m 50% done", "40% 10m 25m break"} {
		if _, _, _, _, err := parseStart(text); err == nil {
			t.Errorf("%q should be rejected: a second level or length is not a note", text)
		}
	}
}
