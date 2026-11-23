package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
)

// older is a config.json written by a shh that had fewer options: the person's own values and comments, no selected_color, no
// shortcuts and no classifier section.
const older = `// my shh settings
{
  "alert": {
    // How banners look.
    "position": "top-left",     // where they go
    "size": "medium",
    "color": 190,               // mine
    "text_color": 0,
    "button_color": 27,
    "button_text_color": 7,    // the buttons' text
    "duration_secs": 0,
    "sound": "none",

    "buttons": [
      { "type": "preview", "label": "Preview", "enabled": true },
      { "type": "train", "label": "meh", "enabled": true }  // I turned this on
    ]
  },

  "menubar": {
    "enabled": true,
    "label": "shh"
  },

  "summarizer": {
    "backend": "none"
  },

  "retention": {
    "ttl_days": 3,             // short
    "keep_untrained_days": 9,
    "auto_train": true,
    "auto_train_after_days": 3
  }
}
`

func keysOf(additions []config.Addition) []string {
	var out []string
	for _, a := range additions {
		out = append(out, a.Name())
	}
	return out
}

func TestUpgradeFindsWhatTheTemplateHasAndTheFileLacks(t *testing.T) {
	got, err := config.PlanUpgrade("config.json", []byte(older))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alert.selected_color", "alert.shortcuts", "classifier"}
	if strings.Join(keysOf(got), " ") != strings.Join(want, " ") {
		t.Errorf("missing = %v, want %v", keysOf(got), want)
	}
	for _, a := range got {
		if a.Name() == "alert.shortcuts" && (!strings.Contains(a.Text(), "dismiss_all") || !strings.Contains(a.Text(), "// Keyboard shortcuts for banners")) {
			t.Errorf("an added option should come with its comment and its whole value:\n%s", a.Text())
		}
	}
}

func TestUpgradeGoesDownIntoObjectsTheFileHas(t *testing.T) {
	raw := `{"alert": {"shortcuts": {"enabled": true, "modifier": "cmd", "keys": {"next": "]", "dismiss": "w"}}}}`
	got, err := config.PlanUpgrade("config.json", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Join(keysOf(got), " ")
	for _, want := range []string{"alert.shortcuts.show_in_label", "alert.shortcuts.keys.train", "alert.shortcuts.keys.previous", "menubar", "retention"} {
		if !strings.Contains(names, want) {
			t.Errorf("%s should be missing: %s", want, names)
		}
	}
	for _, not := range []string{"alert.shortcuts.keys.next", "alert.shortcuts.modifier", "alert.shortcuts.keys.dismiss"} {
		if strings.Contains(names+" ", not+" ") {
			t.Errorf("%s is there already: %s", not, names)
		}
	}
}

func TestAFileWithEverythingNeedsNothing(t *testing.T) {
	got, err := config.PlanUpgrade("config.json", []byte(config.Template))
	if err != nil || len(got) != 0 {
		t.Errorf("the template itself: %v, %v", keysOf(got), err)
	}
}

func TestUpgradeKeepsEverythingThePersonWroteAndChangesNothingInForce(t *testing.T) {
	plan, err := config.PlanUpgrade("config.json", []byte(older))
	if err != nil {
		t.Fatal(err)
	}
	out, err := config.ApplyUpgrade("config.json", []byte(older), plan)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, kept := range []string{"// my shh settings", `"position": "top-left",     // where they go`, `"color": 190,               // mine`,
		`{ "type": "train", "label": "meh", "enabled": true }  // I turned this on`, `"ttl_days": 3,             // short`} {
		if !strings.Contains(text, kept) {
			t.Errorf("the upgrade lost %q:\n%s", kept, text)
		}
	}
	before, err := config.ParseBytes("config.json", []byte(older))
	if err != nil {
		t.Fatal(err)
	}
	after, err := config.ParseBytes("config.json", out)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := before.Effective()
	a, _ := after.Effective()
	if string(a) != string(b) {
		t.Errorf("the configuration in force changed:\nbefore %s\nafter  %s", b, a)
	}
	if again, err := config.PlanUpgrade("config.json", out); err != nil || len(again) != 0 {
		t.Errorf("a second upgrade should find nothing: %v %v", keysOf(again), err)
	}
}

func TestANewOptionLandsBesideItsNeighbourInTheTemplate(t *testing.T) {
	plan, _ := config.PlanUpgrade("config.json", []byte(older))
	out, err := config.ApplyUpgrade("config.json", []byte(older), plan)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	i, j, k := strings.Index(text, `"text_color"`), strings.Index(text, `"selected_color"`), strings.Index(text, `"button_color"`)
	if !(i < j && j < k) {
		t.Errorf("selected_color should follow text_color, where the template has it, and come before button_color:\n%s", text)
	}
	if !strings.Contains(text, "\n\n  \"classifier\": {") {
		t.Errorf("a section the template sets apart with a blank line should be set apart:\n%s", text)
	}
	if !strings.Contains(text, `"selected_color": 15,       // the ring`) {
		t.Errorf("a comment after an option should keep the template's spacing:\n%s", text)
	}
	if line := text[strings.LastIndex(text[:j], "\n")+1:]; !strings.HasPrefix(line, "    \"selected_color\"") {
		t.Errorf("it should be indented like its neighbours: %q", line)
	}
}

func TestUpgradeAddsCommasWhereNeededAndKeepsTrailingOnes(t *testing.T) {
	for name, raw := range map[string]string{
		"no trailing comma":   "{\n  \"alert\": {\n    \"color\": 190\n  }\n}\n",
		"a trailing comma":    "{\n  \"alert\": {\n    \"color\": 190,\n  },\n}\n",
		"on one line":         `{"alert": {"color": 190}}`,
		"an empty object":     "{\n  \"alert\": {\n  }\n}\n",
		"an empty file":       "{}",
		"only a comment":      "// nothing yet\n{\n}\n",
		"a comment after one": "{\n  \"alert\": {\n    \"color\": 190 // mine, really\n  }\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := config.PlanUpgrade("config.json", []byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			out, err := config.ApplyUpgrade("config.json", []byte(raw), plan)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			after := load(t, string(out))
			if got := after.Alert.Base.Color; got != load(t, raw).Alert.Base.Color {
				t.Errorf("color changed to %d", got)
			}
			if again, _ := config.PlanUpgrade("config.json", out); len(again) != 0 {
				t.Errorf("still missing after the upgrade: %v\n%s", keysOf(again), out)
			}
		})
	}
}

func TestUpgradeRefusesAFileThatDoesNotLoad(t *testing.T) {
	raw := `{"alert": {"colour": 1}}`
	if _, err := config.PlanUpgrade("config.json", []byte(raw)); err == nil || !strings.Contains(err.Error(), "colour") {
		t.Errorf("err = %v, want the file's own error", err)
	}
}

func upgradePath(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpgradeOfAFileShowsTheChangeButWritesOnlyWhenAsked(t *testing.T) {
	path := upgradePath(t, older)
	res, err := config.Upgrade(path, false)
	if err != nil || len(res.Added) != 3 || res.Written {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if got, _ := os.ReadFile(path); string(got) != older {
		t.Error("the file was changed without --write")
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("nothing else should be written: %v", entries)
	}
}

func TestUpgradeWritesABackupAndKeepsTheFileMode(t *testing.T) {
	path := upgradePath(t, older)
	res, err := config.Upgrade(path, true)
	if err != nil || !res.Written || len(res.Added) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if backup, _ := os.ReadFile(res.Backup); string(backup) != older || res.Backup != path+".bak" {
		t.Errorf("backup %s holds %q", res.Backup, backup)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), `"classifier"`) {
		t.Error("the file was not upgraded")
	}
	if again, err := config.Upgrade(path, true); err != nil || again.Written || len(again.Added) != 0 {
		t.Errorf("a second run: %+v %v", again, err)
	}
}

func TestUpgradeNeverOverwritesAnEarlierBackup(t *testing.T) {
	path := upgradePath(t, older)
	if err := os.WriteFile(path+".bak", []byte("an older copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := config.Upgrade(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path + ".bak"); string(got) != "an older copy" || res.Backup == path+".bak" {
		t.Errorf("the earlier backup was touched, new one at %s", res.Backup)
	}
	if got, _ := os.ReadFile(res.Backup); string(got) != older {
		t.Errorf("the new backup holds %q", got)
	}
}

func TestUpgradeWithNoFileSaysToRunInit(t *testing.T) {
	_, err := config.Upgrade(filepath.Join(t.TempDir(), "config.json"), true)
	if err == nil || !strings.Contains(err.Error(), "shh init") {
		t.Errorf("err = %v", err)
	}
}

func TestUpgradeWritesNothingWhenTheFileDoesNotLoad(t *testing.T) {
	path := upgradePath(t, `{"alert": {"colour": 1}}`)
	if _, err := config.Upgrade(path, true); err == nil {
		t.Fatal("want an error")
	}
	if got, _ := os.ReadFile(path); string(got) != `{"alert": {"colour": 1}}` {
		t.Errorf("the file was changed: %q", got)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("no backup should be made: %v", entries)
	}
}

func TestCountMissingIsWhatDoctorReports(t *testing.T) {
	path := upgradePath(t, older)
	if n, err := config.CountMissing(path); err != nil || n != 3 {
		t.Errorf("CountMissing = %d, %v", n, err)
	}
	if n, err := config.CountMissing(filepath.Join(t.TempDir(), "none.json")); err != nil || n != 0 {
		t.Errorf("no file: %d, %v (the defaults are in force, so nothing is missing)", n, err)
	}
}
