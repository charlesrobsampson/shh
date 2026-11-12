package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
)

// workerCmd is the commands for the background worker: the process that holds banners back, moves routines along and starts them from
// their triggers. shh starts one by itself when a command needs it; these are for the times you want to say so.
type workerCmd struct {
	Start   workerStartCmd   `cmd:"" help:"Start the worker in the background (nothing happens if it is running)."`
	Stop    workerStopCmd    `cmd:"" help:"Shut the worker down (asks first if focus or a routine is on, since banners would come through)."`
	Restart workerRestartCmd `cmd:"" help:"Stop the worker and start a new one, to pick up a new build of shh."`
	Status  workerStatusCmd  `cmd:"" help:"Say whether the worker is running, since when, and what it is keeping going."`

	Install   workerInstallCmd   `cmd:"" help:"Start the worker at every login (a LaunchAgent), so routine triggers fire without anything to remember."`
	Uninstall workerUninstallCmd `cmd:"" help:"Stop starting the worker at login (removes the LaunchAgent)."`
}

type workerInstallCmd struct{}

// Run has launchd start the worker at login, and now. It changes one file, ~/Library/LaunchAgents/app.shh.worker.plist, and loads it.
func (c *workerInstallCmd) Run(env *Env) error {
	if env.Agent == nil {
		return errors.New("this build cannot install a login agent")
	}
	running := false
	if _, err := env.client().WorkerInfo(); err == nil {
		running = true
	}
	if err := env.Agent.Install(); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Installed %s: the shh worker now starts at every login, and starts again if it crashes.\n", env.Agent.Path())
	if running {
		fmt.Fprintln(env.Stdout, "A worker was already running, so the new one stepped aside; launchd takes over at the next login. (shh worker stop and then shh worker start is not needed.)")
	} else {
		fmt.Fprintln(env.Stdout, "It is starting now. shh worker stop stops it until the next login; shh worker uninstall removes the agent.")
	}
	return nil
}

type workerUninstallCmd struct{}

func (c *workerUninstallCmd) Run(env *Env) error {
	if env.Agent == nil {
		return errors.New("this build cannot install a login agent")
	}
	was, err := env.Agent.Uninstall()
	if err != nil {
		return err
	}
	if !was {
		fmt.Fprintln(env.Stdout, "The login agent was not installed: nothing to remove.")
		return nil
	}
	fmt.Fprintln(env.Stdout, "Removed the login agent. The worker no longer starts at login (it still starts by itself when a command needs it).")
	return nil
}

type workerStartCmd struct{}

func (c *workerStartCmd) Run(env *Env) error {
	if info, err := env.client().WorkerInfo(); err == nil {
		fmt.Fprintf(env.Stdout, "The shh worker is already running (pid %d, up %s).\n", info.PID, upFor(info))
		env.warnIfStaleBuild(info.Build)
		return nil
	} else if !errors.Is(err, session.ErrNotRunning) {
		return err
	}
	return env.startWorker()
}

// startWorker starts a worker and says which.
func (e *Env) startWorker() error {
	if _, err := session.Ensure(e.client(), e.Spawn, e.SpawnWait); err != nil {
		return err
	}
	info, err := e.client().WorkerInfo()
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "Started the shh worker (pid %d).\n", info.PID)
	return nil
}

type workerStopCmd struct {
	Force bool `short:"f" help:"Stop it even if focus or a routine is on, without asking."`
}

func (c *workerStopCmd) Run(env *Env) error {
	info, err := env.client().WorkerInfo()
	if errors.Is(err, session.ErrNotRunning) {
		fmt.Fprintln(env.Stdout, "The shh worker is not running.")
		return nil
	}
	if err != nil {
		return err
	}
	if !c.Force {
		if lost := whatStoppingLoses(info); lost != "" {
			if !env.IsTerminal || env.Stdin == nil {
				return fmt.Errorf("%s; add --force to stop it anyway", lost)
			}
			fmt.Fprintf(env.Stdout, "%s. Stop the worker anyway? [y/N] ", lost)
			answer, _ := bufio.NewReader(env.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
				fmt.Fprintln(env.Stdout, "Left it running.")
				return nil
			}
		}
	}
	return env.stopWorker(info.PID)
}

// whatStoppingLoses says what would stop working if the worker went now, or "" if nothing would.
func whatStoppingLoses(info session.WorkerInfo) string {
	var parts []string
	if info.Level > 0 {
		parts = append(parts, fmt.Sprintf("focus is on at %s, so notification banners would come through while it is down", decide.FormatLevel(info.Level)))
	}
	if info.Routine != "" {
		parts = append(parts, fmt.Sprintf("the routine %s is running and would stop moving on", info.Routine))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", and ") + " (focus and the routine are saved, and carry on when a worker starts again)"
}

// stopWorker asks the worker to go and waits until it has.
func (e *Env) stopWorker(pid int) error {
	if err := e.client().WorkerQuit(); err != nil {
		return err
	}
	if err := e.client().WaitGone(pid, e.SpawnWait); err != nil {
		return err
	}
	fmt.Fprintln(e.Stdout, "Stopped the shh worker. Start it again with: shh worker start")
	return nil
}

type workerRestartCmd struct{}

func (c *workerRestartCmd) Run(env *Env) error {
	info, err := env.client().WorkerInfo()
	switch {
	case err == nil:
		// A restart is brief and what the worker was doing is saved, so it is not asked about.
		if err := env.client().WorkerQuit(); err != nil {
			return err
		}
		if err := env.client().WaitGone(info.PID, env.SpawnWait); err != nil {
			return err
		}
	case !errors.Is(err, session.ErrNotRunning):
		return err
	}
	return env.startWorker()
}

type workerStatusCmd struct{}

func (c *workerStatusCmd) Run(env *Env) error {
	info, err := env.client().WorkerInfo()
	if errors.Is(err, session.ErrNotRunning) {
		fmt.Fprintln(env.Stdout, "The shh worker is not running. Start it with: shh worker start")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "The shh worker is running (pid %d, up %s).\n", info.PID, upFor(info))
	if info.Level > 0 {
		fmt.Fprintf(env.Stdout, "  focus:    on at %s\n", decide.FormatLevel(info.Level))
	} else {
		fmt.Fprintln(env.Stdout, "  focus:    off")
	}
	if info.Routine != "" {
		fmt.Fprintf(env.Stdout, "  routine:  %s is running\n", info.Routine)
	} else {
		fmt.Fprintln(env.Stdout, "  routine:  none running")
	}
	if env.Agent != nil {
		if env.Agent.Installed() {
			fmt.Fprintln(env.Stdout, "  at login: starts by itself (shh worker uninstall removes that)")
		} else {
			fmt.Fprintln(env.Stdout, "  at login: not started (shh worker install makes it)")
		}
	}
	switch info.Triggers {
	case 0:
		fmt.Fprintln(env.Stdout, "  triggers: none switched on")
	case 1:
		fmt.Fprintln(env.Stdout, "  triggers: 1 switched on (the worker stays up to start it)")
	default:
		fmt.Fprintf(env.Stdout, "  triggers: %d switched on (the worker stays up to start them)\n", info.Triggers)
	}
	env.warnIfStaleBuild(info.Build)
	return nil
}

// upFor is how long the worker has been up, as 2h14m.
func upFor(info session.WorkerInfo) string {
	return decide.FormatRemaining(max(0, int(info.Now.Sub(info.Started)/time.Second)))
}

// warnIfStaleBuild says so when the worker is from a different build than this command.
func (e *Env) warnIfStaleBuild(build string) {
	if e.Build != "" && build != e.Build {
		fmt.Fprintln(e.Stdout, "  note: it is running an older build than this shh. Pick up the new one with: shh worker restart")
	}
}
