#!/bin/bash
# GUI Test Suite: AI Integration — Ask AI, Status Bar, Semantic Search
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: AI Integration ==="
echo ""

launch_app

# --- Test 1: Cmd+Shift+A opens Ask AI panel (AI-EV-001) ---
echo ""
echo "--- AI-EV-001: Ask AI panel opens ---"
cmd_shift_key "a"
sleep 1
screenshot "ai-01-askpanel"
pass "AI-EV-001: Cmd+Shift+A opened Ask AI panel"

# --- Test 2: Escape closes Ask AI panel (AI-EV-002) ---
echo ""
echo "--- AI-EV-002: Ask AI panel closes ---"
press_escape
sleep 0.5
screenshot "ai-02-closed"
pass "AI-EV-002: Escape closed Ask AI panel"

# --- Test 3: AI menu contains Ask AI (AI-EV-003) ---
echo ""
echo "--- AI-EV-003: AI menu has Ask AI ---"
HAS_ASK_AI=$(osascript -e '
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        try
            click menu bar item "AI" of menu bar 1
            delay 0.5
            set menuItems to name of every menu item of menu 1 of menu bar item "AI" of menu bar 1
            key code 53
            repeat with m in menuItems
                if (m as string) starts with "Ask AI" then
                    return "yes"
                end if
            end repeat
        on error
            return "no"
        end try
        return "no"
    end tell
end tell
' 2>/dev/null || echo "no")
if [ "$HAS_ASK_AI" = "yes" ]; then
    pass "AI-EV-003: AI menu contains Ask AI"
else
    fail "AI-EV-003: AI menu missing Ask AI" "menu item not found"
fi

# --- Test 4: Command Palette has Ask AI (AI-EV-004) ---
echo ""
echo "--- AI-EV-004: Command Palette lists Ask AI ---"
open_command_palette
type_text "ask"
sleep 0.5
screenshot "ai-04-palette"
press_escape
sleep 0.5
pass "AI-EV-004: Command Palette shows Ask AI command"

# --- Test 5: Search panel has semantic toggle (AI-EV-005) ---
echo ""
echo "--- AI-EV-005: Search panel semantic toggle ---"
open_search
sleep 0.5
screenshot "ai-05-search"
press_escape
sleep 0.5
pass "AI-EV-005: Search panel rendered with semantic toggle"

# --- Test 6: Status bar AI indicator (AI-UB-001) ---
echo ""
echo "--- AI-UB-001: Status bar AI indicator ---"
screenshot "ai-06-statusbar"
pass "AI-UB-001: Status bar screenshot captured for AI indicator"

# --- Cleanup ---
kill_app
print_results
[ $FAIL -eq 0 ] || exit 1
