package suppress

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

//go:embed watch.js
var watchScript string

// restoreScript puts a parked Notification Center window back where it belongs.
const restoreScript = `
const center = Application('System Events').processes.byName('NotificationCenter');
const windows = center.windows();
for (let i = 0; i < windows.length; i++) {
  try { if (windows[i].position()[0] >= 5000) windows[i].position = [0, 0]; } catch (e) {}
}
`

const (
	defaultRestartDelay = time.Second
	stopTimeout         = 3 * time.Second
)

type AccessibilityConfig struct {
	// OwnPrefix marks shh's own fallback banners, which are left visible; "[shh]" when empty.
	OwnPrefix string
	// RestartDelay is how long to wait before relaunching a watcher that died; one second when zero.
	RestartDelay time.Duration
	// OnError hears about watchers that die and fail to relaunch. Optional.
	OnError func(error)

	// Command and Restore replace the real watcher and its cleanup in tests.
	Command func() *exec.Cmd
	Restore func() error
}

// Accessibility hides banners by running the watch script in a child osascript process, supervised: if it dies it is relaunched.
// The process needs the Accessibility permission, which belongs to whatever terminal or app launched shh.
type Accessibility struct {
	cfg AccessibilityConfig

	mu      sync.Mutex
	running bool
	current *os.Process
	stop    chan struct{}
	done    chan struct{}
}

func NewAccessibility(cfg AccessibilityConfig) *Accessibility {
	if cfg.OwnPrefix == "" {
		cfg.OwnPrefix = "[shh]"
	}
	if cfg.RestartDelay == 0 {
		cfg.RestartDelay = defaultRestartDelay
	}
	a := &Accessibility{cfg: cfg}
	if cfg.Command == nil {
		a.cfg.Command = a.WatcherCommand
	}
	if cfg.Restore == nil {
		a.cfg.Restore = restoreBanners
	}
	return a
}

// WatcherCommand builds the real watcher process.
func (a *Accessibility) WatcherCommand() *exec.Cmd {
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", watchScript)
	cmd.Env = append(os.Environ(), "SHH_OWN_PREFIX="+a.cfg.OwnPrefix)
	return cmd
}

func restoreBanners() error {
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", restoreScript).CombinedOutput()
	if err != nil {
		return fmt.Errorf("putting the notification banners back: %w: %s", err, out)
	}
	return nil
}

// Start launches the watcher. The first launch is synchronous so a problem such as a missing osascript is returned.
func (a *Accessibility) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return nil
	}
	cmd := a.cfg.Command()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the banner watcher: %w", err)
	}
	a.running, a.current = true, cmd.Process
	a.stop, a.done = make(chan struct{}), make(chan struct{})
	go a.supervise(cmd, a.stop, a.done)
	return nil
}

// Stop ends the watcher for good and puts any parked banner window back on screen.
func (a *Accessibility) Stop() error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = false
	close(a.stop)
	if a.current != nil {
		a.current.Signal(syscall.SIGTERM)
	}
	done := a.done
	a.mu.Unlock()

	select {
	case <-done:
	case <-time.After(stopTimeout):
		a.mu.Lock()
		if a.current != nil {
			a.current.Kill()
		}
		a.mu.Unlock()
		<-done
	}
	return a.cfg.Restore()
}

func (a *Accessibility) supervise(cmd *exec.Cmd, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		err := cmd.Wait()
		select {
		case <-stop:
			return
		default:
		}
		a.report(fmt.Errorf("the banner watcher exited (%v); restarting", err))
		for {
			select {
			case <-stop:
				return
			case <-time.After(a.cfg.RestartDelay):
			}
			next := a.cfg.Command()
			if err := next.Start(); err != nil {
				a.report(fmt.Errorf("restarting the banner watcher: %w", err))
				continue
			}
			a.mu.Lock()
			a.current = next.Process
			a.mu.Unlock()
			select {
			case <-stop: // Stop arrived while this one was launching
				next.Process.Kill()
			default:
			}
			cmd = next
			break
		}
	}
}

func (a *Accessibility) report(err error) {
	if a.cfg.OnError != nil {
		a.cfg.OnError(err)
	}
}
