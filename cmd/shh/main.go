package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/cli"
	"github.com/charlesrobsampson/shh/internal/clock"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/doctor"
	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/menubar"
	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
	"github.com/charlesrobsampson/shh/internal/summarize"
	"github.com/charlesrobsampson/shh/internal/suppress"
	"github.com/charlesrobsampson/shh/internal/tui"
)

var version = "dev"

func main() {
	session.BuildID = buildID()
	os.Exit(cli.Run(os.Args[1:], realEnv()))
}

// buildID identifies this executable by a hash of its contents, so two builds of the same version still tell apart.
func buildID() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func realEnv() cli.Env {
	env := cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Version: version, Build: session.BuildID, Label: "shh", IsTerminal: isTerminal(os.Stdout)}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shh: cannot read the configuration:", err)
		os.Exit(1)
	}
	env.SocketPath, env.ConfigDir, env.Label = cfg.Paths.Socket, cfg.Paths.Dir, cfg.Menubar.Label
	if path, err := config.DefaultPath(); err == nil {
		env.ConfigPath = path
	}
	env.ConfigShow = func() ([]byte, error) { return cfg.Effective() }
	env.RoutinesPath, env.Stdin = routine.DefaultPath(cfg.Paths.Dir), os.Stdin
	env.Spawn = func() error { return spawnDaemon(cfg) }
	env.PersistedFocus = func() bool { return persistedFocus(cfg.Paths.DB) }
	env.Daemon = func(context.Context) error { return runDaemon(cfg) }
	env.Menubar = func(ctx context.Context) error { return runMenubar(ctx, cfg) }
	env.TUI = func() error { return runTUI(cfg, "", "", false, "") }
	env.UI = func(tab string) error { return runTUI(cfg, "", "", false, tab) }
	env.Guide = func(section, search string) error { return runTUI(cfg, section, search, true, "") }
	env.WithStore = func(do func(*store.Store) error) error { return withStore(cfg.Paths.DB, do) }
	env.Doctor = doctorEnv(cfg)
	env.FetchModel = func() (string, error) {
		dir := filepath.Join(cfg.Paths.Dir, "models")
		if _, where, err := classify.ResolveEmbedder(dir, classify.DefaultModelSHA256); err == nil {
			return "already there (" + where + ").", nil
		} else if !errors.Is(err, classify.ErrNoEmbeddingModel) {
			return "", err // a model that is there but wrong: say so rather than write over it
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		path, err := classify.DownloadModel(ctx, dir, classify.DefaultModelURL, classify.DefaultModelSHA256, nil)
		if err != nil {
			return "", err
		}
		return "downloaded to " + path + ". Restart the worker (shh worker restart) to use it.", nil
	}
	env.Retention = func() store.Retention {
		return store.Retention{TTL: days(cfg.Retention.TTLDays), KeepUntrained: days(cfg.Retention.KeepUntrainedDays)}
	}
	if home, err := os.UserHomeDir(); err == nil {
		env.Notifications = ingest.NewMacOS(ingest.DefaultPaths(home)...)
	}
	for source := range cfg.Alert.Source {
		env.AlertSources = append(env.AlertSources, source)
	}
	sort.Strings(env.AlertSources)
	return env
}

// doctorEnv is the machine as shh doctor looks at it. Nothing in it changes a file or shows anything on the screen.
func doctorEnv(cfg config.Config) *doctor.Env {
	home, _ := os.UserHomeDir()
	rulesPath := filepath.Join(cfg.Paths.Dir, "rules.json")
	routinesPath := routine.DefaultPath(cfg.Paths.Dir)
	e := &doctor.Env{
		LoadConfig: func() error { _, err := loadConfig(); return err },
		DBPath:     cfg.Paths.DB,
		OpenDB: func() (int, error) {
			var v int
			err := withStore(cfg.Paths.DB, func(s *store.Store) (err error) { v, err = s.SchemaVersion(); return })
			return v, err
		},
		NotificationDB: func() (string, error) { return ingest.NewMacOS(ingest.DefaultPaths(home)...).Check() },
		Model: func() (string, error) {
			_, where, err := classify.ResolveEmbedder(filepath.Join(cfg.Paths.Dir, "models"), classify.DefaultModelSHA256)
			return where, err
		},
		ModelMissing: classify.ErrNoEmbeddingModel,
		RulesPath:    rulesPath, LoadRules: func() error { _, err := rules.Load(rulesPath); return err },
		RoutinesPath: routinesPath, LoadRoutines: func() error { _, err := routine.Load(routinesPath); return err },
		Tool: func(name string) bool { _, err := exec.LookPath(name); return err == nil },
		// Asks the system, headless, whether this process is trusted for accessibility.
		Accessibility: func() (bool, error) {
			out, err := exec.Command("osascript", "-l", "JavaScript", "-e", `ObjC.import("ApplicationServices"); $.AXIsProcessTrusted()`).Output()
			if err != nil {
				return false, err
			}
			return strings.TrimSpace(string(out)) == "true", nil
		},
	}
	if path, err := config.DefaultPath(); err == nil {
		e.ConfigPath = path
	}
	return e
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func loadConfig() (config.Config, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return config.Config{}, err
	}
	return config.Load(path)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// spawnDaemon starts `shh daemon` as a detached background process whose output goes to the log file.
func spawnDaemon(cfg config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Paths.Dir, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(cfg.Paths.Dir, "shh.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// withStore opens the database for a command that reads the history, without creating one that is not there, and closes it after.
func withStore(path string, do func(*store.Store) error) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("there is no history yet (%s does not exist): it starts when the worker first runs", path)
	}
	s, err := store.Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	return do(s)
}

// persistedFocus reports whether the database holds an active focus level, without creating a database that is not there.
func persistedFocus(dbPath string) bool {
	if _, err := os.Stat(dbPath); err != nil {
		return false
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return false
	}
	defer s.Close()
	levels, err := s.ActiveLevels()
	return err == nil && len(levels) > 0
}

// newAlerter draws alerts in the AppKit overlay and falls back to a Notification Center notification (titled with the own-banner
// prefix so the suppressor lets it through) if the overlay cannot run or dies.
func newAlerter(alertConfig func() config.Alert, onError func(error), onFlag func(notificationID int64, flagged bool), onTrain func(notificationID int64), onAction func(token, id string)) (alert.Alerter, *alert.Overlay) {
	fallback := alert.NewFallback(alert.FallbackConfig{OwnPrefix: ingest.OwnTitlePrefix})
	overlay := alert.NewOverlay(alert.OverlayConfig{
		OnFlag:   onFlag,
		OnTrain:  onTrain,
		OnAction: onAction,
		OnFailure: func(req alert.Request, err error) {
			onError(fmt.Errorf("the alert overlay failed (%w); using a notification instead", err))
			if showErr := fallback.Show(req); showErr != nil {
				onError(showErr)
			}
		},
	})
	return alert.NewLiveDisplay(alertConfig, overlay, fallback, onError), overlay
}

// runDaemon is the background worker: real notification source, real banner suppressor and the real alerter.
func runDaemon(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	logger := log.New(os.Stderr, "", log.LstdFlags)
	onError := func(err error) { logger.Println("error:", err) }

	if err := os.MkdirAll(cfg.Paths.Dir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(cfg.Paths.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	r, err := rules.Load(filepath.Join(cfg.Paths.Dir, "rules.json"))
	if err != nil {
		return err
	}
	embedder, _, err := classify.ResolveEmbedder(filepath.Join(cfg.Paths.Dir, "models"), classify.DefaultModelSHA256)
	if errors.Is(err, classify.ErrNoEmbeddingModel) {
		logger.Println("note:", err, "- running without the embedding layer")
		embedder = nil
	} else if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// A routine's banner can carry a button ("Start next", "Skip"); the worker, made just below, is the one that acts on it.
	var worker *session.Worker
	// A banner's Flag button marks its notification for review in the Train tab (and pressing it again clears the mark).
	// Banners are styled from config.json as it is when they are shown, not as it was when the worker started.
	var watcher *config.Watcher
	if path, err := config.DefaultPath(); err == nil {
		watcher = config.NewWatcher(path, cfg, onError)
	}
	alertConfig := func() config.Alert { return cfg.Alert }
	if watcher != nil {
		alertConfig = watcher.Alert
	}
	alerter, overlay := newAlerter(alertConfig, onError, func(id int64, flagged bool) {
		if id < 0 {
			return // a sample from `shh alert test`: nothing real to mark
		}
		if err := db.SetFlagged(id, flagged); err != nil {
			onError(fmt.Errorf("changing the flag on notification %d: %w", id, err))
		}
	}, func(id int64) {
		// The train button of a banner: this alert should not have come. It is the same answer as n on the Notifications tab (undo with u).
		if id < 0 || worker == nil {
			return // a sample from `shh alert test`, or no worker yet
		}
		if _, err := worker.Train(session.TrainRequest{NotificationID: id, Feedback: "n"}); err != nil && !errors.Is(err, session.ErrAlreadyTrained) {
			onError(fmt.Errorf("the train button of a banner (notification %d): %w", id, err))
		}
	}, func(token, id string) {
		if worker == nil {
			return
		}
		if err := worker.RoutineAction(token, id); err != nil {
			onError(fmt.Errorf("the %q button of a routine banner: %w", id, err))
		}
	})
	worker, err = session.NewWorker(session.Config{
		Store: db, Source: ingest.NewMacOS(ingest.DefaultPaths(home)...), Clock: clock.Real{}, Rules: r, Embedder: embedder,
		Alerter:    alerter,
		Suppressor: suppress.NewAccessibility(suppress.AccessibilityConfig{OwnPrefix: ingest.OwnTitlePrefix, OnError: onError}),
		Location:   time.Local, OnError: onError,
		// Routines are read from routines.json each time one is needed, so edits apply to the next run.
		Routines: func() ([]routine.Routine, error) { return routine.Load(routine.DefaultPath(cfg.Paths.Dir)) },
		// The sound for alerts that are only a sound is the one the alert style uses.
		PlaySound:   playSystemSound,
		OpenUI:      openUI,
		AlertConfig: alertConfig,
		// How long notifications are kept, and whether the ones nobody labelled are taken as handled correctly.
		// config.json is looked at again each time, so an edit applies to the next pass (every six hours) without a restart.
		RetentionNow: func() (store.Retention, bool, time.Duration) {
			r := cfg.Retention
			if watcher != nil {
				r = watcher.Retention()
			}
			return store.Retention{TTL: days(r.TTLDays), KeepUntrained: days(r.KeepUntrainedDays)}, r.AutoTrain, days(r.AutoTrainAfterDays)
		},
	})
	if err != nil {
		return err
	}
	logger.Println("shh worker started")
	defer logger.Println("shh worker stopped")
	// The menu bar item is this worker's: started once the worker owns the socket (a second worker that loses the race for it must not
	// leave an item behind), and sent away when the worker goes, so a restarted worker is not left with its old one beside the new.
	var menubarProc *os.Process
	defer func() {
		if menubarProc != nil {
			_ = menubarProc.Signal(syscall.SIGTERM)
		}
	}()
	return session.RunDaemon(ctx, session.DaemonConfig{
		OnReady: func() {
			if !cfg.Menubar.Enabled {
				return
			}
			proc, err := spawnMenubar(cfg)
			if err != nil {
				onError(fmt.Errorf("starting the menu bar item: %w", err))
				return
			}
			menubarProc = proc
		},
		Worker: worker, Clock: clock.Real{}, SocketPath: cfg.Paths.Socket,
		LockPath: filepath.Join(cfg.Paths.Dir, "shh.lock"), PIDPath: cfg.Paths.PID, OnError: onError,
		// Banners stay until dismissed, and only this process can lay them out, so do not leave while any are up.
		// A routine waiting for the user may have nothing in force, but it still needs this process to carry on.
		// A routine with a trigger switched on is started by this process, so it stays up for as long as there is one.
		Busy: func() bool { return overlay.Active() > 0 || worker.RoutineActive() || worker.HasTriggers() },
	})
}

// spawnMenubar starts `shh menubar` as a child of the worker. It is not waited for or killed: it polls the worker's socket and
// leaves on its own when the worker is gone.
func spawnMenubar(cfg config.Config) (*os.Process, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(cfg.Paths.Dir, "shh.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "menubar")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go cmd.Wait() // reap it when it ends
	return cmd.Process, nil
}

// runMenubar is the menu bar item's process: a status item driven by the worker over its socket.
func runMenubar(ctx context.Context, cfg config.Config) error {
	logger := log.New(os.Stderr, "menubar: ", log.LstdFlags)
	parent := os.Getppid() // the worker that started it: when that changes, the worker is gone and so is this item
	return menubar.Run(ctx, menubar.Config{
		Gone:       func() bool { return os.Getppid() != parent },
		Controller: session.NewClient(cfg.Paths.Socket),
		Label:      cfg.Menubar.Label,
		Open:       func() error { return openUI("") },
		Review:     func() error { return openUI("train") },
		OnError:    func(err error) { logger.Println("error:", err) },
	})
}

// openUI opens a Terminal window running shh, which shows the terminal UI.
// With a tab (focus, notifications, train, routines) the UI opens on it.
func openUI(tab string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
	if tab != "" {
		quoted += " ui --tab " + tab
	}
	script := `tell application "Terminal"
	activate
	do script ` + strconv.Quote(quoted) + `
end tell`
	return exec.Command("osascript", "-e", script).Run()
}

// runTUI opens the terminal UI. It reads the same database as the worker (SQLite allows that) and asks the worker for the session
// status, which it does without: with no worker the UI is for review and training.
func runTUI(cfg config.Config, helpSection, helpSearch string, helpOnly bool, startTab string) error {
	if err := os.MkdirAll(cfg.Paths.Dir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(cfg.Paths.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	client := session.NewClient(cfg.Paths.Socket)
	summarizer, err := summarize.New(cfg.Summarizer.Backend, summarize.Options{URL: cfg.Summarizer.URL, Model: cfg.Summarizer.Model})
	if err != nil {
		return err
	}
	return tui.Run(tui.Config{
		// s writes a short summary of the selected notification with the configured backend, and keeps it with the notification.
		Summarize: func(n store.Notification) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			text, err := summarizer.Summarize(ctx, summarize.Input{Source: n.Source, Sender: n.Sender, Title: n.Title, Subtitle: n.Subtitle, Body: n.Body})
			if errors.Is(err, summarize.ErrDisabled) {
				return "", tui.ErrNoSummarizer
			}
			if err != nil {
				return "", err
			}
			return text, db.SetSummary(n.ID, text)
		},
		SummarizerName: summarizer.Name(), SummarizerRemote: summarizer.Remote(),
		Data: db, Worker: client, Label: cfg.Menubar.Label,
		HelpOnly: helpOnly, HelpSection: helpSection, HelpSearch: helpSearch, StartTab: startTab,
		// The Focus tab does everything the command line does, through the worker.
		Control: client,
		// The Routines tab starts and steers routines through the worker and reads the routines file itself.
		Routines: client,
		Saved:    func() ([]routine.Routine, error) { return routine.Load(routine.DefaultPath(cfg.Paths.Dir)) },
		SaveRoutine: func(r routine.Routine, replace bool) error {
			return routine.Save(routine.DefaultPath(cfg.Paths.Dir), r, replace)
		},
		RemoveRoutine: func(name string) error { return routine.Remove(routine.DefaultPath(cfg.Paths.Dir), name) },
		// Training goes through the worker, which owns the model; start one if none is running.
		Trainer: client,
		Ensure: func() error {
			_, err := session.Ensure(client, func() error { return spawnDaemon(cfg) }, 0)
			return err
		},
	})
}

// systemSound matches the names of the sounds macOS ships (Glass, Hero, Sosumi, ...), and keeps a configured name from being used as a
// path.
var systemSound = regexp.MustCompile(`^[A-Za-z0-9 _-]+$`)

// playSystemSound plays a macOS system sound by name without waiting for it; "none" and unknown or odd names play nothing.
func playSystemSound(name string) error {
	if name == "" || name == "none" || !systemSound.MatchString(name) {
		return nil
	}
	cmd := exec.Command("afplay", filepath.Join("/System/Library/Sounds", name+".aiff"))
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap it when it ends
	return nil
}
