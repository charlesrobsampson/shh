package cli_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/launchd"
	"github.com/charlesrobsampson/shh/internal/session"
)

func TestWorkerStatusSaysWhetherItIsRunningAndWhatItIsKeepingGoing(t *testing.T) {
	r := newRig(t, false)
	if out := r.mustRun("worker", "status"); !strings.Contains(out, "not running") || !strings.Contains(out, "shh worker start") {
		t.Errorf("not running:\n%s", out)
	}
	r = newRig(t, true)
	r.saveRoutines(`{"routines": {"a": {"steps": ["75% 5m x"], "triggers": ["daily at 09:30", "fri at 10:00 --disabled", "mon at 10:00"]}}}`)
	r.mustRun("focus", "75%", "20m")
	out := r.mustRun("worker", "status")
	for _, want := range []string{"is running (pid", "up ", "focus:    on at 75%", "routine:  none running", "triggers: 2 switched on (the worker stays up to start them)"} {
		if !strings.Contains(out, want) {
			t.Errorf("status is missing %q:\n%s", want, out)
		}
	}
	r2 := newRig(t, true)
	r2.saveRoutines(lite)
	r2.mustRun("routine", "run", "pomo")
	if out = r2.mustRun("worker", "status"); !strings.Contains(out, "routine:  pomo is running") || !strings.Contains(out, "triggers: none switched on") {
		t.Errorf("status:\n%s", out)
	}
}

func TestWorkerStartStartsOneOrSaysItIsRunning(t *testing.T) {
	r := newRig(t, false)
	r.canSpawn()
	if out := r.mustRun("worker", "start"); !strings.Contains(out, "Started the shh worker (pid") {
		t.Errorf("start:\n%s", out)
	}
	if out := r.mustRun("worker", "start"); !strings.Contains(out, "already running (pid") {
		t.Errorf("second start:\n%s", out)
	}
	n := newRig(t, false)
	if code := n.run("worker", "start"); code == 0 || !strings.Contains(n.err.String(), "cannot be started from here") {
		t.Errorf("code %d stderr %q: a build that cannot spawn says so", code, n.err.String())
	}
}

func TestWorkerStopShutsItDownAndSaysSoWhenThereIsNone(t *testing.T) {
	r := newRig(t, true)
	if out := r.mustRun("worker", "stop"); !strings.Contains(out, "Stopped the shh worker") {
		t.Errorf("stop:\n%s", out)
	}
	if _, err := session.NewClient(r.socket).Status(); err == nil {
		t.Error("the worker should be gone")
	}
	if out := r.mustRun("worker", "stop"); !strings.Contains(out, "not running") {
		t.Errorf("stop with none:\n%s", out)
	}
}

func TestWorkerStopRefusesWhileFocusOrARoutineIsOnUnlessForcedOrConfirmed(t *testing.T) {
	r := newRig(t, true)
	r.mustRun("focus", "75%", "20m")
	if code := r.run("worker", "stop"); code == 0 || !strings.Contains(r.err.String(), "banners would come through") || !strings.Contains(r.err.String(), "--force") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
	if _, err := session.NewClient(r.socket).Status(); err != nil {
		t.Fatal("a refused stop must leave the worker running")
	}
	r.env.IsTerminal = true
	r.env.Stdin = strings.NewReader("n\n")
	if out := r.mustRun("worker", "stop"); !strings.Contains(out, "Stop the worker anyway? [y/N]") || !strings.Contains(out, "Left it running.") {
		t.Errorf("declined:\n%s", out)
	}
	r.env.Stdin = strings.NewReader("y\n")
	if out := r.mustRun("worker", "stop"); !strings.Contains(out, "Stopped the shh worker") {
		t.Errorf("confirmed:\n%s", out)
	}
	r2 := newRig(t, true)
	r2.saveRoutines(lite)
	r2.mustRun("routine", "run", "pomo")
	if code := r2.run("worker", "stop"); code == 0 || !strings.Contains(r2.err.String(), "the routine pomo is running and would stop moving on") {
		t.Errorf("code %d stderr %q", code, r2.err.String())
	}
	if out := r2.mustRun("worker", "stop", "--force"); !strings.Contains(out, "Stopped") {
		t.Errorf("forced:\n%s", out)
	}
}

func TestWorkerRestartReplacesItWithoutAskingAndStartsOneWhenThereIsNone(t *testing.T) {
	r := newRig(t, true)
	r.canSpawn()
	r.mustRun("focus", "75%", "20m")
	if out := r.mustRun("worker", "restart"); !strings.Contains(out, "Started the shh worker") {
		t.Errorf("restart:\n%s", out)
	}
	if _, err := session.NewClient(r.socket).Status(); err != nil {
		t.Errorf("a worker should be answering after a restart: %v", err)
	}
	r.mustRun("worker", "stop", "--force")
	if out := r.mustRun("worker", "restart"); !strings.Contains(out, "Started the shh worker") {
		t.Errorf("restart with none:\n%s", out)
	}
}

func TestWorkerStatusNotesAnOlderBuild(t *testing.T) {
	r := newRig(t, true)
	session.BuildID = "oldbuild0001"
	t.Cleanup(func() { session.BuildID = "" })
	r.env.Build = "newbuild0002"
	if out := r.mustRun("worker", "status"); !strings.Contains(out, "older build") || !strings.Contains(out, "shh worker restart") {
		t.Errorf("status:\n%s", out)
	}
}

func TestWorkerInstallAndUninstallManageTheLoginAgentAndStatusSaysSo(t *testing.T) {
	r := newRig(t, false)
	loaded := false
	home := t.TempDir()
	r.env.Agent = &launchd.Agent{Home: home, UID: 501, Exe: "/usr/local/bin/shh", Log: filepath.Join(home, "shh.log"),
		Launchctl: func(args ...string) ([]byte, error) {
			switch args[0] {
			case "bootstrap":
				loaded = true
			case "bootout":
				loaded = false
			case "print":
				if !loaded {
					return nil, errors.New("not loaded")
				}
			}
			return nil, nil
		}}
	if out := r.mustRun("worker", "uninstall"); !strings.Contains(out, "not installed: nothing to remove") {
		t.Errorf("out = %q", out)
	}
	out := r.mustRun("worker", "install")
	if !strings.Contains(out, "starts at every login") || !strings.Contains(out, "It is starting now") || !loaded {
		t.Errorf("out = %q loaded %v", out, loaded)
	}
	if _, err := os.Stat(r.env.Agent.Path()); err != nil {
		t.Errorf("the plist is not there: %v", err)
	}
	r.serve()
	if out := r.mustRun("worker", "status"); !strings.Contains(out, "at login: starts by itself") {
		t.Errorf("status:\n%s", out)
	}
	if out := r.mustRun("worker", "install"); !strings.Contains(out, "already running") {
		t.Errorf("with a worker running: %q", out)
	}
	if out := r.mustRun("worker", "uninstall"); !strings.Contains(out, "Removed the login agent") || loaded {
		t.Errorf("out = %q loaded %v", out, loaded)
	}
	if out := r.mustRun("worker", "status"); !strings.Contains(out, "at login: not started") {
		t.Errorf("status:\n%s", out)
	}
}

func TestADaemonThatFindsAWorkerAlreadyRunningExitsCleanly(t *testing.T) {
	r := newRig(t, false)
	r.env.Daemon = func(context.Context) error { return fmt.Errorf("%w (socket x)", session.ErrAlreadyRunning) }
	if code := r.run("daemon"); code != 0 || !strings.Contains(r.err.String(), "already running") {
		t.Errorf("code %d: %s", code, r.err.String())
	}
	r.env.Daemon = func(context.Context) error { return errors.New("disk full") }
	if code := r.run("daemon"); code != 1 {
		t.Errorf("a real failure exits %d", code)
	}
}
