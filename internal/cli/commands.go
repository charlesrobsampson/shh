package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/replay"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/session"
)

type focusCmd struct {
	Timer      string   `short:"t" help:"Session length such as 25m or 1h30m; 0 means until stopped (the default)."`
	Focus      string   `short:"f" help:"Threshold: a percentage like 75% or a tier: off (0), light (35%), medium (65%), deep (90%), lockdown (99%). Default: medium."`
	Note       string   `short:"m" help:"A note about what you are doing."`
	Then       string   `aliases:"clear" help:"What to fall back to when the timer ends: a percentage or a tier (see -f). Default: the layer beneath."`
	EndRoutine bool     `name:"end-routine" help:"If a routine is running, end it and start this session (without it you are asked)."`
	Continue   bool     `name:"continue-routine" help:"If a routine is running, interrupt it: this session starts now, the routine waits, and it carries on when this session ends."`
	After      bool     `name:"after-routine" help:"If a routine is running, queue this session to start when the routine ends (nothing changes now)."`
	Alert      string   `help:"Announce when the timer ends: none (the default), sound or banner. Needs a length."`
	Words      []string `arg:"" optional:"" name:"note" help:"The note, as plain words (same as -m). A leading level or length is read as one, so \"shh focus deep 25m writing\" works; tiers: off (0), light (35%), medium (65%), deep (90%), lockdown (99%)."`
}

func (c *focusCmd) Run(env *Env) error {
	levelText, timerText, words, err := decide.SplitFocusWords(c.Focus, c.Timer, c.Words)
	if err != nil {
		return usagef("%w", err)
	}
	if levelText == "" {
		levelText = "medium"
	}
	if timerText == "" {
		timerText = "0"
	}
	level, err := decide.ParseThreshold(levelText)
	if err != nil {
		return usagef("level: %w", err)
	}
	duration, err := decide.ParseDuration(timerText)
	if err != nil {
		return usagef("length: %w", err)
	}
	var then *int
	if c.Then != "" {
		v, err := decide.ParseThreshold(c.Then)
		if err != nil {
			return usagef("--then: %w", err)
		}
		then = &v
	}
	note := c.Note
	if joined := strings.Join(words, " "); joined != "" {
		if note != "" {
			return usagef("give the note once, either with -m or as plain words")
		}
		note = joined
	}
	if err := session.CheckAlert(c.Alert); err != nil {
		return usagef("--alert: %w", err)
	}
	if c.Alert != "" && c.Alert != session.AlertNone && duration == 0 {
		return usagef("--alert needs a length, since a session with no timer never ends: for example shh 25m --alert banner")
	}
	if err := env.ensureWorker(); err != nil {
		return err
	}
	view, err := env.client().FocusAlert(level, duration, then, note, c.Alert)
	if errors.Is(err, session.ErrRoutineRunning) {
		if n := b2i(c.EndRoutine) + b2i(c.Continue) + b2i(c.After); n > 1 {
			return usagef("--end-routine, --continue-routine and --after-routine are three different answers: use one")
		}
		choice, askErr := env.routineInTheWay(err, c.EndRoutine, c.Continue, c.After, "start this session", true)
		if askErr != nil {
			return askErr
		}
		switch choice {
		case choiceEnd:
			view, err = env.client().FocusAlert(level, duration, then, note, c.Alert)
		case choiceContinue:
			view, err = env.client().FocusAfterRoutine(level, duration, then, note, c.Alert)
		case choiceQueue:
			view, err = env.client().FocusQueued(level, duration, then, note, c.Alert)
			if err == nil {
				fmt.Fprintf(env.Stdout, "Queued: this session starts when the routine ends. Drop it with: shh routine unqueue\n")
			}
		default:
			return nil
		}
	}
	if err != nil {
		return err
	}
	renderStatus(env.Stdout, view, true)
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type stopCmd struct {
	All        bool `help:"Also clear the baseline, ending all focus."`
	EndRoutine bool `name:"end-routine" help:"If a routine is running, end it too (without it you are asked)."`
}

func (c *stopCmd) Run(env *Env) error {
	view, err := env.client().Stop(c.All)
	if errors.Is(err, session.ErrNotRunning) {
		fmt.Fprintln(env.Stdout, "shh is not running; nothing to stop")
		return nil
	}
	if errors.Is(err, session.ErrRoutineRunning) {
		choice, askErr := env.routineInTheWay(err, c.EndRoutine, false, false, "stop", false)
		if askErr != nil || choice != choiceEnd {
			return askErr
		}
		// Ending the routine closed the session it was running, which is what stop asked for; only --all has more (the baseline) to do.
		if c.All {
			view, err = env.client().Stop(true)
		} else {
			view, err = env.client().Status()
		}
	}
	if err != nil {
		return err
	}
	renderStatus(env.Stdout, view, true)
	return nil
}

type baselineCmd struct {
	Level string `arg:"" name:"level" help:"A percentage like 30% or a tier: off (0), light (35%), medium (65%), deep (90%), lockdown (99%)."`
}

func (c *baselineCmd) Run(env *Env) error {
	level, err := decide.ParseThreshold(c.Level)
	if err != nil {
		return usagef("baseline: %w", err)
	}
	if err := env.ensureWorker(); err != nil {
		return err
	}
	view, err := env.client().Baseline(level)
	if err != nil {
		return err
	}
	// Say what was set before showing the level in force: a session running on top of the baseline keeps the level at its own
	// value, and reading "Focus is on at 65%" straight after typing 30% looks like the command did the wrong thing.
	if level == 0 {
		fmt.Fprintln(env.Stdout, "Baseline cleared.")
	} else {
		fmt.Fprintf(env.Stdout, "Baseline set to %s.\n", decide.FormatLevel(level))
	}
	if view.Level != level && len(view.Layers) > 0 {
		top := view.Layers[len(view.Layers)-1]
		until := "until it is stopped"
		if top.Timed {
			until = "until it ends in " + decide.FormatRemaining(top.RemainingSecs)
		}
		fmt.Fprintf(env.Stdout, "A session at %s is running on top of it, so focus stays at %s %s.\n", decide.FormatLevel(top.Percent), decide.FormatLevel(view.Level), until)
	}
	renderStatus(env.Stdout, view, true)
	return nil
}

type statusCmd struct {
	JSON  bool `help:"Print machine-readable JSON."`
	Short bool `help:"Print a one-line summary for a prompt or status bar; nothing when focus is off."`
}

func (c *statusCmd) Run(env *Env) error {
	view, err := env.client().Status()
	running := true
	if errors.Is(err, session.ErrNotRunning) {
		view, running, err = session.StatusView{Layers: []session.LayerView{}, Now: time.Now()}, false, nil
	}
	if err != nil {
		return err
	}
	if running && !c.Short {
		env.warnIfStale(view)
	}
	switch {
	case c.JSON:
		return printJSON(env.Stdout, view, running)
	case c.Short:
		if line := shortStatus(view, env.Label); line != "" {
			fmt.Fprintln(env.Stdout, line)
		}
	default:
		renderStatus(env.Stdout, view, running)
	}
	return nil
}

type pauseCmd struct{}

func (c *pauseCmd) Run(env *Env) error {
	view, err := env.client().Pause()
	if err != nil {
		return err
	}
	renderStatus(env.Stdout, view, true)
	return nil
}

type resumeCmd struct{}

func (c *resumeCmd) Run(env *Env) error {
	view, err := env.client().Resume()
	if err != nil {
		return err
	}
	renderStatus(env.Stdout, view, true)
	return nil
}

type uiCmd struct {
	Tab string `help:"The tab to open on: focus, notifications, train or routines." enum:",focus,notifications,train,routines" default:""`
}

// Run opens the terminal UI on a terminal; anywhere else, or without a UI, it prints the status so `shh` is still useful in a
// pipe or a script.
func (c *uiCmd) Run(env *Env) error {
	if env.IsTerminal && env.UI != nil {
		return env.UI(c.Tab)
	}
	if env.IsTerminal && env.TUI != nil {
		return env.TUI()
	}
	return (&statusCmd{}).Run(env)
}

type alertCmd struct {
	Test   alertTestCmd   `cmd:"" help:"Show a sample alert in each configured style."`
	Colors alertColorsCmd `cmd:"" help:"Show the 8-bit ANSI palette that banner colors are chosen from, with each color's number."`
}

type alertTestCmd struct{}

// demoNotification is the notification id the message samples carry: negative, so the Flag button shows (and shows its pressed state)
// without there being a stored notification to mark.
const demoNotification = -1

// Run shows one sample per kind of alert and one per app that has its own style, so changes to config.json can be previewed.
// The first sample carries Calculator's bundle id: click it to check that clicking opens the source app.
func (c *alertTestCmd) Run(env *Env) error {
	// The samples are shown by the running worker (started if need be), so they share the screen with its other banners instead of
	// being laid out by this short-lived process.
	if err := env.ensureWorker(); err != nil {
		return err
	}
	client := env.client()
	samples := []alert.Alert{
		{Kind: config.KindMessage, Source: "Calculator", BundleID: "com.apple.calculator", NotificationID: demoNotification, Title: "Sample message", Body: "A message alert in the default style. Press Go to to open Calculator."},
		{Kind: config.KindMessage, Source: "Calculator", BundleID: "com.apple.calculator", NotificationID: demoNotification, Title: "Sample long message",
			Body: "A long message, so the Preview button has something to show. The banner cuts this text off after a few lines; press Preview to read all of it without dismissing the alert, Go to to open Calculator, or Dismiss to close it. Long messages are capped even in the preview, because alerts are glances and not documents."},
		{Kind: config.KindCalendar, Source: "Calendar", Title: "Sample meeting", Body: "A calendar alert: Starts at 10:30"},
	}
	for _, source := range env.AlertSources {
		samples = append(samples, alert.Alert{Kind: config.KindMessage, Source: source, Title: "Sample from " + source, Body: "A message alert styled for " + source})
	}
	for _, sample := range samples {
		if err := client.ShowAlert(sample); err != nil {
			return fmt.Errorf("showing the %s sample: %w", sample.Kind, err)
		}
		fmt.Fprintf(env.Stdout, "showed a %s alert from %s\n", sample.Kind, sample.Source)
	}
	return nil
}

type daemonCmd struct{}

func (c *daemonCmd) Run(env *Env) error {
	if env.Daemon == nil {
		return errors.New("this build cannot run the worker")
	}
	err := env.Daemon(context.Background())
	if errors.Is(err, session.ErrAlreadyRunning) {
		// Not a failure: the login agent and a worker started by hand can both try, and the one that comes second just steps aside. A
		// failing exit would have launchd start it again and again.
		fmt.Fprintln(env.Stderr, "shh: a worker is already running, so this one is not needed.")
		return nil
	}
	return err
}

type replayCmd struct {
	File      string `arg:"" type:"existingfile" help:"JSON lines with source, sender, title, subtitle, body, category, and optionally at (RFC 3339) and label (0-100)."`
	Threshold string `short:"t" default:"65%" help:"Focus level to decide at: a percentage like 75% or a tier (off, light, medium, deep, lockdown)."`
}

func (c *replayCmd) Run(env *Env) error {
	threshold, err := decide.ParseThreshold(c.Threshold)
	if err != nil {
		return usagef("-t: %w", err)
	}
	r, err := rules.Load(filepath.Join(env.ConfigDir, "rules.json"))
	if err != nil {
		return err
	}
	embedder, _, err := classify.ResolveEmbedder(filepath.Join(env.ConfigDir, "models"), classify.DefaultModelSHA256)
	if errors.Is(err, classify.ErrNoEmbeddingModel) {
		fmt.Fprintln(env.Stderr, "note:", err, "- running without the embedding layer")
		embedder = nil
	} else if err != nil {
		return err
	}
	in, err := os.Open(c.File)
	if err != nil {
		return err
	}
	defer in.Close()
	return replay.Run(in, env.Stdout, replay.Options{Threshold: threshold, Rules: r, Embedder: embedder})
}

type menubarCmd struct{}

// Run is the menu bar item's own process: it shows the worker's status until the worker is gone. It never starts a worker itself.
func (c *menubarCmd) Run(env *Env) error {
	if env.Menubar == nil {
		return errors.New("this build has no menu bar item")
	}
	return env.Menubar(context.Background())
}
