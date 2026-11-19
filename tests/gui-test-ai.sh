#!/bin/bash
# GUI Test Suite: Ask Gemini RAG Flow (Cmd+Shift+K)
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Ask Gemini RAG ==="

TEST_VAULT=$(mktemp -d /tmp/sb-gui-ai.XXXXXX)
cleanup() {
    kill_app
    rm -rf "$TEST_VAULT" "$GUI_LOG_DIR"
}
trap cleanup EXIT

setup_temp_vault "$TEST_VAULT"
BEACON_PATH=$(seed_note "$TEST_VAULT" "Project Beacon Protocol" "Project Beacon uses delta-7 acoustic beacons operating on 433.92 MHz for underwater telemetry.")
env 2NB_TEST=1 "$CLI_BIN" index --vault "$TEST_VAULT" >/dev/null

launch_app_on_vault "$TEST_VAULT"

click_menu_item "AI" "Ask & Search Vault…"
wait_gui_state 'state.get("activeTab") == "Ask & Search" and state.get("askSearchMode") == "Ask Gemini"' 10
screenshot "ai-01-ask-tab"
pass "AI > Ask & Search Vault… opened Ask Gemini mode"

if probe_gemini_capability "generation" "$TEST_VAULT"; then
    set_field_text "askSearch.askField" "What frequency does Project Beacon operate on?" false
    click_ax_button "askSearch.sendButton"

    wait_gui_state 'state.get("lastAskStatus") == "done" and (state.get("lastAskSourcesCount") or 0) >= 1' 45
    screenshot "ai-02-ask-answered"
    assert_gui_action "ask.query" "succeeded" "Ask Gemini completed with cited sources ($BEACON_PATH)"
else
    echo "SKIP: Live Ask Gemini query (gemini-4-hydrogen generation probe unavailable)"
fi

assert_no_leaked_secrets
print_results
[ "$FAIL" -eq 0 ]
