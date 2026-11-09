#!/usr/bin/env bash
# End-to-end check of routines against the real binary and a real worker, in a throwaway HOME (the real ~/.config/shh is never touched).
# The steps are at level "off" with no alerts and the menu bar is disabled, so nothing is hidden or shown on the screen. The real
# ~/Library is linked in only so the worker can find the notification database it reads.
set -u
cd "$(dirname "$0")/.."
go build -o "${TMPDIR:-/tmp}/shh-e2e" ./cmd/shh || exit 1
BIN="${TMPDIR:-/tmp}/shh-e2e"
HOME_DIR=$(mktemp -d)
trap 'HOME="$HOME_DIR" "$BIN" stop --all --end-routine >/dev/null 2>&1; rm -rf "$HOME_DIR" "$BIN"' EXIT
mkdir -p "$HOME_DIR/.config/shh"
ln -s "$HOME/Library" "$HOME_DIR/Library"
echo '{"menubar": {"enabled": false}}' > "$HOME_DIR/.config/shh/config.json"
cat > "$HOME_DIR/.config/shh/routines.json" <<'J'
{"routines": {
  "quick test": {
    "summary": ["ui"],
    "steps": [
      {"name": "one", "level": "off", "length": "3s"},
      {"name": "two", "level": "off", "length": "3s", "advance": "ask"},
      {"name": "three", "level": "off", "length": "3s"}
    ]
  }
}}
J
shh() { HOME="$HOME_DIR" "$BIN" "$@" 2>&1; }
fail=0
check() { # check <description> <text> <pattern>
  if grep -q -- "$3" <<<"$2"; then echo "ok   $1"; else echo "FAIL $1: wanted /$3/ in:"; sed 's/^/       /' <<<"$2"; fail=1; fi
}
wait_for() { # wait_for <pattern> [seconds]: poll the routine status until it matches
  for _ in $(seq 1 $(( ${2:-25} * 2 ))); do
    out=$(shh routine status); grep -q -- "$1" <<<"$out" && return 0; sleep 0.5
  done
  return 1
}

check "list shows the routine" "$(shh routine list)" "quick test"
check "a name with spaces needs no quotes" "$(shh routine run quick test)" "Started quick test: one"
check "status shows the first step" "$(shh routine status)" "quick test: one, step 1 of 3"
if wait_for "waiting to start three"; then echo "ok   the step that asks waits for the user"; else echo "FAIL never reached the waiting state"; shh routine status | sed 's/^/       /'; fail=1; fi
check "focus status shows the routine" "$(shh status)" "Routine: quick test"
check "next starts the waiting step" "$(shh routine next)" "quick test: three, step 3 of 3"
if wait_for "Last run: quick test finished"; then echo "ok   the routine finishes and is summarised"; else echo "FAIL never finished"; shh routine status | sed 's/^/       /'; fail=1; fi
check "the summary counts every step" "$(shh routine status)" "3 of 3 steps"
check "a second run can start after one finished" "$(shh routine run quick test)" "Started quick test"
check "a manual session is refused while it runs" "$(shh focus 90% 2>&1)" "routine is running"
check "stop ends it" "$(shh routine stop)" "Routine quick test stopped"
exit $fail
