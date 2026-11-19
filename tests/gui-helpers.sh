#!/bin/bash
# Shared helpers for isolated Beta GUI test suites.
# Drives SecondBrain.app via its first-party --gui-control-dir IPC controller
# and verifies state/telemetry via --gui-log-dir.
# Never mutates developer UserDefaults (`lastVaultPath`) or live Obsidian vaults.

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_BIN="${TWONB_CLI_PATH:-$REPO_ROOT/cli/bin/2nb}"
APP="${APP:-$HOME/Applications/SecondBrain.app}"
SCREENSHOTS="${SCREENSHOTS:-/tmp/sb-gui-tests}"
GUI_LOG_DIR="${GUI_LOG_DIR:-/tmp/sb-gui-logs-$$}"
GUI_CONTROL_DIR="$GUI_LOG_DIR/control"
APP_PID=""
PASS=0
FAIL=0
TESTS=()

mkdir -p "$SCREENSHOTS" "$GUI_LOG_DIR" "$GUI_CONTROL_DIR"

pass() { PASS=$((PASS+1)); TESTS+=("PASS: $1"); echo "✓ $1"; }
fail() { FAIL=$((FAIL+1)); TESTS+=("FAIL: $1 — $2"); echo "✗ $1 — $2"; }

assert_safe_test_vault() {
    local v="${1:-}"
    if [ -z "$v" ]; then
        echo "FATAL GUARDRAIL VIOLATION: Test vault path is empty!" >&2
        exit 99
    fi
    if [ "$v" = "/" ] || [ "$v" = "/tmp" ] || [ "$v" = "/tmp/" ] || [ "$v" = "/private/tmp" ] || [ "$v" = "/private/tmp/" ]; then
        echo "FATAL GUARDRAIL VIOLATION: Test vault path cannot be root or bare /tmp!" >&2
        exit 99
    fi
    local real_v
    real_v="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$v" 2>/dev/null || echo "$v")"
    if [ "$real_v" = "/" ] || [ "$real_v" = "/tmp" ] || [ "$real_v" = "/private/tmp" ]; then
        echo "FATAL GUARDRAIL VIOLATION: Resolved test vault path cannot be root or bare /tmp!" >&2
        exit 99
    fi
    if [[ "$real_v" == *"/dev/obsidian"* ]] || [[ "$real_v" == *"/Library/Application Support/obsidian"* ]] || [[ "$real_v" == "${HOME:-/nonexistent}/dev/obsidian"* ]]; then
        echo "FATAL GUARDRAIL VIOLATION: Test vault '$real_v' matches protected Obsidian vault!" >&2
        exit 99
    fi
    if [[ "$real_v" != "/tmp/"* ]] && [[ "$real_v" != "/private/tmp/"* ]]; then
        echo "FATAL GUARDRAIL VIOLATION: Test vault '$real_v' must be located in /tmp or /private/tmp!" >&2
        exit 99
    fi
}

file_contains() {
    grep -Fq -- "$2" "$1"
}

print_results() {
    echo ""
    echo "==============================="
    echo "  Results: $PASS passed, $FAIL failed"
    echo "==============================="
    for t in "${TESTS[@]}"; do
        echo "  $t"
    done
    echo ""
    echo "Screenshots: $SCREENSHOTS/"
    echo "GUI Logs:    $GUI_LOG_DIR/"
}

ensure_cli_and_app() {
    if [ ! -x "$CLI_BIN" ]; then
        make -C "$REPO_ROOT" build-cli >/dev/null
    fi
    if [ "${SKIP_BUILD:-}" != "1" ] || [ ! -x "$APP/Contents/MacOS/SecondBrain" ]; then
        echo "Building & installing SecondBrain.app..."
        make -C "$REPO_ROOT" install-app >/dev/null
    fi
}

setup_temp_vault() {
    local dir="$1"
    assert_safe_test_vault "$dir"
    mkdir -p "$dir/.obsidian"
    env 2NB_TEST=1 "$CLI_BIN" vault create "$dir" >/dev/null
    env 2NB_TEST=1 "$CLI_BIN" config set ai.provider gemini --vault "$dir" >/dev/null
    env 2NB_TEST=1 "$CLI_BIN" config set ai.embedding_model gemini-embedding-2 --vault "$dir" >/dev/null
    env 2NB_TEST=1 "$CLI_BIN" config set ai.generation_model gemini-4-hydrogen --vault "$dir" >/dev/null
    env 2NB_TEST=1 "$CLI_BIN" config set ai.dimensions 768 --vault "$dir" >/dev/null
}

seed_note() {
    local vault_dir="$1"
    local title="$2"
    local body="$3"
    local created_json rel_path
    created_json=$(env 2NB_TEST=1 "$CLI_BIN" create --title "$title" --type note --vault "$vault_dir" --json --porcelain)
    rel_path=$(python3 -c 'import json,sys; print(json.loads(sys.stdin.read())["path"])' <<< "$created_json")
    printf "\n%s\n" "$body" >> "$vault_dir/$rel_path"
    echo "$rel_path"
}

kill_app() {
    if [ -n "${APP_PID:-}" ]; then
        kill "$APP_PID" 2>/dev/null || true
        APP_PID=""
    fi
    pkill -9 -x SecondBrain 2>/dev/null || true
    sleep 0.4
}

wait_gui_control_ready() {
    local timeout="${1:-15}"
    local ready_file="$GUI_CONTROL_DIR/ready.json"
    python3 - "$ready_file" "$timeout" <<'PY'
import json, os, sys, time

ready_file, timeout_s = sys.argv[1], float(sys.argv[2])
deadline = time.time() + timeout_s
while time.time() < deadline:
    if os.path.exists(ready_file):
        try:
            with open(ready_file, "r", encoding="utf-8") as f:
                data = json.load(f)
            if data.get("ready") is True:
                sys.exit(0)
        except Exception:
            pass
    time.sleep(0.1)

print(f"wait_gui_control_ready TIMEOUT ({timeout_s}s): {ready_file}", file=sys.stderr)
sys.exit(1)
PY
}

launch_app_on_vault() {
    local vault_dir="$1"
    assert_safe_test_vault "$vault_dir"
    shift || true
    ensure_cli_and_app
    kill_app
    rm -rf "$GUI_LOG_DIR"
    GUI_CONTROL_DIR="$GUI_LOG_DIR/control"
    mkdir -p "$GUI_LOG_DIR" "$GUI_CONTROL_DIR"

    "$APP/Contents/MacOS/SecondBrain" \
        --vault "$vault_dir" \
        --skip-onboarding \
        --no-startup-sync \
        --gui-log-dir "$GUI_LOG_DIR" \
        --gui-control-dir "$GUI_CONTROL_DIR" \
        "$@" \
        >"$GUI_LOG_DIR/process.log" 2>&1 &
    APP_PID=$!

    wait_gui_control_ready 15
    TARGET_VAULT="$vault_dir" wait_gui_state 'state.get("vaultPath") in {os.environ["TARGET_VAULT"], os.path.realpath(os.environ["TARGET_VAULT"])}' 15
    sleep 0.25
}

gui_ctl() {
    local op="$1"
    shift || true
    python3 - "$GUI_CONTROL_DIR" "$op" "$@" <<'PY'
import fcntl, json, os, sys, time

control_dir = sys.argv[1]
op = sys.argv[2]
payload = {"op": op}
for kv in sys.argv[3:]:
    if "=" not in kv:
        raise ValueError(f"Expected key=value argument, got: {kv}")
    key, val = kv.split("=", 1)
    if key == "submit":
        payload[key] = val.lower() in ("1", "true", "yes")
    elif key == "modifiers":
        payload[key] = [m for m in val.split(",") if m]
    else:
        payload[key] = val

os.makedirs(control_dir, exist_ok=True)
lock_path = os.path.join(control_dir, ".seq.lock")
seq_path = os.path.join(control_dir, ".seq")
with open(lock_path, "a+") as lf:
    fcntl.flock(lf, fcntl.LOCK_EX)
    seq = 1
    if os.path.exists(seq_path):
        try:
            with open(seq_path, "r", encoding="utf-8") as sf:
                seq = int(sf.read().strip() or "0") + 1
        except Exception:
            seq = 1
    with open(seq_path, "w", encoding="utf-8") as sf:
        sf.write(str(seq))
    fcntl.flock(lf, fcntl.LOCK_UN)

req_id = f"{seq:04d}"
tmp_path = os.path.join(control_dir, f".req-{req_id}.tmp")
req_path = os.path.join(control_dir, f"req-{req_id}.json")
resp_path = os.path.join(control_dir, f"resp-{req_id}.json")

with open(tmp_path, "w", encoding="utf-8") as tf:
    json.dump(payload, tf)
os.replace(tmp_path, req_path)

deadline = time.time() + 15.0
while time.time() < deadline:
    if os.path.exists(resp_path):
        try:
            with open(resp_path, "r", encoding="utf-8") as rf:
                resp = json.load(rf)
            os.remove(resp_path)
            if resp.get("ok"):
                val = resp.get("value")
                if val is not None:
                    print(val)
                sys.exit(0)
            else:
                print(f"gui_ctl({op}) FAILED: {resp.get('error')}", file=sys.stderr)
                sys.exit(1)
        except json.JSONDecodeError:
            pass
    time.sleep(0.05)

print(f"gui_ctl({op}) TIMEOUT waiting for {resp_path}", file=sys.stderr)
sys.exit(1)
PY
}

screenshot() {
    gui_ctl screenshot "path=$SCREENSHOTS/$1.png" >/dev/null || true
}

inspect_menus() {
    gui_ctl inspect_menus
}

click_menu_item() {
    local menu="$1"
    local item="$2"
    gui_ctl click_menu "menu=$menu" "item=$item" >/dev/null
    sleep 0.25
}

click_ax_button() {
    local target="$1"
    gui_ctl click_button "target=$target" >/dev/null
    sleep 0.2
}

set_field_text() {
    local target="$1"
    local text="$2"
    local submit="${3:-false}"
    gui_ctl set_text "target=$target" "text=$text" "submit=$submit" >/dev/null
    sleep 0.15
}

hud_set_text() {
    local text="$1"
    gui_ctl set_text "target=hud.inputField" "text=$text" "submit=false" >/dev/null
    sleep 0.2
}

select_polish_note() {
    local rel_path="$1"
    gui_ctl select_polish_note "path=$rel_path" >/dev/null
    sleep 0.15
}

press_return() {
    gui_ctl return_key >/dev/null
    sleep 0.2
}

press_escape() {
    gui_ctl escape >/dev/null
    sleep 0.2
}

cmd_key() {
    gui_ctl shortcut "key=$1" "modifiers=command" >/dev/null
    sleep 0.25
}

cmd_shift_key() {
    gui_ctl shortcut "key=$1" "modifiers=command,shift" >/dev/null
    sleep 0.25
}

wait_gui_state() {
    local expr="$1"
    local timeout="${2:-15}"
    local state_file="$GUI_LOG_DIR/gui-state.json"
    local actions_file="$GUI_LOG_DIR/gui-actions.jsonl"

    if python3 - "$state_file" "$actions_file" "$expr" "$timeout" <<'PY'
import json, os, sys, time

state_file, actions_file, expr, timeout_s = sys.argv[1], sys.argv[2], sys.argv[3], float(sys.argv[4])
deadline = time.time() + timeout_s
last_state = None
while time.time() < deadline:
    if os.path.exists(state_file):
        try:
            with open(state_file, "r", encoding="utf-8") as f:
                state = json.load(f)
            last_state = state
            if eval(expr, {"state": state, "os": os}):
                sys.exit(0)
        except Exception:
            pass
    time.sleep(0.15)

print(f"wait_gui_state TIMEOUT ({timeout_s}s) for: {expr}", file=sys.stderr)
print(f"Last gui-state.json: {json.dumps(last_state, indent=2)}", file=sys.stderr)
if os.path.exists(actions_file):
    with open(actions_file, "r", encoding="utf-8") as f:
        tail = f.readlines()[-12:]
    print("Recent gui-actions.jsonl:", file=sys.stderr)
    for line in tail:
        print("  " + line.rstrip(), file=sys.stderr)
sys.exit(1)
PY
    then
        return 0
    else
        return 1
    fi
}

gui_state_field() {
    local field="$1"
    python3 - "$GUI_LOG_DIR/gui-state.json" "$field" <<'PY'
import json, sys
with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
val = data.get(sys.argv[2], "")
print("" if val is None else val)
PY
}

assert_gui_action() {
    local action="$1"
    local phase="${2:-}"
    local label="${3:-Action $action ${phase:+($phase)}}"
    local actions_file="$GUI_LOG_DIR/gui-actions.jsonl"

    if python3 - "$actions_file" "$action" "$phase" <<'PY'
import json, sys
actions_file, target_action, target_phase = sys.argv[1], sys.argv[2], sys.argv[3]
with open(actions_file, "r", encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        ev = json.loads(line)
        if ev.get("action") == target_action:
            if not target_phase or ev.get("phase") == target_phase:
                sys.exit(0)
sys.exit(1)
PY
    then
        pass "$label"
    else
        fail "$label" "missing in $actions_file"
    fi
}

assert_no_leaked_secrets() {
    local label="${1:-Telemetry logs contain no unredacted API secrets}"
    if python3 - "$GUI_LOG_DIR" <<'PY'
import os, re, sys
pattern = re.compile(r"AIza[0-9A-Za-z_-]{20,}|sk-(?:or-v1-|proj-|ant-)?[0-9A-Za-z_-]{16,}|ABSK[0-9A-Za-z+/=_-]{16,}|AKIA[0-9A-Z]{16,}")
live_key = os.environ.get("GEMINI_API_KEY", "").strip()
log_dir = sys.argv[1]
for name in ("gui-actions.jsonl", "gui-state.json", "process.log"):
    path = os.path.join(log_dir, name)
    if os.path.exists(path):
        with open(path, "r", encoding="utf-8", errors="ignore") as f:
            text = f.read()
        if pattern.search(text):
            print(f"SECRET PATTERN FOUND in {path}", file=sys.stderr)
            sys.exit(1)
        if len(live_key) >= 8 and live_key in text:
            print(f"LIVE GEMINI_API_KEY LEAKED in {path}", file=sys.stderr)
            sys.exit(1)
sys.exit(0)
PY
    then
        pass "$label"
    else
        fail "$label" "secret pattern detected in $GUI_LOG_DIR"
    fi
}

probe_gemini_capability() {
    local kind="$1"
    local vault_dir="$2"
    local model="gemini-embedding-2"
    if [ "$kind" = "generation" ]; then
        model="gemini-4-hydrogen"
    fi
    local out
    out=$(env 2NB_TEST=1 "$CLI_BIN" models test "$model" --provider gemini --type "$kind" --vault "$vault_dir" --json --porcelain 2>/dev/null || true)
    python3 -c 'import json,sys
try:
    data = json.loads(sys.stdin.read())
    sys.exit(0 if data.get("ok") is True else 1)
except Exception:
    sys.exit(1)' <<< "$out"
}
