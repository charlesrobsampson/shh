package menubar

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/session"
)

//go:embed statusitem.js
var statusItemScript string

// Controller is the part of the worker's client the menu bar uses; *session.Client satisfies it.
type Controller interface {
	Status() (session.StatusView, error)
	Pause() (session.StatusView, error)
	Resume() (session.StatusView, error)
	Adjust(delta time.Duration) (session.StatusView, error)
	Stop(all bool) (session.StatusView, error)
}

// RoutineController is the worker's routine commands. A Controller that also has them gets the routine items of the menu to work;
// *session.Client does.
type RoutineController interface {
	RoutineNext() (session.RoutineInfo, error)
	RoutineBack() (session.RoutineInfo, error)
	RoutineStop() (session.RoutineInfo, error)
}

// Helper is one status item on screen. Send replaces what it shows; Clicks delivers the id of each menu item chosen and is closed
// when the item has gone, whether it was stopped or died; Stop removes it.
type Helper interface {
	Send(View) error
	Clicks() <-chan string
	Stop()
}

// Config is everything Run needs from the outside world.
type Config struct {
	Controller Controller
	// Label starts the title, for example "shh".
	Label string
	// Open handles the "Open shh…" item, typically by opening a terminal with the UI. Nil means the item does nothing.
	Open func() error
	// Start launches the status item. The default runs statusitem.js with osascript.
	Start func() (Helper, error)
	// Interval is how often the worker is asked for its status; one second when zero.
	Interval time.Duration
	// RestartDelay is how long to wait before relaunching a status item that died; one second when zero.
	RestartDelay time.Duration
	// GoneAfter is how many polls in a row may find no worker before Run gives up; 2 when zero. The menu bar item exists for the
	// worker's sake, so it leaves when the worker does.
	GoneAfter int
	// Gone, if set, is asked at every poll: true means the process that started this one (the worker) is no longer there, and the item
	// leaves at once. Without it a new worker that answers on the same socket would keep an old item alive beside its own.
	Gone    func() bool
	OnError func(error)
}

// Run keeps the menu bar item up to date until ctx ends or the worker is gone: once per Interval it asks the worker for its status
// and, when what the item shows has changed, sends it. Clicks on the menu act on the worker and refresh the item at once.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Interval == 0 {
		cfg.Interval = time.Second
	}
	if cfg.RestartDelay == 0 {
		cfg.RestartDelay = time.Second
	}
	if cfg.GoneAfter == 0 {
		cfg.GoneAfter = 2
	}
	if cfg.Start == nil {
		cfg.Start = startOsascript
	}
	r := &runner{cfg: cfg}
	return r.run(ctx)
}

type runner struct {
	cfg    Config
	helper Helper
	last   View
	misses int
}

func (r *runner) report(err error) {
	if r.cfg.OnError != nil {
		r.cfg.OnError(err)
	}
}

func (r *runner) run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	defer func() {
		if r.helper != nil {
			r.helper.Stop()
		}
	}()
	var restart <-chan time.Time
	if err := r.launch(); err != nil {
		return err
	}
	if r.refresh() {
		return nil
	}
	for {
		var clicks <-chan string
		if r.helper != nil {
			clicks = r.helper.Clicks()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if r.refresh() {
				return nil
			}
		case id, ok := <-clicks:
			if !ok { // the status item died: bring it back
				r.helper = nil
				restart = time.After(r.cfg.RestartDelay)
				continue
			}
			r.act(id)
			if r.refresh() {
				return nil
			}
		case <-restart:
			restart = nil
			if err := r.launch(); err != nil {
				r.report(err)
				restart = time.After(r.cfg.RestartDelay)
				continue
			}
			if r.refresh() {
				return nil
			}
		}
	}
}

func (r *runner) launch() error {
	h, err := r.cfg.Start()
	if err != nil {
		return fmt.Errorf("starting the menu bar item: %w", err)
	}
	r.helper, r.last = h, View{}
	return nil
}

// refresh polls the worker and updates the item. It reports whether the worker is gone.
func (r *runner) refresh() (gone bool) {
	if r.cfg.Gone != nil && r.cfg.Gone() {
		return true
	}
	st, err := r.cfg.Controller.Status()
	if errors.Is(err, session.ErrNotRunning) {
		r.misses++
		return r.misses >= r.cfg.GoneAfter
	}
	r.misses = 0
	if err != nil {
		r.report(err)
		return false
	}
	if r.helper == nil {
		return false
	}
	view := Build(st, r.cfg.Label)
	if reflect.DeepEqual(view, r.last) {
		return false
	}
	if err := r.helper.Send(view); err != nil {
		return false // the item is going away; its click channel closing will bring it back
	}
	r.last = view
	return false
}

// act does what a menu item says.
func (r *runner) act(id string) {
	c := r.cfg.Controller
	var err error
	switch id {
	case IDPause:
		_, err = c.Pause()
	case IDResume:
		_, err = c.Resume()
	case IDPlus:
		_, err = c.Adjust(5 * time.Minute)
	case IDMinus:
		_, err = c.Adjust(-5 * time.Minute)
	case IDStop:
		_, err = c.Stop(false)
	case IDOff:
		_, err = c.Stop(true)
	case IDRoutineNext, IDRoutineBack, IDRoutineStop:
		rc, ok := c.(RoutineController)
		if !ok {
			err = fmt.Errorf("this controller cannot steer routines")
			break
		}
		switch id {
		case IDRoutineNext:
			_, err = rc.RoutineNext()
		case IDRoutineBack:
			_, err = rc.RoutineBack()
		default:
			_, err = rc.RoutineStop()
		}
	case IDOpen:
		if r.cfg.Open != nil {
			err = r.cfg.Open()
		}
	}
	if err != nil {
		r.report(fmt.Errorf("menu item %q: %w", id, err))
	}
}

// osascriptHelper is the real status item: statusitem.js in an osascript process.
type osascriptHelper struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	clicks chan string
}

func startOsascript() (Helper, error) {
	spec, _ := json.Marshal(map[string]int{"parent": os.Getpid()})
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", statusItemScript)
	cmd.Env = append(os.Environ(), "SHH_MENUBAR="+string(spec))
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
	writer.Close()
	h := &osascriptHelper{cmd: cmd, stdin: stdin, clicks: make(chan string, 8)}
	go func() {
		defer close(h.clicks)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			if id, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "event click "); ok {
				h.clicks <- id
			}
		}
		reader.Close()
		cmd.Wait()
	}()
	return h, nil
}

func (h *osascriptHelper) Send(v View) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = h.stdin.Write(append(line, '\n'))
	return err
}

func (h *osascriptHelper) Clicks() <-chan string { return h.clicks }

func (h *osascriptHelper) Stop() {
	h.stdin.Close()
	if h.cmd.Process != nil {
		h.cmd.Process.Kill()
	}
}

// Script is the status item script, for tests.
func Script() string { return statusItemScript }
