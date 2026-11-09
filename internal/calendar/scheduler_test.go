package calendar_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/calendar"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/store"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

var start0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

type schedRig struct {
	t         *testing.T
	clock     *testkit.FakeClock
	alerter   *testkit.FakeAlerter
	store     *store.Store
	scheduler *calendar.Scheduler
	errs      []error
}

func newSchedRig(t *testing.T, tweak ...func(*calendar.SchedulerConfig)) *schedRig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := &schedRig{t: t, clock: testkit.NewFakeClock(start0), alerter: testkit.NewFakeAlerter(), store: s}
	r.scheduler = r.newScheduler(tweak...)
	return r
}

func (r *schedRig) newScheduler(tweak ...func(*calendar.SchedulerConfig)) *calendar.Scheduler {
	cfg := calendar.SchedulerConfig{
		Clock: r.clock, Store: r.store, Alerter: r.alerter, Location: time.UTC,
		OnError: func(err error) { r.errs = append(r.errs, err) },
	}
	for _, f := range tweak {
		f(&cfg)
	}
	return calendar.NewScheduler(cfg)
}

func (r *schedRig) meeting(title string, startsIn time.Duration) calendar.Reminder {
	return calendar.Reminder{Title: title, Subtitle: "Microsoft Teams Meeting", Delivered: r.clock.Now(), Start: r.clock.Now().Add(startsIn)}
}

func (r *schedRig) schedule(rem calendar.Reminder) {
	r.t.Helper()
	if err := r.scheduler.Schedule(rem, 0); err != nil {
		r.t.Fatalf("Schedule: %v", err)
	}
}

func (r *schedRig) shown() []alert.Alert { return r.alerter.Shown() }

func TestTheAlertFiresExactlyOneMinuteBeforeTheStart(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("Weekly Sync", 10*time.Minute))

	r.clock.Advance(8*time.Minute + 59*time.Second)
	if len(r.shown()) != 0 {
		t.Fatal("alerted before T-60s")
	}
	r.clock.Advance(time.Second)
	shown := r.shown()
	if len(shown) != 1 {
		t.Fatalf("alerts at T-60s = %d, want 1", len(shown))
	}
	a := shown[0]
	if a.Kind != config.KindCalendar || a.Title != "Weekly Sync" || !strings.Contains(a.Body, "09:10") {
		t.Errorf("alert = %+v, want a calendar alert titled Weekly Sync that says it starts at 09:10", a)
	}
	r.clock.Advance(time.Hour)
	if len(r.shown()) != 1 {
		t.Error("the alert fired more than once")
	}
}

func TestALateReminderAlertsImmediately(t *testing.T) {
	tests := []struct {
		name     string
		startsIn time.Duration
	}{
		{"less than a minute away", 30 * time.Second},
		{"exactly a minute away", time.Minute},
		{"already started", -5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSchedRig(t)
			r.schedule(r.meeting("Standup", tt.startsIn))
			if len(r.shown()) != 1 {
				t.Fatalf("alerts = %d, want one straight away", len(r.shown()))
			}
			r.clock.Advance(time.Hour)
			if len(r.shown()) != 1 {
				t.Error("the late alert fired again")
			}
		})
	}
}

func TestTheSameMeetingScheduledTwiceAlertsOnce(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("Weekly Sync", 10*time.Minute))
	r.schedule(r.meeting("Weekly Sync", 10*time.Minute))
	r.clock.Advance(15 * time.Minute)
	r.schedule(calendar.Reminder{Title: "Weekly Sync", Start: start0.Add(10 * time.Minute), Delivered: r.clock.Now()}) // after it fired
	r.clock.Advance(time.Hour)
	if got := len(r.shown()); got != 1 {
		t.Fatalf("alerts = %d, want 1 for one meeting however often it was reported", got)
	}
}

func TestDifferentMeetingsAreSeparateAlerts(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("Weekly Sync", 10*time.Minute))
	r.schedule(r.meeting("Weekly Sync", 70*time.Minute)) // same title, later start
	r.schedule(r.meeting("Design review", 10*time.Minute))
	r.clock.Advance(2 * time.Hour)
	if got := len(r.shown()); got != 3 {
		t.Fatalf("alerts = %d, want 3", got)
	}
}

func TestEventsArePersistedWithTheirAlertTime(t *testing.T) {
	r := newSchedRig(t)
	if err := r.scheduler.Schedule(r.meeting("Weekly Sync", 10*time.Minute), 0); err != nil {
		t.Fatal(err)
	}
	pending, _ := r.store.PendingCalendarEvents()
	if len(pending) != 1 || pending[0].Title != "Weekly Sync" || !pending[0].AlertAt.Equal(start0.Add(9*time.Minute)) || !pending[0].Start.Equal(start0.Add(10*time.Minute)) {
		t.Fatalf("pending = %+v, want the meeting with an alert at T-60s", pending)
	}
	r.clock.Advance(10 * time.Minute)
	if pending, _ := r.store.PendingCalendarEvents(); len(pending) != 0 {
		t.Errorf("a fired event is still pending: %+v", pending)
	}
}

func TestTheSourceNotificationIsRemembered(t *testing.T) {
	r := newSchedRig(t)
	threshold := 65
	id, _, _ := r.store.InsertNotification(store.Notification{
		Source: "Outlook", Sender: "Calendar", Body: "x", Timestamp: start0, Status: store.StatusAlerted, Score: 0.5, ThresholdAtDecision: &threshold,
	})
	r.scheduler.Schedule(r.meeting("Weekly Sync", 10*time.Minute), id)
	pending, _ := r.store.PendingCalendarEvents()
	if len(pending) != 1 || pending[0].SourceNotificationID != id {
		t.Fatalf("pending = %+v, want the source notification id %d", pending, id)
	}
}

func TestARestartRearmsOnlyWhatIsStillPending(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("First", 10*time.Minute))
	r.schedule(r.meeting("Second", 30*time.Minute))
	r.clock.Advance(9 * time.Minute) // First fires
	if len(r.shown()) != 1 {
		t.Fatalf("setup: %d alerts, want First to have fired", len(r.shown()))
	}

	r.scheduler.Stop() // the process goes away
	restarted := r.newScheduler()
	if err := restarted.Rearm(); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(20 * time.Minute) // Second's alert time passes
	titles := []string{}
	for _, a := range r.shown() {
		titles = append(titles, a.Title)
	}
	if len(titles) != 2 || titles[0] != "First" || titles[1] != "Second" {
		t.Fatalf("alerts = %v, want First once and Second once", titles)
	}
}

func TestEventsDueWhileTheWorkerWasDownAlertOnRestart(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("Standup", 10*time.Minute))
	r.scheduler.Stop()
	r.clock.Advance(time.Hour) // down through the meeting

	restarted := r.newScheduler()
	if err := restarted.Rearm(); err != nil {
		t.Fatal(err)
	}
	if got := len(r.shown()); got != 1 {
		t.Fatalf("alerts = %d, want the missed one delivered late rather than lost", got)
	}
}

func TestFiredEventsNeverFireAgain(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("Standup", 10*time.Minute))
	r.clock.Advance(15 * time.Minute)
	for i := 0; i < 3; i++ {
		r.scheduler.Stop()
		if err := r.newScheduler().Rearm(); err != nil {
			t.Fatal(err)
		}
	}
	r.clock.Advance(time.Hour)
	if got := len(r.shown()); got != 1 {
		t.Fatalf("alerts = %d after restarts, want 1", got)
	}
}

func TestRearmingTwiceDoesNotDoubleUpTimers(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("Standup", 10*time.Minute))
	if err := r.scheduler.Rearm(); err != nil {
		t.Fatal(err)
	}
	if err := r.scheduler.Rearm(); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(15 * time.Minute)
	if got := len(r.shown()); got != 1 {
		t.Fatalf("alerts = %d, want 1", got)
	}
}

func TestStoppingCancelsPendingAlertsButKeepsThem(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("Standup", 10*time.Minute))
	r.scheduler.Stop()
	r.clock.Advance(15 * time.Minute)
	if len(r.shown()) != 0 {
		t.Fatal("a stopped scheduler still alerted")
	}
	if pending, _ := r.store.PendingCalendarEvents(); len(pending) != 1 {
		t.Fatalf("pending = %+v, want the event kept for the next start", pending)
	}
}

func TestRemindersWithNoStartTimeAlertNowOnce(t *testing.T) {
	tests := []struct {
		name string
		make func(delivered time.Time) calendar.Reminder
	}{
		{"unparsed", func(d time.Time) calendar.Reminder {
			return calendar.Reminder{Title: "Mystery", Delivered: d, Unparsed: true}
		}},
		{"all day", func(d time.Time) calendar.Reminder {
			return calendar.Reminder{Title: "Offsite", Delivered: d, AllDay: true}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSchedRig(t)
			rem := tt.make(r.clock.Now())
			r.schedule(rem)
			if len(r.shown()) != 1 {
				t.Fatalf("alerts = %d, want one immediately", len(r.shown()))
			}
			r.schedule(rem) // Outlook posts the same reminder repeatedly
			r.clock.Advance(20 * time.Second)
			r.schedule(tt.make(r.clock.Now())) // same title, same minute
			if len(r.shown()) != 1 {
				t.Errorf("alerts = %d, want the repeats of one reminder deduplicated", len(r.shown()))
			}
			r.clock.Advance(24 * time.Hour)
			r.schedule(tt.make(r.clock.Now())) // the same title another day is a new reminder
			if len(r.shown()) != 2 {
				t.Errorf("alerts = %d, want a new one for another day", len(r.shown()))
			}
		})
	}
}

func TestTheAlertSaysWhenTheMeetingStartedIfItIsLate(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("Standup", -5*time.Minute))
	if body := r.shown()[0].Body; !strings.Contains(body, "Started") || !strings.Contains(body, "08:55") {
		t.Errorf("body = %q, want it to say the meeting started at 08:55", body)
	}
}

func TestAFailingAlerterKeepsTheEventPendingForARetry(t *testing.T) {
	r := newSchedRig(t)
	boom := errors.New("display unavailable")
	r.alerter.FailWith(boom)
	r.schedule(r.meeting("Standup", 10*time.Minute))
	r.clock.Advance(15 * time.Minute)
	if len(r.shown()) != 0 || len(r.errs) != 1 || !errors.Is(r.errs[0], boom) {
		t.Fatalf("shown=%d errs=%v, want the failure reported and nothing shown", len(r.shown()), r.errs)
	}
	if pending, _ := r.store.PendingCalendarEvents(); len(pending) != 1 {
		t.Fatalf("pending = %+v, want the event kept", pending)
	}

	r.alerter.FailWith(nil)
	if err := r.scheduler.Rearm(); err != nil {
		t.Fatal(err)
	}
	if len(r.shown()) != 1 {
		t.Fatalf("alerts after recovering = %d, want 1", len(r.shown()))
	}
}

func TestTheLeadTimeCanBeChanged(t *testing.T) {
	r := newSchedRig(t, func(c *calendar.SchedulerConfig) { c.Lead = 5 * time.Minute })
	r.schedule(r.meeting("Standup", 10*time.Minute))
	r.clock.Advance(4*time.Minute + 59*time.Second)
	if len(r.shown()) != 0 {
		t.Fatal("alerted before T-5m")
	}
	r.clock.Advance(time.Second)
	if len(r.shown()) != 1 {
		t.Fatal("did not alert at T-5m")
	}
}

func TestTimesInTheAlertUseTheConfiguredZone(t *testing.T) {
	denver := zone(t, "America/Denver")
	r := newSchedRig(t, func(c *calendar.SchedulerConfig) { c.Location = denver })
	r.schedule(r.meeting("Standup", 10*time.Minute)) // 09:10 UTC is 02:10 in Denver
	r.clock.Advance(10 * time.Minute)
	if body := r.shown()[0].Body; !strings.Contains(body, "02:10") {
		t.Errorf("body = %q, want the Denver time 02:10", body)
	}
}

// source stores a notification from the given app, as the worker does before it schedules a reminder.
func (r *schedRig) source(bundle string) int64 {
	r.t.Helper()
	threshold := 65
	id, _, err := r.store.InsertNotification(store.Notification{
		Source: "Outlook", Sender: "Reminder", Body: "x", Timestamp: start0, Status: store.StatusAlerted, Score: 0.5,
		ThresholdAtDecision: &threshold, BundleID: bundle, SourceRecID: int64(len(bundle)) + 1,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func TestTheAlertOpensTheAppTheReminderCameFrom(t *testing.T) {
	r := newSchedRig(t)
	id := r.source("com.microsoft.Outlook")
	if err := r.scheduler.Schedule(r.meeting("Weekly Sync", 10*time.Minute), id); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(9 * time.Minute)
	shown := r.shown()
	if len(shown) != 1 || shown[0].BundleID != "com.microsoft.Outlook" {
		t.Fatalf("alert = %+v, want Go to to open Outlook, the app the reminder came from", shown)
	}
	if shown[0].Kind != config.KindCalendar {
		t.Errorf("kind = %v, want a calendar alert", shown[0].Kind)
	}
}

func TestAnImmediateAlertAlsoOpensTheSourceApp(t *testing.T) {
	r := newSchedRig(t)
	id := r.source("com.microsoft.Outlook")
	if err := r.scheduler.Schedule(calendar.Reminder{Title: "Offsite", Delivered: r.clock.Now(), AllDay: true}, id); err != nil {
		t.Fatal(err)
	}
	late := r.meeting("Started already", -time.Hour)
	if err := r.scheduler.Schedule(late, id); err != nil {
		t.Fatal(err)
	}
	shown := r.shown()
	if len(shown) != 2 {
		t.Fatalf("alerts = %d, want both to fire at once", len(shown))
	}
	for _, a := range shown {
		if a.BundleID != "com.microsoft.Outlook" {
			t.Errorf("alert %q has bundle %q, want the source app", a.Title, a.BundleID)
		}
	}
}

func TestWithoutASourceNotificationThereIsNothingToOpen(t *testing.T) {
	r := newSchedRig(t)
	r.schedule(r.meeting("No source", 10*time.Minute)) // scheduled with notification id 0
	r.clock.Advance(9 * time.Minute)
	shown := r.shown()
	if len(shown) != 1 || shown[0].BundleID != "" {
		t.Errorf("alerts = %+v, want one with no app to open: a missing source must not stop the alert", shown)
	}
}

func TestARestartKeepsTheSourceApp(t *testing.T) {
	r := newSchedRig(t)
	id := r.source("com.microsoft.Outlook")
	r.scheduler.Schedule(r.meeting("Standup", 10*time.Minute), id)
	r.scheduler.Stop()
	r.clock.Advance(time.Hour)
	if err := r.newScheduler().Rearm(); err != nil {
		t.Fatal(err)
	}
	shown := r.shown()
	if len(shown) != 1 || shown[0].BundleID != "com.microsoft.Outlook" {
		t.Errorf("alerts after a restart = %+v, want the source app kept", shown)
	}
}
