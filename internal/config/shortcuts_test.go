package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
)

func TestShortcutsDefaults(t *testing.T) {
	got := load(t, "").Alert.Shortcuts
	want := config.Shortcuts{
		Enabled:     true,
		Modifier:    config.Ctrl | config.Alt,
		ShowInLabel: true,
		Keys: map[string]string{
			"next": "]", "previous": "[", "clear": "escape", "dismiss_all": "x",
			"preview": "p", "goto": "g", "dismiss": "d", "flag": "f", "train": "n",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default shortcuts = %+v, want %+v", got, want)
	}
}

func TestShortcutsFromTheFileMergeOverTheDefaults(t *testing.T) {
	got := load(t, `{"alert": {"shortcuts": {"modifier": "Cmd+Shift", "show_in_label": false, "keys": {"dismiss": "w", "goto": "return"}}}}`).Alert.Shortcuts
	if got.Modifier != config.Cmd|config.Shift || got.ShowInLabel || !got.Enabled {
		t.Errorf("shortcuts = %+v", got)
	}
	if got.Keys["dismiss"] != "w" || got.Keys["goto"] != "return" || got.Keys["next"] != "]" {
		t.Errorf("keys = %v: the two given should change and the rest stay", got.Keys)
	}
}

func TestShortcutsCanBeSwitchedOffAsAWhole(t *testing.T) {
	if load(t, `{"alert": {"shortcuts": {"enabled": false}}}`).Alert.Shortcuts.Enabled {
		t.Error("enabled: false should switch them off")
	}
}

func TestAnEmptyKeySwitchesOffThatOneShortcut(t *testing.T) {
	got := load(t, `{"alert": {"shortcuts": {"keys": {"train": ""}}}}`).Alert.Shortcuts
	if key, ok := got.Keys["train"]; !ok || key != "" {
		t.Errorf("train = %q, %v: it should be there and empty", key, ok)
	}
	// its old key is free for another
	load(t, `{"alert": {"shortcuts": {"keys": {"train": "", "flag": "n"}}}}`)
}

func TestModifiersAreReadInAnyOrderCaseAndSpelling(t *testing.T) {
	tests := []struct {
		in   string
		want config.Modifiers
		text string
	}{
		{"ctrl+alt", config.Ctrl | config.Alt, "ctrl+alt"},
		{"Alt+Control", config.Ctrl | config.Alt, "ctrl+alt"},
		{"option + command + shift", config.Alt | config.Shift | config.Cmd, "alt+shift+cmd"},
		{"cmd", config.Cmd, "cmd"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := config.ParseModifiers(tt.in)
			if err != nil || got != tt.want || got.String() != tt.text {
				t.Errorf("ParseModifiers(%q) = %v %q, %v; want %v %q", tt.in, got, got.String(), err, tt.want, tt.text)
			}
		})
	}
}

func TestInvalidShortcutsNameTheKey(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantKey string
		wantIn  string
	}{
		{"unknown modifier", `{"alert": {"shortcuts": {"modifier": "ctrl+hyper"}}}`, "alert.shortcuts.modifier", "hyper"},
		{"no modifier", `{"alert": {"shortcuts": {"modifier": ""}}}`, "alert.shortcuts.modifier", "at least one"},
		{"a modifier twice", `{"alert": {"shortcuts": {"modifier": "ctrl+control"}}}`, "alert.shortcuts.modifier", "twice"},
		{"unknown shortcut", `{"alert": {"shortcuts": {"keys": {"nxt": "]"}}}}`, "alert.shortcuts.keys.nxt", "next"},
		{"unknown key name", `{"alert": {"shortcuts": {"keys": {"dismiss": "banana"}}}}`, "alert.shortcuts.keys.dismiss", "banana"},
		{"one key twice", `{"alert": {"shortcuts": {"keys": {"goto": "d"}}}}`, "alert.shortcuts.keys.goto", `"dismiss"`},
		{"a reserved digit", `{"alert": {"shortcuts": {"keys": {"flag": "3"}}}}`, "alert.shortcuts.keys.flag", "1 to 9"},
		{"enabled of the wrong type", `{"alert": {"shortcuts": {"enabled": "yes"}}}`, "alert.shortcuts.enabled", ""},
		{"a key of the wrong type", `{"alert": {"shortcuts": {"keys": {"flag": 5}}}}`, "alert.shortcuts.keys.flag", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(write(t, tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), `"`+tt.wantKey+`"`) || !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error %q should name %q and mention %q", err, tt.wantKey, tt.wantIn)
			}
		})
	}
}

func TestShortcutsBelongToTheWholeAlertSectionNotToAKindOrAnApp(t *testing.T) {
	for _, content := range []string{
		`{"alert": {"calendar": {"shortcuts": {}}}}`,
		`{"alert": {"source": {"Slack": {"shortcuts": {}}}}}`,
	} {
		_, err := config.Load(write(t, content))
		if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "shortcuts") {
			t.Errorf("%s: err = %v, want an unknown key error naming shortcuts", content, err)
		}
	}
}

func TestOnlyOptionChordsGetAWarning(t *testing.T) {
	tests := []struct {
		modifier string
		warns    bool
	}{
		{"ctrl+alt", false}, {"alt", true}, {"alt+shift", true}, {"ctrl+alt+shift", false}, {"cmd+alt", false}, {"ctrl", false},
	}
	for _, tt := range tests {
		warnings := load(t, `{"alert": {"shortcuts": {"modifier": "`+tt.modifier+`"}}}`).Alert.Shortcuts.Warnings()
		if (len(warnings) > 0) != tt.warns {
			t.Errorf("modifier %q: warnings = %v, want a warning: %v", tt.modifier, warnings, tt.warns)
		}
		for _, w := range warnings {
			if !strings.Contains(w, "alert.shortcuts.modifier") {
				t.Errorf("warning %q does not name the key", w)
			}
		}
	}
	if w := load(t, `{"alert": {"shortcuts": {"enabled": false, "modifier": "alt"}}}`).Alert.Shortcuts.Warnings(); len(w) != 0 {
		t.Errorf("shortcuts that are off need no warning: %v", w)
	}
}

func TestSelectedColorDefaultsToWhiteAndOverridesLikeTheOtherColors(t *testing.T) {
	cfg := load(t, `{"alert": {"selected_color": 226, "calendar": {"selected_color": 39}, "source": {"Slack": {"selected_color": 196}}}}`)
	tests := []struct {
		kind   config.Kind
		source string
		want   int
	}{
		{config.KindMessage, "", 226}, {config.KindCalendar, "", 39}, {config.KindMessage, "Slack", 196}, {config.KindCalendar, "Slack", 196},
	}
	for _, tt := range tests {
		if got := cfg.Alert.Resolve(tt.kind, tt.source).SelectedColor; got != tt.want {
			t.Errorf("selected color for %s/%q = %d, want %d", tt.kind, tt.source, got, tt.want)
		}
	}
	if got := load(t, "").Alert.Resolve(config.KindMessage, "").SelectedColor; got != 15 {
		t.Errorf("default selected color = %d, want 15", got)
	}
}

func TestSelectedColorMustBeInThePalette(t *testing.T) {
	for _, content := range []string{
		`{"alert": {"selected_color": 256}}`,
		`{"alert": {"selected_color": -1}}`,
		`{"alert": {"calendar": {"selected_color": 300}}}`,
		`{"alert": {"source": {"Slack": {"selected_color": "red"}}}}`,
	} {
		if _, err := config.Load(write(t, content)); err == nil || !strings.Contains(err.Error(), "selected_color") {
			t.Errorf("%s: err = %v, want one naming selected_color", content, err)
		}
	}
}

func TestShortcutsAndSelectedColorSurviveTheEffectiveRoundTrip(t *testing.T) {
	original := load(t, `{"alert": {"selected_color": 226, "shortcuts": {"modifier": "shift+ctrl", "show_in_label": false, "keys": {"dismiss": "w", "train": ""}}}}`)
	shown, err := original.Effective()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(write(t, string(shown)))
	if err != nil {
		t.Fatalf("effective config does not load back: %v\n%s", err, shown)
	}
	if !reflect.DeepEqual(reloaded.Alert.Shortcuts, original.Alert.Shortcuts) {
		t.Errorf("shortcuts after a round trip = %+v, want %+v\n%s", reloaded.Alert.Shortcuts, original.Alert.Shortcuts, shown)
	}
	if got := reloaded.Alert.Base.SelectedColor; got != 226 {
		t.Errorf("selected color after a round trip = %d", got)
	}
	if !strings.Contains(string(shown), `"modifier": "ctrl+shift"`) {
		t.Errorf("the modifier should be written in its canonical form:\n%s", shown)
	}
}
