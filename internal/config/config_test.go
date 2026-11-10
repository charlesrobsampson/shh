package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, content string) config.Config {
	t.Helper()
	cfg, err := config.Load(write(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestDefaultsWhenTheFileIsMissing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Style{Position: "top-right", Size: "medium", Color: 208, TextColor: 15, DurationSecs: 0, Sound: "Glass"}
	if got := cfg.Alert.Resolve(config.KindMessage, ""); got != want {
		t.Errorf("default alert style = %+v, want %+v", got, want)
	}
	if cfg.Menubar.Label != "shh" {
		t.Errorf("menubar label = %q, want shh", cfg.Menubar.Label)
	}
	if cfg.Summarizer.Backend != "none" {
		t.Errorf("summarizer backend = %q, want none", cfg.Summarizer.Backend)
	}
	wantPaths := config.Paths{
		Dir: dir, DB: filepath.Join(dir, "shh.db"), Socket: filepath.Join(dir, "shh.sock"), PID: filepath.Join(dir, "shh.pid"),
	}
	if cfg.Paths != wantPaths {
		t.Errorf("paths = %+v, want %+v", cfg.Paths, wantPaths)
	}
}

func TestAnEmptyFileMeansDefaults(t *testing.T) {
	cfg := load(t, "")
	if got := cfg.Alert.Resolve(config.KindMessage, "").Color; got != 208 {
		t.Errorf("color = %d, want the default", got)
	}
}

func TestPartialFileMergesOverDefaults(t *testing.T) {
	cfg := load(t, `{
  "alert": {"color": 33, "duration_secs": 5},
  "menubar": {"label": "🔇 shh"},
  "summarizer": {"backend": "ollama"}
}`)
	got := cfg.Alert.Resolve(config.KindMessage, "")
	want := config.Style{Position: "top-right", Size: "medium", Color: 33, TextColor: 15, DurationSecs: 5, Sound: "Glass"}
	if got != want {
		t.Errorf("alert style = %+v, want %+v (only color and duration change)", got, want)
	}
	if cfg.Menubar.Label != "🔇 shh" || cfg.Summarizer.Backend != "ollama" {
		t.Errorf("menubar=%q summarizer=%q", cfg.Menubar.Label, cfg.Summarizer.Backend)
	}
}

func TestStyleResolutionLayersKindThenSource(t *testing.T) {
	cfg := load(t, `{
  "alert": {
    "color": 233,
    "sound": "Glass",
    "size": "small",
    "calendar": {"color": 39, "sound": "Hero"},
    "source": {
      "Slack": {"sound": "none", "position": "bottom-left"},
      "Outlook": {"color": 235}
    }
  }
}`)
	tests := []struct {
		name   string
		kind   config.Kind
		source string
		want   config.Style
	}{
		{"plain message, unknown source", config.KindMessage, "Teams",
			config.Style{Position: "top-right", Size: "small", Color: 233, TextColor: 15, DurationSecs: 0, Sound: "Glass"}},
		{"calendar kind overrides the base", config.KindCalendar, "",
			config.Style{Position: "top-right", Size: "small", Color: 39, TextColor: 15, DurationSecs: 0, Sound: "Hero"}},
		{"source override applies to messages", config.KindMessage, "Slack",
			config.Style{Position: "bottom-left", Size: "small", Color: 233, TextColor: 15, DurationSecs: 0, Sound: "none"}},
		{"source wins over kind, other kind fields still apply", config.KindCalendar, "Slack",
			config.Style{Position: "bottom-left", Size: "small", Color: 39, TextColor: 15, DurationSecs: 0, Sound: "none"}},
		{"source color wins over kind color", config.KindCalendar, "Outlook",
			config.Style{Position: "top-right", Size: "small", Color: 235, TextColor: 15, DurationSecs: 0, Sound: "Hero"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.Alert.Resolve(tt.kind, tt.source); got != tt.want {
				t.Errorf("Resolve(%q, %q) = %+v, want %+v", tt.kind, tt.source, got, tt.want)
			}
		})
	}
}

func TestPathsCanBeOverridden(t *testing.T) {
	custom := t.TempDir()
	cfg := load(t, `{"paths": {"dir": "`+custom+`", "socket": "/tmp/shh-custom.sock", "db": "data/notes.db"}}`)
	want := config.Paths{
		Dir: custom, DB: filepath.Join(custom, "data", "notes.db"), Socket: "/tmp/shh-custom.sock", PID: filepath.Join(custom, "shh.pid"),
	}
	if cfg.Paths != want {
		t.Errorf("paths = %+v, want %+v (relative paths are relative to dir)", cfg.Paths, want)
	}
}

func TestInvalidValuesNameTheKey(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantKey string
	}{
		{"unknown position", `{"alert": {"position": "middle"}}`, "alert.position"},
		{"unknown size", `{"alert": {"size": "huge"}}`, "alert.size"},
		{"color above 255", `{"alert": {"color": 256}}`, "alert.color"},
		{"a negative color", `{"alert": {"color": -1}}`, "alert.color"},
		{"color that is a name", `{"alert": {"color": "orange"}}`, "alert.color"},
		{"color that is a hex string", `{"alert": {"color": "#ff9f0a"}}`, "alert.color"},
		{"negative duration", `{"alert": {"duration_secs": -1}}`, "alert.duration_secs"},
		{"duration of the wrong type", `{"alert": {"duration_secs": "soon"}}`, "alert.duration_secs"},
		{"empty sound", `{"alert": {"sound": ""}}`, "alert.sound"},
		{"bad calendar override", `{"alert": {"calendar": {"position": "nowhere"}}}`, "alert.calendar.position"},
		{"bad per-source override", `{"alert": {"source": {"Slack": {"color": "red"}}}}`, "alert.source.Slack.color"},
		{"unknown summarizer backend", `{"summarizer": {"backend": "gpt"}}`, "summarizer.backend"},
		{"label of the wrong type", `{"menubar": {"label": 3}}`, "menubar.label"},
		{"empty path", `{"paths": {"db": ""}}`, "paths.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(write(t, tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), `"`+tt.wantKey+`"`) {
				t.Errorf("error %q does not name the key %q", err, tt.wantKey)
			}
		})
	}
}

func TestUnknownKeysAreErrorsThatNameThem(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantKey string
	}{
		{"misspelled key", `{"alert": {"colour": 15}}`, "alert.colour"},
		{"unknown section", `{"alrt": {"color": 15}}`, "alrt"},
		{"unknown key in a per-source override", `{"alert": {"source": {"Slack": {"volume": 3}}}}`, "alert.source.Slack.volume"},
		{"unknown menubar key", `{"menubar": {"lable": "x"}}`, "menubar.lable"},
		{"unknown top-level key", `{"theme": "dark"}`, "theme"},
		{"a key with the wrong capitalisation", `{"alert": {"Color": 15}}`, "alert.Color"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(write(t, tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			msg := err.Error()
			if !strings.Contains(msg, "unknown key") || !strings.Contains(msg, `"`+tt.wantKey+`"`) {
				t.Errorf("error %q should say 'unknown key' and name %q", msg, tt.wantKey)
			}
		})
	}
}

func TestErrorsPointAtTheFileAndLine(t *testing.T) {
	path := write(t, "{\n  \"alert\": {\n    // the second key is a typo\n    \"color\": 15,\n    \"colour\": \"x\"\n  }\n}\n")
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load succeeded")
	}
	if !strings.Contains(err.Error(), path+":5") {
		t.Errorf("error %q should point at %s line 5 (comments do not shift the lines)", err, path)
	}
}

func TestBrokenJSONIsReportedWithTheFile(t *testing.T) {
	path := write(t, "{\n  \"alert\": {\"color\": 1\n")
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want one that mentions %s", err, path)
	}
}

func TestAPathThatIsADirectoryIsAnError(t *testing.T) {
	if _, err := config.Load(t.TempDir()); err == nil {
		t.Fatal("Load of a directory succeeded")
	}
}

func TestEffectiveConfigRoundTrips(t *testing.T) {
	original := load(t, `{
  "alert": {
    "color": 33,
    "size": "large",
    "calendar": {"sound": "Hero"},
    "source": {"Slack": {"sound": "none"}}
  },
  "menubar": {"label": "focus"},
  "summarizer": {"backend": "claude"}
}`)
	shown, err := original.Effective()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shown), `"color": 33`) || !strings.Contains(string(shown), `"menubar"`) {
		t.Fatalf("effective config looks wrong:\n%s", shown)
	}
	reloaded, err := config.Load(write(t, string(shown)))
	if err != nil {
		t.Fatalf("effective config does not load back: %v\n%s", err, shown)
	}
	for _, source := range []string{"", "Slack", "Teams"} {
		for _, kind := range []config.Kind{config.KindMessage, config.KindCalendar} {
			if got, want := reloaded.Alert.Resolve(kind, source), original.Alert.Resolve(kind, source); got != want {
				t.Errorf("Resolve(%q, %q) after round trip = %+v, want %+v", kind, source, got, want)
			}
		}
	}
	if reloaded.Menubar != original.Menubar || reloaded.Summarizer != original.Summarizer {
		t.Errorf("round trip changed menubar or summarizer: %+v %+v", reloaded.Menubar, reloaded.Summarizer)
	}
	if reloaded.Paths != original.Paths {
		t.Errorf("paths after round trip = %+v, want %+v", reloaded.Paths, original.Paths)
	}
}

func TestDefaultPathLivesUnderTheHomeConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "shh", "config.json"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestCommentsAndTrailingCommasWorkInTheConfigFile(t *testing.T) {
	cfg := load(t, `// my shh settings
{
  "alert": {
    "color": 41, // green
    "position": "bottom-left",
  },
}
`)
	got := cfg.Alert.Resolve(config.KindMessage, "")
	if got.Color != 41 || got.Position != "bottom-left" {
		t.Errorf("style = %+v, want the commented file read as written", got)
	}
}

func types(buttons []config.Button) []string {
	var out []string
	for _, b := range buttons {
		out = append(out, b.Type)
	}
	return out
}

func enabledTypes(buttons []config.Button) []string {
	var out []string
	for _, b := range buttons {
		if b.Enabled {
			out = append(out, b.Type)
		}
	}
	return out
}

func TestDefaultButtonsListEveryKnownTypeWithTrainOff(t *testing.T) {
	cfg := load(t, "")
	got := cfg.Alert.ButtonsFor(config.KindMessage, "")
	if want := []string{"preview", "goto", "dismiss", "flag", "train"}; strings.Join(types(got), ",") != strings.Join(want, ",") {
		t.Fatalf("button types = %v, want %v", types(got), want)
	}
	if want := "preview,goto,dismiss,flag"; strings.Join(enabledTypes(got), ",") != want {
		t.Errorf("enabled = %v, want %s (train is off until task 45)", enabledTypes(got), want)
	}
	if got[0].Label != "Preview" || got[1].Label != "Go to" || got[2].Label != "Dismiss" {
		t.Errorf("default labels = %+v", got)
	}
}

func TestButtonOrderIsTheListOrderAndMissingTypesComeBackDisabled(t *testing.T) {
	cfg := load(t, `{"alert": {"buttons": [{"type": "dismiss"}, {"type": "goto", "label": "Open"}]}}`)
	got := cfg.Alert.ButtonsFor(config.KindMessage, "")
	if want := "dismiss,goto,preview,flag,train"; strings.Join(types(got), ",") != want {
		t.Fatalf("order = %v, want %s (listed first, in the user's order; the rest appended)", types(got), want)
	}
	if want := "dismiss,goto"; strings.Join(enabledTypes(got), ",") != want {
		t.Errorf("enabled = %v, want %s (a type left out is disabled)", enabledTypes(got), want)
	}
	if got[1].Label != "Open" || got[0].Label != "Dismiss" {
		t.Errorf("labels = %q, %q; want an unset label to take the default", got[0].Label, got[1].Label)
	}
}

func TestButtonCanBeDisabledExplicitly(t *testing.T) {
	cfg := load(t, `{"alert": {"buttons": [{"type": "preview", "enabled": false}]}}`)
	got := cfg.Alert.ButtonsFor(config.KindMessage, "")
	if strings.Contains(strings.Join(enabledTypes(got), ","), "preview") {
		t.Errorf("preview enabled in %v", got)
	}
	if got[0].Type != "preview" || got[0].Enabled {
		t.Errorf("first = %+v, want the disabled preview", got[0])
	}
}

func TestButtonOverridesReplaceTheWholeList(t *testing.T) {
	cfg := load(t, `{"alert": {
	  "buttons": [{"type": "dismiss"}],
	  "calendar": {"buttons": [{"type": "goto"}]},
	  "source": {"Slack": {"buttons": [{"type": "preview"}]}}
	}}`)
	tests := []struct {
		kind   config.Kind
		source string
		want   string
	}{
		{config.KindMessage, "Teams", "dismiss"},
		{config.KindCalendar, "Teams", "goto"},
		{config.KindMessage, "Slack", "preview"},
		{config.KindCalendar, "Slack", "preview"},
	}
	for _, tt := range tests {
		got := enabledTypes(cfg.Alert.ButtonsFor(tt.kind, tt.source))
		if strings.Join(got, ",") != tt.want {
			t.Errorf("ButtonsFor(%q, %q) enabled = %v, want %s", tt.kind, tt.source, got, tt.want)
		}
	}
}

func TestInvalidButtonsNameTheKey(t *testing.T) {
	tests := []struct {
		name, content, wantKey string
	}{
		{"unknown type", `{"alert": {"buttons": [{"type": "snooze"}]}}`, "alert.buttons[0].type"},
		{"missing type", `{"alert": {"buttons": [{"label": "x"}]}}`, "alert.buttons[0].type"},
		{"duplicate type", `{"alert": {"buttons": [{"type": "goto"}, {"type": "goto"}]}}`, "alert.buttons[1].type"},
		{"empty label", `{"alert": {"buttons": [{"type": "goto", "label": ""}]}}`, "alert.buttons[0].label"},
		{"bad calendar override", `{"alert": {"calendar": {"buttons": [{"type": "nope"}]}}}`, "alert.calendar.buttons[0].type"},
		{"bad source override", `{"alert": {"source": {"Slack": {"buttons": [{"type": "nope"}]}}}}`, "alert.source.Slack.buttons[0].type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(write(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), `"`+tt.wantKey+`"`) {
				t.Errorf("err = %v, want one naming %q", err, tt.wantKey)
			}
		})
	}
}

func TestUnknownKeyInAButtonIsAnError(t *testing.T) {
	_, err := config.Load(write(t, `{"alert": {"buttons": [{"type": "goto", "colour": "red"}]}}`))
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("err = %v, want an unknown key error", err)
	}
}

func TestEffectiveConfigKeepsButtons(t *testing.T) {
	original := load(t, `{"alert": {
	  "buttons": [{"type": "dismiss", "label": "Close"}, {"type": "preview", "enabled": false}],
	  "source": {"Slack": {"buttons": [{"type": "goto"}]}}
	}}`)
	shown, err := original.Effective()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(write(t, string(shown)))
	if err != nil {
		t.Fatalf("effective config does not load back: %v\n%s", err, shown)
	}
	for _, source := range []string{"", "Slack"} {
		for _, kind := range []config.Kind{config.KindMessage, config.KindCalendar} {
			got, want := reloaded.Alert.ButtonsFor(kind, source), original.Alert.ButtonsFor(kind, source)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("ButtonsFor(%q, %q) after round trip = %v, want %v", kind, source, got, want)
			}
		}
	}
	if !strings.Contains(string(shown), `"train"`) {
		t.Errorf("effective config should list every button, including train:\n%s", shown)
	}
}

func TestAlertsStayUntilDismissedUnlessATimeoutIsConfigured(t *testing.T) {
	if got := load(t, "").Alert.Resolve(config.KindMessage, "").DurationSecs; got != 0 {
		t.Errorf("default duration = %d, want 0 (stay until dismissed)", got)
	}
	cfg := load(t, `{"alert": {"calendar": {"duration_secs": 30}, "source": {"Slack": {"duration_secs": 5}}}}`)
	for _, tt := range []struct {
		kind   config.Kind
		source string
		want   int
	}{{config.KindMessage, "Teams", 0}, {config.KindCalendar, "Teams", 30}, {config.KindMessage, "Slack", 5}} {
		if got := cfg.Alert.Resolve(tt.kind, tt.source).DurationSecs; got != tt.want {
			t.Errorf("duration for %s/%s = %d, want %d", tt.kind, tt.source, got, tt.want)
		}
	}
}

func TestPreviewHasACollapseLabelThatCanBeCustomised(t *testing.T) {
	def := load(t, "").Alert.ButtonsFor(config.KindMessage, "")
	if def[0].Type != "preview" || def[0].CollapseLabel != "Collapse" {
		t.Errorf("default preview = %+v, want collapse label Collapse", def[0])
	}
	if def[1].CollapseLabel != "" {
		t.Errorf("only preview has a collapse label, goto has %q", def[1].CollapseLabel)
	}
	custom := load(t, `{"alert": {"buttons": [{"type": "preview", "label": "More", "collapse_label": "Less"}]}}`).Alert.ButtonsFor(config.KindMessage, "")
	if custom[0].Label != "More" || custom[0].CollapseLabel != "Less" {
		t.Errorf("custom preview = %+v", custom[0])
	}
}

func TestCollapseLabelErrors(t *testing.T) {
	tests := []struct{ name, content, wantKey string }{
		{"on another button", `{"alert": {"buttons": [{"type": "goto", "collapse_label": "x"}]}}`, "alert.buttons[0].collapse_label"},
		{"empty", `{"alert": {"buttons": [{"type": "preview", "collapse_label": ""}]}}`, "alert.buttons[0].collapse_label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(write(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), `"`+tt.wantKey+`"`) {
				t.Errorf("err = %v, want one naming %q", err, tt.wantKey)
			}
		})
	}
}

func TestCollapseLabelSurvivesTheEffectiveRoundTrip(t *testing.T) {
	original := load(t, `{"alert": {"buttons": [{"type": "preview", "collapse_label": "Less"}]}}`)
	shown, err := original.Effective()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(write(t, string(shown)))
	if err != nil {
		t.Fatalf("%v\n%s", err, shown)
	}
	if got := reloaded.Alert.ButtonsFor(config.KindMessage, "")[0].CollapseLabel; got != "Less" {
		t.Errorf("collapse label after round trip = %q, want Less", got)
	}
}

func TestTheMenuBarItemIsOnByDefaultAndCanBeTurnedOff(t *testing.T) {
	if !load(t, "").Menubar.Enabled {
		t.Error("the menu bar item should be on by default")
	}
	cfg := load(t, `{"menubar": {"enabled": false, "label": "focus"}}`)
	if cfg.Menubar.Enabled || cfg.Menubar.Label != "focus" {
		t.Errorf("menubar = %+v", cfg.Menubar)
	}
	shown, err := cfg.Effective()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(write(t, string(shown)))
	if err != nil || reloaded.Menubar != cfg.Menubar {
		t.Errorf("round trip: %+v, %v", reloaded.Menubar, err)
	}
	if _, err := config.Load(write(t, `{"menubar": {"enabled": "yes"}}`)); err == nil || !strings.Contains(err.Error(), `"menubar.enabled"`) {
		t.Errorf("err = %v, want one naming menubar.enabled", err)
	}
}

func TestTheFlagButtonIsOnByDefaultAfterDismissAndHasAFlaggedLabel(t *testing.T) {
	got := load(t, "").Alert.ButtonsFor(config.KindMessage, "")
	var flag config.Button
	for _, b := range got {
		if b.Type == "flag" {
			flag = b
		}
	}
	if !flag.Enabled || flag.Label != "Flag" || flag.FlaggedLabel != "Flagged" {
		t.Errorf("default flag button = %+v, want it on, labelled Flag, saying Flagged once pressed", flag)
	}
	if got[3].Type != "flag" {
		t.Errorf("flag sits at %d, want right after dismiss", 3)
	}
	for _, b := range got {
		if b.Type != "flag" && b.FlaggedLabel != "" {
			t.Errorf("%s has a flagged label %q; only the flag button does", b.Type, b.FlaggedLabel)
		}
	}
	custom := load(t, `{"alert": {"buttons": [{"type": "flag", "label": "Review later", "flagged_label": "Noted"}]}}`).Alert.ButtonsFor(config.KindMessage, "")
	if custom[0].Label != "Review later" || custom[0].FlaggedLabel != "Noted" {
		t.Errorf("custom flag button = %+v", custom[0])
	}
}

func TestFlaggedLabelErrorsAndRoundTrip(t *testing.T) {
	for name, content := range map[string]string{
		"on another button": `{"alert": {"buttons": [{"type": "goto", "flagged_label": "x"}]}}`,
		"empty":             `{"alert": {"buttons": [{"type": "flag", "flagged_label": ""}]}}`,
	} {
		_, err := config.Load(write(t, content))
		if err == nil || !strings.Contains(err.Error(), `"alert.buttons[0].flagged_label"`) {
			t.Errorf("%s: err = %v, want one naming alert.buttons[0].flagged_label", name, err)
		}
	}
	original := load(t, `{"alert": {"buttons": [{"type": "flag", "flagged_label": "Noted"}]}}`)
	shown, err := original.Effective()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(write(t, string(shown)))
	if err != nil {
		t.Fatalf("%v\n%s", err, shown)
	}
	if got := reloaded.Alert.ButtonsFor(config.KindMessage, "")[0].FlaggedLabel; got != "Noted" {
		t.Errorf("flagged label after round trip = %q", got)
	}
}
