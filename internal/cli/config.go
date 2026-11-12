package cli

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/doctor"
)

type initCmd struct {
	History string `default:"3d" help:"Store what the notification database holds from this last while as unlabeled history for the Train tab (90m, 2h, 3d), or 0 for none."`
}

// Run sets shh up for a new user: it makes the configuration folder and writes config.json with every option at its default and
// explained, so what can be changed is in front of them. A file that is there already is never touched. It then checks that shh can read
// the notification database and use the accessibility API, and stores recent notifications as history so the Train tab has something to
// label on the first day.
func (c *initCmd) Run(env *Env) error {
	if env.ConfigPath == "" {
		return errors.New("this build does not know where the configuration lives")
	}
	created, err := config.WriteTemplate(env.ConfigPath)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(env.Stdout, "Created %s with every option at its default, explained in comments.\n", env.ConfigPath)
		fmt.Fprintln(env.Stdout, "Edit it to change how banners look: a change to the alert section shows in the next banner (shh guide config explains each option).")
	} else {
		fmt.Fprintf(env.Stdout, "%s already exists, so it was left as it is. shh config defaults prints the documented defaults to compare with.\n", env.ConfigPath)
	}
	var history time.Duration
	if c.History != "" && c.History != "0" {
		d, err := parseSince(c.History)
		if err != nil {
			return usagef("--history: %w", err)
		}
		history = d
	}
	c.fetchModel(env)
	c.checkAccess(env)
	c.importHistory(env, history)
	fmt.Fprintln(env.Stdout, "\nNext:")
	fmt.Fprintln(env.Stdout, "  shh worker start         start the background worker (it also starts by itself when a command needs it)")
	fmt.Fprintln(env.Stdout, "  shh routine example      print a routines file to start from (focus sessions in sequence, even by themselves on a schedule)")
	fmt.Fprintln(env.Stdout, "  shh alert test           see a sample of each banner style")
	fmt.Fprintln(env.Stdout, "  shh guide                the guide to the terminal UI, routines and commands")
	return nil
}

type configCmd struct {
	Path     configPathCmd     `cmd:"" help:"Print where config.json is."`
	Show     configShowCmd     `cmd:"" help:"Print the configuration in force: your file over the defaults."`
	Defaults configDefaultsCmd `cmd:"" help:"Print config.json with every option at its default, explained."`
}

type configPathCmd struct{}

func (c *configPathCmd) Run(env *Env) error {
	_, err := fmt.Fprintln(env.Stdout, env.ConfigPath)
	return err
}

type configShowCmd struct{}

func (c *configShowCmd) Run(env *Env) error {
	if env.ConfigShow == nil {
		return errors.New("the configuration cannot be shown from here")
	}
	out, err := env.ConfigShow()
	if err != nil {
		return err
	}
	_, err = env.Stdout.Write(out)
	return err
}

type configDefaultsCmd struct{}

func (c *configDefaultsCmd) Run(env *Env) error {
	_, err := io.WriteString(env.Stdout, config.Template)
	return err
}

// fetchModel makes sure the embedding model is available, which is what gives the classifier its second layer. It says what happened and
// goes on: without the model shh works on its first two layers, and shh init can be run again.
func (c *initCmd) fetchModel(env *Env) {
	if env.FetchModel == nil {
		return
	}
	fmt.Fprintln(env.Stdout)
	line, err := env.FetchModel()
	if err != nil {
		fmt.Fprintf(env.Stdout, "Embedding model: not available (%v). shh works without it, a little less well; shh init tries again.\n", err)
		return
	}
	fmt.Fprintln(env.Stdout, "Embedding model: "+line)
}

// checkAccess says whether shh can read the notification database and use the accessibility API: the two permissions it needs.
// It reports problems and goes on, because setup is not over for a missing permission.
func (c *initCmd) checkAccess(env *Env) {
	if env.Doctor == nil {
		return
	}
	probes := doctor.Env{NotificationDB: env.Doctor.NotificationDB, Accessibility: env.Doctor.Accessibility}
	fmt.Fprintln(env.Stdout)
	doctor.Print(env.Stdout, doctor.Run(doctor.Standard(probes)))
}

// importHistory has the worker store recent notifications as history. A worker that cannot be had, or a database that cannot be read, is
// said and left at that: the import can be run again with shh init.
func (c *initCmd) importHistory(env *Env, since time.Duration) {
	if since <= 0 {
		return
	}
	fmt.Fprintln(env.Stdout)
	if err := env.ensureWorker(); err != nil {
		fmt.Fprintf(env.Stdout, "History not imported: the worker could not be started (%v). shh init --history %s tries again.\n", err, c.History)
		return
	}
	n, err := env.client().ImportHistory(since)
	if err != nil {
		fmt.Fprintf(env.Stdout, "History not imported: %v\n", err)
		return
	}
	fmt.Fprintf(env.Stdout, "Stored %d notification%s from the last %s as history, unlabeled: shh ui --tab train is where to label them.\n", n, plural(n), c.History)
}
