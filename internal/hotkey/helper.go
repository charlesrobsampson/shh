package hotkey

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/charlesrobsampson/shh/internal/config"
)

// Platform is the part of the helper that is the operating system's: it takes keys and tells when they are pressed. Register and Run
// are called on the thread the process started on, because that is the thread the macOS event loop must own.
type Platform interface {
	// Start prepares to hear presses; onPress is told the id given to Register, from the thread that runs the loop.
	Start(onPress func(id int)) error
	// Register holds one key of the chord, and fails when the system will not give it.
	Register(mods config.Modifiers, key string, id int) error
	// Run delivers presses until Stop is called.
	Run() error
	// Stop ends Run, or the whole process when the platform cannot leave Run any other way. It may be called from any goroutine.
	Stop()
}

// Serve is the helper process: it reads the binding from in, holds every key it can, says what became of each on out, and then reports
// presses until in ends, which is how it learns that the worker is gone.
func Serve(in io.Reader, out io.Writer, p Platform) error {
	reader := bufio.NewReader(in)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("reading the shortcuts to hold: %w", err)
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return fmt.Errorf("the shortcuts to hold are not understood: %w", err)
	}
	mods, err := config.ParseModifiers(req.Modifier)
	if err != nil {
		return err
	}

	var mu sync.Mutex
	encoder := json.NewEncoder(out)
	emit := func(e event) {
		mu.Lock()
		defer mu.Unlock()
		_ = encoder.Encode(e) // a worker that has gone is noticed through in
	}
	if err := p.Start(func(id int) {
		if id >= 1 && id <= len(config.ShortcutActions) {
			emit(event{Event: "press", Action: config.ShortcutActions[id-1]})
		}
	}); err != nil {
		return err
	}
	for i, action := range config.ShortcutActions { // the id is the action's place in the list, so a press names its action
		key := req.Keys[action]
		if key == "" {
			continue
		}
		if err := p.Register(mods, key, i+1); err != nil {
			emit(event{Event: "refused", Action: action, Reason: err.Error()})
		}
	}
	emit(event{Event: "ready"})

	go func() {
		_, _ = io.Copy(io.Discard, reader)
		p.Stop()
	}()
	if err := p.Run(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
