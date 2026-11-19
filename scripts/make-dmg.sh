#!/usr/bin/env bash
#
# make-dmg.sh — build the branded SecondBrain installer DMG (the drag-to-
# Applications window) from an already-built SecondBrain.app, and code-sign the
# disk image.
#
# This is the single home for the DMG window layout, shared by two callers:
#   * `make package-app` (dev)        — a local DMG for a quick visual / drag test.
#   * scripts/release-app-local.sh    — builds the DMG here, then NOTARIZES +
#                                        STAPLES it. Notarization is NOT done in
#                                        this script: it needs the App Store
#                                        Connect key and only runs in the release
#                                        path.
#
# Signing is best-effort, keyed on scripts/sign.env (gitignored):
#   * sign.env present -> Developer ID-sign the DMG (+ secure timestamp), so the
#     release path can notarize it and a dev build matches the shipped artifact.
#   * sign.env absent  -> ad-hoc sign (`codesign -s -`): fine for a local shape
#     test, NOT distributable (a fresh Mac warns via Gatekeeper until notarized).
#
# Usage: scripts/make-dmg.sh <app-bundle> <output.dmg>
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

BUNDLE="${1:-}"
OUT_DMG="${2:-}"
if [ -z "$BUNDLE" ] || [ -z "$OUT_DMG" ]; then
  echo "usage: $0 <app-bundle> <output.dmg>" >&2
  exit 2
fi
if [ ! -d "$BUNDLE" ]; then
  echo "error: app bundle not found: $BUNDLE" >&2
  exit 1
fi
if ! command -v create-dmg >/dev/null 2>&1; then
  echo "error: create-dmg not found. Install it with:  brew install create-dmg" >&2
  exit 1
fi

BG="$ROOT/app/Resources/dmg-background.png"
VOLICON="$ROOT/app/Resources/AppIcon.icns"
DS_STORE_TEMPLATE="$ROOT/app/Resources/dmg-DS_Store"

finder_reachable() {
  [ "${HEADLESS_DMG:-}" = "1" ] && return 1
  python3 -c 'import subprocess, sys
r = subprocess.run(
  ["osascript", "-e", "with timeout of 2 seconds\ntell application \"Finder\" to get name\nend timeout"],
  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3
)
sys.exit(r.returncode)' 2>/dev/null
}

run_create_dmg_styled() {
  create-dmg \
    --volname "SecondBrain" \
    --volicon "$VOLICON" \
    --background "$BG" \
    --window-pos 200 120 \
    --window-size 660 400 \
    --icon-size 128 \
    --icon "SecondBrain.app" 165 200 \
    --app-drop-link 495 200 \
    --hdiutil-quiet \
    --no-internet-enable \
    "$OUT_DMG" "$STAGE"
}

run_create_dmg_headless() {
  rm -f "$OUT_DMG"
  local extra_args=(--skip-jenkins)
  if [ -f "$DS_STORE_TEMPLATE" ]; then
    extra_args+=(--add-file ".DS_Store" "$DS_STORE_TEMPLATE" 760 100)
  fi
  create-dmg \
    --volname "SecondBrain" \
    --volicon "$VOLICON" \
    --background "$BG" \
    "${extra_args[@]}" \
    --app-drop-link 495 200 \
    --hdiutil-quiet \
    --no-internet-enable \
    "$OUT_DMG" "$STAGE"
}

# Stage the bundle alone in a temp dir; create-dmg copies the folder's whole
# contents into the image. ditto (not cp) preserves the bundle's extended
# attributes, including any stapled notarization ticket the caller wrote.
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
ditto "$BUNDLE" "$STAGE/SecondBrain.app"

# create-dmg refuses to overwrite an existing output file.
rm -f "$OUT_DMG"

# Branded drag-to-install window. The icon centers (165,200) and (495,200) line
# up with the arrow gap baked into dmg-background.png. create-dmg occasionally
# exits non-zero on the Finder AppleScript / hdiutil detach step even when the
# DMG built fine (it already retries "Resource busy" up to 5x), so success is
# judged by the output file, not the exit code.
#
# When running over SSH/headless sessions or when Finder AppleEvents time out,
# fall back to `--skip-jenkins` so notarization and release never hang on Finder.
rc=0
if finder_reachable; then
  run_create_dmg_styled || rc=$?
  if [ ! -f "$OUT_DMG" ]; then
    echo "warning: styled create-dmg exited $rc without producing $OUT_DMG; retrying with --skip-jenkins" >&2
    rc=0
    run_create_dmg_headless || rc=$?
  fi
else
  echo "==> Finder AppleEvents unavailable (headless/SSH session); building DMG with --skip-jenkins"
  run_create_dmg_headless || rc=$?
fi

if [ ! -f "$OUT_DMG" ]; then
  echo "error: create-dmg did not produce $OUT_DMG (exit $rc)" >&2
  [ "$rc" -ne 0 ] && exit "$rc"
  exit 1   # no file but rc==0: still a failure, never exit 0 here
fi
if [ "$rc" -ne 0 ]; then
  echo "warning: create-dmg exited $rc but produced the DMG (known Finder/hdiutil flakiness); continuing" >&2
fi

# Sign the disk image. Developer ID when sign.env is available (the maintainer's
# machine, release + dev), else ad-hoc (fresh worktree / no signing config).
SIGN_ENV="$ROOT/scripts/sign.env"
if [ -f "$SIGN_ENV" ]; then
  # shellcheck disable=SC1090
  source "$SIGN_ENV"
fi
if [ -n "${SIGN_IDENTITY:-}" ]; then
  echo "==> Signing DMG with Developer ID (+ secure timestamp)"
  codesign --force --timestamp --sign "$SIGN_IDENTITY" "$OUT_DMG"
else
  echo "==> Signing DMG ad-hoc (no scripts/sign.env — local use only, not distributable)"
  codesign --force --sign - "$OUT_DMG"
fi

echo "Built ${OUT_DMG}"
