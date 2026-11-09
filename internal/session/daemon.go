package session

import (
	"context"
	"sync"
	"time"

	"github.com/charlesrobsampson/shh/internal/clock"
)

// DefaultIdleGrace is how long the daemon stays up with no focus level before it exits. It must outlast the gap between a
// command starting the daemon and that command's request arriving.
const DefaultIdleGrace = 3 * time.Second

type DaemonConfig struct {
	Worker                        *Worker
	Clock                         clock.Clock
	SocketPath, LockPath, PIDPath string
	IdleGrace                     time.Duration // DefaultIdleGrace when zero
	// Busy, if set, says there is something on screen that only this process can manage (alert banners stay until dismissed, and
	// the daemon moves them when others come and go). While it returns true the daemon does not count as idle.
	Busy    func() bool
	OnError func(error)
}

// RunDaemon is the background worker process: it takes the single-instance lock, serves the socket and runs the worker until ctx
// is cancelled or focus has been off for IdleGrace, then shuts down cleanly (suppressor stopped, socket and PID file removed,
// lock released). Levels persist in the store, so a daemon that starts with a baseline already set simply resumes it.
func RunDaemon(ctx context.Context, cfg DaemonConfig) error {
	l, err := acquireLock(cfg.LockPath)
	if err != nil {
		return err
	}
	defer l.release()
	if err := writePID(cfg.PIDPath); err != nil {
		return err
	}
	defer removePID(cfg.PIDPath)
	server, err := Listen(cfg.SocketPath, cfg.Worker)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var running sync.WaitGroup
	running.Add(2)
	go func() {
		defer running.Done()
		if err := server.Serve(ctx); err != nil {
			report(cfg, err)
		}
	}()
	go func() {
		defer running.Done()
		if err := cfg.Worker.Run(ctx, time.Time{}); err != nil {
			report(cfg, err)
			cancel()
		}
	}()
	defer running.Wait()

	grace := cfg.IdleGrace
	if grace == 0 {
		grace = DefaultIdleGrace
	}
	ticker := cfg.Clock.NewTicker(time.Second)
	defer ticker.Stop()
	var offSince time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case at := <-ticker.C():
			st, err := cfg.Worker.Status()
			if err != nil {
				report(cfg, err)
				continue
			}
			if st.Level > 0 || (cfg.Busy != nil && cfg.Busy()) {
				offSince = time.Time{}
				continue
			}
			if offSince.IsZero() {
				offSince = at
			}
			if at.Sub(offSince) >= grace {
				cancel()
				return nil
			}
		}
	}
}

func report(cfg DaemonConfig, err error) {
	if cfg.OnError != nil {
		cfg.OnError(err)
	}
}
