# Backlog

Non-blocking follow-ups (MEDIUM/LOW) filed from `/chad-review`. CRITICAL/HIGH are fixed before merge; these are tracked here.

## macOS app — Consolidated Home follow-ups (from PR review, 2026-06-07)

Resolved in 0.5.6 / the Home-polish batch (PR #11 + `polish/home-followups`): CLI errors now surface the real `2nb` stderr; every CLI failure is recorded to `.2ndbrain/logs/`; the Re-embed confirmation warns it's a paid full regen; the per-render `ObsidianRegistry.load()` is cached in `.task`; `friendlyModel`/`statusLine` are extracted to `HomeAI` and unit-tested; index buttons clear a stale `actionMessage`.

Still open:

- **LOW — Save can leave a model/dimension mismatch without prompting a re-embed.** "Save as default" overwrites `embedding_model`/`dimensions`; if the prior embeddings were a different dim, they go stale until the user manually re-embeds. Consider offering a re-embed after a model change on Home.
- **LOW — No test asserts `DashboardTab.advanced ∪ .home == allCases`.** A tab silently dropped from the sidebar would go uncaught; add a small assertion.
- **LOW — `IndexProgressView` sheet title is always "Rebuild Index", even for a Re-embed.** The body copy and confirm button correctly say "Re-embed All", but the header doesn't. Making it conditional on `pendingForceReembed` would flip it mid-run (the flag clears when the run starts); to do it right, thread the run mode into `IndexProgress` so the title stays accurate through all phases.
- **LOW — OpenRouter key is passed as an argv to `/usr/bin/security` (`add-generic-password … -w <key>`).** Briefly visible to `ps` while storing. Pre-existing (not in the error-log path, so never written to a file); pipe it via stdin like the CLI's `config set-key` does for the Bedrock token.
