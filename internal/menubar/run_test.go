package menubar_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/menubar"
	"github.com/charlesrobsampson/shh/internal/session"
)

type fakeController struct {
	mu     sync.Mutex
	status session.StatusView
	err    error
	calls  []string
}

func (c *fakeController) record(call string) (session.StatusView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
	return c.status, c.err
}

func (c *fakeController) Status() (session.StatusView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status, c.err
}
func (c *fakeController) Pause() (session.StatusView, error)  { return c.record("pause") }
func (c *fakeController) Resume() (session.StatusView, error) { return c.record("resume") }
func (c *fakeController) Adjust(d time.Duration) (session.StatusView, error) {
	return c.record("adjust " + d.String())
}
func (c *fakeController) Stop(all bool) (session.StatusView, error) {
	return c.record(fmt.Sprintf("stop all=%v", all))
}
func (c *fakeController) RoutineNext() (session.RoutineInfo, error) {
	_, err := c.record("routine next")
	return session.RoutineInfo{}, err
}
func (c *fakeController) RoutineBack() (session.RoutineInfo, error) {
	_, err := c.record("routine back")
	return session.RoutineInfo{}, err
}
func (c *fakeController) RoutineStop() (session.RoutineInfo, error) {
	_, err := c.record("routine stop")
	return session.RoutineInfo{}, err
}
func (c *fakeController) set(st session.StatusView, err error) {
	c.mu.Lock()
	c.status, c.err = st, err
	c.mu.Unlock()
}
func (c *fakeController) called() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.calls, ",")
}

type fakeHelper struct {
	mu      sync.Mutex
	sent    []menubar.View
	clicks  chan string
	stopped bool
}

func newFakeHelper() *fakeHelper { return &fakeHelper{clicks: make(chan string, 8)} }

func (h *fakeHelper) Send(v menubar.View) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sent = append(h.sent, v)
	return nil
}
func (h *fakeHelper) Clicks() <-chan string { return h.clicks }
func (h *fakeHelper) Stop()                 { h.mu.Lock(); h.stopped = true; h.mu.Unlock() }
func (h *fakeHelper) titles() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, v := range h.sent {
		out = append(out, v.Title)
	}
	return out
}
func (h *fakeHelper) isStopped() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.stopped }

type rig struct {
	t       *testing.T
	ctl     *fakeController
	started []*fakeHelper
	mu      sync.Mutex
	errs    []error
	opened  int
	done    chan error
	cancel  context.CancelFunc
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, ctl: &fakeController{status: session.StatusView{Level: 30, Layers: []session.LayerView{baseline}}}, done: make(chan error, 1)}
	return r
}

func (r *rig) start(tweak ...func(*menubar.Config)) {
	r.t.Helper()
	cfg := menubar.Config{
		Controller: r.ctl, Label: "shh", Interval: 5 * time.Millisecond, RestartDelay: 5 * time.Millisecond, GoneAfter: 2,
		Open: func() error { r.mu.Lock(); r.opened++; r.mu.Unlock(); return nil },
		Start: func() (menubar.Helper, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			h := newFakeHelper()
			r.started = append(r.started, h)
			return h, nil
		},
		OnError: func(err error) { r.mu.Lock(); r.errs = append(r.errs, err); r.mu.Unlock() },
	}
	for _, f := range tweak {
		f(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.t.Cleanup(cancel)
	go func() { r.done <- menubar.Run(ctx, cfg) }()
}

func (r *rig) helper(i int) *fakeHelper {
	r.t.Helper()
	r.eventually(fmt.Sprintf("helper %d to start", i), func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.started) > i })
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started[i]
}

func (r *rig) eventually(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s", what)
}

func TestTheItemShowsTheWorkersStatusAndOnlySendsChanges(t *testing.T) {
	r := newRig(t)
	r.start()
	h := r.helper(0)
	r.eventually("the first title", func() bool { return len(h.titles()) == 1 })
	time.Sleep(40 * time.Millisecond) // many polls with nothing new
	if got := h.titles(); len(got) != 1 || got[0] != "shh 30%" {
		t.Fatalf("titles = %q, want one send of %q however many polls happen", got, "shh 30%")
	}
	r.ctl.set(session.StatusView{Level: 65, Layers: []session.LayerView{baseline, timed}}, nil)
	r.eventually("the new title", func() bool { return len(h.titles()) == 2 })
	if got := h.titles()[1]; got != "shh 28m · 65%" {
		t.Errorf("second title = %q", got)
	}
}

func TestClicksActOnTheWorkerAndRefreshTheItem(t *testing.T) {
	r := newRig(t)
	r.ctl.set(session.StatusView{Level: 65, Layers: []session.LayerView{baseline, timed}}, nil)
	r.start(func(c *menubar.Config) { c.Interval = time.Hour }) // only clicks refresh
	h := r.helper(0)
	r.eventually("the first title", func() bool { return len(h.titles()) == 1 })

	for _, id := range []string{"pause", "resume", "plus", "minus", "stop", "off", "open", "nonsense"} {
		h.clicks <- id
	}
	r.eventually("every click to be handled", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.opened == 1 && strings.Count(r.ctl.called(), ",") == 5
	})
	want := "pause,resume,adjust 5m0s,adjust -5m0s,stop all=false,stop all=true"
	if got := r.ctl.called(); got != want {
		t.Errorf("worker calls = %s, want %s", got, want)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.errs) != 0 {
		t.Errorf("errors reported: %v", r.errs)
	}
}

func TestAFailedActionIsReportedAndTheItemKeepsGoing(t *testing.T) {
	r := newRig(t)
	r.start()
	h := r.helper(0)
	r.eventually("the first title", func() bool { return len(h.titles()) == 1 })
	r.ctl.set(session.StatusView{Level: 30, Layers: []session.LayerView{baseline}}, errors.New("no timer to pause"))
	h.clicks <- "pause"
	r.eventually("the error", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.errs) > 0 })
	r.mu.Lock()
	msg := r.errs[0].Error()
	r.mu.Unlock()
	if !strings.Contains(msg, "pause") || !strings.Contains(msg, "no timer to pause") {
		t.Errorf("error %q should name the item and the cause", msg)
	}
	select {
	case err := <-r.done:
		t.Fatalf("Run returned after a failed click: %v", err)
	default:
	}
}

func TestRunEndsWhenTheWorkerIsGone(t *testing.T) {
	r := newRig(t)
	r.start()
	h := r.helper(0)
	r.eventually("the first title", func() bool { return len(h.titles()) == 1 })
	r.ctl.set(session.StatusView{}, session.ErrNotRunning)
	select {
	case err := <-r.done:
		if err != nil {
			t.Errorf("err = %v, want a clean end: the menu bar leaves with the worker", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going with no worker")
	}
	if !h.isStopped() {
		t.Error("the status item was left on screen")
	}
}

func TestOneMissedPollIsNotEnoughToLeave(t *testing.T) {
	r := newRig(t)
	r.start(func(c *menubar.Config) { c.GoneAfter = 3; c.Interval = 20 * time.Millisecond })
	h := r.helper(0)
	r.eventually("the first title", func() bool { return len(h.titles()) == 1 })
	r.ctl.set(session.StatusView{}, session.ErrNotRunning)
	time.Sleep(30 * time.Millisecond) // one or two polls
	r.ctl.set(session.StatusView{Level: 30, Layers: []session.LayerView{baseline}}, nil)
	time.Sleep(100 * time.Millisecond)
	select {
	case <-r.done:
		t.Fatal("Run ended after a short gap: a worker restarting should not take the item away")
	default:
	}
}

func TestCancellingRemovesTheItem(t *testing.T) {
	r := newRig(t)
	r.start()
	h := r.helper(0)
	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			t.Errorf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if !h.isStopped() {
		t.Error("the status item was left on screen")
	}
}

func TestAnItemThatDiesIsBroughtBack(t *testing.T) {
	r := newRig(t)
	r.start()
	first := r.helper(0)
	r.eventually("the first title", func() bool { return len(first.titles()) == 1 })
	close(first.clicks) // the osascript process died
	second := r.helper(1)
	r.eventually("the new item to be filled in", func() bool { return len(second.titles()) == 1 })
	if got := second.titles()[0]; got != "shh 30%" {
		t.Errorf("new item title = %q, want it brought up to date at once", got)
	}
}

func TestAnItemThatCannotStartIsAnError(t *testing.T) {
	r := newRig(t)
	r.start(func(c *menubar.Config) {
		c.Start = func() (menubar.Helper, error) { return nil, errors.New("osascript: not found") }
	})
	select {
	case err := <-r.done:
		if err == nil || !strings.Contains(err.Error(), "osascript: not found") {
			t.Errorf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not report the failure")
	}
}

func TestTheDefaultHelperRunsTheEmbeddedScript(t *testing.T) {
	if !strings.Contains(menubar.Script(), "NSStatusBar") || !strings.Contains(menubar.Script(), "getppid") {
		t.Error("the embedded script does not look like the status item script")
	}
	for _, want := range []string{"event click", "SHH_MENUBAR", "monospacedDigitSystemFont", "cfg.parent"} {
		if !strings.Contains(menubar.Script(), want) {
			t.Errorf("the status item script does not mention %s", want)
		}
	}
}

func TestRoutineItemsSteerTheRoutine(t *testing.T) {
	r := newRig(t)
	r.ctl.set(session.StatusView{Level: 75, Layers: []session.LayerView{timed}, Routine: &session.RoutineView{Name: "pomo", Step: "work (1/2)"}}, nil)
	r.start(func(c *menubar.Config) { c.Interval = time.Hour })
	h := r.helper(0)
	r.eventually("the first title", func() bool { return len(h.titles()) == 1 })
	for _, id := range []string{"routine_next", "routine_back", "routine_stop"} {
		h.clicks <- id
	}
	r.eventually("every click to be handled", func() bool { return strings.Count(r.ctl.called(), ",") == 2 })
	if got, want := r.ctl.called(), "routine next,routine back,routine stop"; got != want {
		t.Errorf("worker calls = %s, want %s", got, want)
	}
}

func TestTheItemLeavesWhenItsWorkerHasGoneEvenIfAnotherAnswersTheSocket(t *testing.T) {
	r := newRig(t)
	r.ctl.set(session.StatusView{Level: 30, Layers: []session.LayerView{baseline}}, nil) // a worker answers
	var gone atomic.Bool
	r.start(func(c *menubar.Config) {
		c.Interval = 10 * time.Millisecond
		c.Gone = gone.Load
	})
	h := r.helper(0)
	r.eventually("the first title", func() bool { return len(h.titles()) == 1 })
	select {
	case err := <-r.done:
		t.Fatalf("Run returned while its worker was still there: %v", err)
	default:
	}
	gone.Store(true)
	select {
	case err := <-r.done:
		if err != nil {
			t.Errorf("err = %v, want a clean end", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the item stayed although the worker that started it had gone: a restarted worker would be left with two items")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !h.stopped {
		t.Error("the status item itself should be taken down")
	}
}

func TestTheReviewItemOpensTheReviewAndFallsBackToOpen(t *testing.T) {
	r := newRig(t)
	r.ctl.set(session.StatusView{Level: 30, Layers: []session.LayerView{baseline}}, nil)
	var reviewed atomic.Int32
	r.start(func(c *menubar.Config) {
		c.Interval = time.Hour
		c.Review = func() error { reviewed.Add(1); return nil }
	})
	h := r.helper(0)
	r.eventually("the first title", func() bool { return len(h.titles()) == 1 })
	h.clicks <- menubar.IDReview
	r.eventually("the review", func() bool { return reviewed.Load() == 1 })
	r.mu.Lock()
	opened := r.opened
	r.mu.Unlock()
	if opened != 0 {
		t.Errorf("with a Review handler Open is not used: %d", opened)
	}
}
