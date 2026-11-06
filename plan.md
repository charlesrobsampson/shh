# shh — Design Plan

Status: DRAFT v2 (decisions from review folded in). Builds on `manifest.md`. Items marked **[OPEN]** need a decision. Items marked **[ASSUMED]** are defaults I picked and you can overrule.

---

## 1. Goals / non-goals

**Goals**
- During a focus session, decide per notification: interrupt now or hold for later.
- Learn from the user's feedback (exact 0–100 labels or quick `y`/`n`, §5.3). Gets better over time.
- Run fully local, on a modest machine, with no required LLM.
- No vendor APIs (no Graph/Entra, no Slack tokens). Everything comes from the macOS notification stream.

**Non-goals (v1)**
- Replying to messages.
- Cross-device sync.
- Non-macOS.
- Hard-blocking notifications at the OS level (not possible, see §2).

---

## 2. Hard constraints discovered (change the design)

1. **We observe after delivery. We cannot intercept.** The notification DB is written once macOS has already received the notification, and only about 5 s after delivery, when the temporary banner expires. "Block" therefore means: shh hides the OS banner (§2.2), and `shh` decides whether to *re-emit* its own alert. "ALLOW" = `shh` shows its own banner through its own overlay window (never a Notification Center banner, or the suppressor would hide it).
2. **Hiding banners [DECIDED, spike 02, `docs/spikes/02-focus.md`]:** macOS Focus is **not** used. On this Mac a custom Focus did not suppress banners, and with any Focus or DND on, Outlook mail left no DB row at all, so shh could never classify it. Instead shh runs a **banner suppressor** (Accessibility API via `osascript` JXA, needs the Accessibility permission): the `NotificationCenter` process creates a window per banner; the suppressor moves it to `AXPosition [6000, 6000]` (off-screen) about 0.4 s after delivery. macOS still records the row. Banners whose title starts with `[shh]` (the Notification Center fallback in §7) are left visible. A hidden banner may flash for under half a second, and other banners sharing a window with a `[shh]` one show too. Closing the banner instead of moving it is **not** an option: it prevents the DB row from being written.
3. **DB access:** **verified on this Mac (Darwin 25.6): `~/Library/Group Containers/group.com.apple.usernoted/db2/db`** (SQLite, WAL mode), readable as the normal user with no Full Disk Access and no admin. Tables: `record` (`rec_id`, `app_id`, `data` plist blob, `delivered_date` in seconds since 2001-01-01), `app` (`app_id`, `identifier`), plus `delivered`, `displayed`, `requests`, `categories`. Plist keys: `req.{titl,subt,body,iden,cate,soun}`, `app`, `date`, `srce`, `styl`, `uuid`. Teams bundle id is `com.microsoft.teams2`. The path differs on older macOS (`.../group.com.apple.usernotificationsd/db`), so the reader must probe for it. Payloads are binary plists, and the schema shifts between releases. The reader must be version-tolerant and fail loudly (`shh doctor`).
4. **macOS here is Darwin 25.x (Tahoe).** The spike (M0) should be done on this exact version first.

---

## 3. Process model **[ASSUMED: session-only, promotable]**

- `shh focus ...` starts a **session worker** and returns immediately.
  - The worker is the same binary, re-exec'd as `shh daemon --session <id>`, detached.
  - PID file: `~/.config/shh/shh.pid`. Control socket: `~/.config/shh/shh.sock` (unix, JSON lines).
- CLI and TUI are thin clients over the socket: status, pause/resume, +/-5m, change threshold, stop, live event stream.
- `shh stop` asks the worker to end gracefully (restore Focus, write session stats), then SIGTERM fallback.
- On worker start it **backfills** from the notification DB since session start time, so nothing is missed in the spawn gap.
- **Worker lifetime [DECIDED]:** the worker lives while *any* level is above 0% (see §3.1). A persistent baseline therefore keeps it running indefinitely. It exits when the effective level returns to 0%/off, and the suppressor stops.
- **Reboot/logout:** the worker dies with the login session. Baseline is persisted in SQLite, and `shh install` (optional launchd agent running the same `shh daemon`) resumes it at login. Without install, the user re-runs `shh` (any command re-arms a persisted baseline, and `shh doctor` warns).
- **Promotion path [DECIDED: session-only first]:** the launchd agent could later also collect notifications while at 0% as extra training data and enable calendar auto-focus. Nothing in the design blocks it.

```
 shh CLI / TUI ──unix socket──▶ session worker
                                  │
   notificationcenter db ──tail──▶│ ingest → normalize → dedupe
                                  │   ├─ calendar parser ──▶ scheduler (T-1m alerts)
                                  │   └─ classifier ──▶ decide ──┬─ ALLOW ──▶ banner/sound
                                  │                              └─ BLOCK ──▶ store
                                  └──── shh.db (SQLite, WAL) ◀──────────────┘
```

---

## 3.1 Focus levels: baseline + escalations **[NEW]**

State is a **stack of levels**, not one session:

| Layer | Example | Ends | Falls back to |
|---|---|---|---|
| Baseline | `shh baseline 30%` | never (until changed) | 0% |
| Escalation (timed) | `shh focus -f 90% -t 50m` | timer ends, or `shh stop` | the layer beneath |
| Escalation (indefinite) | `shh focus -f 75% -t 0` | `shh stop` | the layer beneath |

- `effective_threshold(now)` = the **top active layer's** threshold. An escalation may also be *lower* than the baseline (e.g. a temporary 10% to let more through). **[ASSUMED]**
- Timer expiry pops the layer, and the effective level reverts to what's underneath, **not** 0%. Baseline 30% → `focus 90% 50m` → back to 30%.
- `--then <pct|tier|off>` (alias `--clear`) overrides the fallback for one escalation. `--then off` also clears the baseline when the timer ends. **[DECIDED, task 12]** `--then X` applies only when the timer runs out: everything beneath that layer is closed and, for X above 0, a new indefinite baseline at X is started (so `--then 65%` leaves you at 65% from then on). A manual `shh stop` ignores `then` and simply falls back to the layer beneath.
- `shh stop` pops the top escalation only. `shh stop --all` clears everything including the baseline.
- Pause/resume applies to the top timed layer, and freezes only its countdown: the focus level stays in force while paused. `+`/`-` adjust its duration. `f` changes the top layer's percentage.
- Multiple stacked escalations are allowed but rare. The UI shows the stack in `shh status`.
- The banner suppressor runs while effective level > 0, and stops at 0%/`off`.
- Every notification row records `session_id` (or layer id) and the threshold used, so history remains interpretable.
- Schema: `focus_sessions` gains `kind` (`baseline`|`escalation`), `parent_id`, `then_level`, `paused_at`, `remaining_secs`, and `expires_at` (set while a timed layer runs; paused layers keep `remaining_secs` instead). Only one baseline can be active.

---

## 4. Pipeline detail

### 4.1 Ingest
- Poll the DB read-only (open with `mode=ro`, copy-on-read of WAL if needed) every ~1s, or watch the file with fsnotify and re-query on change.
- **Progress is a delivery-time cursor, not a `rec_id` high-water mark [DECIDED, tasks 16/17, from spike 02]:** macOS reuses record ids and writes rows about 5 s after delivery, sometimes out of order. Each poll re-reads the table and handles rows delivered within 30 s of the cursor whose `(rec_id, delivered)` pair has not been handled; the cursor and the handled pairs persist in the store, and a handled pair is forgotten only once macOS has deleted its row.
- Decode the plist payload into `{app_bundle_id, title, subtitle, body, delivered_at, thread_id}`.
- Map `bundle_id` to a friendly `source` (Teams, Outlook, Slack, Mail, …). Keep the raw bundle id too.
- **Dedupe:** same app and title and body within 10s counts as one event. (Teams re-posts.)
- Ignore notifications that `shh` itself posted: its own bundle id, and banners titled `[shh]` (the Notification Center fallback, which macOS attributes to Script Editor).

### 4.2 Calendar pre-empt **[ASSUMED: parse Outlook/Teams notifications]**
No calendar API. Instead, recognize meeting-reminder notifications:
- Detector: per-source regexes/rules (Outlook reminder "… starts in N minutes", "Starts at 10:30 AM", Teams "meeting started"/"is starting").
- Extract `title`, `start_time` (resolve relative to `delivered_at`, local timezone).
- Schedule an alert at `start_time − 60s`. If that time already passed, alert immediately.
- Persist a row in `calendar_events` so a worker restart re-arms timers. Dedupe by `(title, start_time)`.
- The alert bypasses the classifier and fires even at the maximum threshold (`lockdown`, 99).
- **Known limits:** requires Outlook reminders enabled, and the reminder lead time must be ≥ 1 min. Meetings without a reminder are missed. Mitigation, later: ICS feed or Calendar.app account. Format parsing is brittle, so ship rules as editable config (`rules.json`) and add a `shh capture` command that logs raw notifications so rules can be tuned on real samples.
- **[FINDING, M0]** On this Mac, macOS notifications for Outlook are OFF (Outlook shows reminders in its own window), so the DB has no Outlook rows at all. The notification-parsing approach **requires macOS notifications to be enabled for Outlook**. Enabling them also lets shh classify Outlook mail, which is desirable. If that's not acceptable, fall back to an ICS feed or Calendar.app account.
- **[DECIDED] Fail-safe parsing, refine later.** Real samples are rare, so:
  1. Detect calendar reminders by **category** (Outlook uses `OLAlertsNotificationReminders…`; first sample: `…WithJoinAndChatCategoryIdentifier`), not by keywords.
  2. Parse the start time leniently, trying formats in order: `Month D, YYYY at HH:MM` (seen), `Today at …`, `in N minutes`, all-day wording. Add formats as samples arrive.
  3. **If parsing fails, alert immediately** and store the row in `unparsed_calendar`. Worst case is an early alert, never a missed one.
  4. Category list and date patterns live in `rules.json`. Every real sample becomes a fixture and a table-test row.
  5. `shh rules review` lists unparsed reminders so edge cases surface during normal use.
- First real sample (2026-11-05): category `OLAlertsNotificationRemindersWithJoinAndChatCategoryIdentifier`, title `Client Portal Sync up`, subtitle `Microsoft Teams Meeting`, body `November 5, 2026 at 13:30`, delivered 3 minutes before start. Saved in `captures/meetings.jsonl` (gitignored: private).

### 4.3 Classify and decide
Score → effective score → threshold (see §5).

```
S_base   = classifier(features)                  in [0,1]
boost    = time_sensitivity(text, meta)          in [0, 0.4]
S_eff    = max(S_base, min(0.99, S_base + boost))   (boost alone can never pierce lockdown; a base score above 0.99 still does)
T        = effective_threshold(now)              (§3.1), 0..99
interrupt = calendar_preempt OR (S_eff > T/100)
```
- **[DECIDED] Score scale = label scale.** `S` is an importance score on 0–100 (stored 0–1). A label/score of **N means "mute me whenever focus is at or above N"**: strictly, it interrupts only while `T < N`. So 0 = always muted while any focus is active, 100 = never muted.
- **Max threshold is 99** (`lockdown`). Percent input accepts 0–99 (`100%` is an alias for 99). A notification scoring above 99, i.e. labeled 100 or learned as critical, pierces even lockdown. Calendar pre-empt also always fires.
- Tiers: `off` 0 (no Focus active, nothing is muted, the worker isn't deciding), `light` 35, `medium` 65, `deep` 90, `lockdown` 99.
- Store on every row (alerted and blocked): `S_base`, `boost`, `threshold_at_decision` (the effective `T` at that moment), and a short `reason`. The `threshold_at_decision` is what makes later `y`/`n` feedback interpretable (§5.3).

### 4.4 Deliver
- ALLOW: post an own-banner via the overlay helper (§7).
- BLOCK: write row, increment session counter, emit event to any attached TUI.

---

## 5. Classifier **[main design area]**

### 5.1 Principle: layered, cheap first
| Layer | What | Required? |
|---|---|---|
| L0 | Rules/priors: VIP senders, app priority, urgency keywords, 1:1 vs channel, @mention | Yes |
| L1 | Hashed text features + metadata → online logistic regression | Yes |
| L2 | Static embedding vectors (Model2Vec-style, pure Go lookup + mean pooling), **shipped one of two ways (decided, spike 05): embedded via `go:embed` in release builds (`-tags embedmodel`), or downloaded once by `shh init` into `~/.config/shh/models/` for `go install` builds** (the model is not in git), appended to L1 features | **Yes, v1** (gives semantic generalization out of the box) |
| L3 | Zero-shot "decision model" judge (Laya/Jev-style Python sidecar) used as feature provider | Optional, **decided by measurement** (see §5.2) |

All layers sit behind an `Features` interface that returns a sparse/dense vector with a **feature version** string. Weights are stored per version. Changing the embedder invalidates old weights, so retrain from stored notification text, which is why we keep bodies for 30 days.

### 5.2 Why not make Laya the core
Facts from its model card: <https://huggingface.co/convaiinnovations/laya>
- It is a *decision model*, not an embedder. It emits per-question option probabilities, no vectors, so it can't take the manifest's SGD-on-weights loop directly.
- 421M params, ~800MB download, Python SDK first. ONNX is an optional Python extra with no published weight file. ~33ms is quoted on a **GPU (T4)**. On CPU it will be slower. Heavy for "weaker machine".
- Zero-shot accuracy on the authors' own benchmark is near chance for the base checkpoint, ships over-confident, and wants temperature calibration on your own data.
- Mismatch with the goal of "learns from my `y`/`n`".

Where it *does* fit: as **L3 feature provider**, a Python sidecar answering fixed questions per notification ("is this a direct question to me?", "urgency 1–5", "is this an outage?"). Those answers become extra input features to L1. This helps the **cold start** (day-1 behavior before any feedback). Gate it to uncertain scores only (|S − T| < 0.15) to keep cost low. Treat it as a later milestone, behind the `Judge` interface, which also allows Jev/Claude/Ollama backends.

**Decision rule for L3 (python is acceptable if it earns its keep):** don't guess, measure. After M3 we'll have a labeled set of real notifications (see §5.3 bootstrapping). Run an offline Python script that scores that set with Laya (calibrated on a held-out part) and compare against L0+L1+L2 using the replay harness (§13). Adopt L3 only if it measurably improves precision/recall of "should have alerted", especially in the first week of use. Core must remain fully functional without it.

**Out-of-box quality without any external system** comes from: (a) L0 priors/seed weights, (b) bundled static embeddings, (c) bootstrapping from history below. **[DECIDED, spike 05, `docs/spikes/05-embeddings.md`]** `minishlab/potion-base-8M` quantized to int8 (7.7 MB), BERT WordPiece tokenizer implemented in Go; verified against the reference library. **Distribution [DECIDED]:** the quantized model is not committed. It is published once as a GitHub release asset under a stable tag (`models-v1`) whose URL and sha256 are constants in the source. Release builds embed it (`-tags embedmodel`); plain `go install ...@version` builds do not, and `shh init` downloads and verifies it. Until the model is present L2 is off (L0+L1 only) and `shh doctor` says so. A new model means a new `models-vN` tag and constants; the feature version includes the model id.

### 5.3 Model and learning
- `p = σ(w·x + b)` is the score `S_base`. Training is soft-target logistic SGD: `w ← w + α (t − p) x` (the manifest formula plus the sigmoid and a bias), where `t` is the target score in [0,1].
- **[DECIDED] Hybrid labels.** Both feed the same update:
  - **Exact label** (`l` then 0–100, or a tier name): `t = N/100`. Always updates.
  - **Quick `y`**: "this should have reached me at the threshold it was at." Constraint `S > T_dec/100`. If violated, `t = min(1, T_dec/100 + margin)` (capped at 1 rather than 0.99 so that `y` at lockdown, which needs `S > 0.99`, is satisfiable); otherwise no update.
  - **Quick `n`**: "this should have been muted at that threshold." Constraint `S ≤ T_dec/100`. If violated (e.g. an item that was alerted), `t = max(0, T_dec/100 − margin)`; otherwise no update.
  - `T_dec` is the row's `threshold_at_decision`, so feedback given hours later is still interpreted correctly. `margin` defaults to 0.1 **[ASSUMED]**.
- **Training order:** the Train list shows the most informative items first (scores near the user's usual thresholds, or where recent labels disagree with the model).
- **Later, not v1:** implicit weak labels (opened right away, dismissed unread).
- Defaults **[ASSUMED]**: α = 0.1 for dense, per-feature AdaGrad-style decay for sparse; L2 = 1e-4; clip |w| ≤ 5.
- Train on `S_base` only, never on the boosted score.
- **Bootstrapping from history [NEW]:** the macOS notification DB already holds recent past notifications. On first run (`shh init`), import them as unlabeled rows so the Train tab has data to label on day one, instead of waiting for sessions to produce it. A quick-label pass of ~50 items sets sane per-sender/app weights immediately.
- **Cold start:** initialize `w` from L0 priors (e.g. known-urgent keywords get positive weights, newsletters/"unsubscribe" negative). Ship a small seed set so day-1 `medium` isn't random.
- **Feedback rebalancing:** quick `n` rarely violates a constraint, so most updates come from `y` and exact labels. If the model still drifts toward "block everything", weight `y` updates ×2–3.
- **Safety rail:** if the recent rate of `y` feedback in a session exceeds a limit, surface a hint ("lower threshold?"). Don't silently adapt.
- Weights persisted in SQLite (`model_weights`, one snapshot per change, the newest 20 kept per feature version, a new feature version starting fresh). Every training call also writes a `training_log` row holding the values it overwrote, so undo is exact; checkpoint rows in that log mark where `--rollback` returns to, and `--reset` takes a checkpoint first so a reset can be rolled back too (`shh train --reset`, `--rollback`).

### 5.4 Time-sensitivity boost
Pure function, easily tested. Keyword list (`now`, `ASAP`, `urgent`, `prod`, `down`, `outage`, …) → +0.1…+0.3; recency/quick-follow-ups from the same sender (3 messages in 2 minutes) → +0.1; capped at +0.4. Keywords come from `rules.json`.

---

## 6. Summaries / previews **[ASSUMED]**
- `Summarizer` interface with backends: `none` (default), `ollama`, `claude` (API key from env/Keychain), pluggable later.
- With `none`, the detail pane shows the notification text truncated to the pane size, with `e` to expand in place to the full text. **Viewing and expanding never changes read/trained/dismissed state.**
- `s` triggers a summary only if a backend is configured, otherwise a hint on how to enable one. Results are cached per notification row.
- Privacy note: the `claude` backend sends notification text off-machine. It's opt-in per config and shown in the TUI status bar when active.

---

## 7. Alert banners (customizable)
- **[DECIDED, spike 03, `docs/spikes/03-banner.md`]** One `osascript -l JavaScript` process per banner (AppKit through JXA, the script embedded in the Go binary, no Swift, no compile step, about 0.1 s to start) draws the banner in a window of its own, so the banner suppressor (§2.2) never hides it. The banner is described by JSON in an environment variable, and Go keeps a slot per screen position so banners shown together stack instead of overlapping. (A persistent helper fed JSON lines was the spike's suggestion; one process per banner is simpler and needed no change in behaviour.) Size, color, position, sound, duration and click-to-open are all supported. Verified over full-screen apps (Ghostty, Teams call). Untested so far: other Spaces, stacking, multiple displays.
- Config (`~/.config/shh/config.json`, **[DECIDED, task 42]** JSON that also allows `//` and `/* */` comments and trailing commas; `rules.json` works the same way):
  ```jsonc
  {
    "alert": {
      "position": "top-right",   // top-left|top-center|top-right|bottom-*
      "size": "medium",          // small|medium|large
      "color": "#ff9f0a",
      "duration_secs": 8,        // 0 = until dismissed
      "sound": "Glass",          // system sound name | "none"
      "calendar": { "color": "#0a84ff", "sound": "Hero" },   // per-kind override
      "source": { "Slack": { "sound": "none" } }              // per-app override
    },
    "menubar": { "label": "shh" }
  }
  ```
- Click → opens the originating app (`open -b <bundle id>`). Fallback if the helper isn't installed: `osascript display notification` (no customization).
- `shh alert test` previews styles.

---

## 7.1 Menu bar status **[NEW]**
- Goal: always-visible countdown with no floating window. Example title: `shh 28m · 65%`, or `shh 30%` for an indefinite baseline, and `shh ⏸` while paused. The label is configurable (`[menubar] label = "shh"`, any text or emoji).
- Implementation **[DECIDED, spike 04, `docs/spikes/04-menubar.md`]**: a separate `shh menubar` process started by the worker (quits with it) that runs a persistent `osascript -l JavaScript` helper drawing an `NSStatusItem` (SF Symbol icon plus text title, monospaced-digit font with figure-space padding so the width does not jitter). Go sends JSON lines (title, menu entries) on stdin and reads click events on stdout; the helper is embedded in the Go binary. No CGo. `fyne.io/systray` needs CGo on macOS and its title width jitters, so it is kept only for the Linux port (task 41). Poll the worker socket once per second.
- Menu items: current stack (baseline + escalations), pause/resume, ±5m, change %, stop, open TUI, view blocked count.
- Fallbacks if the tray lib doesn't fit: (1) `fyne.io/systray` (CGo on macOS), or (2) text-only `shh status --short` for prompts, tmux, SwiftBar/xbar.
- No floating window, per requirements.

---

## 8. TUI (Bubble Tea + Lipgloss)

Keep manifest layout, but make the state model explicit:

| Flag | Meaning | Set by |
|---|---|---|
| `is_read` | Opened in detail view | Opening (`Enter`/`o`) only. Preview/expand does **not**. |
| `is_dismissed_inbox` | Gone from Tab 1 | `d`/`x`, or training with quick-train |
| `is_trained` | Gone from Train list | `y`, `n`, or `l` + number (stored in `user_feedback` / `user_label`) |

Train tab keys: `y` = "should have reached me at that level", `n` = "should have been muted at that level", `l` = exact label (0–100 or a tier name, e.g. `light`). Every row in the Train list shows the score the model gave (0–100) and whether it was alerted or blocked. The detail pane shows the score, boost, `threshold_at_decision`, and the reason.

- **Entry point [DECIDED]:** bare `shh` opens the TUI (`shh ui` remains an alias). (`ssh` is OpenSSH and can't be used.) Three tabs, switched with `Tab`/`Shift-Tab` and `F1`–`F3` (digits are reserved for training labels):
  1. **Focus:** level stack and time left; start, stop, baseline, pause/resume, adjust, change level. Everything the CLI can do.
  2. **Notifications:** `is_dismissed_inbox = 0`.
  3. **Train:** `is_trained = 0`.
- Rows hidden from one list remain visible in the other, per manifest.
- Layout: list on the left, detail/preview on the right, status bar at bottom (session timer, threshold, blocked count, summarizer on/off).
- Keys: manifest table, plus `e` expand/collapse preview, `u` undo last action (training reverts weights via the saved pre-update snapshot of the delta), `?` help, `/` filter.
- **Undo is cheap:** store `(row_id, x_hash, delta_w)` for the last N updates.
- `shh` also works without an active session (review and train only; Focus tab can start one).

---

## 9. CLI
As in manifest, with these additions:
- `shh status`: stack of levels, time remaining, effective threshold (`--json`, `--short` for prompt/menu bar).
- `shh baseline <pct|tier|off>`: set/clear the persistent underlying level.
- `shh focus ... --then <pct|tier|off>` (alias `--clear`): what to fall back to when the timer ends. Default: the layer beneath.
- `shh stop [--all]`: pop top escalation, or clear everything.
- `shh pause|resume`.
- `shh doctor`: checks the Accessibility permission, DB path/version, Outlook "Display new message alert on desktop" and reminder style, overlay helper, Ollama (if enabled).
- `shh capture`: log raw notifications for rule-building (dev/M0).
- `shh train --reset|--rollback|--export|--import`.
- `shh ls` filters: `--source`, `--since`, `--status`, `--min-score`; read-only.
- Parse positional note: `shh f -f 90% "note"` is a positional note, and `-m` is the flag form.
- `-t 0` = indefinite. Durations like `25m`, `1h30m`.

---

## 10. Storage changes vs manifest
New or changed columns (migrations via a `schema_version` table, applied on launch):

```sql
ALTER TABLE notifications ADD COLUMN bundle_id TEXT;
ALTER TABLE notifications ADD COLUMN source_rec_id INTEGER;       -- macOS DB id, dedupe/high-water
ALTER TABLE notifications ADD COLUMN threshold_at_decision INTEGER;  -- effective T (0..99) when decided
ALTER TABLE notifications ADD COLUMN user_label INTEGER;             -- exact 0..100 label, NULL if y/n or untrained
ALTER TABLE notifications ADD COLUMN reason TEXT;                 -- top features, "why"
ALTER TABLE notifications ADD COLUMN is_read BOOLEAN DEFAULT 0;
ALTER TABLE notifications ADD COLUMN session_id INTEGER REFERENCES focus_sessions(id);
ALTER TABLE notifications ADD COLUMN summary TEXT;
ALTER TABLE model_weights ADD COLUMN feature_version TEXT NOT NULL;  -- plus drop CHECK(id=1) for history
CREATE TABLE calendar_events (id INTEGER PRIMARY KEY, title TEXT, start_time DATETIME,
  alert_at DATETIME, fired BOOLEAN DEFAULT 0, source_notification_id INTEGER);
CREATE TABLE training_log (id INTEGER PRIMARY KEY, notification_id INTEGER, label TEXT,
  predicted REAL, delta_blob BLOB, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
```
- Fix TTL: manifest purges only `trained AND dismissed`, so untouched rows live forever. Proposed: purge anything older than 30d *except* untrained rows younger than 90d (they're still training candidates). Alerted-and-never-reviewed rows purge at 30d.
- Indices: `(timestamp)`, `(is_dismissed_inbox, timestamp)`, `(is_trained, timestamp)`, unique `(source_rec_id, timestamp)` (macOS reuses record ids after dismissal, so the id alone is not unique: spike 02).
- SQLite in WAL mode, single writer (the worker). TUI/CLI read directly or go through the socket for mutations.
- Keep notification *bodies* local only. `shh.db` is `0600`.

---

## 11. Package layout (Go)
```
cmd/shh/               main, kong CLI wiring
internal/ingest/       macOS db reader, plist decode, version adapters
internal/calendar/     reminder parser + scheduler
internal/classify/     features, embedder iface, logistic model, priors, boost
internal/judge/        optional zero-shot sidecar client (later)
internal/decide/       thresholds, tiers, effective-score logic
internal/alert/        overlay client, fallbacks, config
internal/summarize/    none/ollama/claude backends
internal/session/      worker, timer, IPC server/client, focus level stack
internal/replay/       replay harness: classifier over a JSONL file
internal/store/        sqlite, migrations, queries
internal/tui/          bubbletea models
internal/suppress/     banner suppressor (Accessibility watcher)
```
**[DECIDED]** Pure-Go everywhere it's sensible: `modernc.org/sqlite` (no CGo), embedded static embeddings (no ONNX). Native bits are isolated behind small interfaces (below).

### 11.1 Cross-platform stance (macOS first, Linux later)
Every OS-specific piece sits behind an interface so a Linux port is additive, not a rewrite:

| Interface | macOS (v1) | Linux (later) |
|---|---|---|
| `NotificationSource` | notification center SQLite + plist | D-Bus session bus monitor (`org.freedesktop.Notifications`, via `godbus`) |
| `Suppressor` | Accessibility watcher moves banner windows off-screen | `dunstctl set-paused` / GNOME `show-banners` / etc. |
| `Alerter` | overlay helper or `osascript` fallback | `notify-send` / libnotify |
| `MenuBar` | tray lib | tray lib (StatusNotifier) |
| `Calendar` | notification parsing | same parser rules, different apps |

Classifier, store, session worker, TUI, CLI are platform-independent. Linux is untested scope until after M4.

---

## 12. Milestones

**M0, de-risk spikes (do first, ~1–2 days)**
- Read and decode the real notification DB on this Mac. Document path, schema, required permissions.
- Hide OS banners with an Accessibility watcher while the notification DB still records them (spike 02, done).
- Capture ~10 real Outlook/Teams meeting notifications and write parser rules.
- Overlay helper proof of concept: a banner with color/size/sound.
- Menu bar proof of concept: tray lib showing live text (`28m`) next to the icon on this Mac.
- Check candidate static-embedding models for size/quality; confirm `go:embed` is viable.
- Exit criteria: all four work, or the design is revised.

**M1, walking skeleton**
- `shh focus`/`stop`/`status`, session worker + socket, ingest → store, no classifier (threshold rules only on keywords), basic `ls`.

**M2, decision engine**
- Priors + hashed features + bundled static embeddings + logistic model, history bootstrap import, tiers/thresholds, baseline + escalation stack, time boost, own-banner delivery, calendar pre-empt, menu bar countdown.

**M3, TUI + training**
- Three tabs (Focus, Notifications, Train), preview/expand, hybrid labels (`y`/`n`/`l`) with SGD, undo, score and reason display, `shh train`.

**M4, polish**
- Summarizer backends, banner config/presets, `doctor`, TTL job, migrations, packaging (`brew` tap or `goreleaser`, driven from `Taskfile.yml`).

**M5, optional**
- Offline eval of Laya/Jev judge (L3) vs. core, adopt only if it helps; `shh install` launchd mode; threshold suggestions from feedback; Linux port.

---

## 13. Testing
- **Golden tests** for plist→notification parsing using captured fixtures (scrubbed).
- **Pure-function tests** for tiers/threshold edge cases (e.g. lockdown never leaks), boost caps, and the calendar time parser (timezones, DST, "starts in 5 minutes").
- **Replay harness:** `shh replay fixtures.jsonl --threshold 65%` runs the classifier offline and prints decisions. It's also the tool for evaluating model changes (precision/recall of "should have alerted").
- Model regression test: seed dataset must keep ≥ X% agreement after a change.

---

## 14. Risks
| Risk | Impact | Mitigation |
|---|---|---|
| macOS changes DB schema/path | Ingest breaks | Version adapters, `doctor`, fixtures, fail loudly |
| FDA permission friction | Won't start | `doctor` guides setup |
| Accessibility permission is manual | Onboarding friction | `shh init` wizard and `shh doctor` |
| Banner flashes for up to ~0.5 s before the suppressor hides it; depends on Notification Center's Accessibility layout | Cosmetic, may break on a macOS update | Accept the flash; `shh doctor` self-test; Outlook mail needs its desktop alert on |
| Calendar via notifications misses meetings | Missed pre-empt | Rules tuned on real data, documented limits, later ICS option |
| Model learns "block all" | Missed important alerts | Class weights, `y` boost, threshold hints, rollback |
| Notification privacy | Sensitive content stored | Local-only, `0600`, 30d TTL, `shh purge`, claude summarizer opt-in |

---

## 15. Open decisions
Decided:
1. Session-only worker (lifetime = while any level > 0), launchd promotion later.
2. Out-of-box quality with no external service: priors + bundled static embeddings + history bootstrap.
3. Python judge (Laya/Jev) only if the offline eval shows it helps.
4. Pure-Go SQLite (`modernc.org/sqlite`).
5. Swift only if the menu bar can't be done well in Go (the overlay needs no Swift: spike 03). Linux support designed in via interfaces, shipped later.
6. No Focus mode: an Accessibility banner suppressor (spike 02). shh alerts use their own overlay, never Notification Center.

Still open:
- Can an escalation be *lower* than the baseline? **[ASSUMED yes]**
- Should `--then off` clear the baseline too, or only the escalation? **[ASSUMED clears baseline]**
- ~~Can the banner overlay be good enough in pure Go/`osascript`, or is Swift needed?~~ **Decided (spike 03): `osascript` with JXA/AppKit is enough, no Swift.**
- Tray library choice and whether it shows live text (M0 spike).
- Static-embedding model choice (M0/M2).
- Real Outlook/Teams meeting notification samples for calendar rules (M0).
