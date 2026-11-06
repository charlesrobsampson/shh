package alert

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

//go:embed overlay.js
var overlayScript string

// OverlayConfig configures the AppKit overlay backend. The zero value runs the real thing; tests replace Start and Open.
type OverlayConfig struct {
	// Start launches one banner process with the extra environment and returns a function that waits for it and reports how it
	// ended: "opened" (Go to), "dismissed", "expired", or an error.
	Start func(env []string) (wait func() (string, error), err error)
	// OnFailure receives alerts whose banner process died without showing them, so they can be shown another way.
	OnFailure func(Request, error)
}

// Overlay draws each banner in its own osascript process (see docs/spikes/03-banner.md), in a window of its own that the banner
// suppressor never touches. Banners that are on screen together are placed so they stack instead of overlapping, each in the
// first free space that fits its height. The Go to button opens the source app from inside that process, so it works however long the
// caller lives.
type Overlay struct {
	cfg OverlayConfig

	mu     sync.Mutex
	active map[string][]*placed // banners on screen, by position
}

// placed is a banner's vertical extent, measured in points from the screen edge it hangs from.
type placed struct{ offset, height int }

const stackGap = 10

func NewOverlay(cfg OverlayConfig) *Overlay {
	if cfg.Start == nil {
		cfg.Start = startOsascript
	}
	return &Overlay{cfg: cfg, active: map[string][]*placed{}}
}

type bannerSpec struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// Bundle is the app the banner script opens when the banner is clicked. The script does it itself, because the process that
	// showed the banner (a short-lived CLI command, say) may be gone by the time of the click.
	Bundle   string `json:"bundle"`
	Color    string `json:"color"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Position string `json:"position"`
	// Offset is how far from its screen edge the banner sits, so banners shown together do not overlap.
	Offset   int    `json:"offset"`
	Duration int    `json:"duration"`
	Sound    string `json:"sound"`
	// Buttons are drawn left to right. Pressing one of type preview, goto or dismiss does that; the outcome the script reports is
	// "dismissed", "opened" or "expired".
	Buttons        []bannerButton `json:"buttons"`
	DismissOnClick bool           `json:"dismissOnClick"`
	// PreviewHeight is the height the banner grows to on Preview.
	PreviewHeight int `json:"previewHeight"`
}

type bannerButton struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

func bannerEnv(r Request, offset int) []string {
	buttons := make([]bannerButton, 0, len(r.Buttons))
	for _, b := range r.Buttons {
		buttons = append(buttons, bannerButton{Type: b.Type, Label: b.Label})
	}
	spec, _ := json.Marshal(bannerSpec{
		Title: r.Title, Body: r.Body, Bundle: r.BundleID, Color: r.Style.Color, Width: r.Width, Height: r.Height,
		Position: r.Style.Position, Offset: offset, Duration: r.Style.DurationSecs, Sound: r.Style.Sound,
		Buttons: buttons, DismissOnClick: r.DismissOnClick, PreviewHeight: r.PreviewHeight,
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
	spot := o.place(r.Style.Position, r.Height)
	wait, err := o.cfg.Start(bannerEnv(r, spot.offset))
	if err != nil {
		o.remove(r.Style.Position, spot)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	go func() {
		outcome, err := wait()
		o.remove(r.Style.Position, spot)
		if outcome != "opened" && outcome != "dismissed" && outcome != "expired" && err != nil && o.cfg.OnFailure != nil {
			o.cfg.OnFailure(r, err)
		}
	}()
	return nil
}

// place reserves the first space, from the screen edge outwards, where a banner of this height fits without touching another.
func (o *Overlay) place(position string, height int) *placed {
	o.mu.Lock()
	defer o.mu.Unlock()
	candidates := []int{0}
	for _, other := range o.active[position] {
		candidates = append(candidates, other.offset+other.height+stackGap)
	}
	sort.Ints(candidates)
	for _, offset := range candidates {
		clear := true
		for _, other := range o.active[position] {
			if offset < other.offset+other.height+stackGap && offset+height+stackGap > other.offset {
				clear = false
				break
			}
		}
		if clear {
			spot := &placed{offset: offset, height: height}
			o.active[position] = append(o.active[position], spot)
			return spot
		}
	}
	panic("unreachable: the space past the last banner is always free")
}

func (o *Overlay) remove(position string, spot *placed) {
	o.mu.Lock()
	defer o.mu.Unlock()
	banners := o.active[position]
	for i, b := range banners {
		if b == spot {
			o.active[position] = append(banners[:i], banners[i+1:]...)
			return
		}
	}
}
