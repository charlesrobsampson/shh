package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/calendar"
	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/clock"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/store"
	"github.com/charlesrobsampson/shh/internal/suppress"
)

// maxAlertBody is the most text an alert carries. The banner shows a few lines and Preview up to MaxPreviewLines, so a body longer
// than that is cut here with an ellipsis rather than shipped whole.
const maxAlertBody = 600

type Config struct {
	Store      *store.Store
	Source     ingest.Source
	Clock      clock.Clock
	Rules      rules.Rules
	Embedder   classify.Embedder // nil leaves layer 2 off
	Alerter    alert.Alerter
	Suppressor suppress.Suppressor
	// Location is where calendar start times are read; time.Local when nil.
	Location *time.Location
	// OwnBundles are shh's own bundle ids, whose notifications are ignored.
	OwnBundles []string
	Interval   time.Duration // how often to poll; ingest.DefaultInterval when zero
	// OnError receives failures that cannot be returned, such as a poll that fails in Run or an alert that cannot be shown.
	OnError func(error)
	// OnDecision is told about every notification that was decided. Optional.
	OnDecision func(Decision)
	// Routines loads the saved routines (routines.json). Nil means there are none.
	Routines RoutineLoader
	// PlaySound plays the alert sound, for sessions and routine steps whose alert is "sound". Nil means it is silent.
	PlaySound func() error
}

// Decision is the outcome for one notification.
type Decision struct {
	Notification store.Notification
	// Alerted is true when shh put an alert in front of the user right now. A calendar reminder is stored as alerted but its alert
	// comes later, from the scheduler.
	Alerted bool
}

// Worker is the session pipeline: it watches the notification database and, while a focus level is active, decides for each new
// notification whether it interrupts, stores it, and hides the OS banners.
type Worker struct {
	cfg        Config
	stack      *Stack
	classifier *classify.Classifier
	parser     *calendar.Parser
	routines   *routineRunner
	scheduler  *calendar.Scheduler
	tailer     *ingest.Tailer

	mu          sync.Mutex
	suppressing bool

	subMu       sync.Mutex
	subscribers map[chan Decision]struct{}
}

func NewWorker(cfg Config) (*Worker, error) {
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	classifier, err := classify.NewClassifier(cfg.Store, cfg.Rules, cfg.Embedder)
	if err != nil {
		return nil, err
	}
	w := &Worker{
		cfg: cfg, stack: NewStack(cfg.Store, cfg.Clock), classifier: classifier,
		parser: calendar.NewParser(cfg.Rules, cfg.Location, cfg.Store),
		scheduler: calendar.NewScheduler(calendar.SchedulerConfig{
			Clock: cfg.Clock, Store: cfg.Store, Alerter: cfg.Alerter, Location: cfg.Location, OnError: cfg.OnError,
		}),
	}
	w.routines = newRoutineRunner(w)
	w.tailer = ingest.NewTailer(ingest.Config{
		Source: cfg.Source, Store: cfg.Store, Clock: cfg.Clock, Sources: cfg.Rules.Sources, OwnBundles: cfg.OwnBundles,
		Interval: cfg.Interval, Handle: w.handle, OnError: cfg.OnError,
	})
	return w, nil
}

func (w *Worker) Status() (Status, error) { return w.stack.Status() }

func (w *Worker) SetBaseline(level int) error {
	return w.then(w.stack.SetBaseline(level))
}

func (w *Worker) Escalate(e Escalation) (int64, error) {
	if w.routines.Active() {
		if e.QueueAfterRoutine {
			return 0, w.routines.Queue(e)
		}
		if !e.ContinueRoutine {
			return 0, ErrRoutineRunning
		}
		if err := w.routines.Interrupt(); err != nil {
			return 0, err
		}
	}
	id, err := w.stack.Escalate(e)
	return id, w.then(err)
}

func (w *Worker) Stop() error {
	if w.routines.Active() && !w.routines.Interrupted() { // ending the user's own session over a routine is what Stop is then for
		return ErrRoutineRunning
	}
	return w.then(w.stack.Stop())
}

func (w *Worker) StopAll() error {
	if w.routines.Active() {
		return ErrRoutineRunning
	}
	return w.then(w.stack.StopAll())
}
func (w *Worker) Pause() error  { return w.then(w.stack.Pause()) }
func (w *Worker) Resume() error { return w.then(w.stack.Resume()) }

func (w *Worker) Adjust(delta time.Duration) error { return w.then(w.stack.Adjust(delta)) }
func (w *Worker) SetLevel(level int) error         { return w.then(w.stack.SetLevel(level)) }

// then follows a level change by bringing the banner suppressor in line with the new effective level.
func (w *Worker) then(err error) error {
	if err != nil {
		return err
	}
	return w.syncSuppressor()
}

// syncSuppressor runs the suppressor exactly while the effective level is above 0.
func (w *Worker) syncSuppressor() error {
	st, err := w.stack.Status()
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	switch want := st.Level > 0; {
	case want && !w.suppressing:
		if err := w.cfg.Suppressor.Start(); err != nil {
			return fmt.Errorf("starting the banner suppressor: %w", err)
		}
		w.suppressing = true
	case !want && w.suppressing:
		if err := w.cfg.Suppressor.Stop(); err != nil {
			return fmt.Errorf("stopping the banner suppressor: %w", err)
		}
		w.suppressing = false
	}
	return nil
}

// Init re-arms calendar alerts that were pending when the last worker stopped, starts the suppressor if a level is already
// in force and, when backfillSince is set, handles
// notifications delivered since then that were not handled yet.
func (w *Worker) Init(backfillSince time.Time) error {
	if err := w.scheduler.Rearm(); err != nil {
		return err
	}
	// A baseline that survived a restart protects from the first moment, not from the first tick.
	if err := w.syncSuppressor(); err != nil {
		return err
	}
	if !backfillSince.IsZero() {
		return w.tailer.Backfill(backfillSince)
	}
	return nil
}

// Tick runs one cycle: bring the suppressor in line with the effective level (a timed layer may have ended), then poll for
// new notifications.
func (w *Worker) Tick() error {
	if err := w.routines.Tick(); err != nil && w.cfg.OnError != nil {
		w.cfg.OnError(fmt.Errorf("routine: %w", err))
	}
	if err := w.announceEnded(); err != nil && w.cfg.OnError != nil {
		w.cfg.OnError(fmt.Errorf("session alerts: %w", err))
	}
	if err := w.syncSuppressor(); err != nil {
		return err
	}
	return w.tailer.Poll()
}

// Run initialises the worker and ticks on the clock until ctx is cancelled, then shuts down.
func (w *Worker) Run(ctx context.Context, backfillSince time.Time) error {
	interval := w.cfg.Interval
	if interval == 0 {
		interval = ingest.DefaultInterval
	}
	ticker := w.cfg.Clock.NewTicker(interval)
	defer ticker.Stop()
	defer w.Shutdown()

	if err := w.Init(backfillSince); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			if err := w.Tick(); err != nil && w.cfg.OnError != nil {
				w.cfg.OnError(err)
			}
		}
	}
}

// Shutdown cancels pending calendar alerts (they stay saved for the next start) and stops the banner suppressor.
func (w *Worker) Shutdown() {
	w.scheduler.Stop()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.suppressing {
		if err := w.cfg.Suppressor.Stop(); err != nil && w.cfg.OnError != nil {
			w.cfg.OnError(err)
		}
		w.suppressing = false
	}
}

// handle is the pipeline for one new notification.
func (w *Worker) handle(e ingest.Event) error {
	st, err := w.stack.Status()
	if err != nil {
		return err
	}
	if st.Level == 0 {
		return nil // no focus: the OS shows it, and there is nothing to decide
	}
	top := st.Layers[len(st.Layers)-1]

	in := classify.Input{
		Source: e.Source, BundleID: e.BundleID, Sender: e.Title, Title: e.Title, Subtitle: e.Subtitle, Body: e.Body, Category: e.Category, Thread: string(e.Thread),
	}
	score, err := w.classifier.Score(in, e.Delivered)
	if err != nil {
		return err
	}
	preempt := w.parser.IsReminder(e.Category)
	decision := decide.Decide(st.Level, score.Base, score.Boost, preempt)

	status := store.StatusBlocked
	if decision.Interrupt {
		status = store.StatusAlerted
	}
	level := st.Level
	n := store.Notification{
		Source: e.Source, Sender: e.Title, Title: e.Title, Subtitle: e.Subtitle, Category: e.Category, Body: e.Body, Timestamp: e.Delivered, Status: status,
		Score: score.Base, Boost: score.Boost, Reason: score.Reason, BundleID: e.BundleID, SourceRecID: e.RecID,
		ThresholdAtDecision: &level, SessionID: top.ID, Thread: string(e.Thread),
	}
	id, inserted, err := w.cfg.Store.InsertNotification(n)
	if err != nil || !inserted {
		return err
	}
	n.ID = id

	alerted := false
	switch {
	case preempt:
		err = w.scheduleReminder(e, id)
	case decision.Interrupt:
		alerted = true
		a := messageAlert(e)
		a.NotificationID = id // so the banner's Flag button can say which notification to mark
		if showErr := w.cfg.Alerter.Show(a); showErr != nil && w.cfg.OnError != nil {
			// The row is stored, so retrying would only find it again; the notification stays visible in the inbox.
			w.cfg.OnError(fmt.Errorf("showing the alert for notification %d: %w", id, showErr))
		}
	}
	if err != nil {
		return err
	}
	if decision.Interrupt {
		err = w.cfg.Store.AddCounts(top.ID, 0, 1)
	} else {
		err = w.cfg.Store.AddCounts(top.ID, 1, 0)
	}
	if err != nil {
		return err
	}
	d := Decision{Notification: n, Alerted: alerted}
	if w.cfg.OnDecision != nil {
		w.cfg.OnDecision(d)
	}
	w.publish(d)
	return nil
}

// subscriberBuffer is how many decisions a subscriber may fall behind before it starts missing them.
const subscriberBuffer = 64

// Subscribe returns a stream of decisions and a function that ends it. A subscriber that does not keep up misses decisions
// rather than stalling the pipeline.
func (w *Worker) Subscribe() (<-chan Decision, func()) {
	ch := make(chan Decision, subscriberBuffer)
	w.subMu.Lock()
	if w.subscribers == nil {
		w.subscribers = map[chan Decision]struct{}{}
	}
	w.subscribers[ch] = struct{}{}
	w.subMu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			w.subMu.Lock()
			delete(w.subscribers, ch)
			w.subMu.Unlock()
			close(ch)
		})
	}
}

func (w *Worker) publish(d Decision) {
	w.subMu.Lock()
	defer w.subMu.Unlock()
	for ch := range w.subscribers {
		select {
		case ch <- d:
		default:
		}
	}
}

func (w *Worker) scheduleReminder(e ingest.Event, notificationID int64) error {
	reminder, ok, err := w.parser.Parse(calendar.Input{
		NotificationID: notificationID, Category: e.Category, Title: e.Title, Subtitle: e.Subtitle, Body: e.Body, Delivered: e.Delivered,
	})
	if err != nil || !ok {
		return err
	}
	return w.scheduler.Schedule(reminder, notificationID)
}

func messageAlert(e ingest.Event) alert.Alert {
	body := strings.TrimSpace(strings.TrimSpace(e.Subtitle) + " " + strings.TrimSpace(e.Body))
	if runes := []rune(body); len(runes) > maxAlertBody {
		body = string(runes[:maxAlertBody-1]) + "…"
	}
	return alert.Alert{Kind: config.KindMessage, Source: e.Source, BundleID: e.BundleID, Title: e.Title, Body: body}
}

// showAlert shows an alert sent by a client, such as `shh alert test`, through the worker's own alerter. One process owns the
// banners on screen, so they stack and follow Preview and Dismiss together.
func (w *Worker) showAlert(msg *AlertMsg) error {
	if msg == nil {
		return fmt.Errorf("%w: alert needs an alert", ErrInvalid)
	}
	kind := config.Kind(msg.Kind)
	if kind != config.KindMessage && kind != config.KindCalendar {
		return fmt.Errorf("%w: unknown alert kind %q", ErrInvalid, msg.Kind)
	}
	if w.cfg.Alerter == nil {
		return errors.New("this worker cannot show alerts")
	}
	return w.cfg.Alerter.Show(alert.Alert{Kind: kind, Source: msg.Source, BundleID: msg.BundleID, NotificationID: msg.NotificationID, Title: msg.Title, Body: msg.Body})
}

// RoutineStart begins a saved routine at its first step.
func (w *Worker) RoutineStart(name string) error { return w.routines.Start(name) }

// RoutineNext starts the step a waiting routine is waiting on, or skips to the next step of a running one.
func (w *Worker) RoutineNext() error { return w.routines.Next() }

// RoutineUnqueue drops the session that was to start when the routine ends.
func (w *Worker) RoutineUnqueue() error { return w.routines.Unqueue() }

// RoutineAction is a button on a routine's banner being pressed (see alert.Action).
func (w *Worker) RoutineAction(token, id string) error { return w.routines.Action(token, id) }

// RoutineBack restarts the step before the one in focus.
func (w *Worker) RoutineBack() error { return w.routines.Back() }

// RoutineStop ends the routine run, closing its focus level.
func (w *Worker) RoutineStop() error { return w.routines.Stop() }

// RoutineActive says whether a routine run is in progress. The worker must stay up while one is, even with nothing in force.
func (w *Worker) RoutineActive() bool { return w.routines.Active() }

// RoutineLast is the summary of the run that ended most recently, or nil.
func (w *Worker) RoutineLast() *RoutineSummary { return w.routines.Last() }

// RoutineInfo is the routine run in progress and the last one that ended.
func (w *Worker) RoutineInfo() (RoutineInfo, error) { return w.routines.Info() }

// staleAlert is how long after a session ended its alert can still be given. A worker that was down for longer than this does not
// announce, on its return, something that ended hours ago.
const staleAlert = time.Hour

// announceEnded gives the alert of every session that ran out of time on its own and asked for one (shh focus --alert), once each. A
// session stopped by hand ended before its time and is not announced.
func (w *Worker) announceEnded() error {
	if _, err := w.stack.Status(); err != nil { // settles timers that have run out, closing their sessions
		return err
	}
	due, err := w.cfg.Store.EndedWithAlert()
	if err != nil {
		return err
	}
	now := w.cfg.Clock.Now()
	for _, l := range due {
		// Marked first: an alert that fails is reported once, not retried on every tick for ever.
		if err := w.cfg.Store.MarkAlertDone(l.ID); err != nil {
			return err
		}
		if l.EndTime != nil && now.Sub(*l.EndTime) > staleAlert {
			continue
		}
		switch l.Alert {
		case AlertBanner:
			w.say(alert.Alert{Kind: config.KindMessage, Source: "Focus", Title: "Focus session over", Body: describeEnded(l)})
		case AlertSound:
			w.playSound()
		}
	}
	return nil
}

// describeEnded says what a session that has just ended was: "25m at 75% · writing docs".
func describeEnded(l store.Level) string {
	text := fmt.Sprintf("%s at %s", decide.FormatRemaining(l.TargetMinutes*60), levelText(l.Percent))
	if l.Note != "" {
		text += " · " + l.Note
	}
	return text
}

func (w *Worker) say(a alert.Alert) {
	if err := w.cfg.Alerter.Show(a); err != nil && w.cfg.OnError != nil {
		w.cfg.OnError(fmt.Errorf("showing the alert %q: %w", a.Title, err))
	}
}

func (w *Worker) playSound() {
	if w.cfg.PlaySound == nil {
		return
	}
	if err := w.cfg.PlaySound(); err != nil && w.cfg.OnError != nil {
		w.cfg.OnError(fmt.Errorf("playing the alert sound: %w", err))
	}
}
