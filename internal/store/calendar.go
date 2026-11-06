package store

import (
	"database/sql"
	"errors"
	"time"
)

// UnparsedCalendar is a calendar reminder whose start time shh could not read, kept so `shh rules review` can show it.
type UnparsedCalendar struct {
	ID             int64
	NotificationID int64 // 0 when none
	Category       string
	Title          string
	Subtitle       string
	RawText        string
	CreatedAt      time.Time
}

func (s *Store) AddUnparsedCalendar(u UnparsedCalendar) error {
	if u.RawText == "" {
		return errors.New("store: an unparsed calendar entry needs the text that failed to parse")
	}
	_, err := s.db.Exec(`INSERT INTO unparsed_calendar (notification_id, category, title, subtitle, raw_text) VALUES (?, ?, ?, ?, ?)`,
		nullID(u.NotificationID), nullString(u.Category), nullString(u.Title), nullString(u.Subtitle), u.RawText)
	return err
}

// UnparsedCalendar lists the entries newest first.
func (s *Store) UnparsedCalendar() ([]UnparsedCalendar, error) {
	rows, err := s.db.Query(`SELECT id, notification_id, category, title, subtitle, raw_text, CAST(created_at AS TEXT) FROM unparsed_calendar ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnparsedCalendar
	for rows.Next() {
		var (
			u                         UnparsedCalendar
			notification              sql.NullInt64
			category, title, subtitle sql.NullString
			created                   string
		)
		if err := rows.Scan(&u.ID, &notification, &category, &title, &subtitle, &u.RawText, &created); err != nil {
			return nil, err
		}
		u.NotificationID, u.Category, u.Title, u.Subtitle = notification.Int64, category.String, title.String, subtitle.String
		if u.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CalendarEvent is a meeting alert that is scheduled or has fired.
type CalendarEvent struct {
	ID                   int64
	Title                string
	Start                time.Time
	AlertAt              time.Time
	Fired                bool
	SourceNotificationID int64 // 0 when none
}

// AddCalendarEvent stores the event unless one with the same title and start time exists.
func (s *Store) AddCalendarEvent(e CalendarEvent) (id int64, inserted bool, err error) {
	res, err := s.db.Exec(`INSERT INTO calendar_events (title, start_time, alert_at, source_notification_id) VALUES (?, ?, ?, ?)
		ON CONFLICT (title, start_time) DO NOTHING`, e.Title, formatTime(e.Start), formatTime(e.AlertAt), nullID(e.SourceNotificationID))
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

const calendarEventColumns = `id, title, CAST(start_time AS TEXT), CAST(alert_at AS TEXT), fired, source_notification_id`

func (s *Store) GetCalendarEvent(id int64) (CalendarEvent, error) {
	e, err := scanCalendarEvent(s.db.QueryRow(`SELECT `+calendarEventColumns+` FROM calendar_events WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return CalendarEvent{}, ErrNotFound
	}
	return e, err
}

// PendingCalendarEvents lists events that have not fired, earliest alert first.
func (s *Store) PendingCalendarEvents() ([]CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT ` + calendarEventColumns + ` FROM calendar_events WHERE fired = 0 ORDER BY alert_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarEvent
	for rows.Next() {
		e, err := scanCalendarEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkCalendarEventFired records that the event fired and reports whether this call was the one that did, so two callers racing
// to fire it cannot both succeed.
func (s *Store) MarkCalendarEventFired(id int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE calendar_events SET fired = 1 WHERE id = ? AND fired = 0`, id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		var exists int
		if err := s.db.QueryRow(`SELECT 1 FROM calendar_events WHERE id = ?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
	}
	return affected == 1, nil
}

func scanCalendarEvent(row scanner) (CalendarEvent, error) {
	var (
		e            CalendarEvent
		start, alert string
		source       sql.NullInt64
	)
	if err := row.Scan(&e.ID, &e.Title, &start, &alert, &e.Fired, &source); err != nil {
		return CalendarEvent{}, err
	}
	var err error
	if e.Start, err = parseTime(start); err != nil {
		return CalendarEvent{}, err
	}
	if e.AlertAt, err = parseTime(alert); err != nil {
		return CalendarEvent{}, err
	}
	e.SourceNotificationID = source.Int64
	return e, nil
}
