package session_test

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

func exact(n int) session.TrainRequest { return session.TrainRequest{Label: &n} }

func withID(req session.TrainRequest, id int64) session.TrainRequest {
	req.NotificationID = id
	return req
}

// scoredAt posts a notification from the sender and returns the stored row, advancing the clock so bursts do not interfere.
func (r *workerRig) scoredAt(sender, body string) store.Notification {
	r.t.Helper()
	before := len(r.notifications())
	r.clock.Advance(10 * time.Minute)
	r.source.Add(ingest.Notification{BundleID: teams, Title: sender, Body: body, Delivered: r.clock.Now().Add(-time.Second)})
	r.tick()
	list := r.notifications()
	if len(list) != before+1 {
		r.t.Fatalf("stored %d notifications, want %d", len(list), before+1)
	}
	return list[0] // newest first
}

func newTrainRig(t *testing.T) *workerRig {
	t.Helper()
	r := newWorkerRig(t)
	if err := r.worker.SetBaseline(65); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTheStoredRowKeepsTheTitleSubtitleAndCategoryAsSentSoFeaturesCanBeRecomputed(t *testing.T) {
	r := newTrainRig(t)
	r.source.Add(ingest.Notification{
		BundleID: outlook, Title: "Ana", Subtitle: "Re: budget", Body: "see attached", Category: "OLAlertsNotificationCategory",
		Thread: ingest.ThreadDirect, Delivered: r.clock.Now().Add(-time.Second),
	})
	r.tick()
	n := r.onlyNotification()
	if n.Title != "Ana" || n.Subtitle != "Re: budget" || n.Category != "OLAlertsNotificationCategory" || n.Thread != "direct" {
		t.Errorf("stored title %q subtitle %q category %q thread %q, want each as the app sent it", n.Title, n.Subtitle, n.Category, n.Thread)
	}
}

func TestAnExactLabelMovesTheScoreTowardItAndLeavesTheRowTrained(t *testing.T) {
	r := newTrainRig(t)
	first := r.scoredAt("Ana", "lunch menu for friday")
	out, err := r.worker.Train(withID(exact(95), first.ID))
	if err != nil {
		t.Fatal(err)
	}
	if out.After <= out.Before || !out.Updated {
		t.Errorf("outcome = %+v, want the score to rise toward 95", out)
	}
	if out.Before != int(first.Score*100+0.5) {
		t.Errorf("before = %d, want the score the notification was decided with (%d)", out.Before, int(first.Score*100+0.5))
	}
	row, _ := r.store.GetNotification(first.ID)
	if !row.IsTrained || row.UserLabel == nil || *row.UserLabel != 95 || row.UserFeedback != "" {
		t.Errorf("row after training = trained %v label %v feedback %q", row.IsTrained, row.UserLabel, row.UserFeedback)
	}
	// A similar new notification now scores closer to the label than the first one did.
	similar := r.scoredAt("Ana", "lunch menu for monday")
	if math.Abs(similar.Score*100-95) >= math.Abs(first.Score*100-95) {
		t.Errorf("a similar notification scores %.0f, no closer to 95 than the original %.0f", similar.Score*100, first.Score*100)
	}
}

func TestQuickLabelsFollowTheConstraintAtTheLevelTheRowWasDecidedAt(t *testing.T) {
	r := newTrainRig(t)
	muted := r.scoredAt("Ana", "lunch menu for friday") // blocked at 65%
	if muted.Status != store.StatusBlocked || *muted.ThresholdAtDecision != 65 {
		t.Fatalf("setup: %+v", muted)
	}
	out, err := r.worker.Train(withID(session.TrainRequest{Feedback: "y"}, muted.ID))
	if err != nil {
		t.Fatal(err)
	}
	if out.After < 65 || !out.Updated {
		t.Errorf("y on a blocked row: %+v, want the score at or above the 65%% it was decided at", out)
	}
	row, _ := r.store.GetNotification(muted.ID)
	if !row.IsTrained || row.UserFeedback != "y" || row.UserLabel != nil {
		t.Errorf("row = %+v", row)
	}

	r2 := newTrainRig(t)
	loud := r2.scoredAt("Ops", "prod outage, need you now")
	if loud.Status != store.StatusAlerted {
		t.Fatalf("setup: %+v", loud)
	}
	out, err = r2.worker.Train(withID(session.TrainRequest{Feedback: "y"}, loud.ID))
	if err != nil {
		t.Fatal(err)
	}
	if out.Updated || out.After != out.Before {
		t.Errorf("y on a row that already clears the threshold changed the model: %+v", out)
	}
	out, err = r2.worker.Train(withID(session.TrainRequest{Feedback: "n"}, loud.ID))
	if !errors.Is(err, session.ErrAlreadyTrained) {
		t.Fatalf("a second label on a trained row: err = %v, want ErrAlreadyTrained", err)
	}
	third := r2.scoredAt("Ops", "prod outage on the database now")
	out, err = r2.worker.Train(withID(session.TrainRequest{Feedback: "n"}, third.ID))
	if err != nil {
		t.Fatal(err)
	}
	if out.After > 65 || !out.Updated {
		t.Errorf("n on an alerted row: %+v, want the score pushed to at or below 65", out)
	}
}

func TestUndoRestoresTheWeightsAndTheRowExactly(t *testing.T) {
	r := newTrainRig(t)
	target := r.scoredAt("Ana", "lunch menu for friday")
	probeBefore := r.scoredAt("Bo", "lunch menu for monday")
	if _, err := r.worker.Train(withID(exact(90), target.ID)); err != nil {
		t.Fatal(err)
	}
	probeTrained := r.scoredAt("Bo", "lunch menu for monday")
	if probeTrained.Score <= probeBefore.Score {
		t.Fatalf("setup: training should have raised similar scores (%v then %v)", probeBefore.Score, probeTrained.Score)
	}
	out, err := r.worker.UndoTraining()
	if err != nil || out.NotificationID != target.ID {
		t.Fatalf("undo = %+v, %v; want it to name the notification %d", out, err, target.ID)
	}
	row, _ := r.store.GetNotification(target.ID)
	if row.IsTrained || row.UserLabel != nil || row.UserFeedback != "" {
		t.Errorf("the row still looks trained after undo: %+v", row)
	}
	probeAfter := r.scoredAt("Bo", "lunch menu for monday")
	if math.Abs(probeAfter.Score-probeBefore.Score) > 1e-9 {
		t.Errorf("score after undo %v, want exactly the untrained %v", probeAfter.Score, probeBefore.Score)
	}
	if _, err := r.worker.UndoTraining(); !errors.Is(err, session.ErrNothingToUndo) {
		t.Errorf("a second undo: err = %v, want ErrNothingToUndo", err)
	}
}

func TestUndoGoesNewestFirst(t *testing.T) {
	r := newTrainRig(t)
	a := r.scoredAt("Ana", "lunch menu for friday")
	b := r.scoredAt("Bo", "team photo on thursday")
	r.worker.Train(withID(exact(80), a.ID))
	r.worker.Train(withID(exact(20), b.ID))
	out, err := r.worker.UndoTraining()
	if err != nil || out.NotificationID != b.ID {
		t.Fatalf("first undo = %+v, %v; want the newest (%d)", out, err, b.ID)
	}
	if rowA, _ := r.store.GetNotification(a.ID); !rowA.IsTrained {
		t.Error("undoing the newest must not touch the older training")
	}
	out, err = r.worker.UndoTraining()
	if err != nil || out.NotificationID != a.ID {
		t.Fatalf("second undo = %+v, %v; want %d", out, err, a.ID)
	}
}

func TestBadTrainingRequestsChangeNothing(t *testing.T) {
	r := newTrainRig(t)
	n := r.scoredAt("Ana", "lunch menu for friday")
	n101, minus := 101, -1
	tests := []struct {
		name string
		req  session.TrainRequest
		want error
	}{
		{"an unknown notification", withID(exact(50), 9999), session.ErrNoSuchNotification},
		{"neither label nor feedback", session.TrainRequest{NotificationID: n.ID}, session.ErrInvalid},
		{"both", session.TrainRequest{NotificationID: n.ID, Label: &n101, Feedback: "y"}, session.ErrInvalid},
		{"a label above 100", withID(session.TrainRequest{Label: &n101}, n.ID), session.ErrInvalid},
		{"a negative label", withID(session.TrainRequest{Label: &minus}, n.ID), session.ErrInvalid},
		{"feedback that is not y or n", session.TrainRequest{NotificationID: n.ID, Feedback: "maybe"}, session.ErrInvalid},
	}
	probe := func() float64 { return r.scoredAt("Bo", "lunch menu for friday").Score }
	before := probe()
	for _, tt := range tests {
		if _, err := r.worker.Train(tt.req); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
	row, _ := r.store.GetNotification(n.ID)
	if row.IsTrained {
		t.Error("a rejected request marked the row trained")
	}
	if after := probe(); math.Abs(after-before) > 1e-9 {
		t.Errorf("a rejected request changed the model: %v -> %v", before, after)
	}
	if _, err := r.worker.UndoTraining(); !errors.Is(err, session.ErrNothingToUndo) {
		t.Errorf("nothing was trained, but undo says %v", err)
	}
}

func TestTrainingUsesTheSubtitleCategoryAndThreadStoredWithTheRow(t *testing.T) {
	// Two rows with the same words but different thread kinds must be trained with different features: the one trained as a
	// meeting chat should not move the score of a plain direct message nearly as much as one trained as direct.
	trainAs := func(thread ingest.Thread) float64 {
		r := newTrainRig(t)
		r.clock.Advance(time.Minute)
		r.source.Add(ingest.Notification{BundleID: teams, Title: "Ana", Body: "sounds good to me", Thread: thread, Delivered: r.clock.Now().Add(-time.Second)})
		r.tick()
		n := r.onlyNotification()
		if n.Thread != string(thread) {
			t.Fatalf("stored thread %q", n.Thread)
		}
		if _, err := r.worker.Train(withID(exact(100), n.ID)); err != nil {
			t.Fatal(err)
		}
		probe := r.scoredAt("Zed", "sounds good to me") // a direct message from someone else (no thread kind given)
		return probe.Score
	}
	if direct, meeting := trainAs(ingest.ThreadDirect), trainAs(ingest.ThreadMeeting); direct <= meeting {
		t.Errorf("training a direct row moved the probe to %v, a meeting row to %v: the stored thread kind was not used", direct, meeting)
	}
}

func TestTrainingErrorsNameTheirCauseForTheClient(t *testing.T) {
	r := newTrainRig(t)
	_, err := r.worker.Train(session.TrainRequest{NotificationID: 12345, Feedback: "y"})
	if err == nil || !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(session.ErrAlreadyTrained.Error(), "already trained") {
		t.Errorf("message = %q", session.ErrAlreadyTrained)
	}
}
