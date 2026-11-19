# The llama.cpp local engine: build and status

**Status: Active in `2nb` CLI and macOS GUI (`Settings > Gemini > Local Gemma (On-Device)`).** The `llama-local` provider runs Gemma GGUF models completely offline via a bundled arm64 Metal `llama.cpp` runtime packaged directly inside `SecondBrain.app/Contents/Resources/llama/` (pinned `b12428`, inside-out codesigned, zero Homebrew dependency). Users download SHA256-verified GGUF weights from Settings or the CLI, and local role servers start invisibly on demand and auto-stop after 10 minutes idle.

## TL;DR

- **What works:** Bundled `b12428` arm64 Metal `llama-server` + `llama-cli` + `@loader_path` dylibs in `SecondBrain.app/Contents/Resources/llama/` (`scripts/vendor-llama.sh`), downloading and verifying Gemma GGUF weights (`embeddinggemma-2` `Q8_0` 310 MB Apache-2.0 — legacy `embeddinggemma-300m` still supported, `gemma4-e2b` `Q4_K_M`, `bge-reranker-v2-m3` `Q8_0`), automatic engine discovery (`LocateEngine`), invisible on-demand per-role supervision (`EnsureEndpointFor` with per-role `flock` + `Setsid` detachment + `10m` idle shutdown and model hot-swap), Matryoshka truncation (`128`/`256`/`512`/`768` with client-side L2 re-normalization), and asymmetric task instruction presets (`search`/`code`/`qa`/`none`).
- **Auto-start on demand:** Calling `LlamaEmbedder`, `LlamaGenerator`, or `LlamaReranker` automatically triggers `EnsureEndpointFor`, which spawns a detached `2nb ai engine serve --idle-timeout 10m` process against the bundled `llama-server` and waits for `/health`. Clicking **"Use Local Models & Re-embed"** in `Settings > Gemini` saves the local configuration and triggers `index --force-reembed`, which wakes the embedder seamlessly without any launchd plist or manual daemon toggles.
- **GUI:** Surfaced under `Settings > Gemini` via the `Google Gemini (Cloud)` / `Local Gemma (On-Device)` segmented selector in `SettingsAIView.swift`, intentionally omitting raw server/PID/port/`launchd` controls so local inference behaves like a built-in feature.
- **Distribution packaging:** Both `make install-app` and `scripts/release-app-local.sh` vendor and inside-out sign `Contents/Resources/llama/*` alongside `2nb` inside the `.app` bundle (Option A below).

## Architecture: out-of-process, not linked in

2ndbrain runs llama.cpp as a **subprocess**, not as a linked library. A
`llama-server` process is supervised in the background (one per role), and the Go
CLI talks to it over HTTP using llama.cpp's OpenAI-compatible endpoints
(`/v1/embeddings`, `/v1/chat/completions`, `/v1/rerank`). The Go binary never
links llama.cpp, which keeps the shipped `2nb` pure-Go and CGO-free.

Whereas a single-model mobile app typically links `llama.cpp` **in-process**
via a prebuilt XCFramework (`.binaryTarget`), the subprocess model fits a
multi-role desktop engine (embed + generation + rerank running concurrently)
that both the CLI and the macOS app share.

Three **roles** each get their own `llama-server` process, because llama.cpp's
`--embeddings` and `--reranking` flags are mutually exclusive:

| Role | Server flags | Provider |
|------|--------------|----------|
| `RoleGen` | (chat defaults) | `LlamaGenerator` → `/v1/chat/completions` |
| `RoleEmbed` | `--embeddings --pooling mean --ctx-size 2048 -b 2048 -ub 2048` | `LlamaEmbedder` → `/v1/embeddings` |
| `RoleRerank` | `--reranking --pooling rank` | `LlamaReranker` → `/v1/rerank` |

On Apple silicon, each server gets `-ngl 999` (all layers on the Metal GPU).

## What is built

Everything below is implemented, bundled, and verified against the live Metal runtime:

| Component | File | Role |
|-----------|------|------|
| Build-time runtime vendor | `scripts/vendor-llama.sh` | Downloads, SHA256-verifies, and stages pinned `b12428` arm64 Metal `llama-server`, `llama-cli`, `LICENSE`, and `*.dylib`s |
| Engine locator + cache dirs + roles | `cli/internal/llama/locate.go` | 8-step `LocateEngine`, `LocateCLI`, `ModelsCacheDir`, `EngineCacheDir`, `StateDir`, Metal-host check |
| On-demand supervisor + sidecar registry | `cli/internal/llama/manager.go` | `EnsureEndpointFor` (`flock` + `Setsid` `2nb ai engine serve --idle-timeout 10m`), `TouchRoleActivity`, `watchIdle`, `RoleCanStart`, `StateDir/<role>.json` |
| Model-weight manifest + download | `cli/internal/llama/models.go` | Pinned GGUF URLs + sha256, `EnsureModel`, progress, stall watchdog, `RemoveModel` |
| Optional launchd background agent | `cli/internal/llama/launchd.go` | CLI helper (`2nb ai engine install\|uninstall\|start\|stop`) for explicit background agent use |
| CLI command group | `cli/internal/cli/ai_engine.go` | `2nb ai engine status\|serve\|install\|uninstall\|start\|stop\|pull\|rm` (`--idle-timeout`) |
| The three providers | `cli/internal/ai/llamacpp.go` | `LlamaEmbedder`, `LlamaGenerator`, `LlamaReranker` (`EnsureEndpointFor` + `TouchRoleActivity`) |
| Catalog entries | `cli/internal/ai/catalog.go` | Local model metadata (`embeddinggemma-2` threshold `0.60`, legacy `embeddinggemma-300m` threshold `0.35`, `gemma4-e2b`, etc.) |
| GUI settings panel | `app/Sources/SecondBrain/SettingsAIView.swift` | Built-in `Local Gemma (On-Device)` UI (model downloads, Matryoshka/preset tuning, legacy upgrade button, one-click activate & re-embed) |

### Model weights: the part that works

`ModelManifest` (`models.go`) pins the local GGUFs to **ungated** Hugging Face repos,
each carrying the file's HF LFS oid as its sha256:

| id | repo | quant | size |
|----|------|-------|------|
| `embeddinggemma-2` *(default)* | `ggml-org/embeddinggemma-2-GGUF` (Apache-2.0) | Q8_0 | ~310 MB (`309,855,456` B) |
| `embeddinggemma-300m` *(legacy)* | `ggml-org/embeddinggemma-300M-GGUF` (Gemma Terms) | Q8_0 | ~334 MB |
| `gemma4-e2b` | `unsloth/gemma-4-E2B-it-GGUF` | Q4_K_M | ~3.11 GB |
| `gemma4-e4b` | `unsloth/gemma-4-E4B-it-GGUF` | Q4_K_M | ~4.98 GB |
| `bge-reranker-v2-m3` | `gpustack/bge-reranker-v2-m3-GGUF` (Apache-2.0) | Q8_0 | ~636 MB |

`EnsureModel` streams to a `.part` file, hashes on the fly, verifies the sha256,
and atomically renames. It **fails closed** on a missing/mismatched hash, and a
60s idle watchdog aborts a stalled transfer without capping a slow-but-progressing
one. Weights cache at `~/Library/Caches/2nb/models/<id>/<file>.gguf`. `2nb ai
engine pull` (progress bar; `--json` for the GUI) and `rm` manage them.

## Engine binary resolution

`LocateEngine` (`cli/internal/llama/locate.go`) resolves the `llama-server` binary across eight sources, in order:

1. Config override `ai.llama.engine_path`
2. `TWONB_ENGINE_PATH` environment variable
3. Executable-relative bundled sibling (`<exeDir>/llama-server` or `<exeDir>/llama/llama-server`, e.g., `SecondBrain.app/Contents/Resources/llama/llama-server` next to bundled `2nb`, or `<plugin>/bin/llama-server`)
4. `$APP/Contents/Resources/llama/llama-server` (when `APP` is set)
5. `~/Applications/SecondBrain.app/Contents/Resources/llama/llama-server`
6. `/Applications/SecondBrain.app/Contents/Resources/llama/llama-server`
7. `~/Library/Caches/2nb/engine/llama-server` (`EngineCacheDir()`)
8. `llama-server` on `PATH` (optional CLI fallback, e.g. `brew install llama.cpp`)

Returns `""` when all eight miss. Packaged builds bundle the runtime in `Contents/Resources/llama/`, so standalone CLI and GUI workflows succeed with zero Homebrew installation.

## GUI Controls (`SettingsAIView.swift`)

- **Per-Slot Hybrid Combo Selection**: **Generation (Ask, Chat & Polish)** and **Embeddings (Semantic Search & Indexing)** each expose an independent `Google Gemini (Cloud)` / `Local Gemma (On-Device)` switcher (`ai.generation_provider` + `ai.embedding_provider`), letting users pair on-device `embeddinggemma-2` embeddings with cloud `gemini-4-hydrogen` generation (or run fully offline / fully cloud).
- **Built-in Integrated Experience**: Deliberately omits server paths, PIDs, ports, `brew` commands, and `launchd` start/stop controls so local AI operates invisibly as a native app feature.
- **Model Stack Downloader & Cache Status**: Displays `embeddinggemma-2` (`310 MB`), `gemma4-e2b` (`3.11 GB`), and `bge-reranker-v2-m3` (`636 MB`) with per-model `Cached` badges or mini `Download` buttons, streaming NDJSON progress, and delete recovery; when a vault is still configured with legacy `embeddinggemma-300m`, an inline callout offers a one-click **Switch to EmbeddingGemma 2** upgrade.
- **EmbeddingGemma Tuning Controls**: Segmented Matryoshka dimension picker (`256d Fast / Compact`, `512d Balanced`, `768d Full Fidelity`) and Task Prefix Preset selector (`search`, `code`, `qa`, `none`) backed by `ai.dimensions` and `ai.task_preset` (`ai.llama.task_prefix` alias). See [embedding-gemma-tuning.md](embedding-gemma-tuning.md) for rationale and math.
- **Re-embedding**: **"Re-embed Vault Now"** runs `2nb index --force-reembed`, which wakes the bundled embedder on demand via `EnsureEndpointFor` (with automatic `10m` idle shutdown).

## Bundled Distribution & Packaging

Of the three engine delivery approaches, **Option A (Bundled Inside-Out Signed Runtime)** is shipped in `Makefile` and `scripts/release-app-local.sh`, with `PATH` / cache kept strictly as CLI-only fallbacks:

| Option | Status | Gatekeeper | Notes |
|--------|--------|-----------|-------|
| **A. Bundle (Shipped)** | `scripts/vendor-llama.sh` downloads, caches, and SHA256-verifies `ggml-org/llama.cpp` `b12428` (`llama-b12428-bin-macos-arm64.tar.gz`) into `build/llama/`; `Makefile` and `scripts/release-app-local.sh` copy it to `SecondBrain.app/Contents/Resources/llama/` and sign inside-out (`*.dylib` -> `llama-server` -> `llama-cli` -> `2nb` -> `.app`) | **Best.** Inherits app signature/notarization; `@loader_path` rpath links stay self-contained (`install_name` portable check verified) | Default for all app & bundled CLI users |
| **B. Download** | `EngineCacheDir()` slot (`LocateEngine` step 7) | Poor for GUI users due to quarantine/signing friction on unnotarized third-party Mach-Os | Kept as cache fallback slot only |
| **C. brew / PATH** | `LocateEngine` step 8 fallback | Clean via Homebrew, but external dependency | CLI-only opportunistic fallback when no `.app` bundle is installed |

## References

- Code: `cli/internal/llama/`, `cli/internal/ai/llamacpp.go`,
  `cli/internal/cli/ai_engine.go`, `app/Sources/SecondBrain/SettingsAIView.swift`
- EmbeddingGemma tuning rationale & benchmarks: [embedding-gemma-tuning.md](embedding-gemma-tuning.md)
- Model download + rerank background: `docs/ai-providers.md` and the Reranking section
  of `docs/search-tuning.md`; `THIRD_PARTY_NOTICES.md`
- Signing/Gatekeeper caveat (why bundling beats downloading): the macOS signing
  notes in the root `CLAUDE.md`
