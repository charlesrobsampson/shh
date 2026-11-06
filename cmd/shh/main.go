package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"

	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/replay"
	"github.com/charlesrobsampson/shh/internal/rules"
)

var version = "dev"

type cli struct {
	Version kong.VersionFlag `help:"Print the version and exit."`

	Replay replayCmd `cmd:"" help:"Run the classifier over a file of notifications and print each decision."`
}

type replayCmd struct {
	File      string `arg:"" type:"existingfile" help:"JSON lines with source, sender, title, subtitle, body, category, and optionally at (RFC 3339) and label (0-100)."`
	Threshold string `short:"t" default:"65%" help:"Focus level to decide at: a percentage like 75% or a tier (off, light, medium, deep, lockdown)."`
}

func (c *replayCmd) Run() error {
	threshold, err := decide.ParseThreshold(c.Threshold)
	if err != nil {
		return err
	}
	configPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	r, err := rules.Load(filepath.Join(cfg.Paths.Dir, "rules.toml"))
	if err != nil {
		return err
	}
	embedder, _, err := classify.ResolveEmbedder(filepath.Join(cfg.Paths.Dir, "models"), classify.DefaultModelSHA256)
	if errors.Is(err, classify.ErrNoEmbeddingModel) {
		fmt.Fprintln(os.Stderr, "note:", err, "- running without the embedding layer")
		embedder = nil
	} else if err != nil {
		return err
	}
	in, err := os.Open(c.File)
	if err != nil {
		return err
	}
	defer in.Close()
	return replay.Run(in, os.Stdout, replay.Options{Threshold: threshold, Rules: r, Embedder: embedder})
}

func main() {
	var cmd cli
	ctx := kong.Parse(&cmd,
		kong.Name("shh"),
		kong.Description("Keeps notifications quiet while you focus, and lets through only what matters."),
		kong.Vars{"version": "shh " + version},
	)
	ctx.FatalIfErrorf(ctx.Run())
}
