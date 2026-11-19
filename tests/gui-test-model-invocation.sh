#!/bin/bash
# GUI Test Suite: Live End-to-End Model Invocation (Zero-Homebrew)
# Exercises real model invocations across Local Gemma, Cloud Google Gemini,
# Hybrid Combo + Home Test Connection, Model Test under dimension_break/model_mismatch,
# and honest dead-endpoint failure detection.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Live Model Invocation ==="

ensure_cli_and_app
SKIP_BUILD=1

TEST_VAULT=$(mktemp -d /tmp/sb-gui-model-test.XXXXXX)
XDG_STATE_HOME=$(mktemp -d /tmp/sb-gui-model-state.XXXXXX)
assert_safe_test_vault "$TEST_VAULT"

NO_BREW_PATH=$(echo "$PATH" | tr ':' '\n' | grep -v '/opt/homebrew' | paste -sd: -)
export PATH="$NO_BREW_PATH"
export XDG_STATE_HOME

cleanup() {
    kill_app
    env PATH="$NO_BREW_PATH" XDG_STATE_HOME="$XDG_STATE_HOME" "$CLI_BIN" ai engine stop >/dev/null 2>&1 || true
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
    local attempts="${2:-120}"
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

last_action_seq() {
    python3 - "$GUI_LOG_DIR/gui-actions.jsonl" <<'PY'
import json, os, sys
p = sys.argv[1]
last = 0
if os.path.exists(p):
    with open(p, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                ev = json.loads(line)
                s = int(ev.get("seq", 0))
                if s > last:
                    last = s
            except Exception:
                pass
print(last)
PY
}

wait_gui_action_after() {
    local after_seq="$1"
    local action="$2"
    local phase="$3"
    local timeout="${4:-30}"
    local actions_file="$GUI_LOG_DIR/gui-actions.jsonl"
    python3 - "$actions_file" "$after_seq" "$action" "$phase" "$timeout" <<'PY'
import json, os, sys, time

actions_file, after_seq, target_action, target_phase, timeout_s = (
    sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], float(sys.argv[5])
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
                    if (
                        int(ev.get("seq", 0)) > after_seq
                        and ev.get("action") == target_action
                        and ev.get("phase") == target_phase
                    ):
                        sys.exit(0)
        except Exception:
            pass
    time.sleep(0.15)

print(
    f"wait_gui_action_after TIMEOUT ({timeout_s}s) for {target_action} ({target_phase}) after seq {after_seq}",
    file=sys.stderr,
)
sys.exit(1)
PY
}

run_settings_test_connection() {
    local reembed_seq="$1"
    sleep 0.6
    python3 - "$GUI_LOG_DIR/gui-actions.jsonl" "$reembed_seq" <<'PY'
import json, os, sys, time
actions_file, after_seq = sys.argv[1], int(sys.argv[2])
deadline = time.time() + 60.0
while time.time() < deadline:
    started = 0
    finished = 0
    if os.path.exists(actions_file):
        with open(actions_file, "r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    ev = json.loads(line)
                except Exception:
                    continue
                if int(ev.get("seq", 0)) > after_seq and ev.get("action") == "settings.doctor_selftest":
                    p = ev.get("phase")
                    if p == "started":
                        started += 1
                    elif p in ("succeeded", "failed"):
                        finished += 1
    if started == finished:
        sys.exit(0)
    time.sleep(0.2)
sys.exit(0)
PY
    local doc_seq
    doc_seq=$(last_action_seq)
    click_when_enabled "settings.testConnection" 120
    wait_gui_action_after "$doc_seq" "settings.doctor_selftest" "succeeded" 60
}

# --- Vault setup & initial local Gemma indexing ---
setup_temp_vault "$TEST_VAULT"
_=$(seed_note "$TEST_VAULT" "Model Invocation Note" "Testing live local Gemma and cloud Google Gemini model invocation through the macOS app.")

env 2NB_TEST=1 "$CLI_BIN" config set ai.provider llama-local --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.embedding_model embeddinggemma-2 --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.generation_model gemma4-e2b --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.dimensions 256 --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" index --vault "$TEST_VAULT" >/dev/null
pass "Seeded and indexed isolated vault on llama-local (embeddinggemma-2 256d + gemma4-e2b)"

launch_app_on_vault "$TEST_VAULT"
SEQ=$(last_action_seq)
click_menu_item "AI" "Gemini Settings…"
wait_gui_action_after "$SEQ" "settings.open" "succeeded" 10
pass "AI Settings opened"
wait_gui_state 'state.get("genProvider") == "llama-local" and state.get("embedProvider") == "llama-local"' 15

# --- Scenario 1: Local Gemma On-Device (llama-local) ---
SEQ=$(last_action_seq)
click_when_enabled "settings.testEmbedding"
wait_gui_action_after "$SEQ" "settings.test_model" "succeeded" 45
wait_gui_state 'state.get("lastEmbedProbeOK") is True and state.get("lastEmbedProbeModel") == "embeddinggemma-2"' 45
LOCAL_EMBED_DETAIL=$(gui_state_field "lastEmbedProbeDetail")
if [[ "$LOCAL_EMBED_DETAIL" == *"dims=256"* ]] && [[ "$LOCAL_EMBED_DETAIL" == *"L2="* ]]; then
    pass "Scenario 1: Local embedding probe passed (embeddinggemma-2: $LOCAL_EMBED_DETAIL)"
else
    fail "Scenario 1: Local embedding detail missing dims=256 / L2=" "got '$LOCAL_EMBED_DETAIL'"
fi

SEQ=$(last_action_seq)
click_when_enabled "settings.testGeneration"
wait_gui_action_after "$SEQ" "settings.test_model" "succeeded" 60
wait_gui_state 'state.get("lastGenProbeOK") is True and state.get("lastGenProbeModel") == "gemma4-e2b"' 60
LOCAL_GEN_DETAIL=$(gui_state_field "lastGenProbeDetail")
if [[ "$LOCAL_GEN_DETAIL" == *"4"* ]]; then
    pass "Scenario 1: Local generation probe passed (gemma4-e2b: $LOCAL_GEN_DETAIL)"
else
    fail "Scenario 1: Local generation detail missing expected '4'" "got '$LOCAL_GEN_DETAIL'"
fi

SEQ=$(last_action_seq)
click_when_enabled "settings.testConnection"
wait_gui_action_after "$SEQ" "settings.doctor_selftest" "succeeded" 60
wait_gui_state 'state.get("lastDoctorOK") is True and state.get("lastDoctorCredentials") == "accepted"' 60
screenshot "model-test-01-local-gemma"
pass "Scenario 1: Local Gemma doctor connection check accepted"

# --- Legacy upgrade button check (embeddinggemma-300m -> embeddinggemma-2) ---
cmd_key "w"
env 2NB_TEST=1 "$CLI_BIN" config set ai.embedding_model embeddinggemma-300m --vault "$TEST_VAULT" >/dev/null
SEQ=$(last_action_seq)
click_menu_item "AI" "Gemini Settings…"
wait_gui_action_after "$SEQ" "settings.open" "succeeded" 10
wait_gui_state 'state.get("embedModel") == "embeddinggemma-300m"' 15
click_when_enabled "settings.upgradeEmbeddingGemma2"
wait_gui_state 'state.get("embedModel") == "embeddinggemma-2"' 15
pass "Scenario 1b: Legacy upgrade button switched embeddinggemma-300m to embeddinggemma-2"

# --- Scenario 2: Cloud Google Gemini (gemini) ---
if probe_gemini_capability "generation" "$TEST_VAULT"; then
    pass "Scenario 2: Cloud Gemini capability probe active (gemini-4-hydrogen)"
else
    fail "Scenario 2: Cloud Gemini generation probe failed" "gemini-4-hydrogen unavailable (required, never skipped)"
    assert_no_leaked_secrets
    print_results
    exit 1
fi

click_when_enabled "settings.genProvider.gemini"
wait_gui_state 'state.get("genProvider") == "gemini"' 15
click_when_enabled "settings.embedProvider.gemini"
wait_gui_state 'state.get("genProvider") == "gemini" and state.get("embedProvider") == "gemini"' 15
pass "Scenario 2: Switched both slots to Google Gemini"

SEQ=$(last_action_seq)
click_when_enabled "settings.testEmbedding"
wait_gui_action_after "$SEQ" "settings.test_model" "succeeded" 45
wait_gui_state 'state.get("lastEmbedProbeOK") is True and state.get("lastEmbedProbeModel") == "gemini-embedding-2"' 45
CLOUD_EMBED_DETAIL=$(gui_state_field "lastEmbedProbeDetail")
if [[ "$CLOUD_EMBED_DETAIL" == *"dims=768"* ]] && [[ "$CLOUD_EMBED_DETAIL" == *"L2="* ]]; then
    pass "Scenario 2: Cloud embedding probe passed (gemini-embedding-2: $CLOUD_EMBED_DETAIL)"
else
    fail "Scenario 2: Cloud embedding detail missing dims=768 / L2=" "got '$CLOUD_EMBED_DETAIL'"
fi

SEQ=$(last_action_seq)
click_when_enabled "settings.testGeneration"
wait_gui_action_after "$SEQ" "settings.test_model" "succeeded" 45
wait_gui_state 'state.get("lastGenProbeOK") is True and state.get("lastGenProbeModel") == "gemini-4-hydrogen"' 45
CLOUD_GEN_DETAIL=$(gui_state_field "lastGenProbeDetail")
if [[ "$CLOUD_GEN_DETAIL" == *"4"* ]]; then
    pass "Scenario 2: Cloud generation probe passed (gemini-4-hydrogen: $CLOUD_GEN_DETAIL)"
else
    fail "Scenario 2: Cloud generation detail missing expected '4'" "got '$CLOUD_GEN_DETAIL'"
fi

REEMBED_SEQ=$(last_action_seq)
click_when_enabled "settings.reembedNow"
wait_gui_action_after "$REEMBED_SEQ" "index.complete" "succeeded" 90
wait_gui_state 'not state.get("isIndexing") and state.get("portabilityStatus") == "ok"' 90
pass "Scenario 2: Re-embedded vault with Cloud Gemini (portabilityStatus=ok)"

run_settings_test_connection "$REEMBED_SEQ"
wait_gui_state 'state.get("lastDoctorOK") is True' 60
screenshot "model-test-02-cloud-gemini"
pass "Scenario 2: Cloud Gemini doctor connection check passed"

# --- Scenario 3: Hybrid Combo + Home Test Connection ---
click_when_enabled "settings.embedProvider.local"
wait_gui_state 'state.get("embedProvider") == "llama-local"' 15
click_when_enabled "settings.matryoshka.512"
wait_gui_state 'state.get("dimensions") == 512' 15
pass "Scenario 3: Configured hybrid combo (embeddinggemma-2 512d + gemini-4-hydrogen)"

SEQ=$(last_action_seq)
click_when_enabled "settings.testEmbedding"
wait_gui_action_after "$SEQ" "settings.test_model" "succeeded" 45
wait_gui_state 'state.get("lastEmbedProbeOK") is True and state.get("lastEmbedProbeModel") == "embeddinggemma-2"' 45
HYBRID_EMBED_DETAIL=$(gui_state_field "lastEmbedProbeDetail")
if [[ "$HYBRID_EMBED_DETAIL" == *"dims=512"* ]] && [[ "$HYBRID_EMBED_DETAIL" == *"L2="* ]]; then
    pass "Scenario 3: Hybrid local embedding probe passed at 512d ($HYBRID_EMBED_DETAIL)"
else
    fail "Scenario 3: Hybrid embedding detail missing dims=512 / L2=" "got '$HYBRID_EMBED_DETAIL'"
fi

REEMBED_SEQ=$(last_action_seq)
click_when_enabled "settings.reembedNow"
wait_gui_action_after "$REEMBED_SEQ" "index.complete" "succeeded" 90
wait_gui_state 'not state.get("isIndexing") and state.get("portabilityStatus") == "ok"' 90
pass "Scenario 3: Re-embedded vault for hybrid combo (512d, portabilityStatus=ok)"

run_settings_test_connection "$REEMBED_SEQ"
wait_gui_state 'state.get("lastDoctorOK") is True' 60
screenshot "model-test-03-hybrid-combo"
pass "Scenario 3: Hybrid combo doctor connection check passed"

cmd_key "w"
HOME_PREV_SEQ=$(last_action_seq)
click_when_enabled "home.testConnectionButton"

if python3 - "$GUI_LOG_DIR/gui-actions.jsonl" "$HOME_PREV_SEQ" <<'PY'
import json, os, sys, time

actions_file, after_seq = sys.argv[1], int(sys.argv[2])
deadline = time.time() + 60.0
while time.time() < deadline:
    seen = {}
    if os.path.exists(actions_file):
        try:
            with open(actions_file, "r", encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    ev = json.loads(line)
                    if (
                        int(ev.get("seq", 0)) > after_seq
                        and ev.get("action") == "settings.test_model"
                        and ev.get("phase") == "succeeded"
                    ):
                        meta = ev.get("metadata", {})
                        model = meta.get("model", "")
                        if model in ("embeddinggemma-2", "gemini-4-hydrogen") and meta.get("ok") == "true":
                            seen[model] = meta
        except Exception:
            pass
    if "embeddinggemma-2" in seen and "gemini-4-hydrogen" in seen:
        sys.exit(0)
    time.sleep(0.2)

print("Timed out waiting for both hybrid Home Test Connection probes (embeddinggemma-2 & gemini-4-hydrogen)", file=sys.stderr)
sys.exit(1)
PY
then
    sleep 0.8
    screenshot "model-test-04-home-test"
    pass "Scenario 3: Home Test Connection verified both embeddinggemma-2 and gemini-4-hydrogen"
else
    fail "Scenario 3: Home Test Connection probe verification failed" "missing succeeded settings.test_model events after seq $HOME_PREV_SEQ"
fi

# --- Scenario 5: Model Test Passes Under dimension_break & model_mismatch Before Re-Embedding ---
SEQ=$(last_action_seq)
click_menu_item "AI" "Gemini Settings…"
wait_gui_action_after "$SEQ" "settings.open" "succeeded" 10
wait_gui_state 'state.get("genProvider") == "gemini" and state.get("embedProvider") == "llama-local"' 15

DIM_SWITCH_SEQ=$(last_action_seq)
click_when_enabled "settings.embedProvider.gemini"
wait_gui_state 'state.get("embedProvider") == "gemini" and state.get("dimensions") == 768 and state.get("portabilityStatus") == "dimension_break"' 15
pass "Scenario 5a: Switched embeddings to Google Gemini 768d over 512d DB (portabilityStatus=dimension_break)"

run_settings_test_connection "$DIM_SWITCH_SEQ"
wait_gui_state 'state.get("portabilityStatus") == "dimension_break" and state.get("lastDoctorOK") is True and state.get("lastModelsTestOK") is True and state.get("lastGenProbeOK") is True and state.get("lastEmbedProbeOK") is True and state.get("lastEmbedProbeModel") == "gemini-embedding-2"' 60
pass "Scenario 5a: Test AI Connection passed under dimension_break before re-embedding"

REEMBED_SEQ=$(last_action_seq)
click_when_enabled "settings.reembedNow"
wait_gui_action_after "$REEMBED_SEQ" "index.complete" "succeeded" 90
wait_gui_state 'not state.get("isIndexing") and state.get("portabilityStatus") == "ok"' 90
pass "Scenario 5b: Re-embedded vault on Cloud Gemini 768d (portabilityStatus=ok)"

click_when_enabled "settings.genProvider.local"
wait_gui_state 'state.get("genProvider") == "llama-local"' 15
click_when_enabled "settings.embedProvider.local"
wait_gui_state 'state.get("embedProvider") == "llama-local"' 15
click_when_enabled "settings.matryoshka.768"
wait_gui_state 'state.get("genProvider") == "llama-local" and state.get("embedProvider") == "llama-local" and state.get("dimensions") == 768 and state.get("localMatryoshkaDim") == 768 and state.get("portabilityStatus") == "model_mismatch"' 15
pass "Scenario 5b: Switched both slots to Local Gemma 768d over Gemini 768d DB (portabilityStatus=model_mismatch)"

MISMATCH_SEQ=$(last_action_seq)
run_settings_test_connection "$MISMATCH_SEQ"
wait_gui_state 'state.get("portabilityStatus") == "model_mismatch" and state.get("lastDoctorOK") is True and state.get("lastModelsTestOK") is True and state.get("lastGenProbeOK") is True and state.get("lastGenProbeModel") == "gemma4-e2b" and state.get("lastEmbedProbeOK") is True and state.get("lastEmbedProbeModel") == "embeddinggemma-2"' 60
sleep 0.5
screenshot "model-test-05-mismatch-models-pass"
pass "Scenario 5b: Test AI Connection passed for Local Gemma under model_mismatch without re-embedding"

# --- Scenario 6: Dead Endpoint Detection (Honest Failure) ---
env 2NB_TEST=1 "$CLI_BIN" config set ai.llama.gen_endpoint "http://127.0.0.1:9" --vault "$TEST_VAULT" >/dev/null
wait_gui_state 'state.get("genProvider") == "llama-local"' 15

FAIL_SEQ=$(last_action_seq)
click_when_enabled "settings.testGeneration"
wait_gui_action_after "$FAIL_SEQ" "settings.test_model" "succeeded" 30
wait_gui_state 'state.get("lastGenProbeOK") is False and state.get("lastGenProbeModel") == "gemma4-e2b"' 30
pass "Scenario 6a: Dead local endpoint honestly reported lastGenProbeOK=false for gemma4-e2b"

run_settings_test_connection "$FAIL_SEQ"
wait_gui_state 'state.get("lastDoctorOK") is False and state.get("lastModelsTestOK") is False and state.get("lastGenProbeOK") is False and state.get("lastGenProbeModel") == "gemma4-e2b"' 60
sleep 0.5
screenshot "model-test-06-honest-failure"
pass "Scenario 6b: Test AI Connection honestly reported lastDoctorOK=false and lastModelsTestOK=false on dead endpoint"

# --- Telemetry, secret hygiene & launchd absence ---
if python3 - "$GUI_LOG_DIR/gui-actions.jsonl" <<'PY'
import json, sys
actions_file = sys.argv[1]
last_seq = 0
count = 0
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
        count += 1
if count < 5:
    print(f"Expected >= 5 logged actions, found {count}", file=sys.stderr)
    sys.exit(1)
sys.exit(0)
PY
then
    pass "gui-actions.jsonl contains valid monotonic event sequence"
else
    fail "gui-actions.jsonl schema/monotonicity check failed" "$GUI_LOG_DIR/gui-actions.jsonl"
fi

assert_no_leaked_secrets

if [ ! -e "$HOME/Library/LaunchAgents/dev.apresai.2ndbrain.engine.plist" ]; then
    pass "No launchd engine plist installed"
else
    fail "Unexpected launchd engine plist present" "$HOME/Library/LaunchAgents/dev.apresai.2ndbrain.engine.plist"
fi

print_results
[ "$FAIL" -eq 0 ]
