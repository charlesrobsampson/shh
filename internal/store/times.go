package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Fixed width so that text comparison in SQL orders timestamps correctly.
const timeLayout = "2006-01-02 15:04:05.000000000"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// parseTime reads both our fixed-width format and SQLite's CURRENT_TIMESTAMP; Go's parser accepts fractional seconds the layout omits.
func parseTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: bad timestamp %q: %w", s, err)
	}
	return t, nil
}

func parseOptionalTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := parseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}
