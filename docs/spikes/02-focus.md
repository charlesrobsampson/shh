# Spike 02: "shh" Focus mode via Shortcuts

Question: can `shh` turn a custom Focus on and off from the CLI, and do notifications still land in the notification DB while it is on? (`plan.md` §2.2, §3.1)

Environment: macOS 26.7.1 (Darwin 25.6).

## Setup (user)

1. **Create the Focus.** System Settings → Focus → Add Focus → Custom. Name it `shh`.
   - Allowed People: none. Allowed Apps: none.
   - Time Sensitive Notifications: off (Options → Time Sensitive).
   - Share Focus Status: off.
   - Do not link it to a schedule or smart activation.
2. **Create two Shortcuts** in the Shortcuts app:
   - `shh-focus-on`: action "Set Focus" → Turn `shh` On → Until Turned Off.
   - `shh-focus-off`: action "Set Focus" → Turn `shh` Off.
3. Confirm they exist: `shortcuts list` shows both.

## Measurements

| Step | Command | Result |
|---|---|---|
| Latency, on | `task spike:focus:on` | TBD |
| Latency, off | `task spike:focus:off` | TBD |
| Test notification suppressed on screen (Focus on) | `task spike:focus:notify` | TBD |
| Row recorded in DB while Focus on | same command, check the newest row | TBD |
| Teams notification suppressed, still recorded | real message from a colleague or yourself | TBD |
| Outlook notification suppressed, still recorded | real mail | TBD |
| Outlook in-app reminder window suppressed | calendar event with a 1 min reminder | TBD |
| Time Sensitive behavior | Time Sensitive notification while Focus on | TBD |

## Findings and decision

TBD.
