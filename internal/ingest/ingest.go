package ingest

import "time"

// Notification is one decoded macOS notification record.
type Notification struct {
	RecID      int64
	BundleID   string
	Title      string
	Subtitle   string
	Body       string
	Category   string
	Identifier string
	Delivered  time.Time
	// Thread is the kind of conversation, when the app's own metadata says (Teams does); otherwise unknown.
	Thread Thread
}

// Skipped is a record that could not be decoded; the rest of the batch is still returned.
type Skipped struct {
	RecID int64
	Err   error
}

type Batch struct {
	Notifications []Notification
	Skipped       []Skipped
}

// Source reads notification records. Since returns those with a record id above recID, oldest first.
//
// macOS deletes dismissed records and reuses the highest record id for the next one, so a record id alone does not identify
// a notification. Callers should dedupe on record id plus delivery time.
type Source interface {
	Since(recID int64) (Batch, error)
}
