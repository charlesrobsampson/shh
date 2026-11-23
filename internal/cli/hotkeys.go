package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/hotkey"
)

type hotkeysCmd struct {
	Test   hotkeysTestCmd   `cmd:"" help:"Hold the banner keyboard shortcuts for a while and print each key you press, to check that they work."`
	Helper hotkeysHelperCmd `cmd:"" hidden:"" help:"Hold the keys for the worker (started by it)."`
}

type hotkeysTestCmd struct {
	For time.Duration `short:"t" default:"30s" help:"How long to listen (30s, 2m). Ctrl-C stops it sooner."`
}

// Run holds the configured shortcuts the way the worker will while banners are up, so it shows whether macOS gives them and whether they
// arrive over the app you are in. It needs no worker and no banner.
func (c *hotkeysTestCmd) Run(env *Env) error {
	if env.Hotkeys == nil {
		return hotkey.ErrUnsupported
	}
	s := env.Shortcuts
	if !s.Enabled {
		return errors.New("the keyboard shortcuts are switched off (alert.shortcuts.enabled in config.json)")
	}
	presses := make(chan string, 16)
	err := env.Hotkeys.Register(hotkey.Binding{Modifier: s.Modifier, Keys: s.Keys}, func(action string) {
		select {
		case presses <- action:
		default:
		}
	})
	defer env.Hotkeys.Unregister()
	var refused map[string]string
	var registerErr *hotkey.RegisterError
	switch {
	case errors.As(err, &registerErr):
		refused = registerErr.Refused
		fmt.Fprintln(env.Stderr, "shh: warning:", err)
		if len(refused) >= countKeys(s) {
			return errors.New("none of the shortcuts could be registered")
		}
	case err != nil:
		return err
	}

	fmt.Fprintf(env.Stdout, "Holding %s plus:\n", s.Modifier)
	for _, action := range config.ShortcutActions {
		if key := s.Keys[action]; key != "" && refused[action] == "" {
			fmt.Fprintf(env.Stdout, "  %-12s %s\n", action, key)
		}
	}
	fmt.Fprintf(env.Stdout, "Press them over any app; listening for %s (Ctrl-C stops).\n", c.For)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	timer := time.NewTimer(c.For)
	defer timer.Stop()
	count := 0
	summarize := func() {
		noun := "presses"
		if count == 1 {
			noun = "press"
		}
		fmt.Fprintf(env.Stdout, "%d %s.\n", count, noun)
	}
	for {
		select {
		case action := <-presses:
			count++
			fmt.Fprintln(env.Stdout, "pressed", action)
		case <-timer.C:
			summarize()
			return nil
		case <-ctx.Done():
			summarize()
			return nil
		}
	}
}

func countKeys(s config.Shortcuts) int {
	n := 0
	for _, key := range s.Keys {
		if key != "" {
			n++
		}
	}
	return n
}

type hotkeysHelperCmd struct{}

// Run is the helper's own process. It holds the keys the worker sends and reports their presses until the worker closes its input.
func (c *hotkeysHelperCmd) Run(env *Env) error {
	if env.HotkeysHelper == nil {
		return hotkey.ErrUnsupported
	}
	return env.HotkeysHelper()
}
