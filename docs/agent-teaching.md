# Teaching AI Coding Agents to Use 2ndbrain

This doc synthesizes how 2nb exposes itself to AI coding tools (Claude Code, Cursor, Copilot, Junie, Kiro, Cline, Roo Code, Windsurf) and proposes concrete improvements to the teaching surface plus a golden-path test battery.

It is deliberately not "how to set up MCP" — see [`mcp-integration.md`](./mcp-integration.md) for that. This is about the *content* that agents receive and the *tests* that keep that content honest as the CLI and MCP evolve.

## Surfaces

2nb has three agent-facing surfaces. All three share the same SQLite index at `<vault>/.2ndbrain/index.db` and enforce the same schemas.

| Surface | What it is | What it's for |
|---|---|---|
| **`2nb` CLI** | Full Cobra command tree with `--json`, `--yaml`, `--porcelain`, `--vault` global flags. Parent-command defaults (`2nb ai` → `ai status`, etc.) | Any agent with shell access. One-shot queries, piping, scripting, or CI. Works even when MCP isn't wired up. |
| **MCP server** | 22 tools served over stdio (`2nb mcp-server`). Sidecar status files at `.2ndbrain/mcp/<pid>.json` record live processes + invocation history. | MCP-capable clients. Persistent session caching (embeddings, threshold), structured JSON I/O, schema validation. |
| **Skills system** | `SKILL.md` embedded in the binary (`cli/internal/skills/content/2ndbrain-skill.md`), installed per-agent by `2nb skills install`. 10 agents supported (`cli/internal/skills/skills.go`), including the cross-tool `agents` (`.agents/skills/`) standard. Also mirrored into the repo at `.agents/skills/2nb/SKILL.md` (+ `.warp/`, `.claude/`) so an agent opened on this repo discovers it with zero install; `make sync-skills` keeps the mirrors in step with the embedded source and release CI fails on drift. | The *teaching layer* — explains the CLI and MCP to an agent in one document. This is the file to invest in. |

## MCP vs CLI — when to use which

**Decision matrix** — per task, with the actual drivers (based on `cli/internal/mcp/tools.go` and command behavior):

| Task | Prefer MCP when… | Prefer CLI when… | Notes |
|------|---|---|---|
| Search | Long agent session, repeated searches | One-shot, scripted, piping | MCP caches embeddings per session (`tools.go:34-82`) and threshold at first use (`tools.go:42-47`). From the 2nd search onward, MCP reuses the cached `AllEmbeddings()` result instead of re-reading from SQLite. |
| RAG Q&A (`ask`) | Interactive multi-turn context with cached threshold | Batch / CI | Same caching rationale as search. |
| Frontmatter edit | **MCP-only** (`kb_update_meta`) | — | There is no CLI equivalent that does atomic schema-validated frontmatter edits. `2nb meta --set` handles one key at a time and re-reads the file. |
| Create document | Either — both return JSON + auto-index | Either — CLI prints to stderr with human-readable hints | Semantically identical. |
| Suggest links | Agent wants JSON with scores + snippets | Human wants readable terminal output | Same result set, different format. |
| Polish prose | Agent wants `{original, polished}` pair for diff | Piping into `diff`/`patch`/external tooling | `kb_polish` (MCP) is read-only; the CLI `2nb polish` adds an opt-in `--write` that applies the polished body in place (still returning `{original, polished}` for audit). |
| Git read operations | Either | Either | `kb_git_*` and `2nb git *` return identical data. |
| **Vault lifecycle** (create/set/list/status) | — | **CLI-only** — `--vault` flag on every command | MCP is scoped to an already-open vault. Creating or switching vaults happens outside any MCP session. |
| **Skills install**, **models bench/calibrate**, **config set/get**, **import-obsidian** | — | **CLI-only** | Session-setup operations that don't belong in an MCP server. |
| Error recovery (e.g. `DIMENSION BREAK`) | Either — MCP surfaces `warnings[]` in the JSON envelope | Either — CLI prints warnings to stderr | The *content* of the warning is identical; only the channel differs. |

### Key takeaways

1. **MCP is (mostly) a strict subset of CLI for read operations**, with three extras: `kb_update_meta`, the embedding cache, and the threshold cache. For read-heavy agent sessions, MCP wins on latency.
2. **CLI is the superset** — vault management, skills, config, models, and import/export are CLI-only by design. MCP is intentionally scoped to vault-content operations.
3. **The JSON envelope is the contract** — since 0.1.12, `2nb search --json` and `2nb ask --json` return `{mode, warnings, results}` and `{mode, warnings, answer, sources}` respectively (multi-turn asks via `--history` add a `rewritten_query` field). These are defined in:

   ```go
   // cli/internal/cli/search.go:31-35
   type SearchResponse struct {
       Mode     string          `json:"mode"`      // "hybrid" or "keyword"
       Warnings []string        `json:"warnings,omitempty"`
       Results  []search.Result `json:"results"`
   }

   // cli/internal/cli/ask.go:22-27
   type AskResponse struct {
       Mode     string   `json:"mode"`
       Warnings []string `json:"warnings,omitempty"`
       Answer   string   `json:"answer"`
       Sources  []string `json:"sources"`
   }
   ```

   Any agent that consumes these must decode the envelope and extract `.results` / `.answer`. A raw array decode will fail.

## Teaching improvements (Phase B — additions to `SKILL.md`)

The current `SKILL.md` (280 lines at `cli/internal/skills/content/2ndbrain-skill.md`) is a solid command reference. It's missing four things an agent reaches for under pressure:

### 1. "Which surface should I use?" section

Embed the matrix above in short form. Agents default to whichever tool they see first; without guidance, they'll pick inconsistently. A compact decision block makes the choice mechanical.

### 2. "Error recovery playbook" section

Derived from `VectorCompat` in `cli/internal/retrieve/compat.go` — that function produces the single actionable warning line that ends up in the `warnings[]` envelope or on stderr. The conceptual labels in the project `CLAUDE.md` decision table (`DIMENSION BREAK`, `MODEL MISMATCH`, etc.) do **not** appear in the actual strings; agents should match on the stable prefix `"semantic search disabled:"` and the phrases below.

| Warning the agent sees | Underlying state | Fix |
|---|---|---|
| `"semantic search disabled: vault was embedded with Nd vectors but current provider X produces Md"` | Dimension mismatch (switched providers, existing embeddings unusable) | `2nb index --force-reembed` OR switch provider back |
| `"semantic search disabled: provider X unavailable — falling back to keyword search"` | Configured provider not reachable right now | No immediate fix. BM25 still runs. Check `2nb ai status` for why (creds, service down, etc.). |
| `"semantic search disabled: no AI provider configured — run '2nb ai setup' to enable"` | Nothing configured | `2nb ai setup` |
| `"semantic search disabled: embedder X not registered"` | Config names a provider that isn't compiled in | Re-check `ai.provider` in `2nb config show` |
| Zero warnings, `mode: keyword` anyway | Vault has no embeddings yet | `2nb index` (BM25 works immediately; embeddings backfill) |
| Empty search result | Usually a threshold issue, not a content gap | Try `--threshold 0.15` or `--bm25-only` |
| `kb_ask` says "no relevant documents" | `ask` and `search` share a threshold but `ask` only considers top 5 results (`tools.go:307`); a borderline match at rank 8 reaches `search` but not `ask` | Drop to `kb_search` with the same query — it will see more ranks |
| CLI errors with "schema version N newer than supported" | Vault opened by a newer `2nb` build than the one installed | `brew upgrade apresai/tap/twonb` |

The battery test `TestBattery_HybridDegradation` should assert on `strings.HasPrefix(warning, "semantic search disabled:")` — matching the full message would be too brittle against provider-name changes.

### 3. "Discoverability commands" section

When an agent lands in an unfamiliar vault (user says "help me with this project", MCP not configured, etc.), it needs a fixed drill for self-orientation:

```bash
2nb vault status        # Is this a vault? Is it healthy? How many docs?
2nb ai status           # Can I use semantic search? Which provider?
2nb config show         # Full config (vault paths, AI config)
2nb list --json --limit 5   # Sample of content
2nb skills show claude-code # Self-referential — what am I supposed to know?
```

The skill file should name this drill so agents consistently reach for the same five commands.

### 4. "Worked JSON examples" section

Show the actual envelope shapes, not prose descriptions. Example:

```bash
$ 2nb search "authentication" --json --limit 2
{
  "mode": "hybrid",
  "warnings": [],
  "results": [
    {
      "path": "use-jwt-for-auth.md",
      "title": "Use JWT for Auth",
      "score": 0.0163,
      "vector_score": 0.72,
      "content": "...",
      "type": "adr",
      "status": "accepted"
    },
    ...
  ]
}
```

And under a degraded state (actual warning string from `cli/internal/retrieve/compat.go`):

```bash
$ 2nb search "authentication" --json
{
  "mode": "keyword",
  "warnings": ["semantic search disabled: vault was embedded with 1024d vectors but current provider \"openrouter\" produces 768d — run '2nb index --force-reembed' or switch provider back to the one that built this vault"],
  "results": [...]
}
```

Agents should be taught to check `warnings[]` and `mode` before assuming hybrid search ran. Match on the stable prefix `"semantic search disabled:"` — the tail of the message includes provider/dim details that change. A second, structurally distinct class also lands in `warnings[]` when the optional rerank stage is enabled and its Bedrock call fails: `"rerank disabled: <error>"` (search still returns, just in the un-reranked hybrid order). Match that stable prefix too if you care whether reranking ran.

## Test battery design (Phase B)

Four tiers now exist, from cheapest to most expensive; the first three are built, the fourth is deliberately future:

1. **Golden-path battery** (`cli/battery_test.go`, `make test-battery`) — curated end-to-end scenarios proving the documented workflows work. Shipped.
2. **Usage battery** (`cli/battery_usage_test.go`, `make test-usage`) — drives the REAL binary and the real MCP server over stdio through the workflows this doc teaches (write→query index round-trips, obsidian-compat forms). Shipped.
3. **Skill-selection eval** (`cli/internal/eval/skillusage_test.go`, `make test-skill-eval`) — feeds the canonical SKILL.md to a real model as its system prompt and scores whether it picks the taught command for ~12 natural tasks (search-not-grep, move-not-mv, meta-not-sed, envelope parsing, degraded-mode reading). Opt-in twice over (real tokens + `2NB_SKILL_EVAL=1`), never in `test-all`; asserts a loose 70% floor so model drift logs rather than flakes. First live run: 12/12 with Haiku 4.5. Shipped.
4. **Full agent-loop benchmark** (an LLM actually executing tools against a seeded vault, scored on task completion) — explicitly future: high build cost and non-deterministic scoring for marginal signal over tiers 2+3, which already separate "the commands work" from "the skill teaches the right commands".

The original two-tier design below describes tier 1 plus targeted **gap-filler tests** that close coverage holes identified during exploration.

### Golden-path battery — `cli/internal/cli/battery_test.go`

A single Go test file containing 7 end-to-end scenarios. Each scenario is one test function (`TestBattery_VaultLifecycle`, etc.) so `go test -run TestBattery_VaultLifecycle` works for iteration.

Shared with existing tests via `cli/internal/testutil.NewTestVault` and `CreateAndIndex`. Real-API policy: embeddings tests skip if no provider is configured (consistent with the rest of the suite).

New Makefile target:

```makefile
test-battery:
	cd cli && go test -race -tags fts5 -run TestBattery -timeout 180s .
```

Add `test-battery` to `test-all` so CI runs it by default.

| Scenario | What it exercises | What it asserts |
|---|---|---|
| `TestBattery_VaultLifecycle` | `vault create` → `vault set` → `vault list` → `vault status` → `vault show --json` | Each command exits 0, sidecar files appear, active vault tracked in `~/.2ndbrain-vaults`, `vault list` marks active with `*`, `vault show --json` is parseable. |
| `TestBattery_DocumentCRUD` | `create --type note` → `read` → `meta --set status=complete` → `search` → `delete --force` | Doc appears on disk with valid frontmatter, `read` returns body, `meta` enforces status machine, `search` finds the doc by title, `delete` removes it from disk AND index (verify via `list`). |
| `TestBattery_IndexRebuild` | `index` → edit → `index` → `index --force-reembed` | Delta indexing picks up content change. After `--force-reembed`, embedding count stable, content hashes updated, all docs have fresh embeddings (verify via `store.DB.AllEmbeddings`). |
| `TestBattery_SearchThreshold` | Seed 3 docs with known similarity profiles → `search --threshold 0.2`, `--threshold 0.9`, `--bm25-only` | Low threshold returns all, high threshold returns none, `--bm25-only` bypasses threshold entirely. Verify `mode` field in JSON envelope reflects the choice. |
| `TestBattery_HybridDegradation` | Stale embeddings (dim 1024) + current provider (dim 768) → `search --json` | `mode == "keyword"` and at least one warning starts with `"semantic search disabled:"` (the stable prefix from `cli/internal/retrieve/compat.go`). Results still return (BM25 works). |
| `TestBattery_MCPLifecycle` | Spawn `2nb mcp-server` → invoke `kb_info` via stdio → read sidecar → kill → respawn | Sidecar at `.2ndbrain/mcp/<pid>.json` exists with invocation logged (tool name, timestamp, ok, duration_ms). `2nb mcp status --json` reports the live server. Stale sidecars cleaned on next spawn. |
| `TestBattery_SkillsRoundtrip` | `skills install claude-code --user` → `skills list` → `skills uninstall` | File at `~/.claude/skills/2nb/SKILL.md` appears, `skills list` marks installed, uninstall removes the file. Don't clobber the user's real skill dir — the test should use a temp `HOME`. |

#### Obsidian-native additions (0.5.0)

These scenarios prove an LLM client can drive the tool against a real Obsidian vault. They live in `cli/battery_obsidian_test.go` (same `e2e_test` package + harness).

| Scenario | What it exercises | What it asserts |
|---|---|---|
| `TestBattery_MCPStdioDriveTools` | Spawn `2nb mcp-server`, speak MCP over **real stdio** (mcp-go client): `initialize` → `kb_create` → `kb_info` → `kb_search` | The JSON-RPC handshake + marshal boundary works (not just direct handler calls): the created note is reported by `kb_info` and surfaced by `kb_search`. This is the genuine "an LLM client drives the server" proof. |
| `TestBattery_Migrate` / `TestBattery_MigrateDryRun` | Build a legacy schema-v2 `index.db` → `2nb migrate` (and `--dry-run`) | Schema upgrades v2→v3; source markdown is byte-for-byte unchanged (non-mutating guarantee); `--dry-run` reports v2 and changes nothing. |
| `TestBattery_ObsidianNativeRAG` | `.obsidian` vault with wikilinks, `aliases`, inline `#tag` → `index` → `search --json` → `list --tag` → (gated) `ask --json` | `search --json` returns the pinned `{mode,warnings,results}` envelope (BM25, ungated); inline `#design` tag is indexed (`list --tag design` finds it); grounded `ask --json` returns a non-empty `answer` + string `sources` when a provider is configured. |
| `TestBattery_CanvasBaseIndexing` | Write `.canvas` (JSON) + `.base` (YAML) → `index` → `list --json` | Both appear as first-class indexed documents. |

The JSON envelope is the contract for every consumer (the Obsidian plugin, the Swift app). `cli/internal/cli/contract_envelope_test.go` pins the `search` envelope shape and that `ask` `sources` is a `[]string` (the exact shape the plugin parses) — without needing a provider.

### Gap-filler tests

These aren't in the battery — they go in existing (or new) test files targeted at specific modules:

| File | New tests | Why |
|---|---|---|
| `cli/internal/cli/vault_test.go` (existing) | `TestVaultSet`, `TestVaultList`, `TestVaultStatus_AllPortabilityStates` | The 8 portability states from the project `CLAUDE.md` table are tested for search warnings (`cli/internal/retrieve/compat_test.go`) but not for how `vault status` renders them. |
| `cli/internal/cli/index_test.go` (existing) | `TestIndex_ForceReembed_ReplacesEmbeddings` | `--force-reembed` is only covered implicitly via the integration test. Add an explicit flag behavior test. |
| `app/Tests/SecondBrainTests/AppStateCRUDTests.swift` (new) | Create / open / save / delete through `AppState`, not just JSON parsing | Swift tests currently stop at `FrontmatterParser` and `JSONDecoding`. The actual state transitions in `AppState` aren't tested. |
| `app/Tests/SecondBrainTests/AutoSaveTests.swift` (new) | 15/30/60s interval picker; disabled-save preference; low-disk warning before save | Autosave is a core write-path feature with zero test coverage today. |
| `app/Tests/SecondBrainTests/CrashJournalTests.swift` (new) | Pre-write snapshot; parse-on-open recovery from `.recovery.md` | Crash recovery is safety-critical and untested. |
| `tests/gui-test-vault-switch.sh` (new) | Open vault A → switch to B → sidebar reflects B's contents | `gui-test-crud.sh` covers vault reopen within one vault; switching vaults is a different flow (AppKit folder picker, FSEvents rebind). |
| `tests/gui-test-polish.sh` (new) | Cmd+Option+P → diff view → Accept writes polished, Reject discards | Polish UI is untested. The diff view is reused by merge conflict resolution, so a regression here spans multiple features. |

## Verification for Phase B

- `make test` — existing Go tests still pass. Skill embed change is compile-time (`go:embed`), so any typo breaks the build immediately.
- `make test-battery` — new golden-path battery runs in under 3 minutes on a warm machine. Timeouts set to 180s to accommodate embedding calls when providers are available.
- `make test-swift` — new `AppState` / autosave / crash tests pass.
- `make test-gui` — includes new vault-switch and polish scripts.
- `make test-all` — full suite.
- Manual MCP check: start `2nb mcp-server` against a test vault, confirm sidecar appears in `.2ndbrain/mcp/`, kill it, confirm sidecar cleaned on next spawn. Verifies the battery's `TestBattery_MCPLifecycle` scenario mirrors real behavior.
- Provider sanity: `2nb ai status` with AWS creds → shows OK. Without → shows actionable fix hint matching the error-recovery playbook text.

## Source-of-truth files (keep in sync)

| File | What it defines | Who depends on it |
|---|---|---|
| `cli/internal/cli/search.go:31-35` | `SearchResponse` JSON envelope | Swift `AppState.swift`, any external agent parsing `search --json` |
| `cli/internal/cli/ask.go:22-27` | `AskResponse` JSON envelope | Same |
| `cli/internal/retrieve/compat.go` | `VectorCompat` state strings (`DIMENSION BREAK`, `MODEL MISMATCH`, etc.) | Skill playbook table, `vault status` rendering, search warnings |
| `cli/internal/ai/config.go` | `ResolveSimilarityThresholdFull` resolution chain | Threshold resolution battery test, skill explanation of per-model thresholds |
| `cli/internal/mcp/tools.go:34-82` | Embedding + threshold cache behavior | MCP vs CLI rationale in skill file |
| `cli/internal/skills/content/2ndbrain-skill.md` | The agent-facing teaching document | All 8 supported agents after `skills install` |

Drift between these files and the skill file is the main failure mode. The battery's `TestBattery_SearchThreshold` and `TestBattery_HybridDegradation` tests exist partly to catch drift — a change to warning strings that doesn't also update the skill will make the battery fail because it expects specific strings in `warnings[]`.
