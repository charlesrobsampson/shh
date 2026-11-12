package session

import (
	"errors"
	"fmt"

	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/store"
)

var (
	// ErrAlreadyTrained means the notification has a label already; undo it before labelling it again.
	ErrAlreadyTrained = errors.New("this notification is already trained")
	// ErrNothingToUndo means there is no training left to reverse.
	ErrNothingToUndo = classify.ErrNothingToUndo
	// ErrNoCheckpoint means there is no earlier state of the model to go back to.
	ErrNoCheckpoint = classify.ErrNoCheckpoint
	// ErrNoSuchNotification means no stored notification has that id.
	ErrNoSuchNotification = store.ErrNotFound
)

// TrainRequest labels one stored notification: an exact 0 to 100 score, or quick feedback ("y" it should have reached the user at the
// level it was decided at, "n" it was rightly held back).
type TrainRequest struct {
	NotificationID int64
	Label          *int
	Feedback       string
}

// TrainOutcome says what a training call did to the score the model gives that notification, on the 0 to 100 scale.
type TrainOutcome struct {
	NotificationID int64
	Before, After  int
	// Updated is false when the model already satisfied the label (a quick label whose constraint held), so nothing changed.
	Updated bool
}

// Train labels a stored notification and teaches the model from it. The notification's features are worked out again from its stored
// fields, as they were when it was decided, then the model takes the step and the row is marked trained. The worker owns the model, so
// every client (the terminal UI, the command line) trains through it and no copy of the model goes stale.
func (w *Worker) Train(req TrainRequest) (TrainOutcome, error) {
	n, err := w.cfg.Store.GetNotification(req.NotificationID)
	if err != nil {
		return TrainOutcome{}, err
	}
	if n.IsTrained {
		return TrainOutcome{}, ErrAlreadyTrained
	}
	var label classify.Label
	switch {
	case req.Label != nil && req.Feedback != "":
		return TrainOutcome{}, fmt.Errorf("%w: give an exact label or y/n, not both", ErrInvalid)
	case req.Label != nil:
		if *req.Label < 0 || *req.Label > 100 {
			return TrainOutcome{}, fmt.Errorf("%w: label %d is outside 0-100", ErrInvalid, *req.Label)
		}
		label = classify.ExactLabel(*req.Label)
	case req.Feedback == "y" || req.Feedback == "n":
		if n.ThresholdAtDecision == nil {
			return TrainOutcome{}, fmt.Errorf("%w: this notification does not record the level it was decided at, so y/n has nothing to compare with; give an exact label", ErrInvalid)
		}
		label = classify.QuickLabel(req.Feedback, *n.ThresholdAtDecision)
	default:
		return TrainOutcome{}, fmt.Errorf("%w: give an exact label or y/n", ErrInvalid)
	}

	vector := w.classifier.Features.Extract(classify.Input{
		Source: n.Source, BundleID: n.BundleID, Sender: n.Sender, Title: n.Title, Subtitle: n.Subtitle, Body: n.Body,
		Category: n.Category, Thread: n.Thread,
	})
	result, err := w.classifier.Model.Train(n.ID, vector, label)
	if err != nil {
		return TrainOutcome{}, err
	}
	if err := w.cfg.Store.Train(n.ID, store.Training{Feedback: req.Feedback, Label: req.Label}); err != nil {
		// The weights moved but the row did not follow: put the weights back so the two agree.
		if undoErr := w.classifier.Model.Undo(1); undoErr != nil {
			err = errors.Join(err, undoErr)
		}
		return TrainOutcome{}, err
	}
	return TrainOutcome{NotificationID: n.ID, Before: percent(result.Before), After: percent(result.After), Updated: result.Updated}, nil
}

// UndoTraining reverses the most recent training call exactly (weights and the row's flags) and says which notification it was.
func (w *Worker) UndoTraining() (TrainOutcome, error) {
	last, ok := w.classifier.Model.LastTraining()
	if !ok {
		return TrainOutcome{}, ErrNothingToUndo
	}
	if err := w.classifier.Model.Undo(1); err != nil {
		return TrainOutcome{}, err
	}
	if last.NotificationID != 0 {
		// The row may be gone (the log keeps the weights change either way); only a vanished row is ignored.
		if err := w.cfg.Store.Untrain(last.NotificationID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return TrainOutcome{}, err
		}
	}
	return TrainOutcome{NotificationID: last.NotificationID}, nil
}

// ResetModel forgets everything learned from the user and goes back to the seeded model. Notifications already labelled stay marked as
// trained (the labels are still theirs); a rollback brings the learned weights back.
func (w *Worker) ResetModel() error { return w.classifier.ResetToSeed() }

// RollbackModel goes back to the last checkpoint, taking back the training since, and marks the notifications that were trained
// then as untrained again. It says how many there were.
func (w *Worker) RollbackModel() (int, error) {
	entries, err := w.classifier.Model.RollbackEntries()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.NotificationID == 0 {
			continue
		}
		if err := w.cfg.Store.Untrain(e.NotificationID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return n, err
		}
		n++
	}
	return n, nil
}

func percent(p float64) int { return int(p*100 + 0.5) }
