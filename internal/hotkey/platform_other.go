//go:build !darwin

package hotkey

// NewPlatform has nothing to offer here: global shortcuts are macOS only for now (plan.md §11.1).
func NewPlatform() (Platform, error) { return nil, ErrUnsupported }
