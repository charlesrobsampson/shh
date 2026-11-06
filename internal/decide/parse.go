package decide

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxLevel is the highest threshold (lockdown); notifications scoring above it still get through.
const MaxLevel = 99

const tierList = "off, light, medium, deep, lockdown"

var tiers = map[string]int{"off": 0, "light": 35, "medium": 65, "deep": 90, "lockdown": MaxLevel}

var (
	wholeNumber   = regexp.MustCompile(`^[0-9]+$`)
	decimalNumber = regexp.MustCompile(`^[0-9]*\.[0-9]+$`)
)

// ParseThreshold reads a focus threshold: a whole percentage such as "75%" or a tier name. "100%" means the maximum, 99.
func ParseThreshold(input string) (int, error) {
	s := strings.TrimSpace(input)
	if level, ok := tiers[strings.ToLower(s)]; ok {
		return level, nil
	}
	digits, hasPercent := strings.CutSuffix(s, "%")
	switch {
	case !hasPercent && wholeNumber.MatchString(s):
		return 0, fmt.Errorf("%q is a bare number: write %s%% or a tier name (%s)", s, s, tierList)
	case decimalNumber.MatchString(digits):
		return 0, fmt.Errorf("%q is not a whole percentage: use whole numbers such as 75%%", s)
	case !hasPercent || !wholeNumber.MatchString(digits):
		return 0, fmt.Errorf("%q is not a threshold: write a percentage like 75%% or a tier name (%s)", s, tierList)
	}
	percent, err := strconv.Atoi(digits)
	if err != nil || percent > 100 {
		return 0, fmt.Errorf("%q is above 100%%", s)
	}
	return min(percent, MaxLevel), nil
}

// ParseLabel reads an exact training label, 0 to 100 (a number with or without %, or a tier name). Unlike thresholds it may be 100.
func ParseLabel(input string) (int, error) {
	s := strings.TrimSpace(input)
	if level, ok := tiers[strings.ToLower(s)]; ok {
		return level, nil
	}
	digits, _ := strings.CutSuffix(s, "%")
	if !wholeNumber.MatchString(digits) {
		return 0, fmt.Errorf("%q is not a label: write a whole number from 0 to 100 or a tier name (%s)", s, tierList)
	}
	label, err := strconv.Atoi(digits)
	if err != nil || label > 100 {
		return 0, fmt.Errorf("%q is above 100", s)
	}
	return label, nil
}

// ParseDuration reads a session length such as "25m" or "1h30m". "0" means indefinite.
func ParseDuration(input string) (time.Duration, error) {
	s := strings.TrimSpace(input)
	if wholeNumber.MatchString(s) {
		if strings.Trim(s, "0") == "" {
			return 0, nil
		}
		return 0, fmt.Errorf("%q needs a unit: write %sm for minutes or %ss for seconds", s, s, s)
	}
	d, err := time.ParseDuration(s)
	switch {
	case err != nil:
		return 0, fmt.Errorf("%q is not a duration: write something like 25m or 1h30m, or 0 for no limit", s)
	case d < 0:
		return 0, fmt.Errorf("%q is negative", s)
	case d > 0 && d < time.Second:
		return 0, fmt.Errorf("%q is shorter than a second", s)
	}
	return d, nil
}
