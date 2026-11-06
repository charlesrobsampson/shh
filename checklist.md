# shh Checklist

**Before any task: read `process.md` (how to work) and the sections of `plan.md` the task names. `manifest.md` is the original spec; `plan.md` wins on conflict.**

Tasks are in dependency order. Each task = one branch (`task/NN-slug`). Tick the box only in the task's final commit. Task fields: **Session** (resume command, filled when started) · **Depends** · **Read** · **Build** · **Tests** · **Manual check**.

Conventions used below: every repeatable command (build, test, run helpers, spike runners, release) is a task in `Taskfile.yml`, never a loose script or Makefile target; Go module path `github.com/charlesrobsampson/shh`; main branch is `master`; packages per `plan.md` §11; fakes live in `internal/testkit`. Spikes and tasks marked "manual check" need the user at the keyboard (macOS settings, visual checks): do everything else, then ask the user to run the check and wait.

---

## Phase 0: Foundations and spikes

### 01 · Repo bootstrap
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/01-bootstrap` · Depends: none
- Note: the repo is already initialized on `master` but has no commits. A `.gitignore` already exists (private data, databases, exports, secrets, build output). Review it, then make the initial commit of the existing files (never `captures/`; ask the user whether to commit `chat.sh`), then record the session on `master`.
- Read: `process.md`, `plan.md` §11
- Build: verify `.gitignore` works (`git status --ignored`); `go mod init`; empty package dirs from `plan.md` §11 with a `doc.go` each; `cmd/shh/main.go` printing a version; `Taskfile.yml` (`test`, `build`, `vet`, `fmt`, `check`); keep `tools/capture.py`.
- Tests: none (scaffolding). `go test ./...` must run (zero tests is fine).
- Manual check: `task build && ./shh --version` prints a version; `git status --ignored` shows `captures/`, `private/`, databases and the built binary ignored.

### 02 · Spike: suppressing banners (Focus vs Accessibility)
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/02-spike-focus` · Depends: 01
- Context: AppleScript can't toggle Focus on modern macOS. `plan.md` §2.2 proposes a custom Focus "shh" with no allowed people/apps, and Time Sensitive notifications off, toggled via Shortcuts. Must confirm notifications stay recorded in the notification DB while Focus is on (the whole design relies on this).
- Read: `plan.md` §2, §3.1; `tools/capture.py`
- Build: write `docs/spikes/02-focus.md`. Walk the user through creating the Focus and two Shortcuts (`shh-focus-on`, `shh-focus-off`); run `shortcuts run` from Go/shell; measure latency; verify with Focus on that Teams/Outlook notifications are suppressed on screen but still appear in `…/group.com.apple.usernoted/db2/db`; test whether Outlook's in-app reminder window is suppressed; check Time Sensitive behavior.
- Tests: none (manual spike).
- Manual check: with the Focus on, a test notification (`osascript -e 'display notification "x"'`) shows no banner and the row appears in the DB.
- Notes: **outcome in `docs/spikes/02-focus.md`.** Focus is dropped. The design now uses a banner suppressor (Accessibility watcher, `internal/suppress`, interface `Suppressor`): it moves each Notification Center banner window off-screen; the DB row is still written about 5 s after delivery; banners titled `[shh]` are left visible. Probe scripts are in `spikes/focus-ax/`. Tasks 24, 25, 28, 29, 36 and 38 were updated to match.

### 03 · Spike: alert banner approach
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/03-spike-banner` · Depends: 01
- Context: need custom banners (size, color, position, sound, duration, click to open app) without a second toolchain if possible. `plan.md` §7 lists options: `osascript`, a Go-driven approach, or a small Swift helper.
- Read: `plan.md` §7, §11.1
- Build: `docs/spikes/03-banner.md`. Try pure-Go/CGo-free approaches first (e.g. `osascript` with JXA/AppKit, or spawning a tiny prebuilt helper). State clearly which of size/color/position/sound/duration/click each approach can do. Recommend one. Code under `spikes/banner/`.
- Tests: none.
- Manual check: user sees a sample banner in each tested style and approves the recommended approach.
- Notes: **outcome in `docs/spikes/03-banner.md`.** Use the JXA/AppKit overlay (`spikes/banner/overlay.js`, `task spike:banner`), not Swift. Task 29 builds the real alerter: a persistent JXA process fed JSON lines, with the `[shh]`-titled `osascript` fallback. JXA gotchas (zero-arg methods are properties, `NSBox` for the background, subclass `mouseDown:` for clicks) are in the doc.

### 04 · Spike: menu bar countdown
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/04-spike-menubar` · Depends: 01
- Context: want text like `shh 28m · 65%` next to a menu bar icon, no floating window. `plan.md` §7.1 proposes `fyne.io/systray` in a separate process polling the worker.
- Read: `plan.md` §7.1, §11.1
- Build: `docs/spikes/04-menubar.md`. Prototype a tray app that shows live-updating title text and a small menu (a few items). Confirm CGo/build requirements and whether it works as a separate process on this Mac. Fall back per the plan if not.
- Tests: none.
- Manual check: user sees the live title ticking in the menu bar and the menu items clickable.
- Notes: **outcome in `docs/spikes/04-menubar.md`.** Use the JXA `NSStatusItem` helper (`spikes/menubar/statusitem.js`), not `fyne.io/systray` (CGo, jittery width). Task 30 therefore drives a persistent JXA helper over JSON lines; no CGo. Run the prototypes with `task spike:menubar` (systray) and `task spike:menubar:jxa`.

### 05 · Spike: static embedding model choice
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/05-spike-embeddings` · Depends: 01
- Context: layer L2 needs small pure-Go embeddings bundled via `go:embed` (Model2Vec-style: token lookup + mean pooling). Binary size and quality matter.
- Read: `plan.md` §5.1–5.2
- Build: `docs/spikes/05-embeddings.md`. Evaluate 2–3 candidate small static embedding models (license, size, vocab/tokenizer needs, dims). Prototype loading and embedding in Go; sanity-check similarity on ~20 hand-written notification examples (urgent vs newsletter). Recommend one with size and a plan for the tokenizer. Do not commit large model files without the user's OK.
- Tests: none.
- Manual check: user reviews the recommendation, size, and similarity sanity results.
- Notes: **outcome in `docs/spikes/05-embeddings.md`.** Recommend `minishlab/potion-base-8M` (MIT, 256 dims, 29,528-token BERT WordPiece vocab), quantized to int8 (7.7 MB, lossless in practice). Go tokenizer prototype matches the Python reference exactly (cosine 1.0000); needs `golang.org/x/text/unicode/norm`. **Asset distribution is decided (see `plan.md` §5.1): not committed; embedded under `-tags embedmodel`, otherwise downloaded by `shh init` from a pinned `models-v1` GitHub release asset with a sha256 check.** Run with `task model:fetch` then `task spike:embeddings`.

### 06 · Clock package and fake
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/06-clock` · Depends: 01
- Read: `process.md` §4
- Build: `internal/clock` (interface with `Now()` and timer/ticker creation) + real implementation; `internal/testkit` fake clock whose time advances only via `Advance(d)` and fires timers deterministically.
- Tests: table cases: timer fires only after enough `Advance`; multiple timers fire in order; stopped timer never fires; ticker fires N times for N intervals.
- Manual check: none (library).
- Notes: `clock.Clock` has `Now`, `NewTimer`, `AfterFunc` (used for "run at T−60s") and `NewTicker`; `clock.Real{}` is the wall clock. `testkit.NewFakeClock(start)` returns a `*FakeClock` whose time only moves with `Advance(d)`; callbacks run synchronously inside `Advance` in deadline order (ties in creation order) and may schedule more timers that fire in the same call. Fake ticker channels are buffered so one `Advance` over many intervals delivers every tick. Stop and Reset discard unread ticks (Go 1.23 semantics).

---

## Phase 1: Storage and config

### 07 · Store: open, migrations, schema v1
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/07-store-schema` · Depends: 01
- Read: `plan.md` §10, `manifest.md` §6
- Build: `internal/store` using `modernc.org/sqlite`, WAL mode, `0600` file, `schema_version` table, ordered migrations. Schema v1 = manifest tables plus the plan §10 additions (`bundle_id`, `source_rec_id` unique, `threshold_at_decision` (integer 0–99), `user_label` (integer 0–100), `reason`, `is_read`, `session_id`, `summary`, `calendar_events`, `unparsed_calendar`, `training_log`, `model_weights` with `feature_version` and history) and `focus_sessions` additions from plan §3.1 (`kind`, `parent_id`, `then_level`, `paused_at`, `remaining_secs`). Open helper supports `:memory:` and a file path.
- Tests: open in-memory → latest version; reopening a file DB is idempotent; an old-version DB upgrades; file permissions are `0600`; schema accepts the documented columns (round trip insert/select of each table).
- Manual check: `sqlite3 ~/.config/shh/shh.db .tables` lists all tables after a build that opens the store (a temporary `main` call is fine, not committed).
- Notes: `store.Open(path)` (`":memory:"` supported), `Close`, `SchemaVersion`. Repositories (tasks 08, 09, 19, ...) live in package `store` and use the unexported `s.db`. Migrations are appended to `migrations` in `internal/store/migrations.go`, never edited once shipped; the high-water mark for the tailer (task 17) needs its own migration. Store times as UTC. Schema choices: `then_level` NULL means "fall back to the layer beneath", 0 means off; `calendar_events` is unique on `(title, start_time)`; `source_rec_id` is unique; `threshold_at_decision` is nullable (history imports have none); `unparsed_calendar` has `notification_id, category, title, subtitle, raw_text`; foreign keys from `calendar_events`, `unparsed_calendar` and `training_log` to `notifications` are `ON DELETE SET NULL` so the TTL purge keeps history; `model_weights` has one row per saved version (history). Checked on a scratch path: all 7 tables created, files `0600`.

### 08 · Store: notifications repository
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/08-store-notifications` · Depends: 07
- Read: `plan.md` §8 (flag table), §10; `manifest.md` §5.1
- Build: insert (dedupe on `source_rec_id`), list with filters (source, since, status, min score on the 0–100 display scale (stored 0–1), tab views: inbox = not dismissed, train = not trained), update flags (`is_read`, `is_dismissed_inbox`, `is_trained` + `user_feedback` / `user_label`), set summary; every inserted row records `threshold_at_decision`. Define the public types here.
- Tests (table, via public API): duplicate insert ignored; tab views show the correct rows after each flag change (training removes from Train view only; dismiss removes from Inbox only); filters combine; ordering newest first.
- Manual check: none.
- Notes: API in `internal/store/notifications.go`: `Notification` (optional ints are pointers: `ThresholdAtDecision`, `UserLabel`; `SourceRecID` and `SessionID` use 0 for none), `InsertNotification` (returns `id, inserted, err`; requires a threshold), `GetNotification`, `ListNotifications(Filter{View, Source, Since, Status, MinScore, Limit})`, `SetRead`, `SetDismissed`, `SetSummary`, `Train(id, Training{Feedback|Label})`, `Untrain`, `ErrNotFound`. Views: `ViewInbox` (not dismissed), `ViewTrain` (not trained). `Train` does not dismiss: the TUI calls `SetDismissed` too when training from the inbox. `MinScore` compares the rounded base score (so 0.57 counts as 57). **Migration 2 changed the dedupe key to `(source_rec_id, timestamp)`** because macOS reuses record ids (spike 02); a repeat with the same pair is ignored, a reused id with a new delivery time is stored. Timestamps are written UTC in a fixed-width text format so SQL ordering is correct.

### 09 · Store: focus levels repository
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/09-store-levels` · Depends: 07
- Read: `plan.md` §3.1
- Build: persist layers (baseline, escalations): create, pop, pause/resume with remaining time, set `then_level`, list active stack, close with end time, counters (blocked/allowed).
- Tests: stack order; pause stores remaining; closing sets end time; reopen DB restores stack.
- Manual check: none.
- Notes: API in `internal/store/levels.go`: `Level` (`Kind` baseline/escalation, `Percent` 0-99, `ThenLevel` nil=layer beneath/0=off, `StartTime`, `EndTime` nil while active, `TargetMinutes` for display, `ExpiresAt` for a running timed level, `PausedAt`+`RemainingSecs` for a paused one, `Blocked`/`Allowed`), `PushLevel` (sets `ParentID` to the current top), `ActiveLevels` (bottom first, baseline always at the bottom, last element is the top), `GetLevel`, `CloseLevel(id, end)` (any level, e.g. an expired one below the top), `PopLevel(end)` (top escalation only, `ErrNotFound` if none), `PauseLevel(id, now)` (stores remaining seconds rounded up, clears `ExpiresAt`), `ResumeLevel(id, now)`, `SetThenLevel`, `AddCounts`. **Migration 3** added `expires_at` and a unique index allowing one active baseline, so the engine must close the old baseline before pushing a new one. Not here yet, add in task 12 if needed: change a level's percent (`SetLevel`) and adjust the expiry (`Adjust`).

### 10 · Config loader
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/10-config` · Depends: 01
- Read: `plan.md` §7, §7.1, §6
- Build: `internal/config` loads `~/.config/shh/config.toml` (path overridable) with defaults: alert style (position, size, color, duration, sound, per-kind and per-source overrides), `[menubar] label = "shh"`, summarizer backend (`none`), paths. Unknown keys are errors with a clear message.
- Tests: defaults when file missing; partial file merges over defaults; per-source override resolves; invalid value or unknown key errors name the key.
- Manual check: `shh config show` (temporary or real command if present) prints effective config.
- Notes: `config.Load(path)` (missing file = defaults; `config.DefaultPath()` is `~/.config/shh/config.toml`), TOML via `github.com/pelletier/go-toml/v2` in strict mode. `Config{Alert, Menubar, Summarizer, Paths}`. `cfg.Alert.Resolve(kind, source)` returns the resolved `Style` (defaults, then base, then kind override (`KindCalendar`), then per-source override); size names (`small|medium|large`) map to pixels in task 29. `Paths` default to the config file's directory (`shh.db`, `shh.sock`, `shh.pid`); `[paths] dir` moves them and relative overrides are relative to it. Errors name the file, line and key (`unknown key "alert.colour"`). `cfg.Effective()` renders the resolved config as TOML that loads back identically: use it for `shh config show` (task 27 or 36). Summarizer settings beyond `backend` (URL, model) are added in task 35. Checked with a throwaway program, not committed.

---

## Phase 2: Levels and decisions (pure logic)

### 11 · Threshold and tier parsing
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/11-thresholds` · Depends: 01
- Read: `manifest.md` §3.3, `plan.md` §4.3
- Build: parse percentages (`75%`) and tier names into an integer level 0–99: `off`=0, `light`=35, `medium`=65, `deep`=90, `lockdown`=99; `100%` is an alias for 99; plain decimals like `0.75` are rejected. Also parse exact training labels 0–100 (tier names allowed) separately, since labels may be 100. Durations (`25m`, `1h30m`, `0`). Read `plan.md` §4.3 for the scale.
- Tests: table of valid/invalid inputs, boundaries (`0%`, `99%`, `100%` as threshold → 99, `101%`, `-5%`, `abc`, `1h30m`, `0`; label `100` valid, label `101` invalid).
- Manual check: none.
- Notes: package `internal/decide` (`parse.go`): `ParseThreshold("75%"|tier)` returns 0-99 (`MaxLevel` = 99); `ParseLabel("80"|"80%"|tier)` returns 0-100; `ParseDuration("25m")` returns a `time.Duration` (0 = indefinite). Decisions: a threshold must be `N%` or a tier name, a bare number such as `75` is rejected with a hint (ambiguous with decimals and durations), while labels accept bare numbers because the training prompt takes them; tier names are case-insensitive; durations need a unit (`25` is rejected, `0` is allowed), reject negatives and anything non-zero under a second, and accept decimals like `1.5h`. Errors quote the input and say what to write.

### 12 · Level stack engine
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/12-level-stack` · Depends: 06, 09, 11
- Read: `plan.md` §3.1
- Build: engine over the store with the injected clock: `SetBaseline`, `Escalate(level, duration, then)` (levels are integers 0–99 from task 11), `Stop`, `StopAll`, `Pause/Resume`, `Adjust(±5m)`, `SetLevel`, `Effective(now)`, expiry handling that pops layers and reverts to the layer beneath (or `then`).
- Tests (fake clock, table): baseline 30% → escalate 90% 50m → at +50m effective is 30%; `then off` clears baseline; escalation lower than baseline wins while active; pause freezes remaining time; adjust extends/shortens; stop pops top only; stop all clears; indefinite escalation never expires; state survives engine restart from the store.
- Manual check: none.
- Notes: lives in `internal/session` (`stack.go`; plan §11 has no separate package). `session.NewStack(store, clock)`; methods `SetBaseline(level)` (0 turns it off), `Escalate(Escalation{Level, Duration, Then, Note})`, `Stop` (top escalation only, `ErrNothingToStop` otherwise), `StopAll`, `Pause`/`Resume` (top timed layer; `ErrNoTimer`, `ErrNotPaused`), `Adjust(delta)`, `SetLevel(level)` (top layer, `ErrNoSession` if empty), `Status()` returning `Status{Level, Layers}` where each `Layer` embeds `store.Level` plus `Timed`, `Paused`, `Remaining`, and `NextExpiry()` for scheduling the worker's wake-up. The stack holds no state, so restarts just reload from the store; expired timers are settled lazily on every call, each closed at its own deadline. Semantics decided here (also in `plan.md` §3.1): `Then` only applies on timer expiry (closes everything beneath, then starts a baseline at X if X is above 0); manual `Stop` ignores it; pause freezes the countdown, not the level; cutting a timer past its end expires it immediately. Added store methods `SetExpiresAt`, `SetRemainingSecs`, `SetPercent`.

### 13 · Rules file loader
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/13-rules-loader` · Depends: 01
- Read: `plan.md` §4.2, §5.1 (L0), §5.4
- Build: `internal/rules` loads `rules.toml` (embedded defaults, user override): urgency keywords with boosts, VIP senders/apps, per-app priorities, calendar categories, date patterns list.
- Tests: defaults load; user file overrides/extends; invalid regex is an error naming the rule.
- Manual check: none.
- Notes: `rules.Load(userPath)` (empty or missing path = embedded defaults from `internal/rules/default_rules.toml`; `rules.Defaults()`), the user file is normally `<config dir>/rules.toml`. `Rules{Urgency []Urgency{Pattern{Name, Regexp}, Boost}, VIPSenders, VIPApps (lower-cased, deduplicated), AppPriority map, CalendarCategories, DatePatterns}`. Merge rules: a rule with the same `name` replaces the default, a new name adds one, `enabled = false` removes one; VIP lists are unioned; `app_priority` entries override per key. All patterns are Go regexps, compiled case-insensitively (a `(?i)` prefix is added). Boost must be above 0 and at most 0.4. Errors name the file, key, or the rule kind and name. Contract for task 23: date patterns are tried in order and expose the named groups `year`, `month`, `day`, `hour`, `minute`, `ampm`, `minutes` (the seeded set is a starting point, edit as real samples arrive). Task 17 should add the bundle-id to source-name mapping to this file (a `[sources]` table) since the plan wants it extensible via rules. New shared package `internal/tomlx` does strict decoding with errors naming file, line and key; `config` now uses it too.

### 14 · Time-sensitivity boost
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/14-boost` · Depends: 06, 08, 13
- Read: `manifest.md` §3.2, `plan.md` §5.4
- Build: boost function (keywords, same-sender burst within a window using recent history) capped at 0.4.
- Tests (table): each keyword; case-insensitivity; cap; burst detection with fake clock; no match → 0.
- Manual check: none.
- Notes: `classify.NewBooster(rules, store)` and `Boost(BoostInput{Source, Sender, Text, At}) (BoostResult{Value, Reasons}, error)` in `internal/classify/boost.go`. Value is 0 to 0.4, rounded to 3 decimals. Every matching urgency rule adds its boost (sum, then cap); a burst adds 0.1 when the new message is the third from the same source and sender within two minutes (window measured back from `At`, inclusive). The caller builds `Text` from title, subtitle and body and must call `Boost` before inserting the notification. Reasons are `keyword: <rule name>` and `burst: ...` strings for task 22. The burst check needs a sender, so unknown senders never burst. Burst size, window and boost are constants in the file, not yet configurable. Added `Sender` to `store.Filter`.

### 15 · Decision function
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/15-decide` · Depends: 11
- Read: `plan.md` §4.3
- Build: `Decide(threshold, baseScore, boost, calendarPreempt)` returns allow/block and the effective score, per `plan.md` §4.3: `S_eff = max(base, min(0.99, base + boost))` (corrected from the original `min(0.99, base + boost)`, which would have capped a label-100 score at 0.99), interrupt iff calendar pre-empt or `S_eff > threshold/100`. No special case for `lockdown` (it is threshold 99).
- Tests (table): equality at threshold is blocked; one point above passes; boost can raise a score over the threshold but never over 0.99 (so boost alone never pierces lockdown); a base score above 0.99 (label-100 style) passes lockdown; score 0 is blocked at any threshold ≥ 0; calendar pre-empt always passes.
- Manual check: none.
- Notes: `decide.Decide(threshold, base, boost, calendarPreempt) Decision{Interrupt, Effective}` in `internal/decide/decide.go`. The comparison is strict (`effective*100 > threshold`) with a 1e-9 tolerance so float noise cannot let a score equal to the threshold through (a sweep test covers every score against every threshold). It is pure math: threshold 0 (focus off) is not special-cased, so callers must not decide at level 0 (the worker does not). Corrected `plan.md` §4.3 to the `max` formula.

---

## Phase 3: Ingest

### 16 · macOS notification reader + fake source
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/16-ingest-reader` · Depends: 01
- Read: `plan.md` §2.3, §4.1, §11.1; `captures/meetings.jsonl` (local, private: build a scrubbed fixture, don't commit it)
- Build: `NotificationSource` interface (`Since(recID)` returning decoded notifications). macOS implementation reads the DB read-only (path probing: `…/group.com.apple.usernoted/db2/db`, then older path), opens a fresh connection per read, decodes the plist (`req.titl/subt/body/cate/iden`, `app`, `date`), converts `delivered_date` from the 2001 epoch. `testkit` fake source supports add/remove records.
- Tests: tests create a SQLite file with the real schema in a temp dir plus plist blobs built from synthetic notifications; cases: new rows since id, rows removed after reading, corrupt plist is skipped and reported, missing DB gives a clear error, epoch conversion, row with no body.
- Manual check: `go run ./cmd/shh ingest-dump` (temporary) prints your latest real notifications; nothing is written anywhere.
- Notes: `internal/ingest`: `Source` interface (`Since(recID) (Batch, error)`), `Notification{RecID, BundleID, Title, Subtitle, Body, Category, Identifier, Delivered}`, `Batch{Notifications, Skipped}` (a record that cannot be decoded goes to `Skipped{RecID, Err}` and never fails the read), `ErrNoDatabase` (lists the probed paths). `ingest.NewMacOS(paths...)` reads the first existing path read-only over a fresh connection per call; `ingest.DefaultPaths(home)` gives the current location then the older `group.com.apple.usernotificationsd/db` one from the plan (the older path is not verified on a real older Mac). Plist decoding uses `howett.net/plist`; `delivered_date` is seconds since 2001 and comes back as UTC. `testkit.FakeSource` (`Add`, `Remove`, `FailWith`, `Since`) hands out `max(id)+1` like SQLite, so removing the newest row makes the next `Add` reuse its id: use it to test the tailer against id reuse. For task 17: macOS reuses record ids and writes rows about 5 s after delivery, and a real run showed three Outlook reminders with identical delivery time (rows 57 to 59), so the tailer should call `Since` with a lookback, dedupe on `(source_rec_id, timestamp)` (the store already does) and on the 10-second same app/title/body rule. Checked against the real database with a throwaway program printing only lengths: 52 notifications, 0 skipped, the database file unchanged.

### 17 · Tailer: high-water mark, dedupe, backfill
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/17-tailer` · Depends: 06, 08, 16
- Read: `plan.md` §4.1
- Build: poll loop on the injected clock using a `NotificationSource`; persist high-water mark; dedupe same app+title+body within 10s; ignore shh's own bundle id; backfill since a given time; map bundle id → friendly source (`com.microsoft.teams2` → Teams, `com.microsoft.outlook` → Outlook, etc., extensible via rules).
- Tests (fake source + fake clock + in-memory store): rows appear once; restart resumes without duplicates; burst duplicates collapse; own-bundle ignored; backfill picks up rows before start time; source mapping table.
- Manual check: none.
- Notes: `ingest.NewTailer(ingest.Config{Source, Store, Clock, Sources, OwnBundles, Interval, Handle, OnError})` in `internal/ingest/tailer.go`; `Poll()` (one pass), `Backfill(since)`, `Run(ctx)` (ticker on the injected clock, errors go to `OnError`, returns nil on cancel). `Event` embeds `Notification` plus the friendly `Source`; it carries raw title, subtitle and body, so deriving the sender is the pipeline's job (task 25). **The high-water mark is a time cursor, not a `rec_id`** (macOS reuses ids and writes rows about 5 s late and out of order): each poll handles unseen rows delivered within 30 s of the cursor, keyed on `(rec_id, delivered)`; cursor and seen set persist in the store (migration 4: `ingest_state`, `ingest_seen`; `Store.IngestCursor/SetIngestCursor/MarkSeen/IsSeen/PruneSeen`), and a seen pair is only forgotten once macOS deleted its row, so a later `Backfill` never repeats rows still in the source. The first poll ever starts the cursor at now (old history is not replayed; the bootstrap import is task 36). Dedupe: same bundle, title and body within 10 s collapse, measured from the last emitted event so a long chain of reposts still produces an event every 10 s or so. Ignored: `OwnBundles` and any title starting with `ingest.OwnTitlePrefix` (`[shh]`, the alerter fallback must use it). A handler error leaves the row unhandled so it is retried. Undecodable records go to `OnError` once each. Source names come from the new `[sources]` table in `rules.toml` (`rules.Rules.Sources`), else the last part of the bundle id capitalised. For task 28: the worker should call `Backfill(max(session start, stored cursor - 30s))` before `Run`, and for task 25: treat a duplicate `InsertNotification` (`inserted == false`) as already handled before alerting.

---

## Phase 4: Classifier

### 18 · Features L0 + L1
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/18-features` · Depends: 13
- Read: `plan.md` §5.1, §5.3
- Build: `Features` interface returning a versioned vector. L0: VIP/app priority/mention/1:1 vs channel/urgency-word flags. L1: hashed n-gram text features plus sender/app/category hashes. Feature version string.
- Tests: same input → same vector; different sender/app change the right features; hash collisions don't panic; version string changes when feature config changes.
- Manual check: none.
- Notes: `internal/classify/features.go`. `classify.Features` interface (`Extract(Input) Vector`, `Dim()`, `Version()`), implemented by `classify.NewL0L1(rules, WithDim(n)) (*L0L1, error)` (default `DefaultDim` = 16384). `Input{Source, BundleID, Sender, Title, Subtitle, Body, Category}`: the caller derives `Sender`. `Vector{Indices (strictly ascending), Values (positive), Labels}` is sparse, and every entry has a human-readable label (`vip sender`, `word: outage`, `phrase: server down`, `sender: ana`, `app priority: Teams`, `urgency: outage`, `question`...), which task 22 uses to explain decisions. The first `L0Slots` (16) indices are reserved for fixed layer 0 flags (VIP sender, VIP app, app priority as a value, mention, channel, direct, urgency matched, question) and never collide with hashed features; everything else (words, bigrams, per-rule urgency flags, sender, app, category) is FNV-1a hashed into the rest, binary (a repeated word counts once), and colliding features add up. Text is capped at 128 tokens. `Version()` is `l0l1-` plus a hash of the extraction revision, dimension and the feature-relevant rules (urgency rules, VIPs, app priorities), so changing any of them invalidates stored weights; task 20 must append the embedding model id to it. **The channel/direct and mention flags are unverified heuristics** (title or subtitle containing `#` or ` in ` means channel; `@you`, `@here`, `@channel`, `@everyone` or "mentioned you" means mention): all 20 real Teams rows here have only a title and body, so refine them with real captures (`shh capture`, task 36).

### 19 · Logistic model, SGD, persistence, undo
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/19-model` · Depends: 07, 18
- Read: `plan.md` §5.3
- Build: logistic regression with bias; `Predict`; `Train(example, label)` single-step soft-target SGD with learning rate, L2, clipping, where `label` is either exact (0–100) or a constraint (`y`/`n` plus `threshold_at_decision`) per `plan.md` §5.3 (constraints update only when violated, with margin 0.1); persist weights keyed by feature version with history; log each update in `training_log` with its delta; `Undo(n)`, `Rollback`, `Reset`.
- Tests (table, in-memory store): exact label 80 moves a similar item's score toward 0.8 and label 10 toward 0.1; `y` at threshold 65 on a score of 40 raises it above 0.65, but does nothing when already above; `n` on an alerted item at 65 lowers it to ≤ 0.65, but does nothing when already below; undo restores exact weights; rollback returns to a prior version; weights persist across restart; feature-version mismatch starts fresh instead of loading garbage; clipping bounds weights.
- Manual check: none.
- Notes: `internal/classify/model.go`. `classify.NewModel(store, features, opts...)`, `Predict(Vector)`, `Train(notificationID, Vector, Label)` returning `TrainResult{Before, After, Updated, Steps}`, `Undo(n)`, `Checkpoint()`, `Rollback()`, `Reset()`, `Weights()` (bias plus non-zero weights, for export). Labels: `ExactLabel(n)` (one SGD step toward n/100) and `QuickLabel("y"|"n", thresholdAtDecision)` (nothing happens if the constraint already holds, otherwise SGD steps, capped at 200, toward `min(1, T+0.1)` or `max(0, T-0.1)` until it holds). Defaults: learning rate 0.1, L2 1e-4, clip ±5 on weights and bias, margin 0.1 (`WithLearningRate` changes the rate). Training uses the vector only, never the boosted score. Persistence: sparse, exact float64 weights per feature version (`Store.LoadWeights/UpdateModel`, newest 20 snapshots kept per version); a feature version with no saved weights starts fresh and drops the old undo log, saved weights that do not parse are an error rather than a silent reset. Every `Train` call is logged (including no-op quick labels, so undoing one does not revert an earlier real update) with the values it overwrote, so `Undo` restores weights bit for bit; `Checkpoint`/`Rollback`/`Reset` use marker rows in the same log (`Rollback` skips a checkpoint with nothing trained after it and uses a checkpoint up). Changed vs the plan: the quick-`y` target cap is 1, not 0.99, so `y` at lockdown (needs `S > 0.99`) can be satisfied. No per-feature AdaGrad decay yet (constant learning rate). Task 21 will want a bulk-training path that does not log each seed example; task 22 can rank `Vector.Labels` by weight times value for the reason string; the TUI undo (task 33) calls `Model.Undo(1)` together with `Store.Untrain`/`SetDismissed`.

### 20 · Static embeddings L2
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/20-embeddings` · Depends: 05, 18
- Read: `docs/spikes/05-embeddings.md`, `plan.md` §5.1
- Build: model loading from spike 05 with three sources in order: embedded (`-tags embedmodel`), then `~/.config/shh/models/`, else L2 off with a clear status; sha256 checked against a constant; tokenizer, mean pooling, `Features` adapter appended to L0+L1; feature version includes the model id.
- Tests: urgent vs newsletter examples score closer to their own class in a tiny trained-model check; deterministic output; empty text handled; missing model disables L2 without error; a file with a wrong sha256 is rejected. Tests use a tiny synthetic model, never the real asset.
- Manual check: `shh classify "server is down"` (temporary command ok) prints a score; binary size noted in the task `Notes:`.
- Notes: in `internal/classify`: `wordpiece.go` (BERT WordPiece tokenizer, needs `golang.org/x/text`), `static.go` (`Embedder` interface, `BuildStaticModel(id, safetensors, tokenizerJSON)` converter, `NewStaticEmbedder(asset)`, `ResolveEmbedder(dir, sha256) (Embedder, source, error)`, `ErrNoEmbeddingModel`, `ModelFileName`, `DefaultModelSHA256`), `embedded_on.go`/`embedded_off.go` (`-tags embedmodel` embeds `assets/potion-base-8M-int8.bin`, which is gitignored). `classify.WithEmbedder(e)` on `NewL0L1` appends the embedding after the hashed features as dense entries all labelled `meaning` (so task 22 can sum them into one factor), `Dim()` becomes hashed size + embedding size, and `Version()` ends with `+<model id>`; empty or all-unknown text adds no dense entries. Note dense values can be negative, unlike the positive L0/L1 values. Resolution order: embedded copy, then `<models dir>/potion-base-8M-int8.bin`, else `ErrNoEmbeddingModel` (callers run without L2 and `shh doctor` says so); a present file with a wrong checksum or damaged contents is an error, not a silent fallback. The asset format is shh's own (int8 rows with per-row scale, vocabulary in id order, about 7.9 MB for potion-base-8M); `task model:fetch` then `task model:build` (via `tools/modelbuild`) makes it deterministically at `private/models/potion-base-8M-int8.bin` with sha256 `bdd4b106b293635d9a1b60eb9f9786f8fd47a7f4215a8f7c5f8fa0984d4356bd` (pinned as `DefaultModelSHA256`). **Still to do, in task 38/36:** publish that file as the `models-v1` release asset, have `shh init` download it into `<config dir>/models/`, and make the release task copy it into `internal/classify/assets/` and build with `-tags embedmodel`. Manual check, against the real model: the spike probes reproduce exactly (0.65, 0.43, 0.36), embedding takes about 6.4 µs per notification, and a binary that links the classifier grows by 8.0 MB with `-tags embedmodel` (9.4 MB to 17.4 MB). A fresh model scores 0.5, so there is no meaningful `shh classify` output until the seed weights in task 21. Tests use a tiny synthetic model built through the real converter: tokenizer cases, pooling, quantization error, damaged assets, checksum, and an end-to-end check that the embedding generalises to words never seen in training.

### 21 · Seed weights and day-one quality
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/21-seed` · Depends: 19, 20
- Read: `plan.md` §5.3 (cold start)
- Build: seed training set (synthetic examples of urgent/non-urgent notifications across Teams/Outlook/Slack/Mail/system) shipped in-repo; first run trains the initial weights from it; replay harness: `shh replay <jsonl> --threshold 65%` prints decisions.
- Tests: with the seeded model: outage/"urgent"/direct-question examples score above 65 (interrupt at `medium`); newsletter/FYI/auto-update examples score at or below 35 (muted at `light`); replay output stable for a synthetic fixture file. The seed set is synthetic and committed; real labeled data never is.
- Manual check: run `shh replay` on a few lines you write; results look sensible.
- Notes: seed set is `internal/classify/seed/seed.jsonl` (124 synthetic examples, 47 clearly urgent and 53 clearly unimportant, across Teams, Outlook, Slack, Mail, Messages and System; embedded in the binary; never real data), loaded by `classify.SeedExamples()`. `classify.NewClassifier(store, rules, embedder)` (embedder may be nil) builds features, model and booster, and on a fresh feature version pretrains once with `Model.Pretrain(examples, epochs)` (60 epochs, fixed order, one save, **no per-example undo rows**, a single checkpoint marker so `Rollback` returns to the seeded state; `Model.Fresh()` tells whether weights were ever saved). `Classifier.Score(input, at)` returns `Score{Base, Boost, BoostReasons, Vector}`; call it before storing the notification. `Reset()` zeroes weights and does not re-seed: if `shh train --reset` should go back to the seed, delete the saved weights instead or call `Pretrain` again (task 36). Quality tests use held-out examples written before the seed: urgent ones score 81 to 96, unimportant 6 to 34 (L0+L1 only, no embeddings, so they run offline); with the real embedding model the same kind of text separates similarly. Known weak spot: words absent from the seed (for example "crashed") score a middling ~50 until the user labels them. **Replay:** `internal/replay` (`replay.Run(in, out, Options{Threshold, Rules, Embedder})`) prints `ALERT|muted <base> +<boost> = <effective> <source> <sender>: <text> [label N]` per JSON line and a summary; it runs on an in-memory store so nothing on disk changes; lines without `at` are spaced five minutes apart (a one-minute spacing triggers bursts); unknown JSON fields are errors; the integer display rounds, so a score of 65.4 shows as 65 yet interrupts at threshold 65. Golden test `internal/replay/testdata/fixture.golden` (regenerate with `go test ./internal/replay -update`). **CLI:** `cmd/shh/main.go` now uses `kong` (with `--version` and `replay <file> [-t 65%]`); task 27 adds the other commands to this struct. `shh replay` loads `<config dir>/rules.toml` and, if present, `<config dir>/models/potion-base-8M-int8.bin`, otherwise runs without layer 2 and says so on stderr.

### 22 · Decision reasons
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/22-reasons` · Depends: 19, 14
- Read: `plan.md` §4.3, §8
- Build: short human-readable `reason` (top contributing features and any boost) returned with each prediction; stored with the notification.
- Tests: reason mentions the dominant factor (VIP sender, keyword, learned phrase); stable ordering; length bounded.
- Manual check: none.
- Notes: `Classifier.Score` now fills `Score.Reason` (task 25 stores it in `notifications.reason`). Built from `Model.Contributions(vector)` (weight times value per feature, in logit units; the dense embedding entries are merged into one `meaning` factor; the score is the sigmoid of the bias plus all contributions) and the pure `classify.Explain(base, factors, boost)`: up to three features that pushed the score the way it went (`important: ...` at 0.5 and above, `unimportant: ...` below), contributions under 0.05 ignored, ties alphabetical, labels cut at 32 characters, then `; boost +N (keyword: x, burst: ...)`, the whole thing at most 140 characters (`no strong signals` when nothing stands out). Function words (the, are, your, ...) are never given as reasons, and the generic `urgency matched` flag is dropped when a specific `urgency: <rule>` factor is present. Example: `important: urgency: urgent, question; boost +40 (keyword: urgent, keyword: down)`. The reason describes the score only, not the decision against a threshold. Task 33 can show it in the Train detail pane as is.

---

## Phase 5: Calendar

### 23 · Calendar reminder parser
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/23-calendar-parser` · Depends: 13, 08
- Read: `plan.md` §4.2 (fail-safe strategy and first sample)
- Build: detect reminders by category from rules; parse the start time leniently with the ordered formats; extract title/subtitle; on parse failure return an "alert immediately" result and record to `unparsed_calendar`.
- Tests (table incl. the real sample, scrubbed): `November 5, 2026 at 13:30`; `Today at 9:00 AM`; `in 5 minutes`; all-day wording; DST edge; unknown format → immediate alert + stored unparsed; non-reminder Outlook mail is ignored; Teams meeting notification.
- Manual check: none.
- Notes: `internal/calendar/parser.go`. `calendar.NewParser(rules, location, store)` (store may be nil; the location is where `Today at 9:00 AM` and `November 5, 2026 at 13:30` are read) and `Parse(calendar.Input{NotificationID, Category, Title, Subtitle, Body, Delivered}) (Reminder, ok, err)`: `ok` is false for anything whose category does not match `rules.CalendarCategories` (mail, no category, other apps); `err` is only a failure to record. `Reminder{Title, Subtitle, Start, AllDay, Unparsed}` with `AlertNow()` true for `AllDay` and `Unparsed`: the scheduler (task 24) fires those immediately and schedules the rest at `Start` minus 60 s. Text is tried in the order body, subtitle, title, and for each the date patterns in rule order; the named groups drive the reading (`minutes` is relative to the delivery time, `hour`/`minute`/`ampm` with optional `year`/`month`/`day`, month names by their first three letters, 12-hour times need hour 1 to 12, impossible dates like November 31 do not parse). A pattern that matches but captures no time means an all-day reminder. Unreadable reminders return `Unparsed`, and are saved with `Store.AddUnparsedCalendar` (`category, title, subtitle, raw_text` plus the notification id) and listed newest first by `Store.UnparsedCalendar()` for `shh rules review` (task 36). DST: times are built in the parser's zone so offsets are right on both sides of a change, a relative "in N minutes" is an exact duration, and a wall-clock time that does not exist (2:30 AM on spring-forward day) still gets a start within the hour. Only the Outlook reminder category is in the default rules; the Teams meeting category is not known (no real sample), so add it under `[[calendar.category]]` in `rules.toml` when captured: a test shows a user-added category works. Tests use `time/tzdata` so they run anywhere.

### 24 · Calendar scheduler
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/24-calendar-scheduler` · Depends: 06, 23
- Read: `plan.md` §4.2, §11.1
- Build: define the `Alerter` and `Suppressor` interfaces (in `internal/alert` and `internal/suppress`) with in-memory fakes in `testkit` (the alerter fake records shown banners; the suppressor fake holds running/stopped state); then schedule an alert at start − 60s; fire immediately if past; dedupe by title+start; persist in `calendar_events`; re-arm on restart; no double fire.
- Tests (fake clock, fake alerter): fires at T−60s exactly; late reminder fires now; same meeting twice → one alert; restart re-arms pending only; fired events don't refire.
- Manual check: none.
- Notes: **Interfaces defined here.** `alert.Alerter` (`Show(alert.Alert) error`) with `alert.Alert{Kind config.Kind, Source, BundleID, Title, Body}` in `internal/alert`; `suppress.Suppressor` (`Start() error`, `Stop() error`, both idempotent) in the new `internal/suppress` (the old `internal/focus` placeholder is removed). Fakes in `testkit`: `FakeAlerter` (`Shown()` returns a copy, `FailWith(err)`) and `FakeSuppressor` (`Running()`, `Starts()`, `Stops()` count effective changes only, `FailWith(err)`). **Scheduler** `internal/calendar/scheduler.go`: `calendar.NewScheduler(SchedulerConfig{Clock, Store, Alerter, Lead (default 60 s), Location (for the times in the text), OnError})`; `Schedule(reminder, sourceNotificationID)` saves a `calendar_events` row (deduped on title plus start via the new `Store.AddCalendarEvent`) and arms it with `clock.AfterFunc` at start minus lead, or fires immediately when that time has passed or the reminder has no start (`AlertNow()`: all-day or unparsed; repeats are told apart by title plus the delivery minute); `Rearm()` re-arms every unfired row (due ones alert late rather than never, and it never doubles up timers); `Stop()` cancels timers and keeps the rows. Firing shows the alert and then marks the row fired, under one lock (`Store.MarkCalendarEventFired` is also atomic), so nothing fires twice; if `Show` fails the row stays pending, `OnError` hears about it and the next `Rearm` retries. Alerts are `Kind: calendar`, `Source: "Calendar"`, body `Starts at 09:10` / `Started at 08:55` / `Meeting reminder` (untimed), and carry no bundle id yet (clicking could open the calendar app later). `calendar.Reminder` gained a `Delivered` field. The worker (task 25) calls the parser, then `Schedule`, and `Rearm` at startup. Added `Store.AddCalendarEvent/GetCalendarEvent/PendingCalendarEvents/MarkCalendarEventFired`.

---

## Phase 6: Worker, IPC, CLI

### 25 · Worker pipeline (end to end, in memory)
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/25-worker-pipeline` · Depends: 12, 15, 17, 19, 22, 24
- Read: `plan.md` §3, §4
- Build: `internal/session` worker wiring tailer → classify → boost → decide → deliver (alerter interface) or store; updates counters; calendar pre-empt path; uses the `Alerter` and `Suppressor` interfaces and fakes from task 24.
- Tests (fakes for source, clock, alerter, suppressor): at 65% an "outage now" Teams message is alerted and stored `alerted`, a newsletter is blocked and stored `blocked` with reason; escalation expiry changes the next decision; calendar reminder pre-empts at T−60s even in lockdown; the suppressor is started when level > 0 and stopped at 0%.
- Manual check: none.
- Notes: `internal/session/worker.go`. `session.NewWorker(session.Config{Store, Source, Clock, Rules, Embedder (nil = no layer 2), Alerter, Suppressor, Location, OwnBundles, Interval, OnError, OnDecision})`. Level control is wrapped so every change brings the suppressor in line: `SetBaseline`, `Escalate`, `Stop`, `StopAll`, `Pause`, `Resume`, `Adjust`, `SetLevel`, `Status`; the suppressor runs exactly while the effective level is above 0 (a timed layer ending is noticed on the next `Tick`, at most one interval later). `Init(backfillSince)` re-arms pending calendar alerts and backfills; `Tick()` is one cycle (sync suppressor, poll); `Run(ctx, backfillSince)` creates its ticker first, then `Init`, then ticks on the clock until cancelled, and `Shutdown()` (also run on exit) cancels calendar timers and stops the suppressor. **Pipeline per new notification (`handle`):** at effective level 0 nothing is decided or stored (the OS shows it); otherwise build `classify.Input` (Sender is the notification title for every source, a best-effort mapping to refine with real captures, and the subtitle is kept in the stored title as `title · subtitle`), score it (before inserting, so a burst does not count itself), decide with the effective level, insert the row with status `alerted` or `blocked`, score, boost, reason, bundle id, macOS record id, `threshold_at_decision` and the active layer id as `session_id`, then act: a calendar reminder (category match) pre-empts, is stored as alerted, and is parsed and handed to the scheduler (its alert comes at T-60s, or at once if unreadable or late), an interrupting message goes to `Alerter.Show` (kind `message`, the source name, bundle id, title, subtitle plus body cut at 200 characters), and the active layer's allowed or blocked counter is incremented. A duplicate insert (`inserted == false`) is skipped entirely. A failing `Alerter.Show` goes to `OnError` and is not retried: the row is already stored and a retry would only find it again, so the notification stays visible in the inbox. `OnDecision(Decision{Notification, Alerted})` reports each decision (for the IPC subscribe stream, task 26; `Alerted` is false for calendar reminders, whose alert comes later). Added `calendar.Parser.IsReminder`. Restart behaviour is covered: no repeat alerts, and pending calendar alerts re-arm.

### 26 · IPC server and client
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/26-ipc` · Depends: 25
- Read: `plan.md` §3
- Build: unix-socket JSON-lines protocol: `status`, `focus`, `baseline`, `stop`, `pause`, `resume`, `adjust`, `subscribe` (live events). Client library. Socket path configurable (temp dir in tests).
- Tests: each command changes worker state and returns the new status; two clients; subscriber receives blocked/alerted events; worker absent gives a clear "not running" error; stale socket file is cleaned.
- Manual check: none.
- Notes: `internal/session` (`protocol.go`, `server.go`, `client.go`). **Protocol:** JSON lines over a unix socket (mode 0600). A request line `{"cmd": ..., "level", "duration_secs", "then", "note", "all", "delta_secs"}` gets one response line `{"ok", "code", "error", "status"}`; commands `status`, `baseline` (level, 0 = off), `focus` (level, duration_secs with 0 = until stopped, then, note), `level` (change the top layer's threshold, for the TUI `f` key), `stop` (all), `pause`, `resume`, `adjust` (delta_secs, may be negative), and `subscribe`, after whose acknowledgement the connection carries only `Event` lines (`{"type":"decision","notification":{id, source, sender, title, body, time, status, score, boost, reason},"alerted"}`). Every command answers with the new `StatusView{Level, Layers[LayerView{ID, Kind, Percent, ThenLevel, Note, Started, Timed, Paused, RemainingSecs, ExpiresAt, Blocked, Allowed}], Now}`. **Server:** `session.Listen(path, worker)` claims the path (replaces a stale socket, returns `ErrAlreadyRunning` for a live worker, refuses to delete a file that is not a socket) and `Serve(ctx)` answers until cancelled, then closes connections and removes the socket. **Client:** `session.NewClient(path)` with `Status`, `Baseline`, `Focus(level, duration, then, note)`, `SetLevel`, `Stop(all)`, `Pause`, `Resume`, `Adjust(delta)`, `Subscribe(ctx)`; it dials per request (5 s timeout), and an absent or refusing socket is `ErrNotRunning` with the path in the message. Errors come back as `*RemoteError` whose `errors.Is` matches `ErrNothingToStop`, `ErrNoTimer`, `ErrNotPaused`, `ErrNoSession` and the new `ErrInvalid` (bad levels or durations). `Worker.Subscribe()` is the fan-out behind it (buffer of 64 per subscriber; a subscriber that falls behind misses decisions instead of stalling the pipeline). Unix socket paths are limited to about 100 characters on macOS, so `~/.config/shh/shh.sock` is fine but deep temp directories are not (tests use a short temp dir). A level change made over IPC starts or stops the suppressor like any other.

### 27 · CLI commands
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/27-cli` · Depends: 26
- Read: `manifest.md` §4, `plan.md` §3.1, §9
- Build: kong CLI: bare `shh` opens the TUI (see 31; prints `status` instead when stdout is not a terminal), `focus|f`, `stop [--all]`, `baseline`, `status [--json|--short]`, `pause`, `resume`, flags (`-t -f -m --then/--clear`), positional note. Commands talk to the worker through IPC and print results. Starting a session with no worker spawns one (spawn mechanics come in 28; use an injectable spawner here).
- Tests (in-process worker + temp socket, drive the CLI entrypoint with args): the manifest syntax examples parse and apply; `status --short` format; stop semantics; invalid input exits non-zero with a clear message.
- Manual check: (after 28) `shh f -t 1m -f 65%` then `shh status`.
- Notes: the CLI lives in `internal/cli` (`cli.Run(args, cli.Env) int`, tested through the real entrypoint against an in-process worker and socket) and `cmd/shh/main.go` only builds the real `Env` (socket, config dir and label from the config, terminal check) and exits with the code. **`cli.Env`** carries `Stdout`, `Stderr`, `SocketPath`, `ConfigDir`, `Version`, `Label`, `IsTerminal`, `TUI func() error` (nil for now; task 31 sets it), `Spawn func() error` (injectable; in `main` it is a placeholder that fails with "shh cannot start its worker yet" until task 28 replaces it with the real `shh daemon` re-exec) and `SpawnWait` (5 s). Commands: `focus|f` (`-t/--timer` default 0 = until stopped, `-f/--focus` default `medium`, `-m/--note`, `--then`/`--clear`, trailing words as the note; giving both `-m` and words is an error), `stop|s [--all]`, `baseline <level|off>`, `status [--json|--short]`, `pause`, `resume`, `replay <file> [-t]`, and `ui` (the default command, so bare `shh`: opens the UI when stdout is a terminal and `Env.TUI` is set, otherwise prints the status). `focus` and `baseline` start a worker through `Spawn` when none answers (then wait for the socket); the rest never spawn. **Exit codes:** 0 success, 1 failure while doing the work (including "shh is not running" for `pause`/`resume`/`focus` without a spawner and "nothing to stop"), 2 for input that could not be used (bad percent, duration or flag; the message names the flag and what to write). `status` with no worker prints `Focus is off (shh is not running)` (exit 0, `--json` has `"running": false`) and `stop` says there is nothing to stop (exit 0), so scripts and prompts stay quiet. **Output:** every mutating command prints the new status: `Focus is on at 90% (deep)` then one line per layer (`escalation  90% (deep)  38m left, then off  "note"`, `until stopped`, `paused with 20m left`). `--short` is `shh 38m · 90%`, `shh 30%` or `shh ⏸ 75%` (the label is `[menubar] label`), empty when focus is off; countdowns read `30s`, `38m` (rounded up), `2h`, `1h05m`. Added `decide.FormatLevel` (`65% (medium)`, `75%`, `off`). Not built here: `ls`, `train`, `doctor`, `init`, `rules review`, `capture`, `purge` (task 36/37).

### 28 · Worker lifecycle: spawn, PID file, graceful stop, re-arm
- [x] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/28-lifecycle` · Depends: 27, 02
- Read: `plan.md` §3, `docs/spikes/02-focus.md`
- Build: detached `shh daemon` re-exec, PID file, single-instance lock, graceful stop (stops the suppressor and restores any parked banner window to 0,0), exit when effective level is 0, re-arm a persisted baseline on any command; real macOS `Suppressor` from spike 02 (a persistent `osascript -l JavaScript` watcher polling about every 50 ms, based on `spikes/focus-ax/ax_filter.js`, leaving `[shh]` banners alone; restart the watcher when the NotificationCenter PID changes and on wake from sleep) with its fake.
- Tests: second start doesn't spawn a second worker; stale PID file recovered; stop stops the suppressor via the fake; worker exits at level 0; persisted baseline re-armed. Real spawn is covered by the manual check.
- Manual check: `shh baseline 30%` starts a background process (`ps`), banners from other apps are hidden (a flash under half a second is OK) while the DB still records them, `shh baseline off` ends it and banners show normally again.
- Notes: **Daemon core** `session.RunDaemon(ctx, session.DaemonConfig{Worker, Clock, SocketPath, LockPath, PIDPath, IdleGrace, OnError})` in `internal/session/daemon.go` and `lock.go`: takes an exclusive `flock` on `shh.lock` (the OS drops it when the process dies, so a crash never leaves a lock behind; a second daemon gets `ErrAlreadyRunning`), writes `shh.pid` (informational: a stale PID file or socket is simply replaced), serves the socket, runs the worker, and exits cleanly (suppressor stopped, socket and PID file removed) when its context ends or the effective level has been 0 for `IdleGrace` (3 s by default: long enough for a freshly spawned daemon to receive the command that started it, short enough that `shh baseline off` ends the process within seconds). `Worker.Init` now starts the suppressor straight away when a level is already persisted. **Real suppressor** `suppress.NewAccessibility(AccessibilityConfig{OwnPrefix, RestartDelay, OnError})` in `internal/suppress/accessibility.go` with the embedded `watch.js`: runs the spike's watcher as a supervised `osascript -l JavaScript` child (relaunched if it dies; the `[shh]` prefix is passed in `SHH_OWN_PREFIX`); `Start` launches the first process synchronously so errors surface; `Stop` signals it (SIGTERM, kill after 3 s) and runs a restore script that moves any parked window back to 0,0. The script also exits, restoring banners, as soon as its parent process changes, so a `kill -9` of the daemon cannot leave banners hidden for good (found in the manual check: the first version orphaned its watcher). **CLI:** hidden `shh daemon` (runs `Env.Daemon`); `Env.Spawn` re-execs the binary as a detached (`setsid`) `shh daemon` logging to `<config dir>/shh.log`; **any command except `daemon` and `replay` first re-arms persisted focus**: if `Env.PersistedFocus()` says the database holds an active level and no worker answers, it spawns one (so after a reboot any `shh` command, even `shh status`, resumes the baseline; a failure is reported but does not block the command). `alert.NewLog` (one line per alert in the log) stands in as the daemon's alerter until task 29. No backfill at daemon start (the tailer's 30 s lookback covers the spawn gap, and anything delivered while no daemon ran was shown by macOS itself). **Manual check, done against the real notification database with a temporary `HOME`** (your real `Library` symlinked in; nothing under your real `~/.config/shh` was touched): `shh baseline 30%` spawned a detached daemon plus one watcher (`ps`), files mode `0600`; a posted test notification had its Notification Center window parked at `[6000, 6000]` 0.7 s after delivery until the banner expired, and was recorded and classified (`blocked`, score 11, threshold 30, with a reason); the watcher uses about 1.2% CPU; a second `shh baseline` did not start a second daemon; after `kill -9` of the daemon (once the orphan bug was fixed) no watcher was left and the next command re-armed the persisted baseline with one daemon and one watcher; `SIGTERM` with a banner parked put the window back to `[0, 0]`; `shh baseline off` ended the daemon within the grace period and removed the socket and PID file.

### 29 · Alerter implementation and config
- [ ] Done
- Session: `claude --resume 433ae5cd-acfb-43bf-a0ab-1a88ad3d57d4`
- Branch: `task/29-alerter` · Depends: 03, 10, 25
- Read: `docs/spikes/03-banner.md`, `plan.md` §7
- Build: real alerter following the spike's recommendation with config (size, color, position, duration, sound, per-kind/per-source overrides); `osascript` fallback whose title starts with `[shh]` so the suppressor leaves it visible; click opens the source app; `shh alert test`.
- Tests: style resolution (defaults → kind → source override) produces the expected request to a fake backend; fallback chosen when helper missing.
- Manual check: `shh alert test` shows styled banners; changing `config.toml` changes them.

### 30 · Menu bar process
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/30-menubar` · Depends: 04, 26
- Read: `docs/spikes/04-menubar.md`, `plan.md` §7.1
- Build: `shh menubar` launched by the worker and exiting with it, driving a persistent JXA `NSStatusItem` helper over JSON lines (stdin: title and menu entries, stdout: clicks; see spike 04); shows `label` + time left + effective level; paused indicator; menu items for the stack, pause/resume, ±5m, stop, open TUI.
- Tests: title/menu formatting from a status payload (table: baseline only, escalation with time left, paused, indefinite, off). Tray rendering itself is manual.
- Manual check: start a 2m session and watch the countdown; click pause.

---

## Phase 7: TUI

### 31 · TUI skeleton and tabs
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/31-tui-skeleton` · Depends: 08, 26
- Read: `manifest.md` §5, `plan.md` §8
- Build: Bubble Tea app opened by bare `shh` (and `shh ui`) with three tabs: **Focus**, **Notifications**, **Train** (switch with `Tab`/`Shift-Tab` and `F1`–`F3`, so digit keys stay free for training labels). List rows show time, source, sender, score (0–100) and alerted/blocked; detail pane beside it. Status bar with session info from IPC (works without a worker too), `j/k/h/l`, `?` help. Focus tab is a placeholder here (built in 34).
- Tests (drive the model with key messages, assert state/view text): tab switching; navigation bounds; Notifications shows only non-dismissed, Train only untrained; status bar shows time left.
- Manual check: run `shh` against some stored rows; keys behave.

### 32 · TUI: preview/expand, read state, open in app
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/32-tui-read` · Depends: 31
- Read: `plan.md` §6, §8
- Build: truncated preview filling the pane; `e` expands/collapses without changing read state; `Enter`/`o` opens the original app and marks read; dismiss `d`/`x`.
- Tests: expanding never sets `is_read`; opening does; dismiss hides from Notifications but not Train; long text truncates to pane size.
- Manual check: run `shh`, expand a long message, confirm it stays unread; open one, confirm it flips.

### 33 · TUI: training with `y`/`n` and undo
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/33-tui-train` · Depends: 19, 22, 32
- Read: `manifest.md` §5.1–5.2, `plan.md` §5.3, §8
- Build: Train tab keys per `plan.md` §8: `y` and `n` (quick constraint labels using the row's `threshold_at_decision`), `l` opens a prompt for an exact 0–100 label or tier name. Each sets `is_trained`, `user_feedback`/`user_label`, trains the model, removes the row from the Train list (also archives it from Notifications when trained from there, per manifest); `u` undo; every Train row shows the score the model gave (0–100) and its status (alerted/blocked) in the list, and the detail pane shows score, boost, `threshold_at_decision` and reason; Train list ordered by most informative first.
- Tests: exact label then a similar new item scores closer to that label; `y`/`n` follow the constraint rules; undo restores flags and weights; invalid label input shows an error and changes nothing; ordering puts scores near common thresholds first; the rendered list and detail contain the stored score and threshold for each row.
- Manual check: label ten real items with a mix of `y`, `n` and `l`, then see scores change in `shh replay`.

### 34 · TUI: Focus tab
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/34-tui-focus` · Depends: 31, 26
- Read: `manifest.md` §5.2, `plan.md` §3.1, §8
- Build: the Focus tab manages everything the CLI does: shows the level stack and time left; start a focus (level, duration, note, `then`), set/clear baseline, stop / stop all, pause/resume (`p`/`Space`), adjust (`+`/`-`), change level (`f`). Inputs via small prompts accepting percent or tier names. Filter `/` on the other tabs.
- Tests: each action sends the right IPC command and the view updates; invalid input shows an error and sends nothing; with no worker running, starting a focus spawns one (injectable spawner).
- Manual check: from `shh`, start a 2m focus, pause, set a baseline, stop, and see the menu bar follow.

---

## Phase 8: Extras

### 35 · Summarizer backends
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/35-summarizer` · Depends: 10, 32
- Read: `plan.md` §6
- Build: `Summarizer` interface with `none` (default), `ollama`, `claude` (key from env/Keychain) backends; `s` in TUI; cache in `summary`; status bar shows when a remote backend is active.
- Tests: `httptest` fake Ollama/Claude servers; `none` shows a hint; results are cached; backend errors don't break the TUI.
- Manual check: with Ollama running, `s` summarizes a notification.

### 36 · CLI: ls, train, init, rules review, doctor, capture
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/36-cli-extras` · Depends: 17, 21, 23, 27
- Read: `plan.md` §9, §5.3
- Build: `ls` (read-only, `--source --since --status --min-score`); `train` (batch, `--reset|--rollback|--export|--import`; exports contain private text, so default to `private/` and warn on any other path); `init` (history bootstrap import, Accessibility permission check, DB check); `rules review` (unparsed calendar rows); `doctor` (embedding model present and sha256 ok, DB path, notification permissions for apps seen, Accessibility permission, suppressor self-test, Outlook new-mail alert setting, alerter, menubar); `capture` (log raw notifications for rule-building to a gitignored file).
- Tests: `ls` doesn't change flags; `doctor` reports each failing check against fakes; `init` imports history from the fake source; export/import round trip.
- Manual check: `shh doctor` on your Mac gives a clear checklist.

### 37 · TTL cleanup and purge
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/37-ttl` · Depends: 08, 06
- Read: `plan.md` §10 (TTL fix)
- Build: cleanup on launch per the plan's rule; `shh purge` with confirmation.
- Tests (fake clock): rows purged/kept per the rule; untrained recent rows survive; purge requires confirmation.
- Manual check: none.

### 38 · Packaging and README
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/38-packaging` · Depends: 36
- Read: `plan.md` §12
- Build: release build target (a `Taskfile.yml` task, optionally using goreleaser) that fetches the model asset and builds with `-tags embedmodel`; a Taskfile task that publishes the quantized model as the `models-vN` release asset; README note that `go install` users need network once for `shh init`, README (install, permissions setup including Accessibility and Outlook's "Display new message alert on desktop", config examples), man-style `--help` text review.
- Tests: none.
- Manual check: a clean install from the built artifact follows the README successfully.

---

## Phase 9: Later (do after the above are merged and used for a while)

### 39 · Evaluate Laya/Jev judge offline
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/39-judge-eval` · Depends: 33
- Read: `plan.md` §5.2 (decision rule)
- Build: `docs/spikes/39-judge.md` comparing the core classifier against a Python Laya/Jev scoring run on your labeled set; recommend adopt or drop.
- Tests: none. Manual check: user reads the results.

### 40 · launchd install (survive reboot)
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/40-launchd` · Depends: 28
- Read: `plan.md` §3
- Build: `shh install|uninstall` writes/removes a LaunchAgent that runs the same daemon; baseline resumes at login.
- Tests: generated plist content; install/uninstall against a temp `LaunchAgents` dir.
- Manual check: log out/in, baseline resumes.

### 41 · Linux port
- [ ] Done
- Session: _(fill on start)_
- Branch: `task/41-linux` · Depends: 38
- Read: `plan.md` §11.1
- Build: D-Bus `NotificationSource`, Focus control, alerter, tray implementations behind the existing interfaces.
- Tests: via fakes and a fake D-Bus service. Manual check: on a Linux desktop.
