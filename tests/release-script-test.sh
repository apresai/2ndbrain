#!/usr/bin/env bash
# Unit tests for the checkpointed release script's PURE parts: syntax, call-site
# arity, state round-trip, and the history-adoption selector. Network-free,
# signing-free, safe anywhere (CI included). The notarization waits and Apple
# round-trips are exercised only by real releases; this guards the machinery a
# real release would crash on — the exact class review caught live: a notarize()
# call site passing two args into a three-arg signature, which set -u aborts
# only at DMG-phase runtime.
set -euo pipefail
cd "$(dirname "$0")/.."

SCRIPT="scripts/release-app-local.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }
pass=0

# 1. The script parses.
bash -n "$SCRIPT" || fail "bash -n"
pass=$((pass+1))

# 2. No notarize call site stops at the state prefix with no sha argument —
#    the exact drift that shipped once (the DMG call), which ${3:?} only
#    catches at RUNTIME in the phase that reaches it. Word-counting can't
#    parse quoted command substitutions, so this encodes the bug class as a
#    negative pattern instead.
if grep -nE '^\s+notarize\s+\S+\s+(app|dmg)\s*$' "$SCRIPT"; then
  fail "notarize call site missing its sha argument (see lines above)"
fi
# And both expected call sites exist with SOMETHING after the prefix.
grep -qE '^\s+notarize\s+"\$NOTARY_ZIP"\s+app\s+\S' "$SCRIPT" || fail "app notarize call shape changed"
grep -qE '^\s+notarize\s+"\$DMG"\s+dmg\s+\S' "$SCRIPT" || fail "dmg notarize call shape changed"
pass=$((pass+1))

# 3. state_get / state_set round-trip, overwrite, and missing-file behavior.
#    The functions are extracted verbatim so the test exercises the shipped
#    code, not a copy.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
extract_fn() { # extract_fn <name> — print the named top-level function
  awk -v fn="$1" '$0 ~ "^"fn"\\(\\) {" {infn=1} infn {print} infn && /^}/ {exit}' "$SCRIPT"
}
{
  echo 'ARTIFACT_DIR='"$TMP"
  echo 'STATE='"$TMP"'/state.json'
  extract_fn state_get
  extract_fn state_set
  cat <<'EOT'
[ -z "$(state_get '.missing')" ] || { echo "missing key not empty"; exit 1; }
state_set app_sub_id abc-123
state_set app_sha deadbeef
[ "$(state_get '.app_sub_id')" = "abc-123" ] || { echo "round-trip failed"; exit 1; }
state_set app_sub_id xyz-789
[ "$(state_get '.app_sub_id')" = "xyz-789" ] || { echo "overwrite failed"; exit 1; }
[ "$(state_get '.app_sha')" = "deadbeef" ] || { echo "sibling key lost on overwrite"; exit 1; }
EOT
} > "$TMP/state-test.sh"
bash "$TMP/state-test.sh" || fail "state round-trip"
pass=$((pass+1))

# 4. The history-adoption selector: newest matching name inside the 10-minute
#    window wins; other names and stale entries never match. Uses the exact
#    jq expression shape the script ships (kept in sync by the grep below).
grep -q 'sort_by(.createdDate) | last | .id // empty' "$SCRIPT" \
  || fail "adoption selector changed; update this test"
NOW_MINUS_2M="$(date -u -v-2M +%Y-%m-%dT%H:%M:%S.000Z 2>/dev/null || date -u -d '2 minutes ago' +%Y-%m-%dT%H:%M:%S.000Z)"
NOW_MINUS_40M="$(date -u -v-40M +%Y-%m-%dT%H:%M:%S.000Z 2>/dev/null || date -u -d '40 minutes ago' +%Y-%m-%dT%H:%M:%S.000Z)"
picked="$(jq -rn --arg name "notarize-9.9.9.zip" \
  --arg fresh "$NOW_MINUS_2M" --arg stale "$NOW_MINUS_40M" '
  {history: [
    {name: "notarize-9.9.9.zip", id: "stale-id", createdDate: $stale},
    {name: "other.dmg",          id: "wrong-name", createdDate: $fresh},
    {name: "notarize-9.9.9.zip", id: "fresh-id", createdDate: $fresh}
  ]} |
  [.history[]? | select(.name == $name)
    | select((.createdDate | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601) > (now - 600))]
  | sort_by(.createdDate) | last | .id // empty')"
[ "$picked" = "fresh-id" ] || fail "adoption selector picked '$picked', want fresh-id"
pass=$((pass+1))

echo "release-script tests: $pass/4 passed"
