package session

import (
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

// RunStore is the part of the store the log reads: the store itself, or anything that answers the same.
type RunStore interface {
	RoutineRuns(limit int) ([]store.RoutineRun, error)
	GetRoutineRun(id int64) (store.RoutineRun, bool, error)
	RoutineRunSteps(runID int64) ([]store.RoutineRunStep, error)
	FlaggedBetween(from, to time.Time) ([]store.FlaggedNotification, error)
}

// RunLog is one run of a routine as the log shows it. The runs are kept in the store, so the log can be read with no worker running.
type RunLog struct {
	ID      int64
	Routine string
	Started time.Time
	// Ended is nil for a run still going.
	Ended   *time.Time
	Reason  string // finished, stopped, until, missing; empty while it runs
	Steps   int    // how many steps were started
	Total   int    // out of how many
	Elapsed time.Duration
	Held    int
	Let     int
	// Flagged is how many notifications were flagged for review during the run (counted when it ended) and ToReview how many of them
	// still wait: a flag goes when the notification is trained.
	Flagged  int
	ToReview int
}

// FlaggedItem is a notification flagged during a run, in brief.
type FlaggedItem struct {
	ID     int64
	Source string
	Sender string
	Title  string
	At     time.Time
}

// RunLogs lists runs, newest first, of one routine (ignoring case) or of all when routine is empty. limit 0 means all. now ends the
// reckoning for a run still going.
func RunLogs(st RunStore, routine string, limit int, now time.Time) ([]RunLog, error) {
	runs, err := st.RoutineRuns(0)
	if err != nil {
		return nil, err
	}
	var out []RunLog
	for _, run := range runs {
		if routine != "" && !strings.EqualFold(run.Routine, routine) {
			continue
		}
		l, err := logOf(st, run, now)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// RunLogOf is one run of the log by its id; false if there is none.
func RunLogOf(st RunStore, id int64, now time.Time) (RunLog, bool, error) {
	run, ok, err := st.GetRoutineRun(id)
	if err != nil || !ok {
		return RunLog{}, false, err
	}
	l, err := logOf(st, run, now)
	return l, err == nil, err
}

func logOf(st RunStore, run store.RoutineRun, now time.Time) (RunLog, error) {
	l := RunLog{ID: run.ID, Routine: run.Routine, Started: run.StartedAt, Ended: run.EndedAt, Reason: run.EndReason, Total: run.StepsTotal,
		Held: run.Blocked, Let: run.Allowed, Flagged: run.Flagged}
	end := now
	if run.EndedAt != nil {
		end = *run.EndedAt
		l.Elapsed = time.Duration(run.ActiveSecs) * time.Second
	} else {
		l.Elapsed = now.Sub(run.StartedAt)
	}
	steps, err := st.RoutineRunSteps(run.ID)
	if err != nil {
		return RunLog{}, err
	}
	started := map[int]bool{}
	for _, s := range steps {
		started[s.Position] = true
	}
	l.Steps = len(started)
	items, err := st.FlaggedBetween(run.StartedAt, end)
	if err != nil {
		return RunLog{}, err
	}
	l.ToReview = len(items)
	if run.EndedAt == nil {
		l.Flagged = l.ToReview // still going: what is flagged so far
	}
	return l, nil
}

// RunFlagged lists the notifications that are flagged and arrived during a run, oldest first: what is left to review of that run.
func RunFlagged(st RunStore, id int64, now time.Time) ([]FlaggedItem, error) {
	run, ok, err := st.GetRoutineRun(id)
	if err != nil || !ok {
		return nil, err
	}
	end := now
	if run.EndedAt != nil {
		end = *run.EndedAt
	}
	items, err := st.FlaggedBetween(run.StartedAt, end)
	if err != nil {
		return nil, err
	}
	out := make([]FlaggedItem, 0, len(items))
	for _, it := range items {
		out = append(out, FlaggedItem{ID: it.ID, Source: it.Source, Sender: it.Sender, Title: it.Title, At: it.Timestamp})
	}
	return out, nil
}
