package suppress_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/suppress"
)

var _ suppress.Suppressor = (*suppress.Accessibility)(nil)

type harness struct {
	t        *testing.T
	dir      string
	restores int
	mu       sync.Mutex
	errs     []error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{t: t, dir: t.TempDir()}
}

// script builds a command that records each launch in a marker file and then runs shellTail.
func (h *harness) script(shellTail string) func() *exec.Cmd {
	marker := filepath.Join(h.dir, "starts")
	pidFile := filepath.Join(h.dir, "pid")
	return func() *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "echo start >> "+marker+"; echo $$ > "+pidFile+"; "+shellTail)
	}
}

func (h *harness) accessibility(command func() *exec.Cmd, delay time.Duration) *suppress.Accessibility {
	a := suppress.NewAccessibility(suppress.AccessibilityConfig{
		Command:      command,
		Restore:      func() error { h.mu.Lock(); h.restores++; h.mu.Unlock(); return nil },
		RestartDelay: delay,
		OnError:      func(err error) { h.mu.Lock(); h.errs = append(h.errs, err); h.mu.Unlock() },
	})
	h.t.Cleanup(func() { a.Stop() })
	return a
}

func (h *harness) starts() int {
	raw, _ := os.ReadFile(filepath.Join(h.dir, "starts"))
	return strings.Count(string(raw), "start")
}

func (h *harness) pid() int {
	raw, _ := os.ReadFile(filepath.Join(h.dir, "pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

func (h *harness) restoreCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.restores
}

func (h *harness) errorCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.errs)
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartLaunchesTheWatcherOnce(t *testing.T) {
	h := newHarness(t)
	a := h.accessibility(h.script("exec sleep 30"), time.Second)
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("starting twice: %v", err)
	}
	eventually(t, "the watcher to run", func() bool { return alive(h.pid()) })
	if got := h.starts(); got != 1 {
		t.Fatalf("launched %d watchers, want 1", got)
	}
}

func TestStopEndsTheWatcherAndRestoresTheBanners(t *testing.T) {
	h := newHarness(t)
	a := h.accessibility(h.script("exec sleep 30"), time.Second)
	a.Start()
	eventually(t, "the watcher to run", func() bool { return alive(h.pid()) })
	pid := h.pid()

	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the watcher to die", func() bool { return !alive(pid) })
	if got := h.restoreCount(); got != 1 {
		t.Fatalf("restore ran %d times, want once so a parked banner window is put back", got)
	}
	if err := a.Stop(); err != nil || h.restoreCount() != 1 {
		t.Errorf("stopping again: err=%v restores=%d, want a harmless no-op", err, h.restoreCount())
	}
}

func TestStoppingSomethingThatNeverStartedDoesNothing(t *testing.T) {
	h := newHarness(t)
	a := h.accessibility(h.script("exec sleep 30"), time.Second)
	if err := a.Stop(); err != nil || h.restoreCount() != 0 {
		t.Fatalf("Stop before Start: err=%v restores=%d", err, h.restoreCount())
	}
}

func TestACrashedWatcherIsRestarted(t *testing.T) {
	h := newHarness(t)
	a := h.accessibility(h.script("sleep 0.05; exit 3"), 20*time.Millisecond)
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "several restarts", func() bool { return h.starts() >= 3 })
	if h.errorCount() == 0 {
		t.Error("the watcher kept dying without anyone being told")
	}
	a.Stop()
	settled := h.starts()
	time.Sleep(150 * time.Millisecond)
	if got := h.starts(); got != settled {
		t.Fatalf("the watcher was restarted after Stop (%d to %d launches)", settled, got)
	}
}

func TestStartReportsAWatcherThatCannotLaunch(t *testing.T) {
	h := newHarness(t)
	a := h.accessibility(func() *exec.Cmd { return exec.Command("/nonexistent/osascript") }, time.Second)
	if err := a.Start(); err == nil {
		t.Fatal("Start succeeded with a command that cannot run")
	}
	if err := a.Stop(); err != nil {
		t.Errorf("Stop after a failed Start: %v", err)
	}
}

func TestACanBeStartedAgainAfterStopping(t *testing.T) {
	h := newHarness(t)
	a := h.accessibility(h.script("exec sleep 30"), time.Second)
	a.Start()
	eventually(t, "the first watcher", func() bool { return alive(h.pid()) })
	first := h.pid()
	a.Stop()
	eventually(t, "it to stop", func() bool { return !alive(first) })

	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second watcher", func() bool { return h.starts() == 2 && alive(h.pid()) })
}

func TestARestoreFailureIsReturnedButTheWatcherIsStillStopped(t *testing.T) {
	h := newHarness(t)
	boom := errors.New("accessibility permission missing")
	a := suppress.NewAccessibility(suppress.AccessibilityConfig{
		Command: h.script("exec sleep 30"), Restore: func() error { return boom }, RestartDelay: time.Second,
	})
	a.Start()
	eventually(t, "the watcher to run", func() bool { return alive(h.pid()) })
	pid := h.pid()
	if err := a.Stop(); !errors.Is(err, boom) {
		t.Fatalf("Stop err = %v, want the restore failure", err)
	}
	eventually(t, "the watcher to die", func() bool { return !alive(pid) })
}

func TestTheDefaultWatcherIsTheAccessibilityScript(t *testing.T) {
	a := suppress.NewAccessibility(suppress.AccessibilityConfig{OwnPrefix: "[mine]"})
	cmd := a.WatcherCommand()
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "osascript") || !strings.Contains(joined, "JavaScript") {
		t.Errorf("command = %q, want osascript running JXA", joined)
	}
	if !strings.Contains(joined, "NotificationCenter") || !strings.Contains(joined, "position") {
		t.Errorf("the script does not look like the banner watcher: %q", joined)
	}
	if !strings.Contains(joined, "getppid") {
		t.Error("the watcher must exit when shh dies, or a crash would leave banners hidden for good")
	}
	var prefix string
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "SHH_OWN_PREFIX="); ok {
			prefix = v
		}
	}
	if prefix != "[mine]" {
		t.Errorf("the own-banner prefix reached the script as %q, want [mine]", prefix)
	}
	if def := suppress.NewAccessibility(suppress.AccessibilityConfig{}).WatcherCommand(); !strings.Contains(strings.Join(def.Env, "\n"), "SHH_OWN_PREFIX=[shh]") {
		t.Error("the default prefix should be [shh]")
	}
}
