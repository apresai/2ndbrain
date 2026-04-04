#!/bin/bash
# GUI Test Suite: Create and Delete Notes
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: CRUD ==="
echo ""

# Clean test artifacts
rm -f "$VAULT/gui-test-"*.md

# Launch
launch_app
screenshot "crud-00-launch"

# --- Test 1: Vault auto-reopen (FST-EV-005) ---
echo ""
echo "--- FST-EV-005: Vault auto-reopen ---"
SAVED_PATH=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || echo "")
if [ "$SAVED_PATH" = "$VAULT" ] && [ -d "$VAULT/.2ndbrain" ]; then
    pass "FST-EV-005: Vault auto-reopened on launch"
else
    fail "FST-EV-005: Vault did not auto-reopen" "path=$SAVED_PATH"
fi
screenshot "crud-01-vault"

# --- Test 2: Create note (DOC-EV-001) ---
echo ""
echo "--- DOC-EV-001: Create note ---"
create_note "GUI Test Note One"
screenshot "crud-02-note"

if file_exists "$VAULT/gui-test-note-one.md"; then
    if file_contains "$VAULT/gui-test-note-one.md" "id:" && file_contains "$VAULT/gui-test-note-one.md" "type: note"; then
        pass "DOC-EV-001: Note created with UUID and type"
    else
        fail "DOC-EV-001: Missing frontmatter" "$(head -5 "$VAULT/gui-test-note-one.md")"
    fi
else
    fail "DOC-EV-001: File not created" "$VAULT/gui-test-note-one.md"
fi

# --- Test 3: Create ADR (DOC-EV-001 variant) ---
echo ""
echo "--- DOC-EV-001: Create ADR ---"
create_typed_note "GUI Test ADR" "Architecture Decision Record"
screenshot "crud-03-adr"

if file_exists "$VAULT/gui-test-adr.md"; then
    if file_contains "$VAULT/gui-test-adr.md" "type: adr"; then
        pass "DOC-EV-001-ADR: ADR created with correct type"
    else
        fail "DOC-EV-001-ADR: Wrong type" "$(grep type: "$VAULT/gui-test-adr.md")"
    fi
else
    fail "DOC-EV-001-ADR: File not created" "$VAULT/gui-test-adr.md"
fi

# --- Test 4: Create second note ---
echo ""
echo "--- Create second note ---"
create_note "GUI Test Note Two"
screenshot "crud-04-note2"

if file_exists "$VAULT/gui-test-note-two.md"; then
    pass "Create second note: File exists"
else
    fail "Create second note" "file not created"
fi

# --- Test 5: UUID uniqueness (AI-EV-002) ---
echo ""
echo "--- AI-EV-002: UUID uniqueness ---"
if file_exists "$VAULT/gui-test-note-one.md" && file_exists "$VAULT/gui-test-note-two.md"; then
    UUID1=$(grep "^id:" "$VAULT/gui-test-note-one.md" | head -1)
    UUID2=$(grep "^id:" "$VAULT/gui-test-note-two.md" | head -1)
    if [ "$UUID1" != "$UUID2" ] && [ -n "$UUID1" ] && [ -n "$UUID2" ]; then
        pass "AI-EV-002: Each doc has unique UUID"
    else
        fail "AI-EV-002: UUIDs not unique" "$UUID1 vs $UUID2"
    fi
else
    fail "AI-EV-002: Can't check UUIDs" "files missing"
fi

# --- Test 6: Quick Open (SRC-EV-004) ---
echo ""
echo "--- SRC-EV-004: Quick Open ---"
open_quick_open
type_text "gui test note one"
sleep 0.5
screenshot "crud-05-quickopen"
press_return
sleep 1
screenshot "crud-05b-opened"
pass "SRC-EV-004: Quick Open filtered and opened document"

# --- Test 7: Delete note ---
echo ""
echo "--- Delete note ---"
rm "$VAULT/gui-test-note-two.md"
sleep 1
# Refresh via Command Palette
open_command_palette
type_text "refresh"
sleep 0.5
press_return
sleep 1
close_command_palette 2>/dev/null
screenshot "crud-06-deleted"

if [ ! -f "$VAULT/gui-test-note-two.md" ]; then
    pass "Delete note: File removed"
else
    fail "Delete note" "file still exists"
fi

# --- Test 8: Vault state ---
echo ""
echo "--- Vault state consistency ---"
REMAINING=$(ls "$VAULT"/gui-test-*.md 2>/dev/null | wc -l | tr -d ' ')
if [ "$REMAINING" -eq 2 ]; then
    pass "Vault state: 2 test files remain"
else
    fail "Vault state" "expected 2, found $REMAINING"
fi

# --- Test 9: Create with invalid title ---
echo ""
echo "--- CLI-UW-004: Invalid title rejection ---"
INVALID_RESULT=$(2nb create -- "-bad-title" 2>&1 || true)
if echo "$INVALID_RESULT" | grep -q "cannot start with a dash"; then
    pass "CLI-UW-004: Dash-prefixed title rejected"
else
    fail "CLI-UW-004: Should reject dash title" "$INVALID_RESULT"
fi

# --- Test 10: Quit + relaunch persistence (FST-EV-005) ---
echo ""
echo "--- FST-EV-005: Quit + relaunch ---"
kill_app
open "$APP"
wait_for_app
sleep 2
screenshot "crud-07-relaunch"
# Verify vault path is still set
SAVED_PATH=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || echo "")
if [ "$SAVED_PATH" = "$VAULT" ]; then
    pass "FST-EV-005: Vault path persisted across relaunch"
else
    fail "FST-EV-005: Vault path not persisted" "got: $SAVED_PATH"
fi

# Cleanup
echo ""
echo "--- Cleanup ---"
rm -f "$VAULT/gui-test-"*.md
kill_app
echo "Removed test files"

print_results
exit $FAIL
