#!/usr/bin/env bash
# check-docs-links.sh: verify every relative markdown link in CLAUDE.md,
# README.md, AGENTS.md, CONTRIBUTING.md, and docs/**/*.md resolves to a real
# file. CLAUDE.md is rules-and-pointers after the diet, so a broken pointer is
# a broken instruction, not cosmetic. External links (scheme://, mailto:) and
# pure #anchors are skipped; an #anchor suffix on a file link is stripped
# before the existence check (anchor validity is not checked here).
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
while IFS= read -r file; do
  dir=$(dirname "$file")
  # Extract (target) parts of [text](target) links, one per line.
  while IFS= read -r target; do
    [[ -z "$target" ]] && continue
    case "$target" in
      *://*|mailto:*|\#*) continue ;;
    esac
    # Only check links that point at markdown files.
    path="${target%%#*}"
    [[ "$path" == *.md ]] || continue
    if [[ "$path" = /* ]]; then
      resolved=".$path"
    else
      resolved="$dir/$path"
    fi
    if [[ ! -f "$resolved" ]]; then
      echo "BROKEN: $file -> $target"
      fail=1
    fi
  done < <(awk '/^[[:space:]]*```/{fence=!fence; next} !fence' "$file" \
             | sed 's/`[^`]*`//g' \
             | grep -o '](\([^)]*\))' 2>/dev/null | sed 's/^](//; s/)$//' || true)
done < <(printf '%s\n' CLAUDE.md README.md AGENTS.md CONTRIBUTING.md; find docs -name '*.md' -type f)

if [[ "$fail" -ne 0 ]]; then
  echo "FAIL: broken relative markdown links (see BROKEN lines above)"
  exit 1
fi
echo "docs links OK: every relative .md link resolves"
