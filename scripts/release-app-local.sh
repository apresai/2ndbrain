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
# Local installer artifacts go under build/ (gitignored), not the repo root where
# they used to accumulate. NOT dist/: `make release-local` runs
# `goreleaser release --clean`, which wipes dist/. Keep in sync with the
# Makefile ARTIFACT_DIR. The uploaded GitHub asset name is still the basename
# (SecondBrain-<version>-arm64.dmg), so the cask URL and release-all verify are
# unaffected by the local path.
ARTIFACT_DIR="build"
DMG="${ARTIFACT_DIR}/SecondBrain-${VERSION}-arm64.dmg"

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

# Fail fast if the bundled CLI drifted from the app version. The point of
# bundling 2nb (Makefile build-app-release) is that the app and CLI ship at a
# single version; a mismatch here means the build copied a stale cli/bin/2nb.
BUNDLED_CLI="$BUNDLE/Contents/Resources/2nb"
if [ ! -x "$BUNDLED_CLI" ]; then
  echo "error: bundled CLI missing at $BUNDLED_CLI (build-app-release should copy cli/bin/2nb)." >&2
  exit 1
fi
# `|| true` so a binary that runs but prints no parseable version (e.g. a dyld
# failure to the swallowed stderr) leaves BUNDLED_CLI_VERSION empty and falls
# into the explicit mismatch branch below with its actionable message, rather
# than tripping `set -euo pipefail` on grep's no-match exit before we get there.
BUNDLED_CLI_VERSION="$("$BUNDLED_CLI" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
if [ "$BUNDLED_CLI_VERSION" != "$VERSION" ]; then
  echo "error: bundled CLI version ($BUNDLED_CLI_VERSION) != app version ($VERSION)." >&2
  echo "       Run 'make build-cli' so cli/bin/2nb is current, then re-run." >&2
  exit 1
fi
echo "==> Bundled CLI verified at v${BUNDLED_CLI_VERSION}"

echo "==> Signing with Developer ID + hardened runtime"
# Sign nested code inside-out: the bundled 2nb binary FIRST, then the outer
# bundle. The outer codesign is intentionally not --deep, so it would leave the
# nested executable unsigned — and an unsigned nested binary fails notarization.
codesign --force --options runtime --timestamp -i dev.apresai.2ndbrain.cli --sign "$SIGN_IDENTITY" "$BUNDLED_CLI"
codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$BUNDLE"
codesign --verify --strict --verbose=2 "$BUNDLE"

echo "==> Verifying load-command paths are portable (no dangling rpaths/dylibs)"
# A dangling LC_RPATH (e.g. the Xcode toolchain path `swift build` bakes in) or a
# non-system LC_LOAD_DYLIB resolves on THIS machine but not on a clean Mac, and is
# the documented SPM Gatekeeper footgun ("Apple could not verify ... is free of
# malware"). codesign --verify and stapler validate do NOT catch it. Gate here,
# before notarizing, so a bad binary fails fast instead of burning a notary wait.
check_portable_macho() {
  local b="$1" bad_rpath bad_dylib
  bad_rpath="$(otool -l "$b" | awk '/LC_RPATH/{getline;getline;print $2}' \
    | grep -vE '^(@executable_path|@loader_path|/usr/lib/swift)' || true)"
  bad_dylib="$(otool -L "$b" | tail -n +2 | awk '{print $1}' \
    | grep -E '^/' | grep -vE '^(/usr/lib/|/System/)' || true)"
  if [ -n "$bad_rpath" ] || [ -n "$bad_dylib" ]; then
    echo "error: non-portable load commands in $b (would dangle on a clean Mac):" >&2
    [ -n "$bad_rpath" ] && { echo "  dangling LC_RPATH:" >&2; echo "$bad_rpath" | sed 's/^/    /' >&2; }
    [ -n "$bad_dylib" ] && { echo "  external LC_LOAD_DYLIB:" >&2; echo "$bad_dylib" | sed 's/^/    /' >&2; }
    exit 1
  fi
}
check_portable_macho "$BUNDLE/Contents/MacOS/SecondBrain"
check_portable_macho "$BUNDLED_CLI"
# The bundled 2nb must ship with the hardened runtime (notarization requires it).
# Capture codesign's output first, then match a here-string: piping it into
# `grep -q` lets grep close the pipe on its first match, which SIGPIPEs codesign,
# and under `set -o pipefail` that surfaces as a false failure even though the
# runtime flag IS present.
cli_codesign="$(codesign -dvv "$BUNDLED_CLI" 2>&1 || true)"
if ! grep -qE 'flags=[^ ]*runtime' <<<"$cli_codesign"; then
  echo "error: bundled 2nb is not signed with the hardened runtime" >&2
  grep -i 'flags=' <<<"$cli_codesign" >&2 || true
  exit 1
fi

# notarize <artifact>: submit to Apple's notary service and poll to completion,
# surviving the intermittent notarytool SIGBUS. `xcrun notarytool submit --wait`
# reliably CRASHES (Bus error 10) mid-poll on the Xcode 26.x toolchain — a bug in
# Apple's tool (SIGBUS in String(format:) on a worker thread), NOT in our build:
# signing + upload succeed, only the wait crashes. So we submit WITHOUT --wait,
# capture the submission id, and poll `notarytool info` on that existing
# submission — a crashed poll returns empty and we simply poll again, with NO
# re-upload. Fails (returns 1) only if the final status is not Accepted.
notarize() {
  local artifact="$1"
  echo "  submitting $(basename "$artifact") to the notary service ..."
  local submit_json sub_id
  submit_json="$(xcrun notarytool submit "$artifact" \
    --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" \
    --output-format json 2>/dev/null || true)"
  sub_id="$(printf '%s' "$submit_json" | jq -r '.id // empty' 2>/dev/null || true)"
  if [ -z "$sub_id" ]; then
    echo "  ERROR: notarytool submit returned no id: $submit_json" >&2
    return 1
  fi
  echo "  submission $sub_id — polling to completion (retries through any notarytool crash, no re-upload) ..."
  local status=""
  for _ in $(seq 1 80); do   # up to ~20 min at 15s
    status="$(xcrun notarytool info "$sub_id" \
      --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" \
      --output-format json 2>/dev/null | jq -r '.status // empty' 2>/dev/null || true)"
    case "$status" in
      Accepted)
        echo "  notarized: $sub_id (Accepted)"
        return 0 ;;
      Invalid|Rejected)
        echo "  ERROR: notarization $status for $artifact" >&2
        xcrun notarytool log "$sub_id" \
          --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" 2>/dev/null | head -60 >&2 || true
        return 1 ;;
      *)
        # "In Progress", or empty from a crashed/failed poll — keep polling.
        sleep 15 ;;
    esac
  done
  echo "  ERROR: timed out waiting for notarization of $artifact (last status: ${status:-unknown})" >&2
  return 1
}

echo "==> Notarizing the app (self-healing poll; survives the notarytool SIGBUS)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
NOTARY_ZIP="$TMP/notarize.zip"
ditto -c -k --keepParent "$BUNDLE" "$NOTARY_ZIP"
notarize "$NOTARY_ZIP"

echo "==> Stapling the notarization ticket"
xcrun stapler staple "$BUNDLE"
xcrun stapler validate "$BUNDLE"

# Sweep prior local installer artifacts before building this one. Each release
# leaves a gitignored SecondBrain-<version>-arm64.dmg; every one is already
# uploaded to its GitHub release, so the local copies just pile up (13 stale DMGs
# / ~150 MB had accumulated by v0.11.0). create-dmg only clears the current
# version's output, so older versions linger — clear them all here (both the
# current build/ dir and the legacy repo-root location, plus the retired .zip
# format); the current version's DMG is rebuilt immediately below.
echo "==> Removing prior local installer artifacts"
mkdir -p "$ARTIFACT_DIR"
rm -f "$ARTIFACT_DIR"/SecondBrain-*.dmg "$ARTIFACT_DIR"/SecondBrain-*.zip \
      SecondBrain-*.dmg SecondBrain-*.zip

echo "==> Building the branded installer DMG"
bash scripts/make-dmg.sh "$BUNDLE" "$DMG"   # builds + Developer ID-signs the DMG

echo "==> Notarizing the DMG (self-healing poll)"
notarize "$DMG"

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
