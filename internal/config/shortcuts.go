package config

import (
	"fmt"
	"slices"
	"strings"
)

// Modifiers is the chord every banner shortcut shares.
type Modifiers uint8

const (
	Ctrl Modifiers = 1 << iota
	Alt
	Shift
	Cmd
)

// modifierOrder is the order a chord is written in, and the names each modifier answers to.
var modifierOrder = []struct {
	flag  Modifiers
	name  string
	other []string
}{
	{Ctrl, "ctrl", []string{"control"}},
	{Alt, "alt", []string{"option", "opt"}},
	{Shift, "shift", nil},
	{Cmd, "cmd", []string{"command"}},
}

// ParseModifiers reads modifiers joined by +, in any order and case.
func ParseModifiers(s string) (Modifiers, error) {
	var out Modifiers
	for _, word := range strings.Split(s, "+") {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" {
			continue
		}
		i := slices.IndexFunc(modifierOrder, func(m struct {
			flag  Modifiers
			name  string
			other []string
		}) bool {
			return m.name == word || slices.Contains(m.other, word)
		})
		if i < 0 {
			return 0, fmt.Errorf("%q is not a modifier (use ctrl, alt, shift or cmd joined by +)", word)
		}
		if out&modifierOrder[i].flag != 0 {
			return 0, fmt.Errorf("%q is given twice", modifierOrder[i].name)
		}
		out |= modifierOrder[i].flag
	}
	if out == 0 {
		return 0, fmt.Errorf("a chord needs at least one modifier (ctrl, alt, shift or cmd joined by +), or the keys would fire while typing")
	}
	return out, nil
}

// String writes the chord as ParseModifiers reads it: ctrl, alt, shift, cmd, in that order.
func (m Modifiers) String() string {
	var names []string
	for _, o := range modifierOrder {
		if m&o.flag != 0 {
			names = append(names, o.name)
		}
	}
	return strings.Join(names, "+")
}

// ShortcutActions is every shortcut there is, in the order they are listed: moving the selection, closing everything, then one per
// button type. Each is bound to a single key of the shared chord.
var ShortcutActions = []string{"next", "previous", "clear", "dismiss_all", ButtonPreview, ButtonGoto, ButtonDismiss, ButtonFlag, ButtonTrain}

var defaultShortcutKeys = map[string]string{
	"next": "]", "previous": "[", "clear": "escape", "dismiss_all": "x",
	ButtonPreview: "p", ButtonGoto: "g", ButtonDismiss: "d", ButtonFlag: "f", ButtonTrain: "n",
}

// NamedKeys are the keys that are not a single character.
var NamedKeys = []string{"escape", "space", "return", "tab", "delete", "left", "right", "up", "down"}

// Shortcuts are the global keyboard shortcuts for banners (plan.md §7.2). They are one setting for the whole alert section: a hotkey is
// system-wide, so it cannot differ by kind of alert or by app.
type Shortcuts struct {
	Enabled bool
	// Modifier is the chord pressed with every key.
	Modifier Modifiers
	// ShowInLabel adds a button's key to its text, kept in step with the key.
	ShowInLabel bool
	// Keys maps each of ShortcutActions to its key: one lower-case character or a name from namedKeys; empty means that shortcut is off.
	Keys map[string]string
}

type shortcutsFile struct {
	Enabled     *bool             `json:"enabled,omitempty"`
	Modifier    *string           `json:"modifier,omitempty"`
	ShowInLabel *bool             `json:"show_in_label,omitempty"`
	Keys        map[string]string `json:"keys,omitempty"`
}

func defaultShortcuts() Shortcuts {
	keys := make(map[string]string, len(defaultShortcutKeys))
	for action, key := range defaultShortcutKeys {
		keys[action] = key
	}
	return Shortcuts{Enabled: true, Modifier: Ctrl | Alt, ShowInLabel: true, Keys: keys}
}

func (f *shortcutsFile) apply(base Shortcuts) (Shortcuts, error) {
	if f == nil {
		return base, nil
	}
	const prefix = "alert.shortcuts"
	if f.Enabled != nil {
		base.Enabled = *f.Enabled
	}
	if f.ShowInLabel != nil {
		base.ShowInLabel = *f.ShowInLabel
	}
	if f.Modifier != nil {
		m, err := ParseModifiers(*f.Modifier)
		if err != nil {
			return Shortcuts{}, fmt.Errorf("%q: %w", prefix+".modifier", err)
		}
		base.Modifier = m
	}
	for _, action := range ShortcutActions { // in a fixed order, so the same file always gives the same error
		key, ok := f.Keys[action]
		if !ok {
			continue
		}
		key = strings.ToLower(key)
		if key != "" && !ValidShortcutKey(key) {
			return Shortcuts{}, fmt.Errorf("%q: %q is not a key (use one character such as d or ], or one of %s)", prefix+".keys."+action, key, strings.Join(NamedKeys, ", "))
		}
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			return Shortcuts{}, fmt.Errorf("%q: the keys 1 to 9 press an alert's own buttons (Start next, Skip), so they cannot be used here", prefix+".keys."+action)
		}
		base.Keys[action] = key
	}
	for action := range f.Keys {
		if !slices.Contains(ShortcutActions, action) {
			return Shortcuts{}, fmt.Errorf("%q: not a shortcut (use %s)", prefix+".keys."+action, strings.Join(ShortcutActions, ", "))
		}
	}
	taken := map[string]string{}
	for _, action := range ShortcutActions {
		key := base.Keys[action]
		if key == "" {
			continue
		}
		if other, ok := taken[key]; ok {
			// blame the one the file set: the other may be a default the user never wrote
			_, otherWritten := f.Keys[other]
			_, actionWritten := f.Keys[action]
			if otherWritten && !actionWritten {
				action, other = other, action
			}
			return Shortcuts{}, fmt.Errorf("%q: %q is already the key of %q", prefix+".keys."+action, key, other)
		}
		taken[key] = action
	}
	return base, nil
}

// ValidShortcutKey says whether key is one the shortcuts can be bound to: a letter, a digit or a punctuation mark of the main block of
// the keyboard, or a named key.
func ValidShortcutKey(key string) bool {
	if len(key) == 1 {
		c := key[0]
		return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("[];',./\\-=`", rune(c))
	}
	return slices.Contains(NamedKeys, key)
}

// Warnings are problems that do not stop the shortcuts from loading but may keep them from working.
func (s Shortcuts) Warnings() []string {
	if !s.Enabled {
		return nil
	}
	if s.Modifier == Alt || s.Modifier == Alt|Shift {
		return []string{fmt.Sprintf(`"alert.shortcuts.modifier": %s alone may not work: macOS 15.0 stopped delivering hotkeys whose only modifier is alt or alt+shift (macOS 26 does deliver them). Add ctrl or cmd if the keys do nothing`, s.Modifier)}
	}
	return nil
}

func (s Shortcuts) file() *shortcutsFile {
	enabled, show, modifier := s.Enabled, s.ShowInLabel, s.Modifier.String()
	keys := make(map[string]string, len(s.Keys))
	for action, key := range s.Keys {
		keys[action] = key
	}
	return &shortcutsFile{Enabled: &enabled, Modifier: &modifier, ShowInLabel: &show, Keys: keys}
}
