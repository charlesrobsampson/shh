package testkit

import (
	"slices"
	"sync"

	"github.com/charlesrobsampson/shh/internal/hotkey"
)

var _ hotkey.Hotkeys = (*FakeHotkeys)(nil)

// FakeHotkeys holds a binding in memory. A test presses its keys with Press, and can make the system refuse some of them (Refuse), as
// when another app has the chord.
type FakeHotkeys struct {
	mu      sync.Mutex
	held    map[string]string // action to key, for the keys that were given
	mods    hotkey.Binding
	onPress func(string)
	active  bool
	refused []string
	// Registrations counts the calls to Register, successful or not.
	registrations int
}

func NewFakeHotkeys() *FakeHotkeys { return &FakeHotkeys{} }

// Refuse makes the system refuse these keys the next time they are registered.
func (f *FakeHotkeys) Refuse(keys ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refused = keys
}

func (f *FakeHotkeys) Register(b hotkey.Binding, onPress func(string)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registrations++
	f.active, f.mods, f.onPress, f.held = false, b, onPress, map[string]string{}
	refused := map[string]string{}
	for action, key := range b.Keys {
		switch {
		case key == "":
		case slices.Contains(f.refused, key):
			refused[action] = "already held by another app"
		default:
			f.held[action] = key
		}
	}
	if len(f.held) == 0 && len(refused) == 0 {
		return &hotkey.RegisterError{Refused: map[string]string{"": "there are no shortcuts to register"}}
	}
	f.active = len(f.held) > 0
	if len(refused) > 0 {
		return &hotkey.RegisterError{Refused: refused}
	}
	return nil
}

func (f *FakeHotkeys) Unregister() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active, f.held, f.onPress = false, nil, nil
}

// Held is the binding in force: false when nothing is held. Only the keys that were given are in it.
func (f *FakeHotkeys) Held() (hotkey.Binding, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.active {
		return hotkey.Binding{}, false
	}
	keys := make(map[string]string, len(f.held))
	for action, key := range f.held {
		keys[action] = key
	}
	return hotkey.Binding{Modifier: f.mods.Modifier, Keys: keys}, true
}

// Registrations is how many times Register was called.
func (f *FakeHotkeys) Registrations() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registrations
}

// Press presses the key of an action, and says whether it was held, so whether anyone heard.
func (f *FakeHotkeys) Press(action string) bool {
	f.mu.Lock()
	_, held := f.held[action]
	onPress := f.onPress
	f.mu.Unlock()
	if !held || onPress == nil {
		return false
	}
	onPress(action)
	return true
}
