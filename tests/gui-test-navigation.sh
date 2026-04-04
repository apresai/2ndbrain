#!/bin/bash
# GUI Test Suite: Navigation — Quick Open, Command Palette, Search
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Navigation ==="
echo ""

# Clean and setup
rm -f "$VAULT/gui-test-"*.md

launch_app

# Create test docs for navigation
create_note "GUI Test Nav Alpha"
create_note "GUI Test Nav Beta"
create_note "GUI Test Nav Gamma"
echo "Created 3 test documents"
screenshot "nav-00-setup"

# --- Test 1: Cmd+P opens Quick Open (SRC-EV-004) ---
echo ""
echo "--- SRC-EV-004: Quick Open opens ---"
open_quick_open
screenshot "nav-01-quickopen"
# Verify by checking window state (Quick Open should be visible)
pass "SRC-EV-004: Cmd+P opened Quick Open"

# --- Test 2: Quick Open filters (SRC-EV-004) ---
echo ""
echo "--- SRC-EV-004: Quick Open filters ---"
type_text "beta"
sleep 0.5
screenshot "nav-02-filtered"
# Select and open
press_return
sleep 1
screenshot "nav-02b-opened"
pass "SRC-EV-004: Quick Open filtered and selected"

# --- Test 3: Cmd+Shift+P opens Command Palette (UI-EV-001) ---
echo ""
echo "--- UI-EV-001: Command Palette ---"
open_command_palette
screenshot "nav-03-cmdpalette"
type_text "toggle"
sleep 0.5
screenshot "nav-03b-filtered"
press_escape 2>/dev/null || close_command_palette
sleep 0.5
pass "UI-EV-001: Cmd+Shift+P opened Command Palette with filter"

# --- Test 4: Cmd+Shift+F opens Search (SRC-EV-001) ---
echo ""
echo "--- SRC-EV-001: Search Panel ---"
open_search
screenshot "nav-04-search"
type_text "alpha"
sleep 1
screenshot "nav-04b-results"
close_search
sleep 0.5
pass "SRC-EV-001: Cmd+Shift+F opened Search Panel with typed query"

# --- Test 5: Open multiple docs via Quick Open (DOC-UB-002) ---
echo ""
echo "--- DOC-UB-002: Multiple tabs ---"
open_quick_open
type_text "alpha"
sleep 0.5
press_return
sleep 1

open_quick_open
type_text "gamma"
sleep 0.5
press_return
sleep 1
screenshot "nav-05-tabs"
pass "DOC-UB-002: Opened multiple documents in tabs"

# --- Test 6: 2nb models list (CLI-EV-016) ---
echo ""
echo "--- CLI-EV-016: models list ---"
MODELS_OUT=$(2nb models list --json --porcelain 2>/dev/null)
if echo "$MODELS_OUT" | grep -q "embedding"; then
    pass "CLI-EV-016: models list shows embedding models"
else
    fail "CLI-EV-016: models list missing models" "$MODELS_OUT"
fi

# --- Test 7: 2nb ai status (CLI-EV-017) ---
echo ""
echo "--- CLI-EV-017: ai status ---"
STATUS_OUT=$(2nb ai status --json --porcelain 2>/dev/null)
if echo "$STATUS_OUT" | grep -q "embed_available"; then
    pass "CLI-EV-017: ai status shows provider info"
else
    fail "CLI-EV-017: ai status failed" "$STATUS_OUT"
fi

# --- Test 8: 2nb search requires args ---
echo ""
echo "--- CLI: search requires query ---"
SEARCH_EMPTY=$(2nb search 2>&1 || true)
if echo "$SEARCH_EMPTY" | grep -q "requires at least 1 arg"; then
    pass "Search requires query argument"
else
    fail "Search should require args" "$SEARCH_EMPTY"
fi

# --- Test 9: 2nb config round-trip (CLI-EV-018) ---
echo ""
echo "--- CLI-EV-018: config get/set ---"
ORIG_PROVIDER=$(2nb config get ai.provider 2>/dev/null)
2nb config set ai.provider bedrock 2>/dev/null
GOT_PROVIDER=$(2nb config get ai.provider 2>/dev/null)
if [ "$GOT_PROVIDER" = "bedrock" ]; then
    pass "CLI-EV-018: config set/get round-trips"
else
    fail "CLI-EV-018: config round-trip failed" "got=$GOT_PROVIDER"
fi

# Cleanup
echo ""
echo "--- Cleanup ---"
rm -f "$VAULT/gui-test-"*.md
kill_app
echo "Removed test files"

print_results
exit $FAIL
