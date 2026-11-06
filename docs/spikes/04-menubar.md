# Spike 04: menu bar countdown

Question: can shh show live text like `shh 28m · 65%` next to a menu bar icon, in a separate process, with a small clickable menu and no floating window? (`plan.md` §7.1)

Environment: macOS 26.7.1, Go 1.27, Command Line Tools (clang).

## Candidates

### A. `fyne.io/systray` v1.12.2 (`spikes/menubar/main.go`, its own Go module)

- Title ticks every second, menu items (greyed status line, Pause/Resume, +5m, -5m, Stop, Quit) all work, clicks reach Go channels (click log confirmed). Works as a separate process; quits cleanly.
- **Requires CGo on macOS.** `CGO_ENABLED=0 go build` fails with undefined symbols (`addSeparator`, `hideMenuItem`, ...). Builds with `CGO_ENABLED=1` using clang from the Command Line Tools.
- The title width shifts as the digits change (proportional font, not configurable through the library) and when Pause/Resume swaps the title.

### B. JXA/AppKit status item (`spikes/menubar/statusitem.js`, run by `osascript -l JavaScript`)

- Same behavior: moon icon (SF Symbol, template image) plus title, live ticking, menu with Pause/Resume, +5m, -5m, Stop and Quit, all clickable. Timer expiry falls back to the baseline level as designed.
- **No CGo and no Go tray library**, same approach as the banner overlay (spike 03).
- **Width stays steady** with `NSFont.monospacedDigitSystemFont` plus figure-space padding (` `) for single digits. Verified through a 20 to 0 second countdown by the user.
- Shifts slightly only when the whole title changes (Pause swaps `2m · 65%` for `⏸ 65%`). Not a problem for a state change.

## JXA gotchas

- Menu item selectors must be passed as plain strings (`'pause:'`), not `$('pause:')`, or the items come up greyed out. Also set `menu.autoenablesItems = false` and `item.enabled = true`.
- Actions and the 1 s timer need an `ObjC.registerSubclass` target object (same as the overlay).
- `NSStatusBar.systemStatusBar.statusItemWithLength` works from an accessory-policy `osascript` process; no extra permission was needed.

## Decision

Use the **JXA status item** on macOS: no CGo, so the main binary stays pure Go and cross-compiles, and the look is better (stable width). The helper is a persistent `osascript -l JavaScript` process that the worker starts and stops with a session. It receives JSON lines on stdin (title text, menu entries) and writes click events as JSON lines on stdout, like the banner helper. `fyne.io/systray` stays as the candidate for the Linux port (task 41), where its tray is D-Bus based and does not need CGo.

Not tested: dark and light menu bar appearance of the SF Symbol, many menu items, behavior on a notched display with a crowded menu bar (items can be hidden by macOS when there is no room).
