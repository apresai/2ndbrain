#!/bin/bash
# GUI Test Suite: End-to-End GUI Index Rebuild, Re-embed All, Hybrid Search, & Quick Search HUD
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/gui-helpers.sh

echo "=== GUI Test: Index Rebuild & Hybrid Search ==="

TEST_VAULT=$(mktemp -d /tmp/sb-gui-index.XXXXXX)
cleanup() {
    kill_app
    rm -rf "$TEST_VAULT" "$GUI_LOG_DIR"
}
trap cleanup EXIT

setup_temp_vault "$TEST_VAULT"
ZEPHYR_PATH=$(seed_note "$TEST_VAULT" "Zephyr Quartz Field Notes" "Specimen zephyrquartz exhibits pale violet photoluminescence under shortwave ultraviolet illumination in granitic pegmatite cavities. Crystal faces show striated prism habits and vitreous luster.")
BREAD_PATH=$(seed_note "$TEST_VAULT" "Wild Culture Fermentation" "Flour and water develop active wild yeast cultures and lactobacilli during warm overnight hydration cycles inside a stoneware crock. Fermenting grain batter builds organic acids and carbon dioxide gas pockets prior to hearth oven baking.")
CONCURRENCY_PATH=$(seed_note "$TEST_VAULT" "Concurrency Actor Isolation" "Swift structured concurrency coordinates MainActor UI isolation and TaskGroup parallel work safely across asynchronous boundaries. Sendable value types prevent shared mutable state data races.")

# Seed a 125-section note (>100 chunks) reproducing Gemini's 100-request BatchEmbedContents ceiling.
LARGE_BODY=$(python3 - <<'PY'
sections = []
for i in range(1, 126):
    if i == 118:
        sections.append("## Section 118\nDeep benthic hydrography notes: bathypelagicsiphonophore colonies emit bioluminescent blue-green flashes across deep ocean thermoclines during nocturnal vertical migration.")
    else:
        sections.append(f"## Section {i}\nOceanographic station log #{i} measuring pelagic salinity, dissolved oxygen, and basaltic ridge pressure gradients.")
print("\n\n".join(sections))
PY
)
DEEP_OCEAN_PATH=$(seed_note "$TEST_VAULT" "Abyssal Hydrography Transect" "$LARGE_BODY")

EMBED_CAPABLE="no"
if probe_gemini_capability "embedding" "$TEST_VAULT"; then
    EMBED_CAPABLE="yes"
    echo "Gemini embedding capability probe: ACTIVE (gemini-embedding-2)"
else
    echo "Gemini embedding capability probe: UNAVAILABLE (vector assertions will skip cleanly)"
fi

launch_app_on_vault "$TEST_VAULT"
screenshot "idx-01-launched"

# --- Step 1: Open Sync Index sheet and Cancel cleanly ---
click_menu_item "Vault" "Sync Index"
wait_gui_state 'state.get("indexSheetVisible") == True' 10
pass "Vault > Sync Index opened IndexProgress sheet"

click_ax_button "indexSheet.cancelButton"
wait_gui_state 'state.get("indexSheetVisible") == False' 8
pass "Cancel dismissed Sync Index sheet without indexing"

# --- Step 2: Run full Sync Index through the GUI ---
click_menu_item "Vault" "Sync Index"
wait_gui_state 'state.get("indexSheetVisible") == True' 10
sleep 0.2

click_ax_button "indexSheet.confirmButton"
wait_gui_state 'state.get("isIndexing") == False and state.get("indexPhase") == "Complete"' 90
screenshot "idx-02-sync-complete"
assert_gui_action "index.start" "started" "GUI Sync Index started (index.start)"
assert_gui_action "index.complete" "succeeded" "GUI Sync Index completed (index.complete)"

DOCS_IN_STATE=$(gui_state_field "docsIndexed")
LAST_INDEX_ERR=$(gui_state_field "lastIndexError")
if [ "${DOCS_IN_STATE:-0}" -ge 4 ] && [ -z "$LAST_INDEX_ERR" ]; then
    pass "Status snapshot reports $DOCS_IN_STATE indexed documents (incl. 125-section note) with no error"
else
    fail "Status snapshot docsIndexed/lastIndexError check failed" "docs=$DOCS_IN_STATE err=$LAST_INDEX_ERR"
fi

DB_FILE="$TEST_VAULT/.2ndbrain/index.db"
DOC_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM documents;" 2>/dev/null || echo "0")
CHUNK_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM chunks;" 2>/dev/null || echo "0")
if [ "$DOC_COUNT" -ge 4 ] && [ "$CHUNK_COUNT" -ge 125 ]; then
    pass "SQLite index.db contains $DOC_COUNT documents and $CHUNK_COUNT chunks"
else
    fail "SQLite documents/chunks count mismatch" "docs=$DOC_COUNT chunks=$CHUNK_COUNT"
fi

if [ "$EMBED_CAPABLE" = "yes" ]; then
    EMBED_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM documents WHERE embedding IS NOT NULL AND length(embedding) > 0;" 2>/dev/null || echo "0")
    EMBED_MODELS=$(sqlite3 "$DB_FILE" "SELECT DISTINCT embedding_model FROM documents WHERE embedding IS NOT NULL AND length(embedding) > 0;" 2>/dev/null || echo "")
    VEC_CHUNK_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM vec_chunks_rowids;" 2>/dev/null || echo "0")
    if [ "$EMBED_COUNT" -ge 4 ] && [ "$VEC_CHUNK_COUNT" -ge 125 ] && [[ "$EMBED_MODELS" == *"gemini-embedding-2"* ]]; then
        pass "SQLite embeddings populated ($EMBED_COUNT docs, $VEC_CHUNK_COUNT vec_chunks, model=$EMBED_MODELS)"
    else
        fail "SQLite embeddings missing or wrong count/model" "docs=$EMBED_COUNT vec_chunks=$VEC_CHUNK_COUNT models=$EMBED_MODELS"
    fi
fi

click_ax_button "indexSheet.doneButton"
wait_gui_state 'state.get("indexSheetVisible") == False' 8
pass "Done closed Sync Index sheet"

# --- Step 3: Trigger Vault > Re-embed All… (exercises >100-chunk force-reembed through GUI) ---
click_menu_item "Vault" "Re-embed All…"
wait_gui_state 'state.get("indexSheetVisible") == True' 10
sleep 0.2

click_ax_button "indexSheet.confirmButton"
wait_gui_state 'state.get("isIndexing") == False and state.get("indexPhase") == "Complete"' 120
screenshot "idx-03-reembed-complete"

LAST_REEMBED_ERR=$(gui_state_field "lastIndexError")
if [ -z "$LAST_REEMBED_ERR" ]; then
    pass "Re-embed All completed cleanly with no lastIndexError"
else
    fail "Re-embed All recorded unexpected lastIndexError" "$LAST_REEMBED_ERR"
fi

if [ "$EMBED_CAPABLE" = "yes" ]; then
    REEMBED_DOCS=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM documents WHERE embedding IS NOT NULL AND length(embedding) > 0;" 2>/dev/null || echo "0")
    REEMBED_VEC_CHUNKS=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM vec_chunks_rowids;" 2>/dev/null || echo "0")
    if [ "$REEMBED_DOCS" -ge 4 ] && [ "$REEMBED_VEC_CHUNKS" -ge 125 ]; then
        pass "Re-embed All populated all $REEMBED_DOCS docs and $REEMBED_VEC_CHUNKS vec_chunks (>100 chunks)"
    else
        fail "Re-embed All lost embeddings or >100 chunks" "docs=$REEMBED_DOCS vec_chunks=$REEMBED_VEC_CHUNKS"
    fi
fi

if python3 - "$GUI_LOG_DIR/gui-actions.jsonl" <<'PY'
import json, sys
with open(sys.argv[1], "r", encoding="utf-8") as f:
    for line in f:
        ev = json.loads(line)
        if ev.get("action") == "index.start" and ev.get("metadata", {}).get("force_reembed") == "true":
            sys.exit(0)
sys.exit(1)
PY
then
    pass "Re-embed All recorded index.start with forceReembed=true and completed"
else
    fail "Re-embed All telemetry check failed" "no index.start with forceReembed=true"
fi

click_ax_button "indexSheet.doneButton"
wait_gui_state 'state.get("indexSheetVisible") == False' 8

# --- Step 4: AI > Search Vault… (Keyword + Zero-Lexical-Overlap Semantic Search) ---
click_menu_item "AI" "Search Vault…"
wait_gui_state 'state.get("activeTab") == "Ask & Search" and state.get("askSearchMode") == "Hybrid Search"' 10

set_field_text "askSearch.searchField" "zephyrquartz" true
wait_gui_state 'state.get("lastSearchQuery") == "zephyrquartz" and (state.get("lastSearchHitCount") or 0) >= 1' 20
screenshot "idx-04-keyword-search"

TOP_KEYWORD=$(gui_state_field "lastSearchTopPath")
if [ "$TOP_KEYWORD" = "$ZEPHYR_PATH" ]; then
    pass "Keyword GUI search hit exact target: $TOP_KEYWORD"
else
    fail "Keyword GUI search top path mismatch" "expected=$ZEPHYR_PATH got=$TOP_KEYWORD"
fi

# Verify deep section (#118 > 100 chunks) inside the 125-section note is indexed and searchable.
set_field_text "askSearch.searchField" "bathypelagicsiphonophore" true
wait_gui_state 'state.get("lastSearchQuery") == "bathypelagicsiphonophore" and (state.get("lastSearchHitCount") or 0) >= 1' 20
TOP_LARGE=$(gui_state_field "lastSearchTopPath")
if [ "$TOP_LARGE" = "$DEEP_OCEAN_PATH" ]; then
    pass "Section 118 (>100-chunk boundary) inside $DEEP_OCEAN_PATH is searchable"
else
    fail "Deep-chunk (>100) search top path mismatch" "expected=$DEEP_OCEAN_PATH got=$TOP_LARGE"
fi

if [ "$EMBED_CAPABLE" = "yes" ]; then
    set_field_text "askSearch.searchField" "homemade sourdough bread rising starter" true
    wait_gui_state 'state.get("lastSearchQuery") == "homemade sourdough bread rising starter" and state.get("lastSearchPhase") == "meaning" and (state.get("lastSearchHitCount") or 0) >= 1' 25
    screenshot "idx-05-semantic-search"

    SEM_MODE=$(gui_state_field "lastSearchMode")
    TOP_SEM=$(gui_state_field "lastSearchTopPath")
    if [ "$SEM_MODE" = "hybrid" ] && [ "$TOP_SEM" = "$BREAD_PATH" ]; then
        pass "Zero-lexical-overlap semantic search (mode=$SEM_MODE) ranked $TOP_SEM #1"
    else
        fail "Semantic hybrid search assertion failed" "mode=$SEM_MODE top=$TOP_SEM expected=$BREAD_PATH"
    fi

    # Semantic query with zero lexical overlap matching Section 118 (>100-chunk boundary).
    set_field_text "askSearch.searchField" "underwater sea luminescence at nighttime" true
    wait_gui_state 'state.get("lastSearchQuery") == "underwater sea luminescence at nighttime" and state.get("lastSearchPhase") == "meaning" and (state.get("lastSearchHitCount") or 0) >= 1' 25
    SEM_DEEP_MODE=$(gui_state_field "lastSearchMode")
    TOP_SEM_DEEP=$(gui_state_field "lastSearchTopPath")
    if [ "$SEM_DEEP_MODE" = "hybrid" ] && [ "$TOP_SEM_DEEP" = "$DEEP_OCEAN_PATH" ]; then
        pass "Semantic search hit Section 118 (>100-chunk boundary) in $TOP_SEM_DEEP (mode=$SEM_DEEP_MODE)"
    else
        fail "Semantic >100-chunk search failed" "mode=$SEM_DEEP_MODE top=$TOP_SEM_DEEP expected=$DEEP_OCEAN_PATH"
    fi
else
    echo "SKIP: semantic hybrid search check (embedding model unavailable)"
fi

# --- Step 5: Quick Ask & Search Panel (Floating HUD) ---
click_menu_item "AI" "Quick Ask & Search Panel"
wait_gui_state 'state.get("hudVisible") == True' 10
assert_gui_action "hud.open" "succeeded" "Quick Search HUD opened"

hud_set_text "lactobacilli"
wait_gui_state 'state.get("lastSearchQuery") == "lactobacilli" and (state.get("lastSearchHitCount") or 0) >= 1' 20
screenshot "idx-06-hud-search"

HUD_TOP=$(gui_state_field "lastSearchTopPath")
if [ "$HUD_TOP" = "$BREAD_PATH" ]; then
    pass "Quick Search HUD live search found $HUD_TOP"
else
    fail "Quick Search HUD top result unexpected" "expected=$BREAD_PATH got=$HUD_TOP"
fi

press_escape
wait_gui_state 'state.get("hudVisible") == False' 10
assert_gui_action "hud.close" "succeeded" "Quick Search HUD closed on Escape"

assert_no_leaked_secrets
print_results
[ "$FAIL" -eq 0 ]
