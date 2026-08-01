#!/usr/bin/env bash
# Fail if known-malware / banned npm packages appear in package manifests or lockfiles.
# Usage:
#   check-blocked-deps.sh [ROOT_DIR]
#   check-blocked-deps.sh              # defaults to cwd
#
# Block list: deps-hygiene/blocked-packages.txt (override with BLOCKLIST_FILE).
set -euo pipefail

ROOT="${1:-.}"
ROOT="$(cd "$ROOT" && pwd)"

# Resolve symlinks so ~/bin/check-blocked-deps still finds ../blocked-packages.txt
_SOURCE="${BASH_SOURCE[0]}"
while [[ -L "$_SOURCE" ]]; do
  _DIR="$(cd "$(dirname "$_SOURCE")" && pwd)"
  _SOURCE="$(readlink "$_SOURCE")"
  [[ "$_SOURCE" != /* ]] && _SOURCE="$_DIR/$_SOURCE"
done
SCRIPT_DIR="$(cd "$(dirname "$_SOURCE")" && pwd)"
HYGIENE_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BLOCKLIST="${BLOCKLIST_FILE:-$HYGIENE_ROOT/blocked-packages.txt}"

if [[ ! -f "$BLOCKLIST" ]]; then
  echo "blocked-deps: missing block list: $BLOCKLIST" >&2
  exit 2
fi

if ! command -v rg >/dev/null 2>&1; then
  echo "blocked-deps: ripgrep (rg) is required" >&2
  exit 2
fi

MANIFESTS=()
while IFS= read -r f; do
  MANIFESTS+=("$f")
done < <(
  find "$ROOT" \
    \( -name node_modules -o -name .git -o -name cdk.out -o -name .next -o -name dist -o -name build -o -name .open-next -o -name vendor -o -name .turbo -o -name coverage -o -name worktrees -o -name .claude \) -prune -o \
    \( -name package.json -o -name package-lock.json -o -name pnpm-lock.yaml -o -name yarn.lock -o -name npm-shrinkwrap.json \) -type f -print \
    2>/dev/null | sort -u
)

if [[ ${#MANIFESTS[@]} -eq 0 ]]; then
  echo "blocked-deps: OK (no npm manifests under $ROOT)"
  exit 0
fi

fail=0

hit() {
  local file="$1" msg="$2"
  echo "blocked-deps: FAIL $file — $msg" >&2
  fail=1
}

while IFS= read -r line || [[ -n "$line" ]]; do
  # strip comments
  line="${line%%#*}"
  # trim
  line="${line#"${line%%[![:space:]]*}"}"
  line="${line%"${line##*[![:space:]]}"}"
  [[ -z "$line" ]] && continue

  pkg=""
  ver=""
  if [[ "$line" == @*/*@* ]]; then
    # @scope/name@version — version is after last @
    ver="${line##*@}"
    pkg="${line%@*}"
  elif [[ "$line" == @* ]]; then
    # @scope/name only
    pkg="$line"
  elif [[ "$line" == *@* ]]; then
    pkg="${line%@*}"
    ver="${line##*@}"
  else
    pkg="$line"
  fi

  for f in "${MANIFESTS[@]}"; do
    rel="${f#"$ROOT"/}"
    if [[ -z "$ver" ]]; then
      if rg -n --fixed-strings -- "$pkg" "$f" >/dev/null 2>&1; then
        hit "$rel" "banned package reference: $pkg"
        rg -n --fixed-strings -- "$pkg" "$f" >&2 || true
      fi
    else
      if rg -n --fixed-strings -- "$pkg" "$f" >/dev/null 2>&1; then
        if rg -n --fixed-strings -- "${pkg}@${ver}" "$f" >/dev/null 2>&1 \
          || rg -n --fixed-strings -- "${pkg}-${ver}" "$f" >/dev/null 2>&1 \
          || rg -n --fixed-strings -- "\"${ver}\"" "$f" >/dev/null 2>&1; then
          # Require package and version both present; for exact malware pins this is enough
          # Avoid pure version false positives by also requiring package on same file (already)
          if rg -n --fixed-strings -- "$ver" "$f" >/dev/null 2>&1; then
            # For version-only match with package also in file: flag
            hit "$rel" "banned pin: ${pkg}@${ver}"
          fi
        fi
      fi
    fi
  done
done < "$BLOCKLIST"

if [[ "$fail" -ne 0 ]]; then
  echo "blocked-deps: FAILED under $ROOT (blocklist: $BLOCKLIST)" >&2
  exit 1
fi

echo "blocked-deps: OK (${#MANIFESTS[@]} manifest(s) under $ROOT)"
