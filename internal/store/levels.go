package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type LevelKind string

const (
	LevelBaseline   LevelKind = "baseline"
	LevelEscalation LevelKind = "escalation"
)

// Level is one layer of the focus stack (plan.md section 3.1), stored in focus_sessions.
//
// A running timed level has ExpiresAt. A paused one has PausedAt and RemainingSecs instead. An indefinite one has neither.
type Level struct {
	ID       int64
	Kind     LevelKind
	ParentID int64 // the level beneath when this one started; 0 for a baseline
	Percent  int   // 0-99
	// ThenLevel is where to go when this level ends: nil means the layer beneath, 0 means off.
	ThenLevel     *int
	Note          string
	StartTime     time.Time
	EndTime       *time.Time // nil while active
	TargetMinutes int        // the requested length for display; 0 means indefinite
	ExpiresAt     *time.Time
	PausedAt      *time.Time
	RemainingSecs *int
	Blocked       int
	Allowed       int
}

const levelColumns = `id, kind, parent_id, focus_percentage, then_level, note, CAST(start_time AS TEXT), CAST(end_time AS TEXT),
	target_duration_minutes, CAST(expires_at AS TEXT), CAST(paused_at AS TEXT), remaining_secs, total_blocked, total_allowed`

// PushLevel starts a new active level on top of the stack and returns its id.
func (s *Store) PushLevel(l Level) (int64, error) {
	switch {
	case l.Kind != LevelBaseline && l.Kind != LevelEscalation:
		return 0, fmt.Errorf("store: unknown level kind %q", l.Kind)
	case l.StartTime.IsZero():
		return 0, errors.New("store: a level needs a start time")
	case l.Percent < 0 || l.Percent > 99:
		return 0, fmt.Errorf("store: level %d is outside 0-99", l.Percent)
	case l.ThenLevel != nil && (*l.ThenLevel < 0 || *l.ThenLevel > 99):
		return 0, fmt.Errorf("store: then level %d is outside 0-99", *l.ThenLevel)
	case (l.TargetMinutes > 0) != (l.ExpiresAt != nil):
		return 0, errors.New("store: a timed level needs both a duration and an expiry; an indefinite one needs neither")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var parent any
	if l.Kind == LevelEscalation {
		var top int64
		err := tx.QueryRow(`SELECT id FROM focus_sessions WHERE end_time IS NULL ORDER BY ` + stackRank + ` DESC, id DESC LIMIT 1`).Scan(&top)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		parent = nullID(top)
	}
	res, err := tx.Exec(`INSERT INTO focus_sessions
		(kind, parent_id, start_time, target_duration_minutes, expires_at, focus_percentage, then_level, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(l.Kind), parent, formatTime(l.StartTime), l.TargetMinutes, nullTime(l.ExpiresAt), l.Percent, l.ThenLevel, nullString(l.Note))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// The baseline is always at the bottom, even when it was set after an escalation started.
const stackRank = `CASE kind WHEN 'baseline' THEN 0 ELSE 1 END`

// ActiveLevels returns the stack bottom first; the last element is the top.
func (s *Store) ActiveLevels() ([]Level, error) {
	rows, err := s.db.Query(`SELECT ` + levelColumns + ` FROM focus_sessions WHERE end_time IS NULL ORDER BY ` + stackRank + `, id`)
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

func (s *Store) GetLevel(id int64) (Level, error) {
	l, err := scanLevel(s.db.QueryRow(`SELECT `+levelColumns+` FROM focus_sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Level{}, ErrNotFound
	}
	return l, err
}

// CloseLevel ends an active level wherever it is in the stack, for example a timer that expired below the top.
func (s *Store) CloseLevel(id int64, end time.Time) error {
	return s.updateActive(id, `UPDATE focus_sessions SET end_time = ? WHERE id = ? AND end_time IS NULL`, formatTime(end), id)
}

// PopLevel ends the topmost active escalation. The baseline is never popped; with no escalation it returns ErrNotFound.
func (s *Store) PopLevel(end time.Time) (Level, error) {
	var top int64
	err := s.db.QueryRow(`SELECT id FROM focus_sessions WHERE end_time IS NULL AND kind = 'escalation' ORDER BY id DESC LIMIT 1`).Scan(&top)
	if errors.Is(err, sql.ErrNoRows) {
		return Level{}, ErrNotFound
	}
	if err != nil {
		return Level{}, err
	}
	if err := s.CloseLevel(top, end); err != nil {
		return Level{}, err
	}
	return s.GetLevel(top)
}

// PauseLevel freezes a running timed level, storing the whole seconds left (rounded up, never negative).
func (s *Store) PauseLevel(id int64, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var expires, paused, ended sql.NullString
	err = tx.QueryRow(`SELECT CAST(expires_at AS TEXT), CAST(paused_at AS TEXT), CAST(end_time AS TEXT) FROM focus_sessions WHERE id = ?`, id).
		Scan(&expires, &paused, &ended)
	if errors.Is(err, sql.ErrNoRows) || ended.Valid {
		return fmt.Errorf("%w: level %d is not active", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if paused.Valid {
		return fmt.Errorf("store: level %d is already paused", id)
	}
	deadline, err := parseOptionalTime(expires)
	if err != nil {
		return err
	}
	if deadline == nil {
		return fmt.Errorf("store: level %d has no timer to pause", id)
	}
	left := max(deadline.Sub(now), 0)
	secs := int((left + time.Second - 1) / time.Second)
	if _, err := tx.Exec(`UPDATE focus_sessions SET paused_at = ?, remaining_secs = ?, expires_at = NULL WHERE id = ?`, formatTime(now), secs, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ResumeLevel restarts a paused level so it expires after the stored remaining seconds.
func (s *Store) ResumeLevel(id int64, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var paused, ended sql.NullString
	var remaining sql.NullInt64
	err = tx.QueryRow(`SELECT CAST(paused_at AS TEXT), remaining_secs, CAST(end_time AS TEXT) FROM focus_sessions WHERE id = ?`, id).
		Scan(&paused, &remaining, &ended)
	if errors.Is(err, sql.ErrNoRows) || ended.Valid {
		return fmt.Errorf("%w: level %d is not active", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if !paused.Valid {
		return fmt.Errorf("store: level %d is not paused", id)
	}
	expires := now.Add(time.Duration(remaining.Int64) * time.Second)
	if _, err := tx.Exec(`UPDATE focus_sessions SET expires_at = ?, paused_at = NULL, remaining_secs = NULL WHERE id = ?`, formatTime(expires), id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetThenLevel sets where the level hands over when it ends; nil means the layer beneath, 0 means off.
func (s *Store) SetThenLevel(id int64, then *int) error {
	if then != nil && (*then < 0 || *then > 99) {
		return fmt.Errorf("store: then level %d is outside 0-99", *then)
	}
	return s.update(id, `UPDATE focus_sessions SET then_level = ? WHERE id = ?`, then, id)
}

// SetExpiresAt moves the deadline of an active running timed level.
func (s *Store) SetExpiresAt(id int64, expires time.Time) error {
	return s.updateActive(id, `UPDATE focus_sessions SET expires_at = ? WHERE id = ? AND end_time IS NULL`, formatTime(expires), id)
}

// SetRemainingSecs changes the time left on an active paused level.
func (s *Store) SetRemainingSecs(id int64, secs int) error {
	if secs < 0 {
		return fmt.Errorf("store: remaining time %d is negative", secs)
	}
	return s.updateActive(id, `UPDATE focus_sessions SET remaining_secs = ? WHERE id = ? AND end_time IS NULL`, secs, id)
}

// SetPercent changes the threshold of an active level.
func (s *Store) SetPercent(id int64, percent int) error {
	if percent < 0 || percent > 99 {
		return fmt.Errorf("store: level %d is outside 0-99", percent)
	}
	return s.updateActive(id, `UPDATE focus_sessions SET focus_percentage = ? WHERE id = ? AND end_time IS NULL`, percent, id)
}

// AddCounts adds to the blocked and allowed counters of a level.
func (s *Store) AddCounts(id int64, blocked, allowed int) error {
	return s.update(id, `UPDATE focus_sessions SET total_blocked = total_blocked + ?, total_allowed = total_allowed + ? WHERE id = ?`, blocked, allowed, id)
}

func (s *Store) updateActive(id int64, query string, args ...any) error {
	if err := s.update(id, query, args...); err != nil {
		return fmt.Errorf("%w: level %d is not active", ErrNotFound, id)
	}
	return nil
}

func scanLevel(row scanner) (Level, error) {
	var (
		l                            Level
		kind, start                  string
		parent, then, remaining      sql.NullInt64
		note, ended, expires, paused sql.NullString
	)
	err := row.Scan(&l.ID, &kind, &parent, &l.Percent, &then, &note, &start, &ended,
		&l.TargetMinutes, &expires, &paused, &remaining, &l.Blocked, &l.Allowed)
	if err != nil {
		return Level{}, err
	}
	l.Kind, l.ParentID, l.Note = LevelKind(kind), parent.Int64, note.String
	if l.StartTime, err = parseTime(start); err != nil {
		return Level{}, err
	}
	for _, f := range []struct {
		from sql.NullString
		to   **time.Time
	}{{ended, &l.EndTime}, {expires, &l.ExpiresAt}, {paused, &l.PausedAt}} {
		if *f.to, err = parseOptionalTime(f.from); err != nil {
			return Level{}, err
		}
	}
	if then.Valid {
		v := int(then.Int64)
		l.ThenLevel = &v
	}
	if remaining.Valid {
		v := int(remaining.Int64)
		l.RemainingSecs = &v
	}
	return l, nil
}
