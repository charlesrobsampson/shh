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
