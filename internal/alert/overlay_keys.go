package alert

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/hotkey"
)

// The keyboard moves through the banners on screen in the order they arrived, across all positions, picks one with a ring, and presses
// its buttons. The overlay keeps the selection; each banner only draws the ring when told (and the script runs the same press() a click
// does, so a key and a click end the same way).

// Next picks the next banner, wrapping; with none picked it picks the first to arrive.
func (o *Overlay) Next() { o.move(1) }

// Previous picks the one before, wrapping; with none picked it picks the newest.
func (o *Overlay) Previous() { o.move(-1) }

func (o *Overlay) move(step int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.order) == 0 {
		return
	}
	at := slices.Index(o.order, o.selected)
	switch {
	case at >= 0:
		at = (at + step + len(o.order)) % len(o.order)
	case step > 0:
		at = 0
	default:
		at = len(o.order) - 1
	}
	if o.order[at] == o.selected {
		return
	}
	o.tellSelectedLocked(o.selected, false)
	o.selected = o.order[at]
	o.tellSelectedLocked(o.selected, true)
}

// Clear lets go of the selection.
func (o *Overlay) Clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tellSelectedLocked(o.selected, false)
	o.selected = nil
}

// Press presses the selected banner's button of that type, and says whether it has one. A key for a button the banner lacks does nothing.
func (o *Overlay) Press(buttonType string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pressLocked(func(b config.Button) bool { return b.Type == buttonType }, 1)
}

// PressAction presses the nth (from 1) of the selected banner's own action buttons, the ones an alert brings with it.
func (o *Overlay) PressAction(n int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pressLocked(func(b config.Button) bool { return b.Type == ButtonAction }, n)
}

func (o *Overlay) pressLocked(match func(config.Button) bool, nth int) bool {
	if o.selected == nil || o.selected.proc == nil {
		return false
	}
	for i, b := range o.selected.req.Buttons {
		if match(b) {
			if nth--; nth == 0 {
				message, _ := json.Marshal(map[string]int{"press": i})
				_ = o.selected.proc.Send(message)
				return true
			}
		}
	}
	return false
}

// DismissAll closes every banner on screen, picked or not, with or without a Dismiss button.
func (o *Overlay) DismissAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, b := range o.order {
		if b.proc != nil {
			_ = b.proc.Send([]byte(`{"dismiss":true}`))
		}
	}
}

func (o *Overlay) tellSelectedLocked(b *banner, on bool) {
	if b == nil || b.proc == nil {
		return
	}
	message, _ := json.Marshal(map[string]bool{"selected": on})
	_ = b.proc.Send(message)
}

// onKey is a shortcut being pressed.
func (o *Overlay) onKey(action string) {
	switch action {
	case "next":
		o.Next()
	case "previous":
		o.Previous()
	case "clear":
		o.Clear()
	case "dismiss_all":
		o.DismissAll()
	default:
		for n := 1; n <= 9; n++ {
			if action == hotkey.ActionButton(n) {
				o.PressAction(n)
				return
			}
		}
		o.Press(action) // preview, goto, dismiss, flag or train: the button type is the action's name
	}
}

// wantedKeys is the binding the keys should be held under now: nothing when shortcuts are off or no banner is up, else the configured
// keys plus one digit for each action button the busiest banner has.
func (o *Overlay) wantedKeys() (hotkey.Binding, bool) {
	sc := o.cfg.Shortcuts()
	o.mu.Lock()
	defer o.mu.Unlock()
	if !sc.Enabled || len(o.order) == 0 {
		return hotkey.Binding{}, false
	}
	b := hotkey.Binding{Modifier: sc.Modifier, Keys: map[string]string{}}
	for action, key := range sc.Keys {
		if key != "" {
			b.Keys[action] = key
		}
	}
	most := 0
	for _, banner := range o.order {
		n := 0
		for _, button := range banner.req.Buttons {
			if button.Type == ButtonAction {
				n++
			}
		}
		most = max(most, n)
	}
	for n := 1; n <= min(most, 9); n++ {
		b.Keys[hotkey.ActionButton(n)] = string(rune('0' + n))
	}
	return b, true
}

// syncKeys brings the held keys in line with what is wanted, without making the caller wait for the helper to start. Each run looks at
// the state as it is when its turn comes, so the last change always wins however the runs interleave.
func (o *Overlay) syncKeys() {
	if o.cfg.Hotkeys == nil || o.cfg.Shortcuts == nil {
		return
	}
	go func() {
		o.keys.mu.Lock()
		defer o.keys.mu.Unlock()
		want, ok := o.wantedKeys()
		switch {
		case !ok && o.keys.held != nil:
			o.cfg.Hotkeys.Unregister()
			o.keys.held = nil
		case ok && (o.keys.held == nil || !reflect.DeepEqual(*o.keys.held, want)):
			err := o.cfg.Hotkeys.Register(want, o.onKey)
			o.keys.held = &want
			if err != nil {
				// Keys that were not given are reported once, not for every banner; nothing running at all is tried again next time.
				var refused *hotkey.RegisterError
				if !errors.As(err, &refused) || len(refused.Refused) >= len(want.Keys) {
					o.keys.held = nil
				}
				if o.cfg.OnKeyError != nil {
					o.cfg.OnKeyError(err)
				}
			}
		}
	}()
}
