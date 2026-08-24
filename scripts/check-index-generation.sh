#!/usr/bin/env bash
# check-index-generation.sh — release guard. If a change since the last release
# tag touched indexing/embedding LOGIC (chunk boundaries, chunk→vector mapping)
# but did NOT bump vault.IndexGeneration / vault.EmbedGeneration, fail so the
# author consciously decides whether existing users must reindex/re-embed. The
# generation constants are what the shipped CLI compares against the stamp in a
# user's index DB to prompt a reindex (see cli/internal/vault/generation.go).
#
# Escape hatch: a `Reindex-Not-Needed: <reason>` trailer in any commit since the
# last tag (for a comment/refactor/no-op change to a watched file).
set -euo pipefail

# Watched files, split by the generation class their changes demand. The
# EMBED-class files (chunk boundaries, embedding production) require an
# EmbedGeneration bump: an IndexGeneration bump cannot make stale vectors
# usable. The INDEX-class store files are broader than ideal (docs.go carries
# general CRUD too), but ResolveLinks outcomes (links.target_id) are persisted
# index state, so a resolution-logic change strands existing vaults until
# `2nb index`; the Reindex-Not-Needed trailer stays the escape hatch for
# incidental edits. A Nova embed-format/purpose change remains a manual
# release-checklist consideration (bedrock.go mixes embed + generation code).
WATCHED_EMBED=(
  cli/internal/document/chunk.go
  cli/internal/embed/embed.go
)
WATCHED_INDEX=(
  cli/internal/store/docs.go
  cli/internal/store/resolve.go
)
GEN_FILE="cli/internal/vault/generation.go"

base="$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || git rev-list --max-parents=0 HEAD | tail -1)"
changed="$(git diff --name-only "$base"..HEAD)"

touched_embed=""
touched_index=""
for f in "${WATCHED_EMBED[@]}"; do
  if grep -qx "$f" <<<"$changed"; then touched_embed+="    $f"$'\n'; fi
done
for f in "${WATCHED_INDEX[@]}"; do
  if grep -qx "$f" <<<"$changed"; then touched_index+="    $f"$'\n'; fi
done
touched_logic="$touched_embed$touched_index"

if [ -z "$touched_logic" ]; then
  echo "check-index-generation: no watched index/embed logic files changed since $base — OK"
  exit 0
fi

# Logic changed. Did a generation constant of the REQUIRED CLASS get bumped in
# the diff? An embed-class change demands EmbedGeneration specifically (an
# IndexGeneration bump cannot make stale vectors usable); an index-class change
# accepts either (a re-embed always implies a reindex). (Process substitution,
# not a pipe, so grep -q exiting early can't SIGPIPE git under `set -o pipefail`
# and read as a spurious "not bumped".)
bump_re='^\+[[:space:]]*(Index|Embed)Generation[[:space:]]*='
[ -n "$touched_embed" ] && bump_re='^\+[[:space:]]*EmbedGeneration[[:space:]]*='
if grep -qE "$bump_re" < <(git diff "$base"..HEAD -- "$GEN_FILE"); then
  echo "check-index-generation: watched logic changed and a matching generation constant was bumped since $base — OK"
  echo "  changed:"
  printf '%s' "$touched_logic" | sed 's/^/  /'
  exit 0
fi

# Escape hatch: explicit acknowledgment in a commit trailer.
if grep -qiE '^Reindex-Not-Needed:' < <(git log "$base"..HEAD --format='%B'); then
  echo "check-index-generation: logic changed but a 'Reindex-Not-Needed:' trailer acknowledges no reindex is required — OK"
  exit 0
fi

cat >&2 <<EOF
ERROR: index/embed logic changed since $base but neither IndexGeneration nor
EmbedGeneration was bumped in $GEN_FILE.

Changed logic files:
$touched_logic
If this release needs existing users to reindex/re-embed, bump the correct
constant in $GEN_FILE (EmbedGeneration for a chunking/embedding change that needs
--force-reembed — required when chunk.go/embed.go changed; IndexGeneration for
an index-only change such as link resolution).

If it genuinely does NOT need a reindex (comment/refactor/no-op), add a
'Reindex-Not-Needed: <reason>' trailer to a commit since $base.
EOF
exit 1
