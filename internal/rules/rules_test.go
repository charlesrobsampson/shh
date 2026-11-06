package rules_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/rules"
)

func writeUser(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, content string) rules.Rules {
	t.Helper()
	r, err := rules.Load(writeUser(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

func urgency(r rules.Rules, name string) (rules.Urgency, bool) {
	for _, u := range r.Urgency {
		if u.Name == name {
			return u, true
		}
	}
	return rules.Urgency{}, false
}

func names(patterns []rules.Pattern) []string {
	out := make([]string, len(patterns))
	for i, p := range patterns {
		out[i] = p.Name
	}
	return out
}

func TestDefaultsLoadAndAreValid(t *testing.T) {
	r, err := rules.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Urgency) == 0 || len(r.CalendarCategories) == 0 || len(r.DatePatterns) == 0 || len(r.AppPriority) == 0 {
		t.Fatalf("defaults are missing a section: %d urgency, %d categories, %d date patterns, %d app priorities",
			len(r.Urgency), len(r.CalendarCategories), len(r.DatePatterns), len(r.AppPriority))
	}
	for _, u := range r.Urgency {
		if u.Boost <= 0 || u.Boost > 0.4 {
			t.Errorf("default urgency rule %q has boost %v outside (0, 0.4]", u.Name, u.Boost)
		}
	}
	for source, priority := range r.AppPriority {
		if priority < 0 || priority > 1 {
			t.Errorf("default priority for %q is %v, want 0 to 1", source, priority)
		}
	}
}

func TestDefaultUrgencyKeywordsMatchCaseInsensitively(t *testing.T) {
	r, _ := rules.Defaults()
	tests := []struct {
		text     string
		wantRule string
	}{
		{"Production outage, all hands", "outage"},
		{"URGENT please review", "urgent"},
		{"need this ASAP", "urgent"},
		{"is prod down?", "prod"},
		{"call me now", "now"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			rule, ok := urgency(r, tt.wantRule)
			if !ok {
				t.Fatalf("no default rule named %q", tt.wantRule)
			}
			if !rule.Regexp.MatchString(tt.text) {
				t.Errorf("rule %q does not match %q", tt.wantRule, tt.text)
			}
		})
	}
	t.Run("whole words only", func(t *testing.T) {
		rule, _ := urgency(r, "now")
		if rule.Regexp.MatchString("knowledge base known issues") {
			t.Error(`"now" matched inside another word`)
		}
	})
}

func TestDefaultsRecogniseTheRealOutlookReminderCategory(t *testing.T) {
	r, _ := rules.Defaults()
	category := "OLAlertsNotificationRemindersWithJoinAndChatCategoryIdentifier"
	if !slices.ContainsFunc(r.CalendarCategories, func(p rules.Pattern) bool { return p.Regexp.MatchString(category) }) {
		t.Errorf("no default calendar category matches %q", category)
	}
	if slices.ContainsFunc(r.CalendarCategories, func(p rules.Pattern) bool { return p.Regexp.MatchString("OLAlertsNotificationCategory") }) {
		t.Error("a mail notification category was treated as a calendar reminder")
	}
}

func TestDefaultDatePatternsCaptureNamedFields(t *testing.T) {
	r, _ := rules.Defaults()
	tests := []struct {
		text string
		want map[string]string
	}{
		{"November 5, 2026 at 13:30", map[string]string{"month": "November", "day": "5", "year": "2026", "hour": "13", "minute": "30"}},
		{"Today at 9:00 AM", map[string]string{"hour": "9", "minute": "00", "ampm": "AM"}},
		{"in 5 minutes", map[string]string{"minutes": "5"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			matched := false
			for _, p := range r.DatePatterns {
				m := p.Regexp.FindStringSubmatch(tt.text)
				if m == nil {
					continue
				}
				matched = true
				for group, want := range tt.want {
					if got := m[p.Regexp.SubexpIndex(group)]; got != want {
						t.Errorf("pattern %q: group %q = %q, want %q", p.Name, group, got, want)
					}
				}
				break
			}
			if !matched {
				t.Fatalf("no default date pattern matches %q", tt.text)
			}
		})
	}
}

func TestDefaultsNameTheCommonApps(t *testing.T) {
	r, _ := rules.Defaults()
	for bundle, want := range map[string]string{
		"com.microsoft.teams2":      "Teams",
		"com.microsoft.outlook":     "Outlook",
		"com.tinyspeck.slackmacgap": "Slack",
		"com.apple.mail":            "Mail",
		"com.apple.mobilesms":       "Messages",
	} {
		if got := r.Sources[bundle]; got != want {
			t.Errorf("Sources[%q] = %q, want %q", bundle, got, want)
		}
	}
}

func TestUserSourcesExtendAndOverride(t *testing.T) {
	r := load(t, `
[sources]
"com.microsoft.teams2" = "MS Teams"
"com.example.pager" = "Pager"
`)
	if r.Sources["com.microsoft.teams2"] != "MS Teams" || r.Sources["com.example.pager"] != "Pager" || r.Sources["com.apple.mail"] != "Mail" {
		t.Errorf("Sources = %v, want the override, the addition and the untouched defaults", r.Sources)
	}
}

func TestEmptySourceNamesAreRejected(t *testing.T) {
	_, err := rules.Load(writeUser(t, "[sources]\n\"com.example.pager\" = \"\"\n"))
	if err == nil || !strings.Contains(err.Error(), `"com.example.pager"`) {
		t.Fatalf("err = %v, want one naming the bundle id", err)
	}
}

func TestMissingUserFileMeansDefaults(t *testing.T) {
	defaults, _ := rules.Defaults()
	got, err := rules.Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names(patternsOf(got)), names(patternsOf(defaults))) || len(got.AppPriority) != len(defaults.AppPriority) {
		t.Error("a missing user file must give exactly the defaults")
	}
}

func patternsOf(r rules.Rules) []rules.Pattern {
	var out []rules.Pattern
	for _, u := range r.Urgency {
		out = append(out, u.Pattern)
	}
	return out
}

func TestUserFileExtendsTheDefaults(t *testing.T) {
	defaults, _ := rules.Defaults()
	r := load(t, `
[[urgency]]
name = "pagerduty"
pattern = 'pagerduty|triggered'
boost = 0.4

[vip]
senders = ["Jane Boss", "ceo@example.com"]
apps = ["PagerDuty"]

[app_priority]
PagerDuty = 0.95

[[calendar.category]]
name = "teams-meeting"
pattern = '^TeamsMeeting'

[[calendar.date]]
name = "tonight"
pattern = 'tonight at (?P<hour>\d+)'
`)
	if _, ok := urgency(r, "pagerduty"); !ok {
		t.Error("new urgency rule was not added")
	}
	if len(r.Urgency) != len(defaults.Urgency)+1 {
		t.Errorf("urgency rules = %d, want the defaults plus one", len(r.Urgency))
	}
	if !slices.Equal(r.VIPSenders, []string{"jane boss", "ceo@example.com"}) || !slices.Equal(r.VIPApps, []string{"pagerduty"}) {
		t.Errorf("VIPs = %v / %v, want lower-cased entries", r.VIPSenders, r.VIPApps)
	}
	if r.AppPriority["PagerDuty"] != 0.95 || r.AppPriority["Teams"] != defaults.AppPriority["Teams"] {
		t.Errorf("app priorities = %v, want the new entry and the defaults kept", r.AppPriority)
	}
	if !slices.Contains(names(r.CalendarCategories), "teams-meeting") || len(r.CalendarCategories) != len(defaults.CalendarCategories)+1 {
		t.Errorf("calendar categories = %v", names(r.CalendarCategories))
	}
	if !slices.Contains(names(r.DatePatterns), "tonight") {
		t.Errorf("date patterns = %v", names(r.DatePatterns))
	}
}

func TestUserRuleWithTheSameNameReplacesTheDefault(t *testing.T) {
	defaults, _ := rules.Defaults()
	before, _ := urgency(defaults, "now")
	r := load(t, `
[[urgency]]
name = "now"
pattern = '\bright now\b'
boost = 0.25
`)
	after, ok := urgency(r, "now")
	if !ok || after.Boost != 0.25 || after.Regexp.MatchString("call me now") || !after.Regexp.MatchString("Right NOW please") {
		t.Fatalf("replaced rule = %+v, want boost 0.25 matching only 'right now' (was %+v)", after, before)
	}
	if len(r.Urgency) != len(defaults.Urgency) {
		t.Errorf("urgency rules = %d, want the same count as the defaults (%d)", len(r.Urgency), len(defaults.Urgency))
	}
}

func TestUserCanSwitchOffADefaultRule(t *testing.T) {
	r := load(t, `
[[urgency]]
name = "now"
enabled = false

[[calendar.category]]
name = "no-such-default"
enabled = false
`)
	if _, ok := urgency(r, "now"); ok {
		t.Error(`the "now" rule is still active after enabled = false`)
	}
	if _, ok := urgency(r, "outage"); !ok {
		t.Error("switching one rule off removed others")
	}
}

func TestVIPEntriesAreDeduplicated(t *testing.T) {
	r := load(t, `
[vip]
senders = ["Jane", "jane", "JANE"]
`)
	if !slices.Equal(r.VIPSenders, []string{"jane"}) {
		t.Errorf("VIPSenders = %v, want one entry", r.VIPSenders)
	}
}

func TestBadRegexNamesTheRule(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantName string
		wantKind string
	}{
		{"urgency", "[[urgency]]\nname = \"broken-urgency\"\npattern = '(unclosed'\nboost = 0.2\n", "broken-urgency", "urgency"},
		{"calendar category", "[[calendar.category]]\nname = \"broken-category\"\npattern = '[a-'\n", "broken-category", "calendar.category"},
		{"calendar date", "[[calendar.date]]\nname = \"broken-date\"\npattern = '(?P<hour>'\n", "broken-date", "calendar.date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rules.Load(writeUser(t, tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), `"`+tt.wantName+`"`) || !strings.Contains(err.Error(), tt.wantKind) {
				t.Errorf("error %q should name rule %q in %s", err, tt.wantName, tt.wantKind)
			}
		})
	}
}

func TestInvalidRuleValuesAreRejected(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantIn  string
	}{
		{"zero boost", "[[urgency]]\nname = \"x\"\npattern = 'x'\nboost = 0\n", `"x"`},
		{"boost above the 0.4 cap", "[[urgency]]\nname = \"x\"\npattern = 'x'\nboost = 0.5\n", `"x"`},
		{"negative boost", "[[urgency]]\nname = \"x\"\npattern = 'x'\nboost = -0.1\n", `"x"`},
		{"missing name", "[[urgency]]\npattern = 'x'\nboost = 0.1\n", "name"},
		{"missing pattern", "[[urgency]]\nname = \"x\"\nboost = 0.1\n", `"x"`},
		{"duplicate names in one file", "[[urgency]]\nname = \"x\"\npattern = 'a'\nboost = 0.1\n[[urgency]]\nname = \"x\"\npattern = 'b'\nboost = 0.1\n", `"x"`},
		{"priority above 1", "[app_priority]\nTeams = 1.5\n", `"Teams"`},
		{"negative priority", "[app_priority]\nSlack = -0.1\n", `"Slack"`},
		{"empty VIP entry", "[vip]\nsenders = [\"ok\", \"\"]\n", "vip.senders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rules.Load(writeUser(t, tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error %q should mention %s", err, tt.wantIn)
			}
		})
	}
}

func TestTheCapBoostIsAllowed(t *testing.T) {
	r := load(t, "[[urgency]]\nname = \"max\"\npattern = 'x'\nboost = 0.4\n")
	if u, ok := urgency(r, "max"); !ok || u.Boost != 0.4 {
		t.Fatalf("rule = %+v, %v", u, ok)
	}
}

func TestUnknownKeysAreErrorsThatNameThem(t *testing.T) {
	tests := []struct {
		content string
		wantKey string
	}{
		{"[[urgency]]\nname = \"x\"\npattern = 'x'\nboost = 0.1\nweight = 3\n", "urgency.weight"},
		{"[vipp]\nsenders = []\n", "vipp"},
		{"[calendar]\ncategories = []\n", "calendar.categories"},
		{"[[calendar.category]]\nname = \"x\"\npattern = 'x'\nboost = 0.1\n", "calendar.category.boost"},
	}
	for _, tt := range tests {
		t.Run(tt.wantKey, func(t *testing.T) {
			_, err := rules.Load(writeUser(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), `"`+tt.wantKey+`"`) {
				t.Fatalf("err = %v, want an unknown-key error naming %q", err, tt.wantKey)
			}
		})
	}
}

func TestBrokenTOMLMentionsTheFile(t *testing.T) {
	path := writeUser(t, "[[urgency\nname = ")
	if _, err := rules.Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want one mentioning %s", err, path)
	}
}

func TestUserPatternsAreCaseInsensitiveByDefault(t *testing.T) {
	r := load(t, "[[urgency]]\nname = \"ci\"\npattern = 'Deploy FAILED'\nboost = 0.1\n")
	u, _ := urgency(r, "ci")
	if !u.Regexp.MatchString("deploy failed") {
		t.Error("user patterns should also match case-insensitively")
	}
}
