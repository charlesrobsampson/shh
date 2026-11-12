package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charlesrobsampson/shh/internal/store"
)

type rulesCmd struct {
	Review rulesReviewCmd `cmd:"" help:"Calendar reminders whose start time shh could not read, with what to add to rules.json."`
}

type rulesReviewCmd struct {
	Count int `short:"n" default:"20" help:"How many to show, newest first (0 for all)."`
}

// Run lists the calendar reminders shh could not read a start time from. Those fire at once instead of at the right moment, so each one
// is a pattern missing from rules.json. It only reads.
func (c *rulesReviewCmd) Run(env *Env) error {
	if env.WithStore == nil {
		return errors.New("the history cannot be read from here")
	}
	return env.WithStore(func(st *store.Store) error {
		rows, err := st.UnparsedCalendar()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Fprintln(env.Stdout, "Every calendar reminder shh has seen was read. Nothing to add.")
			return nil
		}
		total := len(rows)
		if c.Count > 0 && len(rows) > c.Count {
			rows = rows[:c.Count]
		}
		fmt.Fprintf(env.Stdout, "%d calendar reminder%s shh could not read a start time from (newest first). They are shown as they arrived, so this is the text of your notifications.\n\n", total, plural(total))
		for _, u := range rows {
			fmt.Fprintf(env.Stdout, "%s  category %s\n", u.CreatedAt.In(env.now().Location()).Format("Mon Jan 2 15:04"), orNone(u.Category))
			for _, line := range []struct{ name, text string }{{"title", u.Title}, {"subtitle", u.Subtitle}, {"text", u.RawText}} {
				if line.text != "" {
					fmt.Fprintf(env.Stdout, "  %-8s %s\n", line.name, oneLineText(line.text))
				}
			}
			fmt.Fprintln(env.Stdout)
		}
		fmt.Fprintf(env.Stdout, "To teach shh one, add a pattern to %s that captures the time, as a named group (hour, minute, ampm, and year, month, day, or minutes for \"in N minutes\"):\n\n", filepath.Join(env.ConfigDir, "rules.json"))
		fmt.Fprintln(env.Stdout, strings.Join([]string{
			`  { "calendar": { "date": [`,
			`    { "name": "my-format", "pattern": "starts (?P<hour>\\d{1,2}):(?P<minute>\\d{2}) ?(?P<ampm>am|pm)" }`,
			`  ] } }`,
		}, "\n"))
		fmt.Fprintln(env.Stdout, "\nThe built-in patterns are in the shipped defaults; yours are tried alongside them (a pattern with the same name replaces one).")
		return nil
	})
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
