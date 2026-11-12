package session

import (
	"errors"
	"fmt"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

// maintainEvery is how often the housekeeping runs: when the worker starts, and about this often after.
const maintainEvery = 6 * time.Hour

// autoTrainBatch bounds how much one pass trains, so a long-unused database does not hold the worker up. The rest waits for the next pass.
const autoTrainBatch = 500

// Maintain does the housekeeping: first it trains on the notifications that went unlabelled to the end of their time (when that is
// switched on), then it deletes the ones past their time to live. Problems are reported, not returned: housekeeping must never stop
// the worker.
func (w *Worker) Maintain() {
	now := w.cfg.Clock.Now()
	w.lastMaintained = now
	retention, autoTrain, after := w.cfg.Retention, w.cfg.AutoTrain, w.cfg.AutoTrainAfter
	if w.cfg.RetentionNow != nil {
		retention, autoTrain, after = w.cfg.RetentionNow()
	}
	if autoTrain && after > 0 {
		if _, err := w.autoTrain(now, after); err != nil {
			w.report(fmt.Errorf("training on notifications nobody labelled: %w", err))
		}
	}
	if retention.TTL > 0 {
		if _, err := w.cfg.Store.PurgeExpired(now, retention); err != nil {
			w.report(fmt.Errorf("deleting old notifications: %w", err))
		}
	}
}

func (w *Worker) report(err error) {
	if err != nil && w.cfg.OnError != nil {
		w.cfg.OnError(err)
	}
}

// autoTrain takes each unlabelled, unflagged notification older than AutoTrainAfter as handled correctly: one shh held back was rightly
// held back ("n"), one it let through was rightly let through ("y"). It says how many it trained. A notification that cannot be trained
// (no recorded level, say) is skipped, not retried for ever: it is left as it is and goes at its time to live.
func (w *Worker) autoTrain(now time.Time, after time.Duration) (int, error) {
	rows, err := w.cfg.Store.UnreviewedBefore(now.Add(-after), autoTrainBatch)
	if err != nil {
		return 0, err
	}
	trained := 0
	for _, n := range rows {
		feedback := "n"
		if n.Status == store.StatusAlerted {
			feedback = "y"
		}
		_, err := w.Train(TrainRequest{NotificationID: n.ID, Feedback: feedback})
		switch {
		case err == nil:
		case errors.Is(err, ErrAlreadyTrained), errors.Is(err, ErrInvalid), errors.Is(err, ErrNoSuchNotification):
			continue
		default:
			return trained, err
		}
		if err := w.cfg.Store.MarkAutoTrained(n.ID); err != nil {
			return trained, err
		}
		trained++
	}
	return trained, nil
}
