package alert_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

// messageTo is what a banner was told besides where to be: the last message that has the field, decoded.
func (p *fakeProcess) told() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, m := range p.raw {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(m), &fields)
		if _, isLayout := fields["height"]; !isLayout {
			out = append(out, m)
		}
	}
	return out
}

// selectedNow is whether the banner has been told it is selected, last word winning: nil when it was never told.
func (p *fakeProcess) selectedNow() *bool {
	var state *bool
	for _, m := range p.told() {
		var msg struct {
			Selected *bool `json:"selected"`
		}
		if json.Unmarshal([]byte(m), &msg) == nil && msg.Selected != nil {
			state = msg.Selected
		}
	}
	return state
}

func (p *fakeProcess) pressedButtons() []int {
	var out []int
	for _, m := range p.told() {
		var msg struct {
			Press *int `json:"press"`
		}
		if json.Unmarshal([]byte(m), &msg) == nil && msg.Press != nil {
			out = append(out, *msg.Press)
		}
	}
	return out
}

func (p *fakeProcess) wasToldToDismiss() bool {
	for _, m := range p.told() {
		var msg struct {
			Dismiss bool `json:"dismiss"`
		}
		if json.Unmarshal([]byte(m), &msg) == nil && msg.Dismiss {
			return true
		}
	}
	return false
}

type keysRig struct {
	*overlayRig
	keys      *testkit.FakeHotkeys
	shortcuts func() config.Shortcuts
	mu        sync.Mutex
	errs      []error
}

func newKeysRig(t *testing.T, shortcuts config.Shortcuts) *keysRig {
	t.Helper()
	k := &keysRig{keys: testkit.NewFakeHotkeys()}
	k.shortcuts = func() config.Shortcuts { return shortcuts }
	k.overlayRig = newOverlayRig(t, func(c *alert.OverlayConfig) {
		c.Hotkeys, c.Shortcuts = k.keys, k.shortcuts
		c.OnKeyError = func(err error) { k.mu.Lock(); k.errs = append(k.errs, err); k.mu.Unlock() }
	})
	return k
}

func defaultShortcuts() config.Shortcuts { return config.Default("/none/config.json").Alert.Shortcuts }

// press is a key press, once the keys are held (they are taken up a moment after the first banner shows).
func (k *keysRig) press(action string) {
	k.t.Helper()
	k.waitHeld(true)
	if !k.keys.Press(action) {
		k.t.Fatalf("%s is not a held key", action)
	}
}

func (k *keysRig) waitHeld(want bool) {
	k.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, held := k.keys.Held(); held == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	k.t.Fatalf("the keys were not held = %v in time", want)
}

// until waits for a condition without holding the rig's lock, which the conditions here need (they look banners up).
func (k *keysRig) until(what string, cond func() bool) {
	k.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	k.t.Fatalf("timed out waiting for %s", what)
}

func buttonReq(title, position string, types ...string) alert.Request {
	r := request(title, position)
	for _, ty := range types {
		r.Buttons = append(r.Buttons, config.Button{Type: ty, Label: ty, Enabled: true})
	}
	return r
}

func TestNothingIsSelectedUntilAKeyPicksABanner(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.show(request("a", "top-right"))
	k.show(request("b", "top-right"))
	k.waitHeld(true)
	for i := 0; i < 2; i++ {
		if state := k.process(i).selectedNow(); state != nil {
			t.Errorf("banner %d has been told %v before any key", i, *state)
		}
	}
}

func TestNextAndPreviousCycleInArrivalOrderAcrossPositionsAndWrap(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.show(request("a", "top-right"))
	k.show(request("b", "bottom-left"))
	k.show(request("c", "top-right"))
	selected := func() string {
		var which []string
		for i, name := range []string{"a", "b", "c"} {
			if s := k.process(i).selectedNow(); s != nil && *s {
				which = append(which, name)
			}
		}
		return strings.Join(which, "")
	}
	for _, want := range []string{"a", "b", "c", "a"} {
		k.press("next")
		if got := selected(); got != want {
			t.Fatalf("after next: selected %q, want %q", got, want)
		}
	}
	k.press("previous")
	if got := selected(); got != "c" {
		t.Errorf("previous from a wraps to the last: selected %q", got)
	}
	k.press("previous")
	if got := selected(); got != "b" {
		t.Errorf("selected %q, want b", got)
	}
}

func TestPreviousWithNothingSelectedStartsAtTheNewest(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.show(request("a", "top-right"))
	k.show(request("b", "top-right"))
	k.press("previous")
	if s := k.process(1).selectedNow(); s == nil || !*s {
		t.Error("the newest banner should be picked")
	}
	if s := k.process(0).selectedNow(); s != nil {
		t.Errorf("the other was told %v", *s)
	}
}

func TestANewBannerNeverTakesTheSelection(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.show(request("a", "top-right"))
	k.press("next")
	k.show(request("b", "top-right"))
	k.show(request("c", "bottom-left"))
	if s := k.process(0).selectedNow(); s == nil || !*s {
		t.Error("a should stay selected")
	}
	for _, i := range []int{1, 2} {
		if s := k.process(i).selectedNow(); s != nil {
			t.Errorf("banner %d was told %v", i, *s)
		}
	}
}

func TestWhenTheSelectedBannerGoesTheOneAfterItIsPicked(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	for _, name := range []string{"a", "b", "c"} {
		k.show(request(name, "top-right"))
	}
	k.press("next")
	k.press("next") // b
	k.process(1).finish <- "dismissed"
	k.until("c to be picked", func() bool { s := k.process(2).selectedNow(); return s != nil && *s })
	k.process(2).finish <- "dismissed" // the last one goes: the one before it is picked
	k.until("a to be picked", func() bool { s := k.process(0).selectedNow(); return s != nil && *s })
	k.process(0).finish <- "dismissed"
	k.until("everything gone", func() bool { return k.overlay.Active() == 0 })
}

func TestClearLetsGoOfTheSelection(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.show(request("a", "top-right"))
	k.show(request("b", "top-right"))
	k.press("next")
	k.press("clear")
	if s := k.process(0).selectedNow(); s == nil || *s {
		t.Errorf("a should have been told it is no longer selected: %v", s)
	}
	k.press("next")
	if s := k.process(0).selectedNow(); s == nil || !*s {
		t.Error("with nothing selected next starts again at the first")
	}
}

func TestAButtonKeyPressesThatButtonOnTheSelectedBannerOnly(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.show(buttonReq("a", "top-right", "preview", "goto", "dismiss", "flag"))
	k.show(buttonReq("b", "top-right", "goto", "dismiss", "flag", "train"))
	k.press("next") // a
	k.press("flag")
	k.press("goto")
	k.press("next") // b
	k.press("train")
	k.press("dismiss")
	if got, want := k.process(0).pressedButtons(), []int{3, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("a was pressed at buttons %v, want %v (flag, then go to: positions in its list)", got, want)
	}
	if got, want := k.process(1).pressedButtons(), []int{3, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("b was pressed at buttons %v, want %v (train, then dismiss)", got, want)
	}
}

func TestAKeyForAButtonTheBannerLacksDoesNothing(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.show(buttonReq("a", "top-right", "dismiss"))
	k.press("preview") // nothing is selected yet either
	k.press("next")
	k.press("preview")
	k.press("train")
	if got := k.process(0).pressedButtons(); len(got) != 0 {
		t.Errorf("buttons pressed: %v", got)
	}
}

func TestDigitsPressTheSelectedBannersOwnActionButtonsInOrder(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	req := buttonReq("step over", "top-right", "dismiss", "goto")
	req.Buttons = append(req.Buttons, config.Button{Type: alert.ButtonAction, Label: "Start next", ID: "start", Enabled: true}, config.Button{Type: alert.ButtonAction, Label: "Skip", ID: "skip", Enabled: true})
	k.show(req)
	k.waitHeld(true)
	k.until("the digits to be held", func() bool { b, _ := k.keys.Held(); return b.Keys["action_2"] == "2" })
	if b, _ := k.keys.Held(); b.Keys["action_3"] != "" {
		t.Errorf("only as many digits as there are action buttons on screen are held: %v", b.Keys)
	}
	k.press("next")
	k.press("action_2")
	k.press("action_1")
	if got, want := k.process(0).pressedButtons(), []int{3, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("pressed %v, want %v (Skip is button 3, Start next button 2)", got, want)
	}
}

func TestDismissAllClosesEveryBannerWithoutASelection(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.show(request("a", "top-right"))
	k.show(request("b", "bottom-left"))
	k.press("dismiss_all")
	for i := 0; i < 2; i++ {
		if !k.process(i).wasToldToDismiss() {
			t.Errorf("banner %d was not told to dismiss", i)
		}
	}
}

func TestTheKeysAreHeldOnlyWhileBannersAreOnScreen(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	if _, held := k.keys.Held(); held || k.keys.Registrations() != 0 {
		t.Fatal("an idle overlay holds keys")
	}
	k.show(request("a", "top-right"))
	k.waitHeld(true)
	held, _ := k.keys.Held()
	want := defaultShortcuts()
	if held.Modifier != want.Modifier || held.Keys["next"] != "]" || held.Keys["dismiss_all"] != "x" {
		t.Errorf("held = %+v", held)
	}
	k.show(request("b", "top-right"))
	time.Sleep(30 * time.Millisecond)
	if n := k.keys.Registrations(); n != 1 {
		t.Errorf("a second banner should not register again: %d registrations", n)
	}
	k.process(0).finish <- "dismissed"
	time.Sleep(30 * time.Millisecond)
	if _, held := k.keys.Held(); !held {
		t.Error("one banner is still up")
	}
	k.process(1).finish <- "expired"
	k.waitHeld(false)
}

func TestShortcutsThatAreSwitchedOffHoldNothing(t *testing.T) {
	sc := defaultShortcuts()
	sc.Enabled = false
	k := newKeysRig(t, sc)
	k.show(request("a", "top-right"))
	time.Sleep(50 * time.Millisecond)
	if k.keys.Registrations() != 0 {
		t.Error("disabled shortcuts registered keys")
	}
}

func TestAnOverlayWithNoHotkeysWorksAsBefore(t *testing.T) {
	r := newOverlayRig(t)
	r.show(request("a", "top-right"))
	r.process(0).finish <- "dismissed"
	r.eventually("the banner to go", func() bool { return r.overlay.Active() == 0 })
}

func TestAKeyAnotherAppHoldsIsReportedAndTheOthersWork(t *testing.T) {
	k := newKeysRig(t, defaultShortcuts())
	k.keys.Refuse("]")
	k.show(buttonReq("a", "top-right", "dismiss"))
	k.waitHeld(true)
	k.until("the problem to be reported", func() bool { k.mu.Lock(); defer k.mu.Unlock(); return len(k.errs) == 1 })
	k.press("previous")
	if s := k.process(0).selectedNow(); s == nil || !*s {
		t.Error("the keys that were given still work")
	}
	k.show(request("b", "top-right"))
	time.Sleep(30 * time.Millisecond)
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.errs) != 1 || k.keys.Registrations() != 1 {
		t.Errorf("the same problem should not be reported for every banner: %d reports, %d registrations", len(k.errs), k.keys.Registrations())
	}
}

func TestTheSelectedColorTravelsWithTheBanner(t *testing.T) {
	r := newOverlayRig(t)
	req := request("a", "top-right")
	req.Style.SelectedColor = 196
	r.show(req)
	var spec struct {
		SelectedColor string `json:"selectedColor"`
	}
	for _, kv := range r.process(0).env {
		if v, ok := strings.CutPrefix(kv, "SHH_BANNER="); ok {
			if err := json.Unmarshal([]byte(v), &spec); err != nil {
				t.Fatal(err)
			}
		}
	}
	if spec.SelectedColor != config.ANSIHex(196) {
		t.Errorf("selectedColor = %q, want %q", spec.SelectedColor, config.ANSIHex(196))
	}
}

func TestTheScriptDrawsTheRingAndTakesKeyboardMessages(t *testing.T) {
	script := strings.Join(alert.OverlayCommand(request("Ana", "top-right")).Args, " ")
	for _, want := range []string{"m.selected", "setBorderColor", "setBorderWidth", "cfg.selectedColor", "m.press", "cfg.buttons[m.press]", "m.dismiss", "startTimer"} {
		if !strings.Contains(script, want) {
			t.Errorf("the banner script does not mention %s", want)
		}
	}
}
