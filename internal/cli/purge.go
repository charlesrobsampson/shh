package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/charlesrobsampson/shh/internal/store"
)

type purgeCmd struct {
	All bool `help:"Delete every stored notification, whatever its age, instead of only the ones past their time to live."`
	Yes bool `help:"Do not ask first."`
}

// Run deletes stored notifications after asking: the ones past their time to live (config.json, retention), or with --all every one. What
// the model learned from them stays learned. The worker does the first of these by itself, on start and every few hours.
func (c *purgeCmd) Run(env *Env) error {
	if env.WithStore == nil || env.Retention == nil {
		return errors.New("the history cannot be changed from here")
	}
	retention := env.Retention()
	return env.WithStore(func(st *store.Store) error {
		var count int
		var err error
		if c.All {
			count, err = st.CountAll()
		} else {
			count, err = st.CountExpired(env.now(), retention)
		}
		if err != nil {
			return err
		}
		what := "past their time to live"
		if c.All {
			what = "stored"
		}
		if count == 0 {
			fmt.Fprintf(env.Stdout, "No notifications %s: nothing to delete.\n", what)
			return nil
		}
		if !c.Yes {
			if !env.IsTerminal || env.Stdin == nil {
				return fmt.Errorf("%d notification%s %s would be deleted: add --yes to do it without being asked", count, plural(count), what)
			}
			fmt.Fprintf(env.Stdout, "Delete %d notification%s %s? What the model learned from them stays. [y/N] ", count, plural(count), what)
			answer, _ := bufio.NewReader(env.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
				fmt.Fprintln(env.Stdout, "Kept them.")
				return nil
			}
		}
		var deleted int
		if c.All {
			deleted, err = st.PurgeAll()
		} else {
			deleted, err = st.PurgeExpired(env.now(), retention)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "Deleted %d notification%s.\n", deleted, plural(deleted))
		return nil
	})
}
