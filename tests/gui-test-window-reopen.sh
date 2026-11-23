#!/bin/bash
# GUI Test Suite: a closed dashboard window must come back.
#
# The app outlives its window in the menu bar. It used to have no path that
# recreated the window, so after a close the process was alive and looked
# hung: a Dock click, the menu bar "Open 2ndbrain" item and every menu command
# that routes into the dashboard all did nothing.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Window Reopen ==="

VAULT=$(mktemp -d /tmp/sb-vault-reopen.XXXXXX)

cleanup() {
    kill_app
    rm -rf "$VAULT" "$GUI_LOG_DIR"
}
trap cleanup EXIT

setup_temp_vault "$VAULT"
_=$(seed_note "$VAULT" "Reopen Fixture" "Window reopen fixture content.")

wait_window_state() {
    local want="$1"
    local deadline=$((SECONDS + 10))
    local got=""
    while [ $SECONDS -lt $deadline ]; do
        got=$(gui_ctl window_state 2>/dev/null || true)
        [ "$got" = "$want" ] && return 0
        sleep 0.2
    done
    echo "window_state: wanted '$want', got '$got'" >&2
    return 1
}

count_action() {
    python3 - "$GUI_LOG_DIR/gui-actions.jsonl" "$1" <<'PY'
import json, sys
n = 0
with open(sys.argv[1], encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if line and json.loads(line).get("action") == sys.argv[2]:
            n += 1
print(n)
PY
}

launch_app_on_vault "$VAULT"
if wait_window_state "dashboards=1 visible=1"; then
    pass "Dashboard window present at launch"
else
    fail "Dashboard window present at launch" "$(gui_ctl window_state || true)"
fi

# 1. Reopen event (Dock click, `open` on the running app).
gui_ctl close_main_window >/dev/null
if wait_window_state "dashboards=0 visible=0"; then
    pass "Closing the dashboard leaves the app running with no window"
else
    fail "Dashboard closed" "$(gui_ctl window_state || true)"
fi
open "$APP"
if wait_window_state "dashboards=1 visible=1"; then
    pass "Reopen event recreates the dashboard window"
else
    fail "Reopen event recreates the dashboard window" "$(gui_ctl window_state || true)"
fi

# 2. Menu bar item.
gui_ctl close_main_window >/dev/null
wait_window_state "dashboards=0 visible=0" || true
gui_ctl status_open_main_window >/dev/null
if wait_window_state "dashboards=1 visible=1"; then
    pass "Menu bar 'Open 2ndbrain' recreates the dashboard window"
else
    fail "Menu bar 'Open 2ndbrain' recreates the dashboard window" "$(gui_ctl window_state || true)"
fi

# 3. A menu command that routes into the dashboard, issued with no window.
gui_ctl close_main_window >/dev/null
wait_window_state "dashboards=0 visible=0" || true
cmd_key "3"
if wait_window_state "dashboards=1 visible=1" \
    && wait_gui_state 'state.get("activeTab") == "Notes"' 10; then
    pass "Cmd+3 with no window reopens the dashboard on Notes"
else
    fail "Cmd+3 with no window reopens the dashboard on Notes" "$(gui_ctl window_state || true)"
fi

# The reopened windows must not re-bind the vault: openVault resets the
# watcher, the timers and per-vault state.
OPENS=$(count_action "vault.open")
if [ "$OPENS" = "1" ]; then
    pass "Vault bound once across three reopens"
else
    fail "Vault bound once across three reopens" "vault.open recorded $OPENS times"
fi

# A second reopen with the window already present must not add a window.
open "$APP"
sleep 1
if [ "$(gui_ctl window_state)" = "dashboards=1 visible=1" ]; then
    pass "Reopen with a window present does not create a second one"
else
    fail "Reopen with a window present does not create a second one" "$(gui_ctl window_state || true)"
fi

screenshot "window-reopen-final"
print_results
[ "$FAIL" -eq 0 ]
