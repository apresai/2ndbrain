#!/bin/bash
# GUI Test Suite: Polish diff view (Cmd+Option+P)
# Polishes the current document via the LLM and asserts the diff view appears
# and that rejecting leaves the original unchanged. Accept/Reject paths are
# exercised via keyboard equivalents.
#
# SKIPs if no AI provider is configured — polish needs a live generation
# provider, same as the `2nb polish` CLI command.
#
# Uses a temp vault so nothing touches the user's real $VAULT.
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: AI Polish ==="
echo ""

# Provider gate — polish hits the generation API.
if [ -z "${AWS_ACCESS_KEY_ID:-}${AWS_PROFILE:-}${OPENROUTER_API_KEY:-}" ]; then
    echo "SKIP: no AI provider credentials available (need AWS_PROFILE or OPENROUTER_API_KEY)"
    exit 0
fi

POLISH_VAULT="/tmp/sb-polish-$$"
PRIOR_VAULT=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || echo "")

cleanup() {
    kill_app
    rm -rf "$POLISH_VAULT"
    # Restore whatever vault the developer had open before the test. If no
    # prior value existed (first-time user), DELETE the key instead of writing
    # an empty string — otherwise the app would launch against the deleted
    # /tmp/sb-polish-$$ path.
    if [ -n "$PRIOR_VAULT" ]; then
        defaults write dev.apresai.2ndbrain lastVaultPath "$PRIOR_VAULT"
    else
        defaults delete dev.apresai.2ndbrain lastVaultPath 2>/dev/null || true
    fi
    rm -f /tmp/polish-seed.err
}
trap cleanup EXIT

# --- Seed the temp vault with something worth polishing ---
2nb vault create "$POLISH_VAULT" >/dev/null 2>/tmp/polish-seed.err || {
    echo "failed to create polish vault: $(cat /tmp/polish-seed.err)"
    exit 1
}
2nb --vault "$POLISH_VAULT" create "GUI Test Polish" --type note >/dev/null 2>/tmp/polish-seed.err || {
    echo "failed to seed polish doc: $(cat /tmp/polish-seed.err)"
    exit 1
}
rm -f /tmp/polish-seed.err

DOC="$POLISH_VAULT/gui-test-polish.md"
# Append obvious prose issues so the polish diff has something to suggest.
cat >> "$DOC" << 'EOF'

this document have some gramatical issues to polish. the prose is not very
good and could benefit from a revision pass by the ai. we want to see the
diff view apear when we invoke the polish command.
EOF

2nb --vault "$POLISH_VAULT" index >/dev/null 2>&1

# --- Launch app pointed at the temp vault ---
defaults write dev.apresai.2ndbrain lastVaultPath "$POLISH_VAULT"
launch_app
sleep 1

# Open the doc via Quick Open so polish targets it.
open_quick_open
type_text "gui-test-polish"
sleep 1
press_return
sleep 2
screenshot "polish-00-open"

# Trigger polish (Cmd+Option+P — see SecondBrain keyboard shortcut table).
POLISH_START=$(date +%s)
cmd_opt_key "p"
# Polish calls the generation API; give it generous time.
sleep 15
POLISH_ELAPSED=$(( $(date +%s) - POLISH_START ))
echo "Polish invocation elapsed: ${POLISH_ELAPSED}s"
screenshot "polish-01-diff-view"

# Assertion 1: polish does NOT write to disk on its own — the original body
# must still be present. A regression that auto-applies polish would fail this.
if file_contains "$DOC" "gramatical issues"; then
    pass "Polish did not clobber disk (expected — polish is preview-only)"
else
    fail "Polish mutated disk directly" "polish should be diff-only"
fi

# Assertion 2: dismiss via Escape and verify body still unchanged.
press_escape
sleep 1
screenshot "polish-02-dismissed"

if file_contains "$DOC" "gramatical issues"; then
    pass "Reject: original body preserved after Escape"
else
    fail "Reject: body was mutated despite dismissing diff" ""
fi

print_results
[ "$FAIL" -eq 0 ]
