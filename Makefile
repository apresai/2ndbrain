.PHONY: build build-cli build-app build-app-release package-app install clean test test-gui test-all version-swift bump-major bump-minor bump-build release release-local update-changelog

VERSION := $(shell cat VERSION | tr -d '\n')
MAJOR := $(word 1,$(subst ., ,$(VERSION)))
MINOR := $(word 2,$(subst ., ,$(VERSION)))
BUILD := $(word 3,$(subst ., ,$(VERSION)))

version-swift:
	@echo '// Auto-generated from VERSION file — do not edit manually.' > app/Sources/SecondBrain/Version.swift
	@echo 'let appVersion = "$(VERSION)"' >> app/Sources/SecondBrain/Version.swift

build-cli:
	$(MAKE) -C cli build

APP_BUNDLE := app/.build/arm64-apple-macosx/debug/SecondBrain.app

build-app: version-swift
	cd app && swift build
	@mkdir -p $(APP_BUNDLE)/Contents/MacOS
	@mkdir -p $(APP_BUNDLE)/Contents/Resources
	@cp -f app/.build/arm64-apple-macosx/debug/SecondBrain $(APP_BUNDLE)/Contents/MacOS/SecondBrain
	@cp -f app/Resources/AppIcon.icns $(APP_BUNDLE)/Contents/Resources/AppIcon.icns
	@sed 's/VERSIONPLACEHOLDER/$(VERSION)/g' <<< '<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>SecondBrain</string><key>CFBundleIdentifier</key><string>dev.apresai.2ndbrain</string><key>CFBundleName</key><string>SecondBrain</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>VERSIONPLACEHOLDER</string><key>CFBundleVersion</key><string>VERSIONPLACEHOLDER</string><key>LSMinimumSystemVersion</key><string>14.0</string><key>NSHighResolutionCapable</key><true/><key>CFBundleIconFile</key><string>AppIcon</string></dict></plist>' > $(APP_BUNDLE)/Contents/Info.plist
	@codesign -s - --deep --force $(APP_BUNDLE) 2>/dev/null || true

APP_BUNDLE_RELEASE := app/.build/arm64-apple-macosx/release/SecondBrain.app

build-app-release: version-swift
	cd app && swift build -c release
	@mkdir -p $(APP_BUNDLE_RELEASE)/Contents/MacOS
	@mkdir -p $(APP_BUNDLE_RELEASE)/Contents/Resources
	@cp -f app/.build/arm64-apple-macosx/release/SecondBrain $(APP_BUNDLE_RELEASE)/Contents/MacOS/SecondBrain
	@cp -f app/Resources/AppIcon.icns $(APP_BUNDLE_RELEASE)/Contents/Resources/AppIcon.icns
	@sed 's/VERSIONPLACEHOLDER/$(VERSION)/g' <<< '<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>SecondBrain</string><key>CFBundleIdentifier</key><string>dev.apresai.2ndbrain</string><key>CFBundleName</key><string>SecondBrain</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>VERSIONPLACEHOLDER</string><key>CFBundleVersion</key><string>VERSIONPLACEHOLDER</string><key>LSMinimumSystemVersion</key><string>14.0</string><key>NSHighResolutionCapable</key><true/><key>CFBundleIconFile</key><string>AppIcon</string></dict></plist>' > $(APP_BUNDLE_RELEASE)/Contents/Info.plist
	@codesign -s - --deep --force $(APP_BUNDLE_RELEASE) 2>/dev/null || true

package-app: build-app-release
	@ditto -c -k --keepParent $(APP_BUNDLE_RELEASE) SecondBrain-$(VERSION)-arm64.zip
	@echo "Created SecondBrain-$(VERSION)-arm64.zip"
	@shasum -a 256 SecondBrain-$(VERSION)-arm64.zip

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

clean:
	$(MAKE) -C cli clean
	cd app && swift package clean

test:
	$(MAKE) -C cli test

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

test-all: test test-swift test-gui

bump-build:
	@echo "$(MAJOR).$(MINOR).$(shell echo $$(($(BUILD)+1)))" > VERSION
	@echo "Version: $$(cat VERSION)"
	@$(MAKE) version-swift

bump-minor:
	@echo "$(MAJOR).$(shell echo $$(($(MINOR)+1))).0" > VERSION
	@echo "Version: $$(cat VERSION)"
	@$(MAKE) version-swift

bump-major:
	@echo "$(shell echo $$(($(MAJOR)+1))).0.0" > VERSION
	@echo "Version: $$(cat VERSION)"
	@$(MAKE) version-swift

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
		git add CHANGELOG.md; \
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
	@echo "  Tag v$(VERSION) pushed — GitHub Actions will handle the rest!"
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo ""
	@echo "GitHub Actions will:"
	@echo "  1. Build CLI binaries (arm64 + x86_64)"
	@echo "  2. Build SecondBrain.app (arm64)"
	@echo "  3. Create GitHub release"
	@echo "  4. Update Homebrew tap:"
	@echo "     brew install apresai/tap/2nb"
	@echo "     brew install --cask apresai/tap/secondbrain"
	@echo ""
	@echo "Monitor: https://github.com/apresai/2ndbrain/actions"

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
