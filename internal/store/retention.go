package store

import "time"

// Retention is how long notifications are kept. The text of a notification is private, so nothing is kept forever.
type Retention struct {
	// TTL is how long a notification is kept at all.
	TTL time.Duration
	// KeepUntrained is how long a notification that is still waiting to be reviewed is kept past TTL: one shh held back, or one flagged,
	// that has not been labelled. It is a training candidate until then. Zero means no extension.
	KeepUntrained time.Duration
}

// expiredWhere selects what a purge at time now takes: older than TTL, except a notification still to review, which is older than
// KeepUntrained before it goes. One that shh let through and nobody reviewed goes at TTL, like anything else.
func (r Retention) expiredWhere(now time.Time) (string, []any) {
	ttlCut := formatTime(now.Add(-r.TTL))
	if r.KeepUntrained <= r.TTL {
		return `timestamp < ?`, []any{ttlCut}
	}
	keepCut := formatTime(now.Add(-r.KeepUntrained))
	return `timestamp < ? AND NOT (is_trained = 0 AND (status = 'blocked' OR is_flagged = 1) AND timestamp >= ?)`, []any{ttlCut, keepCut}
}

// CountExpired says how many notifications PurgeExpired would delete.
func (s *Store) CountExpired(now time.Time, r Retention) (int, error) {
	where, args := r.expiredWhere(now)
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE `+where, args...).Scan(&n)
	return n, err
}

// PurgeExpired deletes the notifications past their time, and says how many. What the model learned from them stays learned: the
// training log keeps its weights changes, only losing the link to the row. Unreadable calendar entries older than TTL go too.
func (s *Store) PurgeExpired(now time.Time, r Retention) (int, error) {
	where, args := r.expiredWhere(now)
	res, err := s.db.Exec(`DELETE FROM notifications WHERE `+where, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	_, err = s.db.Exec(`DELETE FROM unparsed_calendar WHERE datetime(created_at) < datetime(?)`, formatTime(now.Add(-r.TTL)))
	return int(n), err
}

// PurgeAll deletes every stored notification and every unreadable calendar entry, whatever its age: the way to wipe the text shh holds.
func (s *Store) PurgeAll() (int, error) {
	res, err := s.db.Exec(`DELETE FROM notifications`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	_, err = s.db.Exec(`DELETE FROM unparsed_calendar`)
	return int(n), err
}

// CountAll is how many notifications are stored.
func (s *Store) CountAll() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&n)
	return n, err
}

// UnreviewedBefore lists the notifications delivered before the time that nobody has labelled and that are not flagged (a flag means
// the user means to look), oldest first, up to limit (0 for all): the ones that are taken as handled correctly when their time is up.
func (s *Store) UnreviewedBefore(before time.Time, limit int) ([]Notification, error) {
	query := `SELECT ` + notificationColumns + ` FROM notifications WHERE is_trained = 0 AND is_flagged = 0 AND threshold_at_decision IS NOT NULL
		AND timestamp < ? ORDER BY timestamp`
	args := []any{formatTime(before)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkAutoTrained records that shh, not the user, trained the notification (it must be trained already).
func (s *Store) MarkAutoTrained(id int64) error {
	return s.update(id, `UPDATE notifications SET auto_trained = 1 WHERE id = ?`, id)
}
