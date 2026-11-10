package store

import (
	"database/sql"
	"errors"
	"time"
)

// ScheduleEvent is what was done about one start of one routine's trigger: Kind is "start" or "reminder", Outcome says what came of it
// ("started", "asked", "skipped", "skipped: busy", "missed", ...).
type ScheduleEvent struct {
	Routine string
	StartAt time.Time
	Kind    string
	Outcome string
	At      time.Time
}

// RecordScheduleEvent notes what was done about a start, and reports whether it was the first note: a start that has one already is not
// done again, whether by a restarted worker or a second tick. It does not change an existing note.
func (s *Store) RecordScheduleEvent(routine string, start time.Time, kind, outcome string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO schedule_events (routine, start_at, kind, outcome, at) VALUES (?, ?, ?, ?, ?)`,
		routine, formatTime(start.UTC()), kind, outcome, formatTime(at))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetScheduleEvent notes what was done about a start, replacing what was noted before (a start that was asked about and then answered).
func (s *Store) SetScheduleEvent(routine string, start time.Time, kind, outcome string, at time.Time) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO schedule_events (routine, start_at, kind, outcome, at) VALUES (?, ?, ?, ?, ?)`,
		routine, formatTime(start.UTC()), kind, outcome, formatTime(at))
	return err
}

// ScheduleEventFor returns what was noted about a start, if anything was.
func (s *Store) ScheduleEventFor(routine string, start time.Time, kind string) (ScheduleEvent, bool, error) {
	var (
		e      ScheduleEvent
		when   string
		raised string
	)
	err := s.db.QueryRow(`SELECT routine, CAST(start_at AS TEXT), kind, outcome, CAST(at AS TEXT) FROM schedule_events WHERE routine = ? AND start_at = ? AND kind = ?`,
		routine, formatTime(start.UTC()), kind).Scan(&e.Routine, &when, &e.Kind, &e.Outcome, &raised)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduleEvent{}, false, nil
	}
	if err != nil {
		return ScheduleEvent{}, false, err
	}
	if e.StartAt, err = parseTime(when); err != nil {
		return ScheduleEvent{}, false, err
	}
	if e.At, err = parseTime(raised); err != nil {
		return ScheduleEvent{}, false, err
	}
	return e, true, nil
}

// DeleteScheduleEvent forgets what was noted about a start (taking back a skip), and reports whether there was anything.
func (s *Store) DeleteScheduleEvent(routine string, start time.Time, kind string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM schedule_events WHERE routine = ? AND start_at = ? AND kind = ?`, routine, formatTime(start.UTC()), kind)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PruneScheduleEvents forgets notes about starts before the given time, which nothing can ask about any more.
func (s *Store) PruneScheduleEvents(before time.Time) error {
	_, err := s.db.Exec(`DELETE FROM schedule_events WHERE start_at < ?`, formatTime(before.UTC()))
	return err
}
