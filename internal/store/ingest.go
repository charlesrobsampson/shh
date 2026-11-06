package store

import (
	"database/sql"
	"errors"
	"time"
)

// IngestCursor is the newest delivery time the tailer has processed; ok is false before the first poll.
func (s *Store) IngestCursor() (cursor time.Time, ok bool, err error) {
	var text string
	err = s.db.QueryRow(`SELECT CAST(cursor AS TEXT) FROM ingest_state WHERE id = 1`).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	cursor, err = parseTime(text)
	return cursor, err == nil, err
}

func (s *Store) SetIngestCursor(cursor time.Time) error {
	_, err := s.db.Exec(`INSERT INTO ingest_state (id, cursor) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET cursor = excluded.cursor`, formatTime(cursor))
	return err
}

// MarkSeen records that the macOS record with this id and delivery time was handled.
func (s *Store) MarkSeen(recID int64, delivered time.Time) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO ingest_seen (rec_id, delivered) VALUES (?, ?)`, recID, formatTime(delivered))
	return err
}

func (s *Store) IsSeen(recID int64, delivered time.Time) (bool, error) {
	var found int
	err := s.db.QueryRow(`SELECT 1 FROM ingest_seen WHERE rec_id = ? AND delivered = ?`, recID, formatTime(delivered)).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// PruneSeen forgets handled records delivered before the cut-off, except those keep still wants (rows macOS still holds, which a
// later backfill could read again).
func (s *Store) PruneSeen(before time.Time, keep func(recID int64, delivered time.Time) bool) error {
	rows, err := s.db.Query(`SELECT rec_id, CAST(delivered AS TEXT) FROM ingest_seen WHERE delivered < ?`, formatTime(before))
	if err != nil {
		return err
	}
	type key struct {
		recID     int64
		delivered time.Time
	}
	var stale []key
	for rows.Next() {
		var k key
		var text string
		if err := rows.Scan(&k.recID, &text); err != nil {
			rows.Close()
			return err
		}
		if k.delivered, err = parseTime(text); err != nil {
			rows.Close()
			return err
		}
		if keep == nil || !keep(k.recID, k.delivered) {
			stale = append(stale, k)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, k := range stale {
		if _, err := s.db.Exec(`DELETE FROM ingest_seen WHERE rec_id = ? AND delivered = ?`, k.recID, formatTime(k.delivered)); err != nil {
			return err
		}
	}
	return nil
}
