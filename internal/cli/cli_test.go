package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/cli"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

var t0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

type rig struct {
	t          *testing.T
	worker     *session.Worker
	clock      *testkit.FakeClock
	suppressor *testkit.FakeSuppressor
	store      *store.Store
	socket     string
	out, err   bytes.Buffer
	env        cli.Env
	stop       context.CancelFunc
	served     chan struct{}
	spawns     int
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "shh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// newRig builds a worker but only serves it when serve is true, so "not running" can be tested too.
func newRig(t *testing.T, serve bool) *rig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := &rig{t: t, clock: testkit.NewFakeClock(t0), suppressor: testkit.NewFakeSuppressor(), store: s, socket: shortSocketPath(t)}
	rl, err := rules.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	r.worker, err = session.NewWorker(session.Config{
		Store: s, Source: testkit.NewFakeSource(), Clock: r.clock, Rules: rl, Alerter: testkit.NewFakeAlerter(), Suppressor: r.suppressor, Location: time.UTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.env = cli.Env{Stdout: &r.out, Stderr: &r.err, SocketPath: r.socket, ConfigDir: t.TempDir(), Version: "test", Label: "shh", SpawnWait: 500 * time.Millisecond}
	if serve {
		r.serve()
	}
	t.Cleanup(r.shutdown)
	return r
}

func (r *rig) serve() {
	r.t.Helper()
	server, err := session.Listen(r.socket, r.worker)
	if err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	r.served = make(chan struct{})
	go func() { server.Serve(ctx); close(r.served) }()
}

func (r *rig) shutdown() {
	if r.stop != nil {
		r.stop()
		<-r.served
		r.stop = nil
	}
}

// run drives the real entrypoint and returns the exit code; output is collected in r.out and r.err.
func (r *rig) run(args ...string) int {
	r.t.Helper()
	r.out.Reset()
	r.err.Reset()
	return cli.Run(args, r.env)
}

func (r *rig) status() session.Status {
	r.t.Helper()
	st, err := r.worker.Status()
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

func (r *rig) mustRun(args ...string) string {
	r.t.Helper()
	if code := r.run(args...); code != 0 {
		r.t.Fatalf("shh %s exited %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, r.out.String(), r.err.String())
	}
	return r.out.String()
}

func TestTheManifestFocusExamplesParseAndApply(t *testing.T) {
	t.Run("25 minute session with a note", func(t *testing.T) {
		r := newRig(t, true)
		out := r.mustRun("focus", "-t", "25m", "-f", "75%", "-m", "Refactoring database connection pool")
		top := r.status().Layers[0]
		if top.Percent != 75 || !top.Timed || top.Remaining != 25*time.Minute || top.Note != "Refactoring database connection pool" || top.Kind != "escalation" {
			t.Fatalf("top layer = %+v", top)
		}
		for _, want := range []string{"75%", "25m", "Refactoring database connection pool"} {
			if !strings.Contains(out, want) {
				t.Errorf("output %q should mention %q", out, want)
			}
		}
	})
	t.Run("short syntax with a positional note and no timer", func(t *testing.T) {
		r := newRig(t, true)
		r.mustRun("f", "-f", "90%", "Prod OOM investigation")
		top := r.status().Layers[0]
		if top.Percent != 90 || top.Timed || top.Note != "Prod OOM investigation" {
			t.Fatalf("top layer = %+v, want an indefinite 90%% session with the note", top)
		}
	})
	t.Run("the long flag names", func(t *testing.T) {
		r := newRig(t, true)
		r.mustRun("focus", "--timer", "50m", "--focus", "deep", "--note", "deploy")
		top := r.status().Layers[0]
		if top.Percent != 90 || top.Remaining != 50*time.Minute || top.Note != "deploy" {
			t.Fatalf("top layer = %+v", top)
		}
	})
	t.Run("an unquoted note is joined", func(t *testing.T) {
		r := newRig(t, true)
		r.mustRun("f", "-f", "light", "write", "the", "report")
		if got := r.status().Layers[0].Note; got != "write the report" {
			t.Errorf("note = %q", got)
		}
	})
	t.Run("a tier and the defaults", func(t *testing.T) {
		r := newRig(t, true)
		r.mustRun("focus")
		top := r.status().Layers[0]
		if top.Percent != 65 || top.Timed {
			t.Fatalf("with no flags: %+v, want an indefinite session at the medium tier", top)
		}
	})
	t.Run("zero means until stopped", func(t *testing.T) {
		r := newRig(t, true)
		r.mustRun("focus", "-t", "0", "-f", "75%")
		if r.status().Layers[0].Timed {
			t.Error("-t 0 should be indefinite")
		}
	})
}

func TestThenAndItsClearAlias(t *testing.T) {
	tests := []struct {
		args []string
		want int
	}{
		{[]string{"focus", "-f", "90%", "-t", "50m", "--then", "off"}, 0},
		{[]string{"focus", "-f", "90%", "-t", "50m", "--clear", "30%"}, 30},
		{[]string{"focus", "-f", "90%", "-t", "50m", "--then", "light"}, 35},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			r := newRig(t, true)
			r.mustRun(tt.args...)
			then := r.status().Layers[0].ThenLevel
			if then == nil || *then != tt.want {
				t.Fatalf("then = %v, want %d", then, tt.want)
			}
		})
	}
	t.Run("not given means the layer beneath", func(t *testing.T) {
		r := newRig(t, true)
		r.mustRun("focus", "-f", "90%", "-t", "50m")
		if then := r.status().Layers[0].ThenLevel; then != nil {
			t.Errorf("then = %v, want none", *then)
		}
	})
}

func TestBaseline(t *testing.T) {
	r := newRig(t, true)
	tests := []struct {
		arg  string
		want int
	}{{"30%", 30}, {"light", 35}, {"LOCKDOWN", 99}, {"100%", 99}, {"off", 0}}
	for _, tt := range tests {
		r.mustRun("baseline", tt.arg)
		if got := r.status().Level; got != tt.want {
			t.Errorf("baseline %s: level %d, want %d", tt.arg, got, tt.want)
		}
	}
	if len(r.status().Layers) != 0 {
		t.Error("baseline off left a layer behind")
	}
}

func TestStopPopsTheTopAndStopAllClearsEverything(t *testing.T) {
	r := newRig(t, true)
	r.mustRun("baseline", "30%")
	r.mustRun("focus", "-f", "90%", "-t", "1h")
	r.mustRun("focus", "-f", "75%")

	r.mustRun("stop")
	if st := r.status(); st.Level != 90 || len(st.Layers) != 2 {
		t.Fatalf("after one stop: level %d, %d layers; want 90 and 2", st.Level, len(st.Layers))
	}
	r.mustRun("s") // the alias
	if st := r.status(); st.Level != 30 || len(st.Layers) != 1 {
		t.Fatalf("after two stops: level %d, %d layers; want the baseline", st.Level, len(st.Layers))
	}
	if code := r.run("stop"); code != 1 || !strings.Contains(r.err.String(), "stop --all") {
		t.Fatalf("stop with only a baseline: exit %d, stderr %q; want a failure that points at --all", code, r.err.String())
	}
	r.mustRun("stop", "--all")
	if st := r.status(); st.Level != 0 || len(st.Layers) != 0 {
		t.Fatalf("after stop --all: %+v", st)
	}
	if r.suppressor.Running() {
		t.Error("stopping everything left the suppressor running")
	}
}

func TestPauseAndResume(t *testing.T) {
	r := newRig(t, true)
	r.mustRun("focus", "-f", "90%", "-t", "30m")
	r.clock.Advance(10 * time.Minute)
	out := r.mustRun("pause")
	if top := r.status().Layers[0]; !top.Paused || top.Remaining != 20*time.Minute || !strings.Contains(out, "paused") {
		t.Fatalf("after pause: %+v\n%s", top, out)
	}
	r.clock.Advance(time.Hour)
	r.mustRun("resume")
	if top := r.status().Layers[0]; top.Paused || top.Remaining != 20*time.Minute {
		t.Fatalf("after resume: %+v", top)
	}
	if code := r.run("resume"); code != 1 {
		t.Errorf("resuming a running timer exited %d, want 1", code)
	}
	r.mustRun("stop")
	if code := r.run("pause"); code != 1 {
		t.Errorf("pausing with nothing to pause exited %d, want 1", code)
	}
}

func TestStatusReadsLikeAHuman(t *testing.T) {
	r := newRig(t, true)
	if out := r.mustRun("status"); !strings.Contains(out, "off") {
		t.Errorf("status with nothing active = %q, want it to say focus is off", out)
	}
	r.mustRun("baseline", "30%")
	r.mustRun("focus", "-f", "90%", "-t", "40m", "--then", "off", "-m", "Prod OOM investigation")
	r.clock.Advance(2 * time.Minute)
	out := r.mustRun("status")
	for _, want := range []string{"90% (deep)", "baseline", "30%", "escalation", "38m left", "then off", "Prod OOM investigation"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output is missing %q:\n%s", want, out)
		}
	}
	r.mustRun("focus", "-f", "75%")
	if out := r.mustRun("status"); !strings.Contains(out, "until stopped") {
		t.Errorf("an indefinite layer should say so:\n%s", out)
	}
}

func TestStatusShortIsPromptSized(t *testing.T) {
	r := newRig(t, true)
	if out := r.mustRun("status", "--short"); strings.TrimSpace(out) != "" {
		t.Errorf("--short with focus off = %q, want nothing so a prompt can stay empty", out)
	}
	r.mustRun("baseline", "30%")
	if got := strings.TrimSpace(r.mustRun("status", "--short")); got != "shh 30%" {
		t.Errorf("baseline only: %q, want 'shh 30%%'", got)
	}
	r.mustRun("focus", "-f", "90%", "-t", "40m")
	r.clock.Advance(2 * time.Minute)
	if got := strings.TrimSpace(r.mustRun("status", "--short")); got != "shh 38m · 90%" {
		t.Errorf("timed escalation: %q, want 'shh 38m · 90%%'", got)
	}
	r.clock.Advance(40*time.Minute - 2*time.Minute - 30*time.Second)
	if got := strings.TrimSpace(r.mustRun("status", "--short")); got != "shh 30s · 90%" {
		t.Errorf("under a minute left: %q", got)
	}
	r.mustRun("stop")
	r.mustRun("focus", "-f", "75%", "-t", "2h5m")
	if got := strings.TrimSpace(r.mustRun("status", "--short")); got != "shh 2h05m · 75%" {
		t.Errorf("over an hour: %q", got)
	}
	r.mustRun("pause")
	if got := strings.TrimSpace(r.mustRun("status", "--short")); got != "shh ⏸ 75%" {
		t.Errorf("paused: %q, want 'shh ⏸ 75%%'", got)
	}
	r.mustRun("resume")
	r.mustRun("focus", "-f", "60%")
	if got := strings.TrimSpace(r.mustRun("status", "--short")); got != "shh 60%" {
		t.Errorf("an indefinite escalation on top: %q", got)
	}
}

func TestTheShortLabelIsConfigurable(t *testing.T) {
	r := newRig(t, true)
	r.env.Label = "🔇"
	r.mustRun("baseline", "30%")
	if got := strings.TrimSpace(r.mustRun("status", "--short")); got != "🔇 30%" {
		t.Errorf("got %q", got)
	}
}

func TestStatusJSON(t *testing.T) {
	r := newRig(t, true)
	r.mustRun("focus", "-f", "90%", "-t", "40m", "-m", "deploy")
	var got struct {
		Running bool `json:"running"`
		Level   int  `json:"level"`
		Layers  []struct {
			Percent       int    `json:"percent"`
			Note          string `json:"note"`
			Timed         bool   `json:"timed"`
			RemainingSecs int    `json:"remaining_secs"`
		} `json:"layers"`
	}
	if err := json.Unmarshal([]byte(r.mustRun("status", "--json")), &got); err != nil {
		t.Fatalf("--json is not JSON: %v\n%s", err, r.out.String())
	}
	if !got.Running || got.Level != 90 || len(got.Layers) != 1 || got.Layers[0].Note != "deploy" || got.Layers[0].RemainingSecs != 2400 || !got.Layers[0].Timed {
		t.Errorf("status JSON = %+v", got)
	}
}

func TestInvalidInputFailsClearlyAndChangesNothing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"a decimal threshold", []string{"focus", "-f", "0.75"}, "0.75"},
		{"a threshold above 100", []string{"focus", "-f", "150%"}, "150%"},
		{"a bare number threshold", []string{"focus", "-f", "75"}, "75%"},
		{"an unknown tier", []string{"focus", "-f", "extreme"}, "extreme"},
		{"a bad duration", []string{"focus", "-t", "soon"}, "soon"},
		{"a duration with no unit", []string{"focus", "-t", "25"}, "25m"},
		{"a negative duration", []string{"focus", "-t=-5m"}, "5m"},
		{"a bad then", []string{"focus", "-f", "90%", "--then", "later"}, "later"},
		{"a note given twice", []string{"focus", "-m", "one", "two"}, "note"},
		{"a bad baseline", []string{"baseline", "abc"}, "abc"},
		{"a baseline with no value", []string{"baseline"}, "level"},
		{"an unknown command", []string{"frobnicate"}, "frobnicate"},
		{"an unknown flag", []string{"focus", "--nope"}, "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, true)
			code := r.run(tt.args...)
			if code == 0 {
				t.Fatalf("exit 0; stdout %q", r.out.String())
			}
			if !strings.Contains(r.err.String(), tt.wantErr) {
				t.Errorf("stderr %q should mention %q", r.err.String(), tt.wantErr)
			}
			if st := r.status(); len(st.Layers) != 0 {
				t.Errorf("a rejected command changed the worker: %d layers", len(st.Layers))
			}
		})
	}
}

func TestInvalidInputExitsWithUsageCodeTwo(t *testing.T) {
	r := newRig(t, true)
	if code := r.run("focus", "-f", "0.75"); code != 2 {
		t.Errorf("bad input exited %d, want 2", code)
	}
}

func TestWhenNothingIsRunning(t *testing.T) {
	r := newRig(t, false)

	if code := r.run("status"); code != 0 || !strings.Contains(r.out.String(), "off") || !strings.Contains(r.out.String(), "not running") {
		t.Errorf("status: exit %d, stdout %q; want focus off and a note that shh is not running", code, r.out.String())
	}
	var js struct {
		Running bool `json:"running"`
		Level   int  `json:"level"`
	}
	r.run("status", "--json")
	if err := json.Unmarshal(r.out.Bytes(), &js); err != nil || js.Running || js.Level != 0 {
		t.Errorf("status --json = %q (%v), want running false", r.out.String(), err)
	}
	if code := r.run("status", "--short"); code != 0 || strings.TrimSpace(r.out.String()) != "" {
		t.Errorf("status --short: exit %d, stdout %q; want an empty prompt segment", code, r.out.String())
	}
	if code := r.run("stop"); code != 0 || !strings.Contains(r.out.String(), "not running") {
		t.Errorf("stop: exit %d, stdout %q; stopping nothing is not a failure", code, r.out.String())
	}
	for _, cmd := range []string{"pause", "resume"} {
		if code := r.run(cmd); code != 1 || !strings.Contains(r.err.String(), "not running") {
			t.Errorf("%s: exit %d, stderr %q; want a failure saying shh is not running", cmd, code, r.err.String())
		}
	}
	if code := r.run("focus", "-f", "75%"); code != 1 || !strings.Contains(r.err.String(), "not running") {
		t.Errorf("focus with no spawner: exit %d, stderr %q", code, r.err.String())
	}
}

func TestStartingASessionSpawnsAWorkerWhenNoneIsRunning(t *testing.T) {
	r := newRig(t, false)
	r.env.Spawn = func() error {
		r.spawns++
		r.serve() // the "new process" starts listening
		return nil
	}
	r.mustRun("focus", "-f", "75%", "-t", "25m")
	if r.spawns != 1 {
		t.Fatalf("spawned %d times, want 1", r.spawns)
	}
	if top := r.status().Layers[0]; top.Percent != 75 || top.Remaining != 25*time.Minute {
		t.Fatalf("the command was not applied after spawning: %+v", top)
	}
	r.mustRun("baseline", "30%")
	if r.spawns != 1 {
		t.Errorf("spawned again although a worker was running (%d spawns)", r.spawns)
	}
}

func TestBaselineAlsoSpawnsAWorker(t *testing.T) {
	r := newRig(t, false)
	r.env.Spawn = func() error { r.spawns++; r.serve(); return nil }
	r.mustRun("baseline", "30%")
	if r.spawns != 1 || r.status().Level != 30 {
		t.Fatalf("spawns=%d level=%d", r.spawns, r.status().Level)
	}
}

func TestSpawnFailuresAreReported(t *testing.T) {
	t.Run("the spawner fails", func(t *testing.T) {
		r := newRig(t, false)
		r.env.Spawn = func() error { return errors.New("cannot find the shh binary") }
		if code := r.run("focus", "-f", "75%"); code != 1 || !strings.Contains(r.err.String(), "cannot find the shh binary") {
			t.Errorf("exit %d, stderr %q", code, r.err.String())
		}
	})
	t.Run("the worker never comes up", func(t *testing.T) {
		r := newRig(t, false)
		r.env.SpawnWait = 150 * time.Millisecond
		r.env.Spawn = func() error { return nil }
		start := time.Now()
		if code := r.run("focus", "-f", "75%"); code != 1 || !strings.Contains(r.err.String(), "did not start") {
			t.Errorf("exit %d, stderr %q", code, r.err.String())
		}
		if time.Since(start) > 2*time.Second {
			t.Error("waited far longer than SpawnWait")
		}
	})
}

func TestBareShhOpensTheTerminalUIOrPrintsStatus(t *testing.T) {
	t.Run("on a terminal with a UI", func(t *testing.T) {
		r := newRig(t, true)
		r.env.IsTerminal = true
		opened := 0
		r.env.TUI = func() error { opened++; return nil }
		if code := r.run(); code != 0 || opened != 1 {
			t.Fatalf("exit %d, UI opened %d times; want 0 and 1", code, opened)
		}
		r.run("ui")
		if opened != 2 {
			t.Errorf("shh ui opened the UI %d times in total, want 2", opened)
		}
	})
	t.Run("a UI that fails", func(t *testing.T) {
		r := newRig(t, true)
		r.env.IsTerminal = true
		r.env.TUI = func() error { return errors.New("no terminal") }
		if code := r.run(); code != 1 || !strings.Contains(r.err.String(), "no terminal") {
			t.Errorf("exit %d, stderr %q", code, r.err.String())
		}
	})
	t.Run("not on a terminal it prints the status instead", func(t *testing.T) {
		r := newRig(t, true)
		r.mustRun("baseline", "30%")
		r.env.IsTerminal = false
		r.env.TUI = func() error { t.Error("the UI opened without a terminal"); return nil }
		if out := r.mustRun(); !strings.Contains(out, "30%") {
			t.Errorf("bare shh piped = %q, want the status", out)
		}
	})
}

func TestVersionAndHelp(t *testing.T) {
	r := newRig(t, true)
	if code := r.run("--version"); code != 0 || strings.TrimSpace(r.out.String()) != "shh test" {
		t.Errorf("--version: exit %d, %q", code, r.out.String())
	}
	if code := r.run("--help"); code != 0 {
		t.Errorf("--help exited %d", code)
	}
	help := r.out.String() + r.err.String()
	for _, want := range []string{"focus", "stop", "baseline", "status", "pause", "resume", "replay"} {
		if !strings.Contains(help, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}

func TestReplayStillWorks(t *testing.T) {
	r := newRig(t, true)
	input := filepath.Join(t.TempDir(), "in.jsonl")
	os.WriteFile(input, []byte(`{"source":"Teams","sender":"Ana","title":"Ana","body":"prod outage, need you now"}`+"\n"), 0o600)
	out := r.mustRun("replay", input, "-t", "65%")
	if !strings.Contains(out, "ALERT") || !strings.Contains(out, "1 notifications: 1 alerted") {
		t.Errorf("replay output:\n%s", out)
	}
	if code := r.run("replay", input, "-t", "0.65"); code != 2 {
		t.Errorf("replay with a bad threshold exited %d, want 2", code)
	}
}

func TestAnyCommandRearmsAPersistedBaseline(t *testing.T) {
	r := newRig(t, false)
	r.worker.SetBaseline(30) // what a worker left behind in the store before the machine restarted
	r.env.PersistedFocus = func() bool { return true }
	r.env.Spawn = func() error { r.spawns++; r.serve(); return nil }

	out := r.mustRun("status")
	if r.spawns != 1 || !strings.Contains(out, "30%") {
		t.Fatalf("spawns=%d output=%q; want a worker started for the persisted baseline and reporting it", r.spawns, out)
	}
	r.mustRun("status")
	if r.spawns != 1 {
		t.Errorf("a running worker was spawned again (%d spawns)", r.spawns)
	}
}

func TestStopEndsAPersistedBaselineThatHadNoWorker(t *testing.T) {
	r := newRig(t, false)
	r.worker.SetBaseline(30)
	r.env.PersistedFocus = func() bool { return true }
	r.env.Spawn = func() error { r.serve(); return nil }
	r.mustRun("stop", "--all")
	if st := r.status(); st.Level != 0 {
		t.Fatalf("level = %d after stop --all, want 0", st.Level)
	}
}

func TestNothingIsRearmedWithoutPersistedFocus(t *testing.T) {
	r := newRig(t, false)
	r.env.PersistedFocus = func() bool { return false }
	r.env.Spawn = func() error { r.spawns++; r.serve(); return nil }
	if code := r.run("status"); code != 0 || r.spawns != 0 {
		t.Fatalf("exit %d, %d spawns; want none when nothing was persisted", code, r.spawns)
	}
}

func TestAFailedRearmDoesNotBlockTheCommand(t *testing.T) {
	r := newRig(t, false)
	r.env.PersistedFocus = func() bool { return true }
	r.env.Spawn = func() error { return errors.New("cannot start") }
	if code := r.run("status"); code != 0 || !strings.Contains(r.out.String(), "off") {
		t.Fatalf("exit %d, stdout %q; status should still answer", code, r.out.String())
	}
	if !strings.Contains(r.err.String(), "cannot start") {
		t.Errorf("stderr %q should say why the saved session was not resumed", r.err.String())
	}
}

func TestTheDaemonAndReplayCommandsDoNotRearm(t *testing.T) {
	r := newRig(t, false)
	r.env.PersistedFocus = func() bool { return true }
	r.env.Spawn = func() error { r.spawns++; return nil }
	r.env.Daemon = func(context.Context) error { return nil }
	input := filepath.Join(t.TempDir(), "in.jsonl")
	os.WriteFile(input, []byte(`{"source":"Teams","title":"Ana","body":"hi"}`+"\n"), 0o600)
	r.run("daemon")
	r.run("replay", input)
	if r.spawns != 0 {
		t.Fatalf("%d spawns, want none: a daemon must not spawn itself and replay needs no worker", r.spawns)
	}
}

func TestTheDaemonCommandRunsTheWorkerInTheForeground(t *testing.T) {
	r := newRig(t, false)
	ran := 0
	r.env.Daemon = func(context.Context) error { ran++; return nil }
	if code := r.run("daemon"); code != 0 || ran != 1 {
		t.Fatalf("exit %d, ran %d times", code, ran)
	}
	r.env.Daemon = func(context.Context) error { return errors.New("already running") }
	if code := r.run("daemon"); code != 1 || !strings.Contains(r.err.String(), "already running") {
		t.Fatalf("exit %d, stderr %q; want the failure reported", code, r.err.String())
	}
	r.env.Daemon = nil
	if code := r.run("daemon"); code != 1 {
		t.Errorf("a daemon command with nothing to run exited %d, want 1", code)
	}
}

func TestTheDaemonCommandIsHiddenFromHelp(t *testing.T) {
	r := newRig(t, false)
	r.run("--help")
	if strings.Contains(r.out.String()+r.err.String(), "daemon") {
		t.Error("the internal daemon command shows up in --help")
	}
}

func TestAlertTestShowsASampleInEachStyle(t *testing.T) {
	r := newRig(t, false)
	fake := testkit.NewFakeAlerter()
	r.env.NewAlerter = func() (alert.Alerter, error) { return fake, nil }
	r.env.AlertSources = []string{"Outlook", "Slack"}

	out := r.mustRun("alert", "test")
	shown := fake.Shown()
	if len(shown) != 5 {
		t.Fatalf("%d alerts shown, want a message, a calendar alert, a long message and one per configured source: %+v", len(shown), shown)
	}
	if shown[0].Kind != config.KindMessage || shown[1].Kind != config.KindCalendar {
		t.Errorf("kinds = %v, %v; want message then calendar", shown[0].Kind, shown[1].Kind)
	}
	if len([]rune(shown[2].Body)) < 200 {
		t.Errorf("the third sample should be long enough to need Preview: %q", shown[2].Body)
	}
	if shown[3].Source != "Outlook" || shown[4].Source != "Slack" || shown[3].Kind != config.KindMessage {
		t.Errorf("per-source samples = %+v, %+v", shown[3], shown[4])
	}
	for _, a := range shown {
		if a.Title == "" || a.Body == "" {
			t.Errorf("a sample has no text: %+v", a)
		}
	}
	for _, want := range []string{"Outlook", "Slack", "calendar"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q should name what was shown (%s)", out, want)
		}
	}
}

func TestAlertTestWithoutAnAlerterOrWithABrokenOne(t *testing.T) {
	r := newRig(t, false)
	if code := r.run("alert", "test"); code != 1 || r.err.String() == "" {
		t.Errorf("no alerter: exit %d, stderr %q", code, r.err.String())
	}
	r.env.NewAlerter = func() (alert.Alerter, error) { return nil, errors.New("overlay script missing") }
	if code := r.run("alert", "test"); code != 1 || !strings.Contains(r.err.String(), "overlay script missing") {
		t.Errorf("broken alerter: exit %d, stderr %q", code, r.err.String())
	}
	fake := testkit.NewFakeAlerter()
	fake.FailWith(errors.New("display unavailable"))
	r.env.NewAlerter = func() (alert.Alerter, error) { return fake, nil }
	if code := r.run("alert", "test"); code != 1 || !strings.Contains(r.err.String(), "display unavailable") {
		t.Errorf("failing Show: exit %d, stderr %q", code, r.err.String())
	}
}

func TestAlertTestDoesNotNeedAWorkerAndNeverRearms(t *testing.T) {
	r := newRig(t, false)
	r.env.PersistedFocus = func() bool { return true }
	r.env.Spawn = func() error { r.spawns++; return nil }
	r.env.NewAlerter = func() (alert.Alerter, error) { return testkit.NewFakeAlerter(), nil }
	r.mustRun("alert", "test")
	if r.spawns != 0 {
		t.Errorf("%d spawns; a style preview should not start a worker", r.spawns)
	}
}
