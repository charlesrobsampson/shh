// Package cli is the shh command line: each command talks to the worker over its socket and prints the result.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/doctor"
	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

// Env is everything the commands need from the outside world, so tests can substitute all of it.
type Env struct {
	Stdout, Stderr io.Writer
	// SocketPath is where the worker listens.
	SocketPath string
	// ConfigDir holds rules.json and the models directory.
	ConfigDir string
	// Version is printed by --version.
	Version string
	// Build identifies this executable, for comparing with the worker's (session.BuildID); empty disables the check.
	Build string
	// Label starts the --short status, for example "shh".
	Label string
	// IsTerminal says whether stdout is a terminal; bare `shh` opens the UI only then.
	IsTerminal bool
	// Stdin is where questions are answered from; nil means they cannot be asked.
	Stdin io.Reader
	// Now is the clock, for showing when a routine starts next; time.Now when nil.
	Now func() time.Time
	// ConfigPath is config.json, which init writes and config path prints; ConfigShow renders the configuration in force.
	ConfigPath string
	ConfigShow func() ([]byte, error)
	// RoutinesPath is the saved routines file, which the list and example commands read without a worker.
	RoutinesPath string
	// TUI opens the terminal UI. Nil when it is not available.
	TUI func() error
	// UI opens the terminal UI on a tab (focus, notifications, train, routines). Nil when it is not available.
	UI func(tab string) error
	// WithStore opens the database for reading history (the runs of routines), calls do with it and closes it again. Nil when there is no
	// database to read.
	WithStore func(do func(*store.Store) error) error
	// Doctor says how to look at the machine for shh doctor (the worker probe is added by the command). Nil when it is not available.
	Doctor *doctor.Env
	// Retention is how long notifications are kept, from config.json, for shh purge. Nil when it is not available.
	Retention func() store.Retention
	// Notifications reads the macOS notification records, for shh capture. Nil when it is not available.
	Notifications ingest.Source
	// Guide opens the guide in the terminal UI, at a section (its slug, or empty) and with a search made (or empty). Nil when it is not
	// available, and the guide is printed instead.
	Guide func(section, search string) error
	// Spawn starts a worker in the background when none is running. Nil when that is not possible.
	Spawn func() error
	// SpawnWait is how long to wait for a spawned worker to answer; 5 seconds when zero.
	SpawnWait time.Duration
	// PersistedFocus reports whether a focus level was left saved by an earlier worker, for example across a reboot. Nil means no.
	PersistedFocus func() bool
	// AlertSources names the apps that have a style override in the configuration, so the preview can show each.
	AlertSources []string
	// Daemon runs the worker in the foreground until ctx ends; it is what `shh daemon` (the spawned process) does.
	Daemon      func(ctx context.Context) error
	staleWarned bool
	// Menubar runs the menu bar item in the foreground until ctx ends or the worker is gone; it is what `shh menubar` (started by
	// the worker) does. Nil when the menu bar is not available.
	Menubar func(ctx context.Context) error
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
	Routine  routineCmd  `cmd:"" help:"Saved sequences of focus sessions, such as a pomodoro (shh guide routine-commands explains them)."`
	Worker   workerCmd   `cmd:"" help:"Start, stop and check the background worker (shh daemon runs it in the foreground)."`
	Ls       lsCmd       `cmd:"" help:"List stored notifications, newest first (read-only): --source, --since, --status, --min-score, --flagged."`
	Purge    purgeCmd    `cmd:"" help:"Delete stored notifications past their time to live (--all: every one), after asking."`
	Capture  captureCmd  `cmd:"" help:"Log raw notifications to a file under private/, to build rules from real examples."`
	Rules    rulesCmd    `cmd:"" help:"Check what the rules did not understand (shh rules review)."`
	Train    trainCmd    `cmd:"" help:"Label notifications (ids from shh ls), undo, reset or roll back the model, export or import labels."`
	Doctor   doctorCmd   `cmd:"" help:"Check the setup: config, database, notification access, model, permissions and the worker."`
	Init     initCmd     `cmd:"" help:"Set shh up: write config.json with every option at its default, explained."`
	Config   configCmd   `cmd:"" help:"Where the configuration is, what is in force, and the documented defaults."`
	Guide    guideCmd    `cmd:"" help:"The guide to the terminal UI, routines and commands: open it, jump to a section, or search it."`
	Alert    alertCmd    `cmd:"" help:"Alert tools."`
	Replay   replayCmd   `cmd:"" help:"Run the classifier over a file of notifications and print each decision."`
	Daemon   daemonCmd   `cmd:"" help:"Run the worker in the foreground. shh starts it by itself when it is needed; run this (or shh worker start) at login to keep routines' triggers firing (shh guide triggers)."`
	Menubar  menubarCmd  `cmd:"" hidden:"" help:"Run the menu bar item (started by the worker)."`
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

	args = expandShorthand(args)
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
	// A saved focus session is picked up again by the next command, by starting a worker. Not for the commands that are about the worker
	// itself: a stop would be undone by the very next one, and start and restart start one anyway.
	if name := ctx.Command(); name != "daemon" && name != "menubar" && name != "doctor" && name != "replay <file>" && name != "alert test" && !strings.HasPrefix(name, "worker") {
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

// expandShorthand turns "shh 30%", "shh 40% 5m" and "shh deep" into "shh focus ...", the quick way to start a session. Anything
// that does not start with a level or a length is left alone, so every full command and flag still works as written, and bare
// "shh" still opens the UI.
func expandShorthand(args []string) []string {
	if len(args) == 0 {
		return args
	}
	first := args[0]
	if decide.IsPercentWord(first) || decide.IsDurationWord(first) || isTierName(first) {
		return append([]string{"focus"}, args...)
	}
	return args
}

// isTierName is true for the names that stand for a level, except "off", which would make "shh off" mean something other than
// what it says.
func isTierName(word string) bool {
	switch word {
	case "light", "medium", "deep", "lockdown":
		return true
	}
	return false
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

// warnIfStale says so when the worker running is not from this executable. A worker keeps running after shh is rebuilt or
// upgraded and goes on serving the old code (including old banner scripts), so a change can look as if it did nothing.
func (e *Env) warnIfStale(view session.StatusView) {
	// A worker that reports no build at all predates the report, so it is older than any build that can tell: that counts as stale too.
	if e.Build == "" || view.Build == e.Build || e.staleWarned {
		return
	}
	e.staleWarned = true
	fmt.Fprintln(e.Stderr, "shh: note: the running worker is from a different build than this command, so it is still running the old code.\n"+
		"     Pick up the new build with: shh worker restart")
}

// ensureWorker makes sure a worker is answering, starting one if none is running and a spawner is available.
func (e *Env) ensureWorker() error {
	view, err := session.Ensure(e.client(), e.Spawn, e.SpawnWait)
	if err == nil {
		e.warnIfStale(view)
	}
	return err
}
