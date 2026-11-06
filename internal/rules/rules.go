package rules

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/charlesrobsampson/shh/internal/tomlx"
)

//go:embed default_rules.toml
var defaultRules []byte

// maxBoost is the cap on a single urgency boost, matching the cap on the total (plan.md section 5.4).
const maxBoost = 0.4

type Pattern struct {
	Name   string
	Regexp *regexp.Regexp
}

type Urgency struct {
	Pattern
	Boost float64
}

type Rules struct {
	Urgency []Urgency
	// VIPSenders and VIPApps are lower-cased and deduplicated.
	VIPSenders         []string
	VIPApps            []string
	AppPriority        map[string]float64
	CalendarCategories []Pattern
	DatePatterns       []Pattern
}

type urgencyRule struct {
	Name    string  `toml:"name"`
	Pattern string  `toml:"pattern"`
	Boost   float64 `toml:"boost"`
	Enabled *bool   `toml:"enabled"`
}

type patternRule struct {
	Name    string `toml:"name"`
	Pattern string `toml:"pattern"`
	Enabled *bool  `toml:"enabled"`
}

type file struct {
	Urgency []urgencyRule `toml:"urgency"`
	VIP     struct {
		Senders []string `toml:"senders"`
		Apps    []string `toml:"apps"`
	} `toml:"vip"`
	AppPriority map[string]float64 `toml:"app_priority"`
	Calendar    struct {
		Category []patternRule `toml:"category"`
		Date     []patternRule `toml:"date"`
	} `toml:"calendar"`
}

// Defaults returns the embedded rules.
func Defaults() (Rules, error) {
	return Load("")
}

// Load merges the user file at path over the embedded defaults. An empty path or a missing file means defaults only.
func Load(path string) (Rules, error) {
	var base file
	if err := tomlx.Decode("default rules", defaultRules, &base); err != nil {
		return Rules{}, err
	}
	merged := base
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Rules{}, err
		}
		if err == nil {
			var user file
			if err := tomlx.Decode(path, raw, &user); err != nil {
				return Rules{}, err
			}
			if merged, err = merge(base, user); err != nil {
				return Rules{}, fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	return compile(merged)
}

func merge(base, user file) (file, error) {
	if err := checkNames("urgency", user.Urgency, func(r urgencyRule) string { return r.Name }); err != nil {
		return file{}, err
	}
	if err := checkNames("calendar.category", user.Calendar.Category, func(r patternRule) string { return r.Name }); err != nil {
		return file{}, err
	}
	if err := checkNames("calendar.date", user.Calendar.Date, func(r patternRule) string { return r.Name }); err != nil {
		return file{}, err
	}

	out := base
	out.Urgency = mergeByName(base.Urgency, user.Urgency, func(r urgencyRule) (string, *bool) { return r.Name, r.Enabled })
	out.Calendar.Category = mergeByName(base.Calendar.Category, user.Calendar.Category, func(r patternRule) (string, *bool) { return r.Name, r.Enabled })
	out.Calendar.Date = mergeByName(base.Calendar.Date, user.Calendar.Date, func(r patternRule) (string, *bool) { return r.Name, r.Enabled })
	out.VIP.Senders = append(append([]string{}, base.VIP.Senders...), user.VIP.Senders...)
	out.VIP.Apps = append(append([]string{}, base.VIP.Apps...), user.VIP.Apps...)
	out.AppPriority = map[string]float64{}
	for _, priorities := range []map[string]float64{base.AppPriority, user.AppPriority} {
		for app, priority := range priorities {
			out.AppPriority[app] = priority
		}
	}
	return out, nil
}

func checkNames[T any](kind string, rules []T, name func(T) string) error {
	seen := map[string]bool{}
	for i, r := range rules {
		n := name(r)
		switch {
		case n == "":
			return fmt.Errorf("%s rule #%d has no name", kind, i+1)
		case seen[n]:
			return fmt.Errorf("%s rule %q is defined twice", kind, n)
		}
		seen[n] = true
	}
	return nil
}

// mergeByName replaces a base rule that has the same name, drops it when the user disables it, and appends new names.
func mergeByName[T any](base, user []T, identify func(T) (string, *bool)) []T {
	out := append([]T{}, base...)
	for _, u := range user {
		name, enabled := identify(u)
		at := -1
		for i, b := range out {
			if existing, _ := identify(b); existing == name {
				at = i
			}
		}
		switch {
		case enabled != nil && !*enabled && at >= 0:
			out = append(out[:at], out[at+1:]...)
		case enabled != nil && !*enabled:
		case at >= 0:
			out[at] = u
		default:
			out = append(out, u)
		}
	}
	return out
}

func compile(f file) (Rules, error) {
	var r Rules
	for _, rule := range f.Urgency {
		if rule.Boost <= 0 || rule.Boost > maxBoost {
			return Rules{}, fmt.Errorf("urgency rule %q: boost %v must be above 0 and at most %v", rule.Name, rule.Boost, maxBoost)
		}
		p, err := compilePattern("urgency", rule.Name, rule.Pattern)
		if err != nil {
			return Rules{}, err
		}
		r.Urgency = append(r.Urgency, Urgency{Pattern: p, Boost: rule.Boost})
	}
	var err error
	if r.CalendarCategories, err = compilePatterns("calendar.category", f.Calendar.Category); err != nil {
		return Rules{}, err
	}
	if r.DatePatterns, err = compilePatterns("calendar.date", f.Calendar.Date); err != nil {
		return Rules{}, err
	}
	if r.VIPSenders, err = cleanList("vip.senders", f.VIP.Senders); err != nil {
		return Rules{}, err
	}
	if r.VIPApps, err = cleanList("vip.apps", f.VIP.Apps); err != nil {
		return Rules{}, err
	}
	r.AppPriority = map[string]float64{}
	for app, priority := range f.AppPriority {
		if priority < 0 || priority > 1 {
			return Rules{}, fmt.Errorf("app priority for %q is %v, want 0 to 1", app, priority)
		}
		r.AppPriority[app] = priority
	}
	return r, nil
}

func compilePatterns(kind string, rules []patternRule) ([]Pattern, error) {
	var out []Pattern
	for _, rule := range rules {
		p, err := compilePattern(kind, rule.Name, rule.Pattern)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func compilePattern(kind, name, pattern string) (Pattern, error) {
	if pattern == "" {
		return Pattern{}, fmt.Errorf("%s rule %q has no pattern", kind, name)
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return Pattern{}, fmt.Errorf("%s rule %q: invalid pattern: %w", kind, name, err)
	}
	return Pattern{Name: name, Regexp: re}, nil
}

func cleanList(key string, entries []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, entry := range entries {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			return nil, fmt.Errorf("%q has an empty entry", key)
		}
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	return out, nil
}
