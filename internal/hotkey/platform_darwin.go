package hotkey

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/ebitengine/purego"
)

// Carbon's hotkey calls, reached through purego so the binary needs no CGo (docs/spikes/06-hotkeys.md). A hotkey is held by the
// application event target and its presses come through RunApplicationEventLoop, which has to run on the thread the process started on.
const (
	carbonPath       = "/System/Library/Frameworks/Carbon.framework/Carbon"
	rtldLazyGlobal   = 0x1 | 0x8
	classKeyboard    = 'k'<<24 | 'e'<<16 | 'y'<<8 | 'b'
	kindHotKeyPress  = 5
	paramDirectObj   = '-'<<24 | '-'<<16 | '-'<<8 | '-'
	typeEventHotKey  = 'h'<<24 | 'k'<<16 | 'i'<<8 | 'd'
	signature        = 's'<<24 | 'h'<<16 | 'h'<<8 | '!'
	eventHotKeyExist = -9878
)

type eventTypeSpec struct{ class, kind uint32 }

type darwinPlatform struct {
	registerEventHotKey func(code, mods uint32, id uint64, target uintptr, options uint32, out *uintptr) int32
	applicationTarget   func() uintptr
	installEventHandler func(target, handler uintptr, n uint32, list *eventTypeSpec, userData uintptr, out *uintptr) int32
	getEventParameter   func(event uintptr, name, typ uint32, actualType uintptr, size uint64, actualSize uintptr, data unsafe.Pointer) int32
	runEventLoop        func()
}

// NewPlatform loads the Carbon calls.
func NewPlatform() (p Platform, err error) {
	defer func() { // purego panics for a symbol it cannot find
		if r := recover(); r != nil {
			p, err = nil, fmt.Errorf("loading the keyboard shortcut calls: %v", r)
		}
	}()
	lib, err := purego.Dlopen(carbonPath, rtldLazyGlobal)
	if err != nil {
		return nil, fmt.Errorf("loading Carbon: %w", err)
	}
	d := &darwinPlatform{}
	purego.RegisterLibFunc(&d.registerEventHotKey, lib, "RegisterEventHotKey")
	purego.RegisterLibFunc(&d.applicationTarget, lib, "GetApplicationEventTarget")
	purego.RegisterLibFunc(&d.installEventHandler, lib, "InstallEventHandler")
	purego.RegisterLibFunc(&d.getEventParameter, lib, "GetEventParameter")
	purego.RegisterLibFunc(&d.runEventLoop, lib, "RunApplicationEventLoop")
	return d, nil
}

func (d *darwinPlatform) Start(onPress func(id int)) error {
	handler := purego.NewCallback(func(next, event, userData uintptr) uintptr {
		var hotKeyID uint64 // an EventHotKeyID: the signature in the low half and our id in the high half
		if d.getEventParameter(event, paramDirectObj, typeEventHotKey, 0, 8, 0, unsafe.Pointer(&hotKeyID)) == 0 {
			onPress(int(hotKeyID >> 32))
		}
		return 0
	})
	spec := eventTypeSpec{classKeyboard, kindHotKeyPress}
	var ref uintptr
	if status := d.installEventHandler(d.applicationTarget(), handler, 1, &spec, 0, &ref); status != 0 {
		return fmt.Errorf("InstallEventHandler failed (%d)", status)
	}
	return nil
}

func (d *darwinPlatform) Register(mods config.Modifiers, key string, id int) error {
	code, ok := KeyCode(key)
	if !ok {
		return fmt.Errorf("%q is not a key", key)
	}
	var carbonMods uint32
	for _, m := range []struct {
		flag config.Modifiers
		bit  uint32
	}{{config.Cmd, 0x0100}, {config.Shift, 0x0200}, {config.Alt, 0x0800}, {config.Ctrl, 0x1000}} {
		if mods&m.flag != 0 {
			carbonMods |= m.bit
		}
	}
	var ref uintptr
	switch status := d.registerEventHotKey(code, carbonMods, uint64(signature)|uint64(id)<<32, d.applicationTarget(), 0, &ref); status {
	case 0:
		return nil
	case eventHotKeyExist:
		return fmt.Errorf("%s+%s is already held by another app", mods, key)
	default:
		return fmt.Errorf("RegisterEventHotKey failed (%d)", status)
	}
}

func (d *darwinPlatform) Run() error {
	d.runEventLoop()
	return nil
}

// Stop ends the process: QuitApplicationEventLoop called from another thread did not take the loop out of its wait (measured: the helper
// then ran until it was killed), and the hotkeys go with the process anyway.
func (d *darwinPlatform) Stop() { os.Exit(0) }
