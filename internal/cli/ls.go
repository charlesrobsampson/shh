package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/store"
)

type lsCmd struct {
	Source    string `help:"Only notifications from this app (the name shh shows, such as Teams)."`
	Sender    string `help:"Only notifications from this sender."`
	Since     string `help:"Only the last while: 90m, 2h, 1d (days) or 1d12h."`
	Status    string `enum:",blocked,alerted" default:"" help:"Only what shh held back (blocked) or let through (alerted)."`
	MinScore  int    `name:"min-score" help:"Only notifications the model scored at least this, 0 to 100."`
	Flagged   bool   `help:"Only notifications flagged for review."`
	Untrained bool   `help:"Only notifications you have not trained yet."`
	Count     int    `short:"n" default:"30" help:"How many to list, newest first (0 for all)."`
	JSON      bool   `name:"json" help:"One JSON object per line, for scripts."`
}

// Run lists stored notifications, newest first. It only reads: nothing about a notification (flags, training, read marks) changes.
func (c *lsCmd) Run(env *Env) error {
	if env.WithStore == nil {
		return errors.New("the notifications cannot be read from here")
	}
	filter := store.Filter{Source: c.Source, Sender: c.Sender, Status: store.Status(c.Status), MinScore: c.MinScore}
	if c.MinScore < 0 || c.MinScore > 100 {
		return usagef("--min-score %d is not between 0 and 100", c.MinScore)
	}
	now := env.now()
	if c.Since != "" {
		d, err := parseSince(c.Since)
		if err != nil {
			return usagef("--since: %w", err)
		}
		filter.Since = now.Add(-d)
	}
	if c.Untrained {
		filter.View = store.ViewTrain
	}
	return env.WithStore(func(st *store.Store) error {
		// the filters that the store does not have are applied here, so the count asked for is of what is shown
		all, err := st.ListNotifications(filter)
		if err != nil {
			return err
		}
		var rows []store.Notification
		for _, n := range all {
			if c.Flagged && !n.IsFlagged {
				continue
			}
			rows = append(rows, n)
			if c.Count > 0 && len(rows) >= c.Count {
				break
			}
		}
		if c.JSON {
			enc := json.NewEncoder(env.Stdout)
			for _, n := range rows {
				if err := enc.Encode(lsRecord(n)); err != nil {
					return err
				}
			}
			return nil
		}
		if len(rows) == 0 {
			fmt.Fprintln(env.Stdout, "No notifications match.")
			return nil
		}
		fmt.Fprintf(env.Stdout, "%-6s %-10s %-8s %3s  %-10s %s\n", "ID", "WHEN", "STATUS", "PCT", "APP", "SENDER · TITLE")
		for _, n := range rows {
			fmt.Fprintln(env.Stdout, lsLine(n, now))
		}
		return nil
	})
}

// parseSince reads how far back to look: 90m, 2h, 1d, 1d12h. A whole number of days is allowed on top of what a duration takes.
func parseSince(text string) (time.Duration, error) {
	text = strings.TrimSpace(strings.ToLower(text))
	var days time.Duration
	if i := strings.Index(text, "d"); i > 0 {
		n, err := strconv.Atoi(text[:i])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not a length of time such as 90m, 2h or 1d", text)
		}
		days, text = time.Duration(n)*24*time.Hour, text[i+1:]
		if text == "" {
			return days, nil
		}
	}
	d, err := decide.ParseDuration(text)
	if err != nil {
		return 0, err
	}
	return days + d, nil
}

// lsLine is a notification on one line: "41 Thu 09:41 blocked 42 Teams Ana · the title" under a header that names the columns (PCT is the model's score).
func lsLine(n store.Notification, now time.Time) string {
	var marks []string
	if n.IsFlagged {
		marks = append(marks, "flagged")
	}
	if n.AutoTrained {
		marks = append(marks, "auto-trained")
	} else if n.IsTrained {
		marks = append(marks, "trained")
	}
	line := fmt.Sprintf("%-6d %-10s %-8s %3d  %-10s %s", n.ID, n.Timestamp.In(now.Location()).Format("Mon 15:04"), n.Status, scorePercent(n), n.Source, oneLineText(joinTitle(n)))
	if len(marks) > 0 {
		line += "  [" + strings.Join(marks, ", ") + "]"
	}
	return line
}

func joinTitle(n store.Notification) string {
	switch {
	case n.Sender != "" && n.Title != "" && n.Title != n.Sender:
		return n.Sender + " · " + n.Title
	case n.Sender != "":
		return n.Sender
	}
	return n.Title
}

// scorePercent is the model's score for the notification on the 0 to 100 scale, the one --min-score compares with.
func scorePercent(n store.Notification) int {
	return int(n.Score*100 + 0.5)
}

func oneLineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// lsRecord is a notification for --json: what shh knows about how it was decided, and nothing it did not store.
func lsRecord(n store.Notification) map[string]any {
	rec := map[string]any{
		"id": n.ID, "time": n.Timestamp.UTC().Format(time.RFC3339), "source": n.Source, "sender": n.Sender, "title": n.Title, "status": string(n.Status),
		"score": scorePercent(n), "boost": int(n.Boost*100 + 0.5), "reason": n.Reason, "flagged": n.IsFlagged, "trained": n.IsTrained, "auto_trained": n.AutoTrained, "read": n.IsRead,
	}
	if n.ThresholdAtDecision != nil {
		rec["level"] = *n.ThresholdAtDecision
	}
	if n.UserLabel != nil {
		rec["label"] = *n.UserLabel
	}
	if n.UserFeedback != "" {
		rec["feedback"] = n.UserFeedback
	}
	return rec
}
