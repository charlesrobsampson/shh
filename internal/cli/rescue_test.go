package cli_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/cli"
	"github.com/charlesrobsampson/shh/internal/config"
)

func TestOnlyTheCommandsThatFixAConfigurationRunWithoutOne(t *testing.T) {
	tests := []struct {
		args   string
		rescue bool
	}{
		{"worker restart", true}, {"worker stop", true}, {"worker status", true}, {"doctor", true}, {"init", true}, {"guide", true},
		{"guide config", true}, {"config path", true}, {"config defaults", true}, {"--help", true}, {"focus --help", true}, {"-h", true},
		{"config show", false}, {"config", false}, {"focus 75% 25m", false}, {"f -f 90%", false}, {"ls", false}, {"ui", false},
		{"daemon", false}, {"menubar", false}, {"routine run pomodoro", false}, {"alert test", false}, {"hotkeys test", false}, {"", false},
	}
	for _, tt := range tests {
		t.Run(tt.args, func(t *testing.T) {
			if got := cli.IsRescueCommand(strings.Fields(tt.args)); got != tt.rescue {
				t.Errorf("IsRescueCommand(%q) = %v, want %v", tt.args, got, tt.rescue)
			}
		})
	}
}

func TestAConfigurationThatLoadsIsUsedWhateverTheCommand(t *testing.T) {
	good := config.Config{Menubar: config.Menubar{Label: "mine"}}
	for _, args := range []string{"ls", "worker restart"} {
		cfg, warning, err := cli.ResolveConfig(strings.Fields(args), func() (config.Config, error) { return good, nil }, nil)
		if err != nil || warning != "" || cfg.Menubar.Label != "mine" {
			t.Errorf("%s: cfg %+v warning %q err %v", args, cfg.Menubar, warning, err)
		}
	}
}

func TestABrokenConfigurationStopsMostCommandsAndSaysWhatStillWorks(t *testing.T) {
	broken := func() (config.Config, error) {
		return config.Config{}, errors.New(`config.json: unknown key "alert.shortcuts"`)
	}
	_, _, err := cli.ResolveConfig([]string{"ls"}, broken, func() (config.Config, error) { t.Error("fallback used"); return config.Config{}, nil })
	if err == nil || !strings.Contains(err.Error(), `unknown key "alert.shortcuts"`) {
		t.Fatalf("err = %v, want the original error", err)
	}
	for _, still := range []string{"shh worker", "shh doctor", "shh config path", "shh config defaults"} {
		if !strings.Contains(err.Error(), still) {
			t.Errorf("the error should say %q still works:\n%v", still, err)
		}
	}
}

func TestTheCommandsThatFixItRunOnTheDefaultsWithAWarning(t *testing.T) {
	broken := func() (config.Config, error) { return config.Config{}, errors.New("config.json:7: unknown key \"x\"") }
	fallback := func() (config.Config, error) { return config.Config{Menubar: config.Menubar{Label: "default"}}, nil }
	for _, args := range []string{"worker restart", "doctor", "config path"} {
		cfg, warning, err := cli.ResolveConfig(strings.Fields(args), broken, fallback)
		if err != nil || cfg.Menubar.Label != "default" {
			t.Errorf("%s: cfg %+v err %v", args, cfg.Menubar, err)
		}
		if !strings.Contains(warning, "using the defaults") || !strings.Contains(warning, `unknown key "x"`) {
			t.Errorf("%s: warning %q should say the defaults are in use and why", args, warning)
		}
	}
	if _, _, err := cli.ResolveConfig([]string{"worker", "stop"}, broken, func() (config.Config, error) { return config.Config{}, errors.New("no home") }); err == nil {
		t.Error("when even the defaults cannot be made the command stops")
	}
}
