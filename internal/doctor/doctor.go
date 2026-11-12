// Package doctor checks that shh has what it needs: its files load, the notification database can be read, the embedding model is the
// right one, the permissions are granted and the worker is running. It only looks: nothing here changes a file or shows anything on the
// screen. The things looked at come in through Env, so tests can stand in for the machine.
package doctor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Level is how a check went.
type Level int

const (
	OK   Level = iota
	Warn       // shh works, but not as well as it could
	Fail       // something shh needs is missing or broken
)

func (l Level) String() string { return [...]string{"ok", "warn", "FAIL"}[l] }

// Result is one line of the report.
type Result struct {
	Name   string
	Level  Level
	Detail string
	// Fix says what to do about a Warn or Fail.
	Fix string
}

// Check is one thing to look at.
type Check struct {
	Name string
	Run  func() (Level, string, string) // level, what was found, what to do about it
}

// Env is the machine as the checks see it. A nil function means that check cannot be made and is skipped with a note.
type Env struct {
	ConfigPath string
	// LoadConfig reads config.json as the worker will.
	LoadConfig func() error
	// DBPath is shh's own database; OpenDB opens it (without creating it) and returns its schema version.
	DBPath string
	OpenDB func() (version int, err error)
	// NotificationDB finds and reads the Notification Center database; it returns where it is.
	NotificationDB func() (path string, err error)
	// Model resolves the embedding model: where it came from ("embedded" or a path).
	Model func() (where string, err error)
	// ModelMissing is the error Model returns when there is no model at all, which only costs the second layer of the classifier.
	ModelMissing error
	// LoadRules and LoadRoutines read the two files, which may not be there (that is fine).
	RulesPath, RoutinesPath string
	LoadRules, LoadRoutines func() error
	// Accessibility says whether this process may use the accessibility API, which closing system banners needs.
	Accessibility func() (bool, error)
	// Worker asks the running worker for its build; running is false when none answers.
	Worker func() (running bool, build string, err error)
	// Build is this executable's build.
	Build string
	// Tool says whether a program is on the PATH.
	Tool func(name string) bool
}

// Standard lists the checks, in the order they are reported.
func Standard(e Env) []Check {
	var cs []Check
	add := func(name string, run func() (Level, string, string)) { cs = append(cs, Check{Name: name, Run: run}) }

	if e.LoadConfig != nil {
		add("config", func() (Level, string, string) {
			if err := e.LoadConfig(); err != nil {
				return Fail, err.Error(), "fix the line it names in " + e.ConfigPath + " (shh config defaults shows what is allowed)"
			}
			if _, err := os.Stat(e.ConfigPath); errors.Is(err, os.ErrNotExist) {
				return Warn, e.ConfigPath + " does not exist, so the defaults are in force", "shh init writes it with every option explained"
			}
			return OK, e.ConfigPath, ""
		})
	}
	if e.OpenDB != nil {
		add("database", func() (Level, string, string) {
			if _, err := os.Stat(e.DBPath); errors.Is(err, os.ErrNotExist) {
				return Warn, e.DBPath + " does not exist yet", "it is made when the worker first runs (shh worker start)"
			}
			v, err := e.OpenDB()
			if err != nil {
				return Fail, err.Error(), "move " + e.DBPath + " aside and let the worker make a new one, if its history can go"
			}
			return OK, fmt.Sprintf("%s (schema %d)", e.DBPath, v), ""
		})
	}
	if e.NotificationDB != nil {
		add("notification database", func() (Level, string, string) {
			path, err := e.NotificationDB()
			if err != nil {
				return Fail, err.Error(), "give the terminal you start shh from Full Disk Access (System Settings, Privacy & Security), then restart it"
			}
			return OK, path, ""
		})
	}
	if e.Model != nil {
		add("embedding model", func() (Level, string, string) {
			where, err := e.Model()
			switch {
			case err == nil:
				return OK, where, ""
			case e.ModelMissing != nil && errors.Is(err, e.ModelMissing):
				return Warn, "there is no embedding model, so the classifier runs without its second layer", "put the model file in the models folder (see the README)"
			default:
				return Fail, err.Error(), "download the model again"
			}
		})
	}
	file := func(name, path string, load func() error) {
		if load == nil {
			return
		}
		add(name, func() (Level, string, string) {
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				return OK, path + " is not there (optional)", ""
			}
			if err := load(); err != nil {
				return Fail, err.Error(), "fix the line it names"
			}
			return OK, path, ""
		})
	}
	file("rules", e.RulesPath, e.LoadRules)
	file("routines", e.RoutinesPath, e.LoadRoutines)
	if e.Accessibility != nil {
		add("accessibility", func() (Level, string, string) {
			trusted, err := e.Accessibility()
			if err != nil {
				return Warn, "could not ask: " + err.Error(), ""
			}
			if !trusted {
				return Warn, "this terminal may not use the accessibility API, so banners cannot be closed after they are read",
					"System Settings, Privacy & Security, Accessibility: add the terminal (or the app that starts the worker)"
			}
			return OK, "granted to this terminal", ""
		})
	}
	if e.Tool != nil {
		add("osascript", func() (Level, string, string) {
			if !e.Tool("osascript") {
				return Fail, "osascript is not on the PATH, and banners and the menu bar need it", "it ships with macOS: check the PATH"
			}
			return OK, "found", ""
		})
	}
	if e.Worker != nil {
		add("worker", func() (Level, string, string) {
			running, build, err := e.Worker()
			switch {
			case err != nil:
				return Fail, err.Error(), "shh worker restart"
			case !running:
				return Warn, "not running", "shh worker start (it also starts by itself when a command needs it)"
			case e.Build != "" && build != e.Build:
				return Warn, "running, but from a different build than this command", "shh worker restart"
			}
			return OK, "running", ""
		})
	}
	return cs
}

// Run makes the checks. A check that panics is a failure of that check, not of the report.
func Run(checks []Check) []Result {
	out := make([]Result, 0, len(checks))
	for _, c := range checks {
		out = append(out, run(c))
	}
	return out
}

func run(c Check) (r Result) {
	r.Name = c.Name
	defer func() {
		if p := recover(); p != nil {
			r.Level, r.Detail, r.Fix = Fail, fmt.Sprint("the check itself failed: ", p), ""
		}
	}()
	r.Level, r.Detail, r.Fix = c.Run()
	return r
}

// Failed says whether any check failed (warnings do not count).
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.Level == Fail {
			return true
		}
	}
	return false
}

// Print writes the report: a line a check, and under a warning or failure what to do about it.
func Print(w io.Writer, rs []Result) {
	width := 0
	for _, r := range rs {
		width = max(width, len(r.Name))
	}
	counts := [3]int{}
	for _, r := range rs {
		counts[r.Level]++
		fmt.Fprintf(w, "%-4s  %-*s  %s\n", r.Level, width, r.Name, r.Detail)
		if r.Fix != "" && r.Level != OK {
			fmt.Fprintf(w, "      %s  -> %s\n", strings.Repeat(" ", width), r.Fix)
		}
	}
	fmt.Fprintf(w, "\n%d ok, %d warnings, %d failed\n", counts[OK], counts[Warn], counts[Fail])
}
