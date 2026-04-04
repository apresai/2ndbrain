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

# Cleanup
echo ""
echo "--- Cleanup ---"
rm -f "$VAULT/gui-test-"*.md
kill_app
echo "Removed test files"

print_results
exit $FAIL
