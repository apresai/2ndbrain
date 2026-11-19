#!/bin/bash
# GUI Test Suite: Gemini Note Polish Preview, Apply, & Undo Flow (Cmd+Shift+P)
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Gemini Note Polish ==="

TEST_VAULT=$(mktemp -d /tmp/sb-gui-polish.XXXXXX)
cleanup() {
    kill_app
    rm -rf "$TEST_VAULT" "$GUI_LOG_DIR"
}
trap cleanup EXIT

setup_temp_vault "$TEST_VAULT"
_=$(seed_note "$TEST_VAULT" "Project Beacon" "Reference target note for wikilink suggestion.")
sleep 1
DOC_REL=$(seed_note "$TEST_VAULT" "GUI Polish Target" "this note have some gramatical mistakes that gemini can clean up clearly. also refer to Project Beacon.")
DOC_ABS="$TEST_VAULT/$DOC_REL"
env 2NB_TEST=1 "$CLI_BIN" index --vault "$TEST_VAULT" >/dev/null

launch_app_on_vault "$TEST_VAULT"

click_menu_item "AI" "Polish Note with Gemini…"
wait_gui_state 'state.get("activeTab") == "Notes" and state.get("activeSection") == "AI Polish"' 10
screenshot "polish-01-opened"
pass "AI > Polish Note with Gemini… opened Notes > AI Polish"

if probe_gemini_capability "generation" "$TEST_VAULT"; then
    sleep 0.5
    select_polish_note "$DOC_REL"
    click_ax_button "polish.previewButton"
    for _ in $(seq 1 90); do
        if grep -q '"action":"polish.run"' "$GUI_LOG_DIR/gui-actions.jsonl" 2>/dev/null && \
           python3 -c 'import json,sys
for line in open(sys.argv[1]):
    ev=json.loads(line)
    if ev.get("action")=="polish.run" and ev.get("phase")=="succeeded":
        sys.exit(0)
sys.exit(1)' "$GUI_LOG_DIR/gui-actions.jsonl"; then
            break
        fi
        sleep 0.5
    done
    sleep 0.2
    screenshot "polish-02-preview"
    assert_gui_action "polish.run" "succeeded" "Gemini polish preview succeeded"

    if file_contains "$DOC_ABS" "gramatical mistakes"; then
        pass "Preview Polish left original note untouched on disk"
    else
        fail "Preview Polish unexpectedly modified disk" "$DOC_ABS"
    fi

    # Apply if the Apply button is enabled (changes were proposed)
    if click_ax_button "polish.applyButton" 2>/dev/null; then
        for _ in $(seq 1 90); do
            if python3 -c 'import json,sys
for line in open(sys.argv[1]):
    ev=json.loads(line)
    if ev.get("action")=="polish.run" and ev.get("phase")=="succeeded" and ev.get("metadata",{}).get("write")=="true":
        sys.exit(0)
sys.exit(1)' "$GUI_LOG_DIR/gui-actions.jsonl"; then
                break
            fi
            sleep 0.5
        done
        sleep 0.2
        screenshot "polish-03-applied"

        # Undo Last Polish and verify original prose returns
        click_ax_button "polish.undoButton"
        for _ in $(seq 1 30); do
            if python3 -c 'import json,sys
for line in open(sys.argv[1]):
    ev=json.loads(line)
    if ev.get("action")=="polish.undo" and ev.get("phase")=="succeeded":
        sys.exit(0)
sys.exit(1)' "$GUI_LOG_DIR/gui-actions.jsonl"; then
                break
            fi
            sleep 0.25
        done
        if file_contains "$DOC_ABS" "gramatical mistakes"; then
            pass "Apply + Undo Last Polish restored original content cleanly"
        else
            fail "Undo did not restore original content" "$DOC_ABS"
        fi
    else
        echo "Note: Apply button disabled (preview proposed zero diff changes)"
    fi
else
    echo "SKIP: Live Note Polish preview/apply (gemini-4-hydrogen generation probe unavailable)"
fi

assert_no_leaked_secrets
print_results
[ "$FAIL" -eq 0 ]
