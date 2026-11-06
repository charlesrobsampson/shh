package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("store: not found")

type Status string

const (
	StatusBlocked Status = "blocked"
	StatusAlerted Status = "alerted"
)

// Notification mirrors a row. Optional integers that can legitimately be 0 are pointers; ids use 0 for "none".
type Notification struct {
	ID        int64
	Source    string
	Sender    string
	Title     string
	Body      string
	Timestamp time.Time
	Status    Status
	// Score is the model's base score and Boost the time-sensitivity boost, both on the stored 0-1 scale.
	Score    float64
	Boost    float64
	Reason   string
	BundleID string
	// SourceRecID is the macOS notification record id; 0 when unknown.
	SourceRecID int64
	// ThresholdAtDecision is the effective level (0-99) when the notification was decided.
	ThresholdAtDecision *int
	SessionID           int64
	Summary             string

	IsRead           bool
	IsDismissedInbox bool
	IsTrained        bool
	UserFeedback     string // "y", "n" or empty
	UserLabel        *int   // exact 0-100 label, nil for quick feedback or untrained
}

// View selects one of the TUI lists.
type View int

const (
	ViewAll   View = iota
	ViewInbox      // not dismissed
	ViewTrain      // not trained
)

type Filter struct {
	View   View
	Source string
	// Since keeps rows delivered at or after it; the zero time means no lower bound.
	Since  time.Time
	Status Status
	// MinScore is on the 0-100 display scale and compares against the rounded base score.
	MinScore int
	Limit    int
}

// Training is either quick feedback ("y" or "n") or an exact label, never both.
type Training struct {
	Feedback string
	Label    *int
}

const notificationColumns = `id, source, sender, title, body, CAST(timestamp AS TEXT), status, predicted_score, time_sensitivity_boost,
	reason, bundle_id, source_rec_id, threshold_at_decision, session_id, summary,
	is_read, is_dismissed_inbox, is_trained, user_feedback, user_label`

// InsertNotification stores n and reports whether it was new. A row with the same macOS record id and delivery time is ignored.
func (s *Store) InsertNotification(n Notification) (id int64, inserted bool, err error) {
	if n.ThresholdAtDecision == nil {
		return 0, false, errors.New("store: a notification must record the threshold it was decided at")
	}
	res, err := s.db.Exec(`INSERT INTO notifications
		(source, sender, title, body, timestamp, status, predicted_score, time_sensitivity_boost,
		 reason, bundle_id, source_rec_id, threshold_at_decision, session_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		n.Source, n.Sender, nullString(n.Title), n.Body, formatTime(n.Timestamp), string(n.Status), n.Score, n.Boost,
		nullString(n.Reason), nullString(n.BundleID), nullID(n.SourceRecID), *n.ThresholdAtDecision, nullID(n.SessionID))
	if err != nil {
		return 0, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return 0, false, err
	}
	id, err = res.LastInsertId()
	return id, true, err
}

func (s *Store) GetNotification(id int64) (Notification, error) {
	n, err := scanNotification(s.db.QueryRow(`SELECT `+notificationColumns+` FROM notifications WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Notification{}, ErrNotFound
	}
	return n, err
}

// ListNotifications returns matching rows newest first.
func (s *Store) ListNotifications(f Filter) ([]Notification, error) {
	var where []string
	var args []any
	switch f.View {
	case ViewInbox:
		where = append(where, "is_dismissed_inbox = 0")
	case ViewTrain:
		where = append(where, "is_trained = 0")
	}
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if !f.Since.IsZero() {
		where = append(where, "timestamp >= ?")
		args = append(args, formatTime(f.Since))
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.MinScore > 0 {
		where = append(where, "CAST(ROUND(predicted_score * 100) AS INTEGER) >= ?")
		args = append(args, f.MinScore)
	}
	query := `SELECT ` + notificationColumns + ` FROM notifications`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY timestamp DESC, id DESC"
	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
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

func (s *Store) SetRead(id int64, read bool) error {
	return s.update(id, `UPDATE notifications SET is_read = ? WHERE id = ?`, read, id)
}

func (s *Store) SetDismissed(id int64, dismissed bool) error {
	return s.update(id, `UPDATE notifications SET is_dismissed_inbox = ? WHERE id = ?`, dismissed, id)
}

func (s *Store) SetSummary(id int64, summary string) error {
	return s.update(id, `UPDATE notifications SET summary = ? WHERE id = ?`, nullString(summary), id)
}

// Train marks the row trained and records the feedback; it does not touch the inbox flag.
func (s *Store) Train(id int64, t Training) error {
	quick := t.Feedback == "y" || t.Feedback == "n"
	switch {
	case quick == (t.Label != nil):
		return errors.New("store: training needs exactly one of quick feedback (y/n) or an exact label")
	case t.Label != nil && (*t.Label < 0 || *t.Label > 100):
		return fmt.Errorf("store: label %d is outside 0-100", *t.Label)
	}
	var feedback any
	if quick {
		feedback = t.Feedback
	}
	return s.update(id, `UPDATE notifications SET is_trained = 1, user_feedback = ?, user_label = ? WHERE id = ?`, feedback, t.Label, id)
}

func (s *Store) Untrain(id int64) error {
	return s.update(id, `UPDATE notifications SET is_trained = 0, user_feedback = NULL, user_label = NULL WHERE id = ?`, id)
}

func (s *Store) update(id int64, query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanNotification(row scanner) (Notification, error) {
	var (
		n                                        Notification
		status, stamp                            string
		title, reason, bundle, summary, feedback sql.NullString
		recID, threshold, session, label         sql.NullInt64
	)
	err := row.Scan(&n.ID, &n.Source, &n.Sender, &title, &n.Body, &stamp, &status, &n.Score, &n.Boost,
		&reason, &bundle, &recID, &threshold, &session, &summary,
		&n.IsRead, &n.IsDismissedInbox, &n.IsTrained, &feedback, &label)
	if err != nil {
		return Notification{}, err
	}
	if n.Timestamp, err = parseTime(stamp); err != nil {
		return Notification{}, err
	}
	n.Status = Status(status)
	n.Title, n.Reason, n.BundleID, n.Summary, n.UserFeedback = title.String, reason.String, bundle.String, summary.String, feedback.String
	n.SourceRecID, n.SessionID = recID.Int64, session.Int64
	if threshold.Valid {
		v := int(threshold.Int64)
		n.ThresholdAtDecision = &v
	}
	if label.Valid {
		v := int(label.Int64)
		n.UserLabel = &v
	}
	return n, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
