package session_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/session"
)

type daemonRig struct {
	*workerRig
	cfg    session.DaemonConfig
	client *session.Client
	cancel context.CancelFunc
	done   chan error
}

func newDaemonRig(t *testing.T) *daemonRig {
	t.Helper()
	dir, err := os.MkdirTemp("", "shh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	r := &daemonRig{workerRig: newWorkerRig(t)}
	r.cfg = session.DaemonConfig{
		Worker: r.worker, Clock: r.clock, SocketPath: filepath.Join(dir, "s.sock"),
		LockPath: filepath.Join(dir, "shh.lock"), PIDPath: filepath.Join(dir, "shh.pid"),
		IdleGrace: 5 * time.Second, OnError: func(err error) { r.errs = append(r.errs, err) },
	}
	r.client = session.NewClient(r.cfg.SocketPath)
	return r
}

// start runs the daemon in the background and waits until it answers.
func (r *daemonRig) start() {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan error, 1)
	go func() { r.done <- session.RunDaemon(ctx, r.cfg) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := r.client.Status(); err == nil {
			break
		}
		select {
		case err := <-r.done:
			r.t.Fatalf("the daemon exited while starting: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			r.t.Fatal("the daemon never started answering")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Cleanup(func() { r.stop() })
}

func (r *daemonRig) stop() error {
	if r.cancel == nil {
		return nil
	}
	r.cancel()
	r.cancel = nil
	select {
	case err := <-r.done:
		r.done <- err // keep it readable for the test
		return err
	case <-time.After(3 * time.Second):
		return errors.New("the daemon did not stop")
	}
}

func (r *daemonRig) exited() (bool, error) {
	select {
	case err := <-r.done:
		r.done <- err
		return true, err
	default:
		return false, nil
	}
}

// waitExit gives the daemon a moment of real time to react to a fake-clock change.
func (r *daemonRig) waitExit() bool {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done, _ := r.exited(); done {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestTheDaemonServesRecordsItsPIDAndCleansUp(t *testing.T) {
	r := newDaemonRig(t)
	r.worker.SetBaseline(30)
	r.start()

	raw, err := os.ReadFile(r.cfg.PIDPath)
	if err != nil {
		t.Fatalf("no PID file: %v", err)
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err != nil || pid != os.Getpid() {
		t.Errorf("PID file holds %q, want this process's id %d", raw, os.Getpid())
	}
	if st, err := r.client.Status(); err != nil || st.Level != 30 {
		t.Errorf("status over the socket = %+v, %v", st, err)
	}

	if err := r.stop(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	for _, path := range []string{r.cfg.PIDPath, r.cfg.SocketPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s was left behind after shutdown", filepath.Base(path))
		}
	}
}

func TestASecondDaemonDoesNotStartAndTheFirstIsUndisturbed(t *testing.T) {
	r := newDaemonRig(t)
	r.worker.SetBaseline(30)
	r.start()

	other := newWorkerRig(t)
	second := r.cfg
	second.Worker, second.Clock = other.worker, other.clock
	err := session.RunDaemon(context.Background(), second)
	if !errors.Is(err, session.ErrAlreadyRunning) {
		t.Fatalf("second daemon: err = %v, want ErrAlreadyRunning", err)
	}
	if st, err := r.client.Status(); err != nil || st.Level != 30 {
		t.Errorf("the first daemon was disturbed: %+v, %v", st, err)
	}
	if raw, _ := os.ReadFile(r.cfg.PIDPath); strings.TrimSpace(string(raw)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("the PID file was overwritten: %q", raw)
	}
}

func TestAStalePIDFileAndSocketAreRecovered(t *testing.T) {
	r := newDaemonRig(t)
	// What a crashed daemon leaves: a PID that no longer exists, an unlocked lock file and a dead socket.
	if err := os.WriteFile(r.cfg.PIDPath, []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg.LockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.worker.SetBaseline(30)
	r.start()
	if raw, _ := os.ReadFile(r.cfg.PIDPath); strings.TrimSpace(string(raw)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("the stale PID file was not replaced: %q", raw)
	}
}

func TestStoppingTheDaemonStopsTheSuppressor(t *testing.T) {
	r := newDaemonRig(t)
	r.worker.SetBaseline(30)
	r.start()
	if !r.suppressor.Running() {
		t.Fatal("the suppressor is not running for a persisted baseline")
	}
	if err := r.stop(); err != nil {
		t.Fatal(err)
	}
	if r.suppressor.Running() {
		t.Fatal("a graceful stop left the banner suppressor running")
	}
}

func TestAPersistedBaselineIsRearmedWithoutAnyCommand(t *testing.T) {
	r := newDaemonRig(t)
	r.worker.SetBaseline(45)
	r.worker.Shutdown() // the previous worker process is gone; the baseline stays in the store

	fresh := r.build()
	r.cfg.Worker = fresh
	r.suppressor.Stop()
	r.start()
	if st, _ := r.client.Status(); st.Level != 45 {
		t.Fatalf("level after restart = %d, want the persisted 45", st.Level)
	}
	if !r.suppressor.Running() {
		t.Error("the suppressor was not started for the persisted baseline")
	}
}

func TestTheDaemonExitsOnceFocusHasBeenOffForTheGracePeriod(t *testing.T) {
	r := newDaemonRig(t)
	r.worker.SetBaseline(30)
	r.start()

	if _, err := r.client.Stop(true); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(4 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if done, _ := r.exited(); done {
		t.Fatal("the daemon exited before the grace period was over")
	}
	r.clock.Advance(2 * time.Second)
	if !r.waitExit() {
		t.Fatal("the daemon is still running after focus was off for longer than the grace period")
	}
	if _, err := os.Stat(r.cfg.SocketPath); !os.IsNotExist(err) {
		t.Error("the socket is still there after the daemon exited")
	}
}

func TestANewLevelDuringTheGracePeriodKeepsTheDaemonAlive(t *testing.T) {
	r := newDaemonRig(t)
	r.start() // nothing active yet, as when the CLI has just spawned it
	r.clock.Advance(3 * time.Second)
	if _, err := r.client.Baseline(30); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(10 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if done, _ := r.exited(); done {
		t.Fatal("the daemon exited although a baseline was set in time")
	}
	// Turning focus off starts a fresh grace period.
	r.client.Baseline(0)
	r.clock.Advance(4 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if done, _ := r.exited(); done {
		t.Fatal("the grace period did not restart")
	}
	r.clock.Advance(2 * time.Second)
	if !r.waitExit() {
		t.Fatal("the daemon did not exit after the second grace period")
	}
}

func TestADaemonThatIsNeverGivenAnythingExits(t *testing.T) {
	r := newDaemonRig(t)
	r.start()
	r.clock.Advance(6 * time.Second)
	if !r.waitExit() {
		t.Fatal("an idle daemon never exited")
	}
}

func TestAnExpiringTimerEndsTheDaemonToo(t *testing.T) {
	r := newDaemonRig(t)
	r.worker.Escalate(session.Escalation{Level: 60, Duration: 10 * time.Minute})
	r.start()
	r.clock.Advance(10 * time.Minute) // the only layer expires
	time.Sleep(50 * time.Millisecond)
	r.clock.Advance(6 * time.Second)
	if !r.waitExit() {
		t.Fatal("the daemon outlived its last timer")
	}
	if r.suppressor.Running() {
		t.Error("the suppressor outlived its last timer")
	}
}

func TestTheLockIsReleasedWhenTheDaemonStops(t *testing.T) {
	r := newDaemonRig(t)
	r.worker.SetBaseline(30)
	r.start()
	if err := r.stop(); err != nil {
		t.Fatal(err)
	}
	r.worker = r.build()
	r.cfg.Worker = r.worker
	r.worker.SetBaseline(30)
	r.start() // would fail with ErrAlreadyRunning if the lock were still held
}
