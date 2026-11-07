package alert

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
)

//go:embed overlay.js
var overlayScript string

// Process is one running banner. Go keeps the layout of everything on screen and tells each banner where to be by sending it
// lines of JSON on its standard input; the banner reports what happens to it as event lines.
type Process interface {
	// Send writes one message to the banner: {"offset": n, "height": h} moves and resizes it. Errors are ignored by the caller,
	// since a banner that is gone is about to be reported by Wait.
	Send(message []byte) error
	// Wait blocks until the banner ends and reports how: "opened" (Go to), "dismissed", "expired", or an error.
	Wait() (string, error)
}

// OverlayConfig configures the AppKit overlay backend. The zero value runs the real thing; tests replace Start.
type OverlayConfig struct {
	// Start launches one banner process with the extra environment. onEvent hears each event line the banner prints, from the
	// goroutine that reads its output.
	Start func(env []string, onEvent func(line string)) (Process, error)
	// OnFailure receives alerts whose banner process died without showing them, so they can be shown another way.
	OnFailure func(Request, error)
}

// Overlay draws each banner in its own osascript process (see docs/spikes/03-banner.md), in a window of its own that the banner
// suppressor never touches, so a banner outlives the process that showed it. The Overlay owns the layout: banners at one screen
// position form a stack in the order they arrived, and whenever one appears, disappears, or grows or shrinks (Preview and Collapse),
// the ones after it are told their new place. The Go to button opens the source app from inside the banner's process.
type Overlay struct {
	cfg OverlayConfig

	mu     sync.Mutex
	stacks map[string][]*banner // banners on screen, by position, nearest the screen edge first
}

// banner is what the overlay knows about one banner on screen.
type banner struct {
	req       Request
	proc      Process
	height    int // the current height, which Preview and Collapse change
	sent      layoutMessage
	maxHeight int
}

type layoutMessage struct {
	Offset int `json:"offset"`
	Height int `json:"height"`
}

const stackGap = 10

func NewOverlay(cfg OverlayConfig) *Overlay {
	if cfg.Start == nil {
		cfg.Start = startOsascript
	}
	return &Overlay{cfg: cfg, stacks: map[string][]*banner{}}
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
	// Managed says Go is on the other end of the banner's standard input and output, sending layout changes and reading events.
	Managed bool `json:"managed"`
}

type bannerButton struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	// CollapseLabel is what the Preview button says while the banner is expanded.
	CollapseLabel string `json:"collapseLabel,omitempty"`
}

func bannerEnv(r Request, offset int) []string {
	buttons := make([]bannerButton, 0, len(r.Buttons))
	for _, b := range r.Buttons {
		buttons = append(buttons, bannerButton{Type: b.Type, Label: b.Label, CollapseLabel: b.CollapseLabel})
	}
	spec, _ := json.Marshal(bannerSpec{
		Title: r.Title, Body: r.Body, Bundle: r.BundleID, Color: r.Style.Color, Width: r.Width, Height: r.Height,
		Position: r.Style.Position, Offset: offset, Duration: r.Style.DurationSecs, Sound: r.Style.Sound,
		Buttons: buttons, DismissOnClick: r.DismissOnClick, PreviewHeight: r.PreviewHeight, Managed: true,
	})
	return []string{"SHH_BANNER=" + string(spec)}
}

// OverlayCommand builds the process that would draw r, without starting it.
func OverlayCommand(r Request) *exec.Cmd {
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", overlayScript)
	cmd.Env = append(os.Environ(), bannerEnv(r, 0)...)
	return cmd
}

type osascriptProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex
	output chan string // the last line the banner printed that was not an event, delivered once it has ended
}

func startOsascript(env []string, onEvent func(string)) (Process, error) {
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", overlayScript)
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = writer, writer // JXA's console.log goes to stderr
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	writer.Close() // the child holds its own copy; ours must go so the reader sees the end
	p := &osascriptProcess{cmd: cmd, stdin: stdin, output: make(chan string, 1)}
	go func() {
		var all []string
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if event, ok := strings.CutPrefix(line, "event "); ok {
				onEvent(event)
				continue
			}
			if line != "" {
				all = append(all, line)
			}
		}
		reader.Close()
		p.output <- strings.Join(all, "\n")
	}()
	return p, nil
}

func (p *osascriptProcess) Send(message []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.stdin.Write(append(message, '\n'))
	return err
}

func (p *osascriptProcess) Wait() (string, error) {
	err := p.cmd.Wait()
	output := <-p.output
	lines := strings.Split(output, "\n")
	outcome := strings.TrimSpace(lines[len(lines)-1])
	if err != nil {
		return outcome, fmt.Errorf("%w: %s", err, output)
	}
	return outcome, nil
}

func (o *Overlay) Show(r Request) error {
	b := &banner{req: r, height: r.Height, maxHeight: max(r.PreviewHeight, r.Height)}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stacks[r.Style.Position] = append(o.stacks[r.Style.Position], b)
	offset := o.offsetOf(b)
	b.sent = layoutMessage{Offset: offset, Height: b.height}
	proc, err := o.cfg.Start(bannerEnv(r, offset), func(line string) { o.onEvent(b, line) })
	if err != nil {
		o.removeLocked(b)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	b.proc = proc
	go func() {
		outcome, err := proc.Wait()
		o.mu.Lock()
		o.removeLocked(b)
		o.layoutLocked(r.Style.Position)
		o.mu.Unlock()
		if outcome != "opened" && outcome != "dismissed" && outcome != "expired" && err != nil && o.cfg.OnFailure != nil {
			o.cfg.OnFailure(r, err)
		}
	}()
	return nil
}

// onEvent handles what a banner reports. "height N" says Preview or Collapse changed its height; the banner has already resized
// itself, and the overlay now moves the banners after it.
func (o *Overlay) onEvent(b *banner, line string) {
	field, value, _ := strings.Cut(line, " ")
	if field != "height" {
		return
	}
	height, err := strconv.Atoi(value)
	if err != nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.onScreenLocked(b) {
		return
	}
	b.height = min(max(height, b.req.Height), b.maxHeight)
	o.layoutLocked(b.req.Style.Position)
}

func (o *Overlay) onScreenLocked(b *banner) bool {
	return slices.Contains(o.stacks[b.req.Style.Position], b)
}

// offsetOf is how far b sits from its screen edge: the heights of the banners before it plus a gap after each.
func (o *Overlay) offsetOf(b *banner) int {
	offset := 0
	for _, other := range o.stacks[b.req.Style.Position] {
		if other == b {
			break
		}
		offset += other.height + stackGap
	}
	return offset
}

// layoutLocked tells every banner at a position whose place changed where it belongs now.
func (o *Overlay) layoutLocked(position string) {
	offset := 0
	for _, b := range o.stacks[position] {
		want := layoutMessage{Offset: offset, Height: b.height}
		offset += b.height + stackGap
		if want == b.sent || b.proc == nil {
			continue
		}
		b.sent = want
		message, _ := json.Marshal(want)
		_ = b.proc.Send(message)
	}
}

// Active is how many banners are on screen.
func (o *Overlay) Active() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, stack := range o.stacks {
		n += len(stack)
	}
	return n
}

func (o *Overlay) removeLocked(b *banner) {
	stack := o.stacks[b.req.Style.Position]
	if i := slices.Index(stack, b); i >= 0 {
		o.stacks[b.req.Style.Position] = slices.Delete(stack, i, i+1)
	}
}
