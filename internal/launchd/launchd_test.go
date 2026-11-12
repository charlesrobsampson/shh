package launchd_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/launchd"
)

type fakeLaunchctl struct {
	calls  []string
	loaded bool
	fail   map[string]error
}

func (f *fakeLaunchctl) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if err := f.fail[args[0]]; err != nil {
		return []byte("boom"), err
	}
	switch args[0] {
	case "print":
		if !f.loaded {
			return nil, errors.New("not loaded")
		}
	case "bootstrap":
		f.loaded = true
	case "bootout":
		f.loaded = false
	}
	return nil, nil
}

func agent(t *testing.T, f *fakeLaunchctl) launchd.Agent {
	home := t.TempDir()
	return launchd.Agent{Home: home, UID: 501, Exe: "/usr/local/bin/shh", Log: filepath.Join(home, ".config", "shh", "shh.log"), Launchctl: f.run}
}

func TestThePlistRunsTheDaemonAtLoginAndRestartsItOnlyAfterACrash(t *testing.T) {
	a := agent(t, &fakeLaunchctl{})
	a.Exe = "/Users/me/my & dir/shh"
	p := string(a.Plist())
	for _, want := range []string{
		"<string>app.shh.worker</string>", "<string>/Users/me/my &amp; dir/shh</string>", "<string>daemon</string>",
		"<key>RunAtLoad</key>\n\t<true/>", "<key>SuccessfulExit</key>\n\t\t<false/>", "<key>StandardErrorPath</key>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the plist is missing %q:\n%s", want, p)
		}
	}
	if !strings.HasSuffix(a.Path(), "/Library/LaunchAgents/app.shh.worker.plist") {
		t.Errorf("path %s", a.Path())
	}
}

func TestInstallWritesThePlistAndLoadsItAndReinstallReplacesIt(t *testing.T) {
	f := &fakeLaunchctl{}
	a := agent(t, f)
	if a.Installed() || a.Loaded() {
		t.Fatal("nothing is installed to start with")
	}
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	if got := f.calls[len(f.calls)-1]; got != "bootstrap gui/501 "+a.Path() {
		t.Errorf("last call %q", got)
	}
	if !a.Installed() || !a.Loaded() {
		t.Error("installed and loaded")
	}
	info, _ := os.Stat(a.Path())
	if info.Mode().Perm() != 0o644 {
		t.Errorf("a LaunchAgent plist is 0644, not %v", info.Mode().Perm())
	}
	a.Exe = "/opt/shh/shh"
	f.calls = nil
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, "|") != "print gui/501/app.shh.worker|bootout gui/501/app.shh.worker|bootstrap gui/501 "+a.Path() {
		t.Errorf("calls %v", f.calls)
	}
	if raw, _ := os.ReadFile(a.Path()); !strings.Contains(string(raw), "/opt/shh/shh") {
		t.Error("the new place is written")
	}
}

func TestInstallRefusesATemporaryBuildAndReportsLaunchctlFailuresWithoutLeavingAHalfInstall(t *testing.T) {
	a := agent(t, &fakeLaunchctl{})
	a.Exe = "/private/var/folders/xx/T/go-build123/b001/exe/shh"
	if err := a.Install(); err == nil || !strings.Contains(err.Error(), "temporary build") {
		t.Errorf("err = %v", err)
	}
	if a.Installed() {
		t.Error("a refused install writes nothing")
	}
	a.Exe = "shh"
	if err := a.Install(); err == nil {
		t.Error("a relative path is refused")
	}
}

func TestUninstallUnloadsAndRemovesAndIsQuietWhenThereIsNothing(t *testing.T) {
	f := &fakeLaunchctl{}
	a := agent(t, f)
	if was, err := a.Uninstall(); err != nil || was {
		t.Errorf("nothing installed: was %v err %v", was, err)
	}
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	if was, err := a.Uninstall(); err != nil || !was {
		t.Fatalf("was %v err %v", was, err)
	}
	if a.Installed() || f.loaded {
		t.Error("removed and unloaded")
	}
	f.fail = map[string]error{"bootstrap": errors.New("exit status 5")}
	if err := a.Install(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("a failing launchctl is reported with what it said: %v", err)
	}
	if a.Installed() {
		t.Error("a failed first install leaves no plist behind")
	}
}

func TestThePlistIsAValidPropertyList(t *testing.T) {
	lint, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil is a macOS tool")
	}
	a := agent(t, &fakeLaunchctl{})
	a.Exe = "/Users/me/my & <dir>/shh"
	path := filepath.Join(t.TempDir(), "x.plist")
	os.WriteFile(path, a.Plist(), 0o644)
	if out, err := exec.Command(lint, "-lint", path).CombinedOutput(); err != nil {
		t.Errorf("plutil -lint: %v: %s", err, out)
	}
}
