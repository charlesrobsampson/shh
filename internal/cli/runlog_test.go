package cli_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

// logRig is a rig whose history can be read: the commands that read it are given the rig's own store.
func logRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, false)
	r.env.WithStore = func(do func(*store.Store) error) error { return do(r.store) }
	historyHours = 0
	r.env.Now = func() time.Time { return t0.Add(10 * time.Hour) }
	return r
}

// run puts a finished run in the history, with one notification flagged during it.
// Runs do not overlap, so each takes the next hour.
var historyHours time.Duration

func (r *rig) historyRun(routine string, flagged bool) int64 {
	r.t.Helper()
	start := t0.Add(historyHours)
	historyHours += 2 * time.Hour
	id, err := r.store.StartRoutineRun(routine, start, 2)
	if err != nil {
		r.t.Fatal(err)
	}
	threshold := 65
	nid, _, err := r.store.InsertNotification(store.Notification{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "x", Timestamp: start.Add(5 * time.Minute), Status: store.StatusBlocked, SourceRecID: id, ThresholdAtDecision: &threshold})
	if err != nil {
		r.t.Fatal(err)
	}
	if flagged {
		if err := r.store.SetFlagged(nid, true); err != nil {
			r.t.Fatal(err)
		}
	}
	if err := r.store.AddRoutineRunStep(id, 0, "work", 0, start); err != nil {
		r.t.Fatal(err)
	}
	if err := r.store.EndRoutineRun(id, start.Add(25*time.Minute), "finished", 3, 1, 1500); err != nil {
		r.t.Fatal(err)
	}
	n := 0
	if flagged {
		n = 1
	}
	if err := r.store.SetRoutineRunFlagged(id, n); err != nil {
		r.t.Fatal(err)
	}
	return id
}

func TestRoutineLogListsRunsWithWhatWasHeldAndFlagged(t *testing.T) {
	r := logRig(t)
	r.historyRun("morning", true)
	r.historyRun("reading", false)
	out := r.mustRun("routine", "log")
	for _, want := range []string{"Runs of routines, newest first", "morning", "reading", "25m", "1 of 2 steps", "held 3, let 1", "flagged 1 (to review)", "1 flagged notification still to review: shh ui --tab train"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log is missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "reading") > strings.Index(out, "morning") {
		t.Errorf("newest first:\n%s", out)
	}
	if one := r.mustRun("routine", "log", "MORNING"); strings.Contains(one, "reading") || !strings.Contains(one, "morning") {
		t.Errorf("one routine, ignoring case:\n%s", one)
	}
	if n := r.mustRun("routine", "log", "-n", "1"); strings.Contains(n, "morning") {
		t.Errorf("a count of one is the newest only:\n%s", n)
	}
}

func TestRoutineLogSaysWhenThereAreNoRuns(t *testing.T) {
	r := logRig(t)
	if out := r.mustRun("routine", "log"); !strings.Contains(out, "No routine has run yet") {
		t.Errorf("out = %q", out)
	}
	r.historyRun("morning", false)
	if out := r.mustRun("routine", "log", "nothing"); !strings.Contains(out, "No runs of nothing yet") {
		t.Errorf("out = %q", out)
	}
}

func TestRoutineLogRunShowsTheFlaggedNotificationsToReview(t *testing.T) {
	r := logRig(t)
	id := r.historyRun("morning", true)
	out := r.mustRun("routine", "log", "--run", itoa(id))
	for _, want := range []string{"Run " + itoa(id) + ": morning", "(finished)", "1 of 2 steps in 25m; held back 3 notifications, let through 1.", "still to review (1)", "Teams · Ana", "Review them: shh ui --tab train"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run is missing %q:\n%s", want, out)
		}
	}
	r2 := logRig(t)
	id2 := r2.historyRun("quiet", false)
	if out := r2.mustRun("routine", "log", "--run", itoa(id2)); !strings.Contains(out, "Nothing was flagged during it.") {
		t.Errorf("out:\n%s", out)
	}
	if code := r2.run("routine", "log", "--run", "999"); code == 0 || !strings.Contains(r2.err.String(), "no run number 999") {
		t.Errorf("code %d stderr %q", code, r2.err.String())
	}
}

func TestReviewedNotificationsStopCountingAsToReview(t *testing.T) {
	r := logRig(t)
	id := r.historyRun("morning", true)
	list, _ := r.store.ListNotifications(store.Filter{})
	if err := r.store.Train(list[0].ID, store.Training{Feedback: "y"}); err != nil {
		t.Fatal(err)
	}
	out := r.mustRun("routine", "log")
	if !strings.Contains(out, "flagged 1 (all reviewed)") || strings.Contains(out, "still to review") {
		t.Errorf("out:\n%s", out)
	}
	if out = r.mustRun("routine", "log", "--run", itoa(id)); !strings.Contains(out, "1 notification flagged during it, all reviewed since.") {
		t.Errorf("out:\n%s", out)
	}
}

func TestRoutineLogNeedsTheHistoryToBeReadable(t *testing.T) {
	r := newRig(t, false)
	if code := r.run("routine", "log"); code == 0 || !strings.Contains(r.err.String(), "cannot be read from here") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestUITakesATabAndOpensOnIt(t *testing.T) {
	r := newRig(t, false)
	var tabs []string
	r.env.IsTerminal = true
	r.env.UI = func(tab string) error { tabs = append(tabs, tab); return nil }
	r.mustRun("ui", "--tab", "train")
	r.mustRun("ui")
	if len(tabs) != 2 || tabs[0] != "train" || tabs[1] != "" {
		t.Errorf("tabs = %q", tabs)
	}
	if code := r.run("ui", "--tab", "nonsense"); code != 2 {
		t.Errorf("code %d: an unknown tab is a usage error", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
