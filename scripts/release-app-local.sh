#!/usr/bin/env bash
#
# Build, Developer ID-sign, notarize, and staple SecondBrain.app entirely on
# this machine — no CI, no GitHub secrets. Produces a notarized
# SecondBrain-<VERSION>-arm64.zip whose embedded .app launches with no
# Gatekeeper prompt (verified with `spctl`).
#
# Signing config (identity + notary key) is read from scripts/sign.env, which is
# gitignored. Code signing is asymmetric: the shipped app embeds only your
# PUBLIC certificate; the private key never leaves your keychain / cert store and
# never enters the repo.
#
# Usage:
#   scripts/release-app-local.sh   build + sign + notarize + staple + zip
#
# Produces SecondBrain-<VERSION>-arm64.zip in the repo root. Uploading it to a
# release and updating the Homebrew cask is a separate step (see the release
# docs) so this script stays focused on producing a verified notarized artifact.
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

VERSION="$(tr -d '\n' < VERSION)"
BUNDLE="app/.build/arm64-apple-macosx/release/SecondBrain.app"
ZIP="SecondBrain-${VERSION}-arm64.zip"

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

echo "==> Packaging ${ZIP}"
rm -f "$ZIP"
ditto -c -k --keepParent "$BUNDLE" "$ZIP"
SHA256="$(shasum -a 256 "$ZIP" | awk '{print $1}')"

# Gatekeeper assessment as a hard gate: fail loudly if the shipped artifact
# wouldn't be accepted (codesign --verify and stapler validate above already
# gate, but this is the end-to-end check users actually hit).
if ! spctl -a -t exec -vv "$BUNDLE" 2>&1 | grep -q "source=Notarized Developer ID"; then
  echo "error: spctl did not accept the bundle as Notarized Developer ID" >&2
  spctl -a -t exec -vv "$BUNDLE" >&2 || true
  exit 1
fi

echo ""
echo "Notarized and stapled — Gatekeeper accepted:"
echo "  artifact : ${ZIP}"
echo "  sha256   : ${SHA256}"
echo ""
echo "Next: attach ${ZIP} to the GitHub release and update the cask sha256."
