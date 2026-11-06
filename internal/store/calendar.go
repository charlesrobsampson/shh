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
