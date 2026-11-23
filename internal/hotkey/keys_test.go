package hotkey_test

import (
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/hotkey"
)

func TestEveryKeyTheConfigAllowsHasAKeyCodeAndNoOther(t *testing.T) {
	candidates := config.NamedKeys
	for c := 0; c < 128; c++ {
		candidates = append(candidates, string(rune(c)))
	}
	seen := map[uint32]string{}
	for _, key := range candidates {
		code, ok := hotkey.KeyCode(key)
		if allowed := config.ValidShortcutKey(key); allowed != ok {
			t.Errorf("key %q: the config allows it = %v, there is a key code for it = %v", key, allowed, ok)
		}
		if ok {
			if other, dup := seen[code]; dup {
				t.Errorf("keys %q and %q share the key code %d", other, key, code)
			}
			seen[code] = key
		}
	}
}
