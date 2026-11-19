#!/bin/bash
# GUI Test Suite: Isolated Vault Switching Without Mutating Live UserDefaults
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Vault Switch ==="

VAULT_A=$(mktemp -d /tmp/sb-vault-a.XXXXXX)
VAULT_B=$(mktemp -d /tmp/sb-vault-b.XXXXXX)
PRIOR_LAST_VAULT=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || echo "")

cleanup() {
    kill_app
    rm -rf "$VAULT_A" "$VAULT_B" "$GUI_LOG_DIR"
}
trap cleanup EXIT

setup_temp_vault "$VAULT_A"
setup_temp_vault "$VAULT_B"
_=$(seed_note "$VAULT_A" "Alpha Vault Note" "Alpha vault fixture content.")
_=$(seed_note "$VAULT_B" "Bravo Vault Note" "Bravo vault fixture content.")

launch_app_on_vault "$VAULT_A"
screenshot "vault-switch-01-a"
assert_gui_action "vault.open" "succeeded" "Bound Vault A cleanly"

launch_app_on_vault "$VAULT_B"
screenshot "vault-switch-02-b"
assert_gui_action "vault.open" "succeeded" "Switched cleanly to Vault B"

launch_app_on_vault "$VAULT_A"
screenshot "vault-switch-03-a"
assert_gui_action "vault.open" "succeeded" "Switched back cleanly to Vault A"

AFTER_LAST_VAULT=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || echo "")
if [ "$AFTER_LAST_VAULT" = "$PRIOR_LAST_VAULT" ]; then
    pass "UserDefaults lastVaultPath preserved untouched ($PRIOR_LAST_VAULT)"
else
    fail "UserDefaults lastVaultPath mutated during test" "before=$PRIOR_LAST_VAULT after=$AFTER_LAST_VAULT"
fi

assert_no_leaked_secrets
print_results
[ "$FAIL" -eq 0 ]
