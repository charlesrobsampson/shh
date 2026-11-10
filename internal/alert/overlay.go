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
	// OnFlag hears that the Flag button of a banner was switched on or off, with the id of the notification it is about, so it can be
	// marked (or unmarked) for review. Called from the goroutine that reads the banner's output, only when the state changes. Optional.
	OnFlag func(notificationID int64, flagged bool)
	// OnAction hears that an action button of a banner was pressed: the alert's ActionToken and the button's ID. Called from the
	// goroutine that reads the banner's output. Optional.
	OnAction func(token, id string)
}

// Overlay draws each banner in its own osascript process (see docs/spikes/03-banner.md), in a window of its own that the banner
// suppressor never touches, so a banner outlives the process that showed it. The Overlay owns the layout: banners at one screen
// position form a stack in the order they arrived, and whenever one appears, disappears, or grows or shrinks (Preview and Collapse),
// the ones after it are told their new place. The Go to button opens the source app from inside the banner's process.
type Overlay struct {
	cfg OverlayConfig

	mu     sync.Mutex
	stacks map[string][]*banner // banners on screen, by position, nearest the screen edge first
	// screen is the height banners can use, as reported by a banner; 0 until one has. A stack taller than that continues in a
	// further column, next to the first and further in from the screen edge.
	screen int
}

// banner is what the overlay knows about one banner on screen.
type banner struct {
	flagged   bool // the Flag button is on
	req       Request
	proc      Process
	height    int // the current height, which Preview and Collapse change
	sent      layoutMessage
	maxHeight int
}

// layoutMessage is where a banner goes: Indent points in from its screen edge (it is in a further column when more than 0),
// Offset points down (or up, at the bottom) from the screen edge, and Height tall.
type layoutMessage struct {
	Indent int `json:"indent"`
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
	// Header is the app the alert is from, drawn small above the title; empty draws nothing.
	Header string `json:"header"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	// Bundle is the app the banner script opens when the banner is clicked. The script does it itself, because the process that
	// showed the banner (a short-lived CLI command, say) may be gone by the time of the click.
	Bundle   string `json:"bundle"`
	Color    string `json:"color"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Position string `json:"position"`
	// Indent and Offset say where the banner sits, so banners shown together do not overlap.
	Indent   int    `json:"indent"`
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
	// Parent is the process id of the Go process that started the banner. The banner compares it with its own parent to tell that Go
	// has gone; it cannot learn it for itself, because Go may already be gone by the time the script starts.
	Parent int `json:"parent"`
}

type bannerButton struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	// CollapseLabel is what the Preview button says while the banner is expanded.
	CollapseLabel string `json:"collapseLabel,omitempty"`
	// FlaggedLabel is what the Flag button says once it has been pressed.
	FlaggedLabel string `json:"flaggedLabel,omitempty"`
	// ID is what an action button reports when it is pressed.
	ID string `json:"id,omitempty"`
}

func bannerEnv(r Request, at layoutMessage) []string {
	buttons := make([]bannerButton, 0, len(r.Buttons))
	for _, b := range r.Buttons {
		buttons = append(buttons, bannerButton{Type: b.Type, Label: b.Label, CollapseLabel: b.CollapseLabel, FlaggedLabel: b.FlaggedLabel, ID: b.ID})
	}
	spec, _ := json.Marshal(bannerSpec{
		Header: strings.TrimSpace(r.Source), Title: r.Title, Body: r.Body, Bundle: r.BundleID, Color: r.Style.Color, Width: r.Width, Height: r.Height,
		Position: r.Style.Position, Indent: at.Indent, Offset: at.Offset, Duration: r.Style.DurationSecs, Sound: r.Style.Sound,
		Buttons: buttons, DismissOnClick: r.DismissOnClick, PreviewHeight: r.PreviewHeight, Managed: true, Parent: os.Getpid(),
	})
	return []string{"SHH_BANNER=" + string(spec)}
}

// OverlayCommand builds the process that would draw r, without starting it.
func OverlayCommand(r Request) *exec.Cmd {
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", overlayScript)
	cmd.Env = append(os.Environ(), bannerEnv(r, layoutMessage{Height: r.Height})...)
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
	b.sent = o.slotOf(b)
	proc, err := o.cfg.Start(bannerEnv(r, b.sent), func(line string) { o.onEvent(b, line) })
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

// onEvent handles what a banner reports. "screen N" is the height the screen offers banners; "height N" says Preview or Collapse changed its height; the banner has already resized
// itself, and the overlay now moves the banners after it.
func (o *Overlay) onEvent(b *banner, line string) {
	field, value, _ := strings.Cut(line, " ")
	if field == "flag" {
		o.flag(b, value == "1")
		return
	}
	if field == "action" {
		o.mu.Lock()
		ok := o.onScreenLocked(b)
		o.mu.Unlock()
		if ok && o.cfg.OnAction != nil {
			o.cfg.OnAction(b.req.ActionToken, value)
		}
		return
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	switch field {
	case "height":
		if o.onScreenLocked(b) {
			b.height = min(max(n, b.req.Height), b.maxHeight)
			o.layoutLocked(b.req.Style.Position)
		}
	case "screen":
		if n > 0 && n != o.screen {
			o.screen = n
			for position := range o.stacks {
				o.layoutLocked(position)
			}
		}
	}
}

// flag passes a change of a banner's Flag button on to the hook: only a real change, and only for a banner that is still on screen
// and is about a stored notification.
func (o *Overlay) flag(b *banner, on bool) {
	o.mu.Lock()
	ok := o.onScreenLocked(b) && b.req.NotificationID != 0 && b.flagged != on
	if ok {
		b.flagged = on
	}
	id := b.req.NotificationID
	o.mu.Unlock()
	if ok && o.cfg.OnFlag != nil {
		o.cfg.OnFlag(id, on)
	}
}

func (o *Overlay) onScreenLocked(b *banner) bool {
	return slices.Contains(o.stacks[b.req.Style.Position], b)
}

// slots places every banner at a position: down the screen edge in order, and when the next one would run past the bottom of
// the screen, at the top of a new column further in. A column is as wide as its widest banner.
func (o *Overlay) slots(position string) []layoutMessage {
	stack := o.stacks[position]
	out := make([]layoutMessage, len(stack))
	indent, offset, widest := 0, 0, 0
	for i, b := range stack {
		if o.screen > 0 && offset > 0 && offset+b.height > o.screen {
			indent += widest + stackGap
			offset, widest = 0, 0
		}
		out[i] = layoutMessage{Indent: indent, Offset: offset, Height: b.height}
		offset += b.height + stackGap
		widest = max(widest, b.req.Width)
	}
	return out
}

// slotOf is where b goes now.
func (o *Overlay) slotOf(b *banner) layoutMessage {
	stack := o.stacks[b.req.Style.Position]
	return o.slots(b.req.Style.Position)[slices.Index(stack, b)]
}

// layoutLocked tells every banner at a position whose place changed where it belongs now.
func (o *Overlay) layoutLocked(position string) {
	slots := o.slots(position)
	for i, b := range o.stacks[position] {
		want := slots[i]
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
