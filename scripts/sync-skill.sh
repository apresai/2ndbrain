#!/usr/bin/env bash
#
# sync-skill.sh — keep the in-repo SKILL.md mirrors identical to the canonical
# embedded source.
#
# The source of truth for the 2nb skill is the Go-embedded file
#   cli/internal/skills/content/2ndbrain-skill.md
# (`//go:embed`, which cannot reference files above its package dir, so the
# source has to live there). For zero-install agent discovery we also ship the
# same content at the repo-root paths agents walk up to find:
#   .agents/skills/2nb/SKILL.md   (Warp-recommended primary)
#   .warp/skills/2nb/SKILL.md
#   .claude/skills/2nb/SKILL.md
#
# Modes:
#   sync-skill.sh            copy the source over each mirror (default)
#   sync-skill.sh --check    diff each mirror against the source; exit 1 on any
#                            drift (used as the CI gate, mirroring the
#                            plugin-manifest-vs-VERSION guard in release.yml).
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

SRC="cli/internal/skills/content/2ndbrain-skill.md"
MIRRORS=(
  ".agents/skills/2nb/SKILL.md"
  ".warp/skills/2nb/SKILL.md"
  ".claude/skills/2nb/SKILL.md"
)

if [[ ! -f "$SRC" ]]; then
  echo "error: canonical skill source not found: $SRC" >&2
  exit 1
fi

mode="sync"
if [[ "${1:-}" == "--check" ]]; then
  mode="check"
fi

if [[ "$mode" == "check" ]]; then
  drift=0
  for m in "${MIRRORS[@]}"; do
    if [[ ! -f "$m" ]]; then
      echo "::error::missing skill mirror: $m" >&2
      drift=1
      continue
    fi
    if ! diff -q "$SRC" "$m" >/dev/null; then
      echo "::error::$m has drifted from $SRC" >&2
      drift=1
    fi
  done
  if [[ "$drift" -ne 0 ]]; then
    echo "Run 'make sync-skills' and commit the result to fix skill-mirror drift." >&2
    exit 1
  fi
  echo "All ${#MIRRORS[@]} skill mirrors match $SRC."
  exit 0
fi

for m in "${MIRRORS[@]}"; do
  mkdir -p "$(dirname "$m")"
  cp "$SRC" "$m"
  echo "synced $m"
done
echo "Synced ${#MIRRORS[@]} skill mirrors from $SRC."
