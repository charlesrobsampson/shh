package testkit_test

import (
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/clock"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

var start = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func newClock() (*testkit.FakeClock, clock.Clock) {
	fake := testkit.NewFakeClock(start)
	return fake, fake
}

func received(ch <-chan time.Time) (time.Time, bool) {
	select {
	case at := <-ch:
		return at, true
	default:
		return time.Time{}, false
	}
}

func TestTimerFiresOnlyAfterEnoughAdvance(t *testing.T) {
	tests := []struct {
		name      string
		advances  []time.Duration
		wantFired bool
		wantAt    time.Time
	}{
		{"not yet", []time.Duration{9 * time.Second}, false, time.Time{}},
		{"exactly at the deadline", []time.Duration{10 * time.Second}, true, start.Add(10 * time.Second)},
		{"in two steps", []time.Duration{4 * time.Second, 6 * time.Second}, true, start.Add(10 * time.Second)},
		{"far past the deadline reports the deadline", []time.Duration{time.Hour}, true, start.Add(10 * time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, c := newClock()
			timer := c.NewTimer(10 * time.Second)
			for _, d := range tt.advances {
				fake.Advance(d)
			}
			at, fired := received(timer.C())
			if fired != tt.wantFired || (fired && !at.Equal(tt.wantAt)) {
				t.Fatalf("fired=%v at=%v, want fired=%v at=%v", fired, at, tt.wantFired, tt.wantAt)
			}
		})
	}
}

func TestNowOnlyMovesWithAdvance(t *testing.T) {
	fake, c := newClock()
	if !c.Now().Equal(start) {
		t.Fatalf("Now() = %v, want %v", c.Now(), start)
	}
	fake.Advance(90 * time.Second)
	if want := start.Add(90 * time.Second); !c.Now().Equal(want) {
		t.Fatalf("Now() = %v, want %v", c.Now(), want)
	}
}

func TestCallbacksRunInDeadlineOrder(t *testing.T) {
	fake, c := newClock()
	var order []string
	record := func(name string) func() { return func() { order = append(order, name) } }
	c.AfterFunc(3*time.Second, record("three"))
	c.AfterFunc(time.Second, record("one"))
	c.AfterFunc(2*time.Second, record("two"))
	c.AfterFunc(2*time.Second, record("two-later"))
	fake.Advance(5 * time.Second)

	want := []string{"one", "two", "two-later", "three"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestCallbackSeesItsOwnDeadlineAsNow(t *testing.T) {
	fake, c := newClock()
	var seen time.Time
	c.AfterFunc(3*time.Second, func() { seen = c.Now() })
	fake.Advance(time.Minute)
	if want := start.Add(3 * time.Second); !seen.Equal(want) {
		t.Fatalf("Now() inside callback = %v, want %v", seen, want)
	}
	if want := start.Add(time.Minute); !c.Now().Equal(want) {
		t.Fatalf("Now() after Advance = %v, want %v", c.Now(), want)
	}
}

func TestCallbackScheduledDuringAdvanceFiresInTheSameAdvance(t *testing.T) {
	fake, c := newClock()
	var fires []time.Time
	c.AfterFunc(time.Second, func() {
		fires = append(fires, c.Now())
		c.AfterFunc(time.Second, func() { fires = append(fires, c.Now()) })
	})
	fake.Advance(5 * time.Second)
	if len(fires) != 2 || !fires[1].Equal(start.Add(2*time.Second)) {
		t.Fatalf("fires = %v, want two fires ending at +2s", fires)
	}
}

func TestStoppedTimerNeverFires(t *testing.T) {
	tests := []struct {
		name     string
		newTimer func(c clock.Clock, fired *bool) clock.Timer
	}{
		{"channel timer", func(c clock.Clock, _ *bool) clock.Timer { return c.NewTimer(time.Second) }},
		{"callback timer", func(c clock.Clock, fired *bool) clock.Timer {
			return c.AfterFunc(time.Second, func() { *fired = true })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, c := newClock()
			var callbackFired bool
			timer := tt.newTimer(c, &callbackFired)
			if !timer.Stop() {
				t.Fatal("first Stop() = false, want true (timer was active)")
			}
			if timer.Stop() {
				t.Fatal("second Stop() = true, want false")
			}
			fake.Advance(time.Hour)
			if timer.C() != nil {
				if _, fired := received(timer.C()); fired {
					t.Fatal("stopped timer fired")
				}
			}
			if callbackFired {
				t.Fatal("stopped callback ran")
			}
		})
	}
}

func TestStopAfterFiringReportsFalse(t *testing.T) {
	fake, c := newClock()
	timer := c.NewTimer(time.Second)
	fake.Advance(time.Second)
	if timer.Stop() {
		t.Fatal("Stop() after the timer fired = true, want false")
	}
}

func TestTimerFiresOnce(t *testing.T) {
	fake, c := newClock()
	timer := c.NewTimer(time.Second)
	fake.Advance(time.Second)
	received(timer.C())
	fake.Advance(time.Hour)
	if _, fired := received(timer.C()); fired {
		t.Fatal("timer fired a second time")
	}
}

func TestResetRearmsTimer(t *testing.T) {
	fake, c := newClock()
	timer := c.NewTimer(10 * time.Second)
	fake.Advance(4 * time.Second)
	if !timer.Reset(10 * time.Second) {
		t.Fatal("Reset() of an active timer = false, want true")
	}
	fake.Advance(9 * time.Second)
	if _, fired := received(timer.C()); fired {
		t.Fatal("timer fired at the old deadline")
	}
	fake.Advance(time.Second)
	if at, fired := received(timer.C()); !fired || !at.Equal(start.Add(14*time.Second)) {
		t.Fatalf("after Reset: fired=%v at=%v, want +14s", fired, at)
	}
}

func TestTickerFiresOncePerInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		advance  time.Duration
		wantN    int
	}{
		{"less than one interval", time.Minute, 59 * time.Second, 0},
		{"exactly one", time.Minute, time.Minute, 1},
		{"five intervals in one advance", time.Second, 5 * time.Second, 5},
		{"partial interval left over", 10 * time.Second, 35 * time.Second, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, c := newClock()
			ticker := c.NewTicker(tt.interval)
			defer ticker.Stop()
			fake.Advance(tt.advance)
			var got []time.Time
			for {
				at, ok := received(ticker.C())
				if !ok {
					break
				}
				got = append(got, at)
			}
			if len(got) != tt.wantN {
				t.Fatalf("ticks = %d, want %d", len(got), tt.wantN)
			}
			for i, at := range got {
				if want := start.Add(time.Duration(i+1) * tt.interval); !at.Equal(want) {
					t.Fatalf("tick %d at %v, want %v", i, at, want)
				}
			}
		})
	}
}

func TestStoppedTickerStopsTicking(t *testing.T) {
	fake, c := newClock()
	ticker := c.NewTicker(time.Second)
	fake.Advance(2 * time.Second)
	for i := 0; i < 2; i++ {
		if _, ok := received(ticker.C()); !ok {
			t.Fatalf("tick %d missing before Stop", i)
		}
	}
	ticker.Stop()
	fake.Advance(10 * time.Second)
	if _, ok := received(ticker.C()); ok {
		t.Fatal("ticker ticked after Stop")
	}
}

func TestStopDiscardsUnreadTick(t *testing.T) {
	fake, c := newClock()
	timer := c.NewTimer(time.Second)
	fake.Advance(time.Second)
	timer.Stop()
	if _, ok := received(timer.C()); ok {
		t.Fatal("received a stale tick after Stop")
	}
}
