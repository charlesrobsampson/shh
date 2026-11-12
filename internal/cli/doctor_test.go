package cli_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/doctor"
)

func TestDoctorReportsAndExitsOneOnlyOnAFailure(t *testing.T) {
	r := newRig(t, false)
	healthy := doctor.Env{Model: func() (string, error) { return "embedded", nil }, Tool: func(string) bool { return true }}
	r.env.Doctor = &healthy
	out := r.mustRun("doctor")
	if !strings.Contains(out, "embedded") || !strings.Contains(out, "warn  worker") || !strings.Contains(out, "0 failed") {
		t.Errorf("out:\n%s", out)
	}
	broken := healthy
	broken.NotificationDB = func() (string, error) { return "", errors.New("operation not permitted") }
	r.env.Doctor = &broken
	if code := r.run("doctor"); code != 1 || !strings.Contains(r.out.String(), "FAIL  notification database") {
		t.Errorf("code %d:\n%s", code, r.out.String())
	}
}

func TestDoctorDoesNotStartAWorker(t *testing.T) {
	r := newRig(t, false)
	spawned := false
	r.env.Spawn = func() error { spawned = true; return nil }
	r.env.PersistedFocus = func() bool { return true }
	r.env.Doctor = &doctor.Env{}
	r.run("doctor")
	if spawned {
		t.Error("doctor must only look")
	}
}
