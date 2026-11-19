#!/bin/bash
# GUI Test Suite: Single-Button Fix Vault & Undo Last Fix Flow (Cmd+3)
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Single-Button Fix Vault & Undo ==="

TEST_VAULT=$(mktemp -d /tmp/sb-gui-fix-vault.XXXXXX)
ORIG_COPY_DIR=$(mktemp -d /tmp/sb-gui-fix-orig.XXXXXX)
cleanup() {
    kill_app
    rm -rf "$TEST_VAULT" "$ORIG_COPY_DIR" "$GUI_LOG_DIR"
}
trap cleanup EXIT

setup_temp_vault "$TEST_VAULT"

mkdir -p "$TEST_VAULT/Guides" "$TEST_VAULT/Notes"
cat > "$TEST_VAULT/Guides/Claude Code CLI.md" <<'EOF'
---
title: Claude Code CLI
type: note
status: active
---

# Claude Code CLI

Reference setup guide.
EOF

cat > "$TEST_VAULT/Notes/broken.md" <<'EOF'
---
created: "2026-04-20T10:00:00Z"
type: note
status: active
---

# Broken Links Note

See [[Guides\Claude Code CLI.md]], [[claude-code]], and [[nonexistent-ghost-note]].
EOF

env 2NB_TEST=1 "$CLI_BIN" index --vault "$TEST_VAULT" >/dev/null

cp "$TEST_VAULT/Notes/broken.md" "$ORIG_COPY_DIR/broken.md"
cp "$TEST_VAULT/.2ndbrain/schemas.yaml" "$ORIG_COPY_DIR/schemas.yaml"

launch_app_on_vault "$TEST_VAULT"

# Navigate to Notes > Links & Schema (Cmd+3) and wait for auto-scan (`lint.run`).
cmd_key "3"
wait_gui_state 'state.get("activeTab") == "Notes" and state.get("activeSection") == "Links & Schema"' 10

for _ in $(seq 1 60); do
    if python3 -c 'import json,sys
for line in open(sys.argv[1]):
    ev=json.loads(line)
    if ev.get("action")=="lint.run" and ev.get("phase")=="succeeded":
        sys.exit(0)
sys.exit(1)' "$GUI_LOG_DIR/gui-actions.jsonl" 2>/dev/null; then
        break
    fi
    sleep 0.2
done
sleep 0.25
screenshot "fix-vault-01-preview"
assert_gui_action "lint.run" "succeeded" "Auto-scan ran lint + fix preview on open"

# Click single-button `Fix Vault`
click_ax_button "notes.fixVaultButton"
for _ in $(seq 1 60); do
    if python3 -c 'import json,sys
for line in open(sys.argv[1]):
    ev=json.loads(line)
    if ev.get("action")=="lint.fix_vault" and ev.get("phase")=="succeeded":
        sys.exit(0)
sys.exit(1)' "$GUI_LOG_DIR/gui-actions.jsonl" 2>/dev/null; then
        break
    fi
    sleep 0.25
done
sleep 0.35
screenshot "fix-vault-02-applied"
assert_gui_action "lint.fix_vault" "succeeded" "Single-button Fix Vault applied cleanly"

# Verify on-disk changes: unquoted date, fixed obvious link, unwrapped dead link, widened schema, 0 lint errors/warnings
if python3 - "$TEST_VAULT/Notes/broken.md" "$TEST_VAULT/.2ndbrain/schemas.yaml" <<'PY'
import sys
broken = open(sys.argv[1], "r", encoding="utf-8").read()
schema = open(sys.argv[2], "r", encoding="utf-8").read()
assert "created: 2026-04-20T10:00:00Z\n" in broken, f"expected unquoted created in:\n{broken}"
assert "[[Claude Code CLI]]" in broken, f"expected relinked Claude Code CLI in:\n{broken}"
assert "nonexistent-ghost-note" in broken and "[[nonexistent-ghost-note]]" not in broken, f"expected unwrapped dead link in:\n{broken}"
assert "active" in schema, f"expected active widened in schema:\n{schema}"
PY
then
    pass "Disk note & schemas.yaml reflect all Fix Vault repairs"
else
    fail "Disk note & schemas.yaml verification failed" "$TEST_VAULT/Notes/broken.md"
fi

if LINT_OUT=$(env 2NB_TEST=1 "$CLI_BIN" lint --vault "$TEST_VAULT" --json --porcelain) && \
   python3 -c 'import json,sys; d=json.loads(sys.stdin.read()); sys.exit(0 if d.get("errors")==0 and d.get("warnings")==0 else 1)' <<< "$LINT_OUT"; then
    pass "2nb lint reports 0 errors and 0 warnings after Fix Vault"
else
    fail "2nb lint still reported issues after Fix Vault" "$LINT_OUT"
fi

# Click Undo Last Fix and verify byte-for-byte restore of broken.md and schemas.yaml
click_ax_button "notes.undoFixButton"
for _ in $(seq 1 60); do
    if python3 -c 'import json,sys
for line in open(sys.argv[1]):
    ev=json.loads(line)
    if ev.get("action")=="lint.undo_fix_vault" and ev.get("phase")=="succeeded":
        sys.exit(0)
sys.exit(1)' "$GUI_LOG_DIR/gui-actions.jsonl" 2>/dev/null; then
        break
    fi
    sleep 0.25
done
sleep 0.25
screenshot "fix-vault-03-undone"
assert_gui_action "lint.undo_fix_vault" "succeeded" "Undo Last Fix executed cleanly"

if cmp -s "$TEST_VAULT/Notes/broken.md" "$ORIG_COPY_DIR/broken.md" && \
   cmp -s "$TEST_VAULT/.2ndbrain/schemas.yaml" "$ORIG_COPY_DIR/schemas.yaml"; then
    pass "Undo Last Fix restored broken.md and schemas.yaml byte-for-byte"
else
    fail "Undo Last Fix did not restore files byte-for-byte" "$TEST_VAULT"
fi

assert_no_leaked_secrets
print_results
[ "$FAIL" -eq 0 ]
