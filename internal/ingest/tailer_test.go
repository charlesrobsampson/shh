package ingest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/store"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

var tailStart = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

var testSources = map[string]string{
	"com.microsoft.teams2":  "Teams",
	"com.microsoft.outlook": "Outlook",
}

type tailRig struct {
	t      *testing.T
	source *testkit.FakeSource
	clock  *testkit.FakeClock
	store  *store.Store

	mu     sync.Mutex
	events []ingest.Event
	errs   []error
	tailer *ingest.Tailer
}

func newTailRig(t *testing.T, tweak ...func(*ingest.Config)) *tailRig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := &tailRig{t: t, source: testkit.NewFakeSource(), clock: testkit.NewFakeClock(tailStart), store: s}
	r.tailer = r.newTailer(tweak...)
	return r
}

func (r *tailRig) newTailer(tweak ...func(*ingest.Config)) *ingest.Tailer {
	cfg := ingest.Config{
		Source: r.source, Store: r.store, Clock: r.clock, Sources: testSources,
		OwnBundles: []string{"app.shh.overlay"},
		Handle: func(e ingest.Event) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.events = append(r.events, e)
			return nil
		},
		OnError: func(err error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.errs = append(r.errs, err)
		},
	}
	for _, f := range tweak {
		f(&cfg)
	}
	return ingest.NewTailer(cfg)
}

func (r *tailRig) add(bundle, title, body string, ago time.Duration) int64 {
	return r.source.Add(ingest.Notification{BundleID: bundle, Title: title, Body: body, Delivered: r.clock.Now().Add(-ago)})
}

func (r *tailRig) poll() {
	r.t.Helper()
	if err := r.tailer.Poll(); err != nil {
		r.t.Fatalf("Poll: %v", err)
	}
}

func (r *tailRig) bodies() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	for i, e := range r.events {
		out[i] = e.Body
	}
	return out
}

func equalStrings(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestRowsAppearExactlyOnce(t *testing.T) {
	r := newTailRig(t)
	r.poll() // first poll sets the cursor
	r.add("com.microsoft.teams2", "Ana", "first", time.Second)
	r.add("com.microsoft.teams2", "Bo", "second", time.Second)
	r.poll()
	r.poll()
	r.clock.Advance(time.Minute)
	r.add("com.microsoft.outlook", "Cy", "third", time.Second)
	r.poll()
	r.poll()

	if got := r.bodies(); !equalStrings(got, "first", "second", "third") {
		t.Fatalf("events = %v, want each row once, oldest first", got)
	}
}

func TestEventsCarryTheNotificationAndItsFriendlySource(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	id := r.source.Add(ingest.Notification{
		BundleID: "com.microsoft.teams2", Title: "Ana", Subtitle: "in General", Body: "hello", Category: "chat",
		Delivered: r.clock.Now().Add(-time.Second),
	})
	r.poll()
	if len(r.events) != 1 {
		t.Fatalf("events = %d, want 1", len(r.events))
	}
	e := r.events[0]
	if e.RecID != id || e.Source != "Teams" || e.BundleID != "com.microsoft.teams2" || e.Title != "Ana" || e.Subtitle != "in General" || e.Body != "hello" || e.Category != "chat" {
		t.Errorf("event = %+v", e)
	}
}

func TestSourceNames(t *testing.T) {
	tests := []struct {
		name   string
		bundle string
		want   string
	}{
		{"from the rules", "com.microsoft.teams2", "Teams"},
		{"another from the rules", "com.microsoft.outlook", "Outlook"},
		{"unknown bundles use the last part of the id", "com.luckymarmot.paw", "Paw"},
		{"a single-part id", "slack", "Slack"},
		{"no bundle id at all", "", "Unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTailRig(t)
			r.poll()
			r.add(tt.bundle, "t", "b", time.Second)
			r.poll()
			if len(r.events) != 1 || r.events[0].Source != tt.want {
				t.Fatalf("source = %+v, want %q", r.events, tt.want)
			}
		})
	}
}

func TestFirstRunIgnoresOldHistoryButCoversTheStartupGap(t *testing.T) {
	r := newTailRig(t)
	r.add("com.microsoft.teams2", "t", "ten minutes ago", 10*time.Minute)
	r.add("com.microsoft.teams2", "t", "thirty-one seconds ago", 31*time.Second)
	r.add("com.microsoft.teams2", "t", "thirty seconds ago", 30*time.Second)
	r.add("com.microsoft.teams2", "t", "five seconds ago", 5*time.Second)
	r.poll()
	if got := r.bodies(); !equalStrings(got, "thirty seconds ago", "five seconds ago") {
		t.Fatalf("events = %v, want only rows from the 30 seconds before the first poll", got)
	}
}

func TestTheCursorAndSeenRowsArePersisted(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	if cursor, ok, _ := r.store.IngestCursor(); !ok || !cursor.Equal(tailStart) {
		t.Fatalf("cursor after the first poll = %v, %v; want the start time", cursor, ok)
	}
	r.clock.Advance(time.Minute)
	id := r.add("com.microsoft.teams2", "t", "b", 2*time.Second)
	r.poll()
	delivered := r.clock.Now().Add(-2 * time.Second)
	if cursor, _, _ := r.store.IngestCursor(); !cursor.Equal(delivered) {
		t.Errorf("cursor = %v, want the newest delivery time %v", cursor, delivered)
	}
	if seen, _ := r.store.IsSeen(id, delivered); !seen {
		t.Error("the processed row was not recorded as seen")
	}
}

func TestRestartResumesWithoutDuplicatesOrGaps(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	r.add("com.microsoft.teams2", "t", "before restart", time.Second)
	r.poll()

	r.tailer = r.newTailer() // a new process on the same store and source
	r.add("com.microsoft.teams2", "t", "while down", time.Second)
	r.poll()
	r.poll()

	if got := r.bodies(); !equalStrings(got, "before restart", "while down") {
		t.Fatalf("events = %v, want no repeat of the first row and no loss of the second", got)
	}
}

type tailRow struct {
	title, body string
	ago         time.Duration
}

func TestBurstsOfTheSameTextCollapse(t *testing.T) {
	tests := []struct {
		name string
		rows []tailRow
		want []string
	}{
		{"identical rows at the same instant", []tailRow{{"Ana", "same", 5 * time.Second}, {"Ana", "same", 5 * time.Second}, {"Ana", "same", 5 * time.Second}}, []string{"same"}},
		{"repost nine seconds later", []tailRow{{"Ana", "same", 12 * time.Second}, {"Ana", "same", 3 * time.Second}}, []string{"same"}},
		{"exactly ten seconds apart still collapses", []tailRow{{"Ana", "same", 15 * time.Second}, {"Ana", "same", 5 * time.Second}}, []string{"same"}},
		{"eleven seconds apart is a new event", []tailRow{{"Ana", "same", 16 * time.Second}, {"Ana", "same", 5 * time.Second}}, []string{"same", "same"}},
		{"a chain of reposts is measured from the last event, not the last repost", []tailRow{
			{"Ana", "same", 28 * time.Second}, {"Ana", "same", 20 * time.Second}, {"Ana", "same", 12 * time.Second}, {"Ana", "same", 4 * time.Second},
		}, []string{"same", "same"}},
		{"different body is a different event", []tailRow{{"Ana", "one", 5 * time.Second}, {"Ana", "two", 4 * time.Second}}, []string{"one", "two"}},
		{"different title is a different event", []tailRow{{"Ana", "same", 5 * time.Second}, {"Bo", "same", 4 * time.Second}}, []string{"same", "same"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTailRig(t)
			r.poll()
			for _, row := range tt.rows {
				r.add("com.microsoft.teams2", row.title, row.body, row.ago)
			}
			r.poll()
			r.poll()
			if got := r.bodies(); !equalStrings(got, tt.want...) {
				t.Fatalf("events = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTheSameTextInDifferentAppsIsNotADuplicate(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	r.add("com.microsoft.teams2", "Ana", "ping", 3*time.Second)
	r.add("com.microsoft.outlook", "Ana", "ping", 2*time.Second)
	r.poll()
	if len(r.events) != 2 {
		t.Fatalf("events = %d, want 2", len(r.events))
	}
}

func TestShhsOwnNotificationsAreIgnored(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	r.add("app.shh.overlay", "from the helper", "ignored by bundle", time.Second)
	r.add("com.apple.scripteditor2", ingest.OwnTitlePrefix+" fallback banner", "ignored by prefix", time.Second)
	r.add("com.apple.scripteditor2", "a normal script notification", "kept", time.Second)
	r.poll()
	if got := r.bodies(); !equalStrings(got, "kept") {
		t.Fatalf("events = %v, want only the row that is not shh's own", got)
	}
	r.poll()
	if got := r.bodies(); !equalStrings(got, "kept") {
		t.Fatalf("ignored rows came back on the next poll: %v", got)
	}
}

func TestBackfillPicksUpRowsBeforeTheStart(t *testing.T) {
	r := newTailRig(t)
	r.add("com.microsoft.teams2", "t", "twelve minutes ago", 12*time.Minute)
	r.add("com.microsoft.teams2", "t", "eight minutes ago", 8*time.Minute)
	r.add("com.microsoft.teams2", "t", "two minutes ago", 2*time.Minute)

	if err := r.tailer.Backfill(tailStart.Add(-10 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := r.bodies(); !equalStrings(got, "eight minutes ago", "two minutes ago") {
		t.Fatalf("backfill events = %v, want the rows since the given time, oldest first", got)
	}
	if err := r.tailer.Backfill(tailStart.Add(-10 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	r.poll()
	if got := r.bodies(); len(got) != 2 {
		t.Fatalf("a second backfill or a poll repeated rows: %v", got)
	}
}

func TestBackfillThenPollingContinuesWithNewRows(t *testing.T) {
	r := newTailRig(t)
	r.add("com.microsoft.teams2", "t", "old", 5*time.Minute)
	r.tailer.Backfill(tailStart.Add(-10 * time.Minute))
	r.clock.Advance(time.Minute)
	r.add("com.microsoft.teams2", "t", "new", time.Second)
	r.poll()
	if got := r.bodies(); !equalStrings(got, "old", "new") {
		t.Fatalf("events = %v", got)
	}
}

func TestAReusedRecordIDIsANewNotification(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	first := r.add("com.microsoft.teams2", "t", "first", time.Second)
	r.poll()

	r.source.Remove(first)
	r.clock.Advance(time.Minute)
	second := r.add("com.microsoft.teams2", "t", "second", time.Second)
	if second != first {
		t.Fatalf("test setup: id %d was not reused (got %d)", first, second)
	}
	r.poll()
	if got := r.bodies(); !equalStrings(got, "first", "second") {
		t.Fatalf("events = %v, want the notification that reused the id to be seen", got)
	}
}

func TestRowsWrittenLateOrOutOfOrderAreStillSeen(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	r.clock.Advance(time.Minute)

	r.add("com.microsoft.teams2", "t", "newer, written first", 5*time.Second)
	r.poll()
	r.add("com.microsoft.teams2", "t", "older, written late", 20*time.Second)
	r.add("com.microsoft.teams2", "t", "far too old", 90*time.Second)
	r.poll()

	if got := r.bodies(); !equalStrings(got, "newer, written first", "older, written late") {
		t.Fatalf("events = %v, want the late row within 30 seconds of the cursor and not the stale one", got)
	}
}

func TestSourceErrorsAreReturnedAndLeaveStateAlone(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	r.add("com.microsoft.teams2", "t", "waiting", time.Second)
	r.source.FailWith(ingest.ErrNoDatabase)
	if err := r.tailer.Poll(); !errors.Is(err, ingest.ErrNoDatabase) {
		t.Fatalf("Poll err = %v, want ErrNoDatabase", err)
	}
	r.source.FailWith(nil)
	r.poll()
	if got := r.bodies(); !equalStrings(got, "waiting") {
		t.Fatalf("events = %v, want the row delivered once the source recovers", got)
	}
}

func TestAFailingHandlerIsRetried(t *testing.T) {
	var attempts int
	r := newTailRig(t, func(c *ingest.Config) {
		c.Handle = func(e ingest.Event) error {
			attempts++
			if attempts == 1 {
				return errors.New("pipeline unavailable")
			}
			return nil
		}
	})
	r.poll()
	r.add("com.microsoft.teams2", "t", "x", time.Second)
	if err := r.tailer.Poll(); err == nil {
		t.Fatal("Poll hid the handler error")
	}
	r.poll()
	if attempts != 2 {
		t.Fatalf("handler ran %d times, want 2 (the row must not be marked seen after a failure)", attempts)
	}
	r.poll()
	if attempts != 2 {
		t.Fatalf("handler ran %d times after success, want it to stay at 2", attempts)
	}
}

func TestRunPollsOnTheClockAndStopsWithItsContext(t *testing.T) {
	r := newTailRig(t)
	delivered := make(chan struct{}, 8)
	r.tailer = r.newTailer(func(c *ingest.Config) {
		c.Interval = 2 * time.Second
		prev := c.Handle
		c.Handle = func(e ingest.Event) error {
			err := prev(e)
			delivered <- struct{}{}
			return err
		}
	})
	wait := func(what string) {
		t.Helper()
		select {
		case <-delivered:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}

	r.add("com.microsoft.teams2", "t", "startup", time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.tailer.Run(ctx) }()
	wait("the immediate first poll")

	r.add("com.microsoft.teams2", "t", "after one tick", time.Second)
	r.clock.Advance(time.Second)
	select {
	case <-delivered:
		t.Fatal("polled before the interval elapsed")
	case <-time.After(50 * time.Millisecond):
	}
	r.clock.Advance(time.Second)
	wait("the poll after two seconds")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancellation, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
	if got := r.bodies(); !equalStrings(got, "startup", "after one tick") {
		t.Fatalf("events = %v", got)
	}
}

func TestRunKeepsPollingThroughSourceErrors(t *testing.T) {
	r := newTailRig(t)
	delivered := make(chan struct{}, 8)
	r.tailer = r.newTailer(func(c *ingest.Config) {
		prev := c.Handle
		c.Handle = func(e ingest.Event) error { err := prev(e); delivered <- struct{}{}; return err }
	})
	r.source.FailWith(ingest.ErrNoDatabase)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.tailer.Run(ctx)

	deadline := time.After(2 * time.Second)
	for {
		r.mu.Lock()
		n := len(r.errs)
		r.mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the source error never reached OnError")
		case <-time.After(5 * time.Millisecond):
		}
	}
	r.source.FailWith(nil)
	r.add("com.microsoft.teams2", "t", "recovered", time.Second)
	r.clock.Advance(time.Second)
	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("Run stopped polling after a source error")
	}
}

func TestBackfillNeverRepeatsRowsStillInTheSource(t *testing.T) {
	r := newTailRig(t)
	r.poll()
	r.add("com.microsoft.teams2", "t", "early", 5*time.Second)
	r.poll()
	for i := 0; i < 5; i++ { // time passes and the polls prune what they can
		r.clock.Advance(10 * time.Minute)
		r.poll()
	}
	if err := r.tailer.Backfill(tailStart.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := r.bodies(); !equalStrings(got, "early") {
		t.Fatalf("events = %v, want no repeat of a row macOS still holds", got)
	}
}

type skippingSource struct{ batch ingest.Batch }

func (s skippingSource) Since(int64) (ingest.Batch, error) { return s.batch, nil }

func TestUndecodableRecordsAreReportedOnce(t *testing.T) {
	r := newTailRig(t, func(c *ingest.Config) {
		c.Source = skippingSource{ingest.Batch{Skipped: []ingest.Skipped{
			{RecID: 4, Err: errors.New("undecodable plist")}, {RecID: 9, Err: errors.New("no req dictionary")},
		}}}
	})
	r.poll()
	r.poll()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.errs) != 2 {
		t.Fatalf("reported %d errors over two polls, want one per bad record (2): %v", len(r.errs), r.errs)
	}
}
