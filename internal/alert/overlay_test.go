package alert_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
)

// fakeProcess stands in for one osascript banner process: it runs until finish is called.
type fakeProcess struct {
	env    []string
	finish chan string
}

type overlayRig struct {
	t         *testing.T
	mu        sync.Mutex
	started   []*fakeProcess
	failures  []error
	failedFor []alert.Request
	startErr  error
	overlay   *alert.Overlay
}

func newOverlayRig(t *testing.T) *overlayRig {
	t.Helper()
	r := &overlayRig{t: t}
	r.overlay = alert.NewOverlay(alert.OverlayConfig{
		Start: func(env []string) (func() (string, error), error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.startErr != nil {
				return nil, r.startErr
			}
			p := &fakeProcess{env: env, finish: make(chan string, 1)}
			r.started = append(r.started, p)
			return func() (string, error) {
				outcome := <-p.finish
				if outcome == "crash" {
					return "", errors.New("osascript: exit status 1")
				}
				return outcome, nil
			}, nil
		},
		OnFailure: func(req alert.Request, err error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.failures, r.failedFor = append(r.failures, err), append(r.failedFor, req)
		},
	})
	return r
}

func (r *overlayRig) show(req alert.Request) {
	r.t.Helper()
	if err := r.overlay.Show(req); err != nil {
		r.t.Fatalf("Show: %v", err)
	}
}

func (r *overlayRig) process(i int) *fakeProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started[i]
}

func (r *overlayRig) eventually(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		ok := cond()
		r.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s", what)
}

type banner struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Bundle   string `json:"bundle"`
	Color    string `json:"color"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Position string `json:"position"`
	Slot     int    `json:"slot"`
	Duration int    `json:"duration"`
	Sound    string `json:"sound"`
}

func decode(t *testing.T, p *fakeProcess) banner {
	t.Helper()
	for _, kv := range p.env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
			var b banner
			if err := json.Unmarshal([]byte(v), &b); err != nil {
				t.Fatalf("SHH_BANNER is not JSON: %v: %s", err, v)
			}
			return b
		}
	}
	t.Fatal("no SHH_BANNER in the process environment")
	return banner{}
}

func request(title, position string) alert.Request {
	return alert.Request{
		Title: title, Body: "body of " + title, BundleID: "com.microsoft.teams2", Kind: config.KindMessage,
		Style: config.Style{Position: position, Size: "medium", Color: "#ff9f0a", DurationSecs: 8, Sound: "Glass"}, Width: 360, Height: 84,
	}
}

func TestTheBannerIsDescribedToTheHelperAsJSON(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("Ana", "top-right"))
	got := decode(t, r.process(0))
	want := banner{Title: "Ana", Body: "body of Ana", Bundle: "com.microsoft.teams2", Color: "#ff9f0a", Width: 360, Height: 84, Position: "top-right", Slot: 0, Duration: 8, Sound: "Glass"}
	if got != want {
		t.Errorf("banner = %+v, want %+v", got, want)
	}
}

func TestShowReturnsWithoutWaitingForTheBannerToEnd(t *testing.T) {
	r := newOverlayRig(t)
	done := make(chan struct{})
	go func() { r.show(request("Ana", "top-right")); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Show blocked until the banner went away")
	}
}

func TestSimultaneousBannersGetDifferentSlotsSoTheyStack(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("first", "top-right"))
	r.show(request("second", "top-right"))
	r.show(request("third", "top-right"))
	r.show(request("elsewhere", "bottom-left"))
	slots := []int{decode(t, r.process(0)).Slot, decode(t, r.process(1)).Slot, decode(t, r.process(2)).Slot, decode(t, r.process(3)).Slot}
	if slots[0] != 0 || slots[1] != 1 || slots[2] != 2 {
		t.Errorf("slots at the same position = %v, want 0, 1, 2", slots[:3])
	}
	if slots[3] != 0 {
		t.Errorf("a banner at another position got slot %d, want its own slot 0", slots[3])
	}
}

func TestASlotIsReusedOnceItsBannerIsGone(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("first", "top-right"))
	r.show(request("second", "top-right"))
	r.process(0).finish <- "expired"
	time.Sleep(30 * time.Millisecond) // the release happens on the banner's own goroutine
	r.show(request("third", "top-right"))
	if slot := decode(t, r.process(2)).Slot; slot != 0 {
		t.Errorf("third banner slot = %d, want the freed slot 0", slot)
	}
}

func TestTheSourceAppTravelsWithTheBannerSoTheScriptCanOpenItOnAClick(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("Ana", "top-right"))
	if got := decode(t, r.process(0)).Bundle; got != "com.microsoft.teams2" {
		t.Errorf("bundle = %q, want the source app's id", got)
	}
	noApp := request("Sync", "top-right")
	noApp.BundleID = ""
	r.show(noApp)
	if got := decode(t, r.process(1)).Bundle; got != "" {
		t.Errorf("a banner with no app carries bundle %q", got)
	}
}

func TestTheScriptOpensTheAppItselfAfterAClick(t *testing.T) {
	cmd := alert.OverlayCommand(request("Ana", "top-right"))
	script := strings.Join(cmd.Args, " ")
	if !strings.Contains(script, "clicked") || !strings.Contains(script, "/usr/bin/open") || !strings.Contains(script, "cfg.bundle") {
		t.Error("the banner script must open cfg.bundle itself on a click: the process that showed it may already be gone")
	}
}

func TestAnEndedBannerIsNotAFailure(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("Ana", "top-right"))
	r.show(request("Bo", "top-right"))
	r.process(0).finish <- "clicked"
	r.process(1).finish <- "expired"
	time.Sleep(50 * time.Millisecond)
	if len(r.failures) != 0 {
		t.Errorf("normal endings were reported as failures: %v", r.failures)
	}
}

func TestAHelperThatCannotStartIsReportedAsUnavailable(t *testing.T) {
	r := newOverlayRig(t)
	r.startErr = errors.New("exec: osascript: not found")
	err := r.overlay.Show(request("Ana", "top-right"))
	if !errors.Is(err, alert.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable so the fallback is used", err)
	}
	r.startErr = nil
	r.show(request("next", "top-right"))
	if slot := decode(t, r.process(0)).Slot; slot != 0 {
		t.Errorf("the failed start kept slot 0 reserved: next banner got slot %d", slot)
	}
}

func TestAHelperThatCrashesAfterStartingHandsTheAlertToTheFailureHook(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("Ana", "top-right"))
	r.process(0).finish <- "crash"
	r.eventually("the failure hook", func() bool { return len(r.failures) == 1 })
	if r.failedFor[0].Title != "Ana" {
		t.Errorf("the hook got %+v, want the alert that was lost, so it can be shown another way", r.failedFor[0])
	}
}

func TestTheDefaultHelperIsOsascriptRunningTheEmbeddedScript(t *testing.T) {
	cmd := alert.OverlayCommand(request("Ana", "top-right"))
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "osascript") || !strings.Contains(joined, "JavaScript") || !strings.Contains(joined, "NSWindow") {
		t.Errorf("command %q does not look like the AppKit overlay script", joined)
	}
	found := false
	for _, kv := range cmd.Env {
		found = found || strings.HasPrefix(kv, "SHH_BANNER={")
	}
	if !found {
		t.Error("the banner description is not in the environment")
	}
}
