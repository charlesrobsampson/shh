package alert

import (
	"fmt"
	"os/exec"
)

// FallbackConfig configures the Notification Center fallback.
type FallbackConfig struct {
	// OwnPrefix starts every title so the banner suppressor and the tailer recognise the notification as shh's own; "[shh]" when empty.
	OwnPrefix string
	// Run executes a command; exec by default. Tests replace it.
	Run func(name string, args ...string) error
}

// Fallback shows an alert as an ordinary macOS notification. It has no styling and cannot open the source app on click, but it
// needs nothing beyond osascript. macOS attributes it to Script Editor, hence the prefix.
type Fallback struct {
	cfg FallbackConfig
}

func NewFallback(cfg FallbackConfig) *Fallback {
	if cfg.OwnPrefix == "" {
		cfg.OwnPrefix = "[shh]"
	}
	if cfg.Run == nil {
		cfg.Run = func(name string, args ...string) error {
			if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
				return fmt.Errorf("%s: %w: %s", name, err, out)
			}
			return nil
		}
	}
	return &Fallback{cfg: cfg}
}

// The title and body travel as arguments to the script, never inside its source, so notification text cannot be run as code.
const fallbackScript = "display notification (item 2 of argv) with title (item 1 of argv)"

func (f *Fallback) Show(r Request) error {
	return f.cfg.Run("osascript", "-e", "on run argv", "-e", fallbackScript, "-e", "end run", f.cfg.OwnPrefix+" "+r.Title, r.Body)
}
