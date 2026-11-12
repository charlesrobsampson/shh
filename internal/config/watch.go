package config

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Watcher hands out the configuration and reads config.json again when the file has changed, so a banner shows the colors, sound and
// position as they are now and not as they were when the worker started. Only the alert and retention settings are meant to be taken
// from it: the rest of the configuration (where files live, the menu bar) is used once, at start.
//
// A file that cannot be read after an edit (a typo mid-way) leaves the last good configuration in use, and the problem is reported once.
type Watcher struct {
	path    string
	onError func(error)

	mu      sync.Mutex
	cfg     Config
	stamp   stamp
	lastErr string
}

// stamp is what is looked at to tell that the file has changed.
type stamp struct {
	mod    time.Time
	size   int64
	exists bool
}

func stampOf(path string) stamp {
	info, err := os.Stat(path)
	if err != nil {
		return stamp{}
	}
	return stamp{mod: info.ModTime(), size: info.Size(), exists: true}
}

// NewWatcher watches the file at path, whose current contents are initial.
func NewWatcher(path string, initial Config, onError func(error)) *Watcher {
	return &Watcher{path: path, onError: onError, cfg: initial, stamp: stampOf(path)}
}

// Config is the configuration as the file says now.
func (w *Watcher) Config() Config {
	w.mu.Lock()
	defer w.mu.Unlock()
	if now := stampOf(w.path); now != w.stamp {
		w.stamp = now
		cfg, err := Load(w.path)
		switch {
		case err != nil:
			if msg := err.Error(); msg != w.lastErr && w.onError != nil {
				w.lastErr = msg
				w.onError(fmt.Errorf("config.json changed but cannot be read, so the last good settings stay in use: %w", err))
			}
		default:
			w.cfg, w.lastErr = cfg, ""
		}
	}
	return w.cfg
}

// Retention is the retention part of Config: how long notifications are kept, as the file says now.
func (w *Watcher) Retention() Retention { return w.Config().Retention }

// Alert is the alert part of Config, for passing where a function that gives it is wanted.
func (w *Watcher) Alert() Alert { return w.Config().Alert }
