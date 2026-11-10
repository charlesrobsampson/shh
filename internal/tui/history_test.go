package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

// historyOf puts a finished run in the rig's store with one flagged notification that arrived during it (or none).
func (r *rig) historyOf(routine string, hoursAgo int, flagged bool) int64 {
	r.t.Helper()
	start := now.Add(-time.Duration(hoursAgo) * time.Hour)
	id, err := r.store.StartRoutineRun(routine, start, 2)
	if err != nil {
		r.t.Fatal(err)
	}
	threshold := 65
	nid, _, err := r.store.InsertNotification(store.Notification{Source: "Teams", Sender: "Ana", Title: "Standup", Body: "x", Timestamp: start.Add(5 * time.Minute), Status: store.StatusBlocked,
		SourceRecID: id + 1000, ThresholdAtDecision: &threshold})
	if err != nil {
		r.t.Fatal(err)
	}
	n := 0
	if flagged {
		n = 1
		if err := r.store.SetFlagged(nid, true); err != nil {
			r.t.Fatal(err)
		}
	}
	if err := r.store.AddRoutineRunStep(id, 0, "work", 0, start); err != nil {
		r.t.Fatal(err)
	}
	if err := r.store.EndRoutineRun(id, start.Add(25*time.Minute), "finished", 4, 1, 1500); err != nil {
		r.t.Fatal(err)
	}
	if err := r.store.SetRoutineRunFlagged(id, n); err != nil {
		r.t.Fatal(err)
	}
	return id
}

func TestHShowsThePastRunsWithWhatWasFlaggedAndHOrEscGoesBack(t *testing.T) {
	r, _ := routinesRig(t)
	r.historyOf("morning", 5, true)
	r.historyOf("reading", 2, false)
	r.press("H")
	if r.m.history == nil {
		t.Fatal("H should open the history")
	}
	v := r.view()
	for _, want := range []string{"Runs of routines", "reading", "morning", "held 4, let 1", "⚑ 1 to review", "Run ", "nothing was flagged during it"} {
		if !strings.Contains(v, want) {
			t.Errorf("the history should show %q:\n%s", want, v)
		}
	}
	if strings.Index(v, "reading") > strings.Index(v, "morning") {
		t.Errorf("newest first:\n%s", v)
	}
	r.press("j")
	if v := r.view(); !strings.Contains(v, "still to review (1)") || !strings.Contains(v, "Teams · Ana · Standup") {
		t.Errorf("the selected run lists what is flagged:\n%s", v)
	}
	if h := r.m.messageLine(); !strings.Contains(h, "R review what was flagged") {
		t.Errorf("hint = %q", h)
	}
	r.press("H")
	if r.m.history != nil {
		t.Error("H again goes back")
	}
	r.press("H", "esc")
	if r.m.history != nil {
		t.Error("Esc goes back")
	}
}

func TestRInTheHistoryOpensTheTrainTabWhereFlaggedOnesLead(t *testing.T) {
	r, _ := routinesRig(t)
	r.historyOf("morning", 5, true)
	r.press("H", "R")
	if r.m.history != nil || r.m.Tab() != TabTrain || !strings.Contains(r.m.notice, "Train tab") {
		t.Errorf("history %v tab %v notice %q", r.m.history, r.m.Tab(), r.m.notice)
	}
}

func TestReviewedRunsSayAllReviewedAndAnEmptyHistorySaysSo(t *testing.T) {
	r, _ := routinesRig(t)
	r.press("H")
	if v := r.view(); !strings.Contains(v, "no routine has run yet") {
		t.Errorf("view:\n%s", v)
	}
	r.press("esc")
	id := r.historyOf("morning", 5, true)
	_ = id
	list, _ := r.store.ListNotifications(store.Filter{})
	if err := r.store.Train(list[0].ID, store.Training{Feedback: "y"}); err != nil {
		t.Fatal(err)
	}
	r.press("H")
	if v := r.view(); !strings.Contains(v, "flagged 1 (reviewed)") || !strings.Contains(v, "1 flagged during it, all reviewed since") {
		t.Errorf("view:\n%s", v)
	}
}

func TestTheRoutinesTabSaysWhenFlaggedNotificationsWaitIfTheRoutineAsksForTheUI(t *testing.T) {
	r, _ := routinesRig(t)
	last := &session.RoutineSummary{Name: "morning", Reason: "finished", Steps: 4, ElapsedSecs: 3600, Flagged: 2, ToReview: 2, Where: []string{"ui"}, LogWhere: []string{"ui"}}
	r.worker.view = session.StatusView{LastRoutine: last}
	r.refresh()
	if v := r.view(); !strings.Contains(v, "⚑ 2 flagged during morning to review") || !strings.Contains(v, "R: Train tab · H: all runs") {
		t.Errorf("view:\n%s", v)
	}
	r.press("R")
	if r.m.Tab() != TabTrain {
		t.Errorf("R should go to the Train tab: %v", r.m.Tab())
	}
	// not asked for: no line, and R says there is nothing to review
	last.LogWhere = nil
	r.press("$")
	r.refresh()
	if v := r.view(); strings.Contains(v, "⚑ 2 flagged during morning") {
		t.Errorf("the routine did not ask for it:\n%s", v)
	}
	last.ToReview = 0
	last.LogWhere = []string{"ui"}
	r.refresh()
	r.press("R")
	if r.m.Tab() != TabRoutines || !strings.Contains(r.m.notice, "nothing flagged") {
		t.Errorf("tab %v notice %q", r.m.Tab(), r.m.notice)
	}
}

func TestTheEditorsLogRowCyclesWhereToReportAndSavesIt(t *testing.T) {
	r, path, _ := editorRigText(t, withTriggers)
	r.press("e")
	for i := 0; i < 6; i++ {
		r.press("j") // name, description, repeat, waiting, summary, style, then log
	}
	if row := r.m.editor.current(); row.kind != rowLog {
		t.Fatalf("row = %+v", row)
	}
	if v := r.view(); !strings.Contains(v, "Log") || !strings.Contains(v, "nowhere") {
		t.Errorf("view:\n%s", v)
	}
	r.press("enter")
	if len(r.m.editor.draft.Log) != 1 || r.m.editor.draft.Log[0] != "banner" || !strings.Contains(r.m.notice, "log: banner") {
		t.Errorf("log = %v notice %q", r.m.editor.draft.Log, r.m.notice)
	}
	r.press("enter", "enter") // menu, ui
	if got := addCommand(r.m.editor.draft, true); !strings.Contains(got, "--log ui") {
		t.Errorf("command = %s", got)
	}
	r.press("s")
	rs := mustLoad(t, *path)
	morning, _ := findRoutine(rs, "morning")
	if len(morning.Log) != 1 || morning.Log[0] != "ui" {
		t.Errorf("saved log = %v", morning.Log)
	}
}

func TestTheUIOpensOnTheTabItIsGiven(t *testing.T) {
	r := newRig(t)
	for name, want := range map[string]Tab{"train": TabTrain, "Routines": TabRoutines, "focus": TabFocus, "notifications": TabNotifications, "": TabNotifications, "nonsense": TabNotifications} {
		cfg := r.m.cfg
		cfg.StartTab = name
		m := New(cfg)
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
		if m.Tab() != want {
			t.Errorf("StartTab %q opened on %v, want %v", name, m.Tab(), want)
		}
	}
}

func mustLoad(t *testing.T, path string) []routine.Routine {
	t.Helper()
	rs, err := routine.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func findRoutine(rs []routine.Routine, name string) (routine.Routine, bool) {
	return routine.Find(rs, name)
}

// ---- v: held back / let through ----

func (r *rig) addWithStatus(sender string, status store.Status, minutesAgo int) int64 {
	return r.add(sender, "message "+sender, minutesAgo, func(n *store.Notification) { n.Status = status })
}

func TestVShowsOnlyWhatWasHeldBackThenOnlyWhatWasLetThroughThenEverything(t *testing.T) {
	r := newRig(t)
	r.addWithStatus("Held One", store.StatusBlocked, 3)
	r.addWithStatus("Held Two", store.StatusBlocked, 2)
	r.addWithStatus("Let Through", store.StatusAlerted, 1)
	r.refresh()
	if got := len(r.m.lists[TabNotifications]); got != 3 {
		t.Fatalf("everything: %d", got)
	}
	r.press("v")
	if got := len(r.m.lists[TabNotifications]); got != 2 || r.m.show != store.StatusBlocked {
		t.Fatalf("held back only: %d rows, show %q", got, r.m.show)
	}
	for _, n := range r.m.lists[TabNotifications] {
		if n.Status != store.StatusBlocked {
			t.Errorf("a notification that was not held back is listed: %+v", n)
		}
	}
	if bar := r.m.tabBar(); !strings.Contains(bar, "held back only") {
		t.Errorf("the tab bar should say what is shown: %q", bar)
	}
	if !strings.Contains(r.m.notice, "n on one that should have reached you") {
		t.Errorf("notice = %q", r.m.notice)
	}
	if hints := r.m.messageLine(); r.m.notice == "" && !strings.Contains(hints, "n = it should have reached me") {
		t.Errorf("hints = %q", hints)
	}
	r.press("v")
	if got := len(r.m.lists[TabNotifications]); got != 1 || r.m.show != store.StatusAlerted || !strings.Contains(r.m.tabBar(), "let through only") {
		t.Errorf("let through only: %d rows, show %q, bar %q", got, r.m.show, r.m.tabBar())
	}
	r.press("v")
	if got := len(r.m.lists[TabNotifications]); got != 3 || r.m.show != "" || strings.Contains(r.m.tabBar(), "only") {
		t.Errorf("everything again: %d rows, show %q", got, r.m.show)
	}
}

func TestVAppliesToTheTrainTabToAndEscShowsEverythingAgain(t *testing.T) {
	r := newRig(t)
	r.addWithStatus("Held", store.StatusBlocked, 3)
	r.addWithStatus("Let", store.StatusAlerted, 1)
	r.refresh()
	r.press("f3", "v")
	if got := len(r.m.lists[TabTrain]); got != 1 || r.m.lists[TabTrain][0].Sender != "Held" {
		t.Fatalf("the Train tab shows only what was held back: %+v", r.m.lists[TabTrain])
	}
	if hints := r.m.keyHints(); !strings.Contains(hints, "n = it should have reached me") {
		t.Errorf("hints = %q", hints)
	}
	r.press("esc")
	if r.m.show != "" || len(r.m.lists[TabTrain]) != 2 {
		t.Errorf("Esc shows everything again: show %q rows %d", r.m.show, len(r.m.lists[TabTrain]))
	}
}

func TestNOnAHeldBackNotificationSaysItShouldHaveReachedTheUser(t *testing.T) {
	r := newRig(t)
	id := r.addWithStatus("Held", store.StatusBlocked, 3)
	r.refresh()
	r.press("v", "n")
	if len(r.trainer.requests) != 1 || r.trainer.requests[0].NotificationID != id || r.trainer.requests[0].Feedback != "y" {
		t.Errorf("requests = %+v: n on a held back notification means it should have been alerted, which is feedback y", r.trainer.requests)
	}
}

func TestVDoesNothingOnTheFocusAndRoutinesTabsAndIsOnTheStateLineOfTheListTabs(t *testing.T) {
	r := newRig(t)
	r.press("f1", "v")
	if r.m.show != "" {
		t.Error("v does nothing on the Focus tab")
	}
	r.press("f4", "v")
	if r.m.show != "" {
		t.Error("v does nothing on the Routines tab")
	}
	r.press("f2")
	if state := r.promptBar(); !strings.Contains(state, "v view") {
		t.Errorf("state line = %q", state)
	}
	r.press("f1")
	if state := r.promptBar(); strings.Contains(state, "v view") {
		t.Errorf("the Focus tab has no view to change: %q", state)
	}
}
