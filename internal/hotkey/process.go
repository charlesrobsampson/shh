package hotkey

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Process holds a binding in a helper process that it starts for the purpose and ends when the keys are let go, so an idle shh holds
// no keys. It is the Hotkeys the worker uses.
type Process struct {
	// Command builds the helper to run: `shh hotkeys helper`. It is called once for each Register. A file given as its Stderr is closed
	// once the helper has started (the helper has its own copy), so a log file opened for each call does not pile up.
	Command func() *exec.Cmd
	// Timeout is how long Register waits for the helper to say it is ready; 5 seconds when zero.
	Timeout time.Duration
	// Grace is how long a helper gets to end by itself, once told to, before it is killed; 2 seconds when zero.
	Grace time.Duration

	mu  sync.Mutex
	cur *helper
}

var _ Hotkeys = (*Process)(nil)

type helper struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	exited  chan struct{} // closed once the helper has ended and its output is read
	stopped bool          // set under Process.mu; presses that arrive after it are dropped
}

func (p *Process) Register(b Binding, onPress func(action string)) error {
	req := request{Modifier: b.Modifier.String(), Keys: map[string]string{}}
	for action, key := range b.Keys {
		if key != "" {
			req.Keys[action] = key
		}
	}
	if len(req.Keys) == 0 {
		return errors.New("there are no shortcuts to register")
	}
	p.Unregister()

	cmd := p.Command()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the hotkey helper: %w", err)
	}
	if f, ok := cmd.Stderr.(*os.File); ok {
		f.Close()
	}
	h := &helper{cmd: cmd, stdin: stdin, exited: make(chan struct{})}
	ready := make(chan map[string]string, 1)
	go func() {
		refused := map[string]string{}
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var e event
			if json.Unmarshal(scanner.Bytes(), &e) != nil {
				continue
			}
			switch e.Event {
			case "refused":
				refused[e.Action] = e.Reason
			case "ready":
				ready <- refused
			case "press":
				p.mu.Lock()
				live := !h.stopped
				p.mu.Unlock()
				if live {
					onPress(e.Action)
				}
			}
		}
		_ = cmd.Wait() // only once its output is all read
		close(h.exited)
	}()

	line, _ := json.Marshal(req)
	if _, err := stdin.Write(append(line, '\n')); err != nil {
		p.stop(h)
		return fmt.Errorf("telling the hotkey helper what to hold: %w", err)
	}
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	select {
	case refused := <-ready:
		p.mu.Lock()
		p.cur = h
		p.mu.Unlock()
		if len(refused) == 0 {
			return nil
		}
		if len(refused) == len(req.Keys) {
			p.Unregister()
		}
		return &RegisterError{Refused: refused}
	case <-h.exited:
		return errors.New("the hotkey helper ended before it was ready (see shh.log)")
	case <-time.After(timeout):
		p.stop(h)
		return fmt.Errorf("the hotkey helper did not answer within %s", timeout)
	}
}

func (p *Process) Unregister() {
	p.mu.Lock()
	h := p.cur
	p.cur = nil
	p.mu.Unlock()
	if h != nil {
		p.stop(h)
	}
}

// stop ends a helper: closing its input is the polite way, and it is killed if it has not gone after a moment.
func (p *Process) stop(h *helper) {
	p.mu.Lock()
	h.stopped = true
	p.mu.Unlock()
	_ = h.stdin.Close()
	grace := p.Grace
	if grace == 0 {
		grace = 2 * time.Second
	}
	select {
	case <-h.exited:
	case <-time.After(grace):
		_ = h.cmd.Process.Kill()
		<-h.exited
	}
}

// Running says whether a helper is holding keys.
func (p *Process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur == nil {
		return false
	}
	select {
	case <-p.cur.exited:
		return false
	default:
		return true
	}
}

// PID is the helper's process id, or 0 when none is running.
func (p *Process) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur == nil {
		return 0
	}
	return p.cur.cmd.Process.Pid
}
