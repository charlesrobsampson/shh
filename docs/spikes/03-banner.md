# Spike 03: alert banner approach

Question: how does shh show its own banner with custom size, color, position, sound, duration and click-to-open, without a second toolchain if possible? (`plan.md` §7)

Environment: macOS 26.7.1, Command Line Tools (Swift 6.3 available but not needed).

## Approaches

| Approach | Size | Color | Position | Sound | Duration | Click opens app | Notes |
|---|---|---|---|---|---|---|---|
| `osascript` `display notification` | no | no | no | named system sound | no | no | Notification Center banner, attributed to Script Editor. The suppressor hides it unless the title starts with `[shh]`. |
| **JXA + AppKit overlay window** (`spikes/banner/overlay.js`) | yes | yes | yes (6 positions) | any system sound | yes (0 = until clicked) | yes | Own window, so never touched by the suppressor. No compile step. |
| Swift helper | yes | yes | yes | yes | yes | yes | Same capabilities, but needs a build step and a second toolchain. Not needed. |

## What was verified (user watched the screen)

- Orange, blue, red, green and purple banners, sizes small/medium/large, positions top-right, top-center, bottom-left and bottom-right all render cleanly with white text.
- System sounds play (`Glass`, `Hero`).
- Auto-dismiss after `duration` works (3 s measured as 3.15 s including process start, about 0.1 s of it startup).
- A real mouse click opens the source app (`open -b <bundle id>`): Finder and Calculator opened.
- **The overlay stays visible while the banner suppressor runs** (spike 02): a Notification Center banner posted at the same time was hidden and the overlay was not.

- The overlay shows over a full-screen Ghostty window and over a full-screen Teams meeting (user confirmed).

## Gotchas found

- JXA exposes zero-argument methods as properties: `sound.play` plays, `sound.play()` throws; `win.orderFrontRegardless` and `win.close` do not exist in the bridge, use `win.orderFront($())` and `win.orderOut($())`.
- Setting `layer.backgroundColor = color.CGColor` crashes the process (SIGKILL); an `NSBox` with a fill color and corner radius works.
- A hand-written `nextEventMatching...` loop never received mouse events (even synthetic ones). A subclass of `NSBox` overriding `mouseDown:`, an `NSTimer` target subclass for expiry, and `NSApplication.run` work. `NSApplication.stop` needs a posted application-defined event to wake the loop.
- Each banner is one `osascript` process (about 0.1 s start). For many banners per second use one persistent helper process reading JSON lines.

## Not tested

- Visibility on other Spaces (window level and collection behavior are set for it).
- Several overlays at once (stacking offsets).
- Multiple displays.

## Decision

Use the JXA/AppKit overlay as the shh banner. No Swift helper. The helper is a persistent `osascript -l JavaScript` process that reads JSON lines on stdin (one banner per line), so Go never compiles anything. Fallback when it cannot run: `osascript display notification` with a `[shh]` title prefix so the suppressor leaves it visible. The prototype is `spikes/banner/overlay.js`, run with `task spike:banner`.
