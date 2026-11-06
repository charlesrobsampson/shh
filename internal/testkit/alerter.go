package testkit

import (
	"sync"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/suppress"
)

var (
	_ alert.Alerter       = (*FakeAlerter)(nil)
	_ suppress.Suppressor = (*FakeSuppressor)(nil)
)

// FakeAlerter remembers every alert it was asked to show.
type FakeAlerter struct {
	mu    sync.Mutex
	shown []alert.Alert
	err   error
}

func NewFakeAlerter() *FakeAlerter { return &FakeAlerter{} }

func (f *FakeAlerter) Show(a alert.Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.shown = append(f.shown, a)
	return nil
}

// Shown returns a copy of what was shown, oldest first.
func (f *FakeAlerter) Shown() []alert.Alert {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]alert.Alert(nil), f.shown...)
}

// FailWith makes every later Show fail with err until it is called with nil.
func (f *FakeAlerter) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// FakeSuppressor holds whether the banner suppressor is running.
type FakeSuppressor struct {
	mu      sync.Mutex
	running bool
	starts  int
	stops   int
	err     error
}

func NewFakeSuppressor() *FakeSuppressor { return &FakeSuppressor{} }

func (f *FakeSuppressor) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if !f.running {
		f.running = true
		f.starts++
	}
	return nil
}

func (f *FakeSuppressor) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		f.running = false
		f.stops++
	}
	return nil
}

func (f *FakeSuppressor) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// Starts and Stops count the calls that actually changed state.
func (f *FakeSuppressor) Starts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

func (f *FakeSuppressor) Stops() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

// FailWith makes Start fail with err until it is called with nil.
func (f *FakeSuppressor) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}
