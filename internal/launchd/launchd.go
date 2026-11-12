// Package launchd installs shh's worker as a LaunchAgent, so it starts at login and is started again if it crashes. Nothing here runs
// unless the user asks (shh worker install); launchctl is reached through a function so tests can stand in for it.
package launchd

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Label names the agent, and its plist is Label + ".plist".
const Label = "app.shh.worker"

// Runner runs launchctl with the arguments and returns what it printed.
type Runner func(args ...string) ([]byte, error)

// Agent is the LaunchAgent for one user.
type Agent struct {
	// Home is the user's home folder: the agent goes in Home/Library/LaunchAgents.
	Home string
	// UID is the user's id: the agent runs in their graphical session, gui/UID.
	UID int
	// Exe is the shh executable the agent runs as `shh daemon`.
	Exe string
	// Log is where the worker's output goes.
	Log string
	// Launchctl runs launchctl; the real one when nil is the caller's to supply (the package does not start processes by itself).
	Launchctl Runner
}

// Path is where the plist is.
func (a Agent) Path() string { return filepath.Join(a.Home, "Library", "LaunchAgents", Label+".plist") }

func (a Agent) domain() string { return fmt.Sprintf("gui/%d", a.UID) }

// Plist is the agent's definition. It runs `shh daemon` (the worker in the foreground, which launchd wants) when the user logs in and
// starts it again if it ends badly. A clean exit, such as shh worker stop, is left alone, so stopping the worker stays stopped until the
// next login. A second worker that finds one already running exits cleanly, so launchd and a worker started by hand never fight.
func (a Agent) Plist() []byte {
	var b bytes.Buffer
	esc := func(s string) string { return html.EscapeString(s) }
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(a.Exe) + `</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>` + esc(a.Log) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(a.Log) + `</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin</string>
	</dict>
</dict>
</plist>
`)
	return b.Bytes()
}

// Installed says whether the plist is in place.
func (a Agent) Installed() bool {
	_, err := os.Stat(a.Path())
	return err == nil
}

// Loaded says whether launchd has the agent loaded now.
func (a Agent) Loaded() bool {
	if a.Launchctl == nil {
		return false
	}
	_, err := a.Launchctl("print", a.domain()+"/"+Label)
	return err == nil
}

// Install writes the plist and has launchd load it, which starts the worker now as well as at every login. An agent that was loaded is
// replaced, so installing again after moving shh picks up the new place. A shh that looks like it was run with `go run` (a temporary
// build) is refused: the agent would point at a file that goes away.
func (a Agent) Install() error {
	if a.Launchctl == nil {
		return errors.New("launchctl is not available")
	}
	if a.Exe == "" || !filepath.IsAbs(a.Exe) {
		return fmt.Errorf("cannot tell where shh is installed (%q)", a.Exe)
	}
	if strings.Contains(a.Exe, "/go-build") || strings.HasPrefix(a.Exe, os.TempDir()) {
		return fmt.Errorf("%s looks like a temporary build: put shh where it stays (for example /usr/local/bin) and run this from there", a.Exe)
	}
	if err := os.MkdirAll(filepath.Dir(a.Path()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.Log), 0o700); err != nil {
		return err
	}
	existed := a.Installed()
	if err := writeFile(a.Path(), a.Plist()); err != nil {
		return err
	}
	if a.Loaded() {
		if _, err := a.Launchctl("bootout", a.domain()+"/"+Label); err != nil {
			return fmt.Errorf("replacing the loaded agent: %w", err)
		}
	}
	if out, err := a.Launchctl("bootstrap", a.domain(), a.Path()); err != nil {
		if !existed {
			os.Remove(a.Path()) // a failed first install leaves nothing behind that would load at the next login
		}
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall has launchd unload the agent (which stops a worker it started) and removes the plist. It says whether there was anything to
// remove; one that was never installed is not an error.
func (a Agent) Uninstall() (bool, error) {
	was := a.Installed()
	if a.Launchctl != nil && a.Loaded() {
		if out, err := a.Launchctl("bootout", a.domain()+"/"+Label); err != nil {
			return was, fmt.Errorf("launchctl bootout: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if err := os.Remove(a.Path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return was, err
	}
	return was, nil
}

// writeFile replaces the file in one step.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".shh-agent-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
