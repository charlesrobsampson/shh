package cli_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

func shortcutsRig(t *testing.T) (*rig, *testkit.FakeHotkeys) {
	t.Helper()
	r := newRig(t, false)
	fake := testkit.NewFakeHotkeys()
	r.env.Hotkeys = fake
	cfg, err := config.Load(t.TempDir() + "/none.json")
	if err != nil {
		t.Fatal(err)
	}
	r.env.Shortcuts = cfg.Alert.Shortcuts
	return r, fake
}

// pressWhenHeld presses a key once the command has registered, as a person would after reading what it says.
func pressWhenHeld(fake *testkit.FakeHotkeys, actions ...string) {
	go func() {
		for i := 0; i < 200; i++ {
			if _, held := fake.Held(); held {
				for _, a := range actions {
					fake.Press(a)
				}
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func TestHotkeysTestHoldsTheKeysAndPrintsWhatIsPressed(t *testing.T) {
	r, fake := shortcutsRig(t)
	pressWhenHeld(fake, "dismiss", "next")
	out := r.mustRun("hotkeys", "test", "-t", "400ms")
	for _, want := range []string{"ctrl+alt", "dismiss", "pressed dismiss", "pressed next", "2 presses"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, held := fake.Held(); held {
		t.Error("the keys should be let go when the test ends")
	}
}

func TestHotkeysTestSaysWhichKeysAnotherAppHolds(t *testing.T) {
	r, fake := shortcutsRig(t)
	fake.Refuse("]")
	pressWhenHeld(fake, "dismiss")
	out := r.mustRun("hotkeys", "test", "-t", "300ms")
	if !strings.Contains(r.err.String(), "next") || !strings.Contains(r.err.String(), "another app") || !strings.Contains(out, "pressed dismiss") {
		t.Errorf("stderr %q, stdout %q: the refused key should be named and the others still work", r.err.String(), out)
	}
}

func TestHotkeysTestFailsWhenNoKeyCanBeHeld(t *testing.T) {
	r, fake := shortcutsRig(t)
	fake.Refuse("]", "[", "escape", "x", "p", "g", "d", "f", "n")
	if code := r.run("hotkeys", "test", "-t", "100ms"); code == 0 || !strings.Contains(r.err.String(), "another app") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestHotkeysTestExplainsWhyItCannotRun(t *testing.T) {
	r, _ := shortcutsRig(t)
	r.env.Shortcuts.Enabled = false
	if code := r.run("hotkeys", "test"); code == 0 || !strings.Contains(r.err.String(), "alert.shortcuts.enabled") {
		t.Errorf("switched off: code %d stderr %q", code, r.err.String())
	}
	r.env.Shortcuts.Enabled = true
	r.env.Hotkeys = nil
	if code := r.run("hotkeys", "test"); code == 0 || !strings.Contains(r.err.String(), "not available") {
		t.Errorf("no support: code %d stderr %q", code, r.err.String())
	}
}

func TestHotkeysHelperRunsWhatTheBuildProvides(t *testing.T) {
	r := newRig(t, false)
	if code := r.run("hotkeys", "helper"); code == 0 || !strings.Contains(r.err.String(), "not available") {
		t.Errorf("without one: code %d stderr %q", code, r.err.String())
	}
	ran := false
	r.env.HotkeysHelper = func() error { ran = true; return nil }
	r.mustRun("hotkeys", "helper")
	if !ran {
		t.Error("the helper was not run")
	}
	r.env.HotkeysHelper = func() error { return errors.New("no event loop") }
	if code := r.run("hotkeys", "helper"); code == 0 || !strings.Contains(r.err.String(), "no event loop") {
		t.Errorf("a failing helper: code %d stderr %q", code, r.err.String())
	}
}

func TestTheHotkeyCommandsNeedNoWorker(t *testing.T) {
	r, _ := shortcutsRig(t) // no worker is being served
	r.env.SpawnWait = 50 * time.Millisecond
	r.env.Spawn = func() error { t.Error("a worker was started for a hotkeys command"); return nil }
	r.run("hotkeys", "test", "-t", "50ms")
}
