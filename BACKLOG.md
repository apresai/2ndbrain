# Backlog

Non-blocking follow-ups (MEDIUM/LOW) filed from `/chad-review`. CRITICAL/HIGH are fixed before merge; these are tracked here.

## macOS app — Consolidated Home follow-ups (from PR review, 2026-06-07)

- **MEDIUM — CLI errors surfaced to the user are non-actionable.** `CLIError.nonZeroExit(code)` renders as "CLI exited with code 1"; the `2nb` stderr (the real reason, e.g. Bedrock AccessDenied) is not carried into the thrown error, so Home's Save/Test failure text and `IndexProgressView`'s failure state are useless without opening Console.app. Fix: add the captured stderr to the error case (`case nonZeroExit(Int32, String)`) and show it. Pre-existing root cause in `AppState.runCLI`; surfaced on the new Home screen.
- **MEDIUM — Save/Test failures don't reach the `.2ndbrain/logs/` file.** `runCLI` logs argv+exit+stderr to os_log, but `saveAIConfig`/`testAndSave` never call `errorLogger?.log` (unlike `rebuildIndex`'s catch), so the Validation tab / "read the logs" workflow misses them. Make logging symmetric across the CLI runners.
- **MEDIUM — Re-embed confirmation doesn't warn it's a paid full regen.** The shared `IndexProgressView` ready/confirm copy is identical for Rebuild Index vs Re-embed All; re-embed regenerates every embedding (paid Bedrock calls). Have `readyView` read `pendingForceReembed` and adjust the copy/warning. Pre-existing (also reachable from VaultStatusView), now on the default screen.
- **MEDIUM — `ObsidianRegistry.load()` is a synchronous disk read inside HomeView's body.** `load()` does `Data(contentsOf:)` and is called from the vault card on every body re-render (each Save/Test/status toggle). Cache the registry into `@State` via `.task` and re-read on demand instead of per-render.
- **MEDIUM — `friendlyModel`/`statusLine` are untested because they're `private`.** Extract the two pure mappers (model-id → friendly name; `AIStatusInfo` → status string) to an `internal` location and add a `HomeViewLogicTests` covering the default-model, empty-id, ready, reason, and credentials branches.
- **LOW — Save can leave a model/dimension mismatch without prompting a re-embed.** "Save as default" overwrites `embedding_model`/`dimensions`; if the prior embeddings were a different dim, they go stale until the user manually re-embeds. Consider offering a re-embed after a model change on Home.
- **LOW — Index buttons don't clear a stale `actionMessage`.** A prior red "Save failed" persists while a later Rebuild runs. Clear `actionMessage` when starting an index action.
- **LOW — No test asserts `DashboardTab.advanced ∪ .home == allCases`.** A tab silently dropped from the sidebar would go uncaught; add a small assertion.
