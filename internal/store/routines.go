package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrRoutineActive means a routine is already running: only one runs at a time.
var ErrRoutineActive = errors.New("store: a routine is already running")

// RoutineState says whether a run is going through a step or waiting for the user to start the next one.
type RoutineState string

const (
	RoutineRunning RoutineState = "running"
	RoutineWaiting RoutineState = "waiting"
)

// RoutineRun is one run of a routine, active or finished.
type RoutineRun struct {
	ID        int64
	Routine   string
	StartedAt time.Time
	EndedAt   *time.Time
	EndReason string // finished, stopped, replaced or missing; empty while active
	// Position is the step the run is at, counting from 0 in the routine's plan; StepsTotal is how many steps the plan had.
	Position   int
	StepsTotal int
	State      RoutineState
	// LevelID is the focus level of the current step, or of the hold while waiting; 0 when there is none.
	LevelID int64
	// Blocked, Allowed and ActiveSecs total what the run did, filled in when it ends.
	Blocked, Allowed, ActiveSecs int
}

// RoutineRunStep is a step a run has started.
type RoutineRunStep struct {
	RunID     int64
	Position  int
	Name      string
	LevelID   int64
	StartedAt time.Time
}

const runColumns = `id, routine, CAST(started_at AS TEXT), CAST(ended_at AS TEXT), end_reason, position, steps_total, state, level_id, blocked, allowed, active_secs`

// StartRoutineRun records a new run at its first step. It returns ErrRoutineActive if one is running already.
func (s *Store) StartRoutineRun(routine string, now time.Time, stepsTotal int) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO routine_runs (routine, started_at, steps_total) VALUES (?, ?, ?)`, routine, formatTime(now), stepsTotal)
	if err != nil {
		var active sql.NullInt64
		if s.db.QueryRow(`SELECT id FROM routine_runs WHERE ended_at IS NULL`).Scan(&active) == nil {
			return 0, ErrRoutineActive
		}
		return 0, err
	}
	return res.LastInsertId()
}

// ActiveRoutineRun returns the run in progress, if there is one.
func (s *Store) ActiveRoutineRun() (RoutineRun, bool, error) {
	run, err := scanRun(s.db.QueryRow(`SELECT ` + runColumns + ` FROM routine_runs WHERE ended_at IS NULL`))
	if errors.Is(err, sql.ErrNoRows) {
		return RoutineRun{}, false, nil
	}
	return run, err == nil, err
}

// SetRoutineRunProgress moves an active run to a step and a state, and says which focus level now belongs to it (0 for none).
func (s *Store) SetRoutineRunProgress(id int64, position int, state RoutineState, levelID int64) error {
	return s.update(id, `UPDATE routine_runs SET position = ?, state = ?, level_id = ? WHERE id = ? AND ended_at IS NULL`, position, string(state), nullID(levelID), id)
}

// AddRoutineRunStep notes that a run started a step on a focus level.
func (s *Store) AddRoutineRunStep(runID int64, position int, name string, levelID int64, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO routine_run_steps (run_id, position, name, level_id, started_at) VALUES (?, ?, ?, ?, ?)`,
		runID, position, name, nullID(levelID), formatTime(at))
	return err
}

// RoutineRunLevels returns the focus levels a run has used, oldest first, so its totals can be added up.
func (s *Store) RoutineRunLevels(runID int64) ([]Level, error) {
	rows, err := s.db.Query(`SELECT `+levelColumns+` FROM focus_sessions WHERE id IN (SELECT level_id FROM routine_run_steps WHERE run_id = ? AND level_id IS NOT NULL) ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Level
	for rows.Next() {
		l, err := scanLevel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// EndRoutineRun finishes the active run with a reason and the totals of what it did.
func (s *Store) EndRoutineRun(id int64, now time.Time, reason string, blocked, allowed, activeSecs int) error {
	return s.update(id, `UPDATE routine_runs SET ended_at = ?, end_reason = ?, blocked = ?, allowed = ?, active_secs = ?, level_id = NULL WHERE id = ? AND ended_at IS NULL`,
		formatTime(now), reason, blocked, allowed, activeSecs, id)
}

// RoutineRuns lists runs, newest first; limit 0 means all.
func (s *Store) RoutineRuns(limit int) ([]RoutineRun, error) {
	query := `SELECT ` + runColumns + ` FROM routine_runs ORDER BY id DESC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutineRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// RoutineRunSteps lists the steps a run has started, in order.
func (s *Store) RoutineRunSteps(runID int64) ([]RoutineRunStep, error) {
	rows, err := s.db.Query(`SELECT run_id, position, name, level_id, CAST(started_at AS TEXT) FROM routine_run_steps WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutineRunStep
	for rows.Next() {
		var (
			st    RoutineRunStep
			level sql.NullInt64
			at    string
		)
		if err := rows.Scan(&st.RunID, &st.Position, &st.Name, &level, &at); err != nil {
			return nil, err
		}
		st.LevelID = level.Int64
		var err error
		if st.StartedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func scanRun(row scanner) (RoutineRun, error) {
	var (
		run     RoutineRun
		started string
		ended   sql.NullString
		reason  sql.NullString
		state   string
		level   sql.NullInt64
	)
	if err := row.Scan(&run.ID, &run.Routine, &started, &ended, &reason, &run.Position, &run.StepsTotal, &state, &level,
		&run.Blocked, &run.Allowed, &run.ActiveSecs); err != nil {
		return RoutineRun{}, err
	}
	var err error
	if run.StartedAt, err = parseTime(started); err != nil {
		return RoutineRun{}, fmt.Errorf("routine run %d: %w", run.ID, err)
	}
	if run.EndedAt, err = parseOptionalTime(ended); err != nil {
		return RoutineRun{}, fmt.Errorf("routine run %d: %w", run.ID, err)
	}
	run.EndReason, run.State, run.LevelID = reason.String, RoutineState(state), level.Int64
	return run, nil
}
