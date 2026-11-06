# Spike 02: "shh" Focus mode via Shortcuts

Question: can `shh` turn a Focus on and off from the CLI, and do notifications still land in the notification DB while it is on? (`plan.md` §2.2, §3.1)

Environment: macOS 26.7.1 (Darwin 25.6), managed Mac (MDM, endpoint security agents installed).

## Setup

- Custom Focus `shh`: no allowed people, no allowed apps, no filters, Smart Activation off, Focus status sharing off.
- Shortcuts `shh-focus-on` ("Set Focus": `shh` On, Until Turned Off) and `shh-focus-off`. Both exist and work.
- Runner tasks: `task spike:focus:on|off|notify`. Activity checked via `~/Library/DoNotDisturb/DB/Assertions.json` (active mode ids) and the notification DB (read-only).

## Results

Latency: `shortcuts run` takes 0.2 to 0.3 s each way. The active mode shows up in `Assertions.json` (mode id `com.apple.donotdisturb.mode.flame` for `shh`, `...mode.default` for Do Not Disturb).

| Mode | Script Editor (`osascript`) | Outlook calendar reminder | Outlook new mail |
|---|---|---|---|
| Focus off | banner, recorded | banner, recorded | banner, recorded |
| Custom `shh` Focus | **banner shown**, recorded | **banner shown**, recorded | no banner, **not recorded** |
| Stock Do Not Disturb | no banner, recorded | not tested | no banner, **not recorded** (also not recorded late after DND ended) |

Teams: a test notification triggered from Teams settings was recorded while `shh` was on, but whether its banner was suppressed was not confirmed (it may be in-app only). Other Teams test notifications never reached macOS.

Config oddity: the `shh` Focus is stored with `applicationConfigurationType` 2 and `senderConfigurationType` 2, while stock DND has 0. A recreated `shh` Focus got the same values. Unknown whether this causes the failed suppression. The Mac is managed, which may also override.

## Findings

1. **Custom Focus does not reliably suppress** on this Mac (Script Editor and Outlook reminders came through). Stock DND did suppress Script Editor while still recording it.
2. **Outlook new mail leaves no trace while any Focus is on.** No banner and no DB row, under both `shh` and DND. With Focus off it is recorded. This breaks the assumption that blocked notifications can still be read from the DB, at least for Outlook mail.
3. Outlook calendar reminders (category `OLAlertsNotificationRemindersWithJoinAndChatCategoryIdentifier`) go through macOS and are recorded. Outlook mail needs Outlook's "Display new message alert on desktop" on, and only Focused-inbox mail alerts.
4. **DB behavior to handle in task 16/17:**
   - Rows are written a few seconds after posting, so a read 2 s after delivery can miss them.
   - Dismissed or expired notifications are deleted, and `rec_id` values are reused (rec 22 to 24 were replaced later). A high-water mark on `rec_id` alone can skip new rows. Track `delivered_date` as well.
   - `record.presented` is 0 even for notifications that were shown, so it cannot tell shown from suppressed.

## Spike 02b: hide banners with the Accessibility API instead of Focus

Idea: leave Focus off so every notification is recorded, and close each banner through System Events (Accessibility permission, which Ghostty already has). Scripts in `spikes/focus-ax/` (JXA, run with `osascript -l JavaScript`, no CGo).

- Notification Center shows each banner as an `AXNotificationCenterBanner` group inside the `NotificationCenter` process. Its actions are `AXPress` (opens the app, do not use), `Show Details`, `Show` and `Close`. Action names are long strings (`Name:Close\nTarget:0x0...`), so match on the `Name:Close` prefix.
- Polling finds the banner about 350 ms after posting and closing takes about 80 ms more, so a closed banner is visible for well under a second.
- **Closing early drops the DB row.** The row is only written when the temporary banner would expire, about 5.1 s after posting (measured with a 100 ms DB poll). A notification closed at 0.7 s never appears in `record`. Reading from the DB and then closing is therefore impossible.
- The banner exposes only `title`, `subtitle`, `body` (static texts with those identifiers) and a UUID. **No app name or bundle id.** Source attribution would need a heuristic.
- So this path replaces DB ingest with screen scraping for suppressed notifications, loses the source app, and depends on Accessibility permission and Notification Center's current layout.

### 02c: move the banner window off-screen instead of closing it

- The `NotificationCenter` window's `AXPosition` is settable (the banner group itself is not). Setting it to `[6000, 6000]` takes the banner off-screen without dismissing it.
- Posted a notification and moved the window 403 to 412 ms later. **The user saw nothing, and the DB row still landed at about 5.16 s** (twice). Restoring to `[0, 0]` afterwards showed no banner either.
- The window does not exist while idle (`windows().length` is 0), and macOS recreates it at `[0, 0]` for each banner. So it cannot be parked once: a watcher must catch every new window and move it.
- Not yet tested: overlapping banners, real Teams and Outlook banners, CPU cost of polling, and notification sound (sounds are off on this Mac).

## Decision

Open. Leading candidate: Focus stays off, shh runs a watcher that moves each new banner window off-screen, then reads the DB as designed. Needs the user's approval because it replaces the Focus-based design in `plan.md` §2.2 and §3.1. Needs the user. Options and the impact on `plan.md` are in the task report.
