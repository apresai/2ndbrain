#!/usr/bin/env bash
#
# Build, Developer ID-sign, notarize, and staple the SecondBrain macOS app and
# its installer DMG entirely on this machine — no CI, no GitHub secrets. Produces
# a notarized SecondBrain-<VERSION>-arm64.dmg: a branded drag-to-Applications
# disk image whose enclosed .app launches with no Gatekeeper prompt.
#
# BOTH the app and the DMG are notarized and stapled (Apple distribution best
# practice). The app's own ticket lets it launch offline even after being dragged
# out of the image; the DMG's ticket lets the downloaded .dmg pass Gatekeeper
# offline. That means two notary round-trips, so expect two waits on Apple.
#
# Signing config (identity + notary key) is read from scripts/sign.env, which is
# gitignored. Code signing is asymmetric: the shipped artifacts embed only your
# PUBLIC certificate; the private key never leaves your keychain / cert store and
# never enters the repo. The DMG window layout lives in scripts/make-dmg.sh.
#
# Usage:
#   scripts/release-app-local.sh            build + sign + notarize + staple (app + DMG)
#   scripts/release-app-local.sh --publish  also upload the DMG to the existing
#                                            GitHub release v<VERSION> and update
#                                            the Homebrew cask (version + sha256)
#
# The --publish step requires the release v<VERSION> to already exist — it is
# created by CI (GoReleaser) on tag push, which also ships the CLI + plugin. Run
# this after that CI run completes. Both publish actions are idempotent (safe to
# re-run): `gh release upload --clobber` and a cask commit only when the sha
# actually changed.
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

CONFIG="scripts/sign.env"
if [ ! -f "$CONFIG" ]; then
  echo "error: $CONFIG not found." >&2
  echo "       Copy scripts/sign.env.example to $CONFIG and fill in your" >&2
  echo "       Developer ID identity and App Store Connect notary key." >&2
  exit 1
fi
# shellcheck disable=SC1090
source "$CONFIG"
: "${SIGN_IDENTITY:?set SIGN_IDENTITY in $CONFIG}"
: "${ASC_KEY_PATH:?set ASC_KEY_PATH in $CONFIG}"
: "${ASC_KEY_ID:?set ASC_KEY_ID in $CONFIG}"
: "${ASC_ISSUER_ID:?set ASC_ISSUER_ID in $CONFIG}"
# Expand a leading ~ / $HOME in the configured key path.
ASC_KEY_PATH="${ASC_KEY_PATH/#\~/$HOME}"
if [ ! -f "$ASC_KEY_PATH" ]; then
  echo "error: notary key not found at $ASC_KEY_PATH (set ASC_KEY_PATH in $CONFIG)" >&2
  exit 1
fi
# Fail fast on the DMG tool BEFORE the slow build+sign+notarize, not after.
if ! command -v create-dmg >/dev/null 2>&1; then
  echo "error: create-dmg not found (needed to build the installer DMG)." >&2
  echo "       Install it with:  brew install create-dmg" >&2
  exit 1
fi

VERSION="$(tr -d '\n' < VERSION)"
BUNDLE="app/.build/arm64-apple-macosx/release/SecondBrain.app"
DMG="SecondBrain-${VERSION}-arm64.dmg"

# When publishing, verify the release exists up front — BEFORE the slow
# build+sign+notarize — so running too early (CI/GoReleaser hasn't created
# release v<VERSION> yet) fails fast instead of burning a notarization.
if [ "${1:-}" = "--publish" ] && ! gh release view "v${VERSION}" >/dev/null 2>&1; then
  echo "error: GitHub release v${VERSION} not found." >&2
  echo "       Push the tag first (\`make release\`) and let CI create the release," >&2
  echo "       then re-run \`make release-app\`." >&2
  exit 1
fi

echo "==> Building release app (v${VERSION})"
make build-app-release >/dev/null

echo "==> Signing with Developer ID + hardened runtime"
codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$BUNDLE"
codesign --verify --strict --verbose=2 "$BUNDLE"

echo "==> Notarizing (uploads to Apple's notary service and waits)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
NOTARY_ZIP="$TMP/notarize.zip"
ditto -c -k --keepParent "$BUNDLE" "$NOTARY_ZIP"
xcrun notarytool submit "$NOTARY_ZIP" \
  --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" --wait

echo "==> Stapling the notarization ticket"
xcrun stapler staple "$BUNDLE"
xcrun stapler validate "$BUNDLE"

echo "==> Building the branded installer DMG"
bash scripts/make-dmg.sh "$BUNDLE" "$DMG"   # builds + Developer ID-signs the DMG

echo "==> Notarizing the DMG (uploads to Apple's notary service and waits)"
xcrun notarytool submit "$DMG" \
  --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" --wait

echo "==> Stapling the DMG"
xcrun stapler staple "$DMG"
xcrun stapler validate "$DMG"

SHA256="$(shasum -a 256 "$DMG" | awk '{print $1}')"

# Gatekeeper as a hard gate on BOTH artifacts (the end-to-end check users hit;
# codesign --verify and stapler validate above already gate each step). Staple
# AND notarize both: the app's own ticket lets it launch offline even after being
# dragged out of the image; the DMG's ticket lets the downloaded .dmg pass
# Gatekeeper offline. Neither alone covers the other.
if ! spctl -a -t exec -vv "$BUNDLE" 2>&1 | grep -q "source=Notarized Developer ID"; then
  echo "error: spctl did not accept the app bundle as Notarized Developer ID" >&2
  spctl -a -t exec -vv "$BUNDLE" >&2 || true
  exit 1
fi
if ! spctl -a -t open --context context:primary-signature -vv "$DMG" 2>&1 | grep -q "source=Notarized Developer ID"; then
  echo "error: spctl did not accept the DMG as Notarized Developer ID" >&2
  spctl -a -t open --context context:primary-signature -vv "$DMG" >&2 || true
  exit 1
fi

echo ""
echo "Notarized and stapled (app + DMG) — Gatekeeper accepted:"
echo "  artifact : ${DMG}"
echo "  sha256   : ${SHA256}"

if [ "${1:-}" != "--publish" ]; then
  echo ""
  echo "Next: re-run with --publish to upload + update the cask, or do it by hand."
  exit 0
fi

# --- publish: attach the notarized DMG to the release, then point the cask at it.
# (Release existence was already verified up front, before the notarization.)
echo ""
echo "==> Uploading ${DMG} to release v${VERSION}"
gh release upload "v${VERSION}" "$DMG" --clobber

# Update the cask AFTER the asset is in place, so the cask never points at a
# sha whose DMG isn't uploaded yet. (A brief window where the asset is new but
# the cask sha is old is unavoidable without atomic publish; this step is
# idempotent, so a re-run reconciles it.)
echo "==> Updating the Homebrew cask (version ${VERSION}, sha ${SHA256:0:12})"
TAP="$TMP/homebrew-tap"
gh repo clone apresai/homebrew-tap "$TAP" -- --depth 1 --quiet
mkdir -p "$TAP/Casks"
sed -e "s/CASK_VERSION/${VERSION}/g" -e "s/CASK_SHA256/${SHA256}/g" \
  casks/secondbrain.rb.tmpl > "$TAP/Casks/secondbrain.rb"
# Stage first, then check the index — detects a new (untracked) cask too, which
# `git diff` against the worktree alone would miss.
( cd "$TAP" && git add Casks/secondbrain.rb )
if ( cd "$TAP" && git diff --cached --quiet ); then
  echo "Cask already up to date — nothing to push."
else
  ( cd "$TAP" \
    && git commit -q -m "SecondBrain v${VERSION}: notarized cask (sha ${SHA256:0:12})" \
    && git push )
  echo "Cask updated and pushed."
fi
echo ""
echo "Published: brew install --cask apresai/tap/secondbrain  →  v${VERSION} (notarized)."
