package doctor_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/doctor"
)

var (
	errNoModel = errors.New("no model")
	errOff     = errors.New("off")
)

func good(t *testing.T) doctor.Env {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	os.WriteFile(cfg, []byte("{}"), 0o600)
	db := filepath.Join(dir, "shh.db")
	os.WriteFile(db, nil, 0o600)
	return doctor.Env{
		ConfigPath: cfg, LoadConfig: func() error { return nil },
		DBPath: db, OpenDB: func() (int, error) { return 13, nil },
		NotificationDB: func() (string, error) { return "/x/db", nil },
		Model:          func() (string, error) { return "embedded", nil }, ModelMissing: errNoModel, ModelOff: errOff,
		RulesPath: filepath.Join(dir, "rules.json"), LoadRules: func() error { return errors.New("never read: file is absent") },
		RoutinesPath: filepath.Join(dir, "routines.json"), LoadRoutines: func() error { return nil },
		Accessibility: func() (bool, error) { return true, nil },
		Worker:        func() (bool, string, error) { return true, "abc", nil }, Build: "abc",
		Tool: func(string) bool { return true },
	}
}

func report(rs []doctor.Result) string {
	var b strings.Builder
	doctor.Print(&b, rs)
	return b.String()
}

func TestAHealthySetupIsAllOK(t *testing.T) {
	rs := doctor.Run(doctor.Standard(good(t)))
	if len(rs) != 9 {
		t.Fatalf("%d checks", len(rs))
	}
	for _, r := range rs {
		if r.Level != doctor.OK {
			t.Errorf("%s: %v %s", r.Name, r.Level, r.Detail)
		}
	}
	if doctor.Failed(rs) || !strings.Contains(report(rs), "9 ok, 0 warnings, 0 failed") {
		t.Errorf("report:\n%s", report(rs))
	}
}

func TestProblemsAreFailuresOrWarningsWithAFix(t *testing.T) {
	e := good(t)
	e.LoadConfig = func() error { return errors.New("config.json:3: unknown key \"colour\"") }
	e.NotificationDB = func() (string, error) { return "", errors.New("operation not permitted") }
	e.Model = func() (string, error) { return "", errNoModel }
	e.Accessibility = func() (bool, error) { return false, nil }
	e.Worker = func() (bool, string, error) { return true, "old", nil }
	e.LoadRoutines = func() error { return errors.New("routines.json: bad") }
	os.WriteFile(e.RoutinesPath, nil, 0o600)
	rs := doctor.Run(doctor.Standard(e))
	want := map[string]doctor.Level{"config": doctor.Fail, "notification database": doctor.Fail, "embedding model": doctor.OK,
		"accessibility": doctor.Warn, "worker": doctor.Warn, "routines": doctor.Fail, "database": doctor.OK, "rules": doctor.OK}
	for _, r := range rs {
		if r.Level != want[r.Name] {
			t.Errorf("%s: %v (%s)", r.Name, r.Level, r.Detail)
		}
		if r.Level != doctor.OK && r.Fix == "" {
			t.Errorf("%s has no fix", r.Name)
		}
	}
	e.Model = func() (string, error) { return "", errOff }
	if got := doctor.Run(doctor.Standard(e)); got[3].Level != doctor.OK || !strings.Contains(got[3].Detail, "turned off") {
		t.Errorf("a model turned off is not a problem: %+v", got[3])
	}
	if !doctor.Failed(rs) || !strings.Contains(report(rs), "->") || !strings.Contains(report(rs), "3 failed") {
		t.Errorf("report:\n%s", report(rs))
	}
}

func TestNothingRunningAndNoFilesAreWarningsNotFailures(t *testing.T) {
	e := good(t)
	os.Remove(e.DBPath)
	os.Remove(e.ConfigPath)
	e.Worker = func() (bool, string, error) { return false, "", nil }
	rs := doctor.Run(doctor.Standard(e))
	if doctor.Failed(rs) {
		t.Errorf("a fresh machine is not a failure:\n%s", report(rs))
	}
}

func TestACheckThatPanicsOnlyFailsItself(t *testing.T) {
	e := good(t)
	e.Worker = func() (bool, string, error) { panic("boom") }
	rs := doctor.Run(doctor.Standard(e))
	for _, r := range rs {
		if (r.Name == "worker") != (r.Level == doctor.Fail) {
			t.Errorf("%s: %v", r.Name, r.Level)
		}
	}
}

func TestChecksWithoutAProbeAreLeftOut(t *testing.T) {
	if n := len(doctor.Standard(doctor.Env{})); n != 0 {
		t.Errorf("%d checks from nothing", n)
	}
}
