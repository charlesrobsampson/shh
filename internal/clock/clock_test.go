package clock_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/clock"
)

const patience = 2 * time.Second

func TestRealClockNowIsWallTime(t *testing.T) {
	before := time.Now()
	got := clock.Real{}.Now()
	if got.Before(before) || got.After(time.Now()) {
		t.Fatalf("Now() = %v, outside [%v, now]", got, before)
	}
}

func TestRealClockTimersAndTickers(t *testing.T) {
	var c clock.Clock = clock.Real{}

	t.Run("timer fires", func(t *testing.T) {
		timer := c.NewTimer(time.Millisecond)
		select {
		case <-timer.C():
		case <-time.After(patience):
			t.Fatal("timer never fired")
		}
	})
	t.Run("stopped timer reports it was active", func(t *testing.T) {
		timer := c.NewTimer(time.Hour)
		if !timer.Stop() {
			t.Fatal("Stop() = false, want true")
		}
	})
	t.Run("callback runs", func(t *testing.T) {
		var ran atomic.Bool
		c.AfterFunc(time.Millisecond, func() { ran.Store(true) })
		deadline := time.Now().Add(patience)
		for !ran.Load() {
			if time.Now().After(deadline) {
				t.Fatal("callback never ran")
			}
			time.Sleep(time.Millisecond)
		}
	})
	t.Run("ticker ticks repeatedly", func(t *testing.T) {
		ticker := c.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for i := 0; i < 2; i++ {
			select {
			case <-ticker.C():
			case <-time.After(patience):
				t.Fatalf("tick %d never arrived", i)
			}
		}
	})
}
