# Bugbot review instructions for 2ndbrain

Layout: `cli/` is a Go CLI (`2nb`) + MCP server; `app/` is a Swift 6 macOS
dashboard (SwiftUI + AppKit, macOS 14 deploy target); `plugins/obsidian-2ndbrain/`
is a TypeScript Obsidian plugin. All three shell into or mirror the Go CLI, so
cross-layer contract drift is a top review target.

## Invariants to enforce (and that any autofix must preserve)

- **Timeouts bound hangs, never working-but-slow models.** Every outer
  deadline is DERIVED from its inner layer's worst case (attempts x
  per-attempt + backoff) plus slack; the constants live in
  `cli/internal/ai/timeouts.go` and nesting is pinned by tests
  (`TestTimeoutBudgetsNested`, `TestToolBudgetsNested`,
  `TestDoctorBudgetsNested`, `CLIWatchdogTests`, the plugin's
  `commandTimeoutMs` vitest). Flag any new flat timeout that sits inside a
  transport budget, and any Swift/TS mirror constant that drifts from its Go
  derivation. Never fix a failing budget pin by weakening the budget; fix the
  derivation.
- **Token/cost budgets bound runaway spend, never simple probes.** Generation
  budgets are shared constants (`probeGenMaxTokens`, `BenchGenMaxTokens`,
  `RAGGenMaxTokens`, `eval` budgets) quoted by the cost estimators; pinned by
  `TestGenerationBudgetsPinned`. Flag estimator/spender drift.
- **Every paid model call in the GUI is cost-gated**: estimate, then
  PaidOperationConfirm, then an explicit `--cost-cap` of 2x estimate + $0.01.
  Flag any path that can spend without a confirm, or that passes a cap the
  run can exceed mid-flight after partial billing (a too-low explicit cap
  aborts mid-run; refusing up front is correct). Zero-API probes
  (`retrieval`, `search`) deliberately skip the confirm.
- **No mocks in tests.** Tests call real endpoints and skip cleanly without
  credentials (Bedrock/OpenRouter/Ollama), or are pure logic. Flag
  `httptest.NewServer`, stub providers, or fake responses.
- **Single-flight claims for heavy runs.** Bench/eval/verify entry points
  claim the shared `@Observable` run models (`BenchRunModel`,
  `VerifyRunModel`) BEFORE their first `await` and release via `defer`. Flag
  a new heavy entry point that skips the claim or claims after an await.
- **JSON contracts are additive.** Swift decoders mirror Go structs
  field-for-field; older-CLI compatibility gates classify by PAYLOAD SHAPE,
  never exit status (cobra parents with a `RunE` swallow unknown subcommands
  and exit 0 with the wrong payload).
- **macOS 14 target**: `.tabItem`, never `Tab(_:systemImage:)` (macOS 15+).
  Parse untrusted YAML via `Yams.compose` + a bounded walk; `Yams.load` can
  `fatalError` on Obsidian frontmatter.
- **Vault writes are sacred.** `2nb` never rewrites note bodies outside the
  explicit, user-invoked write commands; flag any new implicit write path to
  vault markdown, and any write that skips the shared reindex path.
- **Secrets**: the Bedrock bearer token may appear only suffix-redacted
  (`token_suffix`); flag any log/JSON/test fixture that carries a full token
  or an Authorization header.

## Style

- No em-dashes or en-dashes as prose punctuation in comments or docs;
  rewrite with commas, colons, periods, or parentheses.
- User-facing changes update README.md and CLAUDE.md in the same PR (the
  `readme-currency` check annotates when README is untouched).
