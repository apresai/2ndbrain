#!/bin/bash
# GUI Test Suite: Re-embed ETA Estimation & Persistent AI Usage/Cost Tracking (Zero-Homebrew)
# Exercises pre-run re-embed ETA estimation in IndexProgressView, live re-embed
# recording into metrics.db, Activity > AI Usage & Cost view + CLI contract,
# Settings > AI & Models usage display, and usage history clearing.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Re-embed ETA & AI Usage/Cost ==="

ensure_cli_and_app
SKIP_BUILD=1

TEST_VAULT=$(mktemp -d /tmp/sb-gui-eta-usage.XXXXXX)
XDG_STATE_HOME=$(mktemp -d /tmp/sb-gui-eta-state.XXXXXX)
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

# --- Vault setup & initial local Gemma indexing ---
setup_temp_vault "$TEST_VAULT"
env 2NB_TEST=1 "$CLI_BIN" config set ai.provider llama-local --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.embedding_model embeddinggemma-2 --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.generation_model gemma4-e2b --vault "$TEST_VAULT" >/dev/null
env 2NB_TEST=1 "$CLI_BIN" config set ai.dimensions 256 --vault "$TEST_VAULT" >/dev/null

for i in $(seq 1 12); do
    _=$(seed_note "$TEST_VAULT" "ETA Benchmark Note $i" "Section one of benchmark note $i discussing local Apple Silicon Metal embeddings, knowledge graph links, and re-embedding throughput estimation across large Markdown vaults.

## Details
Detailed paragraph $i with enough distinct prose tokens so SQLite FTS5 and sqlite-vec chunking generate consistent non-empty vector chunks for timing and usage telemetry.")
done

env 2NB_TEST=1 "$CLI_BIN" index --vault "$TEST_VAULT" >/dev/null
pass "Seeded and indexed 12 notes with local embeddinggemma-2"

METRICS_INITIAL=$(env 2NB_TEST=1 "$CLI_BIN" metrics --json --vault "$TEST_VAULT")
if python3 - "$METRICS_INITIAL" <<'PY'
import json, sys
m = json.loads(sys.argv[1])
est = m.get("reembed_estimate") or {}
usage = (m.get("ai_usage") or {}).get("all_time") or {}
assert est.get("embeddable_docs") == 12, f"embeddable_docs={est.get('embeddable_docs')}"
assert est.get("total_chunks", 0) >= 12, f"total_chunks={est.get('total_chunks')}"
assert est.get("estimated_seconds", 0) > 0, f"estimated_seconds={est.get('estimated_seconds')}"
assert est.get("is_cloud") is False, f"is_cloud={est.get('is_cloud')}"
assert est.get("estimated_cost_usd") == 0.0, f"estimated_cost_usd={est.get('estimated_cost_usd')}"
assert usage.get("local_calls", 0) >= 1, f"local_calls={usage.get('local_calls')}"
assert usage.get("cloud_cost_usd") == 0.0, f"cloud_cost_usd={usage.get('cloud_cost_usd')}"
PY
then
    pass "CLI metrics --json reports 12-doc reembed_estimate and \$0.00 local usage"
else
    fail "CLI metrics --json mismatch" "$METRICS_INITIAL"
fi

# --- Launch app & wait for initial metrics refresh ---
launch_app_on_vault "$TEST_VAULT"
wait_gui_state 'state.get("aiUsage30dLocalCalls") is not None and state.get("aiUsage30dLocalCalls", 0) >= 1' 20
sleep 0.3
screenshot "usage-00-home-cost-summary"
pass "App loaded initial AI usage telemetry from metrics.db"

# --- Step 1: Open Re-embed All sheet and inspect pre-run ETA ---
SEQ=$(last_action_seq)
click_menu_item "Vault" "Re-embed All…"
wait_gui_action_after "$SEQ" "index.open_sheet" "succeeded" 10
wait_gui_state 'state.get("indexSheetVisible") is True and state.get("indexPhase") == "Ready" and state.get("indexEstimatedRemainingSeconds") is not None' 15
sleep 0.8
screenshot "usage-01-reembed-sheet-eta"
pass "Re-embed sheet opened with pre-run ETA populated"

# --- Step 2: Confirm re-embed and wait for completion + updated history speed ---
SEQ=$(last_action_seq)
click_ax_button "indexSheet.confirmButton"
wait_gui_action_after "$SEQ" "index.complete" "succeeded" 120
click_ax_button "indexSheet.doneButton"
wait_gui_state 'state.get("indexSheetVisible") is False and (state.get("aiUsage30dLocalCalls") or 0) >= 2' 20

METRICS_AFTER_REEMBED=$(env 2NB_TEST=1 "$CLI_BIN" metrics --json --vault "$TEST_VAULT")
if python3 - "$METRICS_AFTER_REEMBED" <<'PY'
import json, sys
m = json.loads(sys.argv[1])
est = m.get("reembed_estimate") or {}
usage = (m.get("ai_usage") or {}).get("all_time") or {}
assert est.get("speed_source") == "history", f"speed_source={est.get('speed_source')}"
assert est.get("docs_per_sec", 0) > 0, f"docs_per_sec={est.get('docs_per_sec')}"
assert usage.get("local_calls", 0) >= 2, f"local_calls={usage.get('local_calls')}"
assert usage.get("cloud_cost_usd") == 0.0, f"cloud_cost_usd={usage.get('cloud_cost_usd')}"
PY
then
    pass "Full re-embed recorded >=2 local runs and upgraded reembed_estimate speed_source to history"
else
    fail "Metrics after re-embed did not upgrade speed_source to history" "$METRICS_AFTER_REEMBED"
fi

# --- Step 3: Navigate to Activity > AI Usage & Cost ---
click_menu_item "AI" "AI Usage & Cost…"
wait_gui_state 'state.get("activeTab") == "Activity" and state.get("activeSection") == "AI Usage & Cost" and (state.get("aiUsage30dLocalCalls") or 0) >= 2 and (state.get("aiUsage30dCostUSD") or 0.0) == 0.0' 15
sleep 0.3
screenshot "usage-02-activity-ai-usage"
pass "Navigated to Activity > AI Usage & Cost via AI menu with live counts"

click_ax_button "aiUsage.refreshButton"
sleep 0.3
pass "Clicked Refresh in AI Usage & Cost view"

# --- Step 4: Open Gemini Settings, capture usage section, and test Reset Usage History ---
SEQ=$(last_action_seq)
click_menu_item "AI" "Gemini Settings…"
wait_gui_action_after "$SEQ" "settings.open" "succeeded" 10
sleep 0.6
screenshot "usage-03-settings-usage"
pass "Opened AI & Models Settings showing 30-day usage & re-embed estimate"

SEQ=$(last_action_seq)
click_ax_button "settings.resetAIUsage"
wait_gui_action_after "$SEQ" "settings.clear_metrics" "succeeded" 15
wait_gui_state '(state.get("aiUsage30dLocalCalls") or 0) == 0 and (state.get("aiUsage30dCloudCalls") or 0) == 0' 15
cmd_key "w"
pass "Reset Usage History cleared metrics.db and updated telemetry to 0 calls"

assert_no_leaked_secrets
print_results
[ "$FAIL" -eq 0 ]
