package cli

import (
	"errors"

	"github.com/charlesrobsampson/shh/internal/doctor"
	"github.com/charlesrobsampson/shh/internal/session"
)

type doctorCmd struct{}

// Run checks the setup and prints what it found. It exits 1 when something shh needs is broken; warnings do not count. It only looks: it
// does not start the worker or show anything on the screen.
func (c *doctorCmd) Run(env *Env) error {
	if env.Doctor == nil {
		return errors.New("this build cannot check the setup")
	}
	probes := *env.Doctor
	probes.Build = env.Build
	probes.Worker = func() (bool, string, error) {
		view, err := env.client().Status()
		if errors.Is(err, session.ErrNotRunning) {
			return false, "", nil
		}
		return err == nil, view.Build, err
	}
	results := doctor.Run(doctor.Standard(probes))
	doctor.Print(env.Stdout, results)
	if doctor.Failed(results) {
		return errors.New("something shh needs is not working (see above)")
	}
	return nil
}
