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

## Decision

Open. Needs the user. Options and the impact on `plan.md` are in the task report.
