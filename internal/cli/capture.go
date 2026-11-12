package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type captureCmd struct {
	File     string `default:"private/captures/raw.jsonl" help:"Where to append what is captured. The text of your notifications is in it, so keep it under private/."`
	App      string `help:"Only notifications from apps whose bundle id contains this (com.microsoft.teams, say)."`
	Backfill string `help:"Also capture what arrived in the last while (90m, 2h), not only what comes next."`
	Once     bool   `help:"Capture what is there now and exit, instead of watching until Ctrl-C."`
}

// captureRow is one raw notification as macOS stored it: what rules are built from.
type captureRow struct {
	RecID     int64     `json:"rec_id"`
	App       string    `json:"app"`
	Delivered time.Time `json:"delivered"`
	Category  string    `json:"category,omitempty"`
	Thread    string    `json:"thread,omitempty"`
	Title     string    `json:"title,omitempty"`
	Subtitle  string    `json:"subtitle,omitempty"`
	Body      string    `json:"body,omitempty"`
}

// Run appends each notification that arrives to a file, as it came, so rules can be written from real examples. It reads the
// notification database and changes nothing in it or in shh's own history.
func (c *captureCmd) Run(env *Env) error {
	if env.Notifications == nil {
		return errors.New("the notification database cannot be read from here")
	}
	var from time.Time
	if c.Backfill != "" {
		d, err := parseSince(c.Backfill)
		if err != nil {
			return usagef("--backfill: %w", err)
		}
		from = env.now().Add(-d)
	} else {
		from = env.now()
	}
	if err := os.MkdirAll(filepath.Dir(c.File), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(c.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	warnIfNotPrivate(env.Stderr, c.File)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	type key struct {
		id int64
		at int64
	}
	seen := map[key]bool{}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	written := 0
	poll := func() error {
		batch, err := env.Notifications.Since(0)
		if err != nil {
			return err
		}
		for _, n := range batch.Notifications {
			k := key{n.RecID, n.Delivered.UnixNano()}
			if seen[k] {
				continue
			}
			seen[k] = true
			if n.Delivered.Before(from) || (c.App != "" && !strings.Contains(strings.ToLower(n.BundleID), strings.ToLower(c.App))) {
				continue
			}
			if err := enc.Encode(captureRow{RecID: n.RecID, App: n.BundleID, Delivered: n.Delivered, Category: n.Category, Thread: string(n.Thread),
				Title: n.Title, Subtitle: n.Subtitle, Body: n.Body}); err != nil {
				return err
			}
			written++
		}
		return nil
	}
	if err := poll(); err != nil {
		return err
	}
	if !c.Once {
		fmt.Fprintf(env.Stderr, "Capturing to %s until Ctrl-C.\n", c.File)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
	watch:
		for {
			select {
			case <-ctx.Done():
				break watch
			case <-tick.C:
				if err := poll(); err != nil {
					fmt.Fprintln(env.Stderr, "shh: capture:", err)
				}
			}
		}
	}
	fmt.Fprintf(env.Stdout, "Captured %d notification%s to %s.\n", written, plural(written), c.File)
	return nil
}
