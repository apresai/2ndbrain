#!/bin/bash
# GUI Test Suite: Beta 4-Tab Workspace Navigation, Telemetry View, Settings, & Menus
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Beta UI Navigation & Telemetry ==="

TEST_VAULT=$(mktemp -d /tmp/sb-gui-ui.XXXXXX)
cleanup() {
    kill_app
    rm -rf "$TEST_VAULT" "$GUI_LOG_DIR"
}
trap cleanup EXIT

setup_temp_vault "$TEST_VAULT"
_=$(seed_note "$TEST_VAULT" "GUI Test Note" "Sample note for navigation verification.")

launch_app_on_vault "$TEST_VAULT"
screenshot "ui-01-home"

assert_gui_action "app.launch" "succeeded" "App launch logged"
assert_gui_action "vault.open" "succeeded" "Vault open logged"

# --- Cmd+1..4 4-Tab Workspace Navigation ---
cmd_key "2"
wait_gui_state 'state.get("activeTab") == "Ask & Search"' 8
pass "Cmd+2 switched to Ask & Search tab"

cmd_key "3"
wait_gui_state 'state.get("activeTab") == "Notes"' 8
pass "Cmd+3 switched to Notes tab"

cmd_key "4"
wait_gui_state 'state.get("activeTab") == "Activity"' 8
pass "Cmd+4 switched to Activity tab"

cmd_key "1"
wait_gui_state 'state.get("activeTab") == "Home"' 8
pass "Cmd+1 switched back to Home tab"

assert_gui_action "nav.tab" "succeeded" "Tab navigation events recorded"

# --- Cmd+Shift+J opens Activity > Actions & Status ---
cmd_shift_key "j"
wait_gui_state 'state.get("activeTab") == "Activity" and state.get("activeSection") == "Actions & Status"' 8
screenshot "ui-02-actions-status"
pass "Cmd+Shift+J opened Activity > Actions & Status"

# --- Settings via AI menu ---
click_menu_item "AI" "Gemini Settings…"
assert_gui_action "settings.open" "succeeded" "AI > Gemini Settings… recorded settings.open"
screenshot "ui-03-settings"
cmd_key "w"
sleep 0.25

# --- Menu bar structure verification ---
MENU_JSON=$(inspect_menus)
if python3 - "$MENU_JSON" <<'PY'
import json, sys
data = json.loads(sys.argv[1])
top = set(data.get("top", []))
required_top = {"SecondBrain", "Vault", "View", "AI"}
if not required_top.issubset(top):
    print(f"missing top menus: {required_top - top} in {sorted(top)}", file=sys.stderr)
    sys.exit(1)
vault_items = set(data.get("menus", {}).get("Vault", []))
required_vault = {"Sync Index", "Re-embed All…"}
if not required_vault.issubset(vault_items):
    print(f"missing Vault items: {required_vault - vault_items} in {sorted(vault_items)}", file=sys.stderr)
    sys.exit(1)
ai_items = set(data.get("menus", {}).get("AI", []))
required_ai = {
    "Ask & Search Vault…",
    "Search Vault…",
    "Quick Ask & Search Panel",
    "Polish Note with Gemini…",
    "Gemini Settings…",
}
if not required_ai.issubset(ai_items):
    print(f"missing AI items: {required_ai - ai_items} in {sorted(ai_items)}", file=sys.stderr)
    sys.exit(1)
PY
then
    pass "Menu bar has Vault/View/AI menus and expected Sync/Re-embed/Gemini actions"
else
    fail "Menu bar structure mismatch" "$MENU_JSON"
fi

assert_no_leaked_secrets
print_results
[ "$FAIL" -eq 0 ]
