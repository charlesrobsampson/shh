#!/usr/bin/env bash
# End-to-end check of triggers against the real binary and a real worker, in a throwaway HOME (the real ~/.config/shh is never touched).
# A routine with a trigger a minute or two ahead is started by the worker by itself. The step is at level "off", the summary is for the
# UI only and the menu bar is disabled, so nothing is hidden or shown on the screen. It takes up to about two and a half minutes.
set -u
cd "$(dirname "$0")/.."
go build -o "${TMPDIR:-/tmp}/shh-e2e-sched" ./cmd/shh || exit 1
BIN="${TMPDIR:-/tmp}/shh-e2e-sched"
HOME_DIR=$(mktemp -d)
DAEMON=""
trap '[ -n "$DAEMON" ] && kill "$DAEMON" 2>/dev/null; rm -rf "$HOME_DIR" "$BIN"' EXIT
mkdir -p "$HOME_DIR/.config/shh"
ln -s "$HOME/Library" "$HOME_DIR/Library"
echo '{"menubar": {"enabled": false}}' > "$HOME_DIR/.config/shh/config.json"
shh() { HOME="$HOME_DIR" "$BIN" "$@" 2>&1; }
fail=0
check() { # check <description> <text> <pattern>
  if grep -q -- "$3" <<<"$2"; then echo "ok   $1"; else echo "FAIL $1: wanted /$3/ in:"; sed 's/^/       /' <<<"$2"; fail=1; fi
}

# The trigger fires at the start of a minute: take the next one, or the one after when this one is nearly over.
if [ "$(date +%S)" -gt 40 ]; then ahead=2; else ahead=1; fi
at=$(date -v+${ahead}M +%H:%M)
cat > "$HOME_DIR/.config/shh/routines.json" <<J
{"routines": {"sched test": {
  "summary": ["ui"],
  "steps": ["off 10s one"],
  "triggers": ["daily at $at until 23:59"]
}}}
J
check "the list shows the trigger and its next start" "$(shh routine list)" "daily at $at until 23:59"
check "and warns that nothing is running yet" "$(shh routine list)" "worker is not running"

HOME="$HOME_DIR" "$BIN" daemon >/dev/null 2>&1 &
DAEMON=$!
sleep 2
check "the worker is up and lists the upcoming start" "$(shh routine status)" "sched test  .*(daily at $at until 23:59)"
check "no longer warns once the worker runs" "$(shh routine list | grep -c 'worker is not running')" "^0$"

echo "waiting for $at ..."
started=0
for _ in $(seq 1 330); do
  if grep -q "sched test" <<<"$(shh routine status | head -1)" && ! grep -q "No routine is running" <<<"$(shh routine status | head -1)"; then started=1; break; fi
  sleep 0.5
done
if [ "$started" = 1 ]; then echo "ok   the trigger started the routine by itself"; else echo "FAIL the routine never started"; shh routine status | sed 's/^/       /'; fail=1; fi

for _ in $(seq 1 60); do
  grep -q "Last run: sched test finished" <<<"$(shh routine status)" && break
  sleep 0.5
done
out=$(shh routine status)
check "it finishes by itself and says so" "$out" "Last run: sched test finished"
check "the next start is tomorrow" "$out" "sched test  tomorrow $at"
check "skip leaves tomorrow's start out" "$(shh routine skip sched test)" "Skipped the next start of sched test (tomorrow $at)"
check "status marks it skipped" "$(shh routine status)" "SKIPPED"
check "undo takes it back" "$(shh routine skip --undo sched test)" "back on: tomorrow $at"
exit $fail
