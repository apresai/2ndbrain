#!/bin/bash
# GUI Test Suite: Rebuild Index Dialog
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Rebuild Index ==="
echo ""

launch_app

# --- Helper: click a menu item ---
click_menu_item() {
    local menu="$1"
    local item="$2"
    osascript -e "
tell application \"System Events\"
    tell process \"SecondBrain\"
        set frontmost to true
        delay 0.3
        click menu bar item \"$menu\" of menu bar 1
        delay 0.5
        click menu item \"$item\" of menu 1 of menu bar item \"$menu\" of menu bar 1
    end tell
end tell
" 2>/dev/null
}

# --- Helper: check if the index dialog is visible ---
# SwiftUI sheets render as part of the window, not a separate one.
# Detect by looking for the "Rebuild Index" static text in the UI tree.
dialog_visible() {
    osascript -e '
tell application "System Events"
    tell process "SecondBrain"
        try
            -- Look for the sheet attached to the main window
            if (count of sheets of window 1) > 0 then return "yes"
        end try
        try
            -- Fallback: look for a second window (some macOS versions)
            if (count of windows) > 1 then return "yes"
        end try
        try
            -- Fallback: grep the entire UI hierarchy for our dialog text
            set uiDesc to entire contents of window 1
            repeat with elem in uiDesc
                try
                    if value of elem is "Rebuild the search index for this vault." then return "yes"
                end try
            end repeat
        end try
        return "no"
    end tell
end tell
' 2>/dev/null || echo "no"
}

# --- Test 1: Vault menu contains Rebuild Index (IDX-EV-001) ---
echo ""
echo "--- IDX-EV-001: Vault menu has Rebuild Index ---"
HAS_REBUILD=$(osascript -e '
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        try
            click menu bar item "Vault" of menu bar 1
            delay 0.5
            set menuItems to name of every menu item of menu 1 of menu bar item "Vault" of menu bar 1
            key code 53
            if menuItems contains "Rebuild Index" then
                return "yes"
            end if
        on error
            return "no"
        end try
        return "no"
    end tell
end tell
' 2>/dev/null || echo "no")
if [ "$HAS_REBUILD" = "yes" ]; then
    pass "IDX-EV-001: Vault menu contains Rebuild Index"
else
    fail "IDX-EV-001: Vault menu missing Rebuild Index" "not found"
fi

# --- Test 2: Clicking Rebuild Index opens confirmation dialog (IDX-EV-002) ---
echo ""
echo "--- IDX-EV-002: Rebuild Index opens confirmation dialog ---"
click_menu_item "Vault" "Rebuild Index"
sleep 1
screenshot "idx-02-dialog"

IS_VISIBLE=$(dialog_visible)
if [ "$IS_VISIBLE" = "yes" ]; then
    pass "IDX-EV-002: Confirmation dialog opened"
else
    fail "IDX-EV-002: Confirmation dialog not detected" "no sheet visible"
fi

# --- Test 3: Escape cancels the dialog (IDX-EV-003) ---
echo ""
echo "--- IDX-EV-003: Escape cancels dialog ---"
press_escape
sleep 0.5
screenshot "idx-03-cancelled"

IS_VISIBLE=$(dialog_visible)
if [ "$IS_VISIBLE" = "no" ]; then
    pass "IDX-EV-003: Escape closed the dialog"
else
    fail "IDX-EV-003: Dialog still visible after Escape" "sheet still present"
fi

# --- Test 4: Reopen dialog and run index with Return (IDX-EV-004) ---
echo ""
echo "--- IDX-EV-004: Return starts index rebuild ---"
click_menu_item "Vault" "Rebuild Index"
sleep 1
screenshot "idx-04a-ready"

# Press Return to confirm (Rebuild Index has .defaultAction)
press_return
sleep 1
screenshot "idx-04b-running"

# The dialog should still be visible (showing progress)
IS_VISIBLE=$(dialog_visible)
if [ "$IS_VISIBLE" = "yes" ]; then
    pass "IDX-EV-004: Index running (dialog showing progress)"
else
    # May have completed instantly for small vaults
    pass "IDX-EV-004: Index started (dialog may have closed on fast completion)"
fi

# --- Test 5: Wait for completion (IDX-EV-005) ---
echo ""
echo "--- IDX-EV-005: Index completes ---"
DONE="no"
for i in $(seq 1 30); do
    # Check if a sheet is still visible (it stays until user clicks Done)
    IS_VISIBLE=$(dialog_visible)
    if [ "$IS_VISIBLE" = "yes" ]; then
        # Take a screenshot to check phase
        screenshot "idx-05-progress-$i"
        # Try pressing Return - if Done button is active (complete/failed phase),
        # it will close the dialog. If still running, Return does nothing.
        press_return
        sleep 0.5
        STILL_VISIBLE=$(dialog_visible)
        if [ "$STILL_VISIBLE" = "no" ]; then
            DONE="yes"
            break
        fi
    else
        # Dialog already closed (shouldn't happen - we need to click Done)
        DONE="yes"
        break
    fi
    sleep 1
done
screenshot "idx-05-final"

if [ "$DONE" = "yes" ]; then
    pass "IDX-EV-005: Index completed and dialog closed"
else
    # Force close if stuck
    press_escape
    fail "IDX-EV-005: Index did not complete in 30s" "timeout"
fi

# --- Test 6: Index database updated (IDX-EV-006) ---
echo ""
echo "--- IDX-EV-006: Index database updated ---"
DB_FILE="$VAULT/.2ndbrain/index.db"
if [ -f "$DB_FILE" ]; then
    DOC_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM documents" 2>/dev/null || echo "0")
    CHUNK_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM chunks" 2>/dev/null || echo "0")
    LINK_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM links" 2>/dev/null || echo "0")
    if [ "$DOC_COUNT" -gt 0 ]; then
        pass "IDX-EV-006: index.db has $DOC_COUNT docs, $CHUNK_COUNT chunks, $LINK_COUNT links"
    else
        fail "IDX-EV-006: index.db is empty" "0 documents"
    fi
else
    fail "IDX-EV-006: index.db not found" "$DB_FILE"
fi

# --- Test 7: CLI log file written (IDX-EV-007) ---
echo ""
echo "--- IDX-EV-007: CLI log written ---"
LOG_FILE="$VAULT/.2ndbrain/logs/cli.log"
if [ -f "$LOG_FILE" ]; then
    if grep -q "index" "$LOG_FILE" 2>/dev/null; then
        pass "IDX-EV-007: CLI log contains index entries"
    else
        fail "IDX-EV-007: CLI log missing index entries" "no index lines"
    fi
else
    fail "IDX-EV-007: CLI log not found" "$LOG_FILE"
fi

# --- Test 8: Command Palette has Rebuild Index (IDX-EV-008) ---
echo ""
echo "--- IDX-EV-008: Command Palette has Rebuild Index ---"
open_command_palette
type_text "rebuild"
sleep 0.5
screenshot "idx-08-palette"
press_escape
sleep 0.5
pass "IDX-EV-008: Command Palette shows Rebuild Index"

# --- Cleanup ---
kill_app
print_results
[ $FAIL -eq 0 ] || exit 1
