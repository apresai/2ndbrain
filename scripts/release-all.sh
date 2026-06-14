#!/usr/bin/env bash
# release-all: the one-command unified release. Version-aligns and ships the
# CLI (Homebrew formula), the Obsidian plugin (release assets), and the macOS
# app (notarized cask) in a single run:
#
#   bump -> changelog/tag/push -> wait for CI -> sign/notarize/publish app -> verify
#
# Usage (from the canonical clone; worktrees lack the gitignored sign.env):
#   make release-all                 # bump-build, full release
#   make release-all BUMP=minor      # minor bump
#   make release-all BUMP=none       # release the current VERSION as-is
#                                    # (e.g. after `make set-version V=0.8.0`)
#   SKIP_TESTS=1 make release-all    # skip the Go test gate (re-runs only)
set -euo pipefail

BUMP="${BUMP:-build}"

die() {
  echo "error: $*" >&2
  exit 1
}

# --- preflight ---------------------------------------------------------------
echo "==> Preflight"

[ -f scripts/sign.env ] || die "scripts/sign.env not found. Run from the canonical clone (worktrees don't carry gitignored signing config); template: scripts/sign.env.example"

command -v create-dmg >/dev/null 2>&1 || die "create-dmg not found (needed to build the installer DMG). Install: brew install create-dmg"

BRANCH=$(git rev-parse --abbrev-ref HEAD)
[ "$BRANCH" = "main" ] || die "releases run from main (currently on $BRANCH)"

# A clean tree, except: uncommitted VERSION-file changes are the expected
# state right after `make set-version` (the BUMP=none flow) and are committed
# by `make release` exactly like a bump's output. Anything else must be
# committed or stashed first.
ALLOWED_DIRTY="VERSION app/Sources/SecondBrain/Version.swift plugins/obsidian-2ndbrain/manifest.json plugins/obsidian-2ndbrain/package.json plugins/obsidian-2ndbrain/package-lock.json"
while IFS= read -r f; do
  [ -z "$f" ] && continue
  case " $ALLOWED_DIRTY " in
    *" $f "*) ;;
    *) die "working tree has uncommitted changes ($f); commit or stash first" ;;
  esac
done <<EOF_DIRTY
$(git diff --name-only HEAD)
EOF_DIRTY

git fetch origin main --quiet
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || die "main is not in sync with origin/main; pull or push first"

gh auth status >/dev/null 2>&1 || die "gh is not authenticated (gh auth login)"

if [ "${SKIP_TESTS:-}" != "1" ]; then
  echo "==> Test gate (cd cli && make test); SKIP_TESTS=1 to skip on re-runs"
  make -C cli test
else
  echo "==> Test gate SKIPPED (SKIP_TESTS=1)"
fi

command -v node >/dev/null || die "node is required (reads the plugin manifest for the version-parity check)"

# --- bump ---------------------------------------------------------------------
case "$BUMP" in
  build|minor|major)
    echo "==> Bumping version ($BUMP)"
    make "bump-$BUMP"
    ;;
  none)
    echo "==> BUMP=none: releasing current VERSION as-is"
    ;;
  *)
    die "BUMP must be build|minor|major|none (got '$BUMP')"
    ;;
esac

VERSION=$(tr -d '\n' < VERSION)
echo "==> Releasing v${VERSION}"

# Version-parity across products (also catches BUMP=none with a desynced
# manifest before anything is tagged).
MANIFEST_VERSION=$(node -p "require('./plugins/obsidian-2ndbrain/manifest.json').version")
[ "$MANIFEST_VERSION" = "$VERSION" ] || die "plugin manifest ($MANIFEST_VERSION) != VERSION ($VERSION). Run 'make version-plugin' (it refuses downgrades; see set-version)."

# --- CI half: changelog, commit, tag, push (existing target) -------------------
# Resume support: a previous run that failed after tagging (CI red, or a
# notarization hiccup in release-app) left tag v$VERSION at HEAD. In that
# case skip the tag step and continue: the CI watch below re-checks the run
# (rerun a red one with `gh run rerun <id> --failed` first), then release-app
# and verification re-run idempotently.
if git rev-parse -q --verify "refs/tags/v${VERSION}" >/dev/null; then
  if [ "$(git rev-parse "v${VERSION}^{commit}")" = "$(git rev-parse HEAD)" ]; then
    echo "==> Tag v${VERSION} already exists at HEAD; resuming a previous run (skipping tag/push)"
  else
    die "tag v${VERSION} exists but does not point at HEAD; bump to a new version"
  fi
else
  make release
fi

# --- wait for the tag's CI run -------------------------------------------------
echo "==> Waiting for the release workflow on v${VERSION}"
RUN_ID=""
for _ in $(seq 1 18); do
  RUN_ID=$(gh run list --workflow=release.yml --limit 10 \
    --json databaseId,headBranch \
    --jq "[.[] | select(.headBranch == \"v${VERSION}\") | .databaseId] | first // empty")
  [ -n "$RUN_ID" ] && break
  sleep 10
done
[ -n "$RUN_ID" ] || die "no release.yml run appeared for v${VERSION} after 3 minutes; check https://github.com/apresai/2ndbrain/actions"

echo "==> Watching run ${RUN_ID} (CLI binaries + formula + plugin assets)"
gh run watch "$RUN_ID" --exit-status --interval 15 \
  || die "CI release failed. Inspect: gh run view ${RUN_ID} --log-failed
Then rerun the red jobs:        gh run rerun ${RUN_ID} --failed
And resume this release:        make release-all BUMP=none SKIP_TESTS=1"

# --- local half: sign, notarize, publish app + cask ----------------------------
echo "==> macOS app: sign, notarize, publish (make release-app)"
make release-app

# --- verify --------------------------------------------------------------------
echo "==> Verifying release v${VERSION}"
ASSETS=$(gh release view "v${VERSION}" --json assets --jq '[.assets[].name] | join(" ")')
for needed in \
  "2nb_${VERSION}_Darwin_arm64.tar.gz" \
  "2nb_${VERSION}_Darwin_x86_64.tar.gz" \
  "manifest.json" "main.js" "styles.css" \
  "SecondBrain-${VERSION}-arm64.dmg"; do
  case " $ASSETS " in
    *" $needed "*) ;;
    *) die "release asset missing: $needed (have: $ASSETS)" ;;
  esac
done

FORMULA_VERSION=$(gh api -H "Accept: application/vnd.github.raw" repos/apresai/homebrew-tap/contents/twonb.rb | grep -m1 -oE '[0-9]+\.[0-9]+\.[0-9]+' || true)
CASK_VERSION=$(gh api -H "Accept: application/vnd.github.raw" repos/apresai/homebrew-tap/contents/Casks/secondbrain.rb | grep -m1 -oE '[0-9]+\.[0-9]+\.[0-9]+' || true)
[ "$FORMULA_VERSION" = "$VERSION" ] || die "tap formula version ($FORMULA_VERSION) != ${VERSION}"
[ "$CASK_VERSION" = "$VERSION" ] || die "tap cask version ($CASK_VERSION) != ${VERSION}"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  v${VERSION} released: CLI + plugin + app, all aligned."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  brew upgrade apresai/tap/twonb"
echo "  brew upgrade --cask apresai/tap/secondbrain"
echo "  2nb plugin install   (or update via BRAT)"
