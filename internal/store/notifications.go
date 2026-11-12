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
	// Thread is the kind of conversation (direct, group, meeting, channel) when the app said; empty when unknown.
	Thread string
	// Subtitle and Category are as the app sent them (the title is kept as sent too, apart from rows stored before these existed, whose
	// title has the subtitle joined on). They are stored so features can be recomputed for training.
	Subtitle string
	Category string

	// IsFlagged marks a notification to review later in the Train tab; training it clears the mark.
	IsFlagged        bool
	IsRead           bool
	IsDismissedInbox bool
	IsTrained        bool
	// AutoTrained is set when shh trained the notification itself (it went unlabelled to the end of its time and was taken as right),
	// not the user.
	AutoTrained  bool
	UserFeedback string // "y", "n" or empty
	UserLabel    *int   // exact 0-100 label, nil for quick feedback or untrained
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
	Sender string
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
	is_read, is_dismissed_inbox, is_trained, user_feedback, user_label, thread, subtitle, category, is_flagged, auto_trained`

// InsertNotification stores n and reports whether it was new. A row with the same macOS record id and delivery time is ignored.
func (s *Store) InsertNotification(n Notification) (id int64, inserted bool, err error) {
	if n.ThresholdAtDecision == nil {
		return 0, false, errors.New("store: a notification must record the threshold it was decided at")
	}
	res, err := s.db.Exec(`INSERT INTO notifications
		(source, sender, title, body, timestamp, status, predicted_score, time_sensitivity_boost,
		 reason, bundle_id, source_rec_id, threshold_at_decision, session_id, thread, subtitle, category)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		n.Source, n.Sender, nullString(n.Title), n.Body, formatTime(n.Timestamp), string(n.Status), n.Score, n.Boost,
		nullString(n.Reason), nullString(n.BundleID), nullID(n.SourceRecID), *n.ThresholdAtDecision, nullID(n.SessionID), nullString(n.Thread),
		nullString(n.Subtitle), nullString(n.Category))
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

// FindNotification says whether a notification with this app, title, body and time is stored, and its id. It is how an import avoids
// storing the same notification twice.
func (s *Store) FindNotification(source, title, body string, at time.Time) (int64, bool, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM notifications WHERE source = ? AND COALESCE(title, '') = ? AND body = ? AND timestamp = ? LIMIT 1`,
		source, title, body, formatTime(at)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
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
	if f.Sender != "" {
		where = append(where, "sender = ?")
		args = append(args, f.Sender)
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

// SetFlagged marks or unmarks a notification for review in the Train tab.
func (s *Store) SetFlagged(id int64, flagged bool) error {
	return s.update(id, `UPDATE notifications SET is_flagged = ? WHERE id = ?`, flagged, id)
}

func (s *Store) SetSummary(id int64, summary string) error {
	return s.update(id, `UPDATE notifications SET summary = ? WHERE id = ?`, nullString(summary), id)
}

// Train marks the row trained and records the feedback; it does not touch the inbox flag, and it clears the review flag: the review
// it was flagged for has happened.
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
	return s.update(id, `UPDATE notifications SET is_trained = 1, is_flagged = 0, user_feedback = ?, user_label = ? WHERE id = ?`, feedback, t.Label, id)
}

func (s *Store) Untrain(id int64) error {
	return s.update(id, `UPDATE notifications SET is_trained = 0, auto_trained = 0, user_feedback = NULL, user_label = NULL WHERE id = ?`, id)
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
		n                                                                    Notification
		status, stamp                                                        string
		title, reason, bundle, summary, feedback, thread, subtitle, category sql.NullString
		recID, threshold, session, label                                     sql.NullInt64
	)
	err := row.Scan(&n.ID, &n.Source, &n.Sender, &title, &n.Body, &stamp, &status, &n.Score, &n.Boost,
		&reason, &bundle, &recID, &threshold, &session, &summary,
		&n.IsRead, &n.IsDismissedInbox, &n.IsTrained, &feedback, &label, &thread, &subtitle, &category, &n.IsFlagged, &n.AutoTrained)
	if err != nil {
		return Notification{}, err
	}
	if n.Timestamp, err = parseTime(stamp); err != nil {
		return Notification{}, err
	}
	n.Status = Status(status)
	n.Title, n.Reason, n.BundleID, n.Summary, n.UserFeedback = title.String, reason.String, bundle.String, summary.String, feedback.String
	n.Thread, n.Subtitle, n.Category = thread.String, subtitle.String, category.String
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
