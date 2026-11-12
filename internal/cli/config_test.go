package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
)

func TestInitWritesTheDocumentedConfigOnceAndSaysWhatToDoNext(t *testing.T) {
	r := newRig(t, false)
	r.env.ConfigPath = filepath.Join(t.TempDir(), "shh", "config.json")
	out := r.mustRun("init")
	for _, want := range []string{"Created " + r.env.ConfigPath, "every option at its default", "shh guide config", "shh worker start", "shh routine example", "shh alert test", "shh guide"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output is missing %q:\n%s", want, out)
		}
	}
	got, err := os.ReadFile(r.env.ConfigPath)
	if err != nil || string(got) != config.Template {
		t.Fatalf("the file should be the template: %v", err)
	}
	if _, err := config.Load(r.env.ConfigPath); err != nil {
		t.Errorf("what init writes must load: %v", err)
	}
	if err := os.WriteFile(r.env.ConfigPath, []byte(`{"alert": {"color": 33}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out = r.mustRun("init")
	if !strings.Contains(out, "already exists, so it was left as it is") || !strings.Contains(out, "shh config defaults") {
		t.Errorf("second init:\n%s", out)
	}
	if got, _ := os.ReadFile(r.env.ConfigPath); string(got) != `{"alert": {"color": 33}}` {
		t.Errorf("an existing config was changed: %q", got)
	}
}

func TestInitNeedsToKnowWhereTheConfigurationIs(t *testing.T) {
	r := newRig(t, false)
	if code := r.run("init"); code == 0 || !strings.Contains(r.err.String(), "does not know where the configuration lives") {
		t.Errorf("code %d stderr %q", code, r.err.String())
	}
}

func TestConfigPathShowAndDefaults(t *testing.T) {
	r := newRig(t, false)
	r.env.ConfigPath = "/somewhere/config.json"
	if out := r.mustRun("config", "path"); strings.TrimSpace(out) != "/somewhere/config.json" {
		t.Errorf("path = %q", out)
	}
	r.env.ConfigShow = func() ([]byte, error) { return []byte("{\"alert\": {}}\n"), nil }
	if out := r.mustRun("config", "show"); out != "{\"alert\": {}}\n" {
		t.Errorf("show = %q", out)
	}
	if out := r.mustRun("config", "defaults"); out != config.Template {
		t.Error("defaults should print the template")
	}
	r.env.ConfigShow = nil
	if code := r.run("config", "show"); code == 0 {
		t.Error("show with nothing to show is an error")
	}
}
