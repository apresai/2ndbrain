#!/bin/bash
# GUI Test Suite: Switching between vaults
# Verifies that changing the lastVaultPath and restarting the app picks up the
# new vault — the sidebar should show the docs from the new vault, not the old
# one. Folder-picker automation is brittle (NSOpenPanel), so this drives the
# switch via UserDefaults, which is how the app persists the choice in prod.
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Vault Switch ==="
echo ""

VAULT_A="/tmp/sb-vault-a-$$"
VAULT_B="/tmp/sb-vault-b-$$"
# Capture the developer's prior vault so we can restore it on cleanup instead
# of leaving the app pointed at one of the deleted /tmp vaults.
PRIOR_VAULT=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || echo "")

cleanup() {
    kill_app
    rm -rf "$VAULT_A" "$VAULT_B"
    if [ -n "$PRIOR_VAULT" ]; then
        defaults write dev.apresai.2ndbrain lastVaultPath "$PRIOR_VAULT"
    else
        defaults delete dev.apresai.2ndbrain lastVaultPath 2>/dev/null || true
    fi
    rm -f /tmp/vault-switch-seed.err
}
trap cleanup EXIT

# --- Setup: two vaults with distinct content ---
# Capture stderr so failures in seed setup surface with context, not just
# a silent "seed doc missing" later.
2nb vault create "$VAULT_A" >/dev/null 2>/tmp/vault-switch-seed.err || {
    echo "failed to create $VAULT_A: $(cat /tmp/vault-switch-seed.err)"
    exit 1
}
2nb vault create "$VAULT_B" >/dev/null 2>/tmp/vault-switch-seed.err || {
    echo "failed to create $VAULT_B: $(cat /tmp/vault-switch-seed.err)"
    exit 1
}

# Seed one doc each so the sidebars are visually distinguishable.
2nb --vault "$VAULT_A" create "Alpha in Vault A" --type note >/dev/null 2>/tmp/vault-switch-seed.err || {
    echo "failed to seed Vault A: $(cat /tmp/vault-switch-seed.err)"
    exit 1
}
2nb --vault "$VAULT_B" create "Bravo in Vault B" --type note >/dev/null 2>/tmp/vault-switch-seed.err || {
    echo "failed to seed Vault B: $(cat /tmp/vault-switch-seed.err)"
    exit 1
}
rm -f /tmp/vault-switch-seed.err

# --- Open vault A ---
defaults write dev.apresai.2ndbrain lastVaultPath "$VAULT_A"
launch_app
screenshot "vault-switch-00-open-a"

if file_exists "$VAULT_A/alpha-in-vault-a.md"; then
    pass "Vault A: doc present on disk"
else
    fail "Vault A: seed doc missing" "$VAULT_A/alpha-in-vault-a.md"
fi

# --- Switch to vault B by setting the default and restarting ---
kill_app
defaults write dev.apresai.2ndbrain lastVaultPath "$VAULT_B"
launch_app
sleep 2
screenshot "vault-switch-01-open-b"

SAVED_PATH=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || echo "")
if [ "$SAVED_PATH" = "$VAULT_B" ]; then
    pass "App launched with vault B path in defaults"
else
    fail "Vault B path not persisted" "got: $SAVED_PATH"
fi

# --- Verify the on-disk state is what vault B has, not what vault A had ---
if file_exists "$VAULT_B/bravo-in-vault-b.md"; then
    pass "Vault B: doc present on disk after switch"
else
    fail "Vault B: seed doc missing" "$VAULT_B/bravo-in-vault-b.md"
fi

# --- Switch back to A to make sure direction doesn't matter ---
kill_app
defaults write dev.apresai.2ndbrain lastVaultPath "$VAULT_A"
launch_app
sleep 2
screenshot "vault-switch-02-back-to-a"

SAVED_PATH=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || echo "")
if [ "$SAVED_PATH" = "$VAULT_A" ]; then
    pass "Switched back to vault A"
else
    fail "Vault A path not persisted on second switch" "got: $SAVED_PATH"
fi

print_results
[ "$FAIL" -eq 0 ]
