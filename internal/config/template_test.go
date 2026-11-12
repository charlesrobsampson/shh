package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
)

func TestTheTemplateIsExactlyTheDefaults(t *testing.T) {
	dir := t.TempDir()
	withTemplate := filepath.Join(dir, "template", "config.json")
	if err := os.MkdirAll(filepath.Dir(withTemplate), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(withTemplate, []byte(config.Template), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(withTemplate)
	if err != nil {
		t.Fatalf("the template does not load: %v", err)
	}
	empty := filepath.Join(dir, "empty", "config.json") // no file at all
	want, err := config.Load(empty)
	if err != nil {
		t.Fatal(err)
	}
	// The buttons are written out in the template and left to their defaults in no file: compare them as they resolve. The paths follow the
	// folder each is in. Everything else must be identical.
	if g, w := got.Alert.ButtonsFor(config.KindMessage, ""), want.Alert.ButtonsFor(config.KindMessage, ""); !reflect.DeepEqual(g, w) {
		t.Errorf("the buttons in the template resolve to\n%+v\nbut the defaults are\n%+v", g, w)
	}
	got.Alert.Buttons, want.Alert.Buttons = nil, nil
	got.Paths, want.Paths = config.Paths{}, config.Paths{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loading the template gave\n%+v\nbut no file gives\n%+v", got, want)
	}
}

func TestTheTemplateNamesEveryOptionTheConfigurationHas(t *testing.T) {
	for _, key := range []string{`"position"`, `"size"`, `"color"`, `"text_color"`, `"button_color"`, `"button_text_color"`, `"duration_secs"`, `"sound"`, `"buttons"`,
		`"calendar"`, `"source"`, `"menubar"`, `"enabled"`, `"label"`, `"summarizer"`, `"backend"`, `"paths"`, `"dir"`, `"db"`, `"socket"`, `"pid"`,
		`"collapse_label"`, `"flagged_label"`, `"trained_label"`, `"preview"`, `"goto"`, `"dismiss"`, `"flag"`, `"train"`,
		"https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit", "shh config show", "shh guide config"} {
		if !strings.Contains(config.Template, key) {
			t.Errorf("the template never mentions %s", key)
		}
	}
}

func TestTheCommentedOptionsAreValidWhenUncommented(t *testing.T) {
	text := config.Template
	for _, line := range []string{`// "button_color": 238,`, `// "button_text_color": 15,`} {
		text = strings.Replace(text, line, strings.TrimPrefix(line, "// "), 1)
	}
	text = strings.Replace(text, `// , "calendar"`, `, "calendar"`, 1)
	text = strings.Replace(text, `// , "source"`, `, "source"`, 1)
	text = strings.Replace(text, `// , "paths": { "dir": "~/.config/shh", "db"`, `, "paths": { "dir": "shh-files", "db"`, 1)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the commented options, uncommented, do not load: %v\n%s", err, text)
	}
	if cfg.Alert.Base.ButtonColor != 238 || cfg.Alert.Base.ButtonTextColor != 15 || cfg.Alert.Calendar.Color == nil || len(cfg.Alert.Source) != 2 {
		t.Errorf("alert = %+v", cfg.Alert)
	}
}

func TestWriteTemplateCreatesTheFileOnceAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "shh", "config.json")
	created, err := config.WriteTemplate(path)
	if err != nil || !created {
		t.Fatalf("created %v err %v", created, err)
	}
	if got, _ := os.ReadFile(path); string(got) != config.Template {
		t.Error("the file should be the template")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0o700 {
		t.Errorf("folder mode = %v", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte(`{"alert": {"color": 33}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err = config.WriteTemplate(path)
	if err != nil || created {
		t.Errorf("an existing file is left alone: created %v err %v", created, err)
	}
	if got, _ := os.ReadFile(path); string(got) != `{"alert": {"color": 33}}` {
		t.Errorf("the file was changed: %q", got)
	}
}
