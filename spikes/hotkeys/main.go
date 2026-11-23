// Spike 53: can Go register Carbon global hotkeys through purego with CGO_ENABLED=0 and hear them?
//
//	go run . -seconds 20                  register ctrl+alt+] [ D P, print every press (press them yourself)
//	go run . -inject                      same, but a script presses the keys (needs Accessibility for the terminal)
//	go run . -inject -banner              also show a real banner and let ctrl+alt+D dismiss it through its stdin
//	go run . -loop cf                     use CFRunLoopRun instead of RunApplicationEventLoop
//	go run . -probe                       registration results only: taken chord, twice, unregister and again, option-only
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Carbon is a main-thread API: the event loop has to run on the thread the process started on.
func init() { runtime.LockOSThread() }

const (
	cmdKey     = 0x0100
	shiftKey   = 0x0200
	optionKey  = 0x0800
	controlKey = 0x1000

	classKeyboard    = 'k'<<24 | 'e'<<16 | 'y'<<8 | 'b'
	kindHotKeyPress  = 5
	paramDirectObj   = '-'<<24 | '-'<<16 | '-'<<8 | '-'
	typeEventHotKey  = 'h'<<24 | 'k'<<16 | 'i'<<8 | 'd'
	signature        = 's'<<24 | 'h'<<16 | 'h'<<8 | '!'
	existsErr        = -9878
	invalidErr       = -9879
	rtldLazyGlobal   = 0x1 | 0x8
	carbonPath       = "/System/Library/Frameworks/Carbon.framework/Carbon"
	coreFoundationFw = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
)

// US ANSI virtual key codes.
var keyCodes = map[string]uint32{"]": 30, "[": 33, "d": 2, "p": 35, "x": 7, "g": 5, "space": 49}

type eventTypeSpec struct{ class, kind uint32 }

var (
	registerEventHotKey   func(code, mods uint32, id uint64, target uintptr, options uint32, out *uintptr) int32
	unregisterEventHotKey func(ref uintptr) int32
	getApplicationTarget  func() uintptr
	installEventHandler   func(target, handler uintptr, n uint32, list *eventTypeSpec, userData uintptr, out *uintptr) int32
	getEventParameter     func(event uintptr, name, typ uint32, actualType uintptr, size uint64, actualSize uintptr, data unsafe.Pointer) int32
	runApplicationLoop    func()
	cfRunLoopRun          func()
)

type press struct {
	id uint32
	at time.Time
}

var presses = make(chan press, 64)

func load() error {
	carbon, err := purego.Dlopen(carbonPath, rtldLazyGlobal)
	if err != nil {
		return err
	}
	cf, err := purego.Dlopen(coreFoundationFw, rtldLazyGlobal)
	if err != nil {
		return err
	}
	purego.RegisterLibFunc(&registerEventHotKey, carbon, "RegisterEventHotKey")
	purego.RegisterLibFunc(&unregisterEventHotKey, carbon, "UnregisterEventHotKey")
	purego.RegisterLibFunc(&getApplicationTarget, carbon, "GetApplicationEventTarget")
	purego.RegisterLibFunc(&installEventHandler, carbon, "InstallEventHandler")
	purego.RegisterLibFunc(&getEventParameter, carbon, "GetEventParameter")
	purego.RegisterLibFunc(&runApplicationLoop, carbon, "RunApplicationEventLoop")
	purego.RegisterLibFunc(&cfRunLoopRun, cf, "CFRunLoopRun")
	return nil
}

// install hears every hotkey press of the process. The callback runs on the loop's thread, so it only passes the id on.
func install() error {
	var hotKeyID uint64
	callback := purego.NewCallback(func(next, event, userData uintptr) uintptr {
		if getEventParameter(event, paramDirectObj, typeEventHotKey, 0, 8, 0, unsafe.Pointer(&hotKeyID)) == 0 {
			select {
			case presses <- press{id: uint32(hotKeyID >> 32), at: time.Now()}:
			default:
			}
		}
		return 0
	})
	spec := eventTypeSpec{classKeyboard, kindHotKeyPress}
	var ref uintptr
	if status := installEventHandler(getApplicationTarget(), callback, 1, &spec, 0, &ref); status != 0 {
		return fmt.Errorf("InstallEventHandler: %d", status)
	}
	return nil
}

// register binds one chord. The id travels as one 64-bit value: the two 32-bit fields of EventHotKeyID side by side.
func register(id uint32, mods uint32, key string) (uintptr, int32) {
	var ref uintptr
	status := registerEventHotKey(keyCodes[key], mods, uint64(signature)|uint64(id)<<32, getApplicationTarget(), 0, &ref)
	return ref, status
}

func probe() {
	const chord = controlKey | optionKey
	ref, s := register(1, chord, "d")
	fmt.Printf("ctrl+alt+D                         -> %d\n", s)
	_, s = register(2, chord, "d")
	fmt.Printf("ctrl+alt+D again (same process)    -> %d (exists = %d)\n", s, existsErr)
	fmt.Printf("unregister                         -> %d\n", unregisterEventHotKey(ref))
	ref, s = register(3, chord, "d")
	fmt.Printf("ctrl+alt+D after unregister        -> %d\n", s)
	unregisterEventHotKey(ref)
	_, s = register(4, cmdKey, "space")
	fmt.Printf("cmd+Space (Spotlight)              -> %d (exists = %d)\n", s, existsErr)
	_, s = register(5, optionKey, "x")
	fmt.Printf("alt+X alone (macOS 15+ ignores)    -> %d (registers, but is it ever delivered? press it)\n", s)
	_, s = register(6, optionKey|shiftKey, "x")
	fmt.Printf("alt+shift+X (macOS 15+ ignores)    -> %d\n", s)
	_, s = register(7, 0, "x")
	fmt.Printf("X with no modifier                 -> %d\n", s)
	for i := 0; i < 200; i++ {
		ref, s = register(100, chord, "p")
		if s != 0 {
			fmt.Printf("register/unregister cycle %d failed: %d\n", i, s)
			return
		}
		unregisterEventHotKey(ref)
	}
	fmt.Println("200 register/unregister cycles     -> ok")
}

var names = map[uint32]string{1: "ctrl+alt+]", 2: "ctrl+alt+[", 3: "ctrl+alt+D", 4: "ctrl+alt+P"}

// patchedOverlay is the shipping banner script plus one thing: a {"press": "<type>"} line on stdin runs the same press() a click does.
func patchedOverlay() (string, error) {
	raw, err := os.ReadFile("../../internal/alert/overlay.js")
	if err != nil {
		return "", err
	}
	const old = "try { const m = JSON.parse(line); layoutTo(m.indent || 0, m.offset, m.height); }"
	if !strings.Contains(string(raw), old) {
		return "", fmt.Errorf("overlay.js changed: the stdin handler line was not found")
	}
	patch := "try { const m = JSON.parse(line); if (m.press) press(cfg.buttons.find(b => b.type === m.press)); else layoutTo(m.indent || 0, m.offset, m.height); }"
	return strings.Replace(string(raw), old, patch, 1), nil
}

func showBanner() (io.WriteCloser, <-chan string, error) {
	script, err := patchedOverlay()
	if err != nil {
		return nil, nil, err
	}
	spec, _ := json.Marshal(map[string]any{
		"header": "spike 53", "title": "Hotkey spike", "body": "Press ctrl+alt+D to dismiss me, ctrl+alt+P for Preview.",
		"bundle": "", "color": "#ff8700", "textColor": "#ffffff", "width": 380, "height": 120, "position": "top-right",
		"indent": 0, "offset": 0, "duration": 0, "sound": "none", "dismissOnClick": false, "previewHeight": 200, "buttonRows": 1,
		"managed": true, "parent": os.Getpid(),
		"buttons": []map[string]string{{"type": "preview", "label": "Preview", "collapseLabel": "Collapse"}, {"type": "dismiss", "label": "Dismiss"}},
	})
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", script)
	cmd.Env = append(os.Environ(), "SHH_BANNER="+string(spec))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	reader, writer, _ := os.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	writer.Close()
	done := make(chan string, 1)
	go func() {
		var last string
		sc := bufio.NewScanner(reader)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "event ") {
				last = line
			}
		}
		cmd.Wait()
		done <- last
	}()
	return stdin, done, nil
}

func inject(keys ...string) {
	for _, k := range keys {
		script := fmt.Sprintf(`tell application "System Events" to key code %d using {control down, option down}`, keyCodes[k])
		out, err := exec.Command("osascript", "-e", script).CombinedOutput()
		if err != nil {
			fmt.Printf("inject %s failed: %v %s\n", k, err, strings.TrimSpace(string(out)))
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func main() {
	seconds := flag.Int("seconds", 20, "how long to listen")
	doInject := flag.Bool("inject", false, "press the keys with System Events instead of waiting for the user")
	doBanner := flag.Bool("banner", false, "show a banner that ctrl+alt+D dismisses and ctrl+alt+P previews")
	loop := flag.String("loop", "app", "event loop: app (RunApplicationEventLoop) or cf (CFRunLoopRun)")
	late := flag.Bool("late", false, "start the loop first, then install and register from another thread (what the worker would do on its first banner)")
	mods := flag.Bool("modifiers", false, "register X with several modifier sets and press each, to see which macOS delivers")
	doProbe := flag.Bool("probe", false, "only report registration results")
	flag.Parse()

	if err := load(); err != nil {
		fmt.Println("load:", err)
		os.Exit(1)
	}
	fmt.Printf("runtime %s, %s/%s, loop=%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, *loop)
	if !*late {
		if err := install(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	if *doProbe {
		probe()
		return
	}
	if *mods {
		modifierTest(*loop)
		return
	}
	registerAll := func() {
		for id, key := range map[uint32]string{1: "]", 2: "[", 3: "d", 4: "p"} {
			if _, s := register(id, controlKey|optionKey, key); s != 0 {
				fmt.Printf("register %s: %d\n", names[id], s)
			}
		}
	}
	if !*late {
		registerAll()
	}

	var banner io.WriteCloser
	var bannerDone <-chan string
	if *doBanner {
		var err error
		if banner, bannerDone, err = showBanner(); err != nil {
			fmt.Println("banner:", err)
			os.Exit(1)
		}
	}
	var mu sync.Mutex
	got := map[uint32]int{}
	go func() {
		for p := range presses {
			mu.Lock()
			got[p.id]++
			mu.Unlock()
			fmt.Printf("%s  pressed %s\n", p.at.Format("15:04:05.000"), names[p.id])
			if banner != nil && p.id == 3 {
				banner.Write([]byte(`{"press":"dismiss"}` + "\n"))
			}
			if banner != nil && p.id == 4 {
				banner.Write([]byte(`{"press":"preview"}` + "\n"))
			}
		}
	}()
	go func() {
		if *late {
			time.Sleep(500 * time.Millisecond)
			if err := install(); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			registerAll()
			fmt.Println("installed and registered from a goroutine after the loop started")
		}
		if bannerDone != nil {
			go func() { fmt.Println("banner outcome:", <-bannerDone) }()
		}
		if *doInject {
			time.Sleep(1500 * time.Millisecond)
			inject("]", "[", "p", "d")
			time.Sleep(1500 * time.Millisecond)
			mu.Lock()
			fmt.Printf("injected 4 keys, heard: %v\n", got)
			mu.Unlock()
			os.Exit(0)
		}
		fmt.Printf("listening for %d s: press ctrl+alt+] [ P D (over other apps, a full-screen app, a call)\n", *seconds)
		time.Sleep(time.Duration(*seconds) * time.Second)
		mu.Lock()
		fmt.Printf("heard: %v\n", got)
		mu.Unlock()
		os.Exit(0)
	}()
	if *loop == "cf" {
		cfRunLoopRun()
	} else {
		runApplicationLoop()
	}
}

// modifierTest registers X under each modifier set, presses each set with System Events and says which ones arrived.
func modifierTest(loop string) {
	sets := []struct {
		name   string
		mask   uint32
		system string
	}{
		{"alt", optionKey, "option down"},
		{"alt+shift", optionKey | shiftKey, "{option down, shift down}"},
		{"ctrl", controlKey, "control down"},
		{"ctrl+alt", controlKey | optionKey, "{control down, option down}"},
		{"ctrl+shift", controlKey | shiftKey, "{control down, shift down}"},
		{"cmd+shift", cmdKey | shiftKey, "{command down, shift down}"},
	}
	for i, set := range sets {
		register(uint32(i+1), set.mask, "x")
	}
	go func() {
		time.Sleep(time.Second)
		heard := map[uint32]bool{}
		for i, set := range sets {
			script := fmt.Sprintf(`tell application "System Events" to key code 7 using %s`, strings.Trim(set.system, "{}"))
			if strings.Contains(set.system, ",") {
				script = fmt.Sprintf(`tell application "System Events" to key code 7 using %s`, set.system)
			}
			exec.Command("osascript", "-e", script).Run()
			time.Sleep(400 * time.Millisecond)
			for len(presses) > 0 {
				heard[(<-presses).id] = true
			}
			fmt.Printf("%-11s registered ok, delivered: %v\n", set.name, heard[uint32(i+1)])
		}
		os.Exit(0)
	}()
	runApplicationLoop()
}
