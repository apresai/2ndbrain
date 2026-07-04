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

# High-signal files whose changes almost always require a re-embed. Kept narrow
# (not e.g. bedrock.go, which mixes embed + generation code) so the guard stays
# low-noise; a Nova embed-format/purpose change is a manual release-checklist
# consideration.
WATCHED=(
  cli/internal/document/chunk.go
  cli/internal/embed/embed.go
)
GEN_FILE="cli/internal/vault/generation.go"

base="$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || git rev-list --max-parents=0 HEAD | tail -1)"
changed="$(git diff --name-only "$base"..HEAD)"

touched_logic=""
for f in "${WATCHED[@]}"; do
  if grep -qx "$f" <<<"$changed"; then touched_logic+="    $f"$'\n'; fi
done

if [ -z "$touched_logic" ]; then
  echo "check-index-generation: no watched index/embed logic files changed since $base — OK"
  exit 0
fi

# Logic changed. Did a generation constant get bumped in the diff? (Process
# substitution, not a pipe, so grep -q exiting early can't SIGPIPE git under
# `set -o pipefail` and read as a spurious "not bumped".)
if grep -qE '^\+[[:space:]]*(Index|Embed)Generation[[:space:]]*=' < <(git diff "$base"..HEAD -- "$GEN_FILE"); then
  echo "check-index-generation: index/embed logic changed and a generation constant was bumped since $base — OK"
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
--force-reembed; IndexGeneration for an index-only change).

If it genuinely does NOT need a reindex (comment/refactor/no-op), add a
'Reindex-Not-Needed: <reason>' trailer to a commit since $base.
EOF
exit 1
