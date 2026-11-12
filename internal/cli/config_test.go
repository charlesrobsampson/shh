package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/cli"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/doctor"
	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/store"
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

func TestInitStoresRecentNotificationsAsUnlabeledHistoryAndChecksAccess(t *testing.T) {
	r := newRig(t, true)
	r.env.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	r.env.Now = r.clock.Now
	r.env.Doctor = &doctor.Env{
		NotificationDB: func() (string, error) { return "", errors.New("operation not permitted") },
		Accessibility:  func() (bool, error) { return true, nil },
	}
	at := r.clock.Now()
	r.source.Add(ingest.Notification{BundleID: "com.microsoft.teams2", Title: "Ana", Body: "can you look at this", Delivered: at.Add(-2 * time.Hour)})
	r.source.Add(ingest.Notification{BundleID: "com.microsoft.teams2", Title: "Bo", Body: "too old", Delivered: at.Add(-5 * 24 * time.Hour)})
	r.source.Add(ingest.Notification{BundleID: "com.apple.mail", Title: "[shh] Banner", Body: "own", Delivered: at.Add(-time.Hour)})

	out := r.mustRun("init")
	if !strings.Contains(out, "FAIL  notification database") || !strings.Contains(out, "ok    accessibility") {
		t.Errorf("the permissions are checked:\n%s", out)
	}
	if !strings.Contains(out, "Stored 1 notification from the last 3d") {
		t.Errorf("out:\n%s", out)
	}
	all, _ := r.store.ListNotifications(store.Filter{})
	if len(all) != 1 || all[0].Source != "Teams" || all[0].IsTrained || !strings.HasPrefix(all[0].Reason, "imported from history") {
		t.Errorf("history = %+v", all)
	}
	if again := r.mustRun("init"); !strings.Contains(again, "Stored 0 notifications") {
		t.Errorf("a second init stores nothing new:\n%s", again)
	}
	if none := r.mustRun("init", "--history", "0"); strings.Contains(none, "Stored") {
		t.Errorf("--history 0 imports nothing:\n%s", none)
	}
	if code := r.run("init", "--history", "soon"); code != 2 {
		t.Errorf("a bad --history exited %d", code)
	}
}

func TestInitDoesNotDownloadAnythingButSaysHowToGetTheModel(t *testing.T) {
	r := newRig(t, false)
	r.env.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	r.env.Model = &cli.ModelOps{Install: func() (string, error) { t.Error("init must not download the model"); return "", nil }}
	if out := r.mustRun("init", "--history", "0"); !strings.Contains(out, "shh model install") || !strings.Contains(out, "optional") {
		t.Errorf("out:\n%s", out)
	}
}

func TestModelStatusInstallAndRemove(t *testing.T) {
	r := newRig(t, false)
	installed := false
	r.env.Model = &cli.ModelOps{
		Status: func() string {
			return map[bool]string{true: "installed at /x/m.bin", false: "not installed"}[installed]
		},
		Install: func() (string, error) { installed = true; return "/x/m.bin", nil },
		Remove: func() (bool, error) {
			was := installed
			installed = false
			return was, nil
		},
	}
	if out := r.mustRun("model"); !strings.Contains(out, "Embedding model: not installed.") {
		t.Errorf("a bare model shows the status: %q", out)
	}
	if out := r.mustRun("model", "install"); !strings.Contains(out, "Downloaded the embedding model to /x/m.bin") || !strings.Contains(out, "shh worker restart") {
		t.Errorf("out = %q", out)
	}
	if out := r.mustRun("model", "status"); !strings.Contains(out, "installed at /x/m.bin") {
		t.Errorf("out = %q", out)
	}
	if out := r.mustRun("model", "remove"); !strings.Contains(out, "Removed") {
		t.Errorf("out = %q", out)
	}
	if out := r.mustRun("model", "remove"); !strings.Contains(out, "No downloaded model") {
		t.Errorf("out = %q", out)
	}
	r.env.Model = &cli.ModelOps{Built: true, Off: true, Status: func() string { return "built into this shh" }}
	if code := r.run("model", "remove"); code != 1 || !strings.Contains(r.err.String(), "embedding_model") {
		t.Errorf("a built in model cannot be removed, only turned off: %d %s", code, r.err.String())
	}
	if out := r.mustRun("model", "status"); !strings.Contains(out, "turned off in config.json") {
		t.Errorf("out = %q", out)
	}
}
