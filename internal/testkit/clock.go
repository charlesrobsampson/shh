// Package testkit holds in-memory fakes of external systems for tests.
package testkit

import (
	"sync"
	"time"

	"github.com/charlesrobsampson/shh/internal/clock"
)

var _ clock.Clock = (*FakeClock)(nil)

// tickerBuffer lets one Advance across many intervals deliver every tick; a real ticker would drop them.
const tickerBuffer = 1024

// FakeClock only moves when Advance is called, and fires due timers in deadline order.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	created int
	events  []*fakeEvent
}

type fakeEvent struct {
	clock  *FakeClock
	order  int
	at     time.Time
	period time.Duration
	ch     chan time.Time
	fn     func()
	active bool
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) NewTimer(d time.Duration) clock.Timer {
	return c.schedule(d, 0, make(chan time.Time, 1), nil)
}

func (c *FakeClock) AfterFunc(d time.Duration, f func()) clock.Timer {
	return c.schedule(d, 0, nil, f)
}

func (c *FakeClock) NewTicker(d time.Duration) clock.Ticker {
	if d <= 0 {
		panic("testkit: non-positive ticker interval")
	}
	return fakeTicker{c.schedule(d, d, make(chan time.Time, tickerBuffer), nil)}
}

func (c *FakeClock) schedule(d, period time.Duration, ch chan time.Time, fn func()) *fakeEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created++
	e := &fakeEvent{clock: c, order: c.created, at: c.now.Add(d), period: period, ch: ch, fn: fn, active: true}
	c.events = append(c.events, e)
	return e
}

// Advance moves time forward, running everything that comes due on the way. Callbacks may schedule
// more timers, and those fire in the same call if they fall inside the window.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		next := c.earliestDue(target)
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		c.now = next.at
		firedAt, ch, fn := next.at, next.ch, next.fn
		if next.period > 0 {
			next.at = next.at.Add(next.period)
		} else {
			next.active = false
		}
		c.mu.Unlock()

		if ch != nil {
			select {
			case ch <- firedAt:
			default:
			}
		}
		if fn != nil {
			fn()
		}
	}
}

func (c *FakeClock) earliestDue(target time.Time) *fakeEvent {
	var best *fakeEvent
	for _, e := range c.events {
		if !e.active || e.at.After(target) {
			continue
		}
		if best == nil || e.at.Before(best.at) || (e.at.Equal(best.at) && e.order < best.order) {
			best = e
		}
	}
	return best
}

type fakeTicker struct{ *fakeEvent }

func (t fakeTicker) Stop() { t.fakeEvent.Stop() }

func (e *fakeEvent) C() <-chan time.Time { return e.ch }

func (e *fakeEvent) Stop() bool {
	e.clock.mu.Lock()
	defer e.clock.mu.Unlock()
	wasActive := e.active
	e.active = false
	e.drain()
	return wasActive
}

func (e *fakeEvent) Reset(d time.Duration) bool {
	e.clock.mu.Lock()
	defer e.clock.mu.Unlock()
	wasActive := e.active
	e.at = e.clock.now.Add(d)
	e.active = true
	e.drain()
	return wasActive
}

// drain discards undelivered ticks, like Go's own timers since 1.23.
func (e *fakeEvent) drain() {
	for e.ch != nil {
		select {
		case <-e.ch:
		default:
			return
		}
	}
}
