# Process: how to complete a task

Read this fully before touching code. Tasks live in `checklist.md`. Design lives in `plan.md` (the source of truth for behavior) and `manifest.md` (original spec; `plan.md` wins on conflict).

## 1. Start
1. Pick the first unchecked task in `checklist.md` whose dependencies are all checked **and merged to `master`**. If a dependency is not merged, stop and tell the user.
2. On `master`, write your session into the task's `Session:` line (`claude --resume <session-id>`) and commit `start task NN`. The session id is the file name (without `.jsonl`) of the most recently modified `*.jsonl` in `~/.claude/projects/<cwd with every "/" replaced by "-">/` (for example, cwd `/Users/me/shh` → `-Users-me-shh`). If you can't determine it, ask the user.
3. Create the branch named in the task (`task/NN-slug`) from `master`.
4. Read the task's `Read:` files and the `plan.md` sections it names. Do not read everything else.

## 2. Work
- **Stay in scope.** Do only what the task lists. If you find extra work, append a new task to `checklist.md` (in order, with dependencies) instead of doing it. If a decision in `plan.md` changes, update `plan.md` in the same branch.
- **TDD where applicable.** Write the failing tests first, then the code. Some tasks (spikes, wiring, UI polish) have no sensible tests; the task says so.
- **Don't break anything.** `task check` (gofmt check, `go vet`, `go test`) must be green before every commit. Never delete or weaken an existing test to make it pass. A contract change needs the user's approval.

## 3. Code style
- Human readable and self-documenting: clear names, small packages, obvious flow.
- Minimal comments. Comment only *why* something is done instead of the obvious alternative. Never narrate what the code does.
- Reuse where it makes sense. No one-off helpers, and no functions that wrap a few lines used once; inline them. Extract only when logic is shared or the abstraction is a real boundary.
- **Everything runnable is a task in `Taskfile.yml`** ([taskfile.dev](https://taskfile.dev)): build, test, spike runners, codegen, release. No one-off shell scripts, Makefile targets or undocumented commands. Not every checklist task needs a Taskfile task: add one only for a command you or the user will run repeatedly (build, test, data-capture or spike runners, release), with a `desc`, and mention it in the checklist task's `Notes:`.
- Follow `plan.md` §11 package layout. Platform-specific code goes behind the interfaces in §11.1.
- Pure Go where sensible. No CGo without the task saying so.
- Never commit private data: real notification text, labels, training exports, local databases, secrets. Anything derived from real notifications lives under `private/` (gitignored). Committed fixtures (`testdata/`) and the seed training set must be synthetic or fully scrubbed. Check `git status` before committing; if in doubt, don't add the file.

## 4. Tests
- **Table-driven, user-style, integration-leaning.** Drive public contracts (CLI commands, worker/IPC calls, TUI key presses, store queries via the public API) and assert observable results. Avoid testing internals, so refactors don't break tests.
- **Fully offline and self-contained.** Tests touch nothing outside the test process: no network, no real notification DB, no real Focus/Shortcuts/osascript, no real `HOME`/config (use `t.TempDir()` and in-memory SQLite). They must pass with the network off.
- **Fakes over mocks.** For each external system a test needs, build a small in-memory fake service with real behavior, in `internal/testkit`: if the real thing can create, change and delete state, the fake can too and tests observe that state. Examples: fake notification source (append/remove records), fake Focus controller (on/off state), fake alerter (records shown banners), fake clock (advance time), `httptest` servers for Ollama/Claude. Build only what the current tasks need. Do not mock individual functions.
- Time always comes from the injected clock, never `time.Now()` in logic.
- Each task lists the cases its tests must cover. Add more if you find gaps.

## 5. Finish
1. Run the task's **Manual check** yourself when possible (spikes and UI tasks often need the user; say so).
2. `task check` green.
3. Tick the task's checkbox in the final commit. Fill in anything the next task needs to know under the task's `Notes:`.
4. Commit in small, meaningful commits. End each commit message with the attribution lines the harness specifies.
5. Do not merge. Tell the user the branch is ready, summarize what changed, and list the manual check.

## 6. Spike tasks
Spikes answer a question. Write findings and the decision to `docs/spikes/NN-name.md` (what you tried, what worked, what didn't, the chosen approach, and any numbers). Keep throwaway code under `spikes/` or delete it. If the answer contradicts `plan.md`, update `plan.md` and tell the user before continuing.
