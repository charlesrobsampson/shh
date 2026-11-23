package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charlesrobsampson/shh/internal/config"
)

// IsRescueCommand says whether a command line is one that has to work even when the configuration cannot be read, because it is how
// you put that right: the worker commands (to stop and restart it), doctor, init, the guide, where the configuration is and its
// documented defaults, and help.
func IsRescueCommand(args []string) bool {
	var words []string
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	if len(words) == 0 {
		return false
	}
	if words[0] == "config" {
		return len(words) > 1 && slices.Contains([]string{"path", "defaults"}, words[1])
	}
	return slices.Contains([]string{"worker", "doctor", "init", "guide"}, words[0])
}

// ResolveConfig is the configuration a command line runs with. When it does not load the command stops with that error, except for the
// rescue commands, which run on the defaults and get a warning to print, so a configuration that is wrong (written for a newer shh,
// say) never locks you out of restarting the worker or finding what is wrong.
func ResolveConfig(args []string, load, fallback func() (config.Config, error)) (cfg config.Config, warning string, err error) {
	cfg, err = load()
	if err == nil {
		return cfg, "", nil
	}
	if !IsRescueCommand(args) {
		return config.Config{}, "", fmt.Errorf("%w\n(these still work when the configuration does not: shh worker, shh doctor, shh config path, shh config defaults, shh init, shh guide)", err)
	}
	cfg, ferr := fallback()
	if ferr != nil {
		return config.Config{}, "", err
	}
	return cfg, fmt.Sprintf("cannot read the configuration, using the defaults: %v", err), nil
}
