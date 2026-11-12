package session_test

import (
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

const day = 24 * time.Hour

// retentionRig holds a held back and a let through notification, unlabelled, plus a flagged one, all stored by the worker, and then
// lets time pass.
func retentionRig(t *testing.T, autoTrain bool) (*workerRig, map[string]int64) {
	t.Helper()
	r := newWorkerRig(t, func(c *session.Config) {
		c.Retention = store.Retention{TTL: 30 * day, KeepUntrained: 90 * day}
		c.AutoTrain, c.AutoTrainAfter = autoTrain, 20*day
	})
	if err := r.worker.SetBaseline(65); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	held := r.scoredAt("Bo", "lunch menu for friday")
	ids["held"] = held.ID
	let := r.scoredAt("Cy", "prod outage, everything is down, need you now")
	ids["let"] = let.ID
	flagged := r.scoredAt("Di", "weekly newsletter")
	r.store.SetFlagged(flagged.ID, true)
	ids["flagged"] = flagged.ID
	if held.Status != store.StatusBlocked || let.Status != store.StatusAlerted {
		t.Fatalf("setup: %s and %s", held.Status, let.Status)
	}
	return r, ids
}

func TestAutoTrainTakesWhatNobodyLabelledAsRightOnceItsTimeIsUp(t *testing.T) {
	r, ids := retentionRig(t, true)
	r.clock.Advance(10 * day)
	r.worker.Maintain()
	for name, id := range ids {
		if n, _ := r.store.GetNotification(id); n.IsTrained {
			t.Errorf("%s is only 10 days old: not yet", name)
		}
	}
	r.clock.Advance(15 * day)
	r.worker.Maintain()
	held, _ := r.store.GetNotification(ids["held"])
	let, _ := r.store.GetNotification(ids["let"])
	flagged, _ := r.store.GetNotification(ids["flagged"])
	if !held.IsTrained || !held.AutoTrained || held.UserFeedback != "n" {
		t.Errorf("a held back one is rightly held back (n): %+v", held)
	}
	if !let.IsTrained || !let.AutoTrained || let.UserFeedback != "y" {
		t.Errorf("a let through one is rightly let through (y): %+v", let)
	}
	if flagged.IsTrained {
		t.Error("a flagged one is left for the user")
	}
	if len(r.errs) != 0 {
		t.Errorf("errors: %v", r.errs)
	}
	// untraining one of them (the user disagrees) takes the auto mark away, and the next pass trains it again only if it is still unlabelled
	if err := r.store.Untrain(ids["held"]); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.store.GetNotification(ids["held"]); n.AutoTrained {
		t.Error("untrained rows are not auto trained")
	}
}

func TestAutoTrainIsOffByDefaultAndNothingIsTrainedBehindTheBack(t *testing.T) {
	r, ids := retentionRig(t, false)
	r.clock.Advance(25 * day)
	r.worker.Maintain()
	for name, id := range ids {
		if n, _ := r.store.GetNotification(id); n.IsTrained {
			t.Errorf("%s was trained with auto_train off", name)
		}
	}
}

func TestMaintainDeletesWhatIsPastItsTimeButKeepsWhatWaitsForReview(t *testing.T) {
	r, ids := retentionRig(t, false)
	r.clock.Advance(40 * day)
	r.worker.Maintain()
	if _, err := r.store.GetNotification(ids["let"]); err == nil {
		t.Error("a let through notification nobody reviewed goes at the time to live")
	}
	for _, name := range []string{"held", "flagged"} {
		if _, err := r.store.GetNotification(ids[name]); err != nil {
			t.Errorf("%s waits for review and stays longer: %v", name, err)
		}
	}
	r.clock.Advance(60 * day)
	r.worker.Maintain()
	if left, _ := r.store.CountAll(); left != 0 {
		t.Errorf("%d left after the longer time", left)
	}
}

func TestAutoTrainedRowsAreDeletedAtTheTimeToLiveLikeTrainedOnes(t *testing.T) {
	r, ids := retentionRig(t, true)
	r.clock.Advance(35 * day)
	r.worker.Maintain() // trains (it is past 20 days) and then deletes (past 30 days)
	if _, err := r.store.GetNotification(ids["held"]); err == nil {
		t.Error("an auto trained row is deleted at its time to live")
	}
	if _, err := r.store.GetNotification(ids["flagged"]); err != nil {
		t.Error("the flagged one stays")
	}
}

func TestTheHousekeepingRunsOnTheTickEverySixHours(t *testing.T) {
	r, ids := retentionRig(t, false)
	r.clock.Advance(40 * day)
	r.tick() // more than six hours since the last pass: it runs
	if _, err := r.store.GetNotification(ids["let"]); err == nil {
		t.Error("the tick should have run the housekeeping")
	}
}

func TestRetentionIsReadAgainEachPass(t *testing.T) {
	policy := store.Retention{TTL: 365 * day, KeepUntrained: 365 * day}
	r := newWorkerRig(t, func(c *session.Config) {
		c.RetentionNow = func() (store.Retention, bool, time.Duration) { return policy, false, 30 * day }
	})
	if err := r.worker.SetBaseline(65); err != nil {
		t.Fatal(err)
	}
	n := r.scoredAt("Cy", "prod outage, everything is down, need you now")
	r.clock.Advance(40 * day)
	r.worker.Maintain()
	if _, err := r.store.GetNotification(n.ID); err != nil {
		t.Fatal("kept for a year: still there")
	}
	policy = store.Retention{TTL: 30 * day, KeepUntrained: 90 * day} // the file was edited
	r.worker.Maintain()
	if _, err := r.store.GetNotification(n.ID); err == nil {
		t.Error("the edit should apply to the next pass")
	}
}
