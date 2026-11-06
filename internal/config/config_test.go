package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
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
	cfg, err := config.Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Style{Position: "top-right", Size: "medium", Color: "#ff9f0a", DurationSecs: 8, Sound: "Glass"}
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
	if got := cfg.Alert.Resolve(config.KindMessage, "").Color; got != "#ff9f0a" {
		t.Errorf("color = %q, want the default", got)
	}
}

func TestPartialFileMergesOverDefaults(t *testing.T) {
	cfg := load(t, `
[alert]
color = "#112233"
duration_secs = 0

[menubar]
label = "🔇 shh"

[summarizer]
backend = "ollama"
`)
	got := cfg.Alert.Resolve(config.KindMessage, "")
	want := config.Style{Position: "top-right", Size: "medium", Color: "#112233", DurationSecs: 0, Sound: "Glass"}
	if got != want {
		t.Errorf("alert style = %+v, want %+v (only color and duration change; 0 means until dismissed)", got, want)
	}
	if cfg.Menubar.Label != "🔇 shh" || cfg.Summarizer.Backend != "ollama" {
		t.Errorf("menubar=%q summarizer=%q", cfg.Menubar.Label, cfg.Summarizer.Backend)
	}
}

func TestStyleResolutionLayersKindThenSource(t *testing.T) {
	cfg := load(t, `
[alert]
color = "#111111"
sound = "Glass"
size = "small"

[alert.calendar]
color = "#0a84ff"
sound = "Hero"

[alert.source.Slack]
sound = "none"
position = "bottom-left"

[alert.source.Outlook]
color = "#222222"
`)
	tests := []struct {
		name   string
		kind   config.Kind
		source string
		want   config.Style
	}{
		{"plain message, unknown source", config.KindMessage, "Teams",
			config.Style{Position: "top-right", Size: "small", Color: "#111111", DurationSecs: 8, Sound: "Glass"}},
		{"calendar kind overrides the base", config.KindCalendar, "",
			config.Style{Position: "top-right", Size: "small", Color: "#0a84ff", DurationSecs: 8, Sound: "Hero"}},
		{"source override applies to messages", config.KindMessage, "Slack",
			config.Style{Position: "bottom-left", Size: "small", Color: "#111111", DurationSecs: 8, Sound: "none"}},
		{"source wins over kind, other kind fields still apply", config.KindCalendar, "Slack",
			config.Style{Position: "bottom-left", Size: "small", Color: "#0a84ff", DurationSecs: 8, Sound: "none"}},
		{"source color wins over kind color", config.KindCalendar, "Outlook",
			config.Style{Position: "top-right", Size: "small", Color: "#222222", DurationSecs: 8, Sound: "Hero"}},
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
	cfg := load(t, `
[paths]
dir = "`+custom+`"
socket = "/tmp/shh-custom.sock"
db = "data/notes.db"
`)
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
		{"unknown position", "[alert]\nposition = \"middle\"\n", "alert.position"},
		{"unknown size", "[alert]\nsize = \"huge\"\n", "alert.size"},
		{"color without a hash", "[alert]\ncolor = \"ff9f0a\"\n", "alert.color"},
		{"color that is a name", "[alert]\ncolor = \"orange\"\n", "alert.color"},
		{"negative duration", "[alert]\nduration_secs = -1\n", "alert.duration_secs"},
		{"duration of the wrong type", "[alert]\nduration_secs = \"soon\"\n", "alert.duration_secs"},
		{"empty sound", "[alert]\nsound = \"\"\n", "alert.sound"},
		{"bad calendar override", "[alert.calendar]\nposition = \"nowhere\"\n", "alert.calendar.position"},
		{"bad per-source override", "[alert.source.Slack]\ncolor = \"red\"\n", "alert.source.Slack.color"},
		{"unknown summarizer backend", "[summarizer]\nbackend = \"gpt\"\n", "summarizer.backend"},
		{"label of the wrong type", "[menubar]\nlabel = 3\n", "menubar.label"},
		{"empty path", "[paths]\ndb = \"\"\n", "paths.db"},
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
		{"misspelled key", "[alert]\ncolour = \"#ffffff\"\n", "alert.colour"},
		{"unknown section", "[alrt]\ncolor = \"#ffffff\"\n", "alrt"},
		{"unknown key in a per-source override", "[alert.source.Slack]\nvolume = 3\n", "alert.source.Slack.volume"},
		{"unknown menubar key", "[menubar]\nlable = \"x\"\n", "menubar.lable"},
		{"unknown top-level key", "theme = \"dark\"\n", "theme"},
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
	path := write(t, "[alert]\ncolor = \"#ffffff\"\ncolour = \"x\"\n")
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load succeeded")
	}
	if !strings.Contains(err.Error(), path+":3") {
		t.Errorf("error %q should point at %s line 3", err, path)
	}
}

func TestBrokenTOMLIsReportedWithTheFile(t *testing.T) {
	path := write(t, "[alert\ncolor = 1")
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
	original := load(t, `
[alert]
color = "#112233"
size = "large"

[alert.calendar]
sound = "Hero"

[alert.source.Slack]
sound = "none"

[menubar]
label = "focus"

[summarizer]
backend = "claude"
`)
	shown, err := original.Effective()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shown), "#112233") || !strings.Contains(string(shown), "[menubar]") {
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
	if want := filepath.Join(home, ".config", "shh", "config.toml"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
