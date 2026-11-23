// Package hotkey holds shh's global keyboard shortcuts: one chord of modifiers plus a key for each action (plan.md §7.2). The keys are
// held by a helper process (`shh hotkeys helper`) because the macOS event loop that delivers them has to own the main thread of its
// process; the worker talks to the helper in JSON lines, and the helper is the only code that touches the operating system.
package hotkey

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charlesrobsampson/shh/internal/config"
)

// ErrUnsupported is what the helper reports on a system that has no implementation.
var ErrUnsupported = errors.New("global keyboard shortcuts are not available on this system")

// Binding is what to hold: the chord every key shares and the key of each action. An action with no key is not held.
type Binding struct {
	Modifier config.Modifiers
	Keys     map[string]string
}

// Hotkeys holds a binding for as long as it is wanted.
type Hotkeys interface {
	// Register holds the binding, replacing any earlier one, and calls onPress with the action each time its key is pressed, from a
	// goroutine of its own. A key the system will not give (another app holds it) is a *RegisterError; the keys that could be held stay
	// held and still call onPress, unless none could.
	Register(b Binding, onPress func(action string)) error
	// Unregister lets go of every key. It does nothing when none is held.
	Unregister()
}

// RegisterError says which keys could not be held and why.
type RegisterError struct {
	// Refused maps an action to the reason its key was not given.
	Refused map[string]string
}

func (e *RegisterError) Error() string {
	actions := make([]string, 0, len(e.Refused))
	for action := range e.Refused {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	parts := make([]string, len(actions))
	for i, action := range actions {
		parts[i] = fmt.Sprintf("%s (%s)", action, e.Refused[action])
	}
	return "these shortcuts could not be registered, another app may hold the keys: " + strings.Join(parts, ", ")
}

// request is the first line the helper reads: the binding. The lines it writes back are events.
type request struct {
	Modifier string            `json:"modifier"`
	Keys     map[string]string `json:"keys"`
}

type event struct {
	Event  string `json:"event"` // ready (every key has been tried), refused or press
	Action string `json:"action,omitempty"`
	Reason string `json:"reason,omitempty"`
}
