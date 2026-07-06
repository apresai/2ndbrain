.PHONY: build build-cli build-app build-app-release package-app notarize-app release-app release-all install clean clean-dmg test test-battery test-usage test-swift test-gui test-all version-swift version-plugin set-version bump-major bump-minor bump-build release release-local update-changelog sync-skills check-skills-sync

VERSION := $(shell cat VERSION | tr -d '\n')
MAJOR := $(word 1,$(subst ., ,$(VERSION)))
MINOR := $(word 2,$(subst ., ,$(VERSION)))
BUILD := $(word 3,$(subst ., ,$(VERSION)))

# Where local macOS installer artifacts (the branded DMG) are written. A
# dedicated gitignored dir keeps them out of the repo root, where they used to
# accumulate one-per-release. NOT dist/: `make release-local` runs
# `goreleaser release --clean`, which wipes dist/ and would delete the DMG.
ARTIFACT_DIR := build

version-swift:
	@echo '// Auto-generated from VERSION file - do not edit manually.' > app/Sources/SecondBrain/Version.swift
	@echo 'let appVersion = "$(VERSION)"' >> app/Sources/SecondBrain/Version.swift

# Sync the Obsidian plugin's manifest.json + package.json + package-lock.json
# to VERSION so the plugin releases at the product version (aligned from
# 0.8.0 onward). Refuses to LOWER the plugin version (Obsidian/BRAT only see
# increases as updates); minAppVersion is untouched; release CI fails on any
# drift (parity guard in .github/workflows/release.yml).
version-plugin:
	@node scripts/sync-plugin-version.js

# Regenerate the in-repo SKILL.md mirrors (.agents/.warp/.claude) from the
# canonical Go-embedded source cli/internal/skills/content/2ndbrain-skill.md,
# so agents that open the repo discover the 2nb skill with zero install.
sync-skills:
	@scripts/sync-skill.sh

# Fail if any mirror has drifted from the embedded source (the CI parity guard,
# same shape as the plugin-manifest-vs-VERSION gate in release.yml). Fix with
# `make sync-skills`.
check-skills-sync:
	@scripts/sync-skill.sh --check

# Fail if index/embed LOGIC changed since the last release tag without bumping a
# vault generation constant (which is what prompts users to reindex/re-embed).
# See cli/internal/vault/generation.go and scripts/check-index-generation.sh.
check-index-generation:
	@scripts/check-index-generation.sh

# One-shot version set across every product: make set-version V=0.8.0
set-version:
	@test -n "$(V)" || { echo "usage: make set-version V=x.y.z"; exit 1; }
	@echo "$(V)" > VERSION
	@$(MAKE) version-swift version-plugin
	@echo "Version: $$(cat VERSION)"

build-cli:
	$(MAKE) -C cli build

APP_BUNDLE := app/.build/arm64-apple-macosx/debug/SecondBrain.app

build-app: version-swift build-cli
	cd app && swift build
	@mkdir -p $(APP_BUNDLE)/Contents/MacOS
	@mkdir -p $(APP_BUNDLE)/Contents/Resources
	@cp -f app/.build/arm64-apple-macosx/debug/SecondBrain $(APP_BUNDLE)/Contents/MacOS/SecondBrain
	@# Bundle the version-matched 2nb CLI so the app never shells out to a stale
	@# Homebrew formula (CLIPath.resolve() prefers Contents/Resources/2nb).
	@cp -f cli/bin/2nb $(APP_BUNDLE)/Contents/Resources/2nb
	@cp -f app/Resources/AppIcon.icns $(APP_BUNDLE)/Contents/Resources/AppIcon.icns
	@sed 's/VERSIONPLACEHOLDER/$(VERSION)/g' <<< '<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>SecondBrain</string><key>CFBundleIdentifier</key><string>dev.apresai.2ndbrain</string><key>CFBundleName</key><string>SecondBrain</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>VERSIONPLACEHOLDER</string><key>CFBundleVersion</key><string>VERSIONPLACEHOLDER</string><key>LSMinimumSystemVersion</key><string>14.0</string><key>NSHighResolutionCapable</key><true/><key>CFBundleIconFile</key><string>AppIcon</string></dict></plist>' > $(APP_BUNDLE)/Contents/Info.plist
	@codesign -s - --deep --force $(APP_BUNDLE) 2>/dev/null || true

APP_BUNDLE_RELEASE := app/.build/arm64-apple-macosx/release/SecondBrain.app

build-app-release: version-swift build-cli
	cd app && swift build -c release
	@# Start from a clean bundle so stale files can't leak into a
	@# signed/notarized release artifact, then bundle the freshly-built,
	@# version-matched 2nb CLI under Contents/Resources/2nb. The build-cli
	@# prerequisite guarantees the copied binary is this release's version, so
	@# the app can never shell out to an older Homebrew-installed CLI (the
	@# "0.5.8 re-embed bug"). release-app-local.sh Developer ID-signs this
	@# nested binary before signing the outer bundle.
	@rm -rf $(APP_BUNDLE_RELEASE)
	@mkdir -p $(APP_BUNDLE_RELEASE)/Contents/MacOS
	@mkdir -p $(APP_BUNDLE_RELEASE)/Contents/Resources
	@cp -f app/.build/arm64-apple-macosx/release/SecondBrain $(APP_BUNDLE_RELEASE)/Contents/MacOS/SecondBrain
	@cp -f cli/bin/2nb $(APP_BUNDLE_RELEASE)/Contents/Resources/2nb
	@cp -f app/Resources/AppIcon.icns $(APP_BUNDLE_RELEASE)/Contents/Resources/AppIcon.icns
	@sed 's/VERSIONPLACEHOLDER/$(VERSION)/g' <<< '<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>SecondBrain</string><key>CFBundleIdentifier</key><string>dev.apresai.2ndbrain</string><key>CFBundleName</key><string>SecondBrain</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>VERSIONPLACEHOLDER</string><key>CFBundleVersion</key><string>VERSIONPLACEHOLDER</string><key>LSMinimumSystemVersion</key><string>14.0</string><key>NSHighResolutionCapable</key><true/><key>CFBundleIconFile</key><string>AppIcon</string></dict></plist>' > $(APP_BUNDLE_RELEASE)/Contents/Info.plist
	@# Strip non-portable LC_RPATHs that `swift build` bakes into the executable
	@# (notably the absolute Xcode toolchain path). They dangle on a Mac without
	@# Xcode and are the documented SPM Gatekeeper footgun. Keep only the portable
	@# entries; done before signing so the (re)signature seals the cleaned binary.
	@exe=$(APP_BUNDLE_RELEASE)/Contents/MacOS/SecondBrain; \
	otool -l "$$exe" | awk '/LC_RPATH/{getline;getline;print $$2}' \
	  | grep -vE '^(@executable_path|@loader_path|/usr/lib/swift)' \
	  | while read -r rp; do echo "  strip dangling rpath: $$rp"; \
	      install_name_tool -delete_rpath "$$rp" "$$exe" 2>/dev/null || true; done
	@codesign -s - --deep --force $(APP_BUNDLE_RELEASE) 2>/dev/null || true

package-app: build-app-release
	@mkdir -p $(ARTIFACT_DIR)
	@bash scripts/make-dmg.sh $(APP_BUNDLE_RELEASE) $(ARTIFACT_DIR)/SecondBrain-$(VERSION)-arm64.dmg
	@shasum -a 256 $(ARTIFACT_DIR)/SecondBrain-$(VERSION)-arm64.dmg

# Local Developer ID signing + Apple notarization (keys stay on this machine).
# Reads scripts/sign.env (gitignored). notarize-app produces a notarized,
# Gatekeeper-clean SecondBrain-<VERSION>-arm64.dmg (app + DMG both stapled);
# release-app additionally uploads it to the existing release v<VERSION> and
# updates the Homebrew cask. Run release-app AFTER `make release` (CI creates the
# release + ships CLI/plugin).
notarize-app:
	@bash scripts/release-app-local.sh

release-app:
	@bash scripts/release-app-local.sh --publish

# The one-command unified release: bump -> tag -> wait for CI (CLI formula +
# plugin assets) -> sign/notarize/publish the app + cask -> verify everything
# shipped at one version. BUMP=build|minor|major|none; SKIP_TESTS=1 for re-runs.
# Canonical clone only (needs gitignored scripts/sign.env).
release-all:
	@bash scripts/release-all.sh

build: build-cli build-app

install-app: build-app
	@rm -rf ~/Applications/SecondBrain.app
	@mkdir -p ~/Applications
	@cp -R $(APP_BUNDLE) ~/Applications/SecondBrain.app
	@codesign -s - --deep --force ~/Applications/SecondBrain.app
	@echo "Installed SecondBrain.app to ~/Applications"

install: build install-app
	$(MAKE) -C cli install
	@echo "Installed 2nb to /usr/local/bin and SecondBrain.app to ~/Applications"

clean: clean-dmg
	$(MAKE) -C cli clean
	cd app && swift package clean

# Sweep the local installer artifacts (one per release) that package-app /
# release-app-local.sh produce. They are gitignored, already uploaded to their
# GitHub release, so the local copies are redundant and otherwise accumulate
# (e.g. 13 stale DMGs / ~150 MB by v0.11.0). Sweeps BOTH the current $(ARTIFACT_DIR)
# and the legacy repo-root location, and the retired pre-0.9.x .zip format (whose
# sweep the old .dmg-only rule missed, so 17 zips / ~83 MB had piled up).
# release-app-local.sh runs this automatically before building a fresh DMG.
clean-dmg:
	@rm -f SecondBrain-*.dmg SecondBrain-*.zip \
	       $(ARTIFACT_DIR)/SecondBrain-*.dmg $(ARTIFACT_DIR)/SecondBrain-*.zip \
	  && echo "Removed local SecondBrain-* installer artifacts (.dmg/.zip)"

test:
	$(MAKE) -C cli test

# Golden-path end-to-end battery: one curated scenario per critical flow
# (vault lifecycle, document CRUD, index rebuild, threshold, MCP lifecycle,
# skills roundtrip). Faster to diagnose than the full test suite when one
# of these flows regresses.
test-battery:
	$(MAKE) -C cli test-battery

# Usage suite: validates how we actually use 2nb. MCP write->query index
# round-trips plus a runnable end-to-end battery (real binary + real mcp-server
# over stdio). Catches index-consistency regressions (e.g. a write tool that
# skips reindex). AI-gated steps skip without provider credentials.
test-usage:
	$(MAKE) -C cli test-usage

# 2NB_TEST makes the 2nb subprocesses the Swift tests spawn (vault create /
# vault set) skip writing the real ~/.2ndbrain-vaults recents and skip reading
# the developer's Obsidian registry, so the suite never pollutes recents or binds
# the developer's live vault.
# Exported for the whole swift-test process so it covers every target regardless
# of run order; the per-target run-once setenv is the bare-swift-test fallback.
test-swift: export 2NB_TEST := 1
test-swift:
	cd app && swift test

test-gui: install-app
	SKIP_BUILD=1 ./tests/gui-test-crud.sh
	SKIP_BUILD=1 ./tests/gui-test-navigation.sh
	SKIP_BUILD=1 ./tests/gui-test-editor.sh
	SKIP_BUILD=1 ./tests/gui-test-ui.sh
	SKIP_BUILD=1 ./tests/gui-test-ai.sh
	SKIP_BUILD=1 ./tests/gui-test-index.sh
	SKIP_BUILD=1 ./tests/gui-test-graph.sh
	SKIP_BUILD=1 ./tests/gui-test-vault-switch.sh
	SKIP_BUILD=1 ./tests/gui-test-polish.sh

test-all: test test-battery test-usage test-swift test-gui

bump-build:
	@echo "$(MAJOR).$(MINOR).$(shell echo $$(($(BUILD)+1)))" > VERSION
	@echo "Version: $$(cat VERSION)"
	@$(MAKE) version-swift version-plugin

bump-minor:
	@echo "$(MAJOR).$(shell echo $$(($(MINOR)+1))).0" > VERSION
	@echo "Version: $$(cat VERSION)"
	@$(MAKE) version-swift version-plugin

bump-major:
	@echo "$(shell echo $$(($(MAJOR)+1))).0.0" > VERSION
	@echo "Version: $$(cat VERSION)"
	@$(MAKE) version-swift version-plugin

update-changelog:
	@echo "Updating CHANGELOG.md with version $(VERSION)..."
	@bash scripts/update-changelog.sh $(VERSION)

release:
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "  Creating Release v$(VERSION)"
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo ""
	@echo "Step 1: Updating CHANGELOG.md..."
	@$(MAKE) update-changelog
	@echo ""
	@echo "Step 2: Checking for uncommitted changes..."
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Found changes, committing..."; \
		git add CHANGELOG.md VERSION app/Sources/SecondBrain/Version.swift plugins/obsidian-2ndbrain/manifest.json plugins/obsidian-2ndbrain/package.json plugins/obsidian-2ndbrain/package-lock.json; \
		git commit -m "Release v$(VERSION)"; \
		git push origin main; \
		echo "Changes committed and pushed"; \
	else \
		echo "No changes to commit"; \
	fi
	@echo ""
	@echo "Step 3: Creating git tag v$(VERSION)..."
	@git tag -a v$(VERSION) -m "Release v$(VERSION)" || (echo "Tag already exists" && exit 1)
	@git push origin v$(VERSION)
	@echo "Tag v$(VERSION) created and pushed"
	@echo ""
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "  Tag v$(VERSION) pushed — this is a TWO-STEP release."
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo ""
	@echo "GitHub Actions is now building (CLI only):"
	@echo "  • CLI binaries (arm64 + x86_64) → formula 'twonb'"
	@echo "  • Obsidian plugin assets"
	@echo "  • the GitHub release v$(VERSION)"
	@echo "  Monitor: https://github.com/apresai/2ndbrain/actions"
	@echo ""
	@echo "  ⚠ THE MACOS APP IS NOT BUILT BY CI. Once the run above finishes,"
	@echo "    finish the release locally (Developer ID sign + notarize + cask):"
	@echo ""
	@echo "        make release-app"
	@echo ""
	@echo "  Until you do, the cask still points at the PREVIOUS app version."
	@echo "  (Next time: 'make release-all' runs both steps and verifies.)"

release-local:
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "  Creating Local Release v$(VERSION)"
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@$(MAKE) update-changelog
	@if [ -n "$$(git status --porcelain)" ]; then \
		git add CHANGELOG.md; \
		git commit -m "Release v$(VERSION)"; \
		git push origin main; \
	fi
	@git tag -a v$(VERSION) -m "Release v$(VERSION)" || true
	@git push origin v$(VERSION) || true
	goreleaser release --clean
	@echo "Local release v$(VERSION) complete"
