package testkit

import (
	"sort"
	"sync"

	"github.com/charlesrobsampson/shh/internal/ingest"
)

var _ ingest.Source = (*FakeSource)(nil)

// FakeSource is an in-memory notification database. Like SQLite's INTEGER PRIMARY KEY it hands out max(id)+1, so removing the
// newest row makes the next Add reuse its id, as macOS does.
type FakeSource struct {
	mu   sync.Mutex
	rows []ingest.Notification
	err  error
}

func NewFakeSource() *FakeSource {
	return &FakeSource{}
}

// Add stores n under a new record id (n.RecID is ignored) and returns that id.
func (f *FakeSource) Add(n ingest.Notification) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	n.RecID = 1
	for _, row := range f.rows {
		n.RecID = max(n.RecID, row.RecID+1)
	}
	f.rows = append(f.rows, n)
	return n.RecID
}

func (f *FakeSource) Remove(recID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.rows[:0]
	for _, row := range f.rows {
		if row.RecID != recID {
			kept = append(kept, row)
		}
	}
	f.rows = kept
}

// FailWith makes every later Since return err until it is called with nil.
func (f *FakeSource) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *FakeSource) Since(recID int64) (ingest.Batch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return ingest.Batch{}, f.err
	}
	var batch ingest.Batch
	for _, row := range f.rows {
		if row.RecID > recID {
			batch.Notifications = append(batch.Notifications, row)
		}
	}
	sort.Slice(batch.Notifications, func(i, j int) bool { return batch.Notifications[i].RecID < batch.Notifications[j].RecID })
	return batch, nil
}
