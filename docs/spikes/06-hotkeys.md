# Spike 06 (task 53): global hotkeys without CGo

Question: can Go register Carbon global hotkeys through `purego` with `CGO_ENABLED=0` and hear them, so banners can be driven from the keyboard (`plan.md` §7.2)?

Environment: macOS 26.7.1, Go 1.27.3, arm64, `github.com/ebitengine/purego` v0.11.3. Code: `spikes/hotkeys/` (its own module), run with `task spike:hotkeys -- <flags>`. Key presses in the automated runs were sent with System Events (`key code ... using {control down, option down}`), which the terminal's Accessibility permission allows.

## What was verified

- **It builds and runs with `CGO_ENABLED=0`.** `RegisterEventHotKey`, `UnregisterEventHotKey`, `InstallEventHandler`, `GetApplicationEventTarget`, `GetEventParameter` and `RunApplicationEventLoop` are called from Carbon with `purego.Dlopen` + `RegisterLibFunc`; the handler is a `purego.NewCallback`.
- **Presses arrive.** ctrl+alt+`]`, `[`, `P` and `D` were each heard once, about 0.5 s apart as sent.
- **A press drives a real banner.** The shipping `internal/alert/overlay.js`, patched at run time with one line (`{"press":"dismiss"}` on stdin calls the existing `press()`), was dismissed by ctrl+alt+D (outcome `dismissed`), and `{"press":"preview"}` toggled it. So the plan (Go gets the key, sends a message, the banner runs the same code a click runs) works as designed.
- **Registering after the loop started, from another thread, works** (`-late`): the install and the registrations ran from a goroutine half a second after `RunApplicationEventLoop` began, which is what the worker would do when its first banner appears. Unregistering and registering again works too (200 cycles, no failure).
- **No Dock icon.** `lsappinfo` lists the process as `type="BackgroundOnly"`, with no bundle.
- **The event loop must be `RunApplicationEventLoop`.** `CFRunLoopRun` alone delivered nothing (`-loop cf`: 0 of 4). The loop has to run on the thread the process started on, so `runtime.LockOSThread()` in an `init()` and the loop blocking `main`.
- **Failure is a status code.** A second registration of the same chord in one process returns `-9878` (`eventHotKeyExistsErr`). `cmd+Space` registers without error even though Spotlight uses it (Spotlight does not go through this API), so a failed registration only means another app that uses the same API holds the chord, and **`shh doctor` cannot rely on registration failing to find every clash**.
- **Modifiers.** On macOS 26.7 every set registered and was delivered with synthetic events: alt, alt+shift, ctrl, ctrl+alt, ctrl+shift, cmd+shift. macOS 15.0 was reported to stop delivering hotkeys whose only modifier is ⌥ or ⌥⇧; that does not show on 26.7 (synthetic events; confirmed by hand below), so the validator should **warn** about those chords, not refuse them.

## Gotchas

- `EventHotKeyID` (two 32-bit fields) is passed by value; it is sent as one 64-bit integer (signature in the low half, id in the high half), which is how both arm64 and x86-64 pass it.
- `ByteCount` is 64-bit (`unsigned long`).
- The handler runs on the loop's thread: it only passes the id on a channel.
- purego needs Go 1.25 or newer; the main module is on 1.27.3, so nothing changes there.

## Decision

Use Carbon hotkeys through purego, no CGo, as `plan.md` §7.2 assumed.

- The loop has to own the main thread of the process that hosts it. In the worker that means `main` locks the thread and blocks in `RunApplicationEventLoop` while the worker runs in goroutines, which is a change to how `cmd/shh` starts. The alternative is a small helper process like `shh menubar` that owns the loop and talks to the worker over a pipe. Task 55 picks one: **proposal: a helper process** (`shh hotkeys`, started and stopped with the first and last banner, JSON lines out), because it leaves the worker's startup alone, matches how the menu bar already works, and a crash of the key code cannot take the worker down. The cost is a process that starts about as fast as the others, so the first banner can be shown before the keys are live (a fraction of a second).
- No fallback is needed (no NSEvent monitor, no CGo).
- Update to `plan.md`: the chord check is a warning for ⌥ and ⌥⇧ (not an error), and `doctor` cannot detect every taken chord.

## Confirmed by hand

The user pressed the keys on a real keyboard over other apps and a full-screen app, and ran the banner demo: everything arrived and worked.

## Not tested

- Several displays and Spaces.
