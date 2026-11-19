#!/bin/bash
# Shared helpers and fixture builders for the Notes Tab Revamp E2E Suite
# (`tests/e2e-notes-revamp.sh`).
# Zero-Mock Policy: never creates mock HTTP servers or fake AI responses.
# Fail-Closed Isolation: all test vaults must live under /tmp/ and pass assert_safe_test_vault.

E2E_NOTES_HELPERS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tests/gui-helpers.sh
source "$E2E_NOTES_HELPERS_DIR/gui-helpers.sh"

# ------------------------------------------------------------------------------
# Vault & Temp Directory Lifecycle
# ------------------------------------------------------------------------------

_track_temp_path() {
    local p="${1:-}"
    if [ -n "$p" ] && [ -n "${VAULT_TRACKING_FILE:-}" ] && [ -f "${VAULT_TRACKING_FILE:-}" ]; then
        echo "$p" >> "$VAULT_TRACKING_FILE"
    fi
}

_track_gui_log_dir() {
    local p="${1:-}"
    if [ -n "$p" ] && [ -n "${GUI_LOG_TRACKING_FILE:-}" ] && [ -f "${GUI_LOG_TRACKING_FILE:-}" ]; then
        echo "$p" >> "$GUI_LOG_TRACKING_FILE"
    fi
}

check_no_leaked_secrets_dir() {
    local dir="${1:-}"
    local label="${2:-No leaked API secrets in $dir}"
    [ -n "$dir" ] && [ -d "$dir" ] || return 0
    local _prev_gui_log="${GUI_LOG_DIR:-}"
    local _prev_fail="${FAIL:-0}"
    GUI_LOG_DIR="$dir"
    assert_no_leaked_secrets "$label" >/dev/null || true
    GUI_LOG_DIR="$_prev_gui_log"
    if [ "${FAIL:-0}" -gt "$_prev_fail" ]; then
        return 1
    fi
    return 0
}

create_notes_vault() {
    local prefix="${1:-sb-e2e-notes}"
    local dir
    dir=$(mktemp -d "/tmp/${prefix}.XXXXXX")
    assert_safe_test_vault "$dir"
    _track_temp_path "$dir"
    setup_temp_vault "$dir"
    echo "$dir"
}

create_bare_notes_dir() {
    local prefix="${1:-sb-e2e-dir}"
    local dir
    dir=$(mktemp -d "/tmp/${prefix}.XXXXXX")
    assert_safe_test_vault "$dir"
    _track_temp_path "$dir"
    echo "$dir"
}

create_notes_gui_log_dir() {
    local prefix="${1:-sb-gui-notes}"
    local dir
    dir=$(mktemp -d "/tmp/${prefix}.XXXXXX")
    assert_safe_test_vault "$dir"
    _track_temp_path "$dir"
    _track_gui_log_dir "$dir"
    GUI_LOG_DIR="$dir"
    GUI_CONTROL_DIR="$dir/control"
    mkdir -p "$GUI_LOG_DIR" "$GUI_CONTROL_DIR"
    export GUI_LOG_DIR GUI_CONTROL_DIR
    echo "$dir"
}

cleanup_notes_dir() {
    local dir="${1:-}"
    if [ -n "$dir" ] && [ -d "$dir" ]; then
        assert_safe_test_vault "$dir"
        if [ "${KEEP_VAULTS:-0}" -eq 0 ]; then
            chmod -R u+w "$dir" 2>/dev/null || true
            rm -rf "$dir" 2>/dev/null || true
        fi
    fi
}

cleanup_notes_gui_session() {
    [ -n "${APP_PID:-}" ] && disown "$APP_PID" 2>/dev/null || true
    kill_app 2>/dev/null || true
    if [ -n "${GUI_LOG_DIR:-}" ] && [ -d "${GUI_LOG_DIR:-}" ]; then
        if ! check_no_leaked_secrets_dir "$GUI_LOG_DIR" "No leaked API secrets in $GUI_LOG_DIR"; then
            if [ -n "${S4_RESULTS_FILE:-}" ] && [ -f "${S4_RESULTS_FILE:-}" ]; then
                echo "FAIL:$GUI_LOG_DIR" >> "$S4_RESULTS_FILE"
            fi
            echo "FATAL GUARDRAIL VIOLATION: Secret leaked in GUI telemetry ($GUI_LOG_DIR)" >&2
            exit 99
        fi
        if [ -n "${S4_RESULTS_FILE:-}" ] && [ -f "${S4_RESULTS_FILE:-}" ]; then
            echo "PASS:$GUI_LOG_DIR" >> "$S4_RESULTS_FILE"
        fi
        if [ "${KEEP_VAULTS:-0}" -eq 0 ]; then
            assert_safe_test_vault "$GUI_LOG_DIR"
            rm -rf "$GUI_LOG_DIR" 2>/dev/null || true
        fi
    fi
}

# ------------------------------------------------------------------------------
# Opaque-Box CLI Runners (Stdout, Stderr, Exit Code captured independently)
# ------------------------------------------------------------------------------

# run_cli <out_var> <err_var> <rc_var> <args...>
run_cli() {
    local _out_var="$1" _err_var="$2" _rc_var="$3"
    shift 3
    local _out_f _err_f _rc=0
    _out_f=$(mktemp /tmp/sb-cli-out.XXXXXX)
    _err_f=$(mktemp /tmp/sb-cli-err.XXXXXX)
    _track_temp_path "$_out_f"
    _track_temp_path "$_err_f"
    env 2NB_TEST=1 "$CLI_BIN" "$@" >"$_out_f" 2>"$_err_f" || _rc=$?
    printf -v "$_out_var" '%s' "$(cat "$_out_f")"
    printf -v "$_err_var" '%s' "$(cat "$_err_f")"
    printf -v "$_rc_var" '%d' "$_rc"
    rm -f "$_out_f" "$_err_f"
}

# run_cli_in_dir <cwd> <out_var> <err_var> <rc_var> <args...>
run_cli_in_dir() {
    local _cwd="$1" _out_var="$2" _err_var="$3" _rc_var="$4"
    shift 4
    local _out_f _err_f _rc=0
    _out_f=$(mktemp /tmp/sb-cli-out.XXXXXX)
    _err_f=$(mktemp /tmp/sb-cli-err.XXXXXX)
    _track_temp_path "$_out_f"
    _track_temp_path "$_err_f"
    (cd "$_cwd" && env 2NB_TEST=1 "$CLI_BIN" "$@") >"$_out_f" 2>"$_err_f" || _rc=$?
    printf -v "$_out_var" '%s' "$(cat "$_out_f")"
    printf -v "$_err_var" '%s' "$(cat "$_err_f")"
    printf -v "$_rc_var" '%d' "$_rc"
    rm -f "$_out_f" "$_err_f"
}

# run_cli_hermetic <out_var> <err_var> <rc_var> <args...>
# Mirrors cli/Makefile HERMETIC_ENV + 2NB_SKIP_KEYCHAIN=1 + 2NB_TEST=1.
run_cli_hermetic() {
    local _out_var="$1" _err_var="$2" _rc_var="$3"
    shift 3
    local _herm_home _out_f _err_f _rc=0
    _herm_home=$(mktemp -d /tmp/sb-herm-home.XXXXXX)
    assert_safe_test_vault "$_herm_home"
    _track_temp_path "$_herm_home"
    mkdir -p "$_herm_home/config" "$_herm_home/cache"
    _out_f=$(mktemp /tmp/sb-cli-out.XXXXXX)
    _err_f=$(mktemp /tmp/sb-cli-err.XXXXXX)
    _track_temp_path "$_out_f"
    _track_temp_path "$_err_f"
    env -u AWS_BEARER_TOKEN_BEDROCK -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY \
        -u AWS_SESSION_TOKEN -u AWS_PROFILE -u AWS_ROLE_ARN \
        -u AWS_WEB_IDENTITY_TOKEN_FILE \
        -u AWS_CONTAINER_CREDENTIALS_RELATIVE_URI -u AWS_CONTAINER_CREDENTIALS_FULL_URI \
        -u OPENROUTER_API_KEY \
        -u GEMINI_API_KEY -u GOOGLE_API_KEY -u GOOGLE_APPLICATION_CREDENTIALS \
        -u GOOGLE_CLOUD_PROJECT -u GCLOUD_PROJECT -u CLOUDSDK_CORE_PROJECT \
        2NB_TEST=1 2NB_BEDROCK_SKIP_KEYCHAIN=1 2NB_SKIP_KEYCHAIN=1 \
        AWS_EC2_METADATA_DISABLED=true \
        AWS_CONFIG_FILE="$_herm_home/no-aws-config" \
        AWS_SHARED_CREDENTIALS_FILE="$_herm_home/no-aws-credentials" \
        HOME="$_herm_home" XDG_CONFIG_HOME="$_herm_home/config" XDG_CACHE_HOME="$_herm_home/cache" \
        "$CLI_BIN" "$@" >"$_out_f" 2>"$_err_f" || _rc=$?
    printf -v "$_out_var" '%s' "$(cat "$_out_f")"
    printf -v "$_err_var" '%s' "$(cat "$_err_f")"
    printf -v "$_rc_var" '%d' "$_rc"
    rm -rf "$_herm_home" "$_out_f" "$_err_f"
}

# run_cli_with_isolated_home <home_dir> <cwd> <out_var> <err_var> <rc_var> <args...>
# Unsets 2NB_TEST so Obsidian registry resolution runs strictly against <home_dir>.
run_cli_with_isolated_home() {
    local _home="$1" _cwd="$2" _out_var="$3" _err_var="$4" _rc_var="$5"
    shift 5
    assert_safe_test_vault "$_home"
    local _out_f _err_f _rc=0
    _out_f=$(mktemp /tmp/sb-cli-out.XXXXXX)
    _err_f=$(mktemp /tmp/sb-cli-err.XXXXXX)
    _track_temp_path "$_out_f"
    _track_temp_path "$_err_f"
    (cd "$_cwd" && env -u 2NB_TEST HOME="$_home" XDG_CONFIG_HOME="$_home/config" XDG_CACHE_HOME="$_home/cache" "$CLI_BIN" "$@") >"$_out_f" 2>"$_err_f" || _rc=$?
    printf -v "$_out_var" '%s' "$(cat "$_out_f")"
    printf -v "$_err_var" '%s' "$(cat "$_err_f")"
    printf -v "$_rc_var" '%d' "$_rc"
    rm -f "$_out_f" "$_err_f"
}

# ------------------------------------------------------------------------------
# Contract & Capability Gating (Zero-Mock Policy, --strict-contracts aware)
# ------------------------------------------------------------------------------

contract_unavailable() {
    local msg="$1"
    if [ "${STRICT_CONTRACTS:-0}" -eq 1 ]; then
        echo "STRICT CONTRACT FAILURE: $msg" >&2
        exit 1
    fi
    echo "Contract not yet implemented: $msg" >&2
    exit 77
}

require_cli_flag() {
    local subcmd="$1"
    local flag="$2"
    local help_out
    help_out=$(env 2NB_TEST=1 "$CLI_BIN" "$subcmd" --help 2>&1 || true)
    if ! grep -Fq -- "$flag" <<< "$help_out"; then
        contract_unavailable "2nb $subcmd missing flag $flag"
    fi
}

cli_has_flag() {
    local subcmd="$1"
    local flag="$2"
    local help_out
    help_out=$(env 2NB_TEST=1 "$CLI_BIN" "$subcmd" --help 2>&1 || true)
    grep -Fq -- "$flag" <<< "$help_out"
}

detect_polish_backlinks_apply_flags() {
    if cli_has_flag "polish" "--apply-backlinks"; then
        echo "--write --backlinks --apply-backlinks"
    else
        echo "--write --backlinks"
    fi
}

require_gui_app() {
    if [ "${SKIP_GUI:-0}" -eq 1 ]; then
        echo "GUI tests skipped (--skip-gui)" >&2
        exit 77
    fi
    if [ ! -x "$APP/Contents/MacOS/SecondBrain" ]; then
        echo "SecondBrain.app not installed at $APP" >&2
        exit 77
    fi
}

require_m3_gui_target() {
    local target="$1"
    local text_val="${2:-}"
    local err_f _rc=0
    err_f=$(mktemp /tmp/sb-gui-probe.XXXXXX)
    _track_temp_path "$err_f"
    if [ "$target" = "polish.searchField" ] || [ "$target" = "polish.goalField" ]; then
        gui_ctl set_text "target=$target" "text=$text_val" "submit=false" >/dev/null 2>"$err_f" || _rc=$?
    else
        gui_ctl click_button "target=$target" >/dev/null 2>"$err_f" || _rc=$?
    fi
    local err_msg
    err_msg=$(cat "$err_f")
    rm -f "$err_f"
    if [ "$_rc" -ne 0 ]; then
        if grep -qE "Unsupported text target:|Button not found:" <<< "$err_msg"; then
            contract_unavailable "M3 GUI target '$target' not supported ($err_msg)"
        fi
        echo "GUI probe error for $target: $err_msg" >&2
        return 1
    fi
    return 0
}

probe_llama_local_capability() {
    local kind="$1"
    local vault_dir="$2"
    local model="embeddinggemma-2"
    [ "$kind" = "generation" ] && model="gemma4-e2b"
    local eng out
    eng=$(env 2NB_TEST=1 "$CLI_BIN" ai engine status --json 2>/dev/null || true)
    jq -e '.engine_path != null and .engine_path != ""' <<< "$eng" >/dev/null 2>&1 || return 1
    out=$(env 2NB_TEST=1 "$CLI_BIN" models test "$model" --provider llama-local --type "$kind" --vault "$vault_dir" --json --porcelain 2>/dev/null || true)
    jq -e '.ok == true' <<< "$out" >/dev/null 2>&1
}

probe_ollama_capability() {
    local kind="$1"
    local vault_dir="$2"
    local status_json model out
    status_json=$(env 2NB_TEST=1 "$CLI_BIN" ai status --vault "$vault_dir" --json --porcelain 2>/dev/null || true)
    jq -e 'any(.providers[]?; .name == "ollama" and .reachable == true)' <<< "$status_json" >/dev/null 2>&1 || return 1
    model=$(env 2NB_TEST=1 "$CLI_BIN" models list --provider ollama --vault "$vault_dir" --json --porcelain 2>/dev/null \
        | python3 -c 'import json,sys
try:
    d=json.loads(sys.stdin.read())
    rows=d if isinstance(d,list) else d.get("models",[])
    for r in rows:
        mid=r.get("id") or r.get("model_id") or ""
        if mid:
            print(mid)
            sys.exit(0)
except Exception:
    pass
sys.exit(1)' 2>/dev/null || true)
    [ -n "$model" ] || return 1
    out=$(env 2NB_TEST=1 "$CLI_BIN" models test "$model" --provider ollama --type "$kind" --vault "$vault_dir" --json --porcelain 2>/dev/null || true)
    jq -e '.ok == true' <<< "$out" >/dev/null 2>&1 || return 1
    echo "$model"
}

# require_live_generation <vault_dir>
# Probes Gemini, then llama-local, then Ollama using real API calls.
# Sets LIVE_GEN_PROVIDER and LIVE_GEN_MODEL and configures <vault_dir> if non-gemini is chosen.
# Exits 77 (SKIP) if no live provider can serve generation.
require_live_generation() {
    local vault_dir="$1"
    LIVE_GEN_PROVIDER=""
    LIVE_GEN_MODEL=""

    if probe_gemini_capability "generation" "$vault_dir"; then
        LIVE_GEN_PROVIDER="gemini"
        LIVE_GEN_MODEL="gemini-4-hydrogen"
        export LIVE_GEN_PROVIDER LIVE_GEN_MODEL
        return 0
    fi

    if probe_llama_local_capability "generation" "$vault_dir"; then
        LIVE_GEN_PROVIDER="llama-local"
        LIVE_GEN_MODEL="gemma4-e2b"
        env 2NB_TEST=1 "$CLI_BIN" config set ai.generation_provider llama-local --vault "$vault_dir" >/dev/null 2>&1 || \
            env 2NB_TEST=1 "$CLI_BIN" config set ai.provider llama-local --vault "$vault_dir" >/dev/null
        env 2NB_TEST=1 "$CLI_BIN" config set ai.generation_model "$LIVE_GEN_MODEL" --vault "$vault_dir" >/dev/null
        export LIVE_GEN_PROVIDER LIVE_GEN_MODEL
        return 0
    fi

    local ollama_model
    ollama_model=$(probe_ollama_capability "generation" "$vault_dir" || true)
    if [ -n "$ollama_model" ]; then
        LIVE_GEN_PROVIDER="ollama"
        LIVE_GEN_MODEL="$ollama_model"
        env 2NB_TEST=1 "$CLI_BIN" config set ai.generation_provider ollama --vault "$vault_dir" >/dev/null 2>&1 || \
            env 2NB_TEST=1 "$CLI_BIN" config set ai.provider ollama --vault "$vault_dir" >/dev/null
        env 2NB_TEST=1 "$CLI_BIN" config set ai.generation_model "$LIVE_GEN_MODEL" --vault "$vault_dir" >/dev/null
        export LIVE_GEN_PROVIDER LIVE_GEN_MODEL
        return 0
    fi

    echo "No live AI generation provider reachable (gemini, llama-local, ollama)" >&2
    exit 77
}

# ------------------------------------------------------------------------------
# GUI Telemetry & Synchronization Helpers
# ------------------------------------------------------------------------------

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
                        and (not target_phase or ev.get("phase") == target_phase)
                    ):
                        print(json.dumps(ev))
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

click_ax_expect_disabled() {
    local target="$1"
    local err_f _rc=0
    err_f=$(mktemp /tmp/sb-gui-dis.XXXXXX)
    _track_temp_path "$err_f"
    gui_ctl click_button "target=$target" >/dev/null 2>"$err_f" || _rc=$?
    local err_msg
    err_msg=$(cat "$err_f")
    rm -f "$err_f"
    if [ "$_rc" -eq 0 ]; then
        echo "Expected button '$target' to be disabled, but click succeeded" >&2
        return 1
    fi
    if ! grep -Fq "Button disabled: $target" <<< "$err_msg"; then
        echo "Expected 'Button disabled: $target', got: $err_msg" >&2
        return 1
    fi
    return 0
}

click_when_enabled() {
    local target="$1"
    local timeout="${2:-10}"
    local deadline
    deadline=$(python3 -c "import time; print(time.time() + float('$timeout'))")
    local err=""
    while python3 -c "import sys,time; sys.exit(0 if time.time() < float('$deadline') else 1)"; do
        local err_f _rc=0
        err_f=$(mktemp /tmp/sb-gui-clk.XXXXXX)
        _track_temp_path "$err_f"
        gui_ctl click_button "target=$target" >/dev/null 2>"$err_f" || _rc=$?
        err=$(cat "$err_f")
        rm -f "$err_f"
        if [ "$_rc" -eq 0 ]; then
            sleep 0.2
            return 0
        fi
        if ! grep -qE "Button disabled:|Button not found:" <<< "$err"; then
            echo "$err" >&2
            return 1
        fi
        sleep 0.2
    done
    echo "click_when_enabled($target) timed out: $err" >&2
    return 1
}

# ------------------------------------------------------------------------------
# Snapshot & SQLite Metrics Verifiers
# ------------------------------------------------------------------------------

copy_originals() {
    local src_dir="$1"
    local dst_dir="$2"
    shift 2
    mkdir -p "$dst_dir"
    local rel
    for rel in "$@"; do
        mkdir -p "$dst_dir/$(dirname "$rel")"
        cp -p "$src_dir/$rel" "$dst_dir/$rel"
    done
}

assert_files_unchanged() {
    local live_dir="$1"
    local orig_dir="$2"
    shift 2
    local rel
    for rel in "$@"; do
        if ! cmp -s "$live_dir/$rel" "$orig_dir/$rel"; then
            echo "File mismatch vs original for $rel" >&2
            diff -u "$orig_dir/$rel" "$live_dir/$rel" >&2 || true
            return 1
        fi
    done
    return 0
}

assert_file_changed() {
    local live_dir="$1"
    local orig_dir="$2"
    local rel="$3"
    if cmp -s "$live_dir/$rel" "$orig_dir/$rel"; then
        echo "Expected $rel to change on disk, but it remained identical to original" >&2
        return 1
    fi
    return 0
}

assert_any_file_changed() {
    local live_dir="$1"
    local orig_dir="$2"
    shift 2
    local rel
    for rel in "$@"; do
        if ! cmp -s "$live_dir/$rel" "$orig_dir/$rel"; then
            return 0
        fi
    done
    echo "Expected at least one of ($*) to change on disk, but all matched originals" >&2
    return 1
}

metrics_ledger_calls() {
    local vault_dir="$1"
    local op="${2:-}"
    local db="$vault_dir/.2ndbrain/metrics.db"
    if [ ! -f "$db" ]; then
        echo "0"
        return 0
    fi
    local sql="SELECT COALESCE(SUM(calls), 0) FROM ai_usage_ledger;"
    if [ -n "$op" ]; then
        sql="SELECT COALESCE(SUM(calls), 0) FROM ai_usage_ledger WHERE operation = '$op';"
    fi
    sqlite3 "$db" "$sql" 2>/dev/null || echo "0"
}

assert_metrics_ledger_recorded() {
    local vault_dir="$1"
    local op="$2"
    local pre_calls="${3:-0}"
    local db="$vault_dir/.2ndbrain/metrics.db"
    [ -f "$db" ] || { echo "Missing metrics.db at $db" >&2; return 1; }
    local rows_json
    rows_json=$(sqlite3 -json "$db" \
        "SELECT day, provider, model, slot, operation, is_cloud, calls, input_tokens, output_tokens, cost_usd FROM ai_usage_ledger WHERE operation = '$op';")
    python3 - "$rows_json" "$op" "$pre_calls" <<'PY'
import json, sys
rows = json.loads(sys.argv[1] or "[]")
op = sys.argv[2]
pre_calls = int(sys.argv[3])
assert len(rows) > 0, f"Expected ai_usage_ledger rows for operation={op}, got none"
total_calls = sum(int(r["calls"]) for r in rows)
total_in = sum(int(r["input_tokens"]) for r in rows)
assert total_calls > pre_calls, f"Expected calls > {pre_calls} for {op}, got {total_calls}"
assert total_in > 0, f"Expected input_tokens > 0 for {op}, got {total_in}"
for r in rows:
    prov = (r.get("provider") or "").lower()
    is_cloud = int(r.get("is_cloud", 0))
    cost = float(r.get("cost_usd", 0.0))
    if prov in ("llama-local", "ollama"):
        assert is_cloud == 0, f"Expected is_cloud=0 for local provider {prov}, got {r}"
        assert cost == 0.0, f"Expected cost_usd=0 for local provider {prov}, got {r}"
    else:
        assert is_cloud == 1, f"Expected is_cloud=1 for cloud provider {prov}, got {r}"
        assert cost >= 0.0, f"Expected non-negative cost_usd for {prov}, got {r}"
PY
    local ok_ops
    ok_ops=$(sqlite3 "$db" "SELECT COUNT(*) FROM operations WHERE operation = '$op' AND ok = 1;")
    [ "${ok_ops:-0}" -ge 1 ] || { echo "Expected ok=1 row in operations for $op" >&2; return 1; }
}

# ------------------------------------------------------------------------------
# Fixture Builders (Isolated /tmp Obsidian Vaults)
# ------------------------------------------------------------------------------

seed_notes_revamp_vault() {
    local v="$1"
    assert_safe_test_vault "$v"
    mkdir -p "$v/notes" "$v/concepts" "$v/guides"

    cat > "$v/concepts/distributed-consensus.md" <<'EOF'
---
title: Distributed Consensus
type: note
status: complete
---

# Distributed Consensus

Paxos and Raft coordinate replicated state machines across unreliable networks.
EOF

    cat > "$v/notes/proj.md" <<'EOF'
---
title: Proj
type: note
status: open
created: "2026-04-20T10:00:00Z"
---

# Proj

Tracking milestone progress.
EOF

    cat > "$v/notes/bad.md" <<'EOF'
---
title: Unclosed: colon: here
status: draft
---

# Bad YAML

Body prose after malformed frontmatter.
EOF

    cat > "$v/notes/missing.md" <<'EOF'
---
title: Outage
type: postmortem
---

# Outage Postmortem

Incident Date: 2026-11-01
Status: complete

Root cause summary.
EOF

    cat > "$v/notes/link.md" <<'EOF'
---
title: Link Test
type: note
status: complete
---

# Link Test

See [[non-obvious-concept]] for consensus details.
EOF
}

seed_large_vault() {
    local v="$1"
    local count="${2:-225}"
    assert_safe_test_vault "$v"
    python3 - "$v" "$count" <<'PY'
import os, sys, time

v, count = sys.argv[1], int(sys.argv[2])
folders = ["areas", "projects", "resources", "archive"]
for f in folders:
    os.makedirs(os.path.join(v, f), exist_ok=True)

now = time.time()
for i in range(1, count + 1):
    folder = folders[(i - 1) % len(folders)]
    rel = f"{folder}/note-{i:03d}.md"
    path = os.path.join(v, rel)
    if i == 225:
        title = "Zebra Final Spec"
        body = "Deep archive architecture specification containing needle-225-alpha for FTS5 search."
    elif i == 220:
        title = "Apollo Backlog Spec"
        body = "Historical design review containing needle-220 for large vault picker validation."
    else:
        title = f"Vault Note {i:03d}"
        body = f"Routine engineering log entry {i} covering cloud operations and testing."
    content = (
        f"---\n"
        f"title: {title}\n"
        f"type: note\n"
        f"status: complete\n"
        f"---\n\n"
        f"# {title}\n\n"
        f"{body}\n"
    )
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(content)
    # Give note-225 the oldest mtime so a modified-DESC top-200 slice would miss it.
    mtime = now - (i * 10)
    os.utime(path, (mtime, mtime))
PY
}

seed_backlink_graph() {
    local v="$1"
    assert_safe_test_vault "$v"
    mkdir -p "$v/notes"

    cat > "$v/hub.md" <<'EOF'
---
title: Auth Architecture
type: note
status: complete
---

# Auth Architecture

Legacy session tokens are being replaced by OAuth2 PKCE authentication flows.
EOF

    cat > "$v/notes/api.md" <<'EOF'
---
title: API Service
type: note
status: complete
---

# API Service

See [[Auth Architecture]] for legacy tokens and session validation.
EOF

    cat > "$v/notes/runbook.md" <<'EOF'
---
title: Auth Runbook
type: runbook
status: active
---

# Auth Runbook

Check [[auth-architecture|Auth]] before deploy.
EOF

    cat > "$v/notes/code-ref.md" <<'EOF'
---
title: Code Snippet Reference
type: note
status: complete
---

# Code Snippet Reference

```markdown
Example literal link: [[Auth Architecture]]
```
EOF
}

seed_crlf_anchor_vault() {
    local v="$1"
    assert_safe_test_vault "$v"
    mkdir -p "$v/Guides"
    cat > "$v/Guides/Target.md" <<'EOF'
---
title: Target
type: note
status: complete
---

# Target

Canonical target note.
EOF

    python3 - "$v/crlf.md" <<'PY'
import sys
content = (
    "---\r\n"
    "id: \"007\"\r\n"
    "title: CRLF Anchors\r\n"
    "type: note\r\n"
    "status: complete # keep comment\r\n"
    "created: \"2026-04-20T10:00:00Z\"\r\n"
    "base_tag: &bt prod\r\n"
    "mirror_tag: *bt\r\n"
    "---\r\n"
    "\r\n"
    "# CRLF Anchors\r\n"
    "\r\n"
    "Inline code `[[fake-inline]]` stays untouched.\r\n"
    "\r\n"
    "```markdown\r\n"
    "[[fake-fence]]\r\n"
    "```\r\n"
    "\r\n"
    "Real drifted link: [[Guides\\Target.md]].\r\n"
)
with open(sys.argv[1], "wb") as f:
    f.write(content.encode("utf-8"))
PY
}

seed_migration_vault() {
    local v="$1"
    assert_safe_test_vault "$v"
    mkdir -p "$v/projects" "$v/adrs" "$v/runbooks" "$v/notes"

    cat > "$v/projects/bedrock-migration-plan.md" <<'EOF'
---
title: Bedrock Migration Plan
type: note
status: open
created: "2026-04-20T10:00:00Z"
modified: "2026-04-21T12:00:00Z"
---

# Bedrock Migration Plan

Move workloads to Gemini via [[old/001-vertex-adc]] and review [[legacy-mantle-notes]].
EOF

    cat > "$v/projects/bedrock-migration-checklist.md" <<'EOF'
---
title: Bedrock Migration Checklist
type: note
status: complete
---

# Checklist

Verify [[bedrock migration plan]] and [[projects/gemini-routing]].
EOF

    cat > "$v/projects/gemini-routing.md" <<'EOF'
---
title: Gemini Routing
type: note
status: complete
---

# Gemini Routing

Routes requests directly to Google Vertex AI on `your-gcp-project-id`.
EOF

    cat > "$v/adrs/001-vertex-adc.md" <<'EOF'
---
title: ADR 001 Vertex ADC
type: adr
---

# ADR 001: Vertex ADC

Use Application Default Credentials for GCP authentication.
EOF

    cat > "$v/runbooks/rotate-keys.md" <<'EOF'
---
title: Rotate Keys
type: runbook
status: complete
---

# Rotate Keys

Steps to rotate Secret Manager credentials.
EOF

    for i in 1 2 3 4 5 6 7; do
        cat > "$v/notes/ref-$i.md" <<EOF
---
title: Reference Note $i
type: note
status: complete
---

# Reference Note $i

Supports [[Bedrock Migration Plan]] phase $i.
EOF
    done
}

seed_postmortem_vault() {
    local v="$1"
    assert_safe_test_vault "$v"
    mkdir -p "$v/incidents" "$v/runbooks" "$v/daily" "$v/notes"

    cat > "$v/incidents/2026-11-01-api-outage.md" <<'EOF'
---
title: API Outage 2026-11-01
type: postmortem
---

# API Outage

Incident Date: 2026-11-01
Status: reviewed

Primary region database connection pool exhausted during traffic spike.
EOF

    cat > "$v/runbooks/failover.md" <<'EOF'
---
title: Failover: Primary -> Secondary
type: runbook
status: open
---

# Failover Runbook

Execute traffic drain to secondary cluster.
EOF

    cat > "$v/daily/2026-11-02.md" <<'EOF'
---
title: Daily 2026-11-02
type: note
status: complete
---

# 2026-11-02

Reviewed [[2026-11-01-api-outage]] and [[Failover]].
EOF

    for i in 1 2 3 4 5; do
        cat > "$v/notes/svc-$i.md" <<EOF
---
title: Service $i
type: note
status: complete
---

# Service $i

Service $i documentation.
EOF
    done
}

# ------------------------------------------------------------------------------
# Global Safety & Guardrail Invariants (S1-S5)
# ------------------------------------------------------------------------------

capture_safety_baseline() {
    local protected_vault="${HOME}/dev/obsidian"
    if [ -d "${protected_vault}/.git" ]; then
        PRE_OBSIDIAN_GIT_STATUS=$(git -C "${protected_vault}" status --short 2>/dev/null || true)
    else
        PRE_OBSIDIAN_GIT_STATUS=""
    fi
    PRE_LAST_VAULT_PATH=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || true)
    export PRE_OBSIDIAN_GIT_STATUS PRE_LAST_VAULT_PATH
}

assert_protected_obsidian_clean() {
    local protected_vault="${HOME}/dev/obsidian"
    if [ -d "${protected_vault}/.git" ]; then
        local cur_git
        cur_git=$(git -C "${protected_vault}" status --short 2>/dev/null || true)
        if [ "$cur_git" != "${PRE_OBSIDIAN_GIT_STATUS:-}" ]; then
            echo "FATAL GUARDRAIL VIOLATION (S1): ${protected_vault} git status changed!" >&2
            echo "Before: ${PRE_OBSIDIAN_GIT_STATUS:-<empty>}" >&2
            echo "After:  $cur_git" >&2
            exit 99
        fi
    fi

    local cur_defaults
    cur_defaults=$(defaults read dev.apresai.2ndbrain lastVaultPath 2>/dev/null || true)
    if [ "$cur_defaults" != "${PRE_LAST_VAULT_PATH:-}" ]; then
        echo "FATAL GUARDRAIL VIOLATION (S2): defaults read dev.apresai.2ndbrain lastVaultPath changed!" >&2
        echo "Before: ${PRE_LAST_VAULT_PATH:-<unset>}" >&2
        echo "After:  ${cur_defaults:-<unset>}" >&2
        exit 99
    fi
}

assert_settings_ai_guardrail() {
    local target="$REPO_ROOT/app/Sources/SecondBrain/SettingsAIView.swift"
    if [ ! -f "$target" ]; then
        echo "Missing $target" >&2
        return 1
    fi
    local matches
    matches=$(grep -nE "llama-server|launchd|pid|port=|brew |engineStatus" "$target" || true)
    if [ -n "$matches" ]; then
        echo "FATAL GUARDRAIL VIOLATION (S3): Prohibited pattern in SettingsAIView.swift:" >&2
        echo "$matches" >&2
        exit 99
    fi
    return 0
}

assert_repo_makefile_gates() {
    make -C "$REPO_ROOT" check-index-generation >/dev/null || {
        echo "S5 FAILED: make check-index-generation" >&2
        return 1
    }
    make -C "$REPO_ROOT" check-docs-links >/dev/null || {
        echo "S5 FAILED: make check-docs-links" >&2
        return 1
    }
    make -C "$REPO_ROOT" check-claude-md-size >/dev/null || {
        echo "S5 FAILED: make check-claude-md-size" >&2
        return 1
    }
    return 0
}
