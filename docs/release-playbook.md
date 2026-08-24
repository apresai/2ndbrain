# Release Playbook

The complete release mechanics for 2ndbrain: how the version is stamped across the three products, when a release must flag a reindex, and how the two-step release pipeline ships the CLI, the Obsidian plugin, and the signed, notarized macOS app. This document is the source of record for release process; `CLAUDE.md` points here.

## Versioning

Format: `major.minor.build`. Single source of truth: the `VERSION` file at the repo root. Each product consumes it as follows:

- **Go CLI**: `cli/Makefile` injects it via LDFLAGS into `internal/cli.Version`.
- **Swift app**: the build generates `app/Sources/SecondBrain/Version.swift` from it. Never edit that file by hand.
- **Obsidian plugin**: `manifest.json`, `package.json`, and `package-lock.json` are synced from it by `make version-plugin` (versions aligned from 0.8.0 onward). Release CI fails if the manifest drifts from `VERSION`. The sync refuses to lower the plugin version, because Obsidian and BRAT only see increases as updates.

Bump targets (root `Makefile`):

| Target | Effect |
|---|---|
| `make bump-build` | `0.1.0` → `0.1.1` |
| `make bump-minor` | `0.1.1` → `0.2.0` |
| `make bump-major` | `0.2.0` → `1.0.0` |
| `make set-version V=x.y.z` | Set an explicit version across all products (used for the one-time 0.8.0 alignment jump) |

Every bump target regenerates `Version.swift` and the plugin version files.

## Reindex-on-release flag (`IndexGeneration` / `EmbedGeneration`)

When a release changes indexing or embedding LOGIC (chunk boundaries, chunk-to-vector mapping, embedding purpose or pooling) but NOT the model, dimension, or DB schema, a user's existing index is silently stale: neither `VectorCompat`'s model/dim check nor the per-row content hash can detect it. Two monotonic counters in `cli/internal/vault/generation.go` are the release flag:

- Bump **`EmbedGeneration`** when the change needs a full re-embed (`2nb index --force-reembed`), because `vec_chunks` must be regenerated to match the rebuilt chunks.
- Bump **`IndexGeneration`** for an index-only change (`2nb index`).

The counters are stamped into the index DB's `meta` table on a full index or force-reembed (`vault.StampAfterIndex`) and compared at runtime by `vault.CheckIndexFreshness`, which surfaces `upgrade_reindex_recommended` / `upgrade_reembed_recommended` through `derivePortability`, so `vault status`, `ai status`, and `config doctor` all prompt the fix. Always prompt, never auto-spend.

`make check-index-generation` runs in release CI and fails if a watched indexing/embedding/resolution file changed since the last tag without a generation bump. Either bump the appropriate constant, or add a `Reindex-Not-Needed: <reason>` commit trailer when the change genuinely needs no reindex.

When you bump a generation, add a warning line to the CHANGELOG ("reindex recommended: `2nb index --force-reembed`") so it also lands in the release notes.

## Release channels

Both the CLI and the macOS app are published via Homebrew:

```bash
brew install apresai/tap/2nb                    # CLI only
brew install --cask apresai/tap/secondbrain     # macOS dashboard app (depends on CLI)
```

## Release contract (`.release.yaml`)

The machine-readable release contract lives in `.release.yaml` at the repo root: the front-door command, every product, and each product's install and verify command. It is what the `oss-release` skill reads to release and verify this repo. The skill encodes the invariants; `.release.yaml` encodes this project's implementation. A packaging change therefore updates the Makefile (and `.release.yaml` only if a channel or command changes), never the skill. Keep it in sync with the pipeline below.

## Pipeline

**`make release-all`** is the front door: one command (canonical clone only; needs the gitignored `scripts/sign.env`) that runs the test gate, bumps (`BUMP=build|minor|major|none`), tags, waits for CI, then signs, notarizes, and publishes the app plus cask, and verifies every product shipped at one version (`scripts/release-all.sh`). The underlying steps remain available individually.

A release is **two steps**: CI ships the CLI and plugin; the macOS app is signed, notarized, and published from the maintainer's machine (signing keys never leave it and never enter CI).

1. `make bump-build` (or `bump-minor` / `bump-major`): increment `VERSION`, regenerate `Version.swift`, sync the plugin version files.
2. `make release`: updates `CHANGELOG.md`, commits, tags `v<VERSION>`, pushes the tag.
3. GitHub Actions (`.github/workflows/release.yml`) on tag push: macos-latest, `CGO_ENABLED=0` (pure-Go modernc). GoReleaser builds the CLI for arm64 and x86_64 from one runner (no C toolchain, so x86_64 genuinely builds; the old CGO setup silently emitted arm64 for the amd64 target) and pushes formula `twonb.rb` to `apresai/homebrew-tap`; the workflow also builds and uploads the Obsidian plugin assets and maintains the `2nb` formula alias. **CI does NOT build the macOS app or the cask.**
4. `make release-app`: local, after the CI release exists. Detailed below.
5. `make release-local`: local CLI-only release (no app, no notarization).

### `make release-app` in detail

Runs `scripts/release-app-local.sh --publish`. Local only, after the CI release exists.

**Checkpointed and resumable.** State lives in `build/release-state-<VERSION>.json`. Every phase proves completion from the artifacts themselves (`stapler validate`), and pending notarization submission ids are persisted keyed to the artifact's sha256, so a re-run after any death (laptop sleep, killed shell, a notarytool crash) resumes in seconds: no rebuild, no re-upload. A changed artifact hash invalidates its pending submission, so a rebuilt bundle can never adopt a stale ticket.

- `RELEASE_NOWAIT=1` submits, records the submission id, and exits instead of blocking on Apple; re-run to continue.
- `make release-app-status` reports every phase plus Apple's live status for pending submissions, changing nothing.
- Submits pass `--no-s3-acceleration` (Apple's documented mitigation for flaky submits).
- A submit whose stdout is lost while the upload actually landed (a live-observed notarytool failure mode, distinct from the SIGBUS below) is recovered by adopting the newest matching submission from `notarytool history`.

**What the script does, in order:**

1. **Build.** Builds `SecondBrain.app`, which bundles the freshly-built, version-matched `2nb` CLI at `Contents/Resources/2nb` via the `build-app-release` → `build-cli` dependency. The script fails fast if the bundled `2nb --version` does not equal `VERSION`.
2. **Sign inside-out.** Developer ID-signs the **nested `2nb` binary first**, then the app, both with hardened runtime. The outer sign is not `--deep`, so the nested binary must be signed inside-out or notarization rejects it.
3. **Portable load-commands gate.** Fails the release if the app executable or the bundled `2nb` carries a dangling `LC_RPATH` / `LC_LOAD_DYLIB` that would resolve on the build Mac but not on a clean one. `swift build` bakes in an absolute Xcode-toolchain `LC_RPATH` (the documented SPM Gatekeeper footgun), which `build-app-release` strips before signing. The gate also verifies the bundled `2nb` carries the hardened-runtime flag.
4. **Notarize and staple the app.** Uses `notarytool`. The `notarize()` helper submits WITHOUT `--wait`, then polls `notarytool info` on the submission id, so it self-heals through the intermittent notarytool SIGBUS (Bus error 10 inside Apple's tool during the `--wait` status poll on the Xcode 26.x toolchain, which used to abort the release and force a manual `make release-app` re-run; a crashed poll just retries, with no re-upload).
5. **Sweep old local DMGs.** Removes prior local `SecondBrain-*.dmg` files from the gitignored `build/` dir (and the legacy repo-root location, plus the retired pre-0.9.x `.zip` format), EXCLUDING the current version's DMG, so a resume can never delete the artifact whose notarization it is continuing. Each release leaves a gitignored DMG under `build/` that is already uploaded to its GitHub release, so local copies otherwise accumulate; `make clean-dmg` sweeps ALL local DMGs on demand, current version included, and `make clean` includes it. The uploaded GitHub asset name stays the `SecondBrain-<VERSION>-arm64.dmg` basename, so the cask URL and the `release-all` verify are unaffected by the local path.
6. **Build the DMG.** A branded drag-to-Applications `SecondBrain-<VERSION>-arm64.dmg` via `scripts/make-dmg.sh` (uses `create-dmg`).
7. **Sign, notarize, and staple the DMG too.** Both the app and the DMG are stapled, per Apple distribution best practice: the app launches offline even after being dragged out of the DMG, and the downloaded `.dmg` passes Gatekeeper offline.
8. **Publish.** Uploads the DMG to release `v<VERSION>` and updates the cask `secondbrain.rb` (version and sha256) in the tap.

Signing config is read from `scripts/sign.env` (gitignored; template at `scripts/sign.env.example`); the private key stays in the keychain / cert store. Requires `create-dmg` (`brew install create-dmg`).

### Key files

`.goreleaser.yaml`, `.github/workflows/release.yml`, `scripts/release-app-local.sh`, `build/release-state-<VERSION>.json` (gitignored per-version checkpoint state: pending notarization submission ids keyed to artifact sha256), `scripts/make-dmg.sh` (branded DMG builder, shared with `make package-app`), `app/Resources/dmg-background.{svg,png}` (the installer window art), `scripts/sign.env.example`, `casks/secondbrain.rb.tmpl` (with `CASK_VERSION` / `CASK_SHA256` tokens), `scripts/update-changelog.sh`, `CHANGELOG.md`.

### CI secrets

The `apresai` GitHub environment provides `HOMEBREW_TAP_TOKEN` (a PAT for `apresai/homebrew-tap`). No code-signing secrets live in CI; signing is local only.

## Distribution model

The macOS app is distributed as an arm64 **Developer ID-signed, Apple-notarized** `.dmg` (a branded drag-to-Applications installer; the enclosed `.app` is itself signed, notarized, and stapled), so it launches with no Gatekeeper prompt, both as a direct download and when Homebrew's cask install quarantines it.

**The app bundles its own version-matched `2nb` CLI** at `Contents/Resources/2nb` (signed and notarized with the app), and `CLIPath.resolve()` prefers it, so the app's AI, indexing, and lint calls always run a CLI that matches the app. This eliminates the "0.5.8 re-embed" drift, where a cask upgrade bumped the app but left an older Homebrew `2nb`.

**Bundled-CLI Gatekeeper caveat.** A standalone Mach-O **cannot carry its own stapled notarization ticket** (an Apple limitation). So when an install quarantines the bundle (a browser download, or `brew install --cask`, which copies via `ditto` and propagates `com.apple.quarantine` to every nested file), the quarantined `2nb` would need an *online* notarization check when the app spawns it, and a failing or offline check makes Gatekeeper deny it with "Apple could not verify '2nb' is free of malware ... Move to Trash", which breaks the whole app. To prevent this, the app strips `com.apple.quarantine` from its bundled `2nb` at launch via `CLIPath.prepareBundledCLI()` (in `AppDelegate.applicationDidFinishLaunching`, before the first CLI spawn; safe for the signature, which excludes `com.apple.*` xattrs). Immediate manual unblock for an already-installed copy:

```bash
xattr -dr com.apple.quarantine /Applications/SecondBrain.app
```

The cask still `depends_on formula: "apresai/tap/twonb"` so the **terminal and the Obsidian plugin** have a `2nb` on PATH. A cask upgrade does not bump that formula, so the app's Home banner still nudges to `brew upgrade` it, but the app itself no longer depends on it.
