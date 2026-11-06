// Package cli is the shh command line: each command talks to the worker over its socket and prints the result.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/alecthomas/kong"

	"github.com/charlesrobsampson/shh/internal/alert"

	"github.com/charlesrobsampson/shh/internal/session"
)

// Env is everything the commands need from the outside world, so tests can substitute all of it.
type Env struct {
	Stdout, Stderr io.Writer
	// SocketPath is where the worker listens.
	SocketPath string
	// ConfigDir holds rules.toml and the models directory.
	ConfigDir string
	// Version is printed by --version.
	Version string
	// Label starts the --short status, for example "shh".
	Label string
	// IsTerminal says whether stdout is a terminal; bare `shh` opens the UI only then.
	IsTerminal bool
	// TUI opens the terminal UI. Nil when it is not available.
	TUI func() error
	// Spawn starts a worker in the background when none is running. Nil when that is not possible.
	Spawn func() error
	// SpawnWait is how long to wait for a spawned worker to answer; 5 seconds when zero.
	SpawnWait time.Duration
	// PersistedFocus reports whether a focus level was left saved by an earlier worker, for example across a reboot. Nil means no.
	PersistedFocus func() bool
	// NewAlerter builds the alerter that `shh alert test` previews styles with. Nil when none is available.
	NewAlerter func() (alert.Alerter, error)
	// AlertSources names the apps that have a style override in the configuration, so the preview can show each.
	AlertSources []string
	// Daemon runs the worker in the foreground until ctx ends; it is what `shh daemon` (the spawned process) does.
	Daemon func(ctx context.Context) error
}

type cli struct {
	Version kong.VersionFlag `help:"Print the version and exit."`

	UI       uiCmd       `cmd:"" default:"1" help:"Open the terminal UI (what bare shh does on a terminal)."`
	Focus    focusCmd    `cmd:"" aliases:"f" help:"Start a focus session, an escalation on top of any baseline."`
	Stop     stopCmd     `cmd:"" aliases:"s" help:"End the current focus escalation (--all also clears the baseline)."`
	Baseline baselineCmd `cmd:"" help:"Set or clear the standing focus level that applies whenever no escalation does."`
	Status   statusCmd   `cmd:"" help:"Show the focus levels in force and the time left."`
	Pause    pauseCmd    `cmd:"" help:"Pause the countdown of the current timed session."`
	Resume   resumeCmd   `cmd:"" help:"Resume a paused countdown."`
	Alert    alertCmd    `cmd:"" help:"Alert tools."`
	Replay   replayCmd   `cmd:"" help:"Run the classifier over a file of notifications and print each decision."`
	Daemon   daemonCmd   `cmd:"" hidden:"" help:"Run the background worker (started by shh itself)."`
}

type exitRequest int

// usageError marks mistakes in what the user typed, which exit with code 2.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error { return usageError{fmt.Errorf(format, args...)} }

// Run executes one shh command line and returns the process exit code: 0 for success, 1 for a failure while doing the work,
// 2 for a command line that could not be used.
func Run(args []string, env Env) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if request, ok := r.(exitRequest); ok {
				code = int(request)
				return
			}
			panic(r)
		}
	}()

	var root cli
	parser, err := kong.New(&root,
		kong.Name("shh"),
		kong.Description("Keeps notifications quiet while you focus, and lets through only what matters."),
		kong.Writers(env.Stdout, env.Stderr),
		kong.Vars{"version": "shh " + env.Version},
		kong.Exit(func(code int) { panic(exitRequest(code)) }),
		kong.Bind(&env),
	)
	if err != nil {
		fmt.Fprintln(env.Stderr, "shh:", err)
		return 1
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintln(env.Stderr, "shh: error:", err)
		return 2
	}
	if name := ctx.Command(); name != "daemon" && name != "replay <file>" && name != "alert test" {
		env.rearmPersistedFocus()
	}
	if err := ctx.Run(); err != nil {
		fmt.Fprintln(env.Stderr, "shh:", err)
		var usage usageError
		if errors.As(err, &usage) {
			return 2
		}
		return 1
	}
	return 0
}

func (e *Env) client() *session.Client { return session.NewClient(e.SocketPath) }

// rearmPersistedFocus starts a worker when an earlier one left a focus level saved (a baseline survives a reboot, the worker does
// not). A failure is reported but does not stop the command the user actually typed.
func (e *Env) rearmPersistedFocus() {
	if e.PersistedFocus == nil || e.Spawn == nil || !e.PersistedFocus() {
		return
	}
	if err := e.ensureWorker(); err != nil {
		fmt.Fprintln(e.Stderr, "shh: could not resume your saved focus session:", err)
	}
}

// ensureWorker makes sure a worker is answering, starting one if none is running and a spawner is available.
func (e *Env) ensureWorker() error {
	client := e.client()
	_, err := client.Status()
	if err == nil || !errors.Is(err, session.ErrNotRunning) {
		return err
	}
	if e.Spawn == nil {
		return fmt.Errorf("%w, and a worker cannot be started from here", err)
	}
	if err := e.Spawn(); err != nil {
		return fmt.Errorf("starting the worker: %w", err)
	}
	wait := e.SpawnWait
	if wait == 0 {
		wait = 5 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if _, err := client.Status(); err == nil {
			return nil
		} else if !errors.Is(err, session.ErrNotRunning) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the worker did not start within %v", wait)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
