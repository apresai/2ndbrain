# The llama.cpp local engine: build and status

**Status (0.13.1): built but not user-ready. Hidden in the macOS GUI.** The
`llama-local` provider (bundled Gemma, fully offline) is fully coded, but the one
piece that makes it run, the `llama-server` engine binary, is never provisioned,
so nothing can actually execute locally. Every GUI surface is hidden behind a
single flag until that is fixed. This doc explains the architecture, exactly what
is and is not there, and the path to making it real.

## TL;DR

- **What works:** downloading and verifying the Gemma model *weights* (the GGUFs).
- **What is missing:** the `llama-server` engine binary itself. It is neither
  bundled in the app nor downloaded. `2nb ai engine status` reports an empty
  `engine_path`, and any embed/generate fails with "engine not running".
- **Also missing:** auto-start. Even with the binary present, the embed path does
  not start the engine; that needs `2nb ai engine install` (launchd) or `serve`.
- **GUI:** every llama-local surface is gated off by
  `AIHubView.localEngineFeatureEnabled = false` so users are never offered a
  provider that cannot run.
- **The fix:** bundle the official ggml-org `llama-server` payload (about 10.6 MB
  compressed) in the app, signed and notarized with it. See "Path forward".

## Architecture: out-of-process, not linked in

2ndbrain runs llama.cpp as a **subprocess**, not as a linked library. A
`llama-server` process is supervised in the background (one per role), and the Go
CLI talks to it over HTTP using llama.cpp's OpenAI-compatible endpoints
(`/v1/embeddings`, `/v1/chat/completions`, `/v1/rerank`). The Go binary never
links llama.cpp, which keeps the shipped `2nb` pure-Go and CGO-free.

This differs from re:Gist (the sibling iOS app), which links llama.cpp
**in-process** via the official prebuilt XCFramework consumed through a SwiftPM
`.binaryTarget` (`import llama`). That model fits an iOS app with one on-device
model; the subprocess model fits a multi-role desktop engine (embed + generation
+ rerank running concurrently) that both the CLI and the app share.

Three **roles** each get their own `llama-server` process, because llama.cpp's
`--embeddings` and `--reranking` flags are mutually exclusive:

| Role | Server flags | Provider |
|------|--------------|----------|
| `RoleGen` | (chat defaults) | `LlamaGenerator` → `/v1/chat/completions` |
| `RoleEmbed` | `--embeddings --pooling mean` | `LlamaEmbedder` → `/v1/embeddings` |
| `RoleRerank` | `--reranking --pooling rank` | `LlamaReranker` → `/v1/rerank` |

On Apple silicon, each server gets `-ngl 999` (all layers on the Metal GPU).

## What is built (all of it, except the binary)

Everything below is implemented and tested; it is just dormant because no engine
binary is present to serve the endpoints.

| Component | File | Role |
|-----------|------|------|
| Engine locator + cache dirs + roles | `cli/internal/llama/locate.go` | `LocateEngine`, `ModelsCacheDir`, `EngineCacheDir`, `StateDir`, Metal-host check |
| Engine supervisor + sidecar registry | `cli/internal/llama/manager.go` | starts/keeps one `llama-server` per role, writes `StateDir/<role>.json`, `EndpointFor`, health |
| Model-weight manifest + download | `cli/internal/llama/models.go` | pinned GGUF URLs + sha256, `EnsureModel`, progress, stall watchdog, `RemoveModel` |
| launchd background agent | `cli/internal/llama/launchd.go` | installs the always-on `2nb ai engine serve` agent |
| CLI command group | `cli/internal/cli/ai_engine.go` | `2nb ai engine status\|serve\|install\|uninstall\|start\|stop\|pull\|rm` |
| The three providers | `cli/internal/ai/llamacpp.go` | `LlamaEmbedder`, `LlamaGenerator`, `LlamaReranker` (HTTP clients) |
| Catalog entries | `cli/internal/ai/catalog.go` | the 4 local models surfaced in `models list` |
| GUI (hidden) | `app/Sources/SecondBrain/AIHubView.swift` | download / use / delete section + provider card, gated by the flag |

### Model weights: the part that works

`ModelManifest` (`models.go`) pins four GGUFs to **ungated** Hugging Face repos,
each carrying the file's HF LFS oid as its sha256:

| id | repo | quant | size |
|----|------|-------|------|
| `embeddinggemma-300m` | `ggml-org/embeddinggemma-300M-GGUF` | Q8_0 | ~334 MB |
| `gemma4-e2b` | `unsloth/gemma-4-E2B-it-GGUF` | Q4_K_M | ~3.11 GB |
| `gemma4-e4b` | `unsloth/gemma-4-E4B-it-GGUF` | Q4_K_M | ~4.98 GB |
| `bge-reranker-v2-m3` | `gpustack/bge-reranker-v2-m3-GGUF` | Q8_0 | ~636 MB |

`EnsureModel` streams to a `.part` file, hashes on the fly, verifies the sha256,
and atomically renames. It **fails closed** on a missing/mismatched hash, and a
60s idle watchdog aborts a stalled transfer without capping a slow-but-progressing
one. Weights cache at `~/Library/Caches/2nb/models/<id>/<file>.gguf`. `2nb ai
engine pull` (progress bar; `--json` for the GUI) and `rm` manage them.

## The gap: the engine binary is never provisioned

`LocateEngine` (`locate.go:115-138`) resolves the `llama-server` binary from four
sources, in order:

1. config override `ai.llama.engine_path`
2. a **bundled sibling** of the running executable named `llama-server`
   (e.g. `SecondBrain.app/Contents/Resources/llama-server`)
3. `~/Library/Caches/2nb/engine/llama-server` (a **downloaded** copy)
4. `llama-server` on `PATH`

...and returns `""` when all four miss. **The resolution logic is all that
exists.** Nothing in any Makefile, `scripts/`, `.release.yaml`, or
`.goreleaser.yaml` copies an engine into the app bundle (source 2) or downloads
one into the cache (source 3): a grep for `llama-server`/`llama.cpp` across the
build/release configs returns zero hits. `2nb ai engine pull` fetches only
*model* ids from `ModelManifest`; there is no path that fetches the binary. #142
shipped the manager and locator but left the binary a "packaging phase" TODO.

Net effect: `2nb ai engine status` shows `engine_path: ""`, `NewManager` errors
"llama-server engine not found", and every `LlamaEmbedder`/`Generator`/`Reranker`
call returns "engine not running". A CLI user can still make it work by putting
`llama-server` on `PATH` (`brew install llama.cpp`) then `2nb ai engine install`.

### Secondary gap: no auto-start on demand

`EndpointFor` (`manager.go:232`) reads the role sidecar and probes `/health`; if
the engine is not already serving, it returns not-running. Nothing starts it on
the first embed request. So even once the binary exists, using llama-local
requires the engine to be running, via `2nb ai engine install` (a launchd agent
that runs `2nb ai engine serve` at login) or a foreground `serve`.

## Why the GUI hides it

`app/Sources/SecondBrain/AIHubView.swift`:

```swift
private let localEngineFeatureEnabled = false
```

The flag gates two surfaces: the "Local models" section (`if
localEngineFeatureEnabled { localModelsSection }`) and the llama-local provider
card (filtered out of the Providers row). Everything behind it, Download / Use
these models / Delete, plus the `AppState` plumbing, stays compiled and
referenced (no dead code). Flipping the flag to `true`, once the engine binary
ships, re-enables the whole GUI with no rewrite.

## Path forward

`LocateEngine`'s four-source order is not three competing designs; it is a layered
resolution we can fill in from the top down. The ggml-org release for macOS arm64
(`llama-<tag>-bin-macos-arm64.tar.gz`, ~10.6 MB compressed) contains a 33 KB
`llama-server` launcher plus 10 `@rpath` dylibs whose only rpath is
`@loader_path`, so co-locating the files resolves them with no `install_name`
surgery. The Metal shader is embedded in `libggml-metal` (nothing extra to ship).

| Option | How | Gatekeeper | Effort |
|--------|-----|-----------|--------|
| **A. Bundle (recommended)** | vendor the macos-arm64 payload (binary + 10 dylibs, ~22 MB on disk) into `SecondBrain.app/Contents/Resources/llama/`, pinned by tag + sha256; `build-app-release` signs it inside-out with the app | **Best.** Inherits the app's notarization; no online check at spawn. Same pattern as the bundled `2nb` | Medium |
| **B. Download** | an engine manifest mirroring `models.go` + `EnsureEngine` into `EngineCacheDir`; reuses `ai engine pull --json` | **Worst.** ggml-org binaries are unsigned and not notarized, so a downloaded, quarantined Mach-O hits "Apple could not verify llama-server ... Move to Trash". Requires ad-hoc re-sign + de-quarantine of 11 files | Medium |
| **C. brew** | `brew install llama.cpp`; resolved by `LocateEngine`'s `PATH` branch | Clean in practice (brew does not quarantine), but an external dependency we do not own or pin | Low |

**Recommendation: ship A as the default that makes the feature real; keep B as an
optional updater and the path for CLI/plugin users who never install the app; keep
C as the opportunistic `PATH` fallback.** A is the only option that ships
notarized bits, works offline on first launch, and locks the engine version to the
app it was tested with. B recreates and compounds the bundled-CLI Gatekeeper trap
(see the signing notes in the root `CLAUDE.md`) because the third-party binary is
not ours to notarize.

### End-to-end sequence to make llama-local work

1. **Provision the engine (A):** vendor the arm64 payload, have `build-app-release`
   copy it into `Contents/Resources/llama/` and sign each Mach-O inside-out before
   the outer app signature. Optionally add an `EnsureEngine` + engine-manifest
   entry so `2nb ai engine pull` can also fetch it (B) for non-app users.
2. **Auto-start on use:** have the "Use these models" activation (or first embed)
   run `2nb ai engine install` so the launchd agent serves the roles, instead of
   silently failing against a dead endpoint. Gate the activation on engine
   readiness (`ai engine status`).
3. **Un-hide the GUI:** flip `AIHubView.localEngineFeatureEnabled` to `true`.

Until step 1 lands, llama-local stays hidden in the GUI and CLI-only (with a
user-supplied `llama-server`).

## References

- Code: `cli/internal/llama/`, `cli/internal/ai/llamacpp.go`,
  `cli/internal/cli/ai_engine.go`, `app/Sources/SecondBrain/AIHubView.swift`
- Model download + rerank background: the "AI Providers" and "Reranking" sections
  of the root `CLAUDE.md`; `THIRD_PARTY_NOTICES.md`
- Signing/Gatekeeper caveat (why bundling beats downloading): the macOS signing
  notes in the root `CLAUDE.md`
- re:Gist's in-process approach (for contrast):
  `~/dev/regist/docs/on-device-ai-design.md`
