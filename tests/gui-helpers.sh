#!/bin/bash
# Shared helpers for GUI test scripts

VAULT="${VAULT:-/Users/chad/dev/2ndbrain/vault}"
SCREENSHOTS="${SCREENSHOTS:-/tmp/sb-gui-tests}"
APP="${APP:-$HOME/Applications/SecondBrain.app}"
PASS=0
FAIL=0
TESTS=()

mkdir -p "$SCREENSHOTS"

# --- Pre-flight check ---
check_screen_unlocked() {
    # Check if screen is locked by testing if we can interact with System Events
    if ! osascript -e 'tell application "System Events" to return name of first process whose frontmost is true' >/dev/null 2>&1; then
        echo "ERROR: Screen appears to be locked. GUI tests require an unlocked display."
        echo "Unlock your Mac and try again."
        exit 1
    fi
}
check_screen_unlocked

# --- Result tracking ---
pass() { PASS=$((PASS+1)); TESTS+=("PASS: $1"); echo "✓ $1"; }
fail() { FAIL=$((FAIL+1)); TESTS+=("FAIL: $1 — $2"); echo "✗ $1 — $2"; }

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
}

# --- App lifecycle ---
screenshot() { screencapture -x "$SCREENSHOTS/$1.png"; }

bring_to_front() {
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to set frontmost to true' 2>/dev/null
}

wait_for_app() {
    for i in {1..10}; do
        if pgrep -x SecondBrain >/dev/null 2>&1; then
            sleep 1
            bring_to_front
            return 0
        fi
        sleep 1
    done
    echo "ERROR: App did not launch"
    return 1
}

launch_app() {
    pkill -9 -f SecondBrain 2>/dev/null || true
    sleep 1
    defaults write dev.apresai.2ndbrain lastVaultPath "$VAULT"
    # Build if running standalone (test-gui Makefile target handles install)
    if [ "${SKIP_BUILD:-}" != "1" ]; then
        echo "Building..."
        make build-app 2>&1 | tail -1 || true
    fi
    open "$APP"
    wait_for_app
    sleep 2
}

kill_app() {
    pkill -9 -f SecondBrain 2>/dev/null || true
    sleep 1
}

# --- GUI interactions ---
create_note() {
    local title="$1"
    bring_to_front
    sleep 0.5
    osascript <<EOF
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        delay 0.5
        keystroke "n" using {command down}
        delay 1
        keystroke "$title"
        delay 0.3
        key code 36
        delay 2
    end tell
end tell
EOF
}

create_typed_note() {
    local title="$1"
    local type_label="$2"  # e.g. "Architecture Decision Record"
    bring_to_front
    sleep 0.5
    osascript <<EOF
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        delay 0.5
        keystroke "n" using {command down}
        delay 1
        keystroke "$title"
        delay 0.3
        try
            tell window 1
                tell sheet 1
                    tell pop up button 1
                        click
                        delay 0.3
                        click menu item "$type_label" of menu 1
                        delay 0.3
                    end tell
                end tell
            end tell
        on error
            try
                click pop up button 1 of window 1
                delay 0.3
                click menu item "$type_label" of menu 1 of pop up button 1 of window 1
                delay 0.3
            end try
        end try
        delay 0.3
        key code 36
        delay 2
    end tell
end tell
EOF
}

open_quick_open() {
    bring_to_front
    sleep 0.3
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to keystroke "p" using {command down}'
    sleep 1
}

close_quick_open() {
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to keystroke "p" using {command down}'
    sleep 0.5
}

open_search() {
    bring_to_front
    sleep 0.3
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to keystroke "f" using {command down, shift down}'
    sleep 1
}

close_search() {
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to keystroke "f" using {command down, shift down}'
    sleep 0.5
}

open_command_palette() {
    bring_to_front
    sleep 0.3
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to keystroke "p" using {command down, shift down}'
    sleep 1
}

close_command_palette() {
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to keystroke "p" using {command down, shift down}'
    sleep 0.5
}

type_text() {
    osascript -e "tell application \"System Events\" to tell process \"SecondBrain\" to keystroke \"$1\""
    sleep 0.3
}

press_return() {
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to key code 36'
    sleep 0.5
}

press_escape() {
    osascript -e 'tell application "System Events" to tell process "SecondBrain" to key code 53'
    sleep 0.5
}

cmd_key() {
    osascript -e "tell application \"System Events\" to tell process \"SecondBrain\" to keystroke \"$1\" using {command down}"
    sleep 0.5
}

cmd_shift_key() {
    osascript -e "tell application \"System Events\" to tell process \"SecondBrain\" to keystroke \"$1\" using {command down, shift down}"
    sleep 0.5
}

# --- File verification ---
file_exists() { [ -f "$1" ]; }
file_contains() { grep -q "$2" "$1" 2>/dev/null; }
file_count() { ls "$1" 2>/dev/null | wc -l | tr -d ' '; }
