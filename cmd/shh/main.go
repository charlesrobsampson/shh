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
	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/menubar"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
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
	env.Spawn = func() error { return spawnDaemon(cfg) }
	env.PersistedFocus = func() bool { return persistedFocus(cfg.Paths.DB) }
	env.Daemon = func(context.Context) error { return runDaemon(cfg) }
	env.Menubar = func(ctx context.Context) error { return runMenubar(ctx, cfg) }
	env.TUI = func() error { return runTUI(cfg) }
	for source := range cfg.Alert.Source {
		env.AlertSources = append(env.AlertSources, source)
	}
	sort.Strings(env.AlertSources)
	return env
}

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
func newAlerter(cfg config.Config, onError func(error), onFlag func(notificationID int64, flagged bool)) (alert.Alerter, *alert.Overlay) {
	fallback := alert.NewFallback(alert.FallbackConfig{OwnPrefix: ingest.OwnTitlePrefix})
	overlay := alert.NewOverlay(alert.OverlayConfig{
		OnFlag: onFlag,
		OnFailure: func(req alert.Request, err error) {
			onError(fmt.Errorf("the alert overlay failed (%w); using a notification instead", err))
			if showErr := fallback.Show(req); showErr != nil {
				onError(showErr)
			}
		},
	})
	return alert.NewDisplay(cfg.Alert, overlay, fallback, onError), overlay
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

	// A banner's Flag button marks its notification for review in the Train tab (and pressing it again clears the mark).
	alerter, overlay := newAlerter(cfg, onError, func(id int64, flagged bool) {
		if id < 0 {
			return // a sample from `shh alert test`: nothing real to mark
		}
		if err := db.SetFlagged(id, flagged); err != nil {
			onError(fmt.Errorf("changing the flag on notification %d: %w", id, err))
		}
	})
	worker, err := session.NewWorker(session.Config{
		Store: db, Source: ingest.NewMacOS(ingest.DefaultPaths(home)...), Clock: clock.Real{}, Rules: r, Embedder: embedder,
		Alerter:    alerter,
		Suppressor: suppress.NewAccessibility(suppress.AccessibilityConfig{OwnPrefix: ingest.OwnTitlePrefix, OnError: onError}),
		Location:   time.Local, OnError: onError,
	})
	if err != nil {
		return err
	}
	if cfg.Menubar.Enabled {
		if err := spawnMenubar(cfg); err != nil {
			onError(fmt.Errorf("starting the menu bar item: %w", err))
		}
	}
	logger.Println("shh worker started")
	defer logger.Println("shh worker stopped")
	return session.RunDaemon(ctx, session.DaemonConfig{
		Worker: worker, Clock: clock.Real{}, SocketPath: cfg.Paths.Socket,
		LockPath: filepath.Join(cfg.Paths.Dir, "shh.lock"), PIDPath: cfg.Paths.PID, OnError: onError,
		// Banners stay until dismissed, and only this process can lay them out, so do not leave while any are up.
		Busy: func() bool { return overlay.Active() > 0 },
	})
}

// spawnMenubar starts `shh menubar` as a child of the worker. It is not waited for or killed: it polls the worker's socket and
// leaves on its own when the worker is gone.
func spawnMenubar(cfg config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(cfg.Paths.Dir, "shh.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "menubar")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap it when it ends
	return nil
}

// runMenubar is the menu bar item's process: a status item driven by the worker over its socket.
func runMenubar(ctx context.Context, cfg config.Config) error {
	logger := log.New(os.Stderr, "menubar: ", log.LstdFlags)
	return menubar.Run(ctx, menubar.Config{
		Controller: session.NewClient(cfg.Paths.Socket),
		Label:      cfg.Menubar.Label,
		Open:       openUI,
		OnError:    func(err error) { logger.Println("error:", err) },
	})
}

// openUI opens a Terminal window running shh, which shows the terminal UI.
func openUI() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
	script := `tell application "Terminal"
	activate
	do script ` + strconv.Quote(quoted) + `
end tell`
	return exec.Command("osascript", "-e", script).Run()
}

// runTUI opens the terminal UI. It reads the same database as the worker (SQLite allows that) and asks the worker for the session
// status, which it does without: with no worker the UI is for review and training.
func runTUI(cfg config.Config) error {
	if err := os.MkdirAll(cfg.Paths.Dir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(cfg.Paths.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	client := session.NewClient(cfg.Paths.Socket)
	return tui.Run(tui.Config{
		Data: db, Worker: client, Label: cfg.Menubar.Label,
		// Training goes through the worker, which owns the model; start one if none is running.
		Trainer: client,
		Ensure: func() error {
			_, err := session.Ensure(client, func() error { return spawnDaemon(cfg) }, 0)
			return err
		},
	})
}
