package ingest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charlesrobsampson/shh/internal/clock"
	"github.com/charlesrobsampson/shh/internal/store"
)

const (
	DefaultInterval = time.Second

	// Lookback is how far behind the newest delivery a row may still show up. macOS writes a row about five seconds after
	// delivery, and not always in delivery order.
	Lookback = 30 * time.Second

	// DuplicateWindow collapses re-posts: the same app, title and body within this long count as one event.
	DuplicateWindow = 10 * time.Second

	// OwnTitlePrefix marks banners shh itself posts through Notification Center (the fallback in plan.md section 7).
	OwnTitlePrefix = "[shh]"
)

// Event is a notification that passed the filters, with its friendly source name.
type Event struct {
	Notification
	Source string
}

type Config struct {
	Source Source
	Store  *store.Store
	Clock  clock.Clock
	// Sources maps bundle ids to friendly names (rules.Rules.Sources); other apps get a name derived from the bundle id.
	Sources map[string]string
	// OwnBundles are ignored; so is anything whose title starts with OwnTitlePrefix.
	OwnBundles []string
	Interval   time.Duration // DefaultInterval when zero
	// Handle receives each new event. If it fails the row is not marked as handled and is offered again on the next poll.
	Handle func(Event) error
	// OnError receives errors from Run's polls and records that could not be decoded (once each). Optional.
	OnError func(error)
}

type duplicateKey struct{ bundle, title, body string }

// Tailer turns the notification database into a stream of events.
//
// macOS record ids are reused and rows arrive late, so progress is a delivery-time cursor plus the set of (record id, delivery time)
// pairs already handled, both kept in the store. Each poll reads the source and handles rows delivered within Lookback of the cursor
// that were not handled yet.
type Tailer struct {
	cfg Config

	mu       sync.Mutex
	accepted map[duplicateKey]time.Time
	reported map[int64]bool
}

func NewTailer(cfg Config) *Tailer {
	if cfg.Interval == 0 {
		cfg.Interval = DefaultInterval
	}
	return &Tailer{cfg: cfg, accepted: map[duplicateKey]time.Time{}, reported: map[int64]bool{}}
}

// Poll handles whatever is new. The first poll ever starts the cursor at the current time, so old history is not replayed.
func (t *Tailer) Poll() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	cursor, err := t.cursor()
	if err != nil {
		return err
	}
	return t.process(cursor.Add(-Lookback), cursor)
}

// Backfill also handles rows delivered since the given time that were not handled before, for the gap between a session
// starting and the worker being ready.
func (t *Tailer) Backfill(since time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	cursor, err := t.cursor()
	if err != nil {
		return err
	}
	return t.process(since, cursor)
}

// Run polls on the clock until ctx is cancelled. Poll errors go to OnError and polling carries on.
func (t *Tailer) Run(ctx context.Context) error {
	ticker := t.cfg.Clock.NewTicker(t.cfg.Interval)
	defer ticker.Stop()
	for {
		if err := t.Poll(); err != nil && t.cfg.OnError != nil {
			t.cfg.OnError(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
		}
	}
}

func (t *Tailer) cursor() (time.Time, error) {
	cursor, ok, err := t.cfg.Store.IngestCursor()
	if err != nil || ok {
		return cursor, err
	}
	now := t.cfg.Clock.Now()
	return now, t.cfg.Store.SetIngestCursor(now)
}

func (t *Tailer) process(floor, cursor time.Time) (err error) {
	batch, err := t.cfg.Source.Since(0)
	if err != nil {
		return err
	}
	t.reportSkipped(batch.Skipped)
	rows := batch.Notifications
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Delivered.Equal(rows[j].Delivered) {
			return rows[i].Delivered.Before(rows[j].Delivered)
		}
		return rows[i].RecID < rows[j].RecID
	})

	defer func() {
		if saveErr := t.saveProgress(cursor, rows); err == nil {
			err = saveErr
		}
	}()
	for _, n := range rows {
		if n.Delivered.Before(floor) {
			continue
		}
		if seen, err := t.cfg.Store.IsSeen(n.RecID, n.Delivered); err != nil {
			return err
		} else if seen {
			continue
		}
		if err := t.handle(n); err != nil {
			return err
		}
		if err := t.cfg.Store.MarkSeen(n.RecID, n.Delivered); err != nil {
			return err
		}
		if n.Delivered.After(cursor) {
			cursor = n.Delivered
		}
	}
	return nil
}

func (t *Tailer) handle(n Notification) error {
	if t.isOwn(n) {
		return nil
	}
	key := duplicateKey{n.BundleID, n.Title, n.Body}
	if last, ok := t.accepted[key]; ok && n.Delivered.Sub(last).Abs() <= DuplicateWindow {
		return nil
	}
	if err := t.cfg.Handle(Event{Notification: n, Source: t.sourceName(n.BundleID)}); err != nil {
		return err
	}
	t.accepted[key] = n.Delivered
	return nil
}

func (t *Tailer) isOwn(n Notification) bool {
	for _, bundle := range t.cfg.OwnBundles {
		if n.BundleID == bundle {
			return true
		}
	}
	return strings.HasPrefix(n.Title, OwnTitlePrefix)
}

// saveProgress stores the cursor and forgets handled rows macOS no longer holds, keeping the store and the in-memory duplicate
// window small. It runs even after a failed handler so progress made before the failure is not lost.
func (t *Tailer) saveProgress(cursor time.Time, present []Notification) error {
	if err := t.cfg.Store.SetIngestCursor(cursor); err != nil {
		return err
	}
	still := make(map[int64]map[time.Time]bool, len(present))
	for _, n := range present {
		if still[n.RecID] == nil {
			still[n.RecID] = map[time.Time]bool{}
		}
		still[n.RecID][n.Delivered.UTC()] = true
	}
	err := t.cfg.Store.PruneSeen(cursor.Add(-Lookback), func(recID int64, delivered time.Time) bool {
		return still[recID][delivered.UTC()]
	})
	for key, at := range t.accepted {
		if at.Before(cursor.Add(-time.Minute)) {
			delete(t.accepted, key)
		}
	}
	return err
}

func (t *Tailer) reportSkipped(skipped []Skipped) {
	for _, s := range skipped {
		if t.reported[s.RecID] || t.cfg.OnError == nil {
			continue
		}
		t.reported[s.RecID] = true
		t.cfg.OnError(fmt.Errorf("notification record %d skipped: %w", s.RecID, s.Err))
	}
}

func (t *Tailer) sourceName(bundle string) string {
	if name, ok := t.cfg.Sources[bundle]; ok {
		return name
	}
	if bundle == "" {
		return "Unknown"
	}
	last := bundle[strings.LastIndex(bundle, ".")+1:]
	runes := []rune(last)
	if len(runes) == 0 {
		return "Unknown"
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
