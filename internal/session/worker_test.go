package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

const (
	teams      = "com.microsoft.teams2"
	outlook    = "com.microsoft.outlook"
	mail       = "com.apple.mail"
	reminderOf = "OLAlertsNotificationRemindersWithJoinAndChatCategoryIdentifier"

	midMessage = "can you send me your comments by Friday?" // scores about 49 with no boost
)

type workerRig struct {
	t          *testing.T
	worker     *session.Worker
	source     *testkit.FakeSource
	clock      *testkit.FakeClock
	alerter    *testkit.FakeAlerter
	suppressor *testkit.FakeSuppressor
	store      *store.Store
	errs       []error
	decisions  []session.Decision
}

func newWorkerRig(t *testing.T, tweak ...func(*session.Config)) *workerRig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := &workerRig{
		t: t, source: testkit.NewFakeSource(), clock: testkit.NewFakeClock(t0), alerter: testkit.NewFakeAlerter(),
		suppressor: testkit.NewFakeSuppressor(), store: s,
	}
	r.worker = r.build(tweak...)
	r.tick() // the first poll starts the tailer's cursor
	return r
}

func (r *workerRig) build(tweak ...func(*session.Config)) *session.Worker {
	rl, err := rules.Defaults()
	if err != nil {
		r.t.Fatal(err)
	}
	cfg := session.Config{
		Store: r.store, Source: r.source, Clock: r.clock, Rules: rl, Alerter: r.alerter, Suppressor: r.suppressor,
		Location: time.UTC, OwnBundles: []string{"app.shh.overlay"},
		OnError:    func(err error) { r.errs = append(r.errs, err) },
		OnDecision: func(d session.Decision) { r.decisions = append(r.decisions, d) },
	}
	for _, f := range tweak {
		f(&cfg)
	}
	w, err := session.NewWorker(cfg)
	if err != nil {
		r.t.Fatal(err)
	}
	return w
}

func (r *workerRig) tick() {
	r.t.Helper()
	if err := r.worker.Tick(); err != nil {
		r.t.Fatalf("Tick: %v", err)
	}
}

func (r *workerRig) post(bundle, title, body string) int64 {
	return r.source.Add(ingest.Notification{BundleID: bundle, Title: title, Body: body, Delivered: r.clock.Now().Add(-time.Second)})
}

func (r *workerRig) postAndTick(bundle, title, body string) {
	r.t.Helper()
	r.post(bundle, title, body)
	r.tick()
}

func (r *workerRig) notifications() []store.Notification {
	list, err := r.store.ListNotifications(store.Filter{})
	if err != nil {
		r.t.Fatal(err)
	}
	return list
}

func (r *workerRig) onlyNotification() store.Notification {
	r.t.Helper()
	list := r.notifications()
	if len(list) != 1 {
		r.t.Fatalf("stored %d notifications, want 1", len(list))
	}
	return list[0]
}

func TestAnAlertWorthyMessageIsAlertedAndStored(t *testing.T) {
	r := newWorkerRig(t)
	if err := r.worker.SetBaseline(65); err != nil {
		t.Fatal(err)
	}
	r.postAndTick(teams, "Ana", "prod outage, need you now")

	shown := r.alerter.Shown()
	if len(shown) != 1 {
		t.Fatalf("alerts = %d, want 1", len(shown))
	}
	a := shown[0]
	if a.Kind != config.KindMessage || a.Source != "Teams" || a.Title != "Ana" || a.BundleID != teams || !strings.Contains(a.Body, "outage") {
		t.Errorf("alert = %+v", a)
	}
	n := r.onlyNotification()
	if n.Status != store.StatusAlerted || n.Source != "Teams" || n.Sender != "Ana" || n.BundleID != teams || n.Body != "prod outage, need you now" {
		t.Errorf("stored = %+v", n)
	}
	if n.Reason == "" || n.Score <= 0.65 || n.Boost <= 0 || n.SourceRecID == 0 {
		t.Errorf("stored = %+v, want a reason, a score above the threshold, the boost and the macOS record id", n)
	}
	if n.ThresholdAtDecision == nil || *n.ThresholdAtDecision != 65 {
		t.Errorf("threshold at decision = %v, want 65", n.ThresholdAtDecision)
	}
	if n.IsRead || n.IsDismissedInbox || n.IsTrained {
		t.Errorf("a new notification must start unread, undismissed and untrained: %+v", n)
	}
}

func TestAMessageBelowTheLevelIsBlockedAndStoredWithItsReason(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(65)
	r.postAndTick(mail, "Weekly digest", "ten tips to boost your productivity, unsubscribe any time")

	if len(r.alerter.Shown()) != 0 {
		t.Fatal("a newsletter interrupted")
	}
	n := r.onlyNotification()
	if n.Status != store.StatusBlocked || n.Reason == "" || !strings.HasPrefix(n.Reason, "unimportant") {
		t.Errorf("stored = %+v, want blocked with an 'unimportant' reason", n)
	}
}

func TestTheSessionAndCountersAreRecorded(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(65)
	st, _ := r.worker.Status()
	baselineID := st.Layers[0].ID

	r.postAndTick(teams, "Ana", "prod outage, need you now")
	r.postAndTick(mail, "Weekly digest", "ten tips to boost your productivity, unsubscribe any time")
	r.clock.Advance(30 * time.Second)
	r.postAndTick(mail, "Big sale", "20% off everything this weekend only, unsubscribe any time")

	level, err := r.store.GetLevel(baselineID)
	if err != nil {
		t.Fatal(err)
	}
	if level.Allowed != 1 || level.Blocked != 2 {
		t.Errorf("counters = allowed %d blocked %d, want 1 and 2", level.Allowed, level.Blocked)
	}
	for _, n := range r.notifications() {
		if n.SessionID != baselineID {
			t.Errorf("notification %q has session %d, want the active level %d", n.Title, n.SessionID, baselineID)
		}
	}
}

func TestEscalationExpiryChangesTheNextDecision(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(30)
	if _, err := r.worker.Escalate(session.Escalation{Level: 90, Duration: 10 * time.Minute}); err != nil {
		t.Fatal(err)
	}

	r.postAndTick(teams, "Evan", midMessage)
	if len(r.alerter.Shown()) != 0 || r.notifications()[0].Status != store.StatusBlocked {
		t.Fatalf("at 90%% a mid-importance message should be blocked: shown=%d", len(r.alerter.Shown()))
	}

	r.clock.Advance(10 * time.Minute) // the escalation ends, the baseline of 30 applies
	r.postAndTick(teams, "Evan", "could you send me the comments on the plan by Friday?")
	if got := len(r.alerter.Shown()); got != 1 {
		t.Fatalf("after the escalation ended the same kind of message gave %d alerts, want 1", got)
	}
}

func TestACalendarReminderPreEmptsEvenInLockdown(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(99)
	r.source.Add(ingest.Notification{
		BundleID: outlook, Title: "Weekly Sync", Subtitle: "Microsoft Teams Meeting", Body: "in 10 minutes", Category: reminderOf,
		Delivered: r.clock.Now().Add(-time.Second),
	})
	r.tick()

	if len(r.alerter.Shown()) != 0 {
		t.Fatal("the reminder alerted straight away instead of at T-60s")
	}
	if n := r.onlyNotification(); n.Status != store.StatusAlerted {
		t.Errorf("stored status = %v, want alerted (calendar reminders bypass the threshold)", n.Status)
	}

	// Delivered a second ago, "in 10 minutes" means a start 9:59 from now and so an alert due at 8:59.
	r.clock.Advance(8*time.Minute + 58*time.Second)
	if len(r.alerter.Shown()) != 0 {
		t.Fatal("alerted before T-60s")
	}
	r.clock.Advance(time.Second)
	shown := r.alerter.Shown()
	if len(shown) != 1 || shown[0].Kind != config.KindCalendar || shown[0].Title != "Weekly Sync" {
		t.Fatalf("alerts = %+v, want one calendar alert titled Weekly Sync at T-60s", shown)
	}

	r.clock.Advance(time.Second)
	r.postAndTick(teams, "Evan", midMessage)
	if len(r.alerter.Shown()) != 1 {
		t.Error("an ordinary message interrupted at lockdown")
	}
}

func TestAnUnreadableReminderAlertsNowAndIsRecordedForReview(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(99)
	r.source.Add(ingest.Notification{BundleID: outlook, Title: "Mystery", Body: "sometime after lunch", Category: reminderOf, Delivered: r.clock.Now().Add(-time.Second)})
	r.tick()

	shown := r.alerter.Shown()
	if len(shown) != 1 || shown[0].Kind != config.KindCalendar {
		t.Fatalf("alerts = %+v, want one immediate calendar alert", shown)
	}
	unparsed, _ := r.store.UnparsedCalendar()
	n := r.onlyNotification()
	if len(unparsed) != 1 || unparsed[0].NotificationID != n.ID || unparsed[0].RawText != "sometime after lunch" {
		t.Errorf("unparsed = %+v, want the reminder linked to notification %d", unparsed, n.ID)
	}
}

func TestTheSuppressorFollowsTheEffectiveLevel(t *testing.T) {
	r := newWorkerRig(t)
	if r.suppressor.Running() {
		t.Fatal("suppressing with no focus level")
	}

	r.worker.SetBaseline(30)
	if !r.suppressor.Running() {
		t.Fatal("not suppressing after a baseline was set")
	}
	r.worker.Escalate(session.Escalation{Level: 90, Duration: 10 * time.Minute})
	r.clock.Advance(10 * time.Minute)
	r.tick()
	if !r.suppressor.Running() {
		t.Fatal("stopped suppressing when only the escalation ended")
	}
	r.worker.SetBaseline(0)
	if r.suppressor.Running() {
		t.Fatal("still suppressing at 0%")
	}
	if r.suppressor.Starts() != 1 || r.suppressor.Stops() != 1 {
		t.Errorf("starts=%d stops=%d, want one of each", r.suppressor.Starts(), r.suppressor.Stops())
	}
}

func TestTheSuppressorStopsWhenTheLastTimerRunsOut(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.Escalate(session.Escalation{Level: 60, Duration: 5 * time.Minute})
	if !r.suppressor.Running() {
		t.Fatal("not suppressing during the escalation")
	}
	r.clock.Advance(5 * time.Minute)
	r.tick()
	if r.suppressor.Running() {
		t.Fatal("still suppressing after the only layer expired")
	}
}

func TestStoppingEverythingStopsSuppressing(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(30)
	r.worker.Escalate(session.Escalation{Level: 75})
	r.worker.StopAll()
	if r.suppressor.Running() {
		t.Fatal("still suppressing after StopAll")
	}
}

func TestAtLevelZeroNothingIsDecided(t *testing.T) {
	r := newWorkerRig(t)
	r.postAndTick(teams, "Ana", "prod outage, need you now")
	if len(r.alerter.Shown()) != 0 || len(r.notifications()) != 0 {
		t.Fatalf("with no focus level: %d alerts, %d stored, want neither (the OS shows the notification itself)", len(r.alerter.Shown()), len(r.notifications()))
	}
}

func TestShhsOwnNotificationsAreIgnored(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(30)
	r.postAndTick("app.shh.overlay", "from the helper", "prod outage, need you now")
	r.postAndTick("com.apple.scripteditor2", ingest.OwnTitlePrefix+" fallback", "prod outage, need you now")
	if len(r.notifications()) != 0 || len(r.alerter.Shown()) != 0 {
		t.Fatalf("shh's own notifications were processed: %d stored, %d alerts", len(r.notifications()), len(r.alerter.Shown()))
	}
}

func TestARestartDoesNotAlertAgain(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(30)
	r.postAndTick(teams, "Ana", "prod outage, need you now")
	if len(r.alerter.Shown()) != 1 {
		t.Fatalf("setup: %d alerts", len(r.alerter.Shown()))
	}

	r.worker = r.build() // a new worker process on the same store and source
	r.tick()
	r.tick()
	if len(r.alerter.Shown()) != 1 || len(r.notifications()) != 1 {
		t.Fatalf("after a restart: %d alerts, %d stored; want no repeats", len(r.alerter.Shown()), len(r.notifications()))
	}
}

func TestAFailingAlerterIsReportedButTheNotificationIsKept(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(30)
	boom := errors.New("display unavailable")
	r.alerter.FailWith(boom)
	r.postAndTick(teams, "Ana", "prod outage, need you now")

	if len(r.errs) != 1 || !errors.Is(r.errs[0], boom) {
		t.Fatalf("errors = %v, want the alerter failure reported", r.errs)
	}
	if n := r.onlyNotification(); n.Status != store.StatusAlerted {
		t.Errorf("stored status = %v, want alerted", n.Status)
	}
	r.alerter.FailWith(nil)
	r.tick()
	r.tick()
	if len(r.alerter.Shown()) != 0 || len(r.notifications()) != 1 {
		t.Errorf("the failed alert was retried or duplicated: %d alerts, %d stored", len(r.alerter.Shown()), len(r.notifications()))
	}
}

func TestABurstBoostIsAppliedAndStored(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(30)
	for _, body := range []string{midMessage, "could you send me the comments on the plan by Friday?", "do you have the comments on the plan?"} {
		r.postAndTick(teams, "Evan", body)
		r.clock.Advance(20 * time.Second)
	}
	list := r.notifications() // newest first
	if len(list) != 3 || list[0].Boost != 0.1 || list[1].Boost != 0 {
		t.Fatalf("boosts newest first = %v %v %v, want 0.1 for the third message of the burst only", list[0].Boost, list[1].Boost, list[2].Boost)
	}
	if !strings.Contains(list[0].Reason, "burst") {
		t.Errorf("reason = %q, want it to mention the burst", list[0].Reason)
	}
}

func TestDecisionsAreReported(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(65)
	r.postAndTick(teams, "Ana", "prod outage, need you now")
	r.postAndTick(mail, "Weekly digest", "ten tips to boost your productivity, unsubscribe any time")
	if len(r.decisions) != 2 {
		t.Fatalf("decisions = %d, want 2", len(r.decisions))
	}
	first, second := r.decisions[0], r.decisions[1]
	if !first.Alerted || first.Notification.ID == 0 || first.Notification.Status != store.StatusAlerted || second.Alerted || second.Notification.Status != store.StatusBlocked {
		t.Errorf("decisions = %+v / %+v", first, second)
	}
}

func TestRunPollsOnTheClockBackfillsAndCleansUp(t *testing.T) {
	r := newWorkerRig(t, func(c *session.Config) { c.Interval = 2 * time.Second })
	r.worker.SetBaseline(30)
	r.source.Add(ingest.Notification{BundleID: teams, Title: "Ana", Body: "prod outage, need you now", Delivered: r.clock.Now().Add(-5 * time.Minute)})

	decided := make(chan struct{}, 8)
	r.worker = r.build(func(c *session.Config) {
		c.Interval = 2 * time.Second
		c.OnDecision = func(session.Decision) { decided <- struct{}{} }
	})
	wait := func(what string) {
		t.Helper()
		select {
		case <-decided:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.worker.Run(ctx, r.clock.Now().Add(-10*time.Minute)) }()
	wait("the backfilled notification")

	r.post(teams, "Bo", "prod is down, need you now")
	r.clock.Advance(2 * time.Second)
	wait("the notification after one interval")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if r.suppressor.Running() {
		t.Error("the suppressor was left running when the worker stopped")
	}
	if got := len(r.notifications()); got != 2 {
		t.Errorf("stored %d notifications, want the backfilled one and the new one", got)
	}
}

func TestPendingCalendarAlertsSurviveARestart(t *testing.T) {
	r := newWorkerRig(t)
	r.worker.SetBaseline(99)
	r.source.Add(ingest.Notification{BundleID: outlook, Title: "Standup", Body: "in 10 minutes", Category: reminderOf, Delivered: r.clock.Now().Add(-time.Second)})
	r.tick()

	r.worker.Shutdown() // the old process is gone and so are its timers
	r.worker = r.build()
	if err := r.worker.Init(time.Time{}); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(9 * time.Minute)
	if got := len(r.alerter.Shown()); got != 1 {
		t.Fatalf("alerts = %d, want the reminder re-armed and delivered once", got)
	}
	r.clock.Advance(time.Hour)
	if got := len(r.alerter.Shown()); got != 1 {
		t.Fatalf("alerts = %d, want it not to repeat", got)
	}
}
