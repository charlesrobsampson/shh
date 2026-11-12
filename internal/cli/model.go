package cli

import (
	"errors"
	"fmt"
)

// ModelOps is what the model commands do, supplied by the program: the embedding model is an optional download, or built in.
type ModelOps struct {
	// Status says where the model is, or that it is not installed.
	Status func() string
	// Off is whether config.json turns the model off.
	Off bool
	// Built is whether this build carries the model inside.
	Built bool
	// Install downloads the model and returns where it went; Remove deletes the downloaded file and says whether there was one.
	Install func() (string, error)
	Remove  func() (bool, error)
}

type modelCmd struct {
	Status  modelStatusCmd  `cmd:"" default:"1" help:"Whether the embedding model is there, and whether it is in use."`
	Install modelInstallCmd `cmd:"" help:"Download the embedding model (8 MB, checked against a pinned checksum). A network use, so it is only when you ask."`
	Remove  modelRemoveCmd  `cmd:"" help:"Delete the downloaded embedding model; shh goes back to its first two scoring layers."`
}

func modelOpsOf(env *Env) (*ModelOps, error) {
	if env.Model == nil {
		return nil, errors.New("this build cannot manage the model")
	}
	return env.Model, nil
}

type modelStatusCmd struct{}

func (c *modelStatusCmd) Run(env *Env) error {
	m, err := modelOpsOf(env)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Embedding model: %s.\n", m.Status())
	if m.Off {
		fmt.Fprintln(env.Stdout, "It is turned off in config.json (classifier.embedding_model), so it is not used.")
	}
	return nil
}

type modelInstallCmd struct{}

func (c *modelInstallCmd) Run(env *Env) error {
	m, err := modelOpsOf(env)
	if err != nil {
		return err
	}
	path, err := m.Install()
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Downloaded the embedding model to %s.\nRestart the worker to use it: shh worker restart. What you have labelled is replayed into the new weights.\n", path)
	if m.Off {
		fmt.Fprintln(env.Stdout, "Note: config.json has classifier.embedding_model \"off\", so it stays unused until you set \"auto\".")
	}
	return nil
}

type modelRemoveCmd struct{}

func (c *modelRemoveCmd) Run(env *Env) error {
	m, err := modelOpsOf(env)
	if err != nil {
		return err
	}
	if m.Built {
		return errors.New("this shh has the model built in: set \"classifier\": {\"embedding_model\": \"off\"} in config.json to stop using it")
	}
	removed, err := m.Remove()
	if err != nil {
		return err
	}
	if !removed {
		fmt.Fprintln(env.Stdout, "No downloaded model to remove.")
		return nil
	}
	fmt.Fprintln(env.Stdout, "Removed the embedding model. Restart the worker to go back to the first two layers: shh worker restart.")
	return nil
}
