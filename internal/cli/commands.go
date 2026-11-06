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
	Timer string   `short:"t" default:"0" help:"Session length such as 25m or 1h30m; 0 means until stopped."`
	Focus string   `short:"f" default:"medium" help:"Threshold: a percentage like 75% or a tier (off, light, medium, deep, lockdown)."`
	Note  string   `short:"m" help:"A note about what you are doing."`
	Then  string   `aliases:"clear" help:"What to fall back to when the timer ends: a percentage, a tier or off. Default: the layer beneath."`
	Words []string `arg:"" optional:"" name:"note" help:"The note, as plain words (same as -m)."`
}

func (c *focusCmd) Run(env *Env) error {
	level, err := decide.ParseThreshold(c.Focus)
	if err != nil {
		return usagef("-f: %w", err)
	}
	duration, err := decide.ParseDuration(c.Timer)
	if err != nil {
		return usagef("-t: %w", err)
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
	if words := strings.Join(c.Words, " "); words != "" {
		if note != "" {
			return usagef("give the note once, either with -m or as plain words")
		}
		note = words
	}
	if err := env.ensureWorker(); err != nil {
		return err
	}
	view, err := env.client().Focus(level, duration, then, note)
	if err != nil {
		return err
	}
	renderStatus(env.Stdout, view, true)
	return nil
}

type stopCmd struct {
	All bool `help:"Also clear the baseline, ending all focus."`
}

func (c *stopCmd) Run(env *Env) error {
	view, err := env.client().Stop(c.All)
	if errors.Is(err, session.ErrNotRunning) {
		fmt.Fprintln(env.Stdout, "shh is not running; nothing to stop")
		return nil
	}
	if err != nil {
		return err
	}
	renderStatus(env.Stdout, view, true)
	return nil
}

type baselineCmd struct {
	Level string `arg:"" name:"level" help:"A percentage like 30%, a tier (light, medium, deep, lockdown) or off."`
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

type uiCmd struct{}

// Run opens the terminal UI on a terminal; anywhere else, or without a UI, it prints the status so `shh` is still useful in a
// pipe or a script.
func (c *uiCmd) Run(env *Env) error {
	if env.IsTerminal && env.TUI != nil {
		return env.TUI()
	}
	return (&statusCmd{}).Run(env)
}

type alertCmd struct {
	Test alertTestCmd `cmd:"" help:"Show a sample alert in each configured style."`
}

type alertTestCmd struct{}

// Run shows one sample per kind of alert and one per app that has its own style, so changes to config.toml can be previewed.
// The first sample carries Calculator's bundle id: click it to check that clicking opens the source app.
func (c *alertTestCmd) Run(env *Env) error {
	if env.NewAlerter == nil {
		return errors.New("this build cannot show alerts")
	}
	alerter, err := env.NewAlerter()
	if err != nil {
		return err
	}
	samples := []alert.Alert{
		{Kind: config.KindMessage, Source: "Teams", BundleID: "com.apple.calculator", Title: "Sample message", Body: "A message alert in the default style. Click it to open Calculator."},
		{Kind: config.KindCalendar, Source: "Calendar", Title: "Sample meeting", Body: "A calendar alert: Starts at 10:30"},
	}
	for _, source := range env.AlertSources {
		samples = append(samples, alert.Alert{Kind: config.KindMessage, Source: source, Title: "Sample from " + source, Body: "A message alert styled for " + source})
	}
	for _, sample := range samples {
		if err := alerter.Show(sample); err != nil {
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
	return env.Daemon(context.Background())
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
	r, err := rules.Load(filepath.Join(env.ConfigDir, "rules.toml"))
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
