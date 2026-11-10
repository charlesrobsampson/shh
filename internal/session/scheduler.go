package session

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/store"
)

var (
	// ErrNoTrigger means a routine has no trigger that is switched on, so there is no next start to skip.
	ErrNoTrigger = errors.New("that routine has no trigger that is switched on")
	// ErrNotSkipped means the next start of a routine was not skipped, so there is nothing to take back.
	ErrNotSkipped = errors.New("the next start of that routine is not skipped")
)

// scheduleReload is how long the routines file is trusted before it is read again: starts are to the minute, and reading a file every
// tick for it would be wasteful.
const scheduleReload = 10 * time.Second

// scheduleKeep is how long notes about past starts are kept.
const scheduleKeep = 7 * 24 * time.Hour

// scheduler starts routines by their triggers. It keeps no state of its own that matters: what was done about each start is in the store
// (schedule_events), so a worker that restarts does not start anything twice, and a start that was skipped stays skipped.
type scheduler struct {
	w *Worker

	mu       sync.Mutex
	routines []routine.Routine
	loaded   time.Time
	loadErr  string // the last loading problem reported, so each is said once
	pruned   time.Time
}

func newScheduler(w *Worker) *scheduler { return &scheduler{w: w} }

func (s *scheduler) location() *time.Location {
	if s.w.cfg.Location != nil {
		return s.w.cfg.Location
	}
	return time.Local
}

// current is the saved routines, read again when they have not been for a while. A file that cannot be read keeps the last good copy
// and is reported once.
func (s *scheduler) current() []routine.Routine {
	now := s.w.cfg.Clock.Now()
	if s.loaded.IsZero() || now.Sub(s.loaded) >= scheduleReload {
		s.loaded = now
		if s.w.cfg.Routines != nil {
			all, err := s.w.cfg.Routines()
			switch {
			case err != nil:
				if msg := err.Error(); msg != s.loadErr && s.w.cfg.OnError != nil {
					s.loadErr = msg
					s.w.cfg.OnError(fmt.Errorf("routines file (scheduled starts are paused until it is fixed): %w", err))
				}
			default:
				s.routines, s.loadErr = all, ""
			}
		}
	}
	return s.routines
}

// TriggerCount is how many triggers are switched on.
func (s *scheduler) TriggerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, rt := range s.current() {
		for _, t := range rt.Triggers {
			if !t.Disabled {
				n++
			}
		}
	}
	return n
}

// HasTriggers says whether any routine has a trigger that is switched on. The worker stays up while one does: it is what starts them.
func (s *scheduler) HasTriggers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rt := range s.current() {
		for _, t := range rt.Triggers {
			if !t.Disabled {
				return true
			}
		}
	}
	return false
}

// Tick is called regularly. It gives the reminder of a start that is near and does what the trigger says about a start that has come.
func (s *scheduler) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.w.cfg.Clock.Now()
	local := now.In(s.location())
	for _, rt := range s.current() {
		for i, t := range rt.Triggers {
			if t.Disabled {
				continue
			}
			start, ok := t.StartOn(local)
			if !ok {
				continue
			}
			if at, ok := t.ReminderFor(start); ok && !now.Before(at) && now.Before(start) {
				s.remind(rt, i, t, start, now)
			}
			if !now.Before(start) {
				s.due(rt, i, t, start, now)
			}
		}
	}
	if now.Sub(s.pruned) >= 24*time.Hour {
		s.pruned = now
		if err := s.w.cfg.Store.PruneScheduleEvents(now.Add(-scheduleKeep)); err != nil {
			s.report(err)
		}
	}
}

func (s *scheduler) report(err error) {
	if s.w.cfg.OnError != nil {
		s.w.cfg.OnError(fmt.Errorf("scheduled routines: %w", err))
	}
}

// token names a start in a banner's button: the start, which trigger, and the routine. Colons never appear in a routine's name.
func token(rt routine.Routine, trigger int, start time.Time) string {
	return fmt.Sprintf("sched:%d:%d:%s", start.Unix(), trigger, rt.Name)
}

// remind shows the banner that says a start is near, once, unless that start has been skipped.
func (s *scheduler) remind(rt routine.Routine, trigger int, t routine.Trigger, start, now time.Time) {
	first, err := s.w.cfg.Store.RecordScheduleEvent(rt.Name, start, "reminder", "shown", now)
	if err != nil || !first {
		s.reportIf(err)
		return
	}
	if skipped, err := s.isSkipped(rt.Name, start); err != nil || skipped {
		s.reportIf(err)
		return
	}
	s.w.say(alert.Alert{Kind: config.KindMessage, Source: "Routine", Title: fmt.Sprintf("%s starts in %s", rt.Name, formatLength(start.Sub(now).Round(time.Minute))),
		Body: t.Command(), ActionToken: token(rt, trigger, start), Actions: []alert.Action{{ID: "skip", Label: "Skip this one"}}})
}

func (s *scheduler) reportIf(err error) {
	if err != nil {
		s.report(err)
	}
}

func (s *scheduler) isSkipped(name string, start time.Time) (bool, error) {
	e, ok, err := s.w.cfg.Store.ScheduleEventFor(name, start, "start")
	return ok && e.Outcome == "skipped", err
}

// busy says whether something is in the way of a start: a routine running, or a session of the user's in force. A baseline alone is
// not: it is a standing level, not something being done.
func (s *scheduler) busy() (what string, busy bool) {
	if s.w.routines.Active() {
		return "a routine", true
	}
	st, err := s.w.stack.Status()
	if err != nil {
		return "", false
	}
	for _, l := range st.Layers {
		if l.Kind != store.LevelBaseline {
			return "a focus session", true
		}
	}
	return "", false
}

// due does what the trigger says about a start that has come, once: a start that was handled already (or skipped) is left alone, one
// later than the trigger's grace is missed, and the rest are started, asked about, or skipped because something is running.
func (s *scheduler) due(rt routine.Routine, trigger int, t routine.Trigger, start, now time.Time) {
	st := s.w.cfg.Store
	first, err := st.RecordScheduleEvent(rt.Name, start, "start", "due", now)
	if err != nil || !first {
		s.reportIf(err)
		return
	}
	set := func(outcome string) {
		if err := st.SetScheduleEvent(rt.Name, start, "start", outcome, now); err != nil {
			s.report(err)
		}
	}
	if end, ok := t.EndFor(start); now.Sub(start) > t.Grace || (ok && !now.Before(end)) {
		set("missed")
		return
	}
	what, busy := s.busy()
	switch {
	case busy && t.WhenBusy == routine.BusySkip:
		set("skipped: busy")
	case t.Ask || (busy && t.WhenBusy == routine.BusyAsk):
		set("asked")
		s.ask(rt, trigger, t, start, what)
	default:
		set("started")
		if err := s.begin(rt, t, start, now); err != nil {
			set("error: " + err.Error())
			s.report(fmt.Errorf("starting %q: %w", rt.Name, err))
		}
	}
}

// begin starts the routine for a start of the trigger. A routine that is running is ended first; a session of the user's is left
// underneath. A start that is already past its end time is not made.
func (s *scheduler) begin(rt routine.Routine, t routine.Trigger, start, now time.Time) error {
	end, hasEnd := t.EndFor(start)
	if hasEnd && !now.Before(end) {
		return errors.New("its end time has already gone by")
	}
	if s.w.routines.Active() {
		if err := s.w.routines.Stop(); err != nil && !errors.Is(err, ErrNoRoutine) {
			return err
		}
	}
	if hasEnd {
		return s.w.routines.StartScheduled(rt.Name, end, t.UntilAlert)
	}
	return s.w.routines.Start(rt.Name)
}

// ask shows the banner that offers a start: Start now, or Skip.
func (s *scheduler) ask(rt routine.Routine, trigger int, t routine.Trigger, start time.Time, busyWith string) {
	body := t.Command()
	if busyWith != "" {
		body = fmt.Sprintf("%s is running: starting now ends it (a focus session of yours stays underneath). %s", busyWith, t.Command())
	}
	s.w.say(alert.Alert{Kind: config.KindMessage, Source: "Routine", Title: fmt.Sprintf("Start %s?", rt.Name), Body: body,
		ActionToken: token(rt, trigger, start), Actions: []alert.Action{{ID: "start", Label: "Start now"}, {ID: "skip", Label: "Skip"}}})
}

// Action is a button of a scheduled routine's banner being pressed.
func (s *scheduler) Action(tok, id string) error {
	parts := strings.SplitN(strings.TrimPrefix(tok, "sched:"), ":", 3)
	if len(parts) != 3 {
		return nil
	}
	unix, err1 := strconv.ParseInt(parts[0], 10, 64)
	trigger, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, ok := routine.Find(s.current(), parts[2])
	if !ok || trigger < 0 || trigger >= len(rt.Triggers) {
		return nil // the routine or its triggers have changed since the banner was shown
	}
	t, start, now := rt.Triggers[trigger], time.Unix(unix, 0), s.w.cfg.Clock.Now()
	st := s.w.cfg.Store
	switch id {
	case "skip":
		return st.SetScheduleEvent(rt.Name, start, "start", "skipped", now)
	case "start":
		// Only a start that was asked about can be answered, and once.
		if e, ok, err := st.ScheduleEventFor(rt.Name, start, "start"); err != nil || !ok || e.Outcome != "asked" {
			return err
		}
		if err := st.SetScheduleEvent(rt.Name, start, "start", "started", now); err != nil {
			return err
		}
		return s.begin(rt, t, start, now)
	}
	return nil
}

// next is the next start of a routine's enabled triggers and which trigger it is.
func (s *scheduler) next(name string) (routine.Routine, time.Time, error) {
	rt, ok := routine.Find(s.current(), name)
	if !ok {
		return routine.Routine{}, time.Time{}, fmt.Errorf("%w: %q", ErrUnknownRoutine, name)
	}
	start, _, ok := rt.NextStart(s.w.cfg.Clock.Now().In(s.location()))
	if !ok {
		return rt, time.Time{}, ErrNoTrigger
	}
	return rt, start, nil
}

// Skip leaves out the routine's next scheduled start.
func (s *scheduler) Skip(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, start, err := s.next(name)
	if err != nil {
		return err
	}
	return s.w.cfg.Store.SetScheduleEvent(rt.Name, start, "start", "skipped", s.w.cfg.Clock.Now())
}

// Unskip takes back a skip of the routine's next scheduled start.
func (s *scheduler) Unskip(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, start, err := s.next(name)
	if err != nil {
		return err
	}
	skipped, err := s.isSkipped(rt.Name, start)
	if err != nil {
		return err
	}
	if !skipped {
		return ErrNotSkipped
	}
	_, err = s.w.cfg.Store.DeleteScheduleEvent(rt.Name, start, "start")
	return err
}

// Upcoming is the next start of every routine that has a trigger switched on, soonest first.
func (s *scheduler) Upcoming() []UpcomingStart {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []UpcomingStart
	now := s.w.cfg.Clock.Now().In(s.location())
	for _, rt := range s.current() {
		start, index, ok := rt.NextStart(now)
		if !ok {
			continue
		}
		u := UpcomingStart{Routine: rt.Name, Trigger: rt.Triggers[index].Command(), Start: start}
		if skipped, err := s.isSkipped(rt.Name, start); err == nil && skipped {
			u.Skipped = true
			if then, _, ok := rt.NextStart(start); ok {
				u.Then = &then
			}
		}
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b UpcomingStart) int { return a.Start.Compare(b.Start) })
	return out
}
