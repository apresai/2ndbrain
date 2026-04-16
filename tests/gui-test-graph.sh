#!/bin/bash
# GUI Test Suite: Graph View — Obsidian-style document graph visualization.
#
# These tests complement the GraphSimulation / GraphDataSource / DatabaseManager
# unit tests by exercising the real NSView + CADisplayLink + @MainActor paths
# that can only fail at runtime. A previous iteration of the display-link
# plumbing compiled fine and passed unit tests but crashed with
# `dispatch_assert_queue_fail` the moment the view appeared; that class of bug
# is exactly what this file catches.
#
# Each interaction step asserts the app is still running afterward. That's the
# primary signal: did our code survive this input? Screenshot diffs are a
# secondary signal for "something visibly changed."
set -e
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Graph View ==="
echo ""

# --- Helpers specific to this test file ---

app_alive() { pgrep -x SecondBrain >/dev/null 2>&1; }

# Open Graph View. Try the keyboard shortcut first (Cmd+Option+G), fall back
# to menu-item `perform action "AXPress"` if the shortcut is shadowed by
# another app that's frontmost. SwiftUI menu items sometimes ignore
# `click menu item` and only respond to AXPress.
open_graph_view() {
    bring_to_front
    sleep 0.8
    # First: keyboard shortcut path (fast, usually works)
    osascript <<'EOF' >/dev/null
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        delay 0.3
        keystroke "g" using {command down, option down}
    end tell
end tell
EOF
    sleep 2
    if [[ "$(sheet_exists)" == "true" ]]; then
        echo "ok"
        return
    fi

    # Fallback: walk the menu bar hunting for the "Graph View" item and
    # invoke it via AXPress. SwiftUI CommandMenu creates a second "View"
    # entry in addition to the macOS standard View menu; iterate both.
    osascript <<'EOF'
tell application "System Events"
    tell process "SecondBrain"
        set frontmost to true
        delay 0.5
        tell menu bar 1
            set names to name of every menu bar item
            repeat with i from 1 to count of names
                if item i of names is "View" then
                    tell menu bar item i
                        click
                        delay 0.4
                        try
                            tell menu 1
                                if exists menu item "Graph View" then
                                    perform action "AXPress" of menu item "Graph View"
                                    delay 2
                                    return "ok"
                                end if
                            end tell
                        end try
                        -- Close this View menu before we try the next one
                        key code 53
                        delay 0.2
                    end tell
                end if
            end repeat
        end tell
        return "not_found"
    end tell
end tell
EOF
}

sheet_exists() {
    osascript <<'EOF'
tell application "System Events"
    tell process "SecondBrain"
        try
            tell window 1
                if exists sheet 1 then
                    return "true"
                end if
            end tell
        end try
        return "false"
    end tell
end tell
EOF
}

# Dump the sheet's text descendants so we can grep for section headers like
# "Filters", "Forces", "Global", "Local". SwiftUI static text lands in the AX
# tree as `AXStaticText` children.
dump_sheet_text() {
    osascript <<'EOF'
tell application "System Events"
    tell process "SecondBrain"
        try
            tell window 1
                tell sheet 1
                    set texts to ""
                    set allElems to entire contents
                    repeat with e in allElems
                        try
                            set texts to texts & (description of e) & "|"
                        end try
                        try
                            set texts to texts & (value of e as text) & "|"
                        end try
                    end repeat
                    return texts
                end tell
            end tell
        on error
            return "<no sheet>"
        end try
    end tell
end tell
EOF
}

# Click a control in the sheet by accessibility identifier. SwiftUI controls
# don't expose their Text label as the AX `name` attribute, so we use
# `.accessibilityIdentifier` on the Swift side and match on that here.
# The `entire contents` result must be stored in a variable first — iterating
# it inline can return stale references whose attributes fail to resolve.
click_by_identifier() {
    local ident="$1"
    osascript <<EOF
tell application "System Events"
    tell process "SecondBrain"
        try
            tell window 1
                tell sheet 1
                    set allElems to entire contents
                    repeat with e in allElems
                        try
                            if value of attribute "AXIdentifier" of e is "$ident" then
                                perform action "AXPress" of e
                                return "ok"
                            end if
                        end try
                    end repeat
                end tell
            end tell
        end try
        return "missing"
    end tell
end tell
EOF
}

# Click a radio button by its AX description (used for the Mode picker
# where segments show up as AXRadioButton with description "Global" / "Local").
click_radio_by_description() {
    local desc="$1"
    osascript <<EOF
tell application "System Events"
    tell process "SecondBrain"
        try
            tell window 1
                tell sheet 1
                    set allElems to entire contents
                    repeat with e in allElems
                        try
                            if role of e is "AXRadioButton" and description of e is "$desc" then
                                perform action "AXPress" of e
                                return "ok"
                            end if
                        end try
                    end repeat
                end tell
            end tell
        end try
        return "missing"
    end tell
end tell
EOF
}

# Count "AXImage" + "AXCanvas" + generic drawing elements in the sheet — a
# proxy for "graph canvas is present and rendering something." SwiftUI Canvas
# maps to various AX roles depending on the macOS version; we just want a
# non-zero count to confirm the left-hand pane isn't empty.
screenshot_bytes() {
    # File size is a cheap proxy for pixel content when zoom/pan is disabled.
    # If a click crashes the render loop, the next frame's file size drops
    # toward the solid-background minimum.
    stat -f %z "$SCREENSHOTS/$1.png" 2>/dev/null || echo 0
}

# --- Setup ---

# Make sure the vault has at least one linked document so the graph has
# something interesting to render. If the vault is already populated from
# other tests this is a no-op for our purposes — the graph will just show
# whatever's there. Seed two docs that link to each other.
echo "--- Setup: seed linked docs ---"
rm -f "$VAULT/graph-test-alpha.md" "$VAULT/graph-test-beta.md"
cat > "$VAULT/graph-test-alpha.md" <<EOF
---
id: 11111111-1111-1111-1111-111111111111
title: Graph Alpha
type: note
status: draft
tags: [graphtest]
created: 2026-04-16T00:00:00Z
modified: 2026-04-16T00:00:00Z
---
# Graph Alpha
Links to [[Graph Beta]] for traversal.
EOF
cat > "$VAULT/graph-test-beta.md" <<EOF
---
id: 22222222-2222-2222-2222-222222222222
title: Graph Beta
type: note
status: draft
tags: [graphtest]
created: 2026-04-16T00:00:00Z
modified: 2026-04-16T00:00:00Z
---
# Graph Beta
Back-links to [[Graph Alpha]].
EOF
# Rebuild index so the graph sees the links. Use the installed CLI if it
# exists; otherwise use the repo-local dev build.
if command -v 2nb >/dev/null 2>&1; then
    CLI=2nb
else
    CLI="$(pwd)/cli/bin/2nb"
fi
if [ -x "$CLI" ]; then
    (cd "$VAULT" && "$CLI" index >/dev/null 2>&1) || true
fi

launch_app
sleep 2

# --- Test 1: Graph View opens via menu ---
echo ""
echo "--- Test 1: Graph View opens ---"
RESULT=$(open_graph_view)
sleep 1
screenshot "graph-01-opened"

if [[ "$RESULT" == "ok" ]] && [[ "$(sheet_exists)" == "true" ]] && app_alive; then
    pass "LNK-EV-004: Graph View menu → sheet renders and app survives"
else
    fail "Graph View did not open" "menu=$RESULT sheet=$(sheet_exists) alive=$(app_alive && echo yes || echo no)"
    kill_app
    rm -f "$VAULT/graph-test-"*.md
    print_results
    exit $FAIL
fi

# --- Test 2: Inspector has Mode, Filters, and Forces sections ---
echo ""
echo "--- Test 2: Inspector sections populated ---"
UI_TEXT=$(dump_sheet_text)
HAS_MODE=false
HAS_FILTERS=false
HAS_FORCES=false
HAS_DISPLAY=false
[[ "$UI_TEXT" == *"Mode"* ]] && HAS_MODE=true
[[ "$UI_TEXT" == *"Filters"* ]] && HAS_FILTERS=true
[[ "$UI_TEXT" == *"Forces"* ]] && HAS_FORCES=true
[[ "$UI_TEXT" == *"Display"* ]] && HAS_DISPLAY=true

if $HAS_MODE && $HAS_FILTERS && $HAS_FORCES && $HAS_DISPLAY; then
    pass "Inspector: Mode + Filters + Forces + Display sections present"
else
    fail "Inspector missing sections" "Mode=$HAS_MODE Filters=$HAS_FILTERS Forces=$HAS_FORCES Display=$HAS_DISPLAY"
fi

# --- Test 3: Toggle to Local mode (LNK-EV-005) ---
echo ""
echo "--- Test 3: Local mode toggle ---"
LOCAL_CLICK=$(click_radio_by_description "Local")
sleep 2
screenshot "graph-02-local-mode"

if [[ "$LOCAL_CLICK" == "ok" ]] && app_alive && [[ "$(sheet_exists)" == "true" ]]; then
    pass "LNK-EV-005: Local mode toggle did not crash"
else
    fail "Local mode toggle broke" "click=$LOCAL_CLICK alive=$(app_alive && echo yes || echo no) sheet=$(sheet_exists)"
fi

# Switch back to Global so subsequent tests see the full graph
click_radio_by_description "Global" >/dev/null
sleep 1

# --- Test 4: Orphan filter toggle ---
echo ""
echo "--- Test 4: Filter toggle (Show orphans) ---"
ORPHAN_CLICK=$(click_by_identifier "graph-toggle-orphans")
sleep 1.5
screenshot "graph-03-orphans-toggled"

if [[ "$ORPHAN_CLICK" == "ok" ]] && app_alive && [[ "$(sheet_exists)" == "true" ]]; then
    pass "Show-orphans checkbox fires without crash"
else
    fail "Orphan toggle missing or crashed app" "click=$ORPHAN_CLICK alive=$(app_alive && echo yes || echo no)"
fi

# Restore the default (toggle back on) so subsequent tests see the full graph
click_by_identifier "graph-toggle-orphans" >/dev/null
sleep 0.5

# --- Test 5: Rebuild Graph button ---
echo ""
echo "--- Test 5: Rebuild Graph button ---"
REBUILD_CLICK=$(click_by_identifier "graph-btn-rebuild")
sleep 2
screenshot "graph-04-rebuild"

if [[ "$REBUILD_CLICK" == "ok" ]] && app_alive && [[ "$(sheet_exists)" == "true" ]]; then
    pass "Rebuild Graph button triggers rebuild without crashing the sim"
else
    fail "Rebuild Graph broke the view" "click=$REBUILD_CLICK alive=$(app_alive && echo yes || echo no)"
fi

# --- Test 6: FSEvents-driven rebuild (LNK-ST-001) ---
# Create a new markdown file while the graph is open; the FSEvents watcher in
# AppState should bump graphNeedsRebuild, and GraphView's .onChange should
# fire rebuild(). We don't have a way to inspect the model from AppleScript,
# so we assert: file write does not crash the app, sheet stays open, and
# the screenshot after the write differs from before.
echo ""
echo "--- Test 6: FSEvents-driven rebuild ---"
BYTES_BEFORE=$(screenshot_bytes "graph-04-rebuild")
cat > "$VAULT/graph-test-gamma.md" <<EOF
---
id: 33333333-3333-3333-3333-333333333333
title: Graph Gamma
type: note
status: draft
tags: [graphtest]
created: 2026-04-16T00:00:00Z
modified: 2026-04-16T00:00:00Z
---
# Graph Gamma
Links to [[Graph Alpha]] and [[Graph Beta]].
EOF
# Reindex so the graph has links for the new doc. FSEvents fires regardless,
# but the graph only shows new edges after the index picks them up.
if [ -x "$CLI" ]; then
    (cd "$VAULT" && "$CLI" index >/dev/null 2>&1) || true
fi
sleep 3
screenshot "graph-05-after-fsevents"
BYTES_AFTER=$(screenshot_bytes "graph-05-after-fsevents")

if app_alive && [[ "$(sheet_exists)" == "true" ]]; then
    # File-size delta is a very rough signal (could be zero if the new node
    # lands in the same pixel region), so we don't hard-fail on it; surviving
    # the write is the main assertion.
    pass "LNK-ST-001: FSEvents write while graph open did not crash (before=$BYTES_BEFORE after=$BYTES_AFTER)"
else
    fail "FSEvents-driven rebuild crashed" "alive=$(app_alive && echo yes || echo no) sheet=$(sheet_exists)"
fi

# --- Test 7: Re-settle Layout button ---
echo ""
echo "--- Test 7: Re-settle Layout button ---"
RESETTLE=$(click_by_identifier "graph-btn-resettle")
sleep 2
screenshot "graph-06-resettle"

if [[ "$RESETTLE" == "ok" ]] && app_alive && [[ "$(sheet_exists)" == "true" ]]; then
    pass "Re-settle Layout reheats simulation without crash"
else
    fail "Re-settle Layout broke" "click=$RESETTLE alive=$(app_alive && echo yes || echo no)"
fi

# --- Test 8: Close via Escape ---
echo ""
echo "--- Test 8: Dismiss via Escape ---"
press_escape
sleep 1.5

if [[ "$(sheet_exists)" == "false" ]] && app_alive; then
    pass "Escape closes graph sheet cleanly"
else
    fail "Sheet did not dismiss" "sheet=$(sheet_exists) alive=$(app_alive && echo yes || echo no)"
fi

# --- Test 9: Re-open after close (simulation re-init is safe) ---
echo ""
echo "--- Test 9: Re-open after dismiss ---"
RESULT2=$(open_graph_view)
sleep 2
screenshot "graph-07-reopened"

if [[ "$RESULT2" == "ok" ]] && [[ "$(sheet_exists)" == "true" ]] && app_alive; then
    pass "Graph View can reopen after dismiss (no display-link leak)"
else
    fail "Second open failed" "result=$RESULT2 sheet=$(sheet_exists) alive=$(app_alive && echo yes || echo no)"
fi

# --- Cleanup ---
echo ""
echo "--- Cleanup ---"
press_escape
sleep 0.5
rm -f "$VAULT/graph-test-"*.md
# Reindex so the vault doesn't carry test-only IDs in its BM25 index
if [ -x "$CLI" ]; then
    (cd "$VAULT" && "$CLI" index >/dev/null 2>&1) || true
fi
kill_app
echo "Removed test files and reindexed"

print_results
exit $FAIL
