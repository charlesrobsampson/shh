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

type rig struct {
	t       *testing.T
	store   *store.Store
	worker  *fakeWorker
	m       *Model
	next    int64
	opened  []string
	openErr error
	trainer *fakeTrainer
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
	r.m = New(Config{Data: s, Worker: r.worker, Now: func() time.Time { return now }, Trainer: r.trainer,
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
		{[]string{"tab"}, TabFocus},
		{[]string{"tab"}, TabNotifications},
		{[]string{"shift+tab"}, TabFocus},
		{[]string{"shift+tab"}, TabTrain},
		{[]string{"f1"}, TabFocus},
		{[]string{"f3"}, TabTrain},
		{[]string{"f2"}, TabNotifications},
		{[]string{"right"}, TabTrain},
		{[]string{"left"}, TabNotifications},
		{[]string{"l"}, TabTrain},
		{[]string{"h"}, TabNotifications},
		{[]string{"h"}, TabFocus},
		{[]string{"l"}, TabNotifications},
	} {
		r.press(step.keys...)
		if r.m.Tab() != step.want {
			t.Fatalf("after %v the tab is %v, want %v", step.keys, r.m.Tab(), step.want)
		}
	}
	for _, name := range []string{"Focus", "Notifications", "Train"} {
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
	if !strings.Contains(got, "baseline 30%") || !strings.Contains(got, "escalation 65% (medium) · 10m left") {
		t.Errorf("Focus tab should list the layers:\n%s", got)
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

func TestHelpOpensAndAnyKeyClosesIt(t *testing.T) {
	r := newRig(t)
	r.press("?")
	if got := r.view(); !strings.Contains(got, "shh keys") || !strings.Contains(got, "F1 F2 F3") {
		t.Errorf("help:\n%s", got)
	}
	r.press("j")
	if got := r.view(); strings.Contains(got, "shh keys") {
		t.Errorf("help is still open:\n%s", got)
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
	ana, bo := find("Ana"), find("Bo")
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
	if ana, bo = find("Ana"), find("Bo"); strings.HasPrefix(ana, ">") || !strings.HasPrefix(bo, ">") {
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
	r.press("f1", "e", "o", "d", "x", "enter")
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

func TestYAndNLabelTheSelectedRowAndItLeavesTheTrainList(t *testing.T) {
	for _, k := range []string{"y", "n"} {
		r := newRig(t)
		first := r.add("Ana", "x", 1, func(n *store.Notification) { n.Score = 0.60 })
		r.add("Bo", "x", 2, func(n *store.Notification) { n.Score = 0.70 })
		r.refresh()
		r.press("f3")
		if r.selectedSender() != "Ana" {
			t.Fatalf("setup: selected %q", r.selectedSender())
		}
		r.press(k)
		if len(r.trainer.requests) != 1 {
			t.Fatalf("%s: %d training requests, want 1", k, len(r.trainer.requests))
		}
		if got := r.trainer.requests[0]; got.NotificationID != first || got.Feedback != k || got.Label != nil {
			t.Errorf("%s: request = %+v", k, got)
		}
		if r.ensured != 1 {
			t.Errorf("%s: the worker was ensured %d times, want once before training", k, r.ensured)
		}
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
	r.press("f3", "y")
	if got := r.view(); !strings.Contains(got, "should have alerted: score 42 → 71") {
		t.Errorf("status bar:\n%s", got)
	}
	r.add("Bo", "x", 2)
	r.refresh()
	r.trainer.outcome = session.TrainView{Before: 30, After: 30}
	r.press("n")
	if got := r.view(); !strings.Contains(got, "the model already agreed (score 30), nothing to change") {
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

func TestADigitStartsTheLabelPromptWithThatDigit(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "x", 1)
	r.refresh()
	r.press("f3", "9")
	if r.m.prompt == nil || r.m.prompt.text != "9" {
		t.Fatalf("prompt = %+v, want it open and holding 9", r.m.prompt)
	}
	r.press("0", "enter")
	if got := r.trainer.requests; len(got) != 1 || got[0].Label == nil || *got[0].Label != 90 {
		t.Errorf("requests = %+v, want an exact label of 90", got)
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
	r.press("f1", "y", "n", "t", "u", "5", "enter")
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

func TestHelpListsTheTrainingKeys(t *testing.T) {
	r := newRig(t)
	r.press("?")
	got := r.view()
	for _, want := range []string{"y / n", "t, or a digit", "undo"} {
		if !strings.Contains(got, want) {
			t.Errorf("help does not mention %q:\n%s", want, got)
		}
	}
}
