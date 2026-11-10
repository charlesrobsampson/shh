package alert_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
)

// fakeProcess stands in for one osascript banner process: it runs until finish is called.
type fakeProcess struct {
	env     []string
	finish  chan string
	onEvent func(string)
	mu      sync.Mutex
	sent    []layout
}

type layout struct{ Indent, Offset, Height int }

func (p *fakeProcess) Send(message []byte) error {
	var l layout
	if err := json.Unmarshal(message, &l); err != nil {
		panic("the overlay sent a message that is not JSON: " + string(message))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, l)
	return nil
}

func (p *fakeProcess) Wait() (string, error) {
	outcome := <-p.finish
	if outcome == "crash" {
		return "", errors.New("osascript: exit status 1")
	}
	return outcome, nil
}

// messages are what the overlay has told this banner so far, oldest first.
func (p *fakeProcess) messages() []layout {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]layout(nil), p.sent...)
}

// emit is the banner reporting an event, as the script does when Preview or Collapse is pressed.
func (p *fakeProcess) emit(line string) { p.onEvent(line) }

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
		Start: func(env []string, onEvent func(string)) (alert.Process, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.startErr != nil {
				return nil, r.startErr
			}
			p := &fakeProcess{env: env, finish: make(chan string, 1), onEvent: onEvent}
			r.started = append(r.started, p)
			return p, nil
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
	Indent   int    `json:"indent"`
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
		Style: config.Style{Position: position, Size: "medium", Color: 208, DurationSecs: 8, Sound: "Glass"}, Width: 360, Height: 64,
	}
}

func TestTheBannerIsDescribedToTheHelperAsJSON(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("Ana", "top-right"))
	got := decode(t, r.process(0))
	want := banner{Title: "Ana", Body: "body of Ana", Bundle: "com.microsoft.teams2", Color: "#ff8700", Width: 360, Height: 64, Position: "top-right", Offset: 0, Duration: 8, Sound: "Glass"}
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

// settle waits for the goroutines that watch banners end, then returns.
func settle() { time.Sleep(40 * time.Millisecond) }

func lastMessage(t *testing.T, p *fakeProcess) layout {
	t.Helper()
	m := p.messages()
	if len(m) == 0 {
		t.Fatal("the banner was told nothing")
	}
	return m[len(m)-1]
}

func TestDismissingABannerMovesTheOnesBelowItUp(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("first", "top-right"))  // 0
	r.show(request("second", "top-right")) // 74
	r.show(request("third", "top-right"))  // 148
	r.process(1).finish <- "dismissed"
	settle()
	if got := lastMessage(t, r.process(2)); got != (layout{Offset: 74, Height: 64}) {
		t.Errorf("third was told %+v, want to close the gap at offset 74", got)
	}
	if n := len(r.process(0).messages()); n != 0 {
		t.Errorf("the first banner did not move but was sent %d messages", n)
	}
}

func TestANewBannerGoesBelowWhatIsStillThereNotWhereAGapUsedToBe(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("first", "top-right"))
	r.show(request("second", "top-right"))
	r.process(0).finish <- "dismissed"
	settle()
	r.show(request("next", "top-right"))
	if got := decode(t, r.process(2)).Offset; got != 74 {
		t.Errorf("new banner offset = %d, want 74: below the second, which moved up to 0", got)
	}
	if got := lastMessage(t, r.process(1)); got != (layout{Offset: 0, Height: 64}) {
		t.Errorf("second was told %+v, want the top", got)
	}
}

func TestPreviewMakesRoomAndCollapseGivesItBack(t *testing.T) {
	r := newOverlayRig(t)
	req := request("middle", "top-right")
	req.PreviewHeight = 200
	r.show(request("top", "top-right"))
	r.show(req)
	r.show(request("bottom", "top-right")) // at 74+64+10 = 148
	r.process(1).emit("height 200")
	if got := lastMessage(t, r.process(2)); got != (layout{Offset: 74 + 200 + 10, Height: 64}) {
		t.Errorf("bottom was told %+v, want to move down below the expanded banner", got)
	}
	if got := lastMessage(t, r.process(1)); got != (layout{Offset: 74, Height: 200}) {
		t.Errorf("the expanded banner was told %+v", got)
	}
	r.process(1).emit("height 64")
	if got := lastMessage(t, r.process(2)); got != (layout{Offset: 148, Height: 64}) {
		t.Errorf("bottom was told %+v after the collapse, want its old place", got)
	}
	if n := len(r.process(0).messages()); n != 0 {
		t.Errorf("the banner above the preview was sent %d messages; it should never move", n)
	}
}

func TestAHeightReportIsKeptBetweenTheCollapsedAndPreviewHeights(t *testing.T) {
	r := newOverlayRig(t)
	req := request("a", "top-right")
	req.PreviewHeight = 200
	r.show(req)
	r.show(request("b", "top-right"))
	r.process(0).emit("height 100000")
	if got := lastMessage(t, r.process(1)); got.Offset != 200+10 {
		t.Errorf("b offset = %d, want 210: a banner cannot claim more than its preview height", got.Offset)
	}
	r.process(0).emit("height 1")
	if got := lastMessage(t, r.process(1)); got.Offset != 64+10 {
		t.Errorf("b offset = %d, want 74: a banner cannot shrink below its collapsed height", got.Offset)
	}
	r.process(0).emit("height soon")
	r.process(0).emit("nonsense")
}

func TestBannersInOtherPositionsAreLeftAlone(t *testing.T) {
	r := newOverlayRig(t)
	req := request("a", "top-right")
	req.PreviewHeight = 200
	r.show(req)
	r.show(request("other", "bottom-left"))
	r.process(0).emit("height 200")
	if n := len(r.process(1).messages()); n != 0 {
		t.Errorf("a banner elsewhere was sent %d messages", n)
	}
}

func TestAnEventFromABannerThatIsGoneIsIgnored(t *testing.T) {
	r := newOverlayRig(t)
	req := request("a", "top-right")
	req.PreviewHeight = 200
	r.show(req)
	r.show(request("b", "top-right"))
	r.process(0).finish <- "dismissed"
	settle()
	before := len(r.process(1).messages())
	r.process(0).emit("height 200")
	if after := len(r.process(1).messages()); after != before {
		t.Errorf("a late event from a dismissed banner moved another one (%d messages before, %d after)", before, after)
	}
}

func TestBannersAreOnlyToldAboutRealChanges(t *testing.T) {
	r := newOverlayRig(t)
	req := request("a", "top-right")
	req.PreviewHeight = 200
	r.show(req)
	r.show(request("b", "top-right"))
	r.process(0).emit("height 64") // no change
	if n := len(r.process(1).messages()); n != 0 {
		t.Errorf("b was sent %d messages for a report that changed nothing", n)
	}
}

func TestEverythingIsFreeAgainOnceAllBannersAreGone(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("first", "top-right"))
	r.show(request("second", "top-right"))
	r.process(0).finish <- "expired"
	r.process(1).finish <- "opened"
	settle()
	r.show(request("next", "top-right"))
	if got := decode(t, r.process(2)).Offset; got != 0 {
		t.Errorf("offset = %d, want the screen edge again", got)
	}
}

func TestTheBannerKnowsItIsManaged(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("a", "top-right"))
	for _, kv := range r.process(0).env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok && strings.Contains(v, `"managed":true`) {
			return
		}
	}
	t.Error("the banner is not told that Go is controlling it, so it would not listen on stdin")
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
	if !strings.Contains(script, "opened") || !strings.Contains(script, "/usr/bin/open") || !strings.Contains(script, "cfg.bundle") {
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

func TestTheBannerTextDoesNotSwallowClicks(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	if !strings.Contains(script, "setSelectable(false)") {
		t.Error("the text labels must not be selectable: a selectable text field keeps the click for itself and the banner never sees it")
	}
}

func TestTheScriptWrapsLongText(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	if !strings.Contains(script, "wrappingLabelWithString") {
		t.Error("the banner text must use wrapping labels, or long bodies are clipped to one line")
	}
	if !strings.Contains(script, "setTruncatesLastVisibleLine") || !strings.Contains(script, "NSLineBreakByWordWrapping") {
		t.Error("multi-line text must use word wrapping with truncatesLastVisibleLine: a truncating line-break mode makes AppKit lay the text out on one line")
	}
}

func TestAnEndedBannerIsNotAFailure(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("Ana", "top-right"))
	r.show(request("Bo", "top-right"))
	r.process(0).finish <- "opened"
	r.process(1).finish <- "expired"
	settle()
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

func TestTheButtonsTravelWithTheBannerInOrder(t *testing.T) {
	r := newOverlayRig(t)
	req := request("Ana", "top-right")
	req.Buttons = []config.Button{{Type: "dismiss", Label: "Close", Enabled: true}, {Type: "goto", Label: "Open", Enabled: true}}
	req.DismissOnClick = true
	req.PreviewHeight = 200
	r.show(req)
	var spec struct {
		Buttons        []struct{ Type, Label string } `json:"buttons"`
		DismissOnClick bool                           `json:"dismissOnClick"`
		PreviewHeight  int                            `json:"previewHeight"`
	}
	for _, kv := range r.process(0).env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
			if err := json.Unmarshal([]byte(v), &spec); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(spec.Buttons) != 2 || spec.Buttons[0].Type != "dismiss" || spec.Buttons[0].Label != "Close" || spec.Buttons[1].Type != "goto" {
		t.Errorf("buttons = %+v", spec.Buttons)
	}
	if !spec.DismissOnClick || spec.PreviewHeight != 200 {
		t.Errorf("dismissOnClick = %v, previewHeight = %d", spec.DismissOnClick, spec.PreviewHeight)
	}
}

func TestTheScriptHandlesEveryButtonTypeAndTheBackgroundRule(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	for _, want := range []string{"'preview'", "'goto'", "'dismiss'", "'opened'", "'dismissed'", "cfg.dismissOnClick", "cfg.previewHeight", "invalidate"} {
		if !strings.Contains(script, want) {
			t.Errorf("the banner script does not mention %s", want)
		}
	}
	if !strings.Contains(script, "ShhBannerButton") || strings.Count(script, "acceptsFirstMouse") < 2 {
		t.Error("buttons must accept the first mouse click too: the banner never becomes the active window, so a button that needs a second click looks broken")
	}
}

func TestTheScriptListensForLayoutAndStaysSilentOnceGoIsGone(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	for _, want := range []string{"waitForDataInBackgroundAndNotify", "layoutTo", "emit('height '", "collapseLabel"} {
		if !strings.Contains(script, want) {
			t.Errorf("the banner script does not mention %s", want)
		}
	}
	if !strings.Contains(script, "getppid") || !strings.Contains(script, "attached()") {
		t.Error("the script must stop writing once its parent is gone: a write to the closed pipe kills it (SIGPIPE)")
	}
}

func TestActiveCountsBannersOnScreen(t *testing.T) {
	r := newOverlayRig(t)
	if r.overlay.Active() != 0 {
		t.Fatal("a new overlay has banners")
	}
	r.show(request("a", "top-right"))
	r.show(request("b", "bottom-left"))
	if got := r.overlay.Active(); got != 2 {
		t.Errorf("Active = %d, want 2", got)
	}
	r.process(0).finish <- "dismissed"
	settle()
	if got := r.overlay.Active(); got != 1 {
		t.Errorf("Active = %d after one was dismissed, want 1", got)
	}
}

func TestTheBannerIsToldWhichProcessStartedIt(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("a", "top-right"))
	var spec struct {
		Parent int `json:"parent"`
	}
	for _, kv := range r.process(0).env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
			if err := json.Unmarshal([]byte(v), &spec); err != nil {
				t.Fatal(err)
			}
		}
	}
	if spec.Parent != os.Getpid() {
		t.Errorf("parent = %d, want this process (%d): a banner cannot work it out for itself, because Go may be gone before the script starts", spec.Parent, os.Getpid())
	}
	script := strings.Join(alert.OverlayCommand(request("a", "top-right")).Args, " ")
	if !strings.Contains(script, "cfg.parent") || strings.Contains(script, "const parent = $.getppid()") {
		t.Error("the script must compare its parent with cfg.parent, not with whatever its parent is when it starts")
	}
}

func TestAFullColumnContinuesInANewOneNextToIt(t *testing.T) {
	r := newOverlayRig(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		r.show(request(name, "top-right")) // each 64 high with a 10 point gap: 0, 74, 148, 222
	}
	r.process(0).emit("screen 220") // room for three: 148+64 = 212 fits, 222+64 does not
	if got := lastMessage(t, r.process(3)); got != (layout{Indent: 360 + 10, Offset: 0, Height: 64}) {
		t.Errorf("d was told %+v, want the top of a second column, one banner width and a gap in from the edge", got)
	}
	if n := len(r.process(2).messages()); n != 0 {
		t.Errorf("c still fits in the first column but was sent %d messages", n)
	}
}

func TestAColumnIsAsWideAsItsWidestBanner(t *testing.T) {
	r := newOverlayRig(t)
	wide := request("wide", "top-right")
	wide.Width = 460
	r.show(wide)
	r.show(request("b", "top-right"))
	r.show(request("c", "top-right"))
	r.process(0).emit("screen 140") // two fit (74+64 = 138), the third does not
	if got := lastMessage(t, r.process(2)); got.Indent != 460+10 || got.Offset != 0 {
		t.Errorf("c was told %+v, want a second column 470 in, past the 460 wide banner", got)
	}
}

func TestDismissingBringsBannersBackIntoTheFirstColumn(t *testing.T) {
	r := newOverlayRig(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		r.show(request(name, "top-right"))
	}
	r.process(0).emit("screen 220")
	r.process(0).finish <- "dismissed"
	settle()
	if got := lastMessage(t, r.process(3)); got != (layout{Indent: 0, Offset: 148, Height: 64}) {
		t.Errorf("d was told %+v, want to be back in the first column now that there is room", got)
	}
}

func TestPreviewCanPushABannerIntoANewColumn(t *testing.T) {
	r := newOverlayRig(t)
	a := request("a", "top-right")
	a.PreviewHeight = 200
	r.show(a)
	r.show(request("b", "top-right"))
	r.process(0).emit("screen 200")
	r.process(0).emit("height 200") // 200 + 10 + 64 does not fit
	if got := lastMessage(t, r.process(1)); got.Indent != 370 || got.Offset != 0 {
		t.Errorf("b was told %+v, want a new column when the preview takes the room", got)
	}
}

func TestNewBannersStartInTheRightColumnOnceTheScreenIsKnown(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("a", "top-right"))
	r.show(request("b", "top-right"))
	r.process(0).emit("screen 140")
	r.show(request("c", "top-right"))
	if got := decode(t, r.process(2)); got.Indent != 370 || got.Offset != 0 {
		t.Errorf("c started at indent %d, offset %d, want the top of the second column at once", got.Indent, got.Offset)
	}
}

func TestWithoutAScreenHeightEverythingStaysInOneColumn(t *testing.T) {
	r := newOverlayRig(t)
	for i := 0; i < 30; i++ {
		r.show(request("x", "top-right"))
	}
	if got := decode(t, r.process(29)); got.Indent != 0 {
		t.Errorf("indent = %d before any screen height was reported", got.Indent)
	}
	r.process(0).emit("screen 0")
	r.process(0).emit("screen -5")
	r.process(0).emit("screen lots")
	for i := 0; i < 30; i++ {
		if n := len(r.process(i).messages()); n != 0 {
			t.Fatalf("banner %d was sent %d messages for screen heights that mean nothing", i, n)
		}
	}
}

func TestTheScriptReportsTheScreenAndHonoursTheIndent(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	for _, want := range []string{"emit('screen '", "m.indent", "cfg.indent"} {
		if !strings.Contains(script, want) {
			t.Errorf("the banner script does not mention %s", want)
		}
	}
}

func TestTheAppNameTravelsWithTheBannerAsItsHeader(t *testing.T) {
	r := newOverlayRig(t)
	req := request("Ana", "top-right")
	req.Source = " Teams "
	r.show(req)
	r.show(request("Bo", "top-right")) // no source
	header := func(i int) string {
		var spec struct {
			Header string `json:"header"`
		}
		for _, kv := range r.process(i).env {
			if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
				if err := json.Unmarshal([]byte(v), &spec); err != nil {
					t.Fatal(err)
				}
			}
		}
		return spec.Header
	}
	if got := header(0); got != "Teams" {
		t.Errorf("header = %q, want the trimmed source", got)
	}
	if got := header(1); got != "" {
		t.Errorf("header = %q for an alert with no source", got)
	}
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	if !strings.Contains(script, "cfg.header") || !strings.Contains(script, "headerRow") {
		t.Error("the banner script does not draw the header")
	}
}

func flagReq(title string, notificationID int64) alert.Request {
	r := request(title, "top-right")
	r.NotificationID = notificationID
	r.Buttons = []config.Button{{Type: "flag", Label: "Flag", Enabled: true, FlaggedLabel: "Noted"}}
	return r
}

type flagCall struct {
	id      int64
	flagged bool
}

// flagRig is an overlay whose OnFlag calls are recorded.
func flagRig(t *testing.T) (*overlayRig, func() []flagCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []flagCall
	r := &overlayRig{t: t}
	r.overlay = alert.NewOverlay(alert.OverlayConfig{
		Start: func(env []string, onEvent func(string)) (alert.Process, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			p := &fakeProcess{env: env, finish: make(chan string, 1), onEvent: onEvent}
			r.started = append(r.started, p)
			return p, nil
		},
		OnFlag: func(id int64, flagged bool) { mu.Lock(); calls = append(calls, flagCall{id, flagged}); mu.Unlock() },
	})
	return r, func() []flagCall { mu.Lock(); defer mu.Unlock(); return append([]flagCall(nil), calls...) }
}

func TestTheFlagButtonToggleReachesTheHookWithTheNewState(t *testing.T) {
	r, calls := flagRig(t)
	r.show(flagReq("a", 41))
	r.show(flagReq("b", 42))
	r.process(0).emit("flag 1")
	r.process(1).emit("flag 1")
	r.process(0).emit("flag 0") // pressed again: cleared
	r.process(0).emit("flag 1") // and flagged once more
	want := []flagCall{{41, true}, {42, true}, {41, false}, {41, true}}
	if got := calls(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("OnFlag calls = %v, want %v", got, want)
	}
}

func TestRepeatedStatesAreNotPassedOnTwice(t *testing.T) {
	r, calls := flagRig(t)
	r.show(flagReq("a", 41))
	r.process(0).emit("flag 1")
	r.process(0).emit("flag 1")
	r.process(0).emit("flag 0")
	r.process(0).emit("flag 0")
	r.process(0).emit("flag") // no state: treated as off, which changes nothing now
	if got := calls(); fmt.Sprint(got) != fmt.Sprint([]flagCall{{41, true}, {41, false}}) {
		t.Errorf("OnFlag calls = %v, want one call per real change", got)
	}
}

func TestAFlagEventFromABannerWithNoNotificationOrThatHasGoneIsIgnored(t *testing.T) {
	r, calls := flagRig(t)
	r.show(flagReq("no notification", 0))
	r.show(flagReq("goes away", 5))
	r.process(0).emit("flag 1")
	r.process(1).finish <- "dismissed"
	settle()
	r.process(1).emit("flag 1")
	if got := calls(); len(got) != 0 {
		t.Errorf("OnFlag called %v for a banner with nothing to mark or one that has gone", got)
	}
}

func TestAFlagEventWithNoHookIsHarmless(t *testing.T) {
	r := newOverlayRig(t)
	r.show(flagReq("a", 1))
	r.process(0).emit("flag 1")
	r.process(0).emit("flag 0")
}

func TestTheFlaggedLabelTravelsWithTheBannerAndTheScriptHandlesTheButton(t *testing.T) {
	r := newOverlayRig(t)
	r.show(flagReq("a", 3))
	var spec struct {
		Buttons []struct {
			Type         string `json:"type"`
			FlaggedLabel string `json:"flaggedLabel"`
		} `json:"buttons"`
	}
	for _, kv := range r.process(0).env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
			if err := json.Unmarshal([]byte(v), &spec); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(spec.Buttons) != 1 || spec.Buttons[0].Type != "flag" || spec.Buttons[0].FlaggedLabel != "Noted" {
		t.Errorf("buttons = %+v", spec.Buttons)
	}
	script := strings.Join(alert.OverlayCommand(request("a", "top-right")).Args, " ")
	for _, want := range []string{"'flag'", "spec.flaggedLabel", "emit('flag '", "flagged = !flagged"} {
		if !strings.Contains(script, want) {
			t.Errorf("the banner script does not mention %s", want)
		}
	}
}

func TestAnActionButtonReportsItsTokenAndIDWhenPressed(t *testing.T) {
	var mu sync.Mutex
	var got []string
	r := &overlayRig{t: t}
	r.overlay = alert.NewOverlay(alert.OverlayConfig{
		Start: func(env []string, onEvent func(string)) (alert.Process, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			p := &fakeProcess{env: env, finish: make(chan string, 1), onEvent: onEvent}
			r.started = append(r.started, p)
			return p, nil
		},
		OnAction: func(token, id string) { mu.Lock(); got = append(got, token+"/"+id); mu.Unlock() },
	})
	req := request("work is over", "top-right")
	req.ActionToken = "7:3"
	req.Buttons = []config.Button{{Type: alert.ButtonAction, Label: "Start next", ID: "next", Enabled: true}}
	r.show(req)
	r.process(0).emit("action next")
	r.process(0).finish <- "dismissed"
	settle()
	r.process(0).emit("action next") // from a banner that has gone: ignored
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(got) != "[7:3/next]" {
		t.Errorf("OnAction calls = %v", got)
	}
	var spec struct {
		Buttons []struct{ Type, Label, ID string } `json:"buttons"`
	}
	for _, kv := range r.process(0).env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
			_ = json.Unmarshal([]byte(v), &spec)
		}
	}
	if len(spec.Buttons) != 1 || spec.Buttons[0].Type != "action" || spec.Buttons[0].ID != "next" || spec.Buttons[0].Label != "Start next" {
		t.Errorf("buttons = %+v", spec.Buttons)
	}
	if script := strings.Join(alert.OverlayCommand(req).Args, " "); !strings.Contains(script, "'action'") || !strings.Contains(script, "emit('action '") {
		t.Error("the script must handle action buttons")
	}
}

func TestAnActionEventWithNoHookIsHarmless(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("a", "top-right"))
	r.process(0).emit("action next")
}

func TestTheTextColorTravelsWithTheBannerAndTheScriptDrawsWithIt(t *testing.T) {
	r := newOverlayRig(t)
	req := request("Ana", "top-right")
	req.Style.TextColor = 0
	r.show(req)
	var spec struct {
		Color     string `json:"color"`
		TextColor string `json:"textColor"`
	}
	for _, kv := range r.process(0).env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
			if err := json.Unmarshal([]byte(v), &spec); err != nil {
				t.Fatal(err)
			}
		}
	}
	if spec.Color != "#ff8700" || spec.TextColor != "#000000" {
		t.Errorf("colors = %+v: the background and the text are separate", spec)
	}
	script := strings.Join(alert.OverlayCommand(req).Args, " ")
	if !strings.Contains(script, "cfg.textColor") || strings.Contains(script, "NSColor.whiteColor") || !strings.Contains(script, "colorWithAlphaComponent(0.75)") {
		t.Error("the text, and the fainter app name above it, must be drawn in the configured text color, not always white")
	}
}

func TestButtonColorsTravelWithTheBannerOnlyWhenSetAndTheScriptPaintsWithThem(t *testing.T) {
	r := newOverlayRig(t)
	plain := request("Ana", "top-right")
	plain.Style.ButtonColor, plain.Style.ButtonTextColor = -1, -1
	styled := request("Bo", "top-right")
	styled.Style.ButtonColor, styled.Style.ButtonTextColor = 238, 15
	r.show(plain)
	r.show(styled)
	read := func(i int) map[string]any {
		for _, kv := range r.process(i).env {
			if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
				var m map[string]any
				if err := json.Unmarshal([]byte(v), &m); err != nil {
					t.Fatal(err)
				}
				return m
			}
		}
		return nil
	}
	if m := read(0); m["buttonColor"] != nil || m["buttonTextColor"] != nil {
		t.Errorf("the system look sends no colors: %v", m)
	}
	if m := read(1); m["buttonColor"] != "#444444" || m["buttonTextColor"] != "#ffffff" {
		t.Errorf("the styled buttons: %v", m)
	}
	script := strings.Join(alert.OverlayCommand(styled).Args, " ")
	for _, want := range []string{"cfg.buttonColor", "cfg.buttonTextColor", "setButtonTitle", "inks", "ink.setTextColor(buttonInk)", "sizeToFit", "NSBox", "backdrops", "setFillColor(buttonFill)"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script does not mention %s", want)
		}
	}
	if strings.Contains(script, "setAttributedTitle") || strings.Contains(script, "NSAttributedString.alloc") || strings.Contains(script, "setContentTintColor") {
		t.Error("an attributed title cannot be built from JXA and a content tint did nothing in the banner window (both left the button text uncolored): the text is a label over the button's face")
	}
	if strings.Contains(script, ".layer") || strings.Contains(script, "CGColor") {
		t.Error("a CGColor handed to a layer from JXA can take osascript down with it (and then every banner falls back to a system notification): the buttons are painted with a box instead")
	}
	if strings.Contains(script, "button.setTitle($(flagged") || strings.Contains(script, "button.setTitle($(expanded") {
		t.Error("a button that changes its label must set it through setButtonTitle, or the new label loses the button text color")
	}
}

func TestAButtonsLabelIsCenteredByHandNotWithTheAlignmentConstant(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	if strings.Contains(script, "setAlignment") || strings.Contains(script, "NSTextAlignmentCenter") {
		t.Error("NSTextAlignmentCenter came out as right-aligned text in the banner (the label sat against the right edge of its button): center the label from its size instead")
	}
	if !strings.Contains(script, "(face.size.width - size.width) / 2") || !strings.Contains(script, "(face.size.height - size.height) / 2") {
		t.Error("the label is centered both ways from its own size")
	}
}

// ---- the train button ----

func trainReq(title string, notificationID int64) alert.Request {
	r := request(title, "top-right")
	r.NotificationID = notificationID
	r.Buttons = []config.Button{{Type: "train", Label: "Not important", Enabled: true, TrainedLabel: "Noted"}}
	return r
}

func trainRig(t *testing.T) (*overlayRig, func() []int64) {
	t.Helper()
	var mu sync.Mutex
	var calls []int64
	r := &overlayRig{t: t}
	r.overlay = alert.NewOverlay(alert.OverlayConfig{
		Start: func(env []string, onEvent func(string)) (alert.Process, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			p := &fakeProcess{env: env, finish: make(chan string, 1), onEvent: onEvent}
			r.started = append(r.started, p)
			return p, nil
		},
		OnTrain: func(id int64) { mu.Lock(); calls = append(calls, id); mu.Unlock() },
	})
	return r, func() []int64 { mu.Lock(); defer mu.Unlock(); return append([]int64(nil), calls...) }
}

func TestTheTrainButtonReachesTheHookOncePerBanner(t *testing.T) {
	r, calls := trainRig(t)
	r.show(trainReq("a", 41))
	r.show(trainReq("b", 42))
	r.process(0).emit("train 1")
	r.process(0).emit("train 1") // pressed again: the button is disabled, but a stray event must not teach twice
	r.process(1).emit("train 1")
	if got := calls(); fmt.Sprint(got) != "[41 42]" {
		t.Errorf("OnTrain calls = %v, want one per banner", got)
	}
}

func TestATrainEventFromABannerWithNoNotificationOrThatHasGoneIsIgnored(t *testing.T) {
	r, calls := trainRig(t)
	r.show(trainReq("no notification", 0))
	r.show(trainReq("goes away", 5))
	r.process(0).emit("train 1")
	r.process(1).finish <- "dismissed"
	settle()
	r.process(1).emit("train 1")
	if got := calls(); len(got) != 0 {
		t.Errorf("OnTrain called %v", got)
	}
	plain := newOverlayRig(t)
	plain.show(trainReq("x", 1))
	plain.process(0).emit("train 1") // no hook: harmless
}

func TestTheTrainButtonTravelsWithItsTrainedLabelAndTheScriptHandlesIt(t *testing.T) {
	r := newOverlayRig(t)
	r.show(trainReq("a", 3))
	var spec struct {
		Buttons []struct {
			Type         string `json:"type"`
			Label        string `json:"label"`
			TrainedLabel string `json:"trainedLabel"`
		} `json:"buttons"`
	}
	for _, kv := range r.process(0).env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
			if err := json.Unmarshal([]byte(v), &spec); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(spec.Buttons) != 1 || spec.Buttons[0].Type != "train" || spec.Buttons[0].Label != "Not important" || spec.Buttons[0].TrainedLabel != "Noted" {
		t.Errorf("buttons = %+v", spec.Buttons)
	}
	script := strings.Join(alert.OverlayCommand(trainReq("a", 3)).Args, " ")
	for _, want := range []string{"'train'", "emit('train 1')", "trainedLabel", "setEnabled(false)", "trainedOnce"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script does not mention %s", want)
		}
	}
}
