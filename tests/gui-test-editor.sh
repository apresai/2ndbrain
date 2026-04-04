#!/bin/bash
# GUI Test Suite: Editor — undo/redo, formatting, save, preview
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Editor ==="
echo ""

rm -f "$VAULT/gui-test-"*.md

launch_app

# Create a test note and open it
create_note "GUI Test Editor"
sleep 1

# Open the note via Quick Open
open_quick_open
type_text "gui test editor"
sleep 0.5
press_return
sleep 1
# Dismiss Quick Open if still showing
close_quick_open 2>/dev/null || true
sleep 0.5
screenshot "edit-00-opened"

# --- Test 1: Type text into editor ---
echo ""
echo "--- Type text ---"
# Click in the editor text area (right side of window, middle height)
bring_to_front
sleep 0.5
# Get window bounds and click center-right area (the editor pane)
osascript <<'EOF'
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        delay 0.3
        tell window 1
            set {x, y, w, h} to {0, 0, 0, 0}
            try
                set winPos to position
                set winSize to size
                set x to (item 1 of winPos) + (item 1 of winSize) * 0.6
                set y to (item 2 of winPos) + (item 2 of winSize) * 0.5
            end try
        end tell
        click at {x, y}
        delay 0.5
    end tell
end tell
EOF
sleep 0.3
type_text "Hello from the test suite."
sleep 0.5
cmd_key "s"
sleep 1
screenshot "edit-01-typed"

if file_contains "$VAULT/gui-test-editor.md" "Hello from the test suite"; then
    pass "Editor: Typed text saved after Cmd+S"
else
    fail "Editor: Typed text not in file" "$(tail -3 "$VAULT/gui-test-editor.md")"
fi

# --- Test 2: Cmd+S saves (DOC-EV-003) ---
echo ""
echo "--- DOC-EV-003: Cmd+S saves ---"
BEFORE=$(stat -f %m "$VAULT/gui-test-editor.md")
type_text " More text."
sleep 0.5
cmd_key "s"
sleep 1
AFTER=$(stat -f %m "$VAULT/gui-test-editor.md")
if [ "$BEFORE" != "$AFTER" ]; then
    pass "DOC-EV-003: Cmd+S updated file on disk"
else
    fail "DOC-EV-003: File timestamp unchanged after Cmd+S" ""
fi
screenshot "edit-02-saved"

# --- Test 3: Cmd+Z undoes (EDT-EV-001) ---
echo ""
echo "--- EDT-EV-001: Undo ---"
CONTENT_BEFORE=$(cat "$VAULT/gui-test-editor.md")
cmd_key "z"
sleep 0.5
cmd_key "s"
sleep 1
CONTENT_AFTER=$(cat "$VAULT/gui-test-editor.md")
if [ "$CONTENT_BEFORE" != "$CONTENT_AFTER" ]; then
    pass "EDT-EV-001: Cmd+Z changed file content (undo worked)"
else
    fail "EDT-EV-001: Cmd+Z did not change content" ""
fi
screenshot "edit-03-undo"

# --- Test 4: Cmd+Shift+Z redoes (EDT-EV-002) ---
echo ""
echo "--- EDT-EV-002: Redo ---"
CONTENT_BEFORE=$(cat "$VAULT/gui-test-editor.md")
cmd_shift_key "z"
sleep 0.5
cmd_key "s"
sleep 1
CONTENT_AFTER=$(cat "$VAULT/gui-test-editor.md")
if [ "$CONTENT_BEFORE" != "$CONTENT_AFTER" ]; then
    pass "EDT-EV-002: Cmd+Shift+Z changed file content (redo worked)"
else
    fail "EDT-EV-002: Cmd+Shift+Z did not change content" ""
fi
screenshot "edit-04-redo"

# Cleanup
echo ""
echo "--- Cleanup ---"
rm -f "$VAULT/gui-test-"*.md
kill_app
echo "Removed test files"

print_results
exit $FAIL
