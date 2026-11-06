package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/charlesrobsampson/shh/internal/cli"
	"github.com/charlesrobsampson/shh/internal/config"
)

var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], realEnv()))
}

func realEnv() cli.Env {
	env := cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Version: version, Label: "shh", IsTerminal: isTerminal(os.Stdout)}
	path, err := config.DefaultPath()
	if err == nil {
		var cfg config.Config
		if cfg, err = config.Load(path); err == nil {
			env.SocketPath, env.ConfigDir, env.Label = cfg.Paths.Socket, cfg.Paths.Dir, cfg.Menubar.Label
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "shh: cannot read the configuration:", err)
		os.Exit(1)
	}
	// Starting the worker in the background arrives with `shh daemon` (task 28).
	env.Spawn = func() error { return errors.New("shh cannot start its worker yet") }
	return env
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
