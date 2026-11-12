package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/charlesrobsampson/shh/internal/config"
)

type initCmd struct{}

// Run sets shh up for a new user: it makes the configuration folder and writes config.json with every option at its default and
// explained, so what can be changed is in front of them. A file that is there already is never touched. (The rest of first-time setup, such
// as checking permissions, belongs here too as it is built.)
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
