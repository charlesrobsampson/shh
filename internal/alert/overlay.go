package alert

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

//go:embed overlay.js
var overlayScript string

// OverlayConfig configures the AppKit overlay backend. The zero value runs the real thing; tests replace Start and Open.
type OverlayConfig struct {
	// Start launches one banner process with the extra environment and returns a function that waits for it and reports how it
	// ended: "clicked", "expired", or an error.
	Start func(env []string) (wait func() (string, error), err error)
	// Open opens an app by bundle id when its banner is clicked; `open -b` by default.
	Open func(bundle string) error
	// OnFailure receives alerts whose banner process died without showing them, so they can be shown another way.
	OnFailure func(Request, error)
	// OnError hears about problems that did not lose an alert, such as an app that would not open.
	OnError func(error)
}

// Overlay draws each banner in its own osascript process (see docs/spikes/03-banner.md), in a window of its own that the banner
// suppressor never touches. Banners that are on screen together take successive slots so they stack instead of overlapping.
type Overlay struct {
	cfg OverlayConfig

	mu    sync.Mutex
	slots map[string]map[int]bool // by position
}

func NewOverlay(cfg OverlayConfig) *Overlay {
	if cfg.Start == nil {
		cfg.Start = startOsascript
	}
	if cfg.Open == nil {
		cfg.Open = func(bundle string) error { return exec.Command("open", "-b", bundle).Run() }
	}
	return &Overlay{cfg: cfg, slots: map[string]map[int]bool{}}
}

type bannerSpec struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Color    string `json:"color"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Position string `json:"position"`
	Slot     int    `json:"slot"`
	Duration int    `json:"duration"`
	Sound    string `json:"sound"`
}

func bannerEnv(r Request, slot int) []string {
	spec, _ := json.Marshal(bannerSpec{
		Title: r.Title, Body: r.Body, Color: r.Style.Color, Width: r.Width, Height: r.Height,
		Position: r.Style.Position, Slot: slot, Duration: r.Style.DurationSecs, Sound: r.Style.Sound,
	})
	return []string{"SHH_BANNER=" + string(spec)}
}

// OverlayCommand builds the process that would draw r, without starting it.
func OverlayCommand(r Request) *exec.Cmd {
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", overlayScript)
	cmd.Env = append(os.Environ(), bannerEnv(r, 0)...)
	return cmd
}

func startOsascript(env []string) (func() (string, error), error) {
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", overlayScript)
	cmd.Env = append(os.Environ(), env...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output // JXA's console.log goes to stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() (string, error) {
		err := cmd.Wait()
		lines := strings.Split(strings.TrimSpace(output.String()), "\n")
		outcome := strings.TrimSpace(lines[len(lines)-1])
		if err != nil {
			return outcome, fmt.Errorf("%w: %s", err, strings.TrimSpace(output.String()))
		}
		return outcome, nil
	}, nil
}

func (o *Overlay) Show(r Request) error {
	slot := o.acquire(r.Style.Position)
	wait, err := o.cfg.Start(bannerEnv(r, slot))
	if err != nil {
		o.release(r.Style.Position, slot)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	go func() {
		outcome, err := wait()
		o.release(r.Style.Position, slot)
		switch {
		case outcome == "clicked" && r.BundleID != "":
			if err := o.cfg.Open(r.BundleID); err != nil && o.cfg.OnError != nil {
				o.cfg.OnError(fmt.Errorf("opening %s: %w", r.BundleID, err))
			}
		case outcome != "clicked" && outcome != "expired" && err != nil && o.cfg.OnFailure != nil:
			o.cfg.OnFailure(r, err)
		}
	}()
	return nil
}

func (o *Overlay) acquire(position string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.slots[position] == nil {
		o.slots[position] = map[int]bool{}
	}
	slot := 0
	for o.slots[position][slot] {
		slot++
	}
	o.slots[position][slot] = true
	return slot
}

func (o *Overlay) release(position string, slot int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.slots[position], slot)
}
