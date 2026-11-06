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
	Offset   int    `json:"offset"`
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
		Style: config.Style{Position: position, Size: "medium", Color: "#ff9f0a", DurationSecs: 8, Sound: "Glass"}, Width: 360, Height: 64,
	}
}

func TestTheBannerIsDescribedToTheHelperAsJSON(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("Ana", "top-right"))
	got := decode(t, r.process(0))
	want := banner{Title: "Ana", Body: "body of Ana", Bundle: "com.microsoft.teams2", Color: "#ff9f0a", Width: 360, Height: 64, Position: "top-right", Offset: 0, Duration: 8, Sound: "Glass"}
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

func tall(title, position string, height int) alert.Request {
	r := request(title, position)
	r.Height = height
	return r
}

func offsets(t *testing.T, r *overlayRig, indexes ...int) []int {
	t.Helper()
	var out []int
	for _, i := range indexes {
		out = append(out, decode(t, r.process(i)).Offset)
	}
	return out
}

func TestSimultaneousBannersStackWithoutOverlapping(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("first", "top-right"))   // 64 high
	r.show(tall("second", "top-right", 96)) // below the first, with a 10 point gap
	r.show(request("third", "top-right"))   // below the second
	r.show(request("elsewhere", "bottom-left"))
	got := offsets(t, r, 0, 1, 2, 3)
	if want := []int{0, 74, 180, 0}; !equalInts(got, want) {
		t.Fatalf("offsets = %v, want %v (heights 64, 96, 64 and a 10 point gap; another position starts again at 0)", got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAFreedSpaceIsReusedWhenTheNewBannerFitsInIt(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("first", "top-right"))  // 0..64
	r.show(request("second", "top-right")) // 74..138
	r.show(request("third", "top-right"))  // 148..212
	r.process(0).finish <- "expired"
	time.Sleep(30 * time.Millisecond) // the release happens on the banner's own goroutine

	r.show(request("fits the gap", "top-right"))
	r.show(tall("too tall for a gap", "top-right", 112))
	got := offsets(t, r, 3, 4)
	if want := []int{0, 222}; !equalInts(got, want) {
		t.Fatalf("offsets = %v, want %v: the 64 point banner takes the freed top space, the 112 point one goes below everything", got, want)
	}
}

func TestEverythingIsFreeAgainOnceAllBannersAreGone(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("first", "top-right"))
	r.show(request("second", "top-right"))
	r.process(0).finish <- "expired"
	r.process(1).finish <- "clicked"
	time.Sleep(50 * time.Millisecond)
	r.show(request("next", "top-right"))
	if got := decode(t, r.process(2)).Offset; got != 0 {
		t.Errorf("offset = %d, want the screen edge again", got)
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

func TestTheScriptOpensTheAppBeforeItLogsTheOutcome(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	open, logOutcome := strings.Index(script, "/usr/bin/open"), strings.Index(script, "console.log(outcome)")
	if open < 0 || logOutcome < 0 || open > logOutcome {
		t.Error("the app must be opened before the outcome is logged: when the starting process has exited, writing to its closed pipe kills the script (SIGPIPE) and the click would do nothing")
	}
}

func TestTheScriptWrapsLongText(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	if !strings.Contains(script, "wrappingLabelWithString") {
		t.Error("the banner text must use wrapping labels, or long bodies are clipped to one line")
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
	if offset := decode(t, r.process(0)).Offset; offset != 0 {
		t.Errorf("the failed start kept its space reserved: the next banner got offset %d", offset)
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
