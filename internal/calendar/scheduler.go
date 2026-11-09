package calendar

import (
	"fmt"
	"sync"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/clock"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/store"
)

// DefaultLead is how long before a meeting starts the alert fires.
const DefaultLead = 60 * time.Second

type SchedulerConfig struct {
	Clock   clock.Clock
	Store   *store.Store
	Alerter alert.Alerter
	// Lead is how long before the start to alert; DefaultLead when zero.
	Lead time.Duration
	// Location is the zone used for the times in the alert text; time.Local when nil.
	Location *time.Location
	// OnError receives failures from alerts that fire on a timer. Optional.
	OnError func(error)
}

// Scheduler fires an alert shortly before each meeting, even at the highest focus level. Events are saved, so a restart re-arms
// what is still pending (Rearm), and each event fires at most once however often it is reported.
type Scheduler struct {
	cfg SchedulerConfig

	mu    sync.Mutex
	armed map[int64]clock.Timer
}

func NewScheduler(cfg SchedulerConfig) *Scheduler {
	if cfg.Lead == 0 {
		cfg.Lead = DefaultLead
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	return &Scheduler{cfg: cfg, armed: map[int64]clock.Timer{}}
}

// Schedule saves the reminder and arms its alert. One that was already scheduled, or already fired, is ignored. Reminders with
// no start time (all-day or unreadable) alert right away; repeats of one are told apart by title and delivery minute.
func (s *Scheduler) Schedule(r Reminder, sourceNotificationID int64) error {
	now := s.cfg.Clock.Now()
	title := r.Title
	if title == "" {
		title = "Meeting"
	}
	event := store.CalendarEvent{Title: title, SourceNotificationID: sourceNotificationID}
	if r.AlertNow() {
		delivered := r.Delivered
		if delivered.IsZero() {
			delivered = now
		}
		event.Start = delivered.Truncate(time.Minute)
		event.AlertAt = event.Start
	} else {
		event.Start = r.Start
		event.AlertAt = r.Start.Add(-s.cfg.Lead)
	}
	id, inserted, err := s.cfg.Store.AddCalendarEvent(event)
	if err != nil || !inserted {
		return err
	}
	event.ID = id
	return s.arm(event, r.AlertNow())
}

// Rearm arms every saved event that has not fired. Those whose time has passed alert now, late rather than never.
func (s *Scheduler) Rearm() error {
	pending, err := s.cfg.Store.PendingCalendarEvents()
	if err != nil {
		return err
	}
	for _, event := range pending {
		s.mu.Lock()
		_, already := s.armed[event.ID]
		s.mu.Unlock()
		if already {
			continue
		}
		// Events with no start time were saved with the alert time equal to the start.
		if err := s.arm(event, event.AlertAt.Equal(event.Start)); err != nil {
			return err
		}
	}
	return nil
}

// Stop cancels the armed alerts. They stay saved for the next Rearm.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, timer := range s.armed {
		timer.Stop()
		delete(s.armed, id)
	}
}

func (s *Scheduler) arm(event store.CalendarEvent, untimed bool) error {
	wait := event.AlertAt.Sub(s.cfg.Clock.Now())
	if wait <= 0 {
		return s.fire(event.ID, untimed)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed[event.ID] = s.cfg.Clock.AfterFunc(wait, func() {
		if err := s.fire(event.ID, untimed); err != nil && s.cfg.OnError != nil {
			s.cfg.OnError(err)
		}
	})
	return nil
}

// fire shows the alert and then marks the event fired, all under the lock, so two paths to the same event cannot both show it.
// If showing fails the event stays pending for the next Rearm.
func (s *Scheduler) fire(id int64, untimed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.armed, id)
	event, err := s.cfg.Store.GetCalendarEvent(id)
	if err != nil || event.Fired {
		return err
	}
	if err := s.cfg.Alerter.Show(s.alertFor(event, untimed)); err != nil {
		return err
	}
	_, err = s.cfg.Store.MarkCalendarEventFired(id)
	return err
}

func (s *Scheduler) alertFor(event store.CalendarEvent, untimed bool) alert.Alert {
	body := "Meeting reminder"
	if !untimed {
		at := event.Start.In(s.cfg.Location).Format("15:04")
		if s.cfg.Clock.Now().Before(event.Start) {
			body = fmt.Sprintf("Starts at %s", at)
		} else {
			body = fmt.Sprintf("Started at %s", at)
		}
	}
	return alert.Alert{
		Kind: config.KindCalendar, Source: "Calendar", BundleID: s.sourceBundle(event), NotificationID: event.SourceNotificationID,
		Title: event.Title, Body: body,
	}
}

// sourceBundle is the app the reminder came from, so that the alert's Go to button opens it (Outlook's reminders carry no meeting
// link, but the meeting's Join is one click away in the app). Empty when the event has no source notification or it cannot be read:
// the alert is still shown, just without something to open.
func (s *Scheduler) sourceBundle(event store.CalendarEvent) string {
	if event.SourceNotificationID == 0 {
		return ""
	}
	n, err := s.cfg.Store.GetNotification(event.SourceNotificationID)
	if err != nil {
		return ""
	}
	return n.BundleID
}
