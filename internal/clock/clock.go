// Package clock abstracts time so logic can be tested deterministically.
package clock

import "time"

type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
	// AfterFunc runs f once after d. Timer.C returns nil for the result.
	AfterFunc(d time.Duration, f func()) Timer
	NewTicker(d time.Duration) Ticker
}

type Timer interface {
	C() <-chan time.Time
	// Stop reports whether the timer was still pending.
	Stop() bool
	// Reset re-arms the timer and reports whether it was still pending.
	Reset(d time.Duration) bool
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Real is the wall clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

func (Real) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

func (Real) AfterFunc(d time.Duration, f func()) Timer { return realTimer{time.AfterFunc(d, f)} }

func (Real) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time        { return r.t.C }
func (r realTimer) Stop() bool                 { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }
