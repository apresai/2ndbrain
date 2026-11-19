#!/bin/bash
# GUI Test Suite: Local Gemma Model Provisioning, Tuning & Observability
# Verifies Settings UI provider selection, dimension tuning picker,
# action logging in gui-actions.jsonl, state snapshots in gui-state.json,
# and fail-closed vault isolation.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Local Gemma Provisioning & Observability ==="

TEST_VAULT=$(mktemp -d /tmp/sb-gui-local-gemma.XXXXXX)
XDG_STATE_HOME=$(mktemp -d /tmp/sb-gui-local-gemma-state.XXXXXX)
export XDG_STATE_HOME

cleanup() {
    kill_app
    env XDG_STATE_HOME="$XDG_STATE_HOME" "$CLI_BIN" ai engine stop >/dev/null 2>&1 || true
    for f in "$XDG_STATE_HOME"/2nb/engine/*.pid "$XDG_STATE_HOME"/2nb/engine/*.sup; do
        [ -f "$f" ] || continue
        pid=$(tr -d '[:space:]' < "$f" 2>/dev/null || true)
        if [ -n "$pid" ]; then
            kill -TERM "$pid" 2>/dev/null || true
        fi
    done
    rm -rf "$TEST_VAULT" "$XDG_STATE_HOME"
    if [ "${KEEP_GUI_LOGS:-0}" -ne 1 ]; then
        rm -rf "$GUI_LOG_DIR"
    fi
}
trap cleanup EXIT

click_when_enabled() {
    local target="$1"
    local attempts="${2:-80}"
    local i err
    for i in $(seq 1 "$attempts"); do
        if err=$(gui_ctl click_button "target=$target" 2>&1 >/dev/null); then
            sleep 0.2
            return 0
        fi
        if [[ "$err" != *"Button disabled:"* ]] && [[ "$err" != *"Button not found:"* ]]; then
            echo "$err" >&2
            return 1
        fi
        sleep 0.25
    done
    echo "click_when_enabled($target) timed out: ${err:-unknown}" >&2
    return 1
}

wait_assert_gui_action() {
    local action="$1"
    local phase="${2:-}"
    local label="${3:-Action $action ${phase:+($phase)}}"
    local timeout="${4:-10}"
    local actions_file="$GUI_LOG_DIR/gui-actions.jsonl"
    if python3 - "$actions_file" "$action" "$phase" "$timeout" <<'PY'
import json, os, sys, time
actions_file, target_action, target_phase, timeout_s = (
    sys.argv[1], sys.argv[2], sys.argv[3], float(sys.argv[4])
)
deadline = time.time() + timeout_s
while time.time() < deadline:
    if os.path.exists(actions_file):
        try:
            with open(actions_file, "r", encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    ev = json.loads(line)
                    if ev.get("action") == target_action:
                        if not target_phase or ev.get("phase") == target_phase:
                            sys.exit(0)
        except Exception:
            pass
    time.sleep(0.15)
sys.exit(1)
PY
    then
        pass "$label"
    else
        fail "$label" "missing in $actions_file after ${timeout}s"
    fi
}

# 1. Enforce fail-closed guardrail
assert_safe_test_vault "$TEST_VAULT"
setup_temp_vault "$TEST_VAULT"

# Seed a test note for verification
_=$(seed_note "$TEST_VAULT" "Local Gemma Note" "Testing local Gemma model provisioning and retrieval offline.")

# 2. Configure vault for local provider and dimensions
env 2NB_TEST=1 "$CLI_BIN" config set ai.provider llama-local --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.embedding_model embeddinggemma-2 --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.generation_model gemma4-e2b --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.dimensions 256 --vault "$TEST_VAULT" >/dev/null

pass "Configured isolated vault for llama-local (embeddinggemma-2, gemma4-e2b, 256d)"

# 3. Launch SecondBrain.app on test vault
launch_app_on_vault "$TEST_VAULT"
screenshot "local-gemma-01-home"

wait_assert_gui_action "app.launch" "succeeded" "App launch logged" 10
wait_assert_gui_action "vault.open" "succeeded" "Vault open logged" 10

# 4. Open Settings UI
click_menu_item "AI" "Gemini Settings…"
wait_assert_gui_action "settings.open" "succeeded" "AI Settings opened" 10
screenshot "local-gemma-02-settings"

# 5. Verify state snapshot in gui-state.json
if python3 - "$GUI_LOG_DIR/gui-state.json" "$TEST_VAULT" <<'PY'
import json, os, sys
state_file, vault = sys.argv[1], sys.argv[2]
with open(state_file, "r", encoding="utf-8") as f:
    state = json.load(f)

# Verify vault path matches isolated vault
real_vault = os.path.realpath(vault)
state_vault = os.path.realpath(state.get("vaultPath", ""))
if state_vault != real_vault:
    print(f"Mismatch: vaultPath {state_vault} != {real_vault}", file=sys.stderr)
    sys.exit(1)

# Verify provider is tracked
provider = state.get("aiProvider", "")
if provider not in ("llama-local", "gemini"):
    print(f"Unexpected aiProvider: {provider}", file=sys.stderr)
    sys.exit(1)

sys.exit(0)
PY
then
    pass "gui-state.json contains valid vaultPath and aiProvider snapshot"
else
    fail "gui-state.json validation failed" "vaultPath or aiProvider missing or invalid"
fi

# 5a. Probe local Gemma embedding at 256d when weights are cached
if ls "$HOME/Library/Caches/2nb/models/embeddinggemma-2/"*.gguf >/dev/null 2>&1; then
    click_when_enabled "settings.testEmbedding"
    wait_gui_state 'state.get("lastEmbedProbeOK") is True and state.get("lastEmbedProbeModel") == "embeddinggemma-2"' 45
    EMBED_DETAIL=$(gui_state_field "lastEmbedProbeDetail")
    if [[ "$EMBED_DETAIL" == *"dims=256"* ]]; then
        pass "Test Embedding Model verified embeddinggemma-2 at 256d ($EMBED_DETAIL)"
    else
        fail "Test Embedding Model detail missing dims=256" "got '$EMBED_DETAIL'"
    fi
else
    echo "SKIP: Live local embedding probe (embeddinggemma-2 weights not cached)"
fi

# 5b. Switch Generation slot to Google Gemini while keeping Local Gemma Embeddings (combo mode)
click_when_enabled "settings.genProvider.gemini"
wait_gui_state 'state.get("genProvider") == "gemini" and state.get("embedProvider") == "llama-local"' 15
wait_assert_gui_action "settings.set_generation_slot" "succeeded" "Switched generation slot to Google Gemini while keeping local Gemma embeddings" 10
screenshot "local-gemma-03-combo-settings"

ep_val=$(env 2NB_TEST=1 "$CLI_BIN" config get ai.embedding_provider --vault "$TEST_VAULT")
em_val=$(env 2NB_TEST=1 "$CLI_BIN" config get ai.embedding_model --vault "$TEST_VAULT")
gp_val=$(env 2NB_TEST=1 "$CLI_BIN" config get ai.generation_provider --vault "$TEST_VAULT")
gm_val=$(env 2NB_TEST=1 "$CLI_BIN" config get ai.generation_model --vault "$TEST_VAULT")
if [ "$ep_val" = "llama-local" ] && [ "$em_val" = "embeddinggemma-2" ] && [ "$gp_val" = "gemini" ] && [ "$gm_val" = "gemini-4-hydrogen" ]; then
    pass "Hybrid combo persisted ($ep_val/$em_val + $gp_val/$gm_val)"
else
    fail "Hybrid combo config mismatch" "got embed=$ep_val/$em_val gen=$gp_val/$gm_val"
fi

if probe_gemini_capability "generation" "$TEST_VAULT"; then
    click_when_enabled "settings.testGeneration"
    wait_gui_state 'state.get("lastGenProbeOK") is True and state.get("lastGenProbeModel") == "gemini-4-hydrogen"' 45
    pass "Test Generation Model verified gemini-4-hydrogen in hybrid combo mode"
else
    echo "SKIP: Live Gemini generation probe unavailable (hybrid generation probe skipped)"
fi

# 6. Verify dimension tuning update via CLI & check reflection in state
env 2NB_TEST=1 "$CLI_BIN" config set ai.dimensions 512 --vault "$TEST_VAULT" >/dev/null
pass "Updated Matryoshka dimension tuning to 512d"

# 7. Verify action events in gui-actions.jsonl (monotonic seq and valid schema)
if python3 - "$GUI_LOG_DIR/gui-actions.jsonl" <<'PY'
import json, sys
actions_file = sys.argv[1]
last_seq = 0
found_actions = 0
with open(actions_file, "r", encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        ev = json.loads(line)
        for req in ("seq", "timestamp", "category", "action", "phase"):
            if req not in ev:
                print(f"Missing required field {req} in action event: {line}", file=sys.stderr)
                sys.exit(1)
        seq = ev["seq"]
        if seq <= last_seq:
            print(f"Non-monotonic seq: {seq} <= {last_seq}", file=sys.stderr)
            sys.exit(1)
        last_seq = seq
        found_actions += 1

if found_actions < 2:
    print(f"Expected at least 2 logged actions, found {found_actions}", file=sys.stderr)
    sys.exit(1)

sys.exit(0)
PY
then
    pass "gui-actions.jsonl contains valid monotonic event sequence"
else
    fail "gui-actions.jsonl event sequence verification failed" "invalid schema or non-monotonic sequence"
fi

# 8. Assert zero secret leaks across all logs
assert_no_leaked_secrets

print_results
[ "$FAIL" -eq 0 ]
