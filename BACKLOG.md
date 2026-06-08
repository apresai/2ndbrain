# Backlog

Non-blocking follow-ups (MEDIUM/LOW) filed from `/chad-review`. CRITICAL/HIGH are fixed before merge; these are tracked here.

## Embeddings status — empty-note handling (from PR review, 2026-06-07)

The fix (`derivePortability` no longer reports a vault perpetually "stale" when the only unembedded docs are empty notes; `EmbeddingCounts` now returns `embeddableUnembedded`) shipped with the LOW from review (all-empty + no-provider should nudge `no_provider`, not "ok") already fixed in-commit. Remaining LOW:

- **RESOLVED (2026-06-08) — empty notes are now hidden from the embedding status.** Rather than surface `vault_empty_docs`, the embedding ratio now uses an *embeddable* denominator (`vault_embeddable_docs` = content-bearing docs), so the dashboard reads "115 / 115" with a clean OK and no skipped-note caveat (empties stay tracked for Obsidian link parity). CLI (`ai status`/`vault status`/`nextStepHint`) and Swift (`AIStatusInfo.embeddableDenominator`, `VaultStatusView`, `HomeView`) all updated. This supersedes the earlier "surface vault_empty_docs in Swift" idea.
- **LOW — `2nb ai local` still counts empty notes in "N need indexing."** `ai_local.go` uses `len(DocumentsNeedingEmbedding())` (which includes empty + stale-hash docs) for `NeedIndexing`, so a vault with blank notes shows "115/117 (2 need indexing)" and a "Run 2nb index to embed 2 remaining" action. Niche command (Ollama local-readiness) with subtler semantics (stale-hash vs empty), so deferred from the hide-empties pass; fix would filter empties from that count to match the dashboard.
- **LOW — `EmbeddingCounts` query error is discarded at all three call sites** (`ai_cmd.go`, `vault_cmd.go`, `root.go` use `..., _`). Pre-existing (the error was already discarded before this change), so not a regression; but a wedged DB would silently report 0 docs / misclassify portability as `empty_vault`. A `slog.Warn("embedding counts query failed", err)` would aid diagnosis. Read-only status path, so it degrades gracefully — low priority.

## Release pipeline — local notarized-app flow (from PR review, 2026-06-07)

The HIGH (forgetting the local `make release-app` step shipped a mismatched CLI/app silently) is mitigated: `make release` now prints a loud two-step reminder and the require-release guard is hoisted before notarization. Remaining LOW hardening for `scripts/release-app-local.sh`:

- **LOW — cask push has no pull/rebase/retry.** `--publish` does a `--depth 1` clone of `apresai/homebrew-tap` → commit → `git push` with no retry. A concurrent push to the tap's `main` (e.g. CI's `2nb` alias commit) rejects it non-fast-forward. The documented ordering (run `release-app` after the CI run completes) avoids it, and the failure is loud + re-runnable; a `git pull --rebase` retry would harden it.
- **LOW — same-version re-run sha window.** On a same-version re-run, `gh release upload --clobber` replaces the zip bytes (a fresh notarization yields a new sha) before the cask push; if the push then fails, the cask holds the old sha and `brew install --cask` errors on checksum until a clean re-run. Loud + recoverable; the script self-documents it. Different-version publishes have no such window (upload precedes cask).

## macOS app — Consolidated Home follow-ups (from PR review, 2026-06-07)

Resolved in 0.5.6 / the Home-polish batch (PR #11 + `polish/home-followups`): CLI errors now surface the real `2nb` stderr; every CLI failure is recorded to `.2ndbrain/logs/`; the Re-embed confirmation warns it's a paid full regen; the per-render `ObsidianRegistry.load()` is cached in `.task`; `friendlyModel`/`statusLine` are extracted to `HomeAI` and unit-tested; index buttons clear a stale `actionMessage`.

Resolved in 0.5.8 (`polish/backlog-final`): a model/dimension mismatch after Save now nudges toward Re-embed All (`HomeAI.reembedHintAfterSave`, unit-tested); a parity test guards `DashboardTab.advanced ∪ .home == allCases` and that every tab has an icon; the run mode is threaded onto `IndexProgress.forceReembed` so the sheet title stays "Re-embed All" through every phase instead of flipping back to "Rebuild Index" once the run starts.

Still open:

- **LOW — `portabilityStatus` strings are duplicated across the Go↔Swift boundary.** `HomeAI.reembedHintAfterSave` matches the literals `dimension_break`/`mixed`/`model_mismatch` emitted by the Go CLI's `derivePortability` (`cli/internal/cli/ai_cmd.go`). If the CLI renames one, Swift silently falls to the `default` case and the re-embed nudge disappears with no compile error. Consider a shared contract test (decode a real `2nb ai status --json` and assert the known status set) or a generated enum.
- **LOW — `IndexProgressView.isReembed` fallback logic is not unit-tested.** `indexProgress?.forceReembed ?? pendingForceReembed` is real branching but lives in a `private var` inside a SwiftUI view body, which this project doesn't unit-test. To make it testable, extract the resolution to a free function the way `HomeAI.reembedHintAfterSave` was.
- **LOW (deferred) — OpenRouter key is passed as an argv to `/usr/bin/security` (`add-generic-password … -w <key>`).** Briefly visible to `ps` while storing. Pre-existing and not in the error-log path, so the key is never written to a file. The clean fix is not app-local: the CLI's own keystore helper (`cli/internal/ai/keystore.go` `keychainSet`) uses the same `security -w` argv form for *every* key it stores (Bedrock token included), so a real fix means replacing the `security(1)` shell-out with the macOS Security framework via cgo across the whole keystore — disproportionate to a LOW that only narrows an already-brief local `ps` window. Deferred until we touch the keystore for another reason. (The CLI's interactive `config set-key` already reads from stdin, so the Bedrock-token path a user types is not exposed; this is specifically the app's programmatic OpenRouter-key write.)
