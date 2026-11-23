package hotkey_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/hotkey"
)

// The helper these tests start is this test binary run again: TestMain answers as `shh hotkeys helper` would, on a fake platform whose
// behavior comes from the environment.
//
//	HK_REFUSE=a,b  these keys are refused, as if another app held them
//	HK_PRESS=k     once running, the key k is pressed (after HK_PRESS_AFTER)
//	HK_DIE=1       exit before saying anything
//	HK_HANG=1      never finish registering
func TestMain(m *testing.M) {
	if os.Getenv("HK_HELPER") == "1" {
		if os.Getenv("HK_DIE") == "1" {
			fmt.Fprintln(os.Stderr, "no event loop here")
			os.Exit(3)
		}
		if err := hotkey.Serve(os.Stdin, os.Stdout, &fakePlatform{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakePlatform struct {
	onPress func(id int)
	byKey   map[string]int
	stop    chan struct{}
	once    sync.Once
}

func (p *fakePlatform) Start(onPress func(id int)) error {
	p.onPress, p.byKey, p.stop = onPress, map[string]int{}, make(chan struct{})
	return nil
}

func (p *fakePlatform) Register(mods config.Modifiers, key string, id int) error {
	if os.Getenv("HK_HANG") == "1" {
		time.Sleep(time.Hour)
	}
	for _, refused := range strings.Split(os.Getenv("HK_REFUSE"), ",") {
		if refused == key {
			return errors.New("already registered by another app")
		}
	}
	p.byKey[key] = id
	return nil
}

func (p *fakePlatform) Run() error {
	if key := os.Getenv("HK_PRESS"); key != "" {
		go func() {
			time.Sleep(100 * time.Millisecond)
			p.onPress(p.byKey[key])
		}()
	}
	<-p.stop
	return nil
}

func (p *fakePlatform) Stop() { p.once.Do(func() { close(p.stop) }) }

func helperCommand(env ...string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=NoSuchTest")
		cmd.Env = append(os.Environ(), append([]string{"HK_HELPER=1"}, env...)...)
		return cmd
	}
}

var chord = hotkey.Binding{Modifier: config.Ctrl | config.Alt, Keys: map[string]string{"next": "]", "dismiss": "d", "dismiss_all": "x"}}

type presses struct {
	mu  sync.Mutex
	got []string
}

func (p *presses) add(action string) { p.mu.Lock(); p.got = append(p.got, action); p.mu.Unlock() }

func (p *presses) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		got := append([]string(nil), p.got...)
		p.mu.Unlock()
		if len(got) > 0 {
			if strings.Join(got, ",") != want {
				t.Fatalf("presses = %v, want %s", got, want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no press arrived, want %s", want)
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestAPressReachesTheHandlerWithItsAction(t *testing.T) {
	var got presses
	p := &hotkey.Process{Command: helperCommand("HK_PRESS=d")}
	defer p.Unregister()
	if err := p.Register(chord, got.add); err != nil {
		t.Fatal(err)
	}
	got.wait(t, "dismiss")
}

func TestKeysAnotherAppHoldsAreReportedAndTheRestStillWork(t *testing.T) {
	var got presses
	p := &hotkey.Process{Command: helperCommand("HK_REFUSE=]", "HK_PRESS=x")}
	defer p.Unregister()
	err := p.Register(chord, got.add)
	var refused *hotkey.RegisterError
	if !errors.As(err, &refused) || len(refused.Refused) != 1 || refused.Refused["next"] == "" {
		t.Fatalf("err = %v, want a RegisterError for next only", err)
	}
	if !strings.Contains(err.Error(), "next") || !strings.Contains(err.Error(), "another app") {
		t.Errorf("the message should say which shortcut and why: %v", err)
	}
	got.wait(t, "dismiss_all")
}

func TestWhenNoKeyCouldBeHeldNothingStaysRunning(t *testing.T) {
	p := &hotkey.Process{Command: helperCommand("HK_REFUSE=],d,x")}
	err := p.Register(chord, func(string) {})
	var refused *hotkey.RegisterError
	if !errors.As(err, &refused) || len(refused.Refused) != 3 {
		t.Fatalf("err = %v", err)
	}
	if p.Running() {
		t.Error("a helper that holds nothing should not be left running")
	}
}

func TestUnregisterEndsTheHelper(t *testing.T) {
	p := &hotkey.Process{Command: helperCommand()}
	if err := p.Register(chord, func(string) {}); err != nil {
		t.Fatal(err)
	}
	pid := p.PID()
	if pid == 0 || !alive(pid) {
		t.Fatalf("helper pid %d should be running", pid)
	}
	p.Unregister()
	if p.Running() {
		t.Error("Running after Unregister")
	}
	waitFor(t, func() bool { return !alive(pid) }, "the helper to end")
	p.Unregister() // nothing held: does nothing
}

func TestRegisteringAgainReplacesTheFirstHelper(t *testing.T) {
	var first, second presses
	p := &hotkey.Process{Command: helperCommand()}
	defer p.Unregister()
	if err := p.Register(chord, first.add); err != nil {
		t.Fatal(err)
	}
	old := p.PID()
	if err := p.Register(hotkey.Binding{Modifier: config.Ctrl, Keys: map[string]string{"dismiss": "w"}}, second.add); err != nil {
		t.Fatal(err)
	}
	if p.PID() == old {
		t.Error("a second helper should have been started")
	}
	waitFor(t, func() bool { return !alive(old) }, "the first helper to end")
}

func TestAHelperThatDiesAtOnceIsAnErrorNotAHang(t *testing.T) {
	p := &hotkey.Process{Command: helperCommand("HK_DIE=1"), Timeout: 5 * time.Second}
	start := time.Now()
	err := p.Register(chord, func(string) {})
	if err == nil || p.Running() {
		t.Fatalf("err = %v, running = %v", err, p.Running())
	}
	if time.Since(start) > 3*time.Second {
		t.Error("it waited for the timeout instead of noticing the helper had gone")
	}
}

func TestAHelperThatNeverAnswersTimesOutAndIsKilled(t *testing.T) {
	p := &hotkey.Process{Command: helperCommand("HK_HANG=1"), Timeout: 300 * time.Millisecond, Grace: 50 * time.Millisecond}
	err := p.Register(chord, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v, want one saying the helper did not answer", err)
	}
	if p.Running() {
		t.Error("the helper should have been stopped")
	}
}

func TestACommandThatCannotStartIsAnError(t *testing.T) {
	p := &hotkey.Process{Command: func() *exec.Cmd { return exec.Command("/nonexistent/shh") }}
	if err := p.Register(chord, func(string) {}); err == nil {
		t.Fatal("want an error")
	}
}

func TestABindingWithNoKeysIsAnError(t *testing.T) {
	p := &hotkey.Process{Command: helperCommand()}
	if err := p.Register(hotkey.Binding{Modifier: config.Ctrl}, func(string) {}); err == nil {
		t.Fatal("want an error: there is nothing to hold")
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
