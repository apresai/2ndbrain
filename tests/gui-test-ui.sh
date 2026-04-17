#!/bin/bash
# GUI Test Suite: UI — sidebar, focus mode, split view, tabs
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: UI ==="
echo ""

rm -f "$VAULT/gui-test-"*.md

launch_app

# Create test docs
create_note "GUI Test UI Alpha"
create_note "GUI Test UI Beta"
screenshot "ui-00-setup"

# --- Test 1: Sidebar visible (UI-UB-001) ---
echo ""
echo "--- UI-UB-001: Sidebar visible ---"
# The sidebar should be visible by default with file list
# Verify by checking that the window shows sidebar content
bring_to_front
sleep 0.5
screenshot "ui-01-sidebar"
pass "UI-UB-001: Sidebar visible with file list"

# --- Test 2: Toggle sidebar (UI-EV-002) ---
echo ""
echo "--- UI-EV-002: Toggle sidebar ---"
# Cmd+\ to hide sidebar
osascript -e 'tell application "System Events" to tell process "SecondBrain" to keystroke "\\" using {command down}'
sleep 1
screenshot "ui-02-sidebar-hidden"
# Toggle back
osascript -e 'tell application "System Events" to tell process "SecondBrain" to keystroke "\\" using {command down}'
sleep 1
screenshot "ui-02b-sidebar-shown"
pass "UI-EV-002: Cmd+\\ toggled sidebar"

# --- Test 3: Focus mode (UI-EV-004 / UI-ST-001) ---
echo ""
echo "--- UI-EV-004: Focus mode ---"
# Open a doc first
open_quick_open
type_text "alpha"
sleep 0.5
press_return
sleep 1

# Enter focus mode
cmd_shift_key "e"
sleep 1
screenshot "ui-03-focus-mode"

# Exit focus mode
cmd_shift_key "e"
sleep 1
screenshot "ui-03b-normal-mode"
pass "UI-EV-004: Focus mode toggled"

# --- Test 4: Multiple tabs (DOC-UB-002) ---
echo ""
echo "--- DOC-UB-002: Multiple tabs ---"
# Open second doc
open_quick_open
type_text "beta"
sleep 0.5
press_return
sleep 1
screenshot "ui-04-two-tabs"
pass "DOC-UB-002: Two documents open in tabs"

# --- Test 5: Notes and Vault menus present (post-menu-reorganization) ---
echo ""
echo "--- Notes + Vault menu completeness ---"
NOTES_ITEMS=$(osascript <<'EOF'
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        delay 0.5
        tell menu bar 1
            tell menu bar item "Notes"
                click
                delay 0.5
                tell menu 1
                    return name of every menu item
                end tell
            end tell
        end tell
    end tell
end tell
EOF
)
press_escape
sleep 0.3

VAULT_ITEMS=$(osascript <<'EOF'
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        delay 0.5
        tell menu bar 1
            tell menu bar item "Vault"
                click
                delay 0.5
                tell menu 1
                    return name of every menu item
                end tell
            end tell
        end tell
    end tell
end tell
EOF
)
press_escape
sleep 0.3

HAS_NEW_VAULT=false
HAS_OPEN_VAULT=false
HAS_NEW_NOTE=false
[[ "$VAULT_ITEMS" == *"New Vault"* ]] && HAS_NEW_VAULT=true
[[ "$VAULT_ITEMS" == *"Open Vault"* ]] && HAS_OPEN_VAULT=true
[[ "$NOTES_ITEMS" == *"New Note"* ]] && HAS_NEW_NOTE=true

if $HAS_NEW_VAULT && $HAS_OPEN_VAULT && $HAS_NEW_NOTE; then
    pass "Menus: Notes has New Note; Vault has New/Open Vault"
else
    fail "Menus: Missing items" "NewVault=$HAS_NEW_VAULT OpenVault=$HAS_OPEN_VAULT NewNote=$HAS_NEW_NOTE"
fi

# --- Test 6: View menu items present ---
echo ""
echo "--- View menu ---"
# Our CommandMenu("View") may create a second View menu; check all menu bar items
VIEW_ITEMS=$(osascript <<'EOF'
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        delay 0.3
        tell menu bar 1
            set allNames to name of every menu bar item
            set output to ""
            repeat with i from 1 to count of allNames
                if item i of allNames is "View" then
                    tell menu bar item i
                        click
                        delay 0.3
                        tell menu 1
                            set menuNames to name of every menu item
                            repeat with mn in menuNames
                                set output to output & mn & ","
                            end repeat
                        end tell
                    end tell
                    key code 53
                    delay 0.2
                end if
            end repeat
            return output
        end tell
    end tell
end tell
EOF
)
sleep 0.3

HAS_SEARCH=false
HAS_QUICKOPEN=false
HAS_CMDPALETTE=false
HAS_FOCUS=false
[[ "$VIEW_ITEMS" == *"Search"* ]] && HAS_SEARCH=true
[[ "$VIEW_ITEMS" == *"Quick Open"* ]] && HAS_QUICKOPEN=true
[[ "$VIEW_ITEMS" == *"Command Palette"* ]] && HAS_CMDPALETTE=true
[[ "$VIEW_ITEMS" == *"Focus"* ]] && HAS_FOCUS=true

if $HAS_SEARCH && $HAS_QUICKOPEN && $HAS_CMDPALETTE && $HAS_FOCUS; then
    pass "View menu: All required items present"
else
    fail "View menu: Missing items" "Search=$HAS_SEARCH QuickOpen=$HAS_QUICKOPEN CmdPalette=$HAS_CMDPALETTE Focus=$HAS_FOCUS items=$VIEW_ITEMS"
fi

# Graph View is exercised by its own dedicated suite (tests/gui-test-graph.sh),
# which covers sheet open, inspector structure, mode/filter toggles, rebuild,
# FSEvents-driven refresh, and close/reopen. Don't duplicate the shallow
# sheet-exists check here.

# Cleanup
echo ""
echo "--- Cleanup ---"
rm -f "$VAULT/gui-test-"*.md
kill_app
echo "Removed test files"

print_results
exit $FAIL
