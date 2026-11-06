package session

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/charlesrobsampson/shh/internal/clock"
	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/store"
)

var (
	ErrNothingToStop = errors.New("no focus escalation to stop (use stop --all to clear the baseline too)")
	ErrNoTimer       = errors.New("no running timer")
	ErrNotPaused     = errors.New("no paused timer")
	ErrNoSession     = errors.New("no focus session is active")
)

// Escalation is a temporary focus level on top of the baseline. Duration 0 means until stopped.
type Escalation struct {
	Level    int
	Duration time.Duration
	// Then replaces everything beneath when the timer ends: 0 clears the baseline too, nil falls back to the layer beneath.
	Then *int
	Note string
}

// Layer is one level of the stack with its time left.
type Layer struct {
	store.Level
	Timed     bool
	Paused    bool
	Remaining time.Duration // timed layers only
}

type Status struct {
	// Level is the effective threshold: the top layer's, or 0 when nothing is active.
	Level  int
	Layers []Layer // bottom first
}

// Stack is the focus level stack (plan.md section 3.1). It keeps no state of its own, so a restart resumes from the store.
// Expired timers are settled lazily at each call, each closed at its own deadline.
type Stack struct {
	mu    sync.Mutex
	store *store.Store
	clock clock.Clock
}

func NewStack(s *store.Store, c clock.Clock) *Stack {
	return &Stack{store: s, clock: c}
}

// SetBaseline replaces the baseline; 0 turns it off.
func (s *Stack) SetBaseline(level int) error {
	if err := checkLevel(level); err != nil {
		return err
	}
	return s.act(func(now time.Time, layers []store.Level) error {
		if len(layers) > 0 && layers[0].Kind == store.LevelBaseline {
			if err := s.store.CloseLevel(layers[0].ID, now); err != nil {
				return err
			}
		}
		if level == 0 {
			return nil
		}
		_, err := s.store.PushLevel(store.Level{Kind: store.LevelBaseline, Percent: level, StartTime: now})
		return err
	})
}

// Escalate puts a new level on top of the stack and returns its id.
func (s *Stack) Escalate(e Escalation) (id int64, err error) {
	if err := checkLevel(e.Level); err != nil {
		return 0, err
	}
	if e.Then != nil {
		if err := checkLevel(*e.Then); err != nil {
			return 0, err
		}
	}
	if e.Duration < 0 {
		return 0, fmt.Errorf("duration %v is negative", e.Duration)
	}
	err = s.act(func(now time.Time, _ []store.Level) error {
		level := store.Level{Kind: store.LevelEscalation, Percent: e.Level, StartTime: now, ThenLevel: e.Then, Note: e.Note}
		if e.Duration > 0 {
			expires := now.Add(e.Duration)
			level.ExpiresAt = &expires
			level.TargetMinutes = int((e.Duration + time.Minute - 1) / time.Minute)
		}
		id, err = s.store.PushLevel(level)
		return err
	})
	return id, err
}

// Stop ends the top escalation, falling back to the layer beneath whatever its Then says.
func (s *Stack) Stop() error {
	return s.act(func(now time.Time, _ []store.Level) error {
		_, err := s.store.PopLevel(now)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNothingToStop
		}
		return err
	})
}

// StopAll ends every layer, the baseline included.
func (s *Stack) StopAll() error {
	return s.act(func(now time.Time, layers []store.Level) error {
		for _, l := range layers {
			if err := s.store.CloseLevel(l.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Pause freezes the top running timer. The focus level stays in force; only the countdown stops.
func (s *Stack) Pause() error {
	return s.act(func(now time.Time, layers []store.Level) error {
		l, ok := topWhere(layers, func(l store.Level) bool { return l.ExpiresAt != nil })
		if !ok {
			return ErrNoTimer
		}
		return s.store.PauseLevel(l.ID, now)
	})
}

// Resume restarts the top paused timer with the time it had left.
func (s *Stack) Resume() error {
	return s.act(func(now time.Time, layers []store.Level) error {
		l, ok := topWhere(layers, func(l store.Level) bool { return l.PausedAt != nil })
		if !ok {
			return ErrNotPaused
		}
		return s.store.ResumeLevel(l.ID, now)
	})
}

// Adjust adds to or removes from the top timer, running or paused. A cut past the end expires it now.
func (s *Stack) Adjust(delta time.Duration) error {
	return s.act(func(now time.Time, layers []store.Level) error {
		l, ok := topWhere(layers, func(l store.Level) bool { return l.ExpiresAt != nil || l.PausedAt != nil })
		if !ok {
			return ErrNoTimer
		}
		if l.PausedAt != nil {
			return s.store.SetRemainingSecs(l.ID, max(*l.RemainingSecs+int(delta/time.Second), 0))
		}
		return s.store.SetExpiresAt(l.ID, laterOf(l.ExpiresAt.Add(delta), now))
	})
}

// SetLevel changes the threshold of the top layer.
func (s *Stack) SetLevel(level int) error {
	if err := checkLevel(level); err != nil {
		return err
	}
	return s.act(func(_ time.Time, layers []store.Level) error {
		if len(layers) == 0 {
			return ErrNoSession
		}
		return s.store.SetPercent(layers[len(layers)-1].ID, level)
	})
}

func (s *Stack) Status() (Status, error) {
	var st Status
	err := s.act(func(now time.Time, layers []store.Level) error {
		for _, l := range layers {
			layer := Layer{Level: l, Timed: l.ExpiresAt != nil || l.PausedAt != nil, Paused: l.PausedAt != nil}
			switch {
			case l.PausedAt != nil:
				layer.Remaining = time.Duration(*l.RemainingSecs) * time.Second
			case l.ExpiresAt != nil:
				layer.Remaining = l.ExpiresAt.Sub(now)
			}
			st.Layers = append(st.Layers, layer)
		}
		if len(layers) > 0 {
			st.Level = layers[len(layers)-1].Percent
		}
		return nil
	})
	return st, err
}

// NextExpiry is when the earliest running timer ends, for scheduling a wake-up. Paused and indefinite layers have none.
func (s *Stack) NextExpiry() (at time.Time, ok bool, err error) {
	err = s.act(func(_ time.Time, layers []store.Level) error {
		for _, l := range layers {
			if l.ExpiresAt != nil && (!ok || l.ExpiresAt.Before(at)) {
				at, ok = *l.ExpiresAt, true
			}
		}
		return nil
	})
	return at, ok, err
}

// act settles expired timers, runs fn on the resulting stack, then settles again in case fn made something expire.
func (s *Stack) act(fn func(now time.Time, layers []store.Level) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	layers, err := s.settle(now)
	if err != nil {
		return err
	}
	if err := fn(now, layers); err != nil {
		return err
	}
	_, err = s.settle(now)
	return err
}

// settle closes every timer that has run out, earliest first, applying each one's Then, and returns the stack that remains.
func (s *Stack) settle(now time.Time) ([]store.Level, error) {
	for {
		layers, err := s.store.ActiveLevels()
		if err != nil {
			return nil, err
		}
		expired := -1
		for i, l := range layers {
			if l.ExpiresAt != nil && !l.ExpiresAt.After(now) && (expired < 0 || l.ExpiresAt.Before(*layers[expired].ExpiresAt)) {
				expired = i
			}
		}
		if expired < 0 {
			return layers, nil
		}
		ended := layers[expired]
		end := *ended.ExpiresAt
		if err := s.store.CloseLevel(ended.ID, end); err != nil {
			return nil, err
		}
		if ended.ThenLevel == nil {
			continue
		}
		for _, beneath := range layers[:expired] {
			if err := s.store.CloseLevel(beneath.ID, end); err != nil {
				return nil, err
			}
		}
		if *ended.ThenLevel > 0 {
			if _, err := s.store.PushLevel(store.Level{Kind: store.LevelBaseline, Percent: *ended.ThenLevel, StartTime: end}); err != nil {
				return nil, err
			}
		}
	}
}

func topWhere(layers []store.Level, match func(store.Level) bool) (store.Level, bool) {
	for i := len(layers) - 1; i >= 0; i-- {
		if match(layers[i]) {
			return layers[i], true
		}
	}
	return store.Level{}, false
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func checkLevel(level int) error {
	if level < 0 || level > decide.MaxLevel {
		return fmt.Errorf("level %d is outside 0-%d", level, decide.MaxLevel)
	}
	return nil
}
