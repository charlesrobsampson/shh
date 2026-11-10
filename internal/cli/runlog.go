package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

type routineLogCmd struct {
	Name  []string `arg:"" optional:"" name:"name" help:"Only the runs of this routine; a name with spaces needs no quotes."`
	Count int      `short:"n" default:"15" help:"How many runs to list (0 for all)."`
	RunID int64    `name:"run" help:"Show one run by its number: what it did and the notifications flagged during it that still wait for review."`
}

func (c *routineLogCmd) Run(env *Env) error {
	if env.WithStore == nil {
		return errors.New("the history cannot be read from here")
	}
	return env.WithStore(func(st *store.Store) error {
		now := env.now()
		if c.RunID != 0 {
			return c.showRun(env, st, now)
		}
		logs, err := session.RunLogs(st, strings.Join(c.Name, " "), c.Count, now)
		if err != nil {
			return err
		}
		if len(logs) == 0 {
			if len(c.Name) > 0 {
				fmt.Fprintf(env.Stdout, "No runs of %s yet.\n", strings.Join(c.Name, " "))
			} else {
				fmt.Fprintln(env.Stdout, "No routine has run yet.")
			}
			return nil
		}
		fmt.Fprintln(env.Stdout, "Runs of routines, newest first (shh routine log --run <number> shows one in full):")
		toReview := 0
		for _, l := range logs {
			fmt.Fprintln(env.Stdout, "  "+logLine(l, now))
			toReview += l.ToReview
		}
		if toReview > 0 {
			fmt.Fprintf(env.Stdout, "\n%d flagged notification%s still to review: shh ui --tab train\n", toReview, plural(toReview))
		}
		return nil
	})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// logLine is a run on one line: "#12  Mon 09:00  morning  finished  1h15m  4 of 4 steps  held 12, let 3  flagged 2 (1 to review)".
func logLine(l session.RunLog, now time.Time) string {
	state := l.Reason
	switch {
	case l.Ended == nil:
		state = "running"
	case l.Reason == "until":
		state = "cut off"
	case l.Reason == "missing":
		state = "ended"
	}
	line := fmt.Sprintf("#%-4d %-10s %-14s %-9s %-7s %d of %d steps", l.ID, l.Started.In(now.Location()).Format("Mon 15:04"), l.Routine, state,
		decide.FormatRemaining(int(l.Elapsed/time.Second)), l.Steps, l.Total)
	line += fmt.Sprintf("  held %d, let %d", l.Held, l.Let)
	switch {
	case l.Flagged > 0 && l.ToReview == 0:
		line += fmt.Sprintf("  flagged %d (all reviewed)", l.Flagged)
	case l.Flagged > 0 && l.ToReview < l.Flagged:
		line += fmt.Sprintf("  flagged %d (%d to review)", l.Flagged, l.ToReview)
	case l.Flagged > 0:
		line += fmt.Sprintf("  flagged %d (to review)", l.Flagged)
	}
	return line
}

func (c *routineLogCmd) showRun(env *Env, st *store.Store, now time.Time) error {
	l, ok, err := session.RunLogOf(st, c.RunID, now)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("there is no run number %d (shh routine log lists them)", c.RunID)
	}
	loc := now.Location()
	when := l.Started.In(loc).Format("Mon 2 Jan 15:04")
	if l.Ended != nil {
		when += " to " + l.Ended.In(loc).Format("15:04")
	}
	fmt.Fprintf(env.Stdout, "Run %d: %s, %s (%s)\n", l.ID, l.Routine, when, stateWord(l))
	fmt.Fprintf(env.Stdout, "  %d of %d steps in %s; held back %d notification%s, let through %d.\n", l.Steps, l.Total, decide.FormatRemaining(int(l.Elapsed/time.Second)),
		l.Held, plural(l.Held), l.Let)
	items, err := session.RunFlagged(st, c.RunID, now)
	if err != nil {
		return err
	}
	switch {
	case len(items) > 0:
		fmt.Fprintf(env.Stdout, "  Flagged during it and still to review (%d):\n", len(items))
		for _, it := range items {
			fmt.Fprintf(env.Stdout, "    %s  %s\n", it.At.In(loc).Format("15:04"), flaggedText(it))
		}
		fmt.Fprintln(env.Stdout, "  Review them: shh ui --tab train")
	case l.Flagged > 0:
		fmt.Fprintf(env.Stdout, "  %d notification%s flagged during it, all reviewed since.\n", l.Flagged, plural(l.Flagged))
	default:
		fmt.Fprintln(env.Stdout, "  Nothing was flagged during it.")
	}
	return nil
}

func stateWord(l session.RunLog) string {
	switch {
	case l.Ended == nil:
		return "running"
	case l.Reason == "until":
		return "cut off at its end time"
	case l.Reason == "missing":
		return "ended: the routine was gone from the file"
	}
	return l.Reason
}

// flaggedText is a flagged notification in brief: "Teams · Ana · the subject".
func flaggedText(it session.FlaggedItem) string {
	parts := []string{it.Source}
	if it.Sender != "" {
		parts = append(parts, it.Sender)
	}
	if it.Title != "" && it.Title != it.Sender {
		parts = append(parts, it.Title)
	}
	return strings.Join(parts, " · ")
}
