# 2ndbrain

Obsidian-native AI companion. **Obsidian stays your editor**; the Go CLI (`2nb`) + MCP server are the engine that indexes, searches, and answers (RAG) over a real Obsidian vault. A thin Obsidian plugin and a macOS configuration dashboard wrap the CLI.

**Write-surface guarantees** (the product's core safety contract; keep every change inside it):

- `2nb` writes only a gitignored `.2ndbrain/` sidecar. Note bodies change only via explicit, user-invoked commands (`append`, `prepend`, `replace`, `daily append`/`prepend`, `task`, `polish --write`, and the link fixers with `--write`); frontmatter only via `meta` (which has always rewritten files in place).
- `polish --write` snapshots the original under `.2ndbrain/recovery/polish/` first, so `polish --undo` reverts it (a whole-file restore that refuses to clobber post-polish edits without `--force`). `repair-links`, `relink`, and `unlink` share that per-note snapshot slot.
- `move`/`rename` is the strongest write surface: it rewrites every `[[wikilink]]` AND markdown-style `[text](path.md)` link across the vault that points at the moved note. Preview with `--dry-run` first; the enforced gates are crash-safe ordering (the target file moves LAST, after referencing notes are rewritten, so an interruption leaves links pointing at the still-present old name), and an ambiguity guard (a non-`--force` move is refused when a bare `[[name]]` could point at more than one note; `--force` rewrites only path-qualified links). **The guard's `name` is the RESOLVER's** (`store.Resolver`: path, unique suffix/basename, title, alias), not the on-disk basename: it used to be byte-exact on the filename, so a link naming the note by a shared TITLE or alias resolved to nothing, was invisible to both `Backlinks` and `LinksByRawName`, and the move completed silently and repointed it. Deliberately CLI-only, never an MCP tool.
- One further explicit exception: `2nb plugin install` writes the plugin bundle under `.obsidian/plugins/obsidian-2ndbrain/`; never notes, never Obsidian settings.

## Repository Layout

- `cli/`: Go CLI binary (`2nb`) + MCP server (the engine)
- `app/`: Swift macOS configuration & companion dashboard, **not an editor** (SwiftUI + AppKit)
- `plugins/obsidian-2ndbrain/`: thin Obsidian plugin that shells out to `2nb`
- `reqs.md`: EARS-format requirements specification; `press-release.md`: product vision; `test-plan.md`: requirements validation plan

### Self-hosted agent skill

Source of truth: `cli/internal/skills/content/2ndbrain-skill.md` (Go-embedded into the CLI). The repo-root mirrors (`.agents/skills/2nb/SKILL.md`, `.warp/skills/2nb/SKILL.md`, `.claude/skills/2nb/SKILL.md`; the last is tracked via a `.gitignore` carve-out, since `.claude/` is otherwise ignored) are generated: edit the source, then run `make sync-skills`; `make check-skills-sync` fails release CI on drift. Never edit a mirror directly.

### Project docs (`docs/`)

Reference detail lives here, not in this file (see Documentation discipline at the end).

- [`cli-reference.md`](docs/cli-reference.md): full per-command reference (flags, JSON shapes, cross-command invariants) behind the slim table below
- [`macos-app.md`](docs/macos-app.md): macOS app tab-by-tab reference, AI Hub, Settings window, GUI test patterns
- [`ai-providers.md`](docs/ai-providers.md): Bedrock auth mechanics, the mantle plane, invoke strategies, live pricing, builtin catalog, cost estimator
- [`search-tuning.md`](docs/search-tuning.md): similarity threshold + Nova asymmetry (measured), hybrid RRF weights, reranking A/B, embedding concurrency, RAG budgets
- [`release-playbook.md`](docs/release-playbook.md): versioning mechanics, reindex-on-release contract, release-app checkpoint/resume and notarization failure modes
- [`quick-start.md`](docs/quick-start.md): end-to-end getting-started walkthrough
- [`agent-teaching.md`](docs/agent-teaching.md): MCP vs CLI decision matrix, the search/ask JSON envelope contract, degraded-mode playbook
- [`obsidian-cli-mapping.md`](docs/obsidian-cli-mapping.md): Obsidian-CLI compatibility mapping (accepted argument forms, non-goals)
- [`mcp-integration.md`](docs/mcp-integration.md): MCP setup snippets per client, tool table with parameters, server internals
- [`templates.md`](docs/templates.md): built-in document type templates and schemas
- [`polish-prompt-eval.md`](docs/polish-prompt-eval.md) / [`link-prompt-eval.md`](docs/link-prompt-eval.md): how the polish and link-suggest prompts were chosen (measured, reproducible)
- [`adr/0001-vector-search.md`](docs/adr/0001-vector-search.md): vector-search architecture decision (per-chunk sqlite-vec primary)
- [`adr/0002-model-routes.md`](docs/adr/0002-model-routes.md): model identity is a route, `(provider, id, plane, region)`
- [`vault-structure.md`](docs/vault-structure.md): SQLite table reference (index.db, bench.db, metrics.db; current) plus legacy layout sections (superseded, see [obsidian/README.md](docs/obsidian/README.md))
- [`obsidian/README.md`](docs/obsidian/README.md): Obsidian-native pivot documentation and architectural model
- [`multi-machine-setup.md`](docs/multi-machine-setup.md): what 2nb state ports across machines vs stays machine-local
- [`claude-md-snippet.md`](docs/claude-md-snippet.md): copy-paste CLAUDE.md block for agent users of 2nb
- [`llama-local-engine.md`](docs/llama-local-engine.md): llama.cpp local-engine build status and the engine-provisioning gap

## Versioning

Format `major.minor.build`; the single source of truth is the `VERSION` file at repo root. The CLI reads it via LDFLAGS into `internal/cli.Version`; the app generates `app/Sources/SecondBrain/Version.swift` (never edit by hand); `make version-plugin` syncs the plugin's manifest/package files (release CI fails on drift; the sync refuses to lower the plugin version). Bump with `make bump-build` / `bump-minor` / `bump-major`; `make set-version V=x.y.z` for explicit jumps. Mechanics: [docs/release-playbook.md](docs/release-playbook.md).

**Reindex-on-release flag:** a release that changes indexing/embedding LOGIC (chunk boundaries, chunk-to-vector mapping, embedding purpose/pooling) without changing model, dimension, or schema leaves users' indexes silently stale. Bump `EmbedGeneration` (change needs `2nb index --force-reembed`) or `IndexGeneration` (index-only) in `cli/internal/vault/generation.go`, and note the reindex in the CHANGELOG. `make check-index-generation` (release CI) fails if a watched indexing/embedding/resolution file changed since the last tag without a bump (the watched list lives in `scripts/check-index-generation.sh`); add a `Reindex-Not-Needed: <reason>` commit trailer when genuinely not needed. Runtime surfacing always prompts, never auto-spends. Contract detail: [docs/release-playbook.md](docs/release-playbook.md).

## Release

Both products ship via Homebrew (`brew install apresai/tap/2nb`; `brew install --cask apresai/tap/secondbrain`). The machine-readable release contract is [`.release.yaml`](.release.yaml) (read by the `oss-release` skill); keep it in sync with the Makefile.

**A curated `## [Unreleased]` block IS the release notes.** `scripts/update-changelog.sh` treats hand-written entries as authoritative and promotes them verbatim under the new version heading (the commit summarizer runs only when nothing human-authored exists). So write that block as user-facing copy, not as engineering notes, and read it once before the front door: an overclaim there ships. Mechanics: [docs/release-playbook.md](docs/release-playbook.md).

**`make release-all` is the front door** (canonical clone only; needs gitignored `scripts/sign.env`): test gate, bump (`BUMP=build|minor|major|none`), tag, wait for CI, then sign/notarize/publish the app + cask, verifying every product shipped at one version. The two-step model underneath: (1) GitHub Actions on tag push ships the CLI via GoReleaser to `apresai/homebrew-tap` (pure Go, both architectures from one runner) and uploads the Obsidian plugin assets to the GitHub release (**CI never builds the macOS app or cask**); (2) `make release-app` locally builds, Developer ID-signs, notarizes, staples, and publishes the DMG + cask (signing keys never enter CI). `release-app` is checkpointed and resumable (`build/release-state-<VERSION>.json`); `make release-app-status` reports without changing anything; `RELEASE_NOWAIT=1` submits and exits; `make release-local` is a CLI-only local release. The app bundles a version-matched `2nb` at `Contents/Resources/2nb` and strips its quarantine at launch (`CLIPath.prepareBundledCLI()`); the cask still depends on the `twonb` formula so the terminal and plugin have a PATH `2nb`. Signing order, notarization self-heal, DMG sweep rules, and every failure mode: [docs/release-playbook.md](docs/release-playbook.md).

## Build

```bash
make build              # Both CLI and app (regenerates Version.swift)
make build-cli          # cli/bin/2nb only
make build-app          # macOS app
make test               # Doc + release-script gates, then all Go tests
cd cli && make install  # Install to /usr/local/bin/2nb
```

**Pure Go (no CGO):** the CLI uses `modernc.org/sqlite` with FTS5 and sqlite-vec compiled in, so the shipped binary builds `CGO_ENABLED=0` and cross-compiles to any GOOS/GOARCH from one host. Tests keep CGO on only because `-race` needs it. Launch the macOS app via `open` on the `.app` bundle, never the raw binary (it won't register with the window server):

```bash
open app/.build/arm64-apple-macosx/debug/SecondBrain.app
```

## Testing

```bash
make test               # Doc + release-script gates, then Go unit tests
make test-battery       # Golden-path E2E battery (cli/battery_test.go)
make test-usage         # MCP write->query index round-trips + real-binary E2E battery; catches index-consistency regressions (AI steps skip without creds)
make test-swift         # Swift unit tests
make test-gui           # GUI tests via AppleScript + screencapture
make test-all           # Everything
make install            # Build + install CLI to /usr/local/bin + app to ~/Applications
```

Go tests use `t.TempDir()` for isolated vaults; run with `cd cli && make test` (`go test -race ./...`).

### No Mock Tests Policy

**All tests MUST use real API endpoints, local or paid. Mocks (`httptest.NewServer`, fake responses, stub implementations) are NOT allowed.** Tests needing a provider call the real API and skip if credentials/services are unavailable. This applies to AI provider tests (Bedrock, OpenRouter, Ollama), MCP tests, and any future integration tests.

- Bedrock: real AWS credentials
- OpenRouter: real `OPENROUTER_API_KEY`
- Ollama: real server at localhost:11434

**Skip on capability, not configuration.** A test that needs a provider probes once per test binary and skips when the provider cannot actually serve a request. Gating on "is the env var set" makes a configured-but-unusable provider FAIL the test instead of skipping it, which is how transient noise once read as a product regression. Helpers: `requireEmbedding` / `requireEmbeddingHostHome` (`cli/capability_test.go`), `requireEmbeddings` (in-package). CI runs the suite credential-free on every PR, so this is enforced.
- Pure logic tests (string classification, price parsing) that don't call any API are fine

### GUI Test Automation

GUI tests drive the installed app via AppleScript + `screencapture` (run `make install` first; scripts live in `tests/`, screenshots in `/tmp/sb-gui-tests/`). Interaction patterns (NSAlert vs SwiftUI overlays, first-responder workarounds, sidebar coordinates): [docs/macos-app.md](docs/macos-app.md).

## Go CLI (`cli/`)

**Module:** `github.com/apresai/2ndbrain` · **CLI:** cobra · **MCP:** mark3labs/mcp-go · **DB:** `modernc.org/sqlite` (pure Go) with FTS5 compiled in; sqlite-vec backs the per-chunk `vec_chunks` vec0 KNN, the primary vector path

### Package Layout

| Package | Purpose |
|---------|---------|
| `internal/ai` | Provider interfaces, registry, Bedrock/OpenRouter/Ollama/llama-local implementations |
| `internal/cli` | Cobra command definitions (one file per command) |
| `internal/vault` | Init/open, config, schemas, templates, indexer |
| `internal/document` | Markdown parsing, frontmatter, chunking, wikilinks |
| `internal/store` | SQLite CRUD, migrations, link resolution |
| `internal/search` | BM25 search engine with structured filters |
| `internal/polish` | Shared copy-edit engine: prompt, grounded link candidates, invented-link stripper, deterministic link repair, snapshot/undo |
| `internal/graph` | Link graph BFS traversal |
| `internal/mcp` | MCP server with 22 tools + sidecar status files |
| `internal/git` | Read-only git wrappers |
| `internal/skills` | Skill file generation and agent registry |
| `internal/instructions` | Managed "2ndbrain" block for agents' global memory files (sentinel-delimited, backup-safe) |
| `internal/output` | JSON/CSV/TSV/YAML/raw/md/text formatters |
| `internal/bench` | Benchmark history DB (`bench.db`) + probes |
| `internal/metrics` | Vault performance observatory DB (`metrics.db`) |
| `internal/eval` | Retrieval/answer-quality evaluation harnesses |
| `internal/retrieve` | Shared hybrid retrieval pipeline (search/ask/MCP) incl. `VectorCompat` |
| `internal/llama` | Bundled llama.cpp engine management (model pins, licenses) |
| `internal/testutil` | Test helpers (NewTestVault, CreateAndIndex) |

Key types: `document.Document`, `store.DB`, `vault.Vault`, `search.Engine`, `graph.Graph`.

### CLI Commands (95)

Full per-command reference (flags, JSON shapes, invariants): **[docs/cli-reference.md](docs/cli-reference.md)**; `--help` on any command. This table is the map plus the gotchas an agent needs before invoking.

| Command | Purpose |
|---------|---------|
| `init` | Deprecated alias for `vault create` |
| `vault` / `vault status` | Unified health report (index coverage, portability, AI reachability, stale docs) |
| `vault show` | Terse summary (path, source, name, doc count) |
| `vault create <path>` | Initialize a new vault + record in recents. Does NOT switch the active vault: 2nb follows the vault Obsidian has open |
| `vault set <path>` | Register an existing vault in recents; does not switch the active vault either |
| `vault list` | List recent vaults (`~/.2ndbrain-vaults`, display-only, never a resolution source) |
| `vault checkpoint` | Collapse + truncate the index WAL; GUI-safe (an active reader reports `busy` rather than forcing) |
| `create` | New doc from template (`--type --title --path --content`; `--overwrite` reuses the id, `--append` appends, `--allow-duplicate` skips the hash guard; default dedupes to `<slug>-1.md`) |
| `read` | Read doc or `--chunk` section (alias `print`) |
| `append` / `prepend` | Add to a doc body (`--text`/`--file`/stdin); frontmatter untouched |
| `replace` | Replace the body, or one heading's section with `--section` (first match wins) |
| `daily` | Resolve/create today's daily note from Obsidian's daily-notes plugin config; `daily read`/`append`/`prepend`/`path`; never hard-errors on missing config |
| `meta` | Frontmatter view/update with schema validation (`--set`/`--get`/`--remove`; aliases `frontmatter`/`fm`/`properties`). Array fields REPLACE on `--set tags=a,b` (`tags=` clears); use `tag add`/`remove` for incremental edits. Writes re-index the file; metadata-only edits never re-embed |
| `index` | Rebuild index; `--doc <path>` single file; `--force-reembed` invalidates all embeddings; the embed pass is concurrent (`ai.embed_concurrency`). An unparseable note is reported and its index row dropped, never fatal; Obsidian's template folders are not indexed |
| `search` | Hybrid BM25 + semantic (`--type --status --tag --limit --threshold --bm25-only`) |
| `list` | List docs with filters (alias `files`; `--total`; `--format paths`/`tree`) |
| `lint [glob]` | Schema validation + broken-wikilink findings; JSON rows carry `fix`/`drift_target`/`candidates` so UIs can offer one-click repairs |
| `stale` | Docs not modified within `--since` days |
| `metrics` | Performance observatory from `.2ndbrain/metrics.db`: last build, gauges, token usage, per-op aggregates; `metrics clear` |
| `related` | Related docs via link graph (`--depth`) |
| `backlinks <path>` / `links <path>` | Resolved inbound links / outbound links incl. unresolved (per-file broken-link view) |
| `orphans` / `deadends` | Docs with no resolved inbound / outbound links |
| `unresolved` | Every broken wikilink vault-wide (`--total`) |
| `repair-links <path>` | Deterministic, AI-free broken-wikilink repair: rewrites only a unique normalized match (case/separator drift); ambiguous targets reported, never guessed; `--target` scopes; previews by default, `--write` snapshots |
| `relink <path>` | Repoint a broken link: `--from <raw> --to <existing note>`, exact matching, suffix/form preserved; preview by default, `--write` snapshots |
| `unlink <path>` | Remove a broken link keeping its visible text; exact `--target` scoping; embeds and code spans never touched; preview/`--write` |
| `graph <path>` | The document's direct inbound and outbound links as a JSON adjacency list (one hop; `related --depth` walks further) |
| `outline <path>` | Heading tree (shared with MCP `kb_structure`) |
| `wordcount <path>` | Word/char/heading counts over the indexable body (alias `wc`) |
| `tasks` / `task <path> <line>` | List GFM checkboxes vault-wide (`--done`/`--todo`/`--path`) / toggle one checkbox at a 1-based body line |
| `folders` | Folder list with doc counts |
| `tags` / `tags rename <old> <new>` | Tag list with counts / vault-wide frontmatter tag rename (frontmatter-only in v1; `--dry-run`; per-file atomic) |
| `tag add\|remove <note> <tag>...` | Per-note frontmatter tag edits (validated, deduped, reindexed) |
| `aliases` | Frontmatter aliases mapped to documents |
| `export-context` | CLAUDE.md-compatible context bundle. A body, like `git diff`: raw/md/text emit it, `--json` wraps it as `{bundle, docs, chars}`, csv/tsv/yaml refused |
| `delete` | Delete doc from disk + index. Prompts `[y/N]`; agents pass `--force` (an unanswered prompt times out after 60s WITHOUT deleting) |
| `move <src> <dst>` / `rename <src> <name>` | Link-rewriting move/rename (see write-surface guarantees; preview with `--dry-run` first) |
| `import-obsidian` / `export-obsidian` / `migrate` | Legacy conversion + legacy-vault migration (`migrate --dry-run`; never mutates source markdown) |
| `mcp-server` | Stdio MCP server; exits with its client (parent-death watchdog); idle self-exit is opt-in (`--idle-timeout`) |
| `mcp status` / `mcp reap` / `mcp configured` / `mcp doctor` | Live server processes / stale-orphan cleanup backstop / durable per-client is-it-configured check / in-process self-test that hard-fails a vault whose semantic channel silently degraded to BM25 |
| `mcp install` / `mcp uninstall` / `mcp-setup` | Write/remove the server entry in a client config (idempotent, backup-first, preserves unrelated keys byte-for-byte) / setup instructions |
| `setup` | One-command front door: skill + MCP + global-instructions block per client (`--client`/`--all`, idempotent, backup-safe) |
| `instructions install\|configured\|uninstall` | Managed "2ndbrain" block in a client's global memory file (sentinel-delimited, version-stamped, removes cleanly) |
| `plugin status` / `plugin install` | Installed-plugin version vs CLI / install-update from the latest GitHub release (manifest written last; no-downgrade guard, `--force` overrides) |
| `suggest-links` | Semantically related docs to link from a given doc |
| `suggest-target <target>` | Ranked "did you mean" candidates for ONE broken link: drift/semantic/keyword tiers, `--llm` re-rank (fail-closed), `--verdict` emits exactly one machine recommendation; LLM picks cap at medium confidence, never silent auto-fixes |
| `polish` | AI copy-edit preview (original + polished); `--write` applies + snapshots; `--links` adds grounded wikilinks (invented links stripped); `--repair-links` runs the deterministic repair BEFORE the copy-edit; `--undo` restores the snapshot |
| `git activity\|show\|diff\|status` | Read-only git views over vault files |
| `ask <question>` | RAG Q&A with sources; feeds full parent notes (windowed only over budget); `--history <path\|->` for multi-turn |
| `chat` | Interactive multi-turn REPL over the ask pipeline |
| `eval` | Retrieval scorecard from the user's own notes (QA set cost-gated + cached in `.2ndbrain/eval/`); `eval tune` (suggest-only sweep), `eval answers` (LLM-jury grades), `--estimate` (prices without calling any model) |
| `ai status` | Provider, models, readiness, portability, model-access summary |
| `ai embed <text>` / `ai embed-probe` | Debug embedding / empirically find a safe `ai.embed_concurrency` (cost-confirmed, discards results) |
| `ai setup` | Multi-provider wizard (bedrock/openrouter/ollama/llama-local); passing probes persist as `user_verified` |
| `ai local` / `ai engine` | Local AI readiness / manage the bundled llama.cpp engine (`pull`/`rm`/`serve`/`install`/`bootout`) |
| `models list` | Catalog: `--discover` (the 24h cache over BOTH Bedrock planes; `--refresh` re-walks), `--working-set`, `--recommended`, `--enabled-only` (dropdowns pass it, CLI use does not), `--sort best` (bench-informed) |
| `models test <id>` | Real smoke probe; `--save` persists pass AND fail with a classified `test_error_code`; strategy-aware deadline (a slow model is never failed, only a hang) |
| `models verify [ids...]` | Batch access probes, cost-gated (`--cost-cap` default $0.50, `--yes`); persists every result; `--discover` reaches mantle-listed models; multi-region fallback on region-shaped failures; `--events` streams NDJSON for the GUI |
| `models add\|remove\|enable\|disable\|enable-state` | User-catalog edits (updates merge; `--vendor` bulk toggles; tri-state enable pointer) |
| `models policy set\|show\|clear` | Enable-only vendor policy per provider; future discoveries arrive pre-disabled; active models are never policy-disabled; stored in dedicated `models-policy.yaml` |
| `models cost-preview [ids...]` | USD estimate per probe kind; local-only, never invokes a model |
| `models discover` | Discovery verb: cached pool with per-source ages + NEW/GONE diff; `--refresh`; `--add` persists a row WITH its routing; `--validate` probes with verify's cost gate |
| `models wizard` | Interactive end-to-end setup (`--set-active` writes the chosen models via the `config set` path) |
| `models bench` / `models calibrate` | Benchmarks against the vault (history in `.2ndbrain/bench.db`; `retrieval`/`search` probes are zero-API) / cosine-distribution threshold recommendation (doc-to-doc sampling overstates asymmetric search thresholds) |
| `models bench fav\|unfav\|favs\|history\|compare` | Benchmark favorites and history views |
| `skills list\|install\|uninstall\|show` / `skills doctor` | SKILL.md management per agent (version-stamped; `skills list` self-heals stale unmodified installs) / verify the skill + that `2nb` resolves on PATH |
| `config show\|get\|set\|set-key\|bedrock\|doctor` | Config read/write with validation; `config bedrock` manages machine-local `~/.config/2nb/bedrock.json` (masked `token_suffix`, included regions, `--prefer-stored-token`); `config doctor` diagnoses AI config (defects fail, environmental issues warn) |
| `doctor` (alias `verify`) | THE end-to-end proof that the setup actually works: tier 1 probes the active models with NO vault; tier 2 folds in `config doctor` + `mcp doctor` wholesale. Calls models for real; automatic/repeating callers MUST use the free `--versions` form |
| `update` | Release check across CLI/app/plugin (24h cache, refetched when behind an install; never hard-errors offline) |
| `completion` / `completion install` | Shell completion scripts / idempotent zsh install |

**Global flags:** `--format` (json/csv/tsv/yaml/raw/md/text; listings also `paths`/`tree`), `--porcelain`, `--json`, `--csv`, `--yaml`, `--vault`, `--unconfigured` (permit a write to a vault Obsidian doesn't know; refused otherwise), `--verbose`/`-v` (structured slog to stderr + `.2ndbrain/logs/cli.log`; without it, file only), `--copy` (also write rendered output to the macOS clipboard).

**An explicit `--format` is honored or refused by name, never silently ignored.** A report command routes every non-empty format through `emitStructured` (`root.go`), so csv/tsv/yaml render and raw/md are refused when the value has no document body; checking only for `--json` and falling through to a human printer is the anti-pattern that made fourteen commands print prose for csv and for raw. `refuseBodylessFormat` runs at the TOP of a handler, before the vault open and before any provider call: `search`/`list` would otherwise refuse only after the zero-result early return, and `suggest-links`/`suggest-target`/`polish` would fail on credentials instead of on the format, which is how the credential-free CI job went red. A JSON-event-STREAM command (`models bench`, `ai engine pull|rm`) cannot render as one document and refuses other formats by name (`refuseNonJSONStream`). `git diff` and `export-context` are the exceptions, and they are symmetric: their output IS a body, so raw/md/text emit it, json wraps it in a record (`{path, diff}` / `{bundle, docs, chars}`), and csv/tsv/yaml are REFUSED (neither a diff nor a context bundle is a row set). In csv/tsv, composite cells are compact JSON (`output.delimitedCell`), never Go syntax, and a pointer cell is its VALUE (`%v` on the pointer printed a heap address). `text` is a READING format, not a parsing one: a row list renders as `name=value` pairs per line, a single struct as `name: value` lines, a map sorted, all through the same `delimitedCell` and the json field names, with nil and `omitempty`-zero fields omitted; it used to be `%v`, which is a Go struct dump. Point anyone parsing at `tsv` or `json`. **`fieldSpecs` follows `encoding/json`'s own field rules**, so text and csv cannot name a field differently from json: an anonymous struct field with no json name is FLATTENED (its index becomes a path, shallower wins on a collision, a nil embedded pointer renders empty rather than panicking), and a `json:"-"` field is skipped. Skipping only unexported fields is what made `doctor --format text` and `skills doctor --format text` print a `SuiteStatus:` / `Verification:` blob keyed by a Go type name. An embedded type that renders ITSELF (TextMarshaler/json.Marshaler) stays one value, and an embedded UNEXPORTED struct is still skipped rather than promoted. `yaml` is the JSON view in YAML syntax: `writeYAML` marshals to JSON and re-reads it, so the field names, tags and `omitempty` are the json ones (yaml.v3 alone lowercases bare Go field names and honors no json tag). The PERSISTED yaml writers in ai/vault/document are separate and untouched.

**Only actual writes take the write opener.** `meta --get` and the bare `meta` view are reads (`openVault`); `--set`/`--remove` are writes, and the flag-combination check runs BEFORE the open so a conflict is reported as a flag conflict. `daily`, `daily path` and `daily read` open through `openDailyVault`, which escalates to the write opener only when today's note has to be CREATED; `daily append`/`prepend` are writes unconditionally.

**Obsidian-CLI compatibility:** an argv preprocessor (`preprocessArgs` in `root.go`) accepts obsidian-CLI-style invocations as a drop-in: `key=value` arguments, boolean tokens, and colon-commands; full mapping in [docs/obsidian-cli-mapping.md](docs/obsidian-cli-mapping.md). `path=` is strict exact, `file=` is the fuzzy resolver (`store.ResolveTarget`), a bare positional is auto. A free-text `search`/`ask`/`chat` query is never parsed as `key=value`, and an unrecognized `key=value` passes through verbatim.

**Parent-command defaults:** `2nb ai` → `ai status`, `models` → `models list`, `git` → `git status`, `mcp` → `mcp status`, `plugin` → `plugin status`, `skills` → `skills list`, `config` → `config show`, `metrics` → `metrics show`, `instructions` → `instructions configured`.

### AI Providers

Default provider is **AWS Bedrock**: generation Claude Haiku 4.5 (`us.anthropic.claude-haiku-4-5-20251001-v1:0`), embeddings Amazon Nova-2 (`amazon.nova-2-multimodal-embeddings-v1:0`, 1024 dims). Defaults live in `DefaultAIConfig()` (`cli/internal/ai/config.go`). Full provider reference (auth mechanics, invoke strategies, Marengo shapes, pricing cache, compatibility gates, builtin catalog, cost estimator): **[docs/ai-providers.md](docs/ai-providers.md)**.

**Bedrock auth:** a Bedrock API key (bearer token) or the AWS SDK credential chain. Precedence: `AWS_BEARER_TOKEN_BEDROCK` env, then `~/.config/2nb/bedrock.json`, then the macOS Keychain, then SigV4; the SDK prefers a bearer token over SigV4, so a stored key overrides `~/.aws` for Bedrock. `prefer_stored_token: true` in bedrock.json inverts env-vs-stored for 2nb only (with no stored key the env var still applies). The token is never written into vault `config.yaml`; a world-readable file is refused. This is how the macOS app reaches Bedrock without shell credentials.

**Model identity is a ROUTE, `(provider, id, plane, region)`** (ADR 0002). The same model can be served on both Bedrock planes and in several US regions with independent entitlement, so each endpoint is its own catalog row. Plane selects the CLIENT, `InvokeStrategy` the ENVELOPE within it; non-Bedrock providers leave both plane and region empty and keep their old identity exactly. Discovery walks both planes across us-east-1/us-east-2/us-west-2 and collapses nothing. Config names the route (`ai.generation_{plane,region}`, `ai.embedding_{plane,region}`, and the nested `ai.rerank.{plane,region}`), written as one unit with the model. **At invoke time a resolved route is authoritative:** a slot whose model has several routes and names none of them is REFUSED with the pick commands, never resolved by preference order, and the generation constructor dispatches on the resolved plane while the embedder and reranker honor the resolved region through `RegionOverride`, which wins over every catalog pin (including in the mantle host). Inference remains only where nothing was resolved at all, i.e. a model no catalog has seen. `PreferRoutes` ranks what to PROBE, where a wrong guess costs a probe; it must never choose what to INVOKE, where a wrong guess silently sends a real query to an endpoint the account cannot serve. Key catalog identity operations (save, overlay, merge, active-marking, slot resolution) on the route, never on `(provider, id)`. Canonical text form `id@plane/region`; print `RouteKey.Unqualified()` in suggested commands, since the provider prefix uses `|` (a shell pipe). The pre-route strategy/region resolvers are unexported and confined to the route-less fallback (a bare id no catalog has seen); invoke paths dispatch on the resolved route. `persistProbedRegion` is a save-path stamp, not a resolver. `inferenceProfileBaseID` stays for discovery-time dedupe. Mechanics: [docs/ai-providers.md](docs/ai-providers.md), ADR 0002.

**Bedrock mantle plane:** partner-hosted frontier models (builtins `openai.gpt-5.5`, `xai.grok-4.3`; Grok 4.6 is dual-plane, with the classic Converse `us.xai.grok-4.6` as its builtin) on an OpenAI-Responses REST API at `https://bedrock-mantle.<region>.api.aws`. Bearer-token only, region-pinned per model, invisible to `ListFoundationModels` but enumerable via each region's `/v1/models` (`--discover` merges the listing with routing hints; listing proves existence, not entitlement or dialect). The plane returns 401 for two causes; the body's `error.code` disambiguates (bad key vs staged-rollout `access_denied`), and remediation for a gated mantle model points at AWS Sales, never the Bedrock console. Resolved endpoint hosts are constrained to `https` + `*.api.aws` so a poisoned catalog cannot exfiltrate the token. Generation-only. Deep mechanics: [docs/ai-providers.md](docs/ai-providers.md).

**Ollama and OpenRouter are opt-in** (`disabled: true` in a fresh vault). `Disabled` only hides a provider's models from dropdowns; an explicitly-chosen active provider still runs. **llama-local** (bundled llama.cpp + Gemma weights, fully offline) is a fourth opt-in provider and is **NOT user-ready**: only weights are provisioned, the `llama-server` engine binary is neither bundled nor downloaded, so the macOS GUI hides every llama-local surface behind `AIHubView.localEngineFeatureEnabled` (currently `false`). Do not surface llama-local in the GUI until the engine ships. Status and path forward: [docs/llama-local-engine.md](docs/llama-local-engine.md).

### Search tuning (threshold, weights, rerank, concurrency, RAG)

Full reference with the measured numbers and re-measure commands: **[docs/search-tuning.md](docs/search-tuning.md)**. The operative rules:

- **Similarity threshold** resolves vault config > saved calibration > model recommendation > `0.20` default (`AIConfig.ResolveSimilarityThresholdFull`). Nova-2 embeds queries with the asymmetric retrieval purpose, which collapses the cosine scale: its threshold is **`0.25`**, and a pre-flip saved calibration (~0.65) silently degrades search to BM25-only (`ai status` warns when an asymmetric model resolves > 0.45). `models calibrate` samples doc-to-doc pairs and overstates asymmetric search-time thresholds.
- **A builtin fact is the builtin's unless the user typed it, and provenance is PER FACT.** A row's Name, Dimensions and ContextLen are the user's only where `authored_facts` lists that field (`ai.FactName`/`FactDimensions`/`FactContextLen`), which only `models add --name/--dimensions/--context-length` appends to, one entry per flag passed; ConfigHint and Recommended are builtin-owned outright. One stamp for three facts was unsound: `models add --context-length` claimed a name the user never typed, and a row authoring one fact donated its EMPTY others to discovered routes. Every save path seeds its row from the MERGED catalog, so an unlisted fact is a copy that would otherwise shadow the builtin's own later correction (a stale `context_length` kept `models list` wrong and, through `inheritModelFacts`, spread to freshly discovered per-region rows). Read side: `mergeFields` decides each fact via `takeTopFact` (the global-under-vault user overlay passes `baseIsBuiltin` false, where the vault row wins UNLESS the global row authored the fact and it did not); `inheritModelFacts` picks a donor per fact, deterministically. Write side: `preserveUserFacts` carries a listed fact, a price override, notes and the route's benchmark from the RAW stored row, drops an unlisted fact for a builtin model (the self-heal) and keeps one for a model no builtin declares; `AdoptRoutingHints` adopts a context length only for a model no builtin declares. Never seed a persisted row from the merged catalog. Those rules apply whatever ROUTE the user row names, not only where it matches a builtin row exactly: `reconcileBuiltinFacts` runs right after `retireSupersededTemplates` and hands every row of a builtin model the builtin's facts, curation, supported dimensions, modalities and recommended threshold, keeping only what `authored_facts` (or a stamped `threshold_source`) claims. Builtins are authored route-less, so a probe row pinned to a plane and region shares no route key with one, `mergeFields` never ran for it, and retirement only fills EMPTY fields: that is how a released 0.22.2 vault had `models list --json` reporting Nova at context_length 2048 and threshold 0.65 while `ai status` correctly ignored the 0.65. Share `takeTopFact`; never restate the rules.
- **A user-catalog threshold is a calibration only if it carries the stamp.** `models calibrate --save` and `models add --similarity-threshold` are the only writers and both stamp `threshold_source: user` (`ai.ThresholdSourceUser`); every probe/benchmark/promotion save goes through `preserveUserThreshold`, which strips the field and restores only a stored user value. `ai.IsUserThreshold` is the single read-side predicate and it is stamp-only: the 0.22.1 heuristic that kept an unstamped value differing from the builtin is gone, because it read a mirror of a RETIRED builtin (0.65 for Nova) as a calibration 2.6x the current recommendation, which rejects every semantic match. An unstamped value is ignored and `ai status` names the file, the value, and how to re-save it.
- **Hybrid weighting:** `ReciprocalRankFusion` (k=60) fuses BM25 + vector rankings; `ai.bm25_weight`/`ai.vector_weight` (default 1.0 each; 0 resolves to default, negatives rejected) bias it. Nova's shared space retrieves cross-lingually without translation (directional eval guard in `internal/eval/crosslingual_test.go`).
- **Reranking is default-OFF and measured to HURT at small scale** (~160 docs: worse R@1 and worse LLM-jury answer quality; the textbook saturated-retrieval outcome). Do not enable on a small vault, and re-measure before enabling anywhere. Providers: Cohere Rerank 3.5 on Bedrock (us-east-1 only) and llama-local bge-reranker; degrades to the RRF order on any failure; `ai.rerank.enabled`/`ai.rerank.model`/`ai.rerank.candidate_docs`.
- **Embedding concurrency:** the bulk embed pass (`vault.EmbedDocuments`, shared by CLI and MCP `kb_index`) uses a bounded worker pool; `ai.embed_concurrency` (1-64; `0` = per-provider automatic: bedrock 4, openrouter 3, ollama 2). Self-correcting under throttling (retries with backoff + jitter). `2nb ai embed-probe` finds an account's real ceiling empirically.
- **A provider retry is never silent.** All five Bedrock retry sites (classic InvokeModel, classic Converse, Cohere rerank, the mantle generation 429 loop, and the mantle MODEL-LISTING 429 loop) log one INFO line and record into the `ai.RetryCounter` on the call's context; the loop records its OWN attempt budget there too, since the classic loops allow five and the mantle ones three, and the notice quotes what the loop reported rather than a constant. The MCP server attaches a fresh counter per tool call (never shared between calls), so agent-driven rows carry retries like the CLI's; `metrics.db.embed_retries` (schema v3) keeps the count per operation, and `vault status` / `ai status` advise lowering `ai.embed_concurrency` only when the last build retried AND the configured value exceeds the automatic one. The CLI's `slowCallNotice` prints one self-erasing stderr line past 2s under noteWrite's rule (TTY, not `--porcelain`), and it REUSES a counter already on the context rather than shadowing it. The notice and the embed pass's per-document progress printer may never share a terminal line (the notice writes without a newline and erases what is there), so exactly one owns it: `embedProgressOpts` is the single decision point, the whole-vault pass takes `withEmbedProgress`, and `index --doc`, which has a notice, takes `withoutEmbedProgress`.
- **RAG context** feeds **full parent notes** to the generator, windowing around the matched heading only when a note exceeds budget (`internal/ragctx.Build`, shared by CLI and MCP). Budgets: `ai.rag_context_budget` 60000 / `ai.rag_note_budget` 20000 runes (`config set` rejects negative or >400000; 0 resolves to the default).

### Vault Portability

(The 0.5.0 path-based identity ADR is [docs/obsidian/identity-model.md](docs/obsidian/identity-model.md); the sidecar read/write boundary is [docs/obsidian/vault-coexistence.md](docs/obsidian/vault-coexistence.md).) A vault is self-contained: markdown + `.2ndbrain/index.db` + `config.yaml`; `tar czf` and open elsewhere. **The DB is the source of truth** for what produced the stored embeddings (`documents.embedding_model` + BLOB length); config is user preference only, and `2nb index` never writes derived state back to `config.yaml` (no merge conflicts in git-shared vaults).

| DB state | Outcome | Fix |
|---|---|---|
| embeddings match dim D and model M, provider available | **OK** | none |
| dim D in DB, provider produces D' ≠ D | **DIMENSION BREAK** | `2nb index --force-reembed` or switch back |
| model M' ≠ M in config, same dim | **MODEL MISMATCH** | next `2nb index` re-embeds on content change, or `--force-reembed` |
| provider configured but unavailable | **PROVIDER UNAVAILABLE** | start/install provider; BM25 runs meanwhile |
| mixed models in DB | **MIXED** | `2nb index --force-reembed` |
| mixed dimensions in DB (partial Matryoshka re-embed) | **MIXED DIM** | `2nb index --force-reembed` (a bare reindex won't normalize widths) |
| zero embeddings, docs present | **UNINDEXED** | `2nb index` (BM25 still works) |
| vault `schema_version > max` | **DB TOO NEW** | `brew upgrade apresai/tap/twonb` |
| `config.yaml` missing/corrupt | **self-heals** | regenerated; `.bak` preserved on corrupt |

**Search row shape:** every `search` row carries `frontmatter` (the note's parsed map, `omitempty`), populated at BOTH scan sites in `internal/search` so BM25, enumerated, and vector-only rows agree; MCP `kb_search` rows inherit it. An enumerate-by-filter row (blank query plus a filter) carries the document's FIRST chunk (`chunk_id`, `heading_path`, and 200 characters of its body), joined in `search.go` rather than `store/docs.go`, which is watched by the index-generation gate. A body-less note has no chunks, so the join is an OUTER one and those three fields come back empty. A VECTOR-ONLY row is hydrated the same way, by `hydrateVectorOnly` immediately after `ReciprocalRankFusion`, so every caller of `HybridSearch` (search, ask, kb_search, eval) sees the matched chunk's first 200 characters: from the matched chunk on the vec0 leg, from the document's first chunk on the brute-force leg, which knows no chunk.

**Frontmatter is a CONTIGUOUS block of keys, and that is the whole rule.** A note opening with a doubled delimiter (`---\n---\n`, what Obsidian writes when you remove the last property and what `SerializeFrontmatter` writes for an empty map) is genuinely ambiguous, so `legacyDoubledDelimiterFrontmatter` finds the closing fence (`\n---\n`, its CRLF form, or `---` at end of file) and reads the region between them as properties ONLY when it is a contiguous key block: skip leading blank lines, then NO blank line may remain, interior or trailing, and it must parse as a non-empty mapping. Blankness is INVISIBILITY, not emptiness: a line is blank when it trims to nothing under `unicode.IsSpace` plus the zero-width format characters (U+200B, U+FEFF, U+200C, U+200D), which `IsSpace` does not cover. And the recognized leading blanks are STRIPPED from the region, not merely stepped over: leaving them in the text handed to YAML is what turned a tab-only line, legal filler and illegal indentation, into a parse failure that discarded a note's real properties into its body. Validating one region and parsing another is the shape of that bug, so `doubledFenceKeyBlock` returns the exact text both the reader and the surgical writer parse. Nothing simpler discriminates, because BOTH competing shapes have a blank line right after the doubled fence: `---\n---\n\nStatus: draft\n\n---\n\nRest\n` is body and `---\n---\n\ntitle: Real\ntags: [a, b]\n---\nBody\n` is properties, and the difference is the blank line BEFORE the closing fence. Judging by the leading blank line got both wrong in turn (absorbing the first whenever that line carried whitespace, discarding the second outright), and the write path is what makes either fatal: `UpdateDocumentFrontmatterAST` consults the same function, so `meta --set` and `tag add` REWRITE the note accordingly, either hoisting body lines into a properties block or dropping real properties into the body, against the write-surface guarantee. Never normalize line endings to find the fences: the body is sliced from the ORIGINAL bytes, because the write path rewrites the whole file from it and a normalized body would silently convert every CRLF note. `closingFence` is the single definition of where frontmatter ends and `doubledFenceKeyBlock` the single definition of what is inside it; the writer used to re-derive both and could disagree with the reader about either. The residual is now small and pinned by test: a body whose first non-blank stretch has no blank line in it and parses as a mapping (`---\n---\nStatus: draft\n---\nbody`) still reads as properties. Real prose rarely takes that shape, and losing real metadata is the worse failure, so the tie falls there. **`SerializeDocument` also guarantees exactly one blank line between an empty block and a body that does not already start with one**, so 2nb never manufactures a shape it would then misread (it did: an empty map plus the body `Status: draft\n\n---\n\nAction items\n` was written and read straight back as `{Status: draft}` with that line gone). The separator is added once and never grows, and `normalizeBody` trims it, so no content hash, chunk or embedding moves. `SerializeFrontmatter` still returns the bare `---\n---\n`: it promises the block, not the note. Moving this boundary is INDEXING LOGIC: `document/frontmatter.go` and `document/document.go` are on the `check-index-generation` watch list, and 0.22.3 bumps `IndexGeneration` to 3 for it (not `EmbedGeneration`: the content hash is computed from the parsed body, so a plain `2nb index` re-embeds exactly the affected notes rather than charging every user for a whole-vault rebuild).

**A note that will not PARSE is data to report; a note that will not READ is a failure that changes nothing.** The distinction is `document.ErrRead`, stamped where the read happens, and it is the difference between reporting a note and destroying it: wrapping every `ParseFile` error as unparseable meant a permission bit or a file locked mid-save deleted the note's row, chunks and embeddings. A parse failure is skipped, named (`index --json` `unparseable[]`, the merged human summary, the MCP `kb_index` result) and its row DROPPED the way a deleted file's row is; a read failure keeps the existing row untouched and lands in `unreadable[]`. Neither is counted as a failed EMBEDDING in EITHER phase (nothing was sent), which is what let one bad note fail a whole `--force-reembed`: the embed pass classifies a read failure into `EmbedStats.Unreadable` and the completeness arithmetic excludes it, so the run keeps every embedding it paid for, names what is left, and exits non-zero WITHOUT restoring. Only a genuine provider failure counts in `Failed` and rolls back. `StampAfterIndex` takes that shortfall (`Failed` plus the unreadable count), not `Failed` alone, because an unreadable note keeps a vector `InvalidateAllEmbeddings` never cleared and would otherwise let a partial run stamp the vault current-generation. `index --doc` on either kind still exits non-zero, and the macOS app surfaces that per-note failure instead of debug-logging it. A FULL index in the app sets that banner too, which is why `startIndex` runs `index --json` (never `--porcelain`, which silences the stderr progress the sheet scrapes) and decides from the envelope: exit 0 stopped meaning "nothing was wrong" once an unparseable note became non-fatal AND had its row dropped, so clearing on exit 0 alone made Rebuild go quiet about a note that had just left the index. It clears only when both lists are empty, keeps what it has when the envelope does not decode, and the vault switch still resets it outright. Both lists merge the walk phase and the embed phase through `vault.MergeUnparseable`, deduplicated by path, shared so `index --json` and MCP `kb_index` cannot drift, and feed one summary that names up to five notes and points at `-v` only when it withheld some. EVERY command that indexes reports that summary, not just `index`: the auto-index on a first `search` and `import-obsidian` call the same helpers. Obsidian's own template folders (`.obsidian/templates.json` `folder`, Templater's `templates_folder`) are excluded from the walk and from `index --doc` (which names the folder), matched on whole path segments, and named by `vault status`; rows already indexed under a folder that LATER becomes excluded are purged by `purgeStale` before the walk, since a walk that skips a folder can never clean up after itself.

**Loud degradation:** `search`/`ask` call `VectorCompat` (`cli/internal/retrieve/compat.go`) at the hybrid gate; unusable embeddings print one stderr line, land in the JSON `warnings`, and force BM25-only (the app shows a yellow banner from the same messages). **JSON envelope (breaking from 0.1.12):** `search --json`/`ask --json` return `{mode, warnings, results}` / `{mode, warnings, answer, sources}`. `mode`, `warnings` and `results`/`sources` are always present and never `null` (`rewritten_query` stays optional). A zero-result `search` still emits the full envelope, which is the point: `warnings` is where degradation is reported, so an empty result set is exactly when an agent needs it. Every `--json` listing returns `[]` rather than `null` on an empty result (`list`, `stale`, `tags`, `aliases`, `orphans`, `deadends`, `unresolved`, `backlinks`, `links`, and `related`'s nested `edges`); those are bare arrays and carry no envelope. `--yaml` gives the same `[]` (`writeYAML` maps a top-level nil slice or map to the empty collection, so one fix covers every command rather than the eight `jsonSafeList` sites), and `csv`/`tsv` give the HEADER ROW alone: `writeDelimited` takes the element TYPE, so a zero-row slice is a csv document instead of falling through to the JSON-record fallback that printed `null` into a delimited stream. `search` keeps its empty csv/tsv stream on zero hits (it returns early, before the writer), and `list`'s bare and `--porcelain` forms keep printing their stderr hint and nothing on stdout. `ask` instead exits non-zero when retrieval finds nothing. Contract and degraded-mode playbook: [docs/agent-teaching.md](docs/agent-teaching.md). Nova Matryoshka dimensions (256/384/1024/3072) validate via `ai.SupportedDimensionsFor`; changing dimension needs `--force-reembed`. Shipping/tar recipe and the embeddings-are-content privacy caveat: README "Portable vaults".

### MCP Server (22 tools)

Tool table with parameters, setup snippets, and server internals (sidecar `<pid>.json` status files, metrics recording, the `ServerInstructions` self-announcement, the parent-death watchdog): **[docs/mcp-integration.md](docs/mcp-integration.md)**. Tools: `kb_info`, `kb_search`, `kb_ask`, `kb_read`, `kb_list`, `kb_create`, `kb_update_meta`, `kb_related`, `kb_structure`, `kb_backlinks`, `kb_links`, `kb_tags`, `kb_tasks`, `kb_delete`, `kb_index`, `kb_append`, `kb_replace_section`, `kb_suggest_links`, `kb_polish` (preview-only, never writes), `kb_git_activity`, `kb_git_diff`, `kb_git_status`. Write tools re-index through the same shared paths as the CLI. `move`/`rename` and `polish --undo` are deliberately CLI-only (highest blast radius).

## Swift macOS App (`app/`)

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+. **Dependencies:** GRDB.swift, Yams, swift-markdown. **Architecture:** MVVM with `@Observable`. Full tab-by-tab and view reference: **[docs/macos-app.md](docs/macos-app.md)**. The invariants below govern changes.

**A configuration and companion dashboard, not an editor.** Obsidian is the editor. The app reads the same `.2ndbrain/index.db` (WAL) the CLI writes and shells out to `2nb` for all AI/index/lint/git work, preferring its bundled, version-matched CLI at `Contents/Resources/2nb` (`CLIPath.resolve()`). An `FSEventsWatcher` keeps the index fresh (debounced `2nb index --doc` for external edits, skipping the app's own writes); on bind, a one-shot incremental sync catches up notes changed while the app was closed.

**Vault binding follows Obsidian, and writes are firmer than reads.** The app binds to the vault Obsidian has open, read from Obsidian's own registry (`~/Library/Application Support/obsidian/obsidian.json`, via `ObsidianRegistry`); the CLI reads the same registry as its authoritative active-vault source (`vault.ObsidianOpenVault`). Read resolution: `--vault` > `2NB_VAULT` > open Obsidian vault > cwd-vault; there is NO 2nb-managed active-vault pointer file. On the write path (`openVaultAndSetActive`, also used by the MCP server): the cwd is never an implicit write target (a vault resolved only by walking UP the tree is refused before any open, so a write can never mint a `.2ndbrain/` sidecar in an unintended vault), and an explicit `--vault` to a vault Obsidian doesn't know is refused without `--unconfigured` (`2NB_UNCONFIGURED=1` for the flagless MCP server). This is the guard that stops a mis-`cd`'d agent from splitting a vault.

**Sidebar (seven entries):** Home (default: Vault/AI/AI-Clients-summary/Index cards), Models (`SimpleModelsView`: vendor checkboxes → Validate → working-set pickers; the full AI Hub catalog behind a "Full catalog" disclosure), Notes (lint findings with verdict-driven link fixes: Fix all for high-confidence, Remove dead links gated behind a confirm sheet, per-finding sheets with no dead ends), Testing (Validate/Benchmarks/Performance/Quality panes), Health (status only, runs no operations), Activity (Git + MCP), Settings (inline `SettingsView`).

**Settings dual-host rule:** the same `SettingsView` renders as the Cmd+, window (the only surface that works with NO vault bound) and inline as the last sidebar tab, sharing one `AppState.settingsTab`; `isInline` gates only host-specific behavior. Each settings view's `reload()` is single-flight per instance with pending-rerun coalescing. Entered keys render masked (from `config bedrock --json`'s `token_suffix`) and are **verified before being accepted**. "Test everything" runs `2nb doctor --json` and must read stdout regardless of exit status (doctor exits non-zero exactly when it has a verdict worth showing). `BedrockRegions.risk` is tri-state (`safe`/`breaks`/`unverifiable`) and fails closed when no vault is bound.

**Single-writer rule for external client configs:** only the Settings Integrations tab writes skill/MCP/global-instructions files; Home shows a read-only summary. **Paid operations are always cost-gated** through `PaidOperationConfirm` (priced by `models cost-preview` / `eval --estimate`; caps derived as 2x estimate + $0.01), and heavy runs share single-flight claims (`BenchRunModel`, `VerifyRunModel`). **`Set Active` is refused while indexing** (prevents mixed-model embeddings). Vendor identity (`vendor`/`family`/`version_sort_key`) and the `compatible` flag are computed by the Go CLI (`applyCatalogUIFields`) and sent over JSON; Swift never mirrors that logic. Long-running probes surface the shared `ColdStartHint` after 15s (patient probe deadlines make a working cold model look hung without it).

### Menus & Shortcuts

**Vault**: New/Open (Cmd+Shift+O)/Reveal/Status/Sync/Validate/Import/Export. **View**: Recent Activity (Cmd+Shift+G). **AI**: Models… (Cmd+Shift+,), Testing & Benchmarks… (Cmd+Shift+T), MCP Configuration, MCP Status (Cmd+Shift+M). `AppDelegate` renames the File menu to "Notes".

### macOS SwiftUI Gotchas

- **Use AppKit dialogs for modals:** prefer `NSAlert.runModal()` / `NSOpenPanel.runModal()` over SwiftUI `.sheet()` when a modal needs reliable button/keyboard events.
- **Computer-use access:** the `.app` bundle must have a real binary (not symlink) and be ad-hoc codesigned; the Makefile handles this.
- **Troubleshooting:** for SwiftUI platform bugs, use Context7 and Brave Search before guessing.
- **Yams traps, uncatchably:** `Yams.load` can `fatalError` (NOT throw) on malformed YAML (Obsidian template placeholders like `date: {{date}}`, duplicate keys), so `do/catch` won't save you; this crashed a shipped release. Parse untrusted frontmatter via `Yams.compose` (AST only) plus a manual, depth-bounded `Node` walk; see `FrontmatterParser`.

### Context7 Library IDs

| Library | ID |
|---------|----|
| SwiftUI (Apple docs) | `/websites/developer_apple_swiftui` |
| Swift language book | `/swiftlang/swift-book` |
| Swift concurrency migration | `/swiftlang/swift-migration-guide` |
| GRDB.swift (SQLite) | `/groue/grdb.swift` |

## Obsidian Plugin (`plugins/obsidian-2ndbrain`)

A thin wrapper that shells out to `2nb`; Obsidian remains the editor. Source of record: `plugins/obsidian-2ndbrain/main.ts`. Install via BRAT or release assets; end users never run npm. Feature detail (commands, chat panel, polish flow, managed CLI download, settings sections): [docs/obsidian/user-guide.md](docs/obsidian/user-guide.md) and [docs/obsidian/integration-guide.md](docs/obsidian/integration-guide.md). The rules:

- **Every CLI call is pinned to the open Obsidian vault** via `--vault adapter.getBasePath()` (`pinVaultArgs`), so 2nb can never resolve a different vault from the registry or cwd.
- **Per-command timeouts carry their derivations in the unit-test failure messages** (`commandTimeoutMs`): `index` unbounded (killing it mid-run leaves a partial index), `ask` 2940s (four sequential legs of the Go `ai.MantleWorstCase` 723s transport budget + slack; the plugin must never kill a working cold model the CLI is still legitimately waiting on), `doctor` 180s, default 120s.
- **The plugin only ever runs the free `doctor --versions` form** for its Components section (bare `doctor` calls the active models for real).
- **CLI resolution is version-aware** (`resolveCliPath`): a plugin-managed download wins over a system install only when at least as new; `ensureCliFresh` re-downloads a managed copy that falls behind; a custom path is honored verbatim.
- Polish is apply-then-review (`polish --write --links --repair-links` after a `flushEditor` save, then a diff modal with Keep/Undo); a single-flight lock serializes its four trigger surfaces.

## Vault Format

Layout, schemas, and the SQLite table reference (index.db incl. the schema v4 `meta` generation stamps and `vec_chunks`, bench.db, metrics.db): **[docs/vault-structure.md](docs/vault-structure.md)** and **[docs/templates.md](docs/templates.md)** (document types). Quick facts: documents are plain `.md` with YAML frontmatter (`title`, `type`, `status`, `tags`, `created`, `modified`); note identity is path-based ([docs/obsidian/identity-model.md](docs/obsidian/identity-model.md)): an `id` UUID is read and preserved when present, and template-created notes include one, but it is never required; wikilinks `[[target]]` / `[[target#heading]]` / `[[target|alias]]`; `.canvas`/`.base` files are indexed as read-only synthetic views (never written back). The `.2ndbrain/` sidecar holds `config.yaml`, `schemas.yaml` (the only committable file in team vaults), `index.db`, `models.yaml`, `models-policy.yaml`, `bench.db`, `metrics.db`, `eval/`, `mcp/`, `recovery/`, `logs/`; `2nb vault create` writes a `.gitignore` covering the personal/local state.

## Obsidian Conversion

Superseded by 0.5.0 native vault operations: see [docs/obsidian/migration-guide.md](docs/obsidian/migration-guide.md) (`migrate`) and README (`import-obsidian`/`export-obsidian`).

## MCP Integration

Per-client config snippets: [docs/mcp-integration.md](docs/mcp-integration.md), or run `2nb mcp-setup` / `2nb setup --client <name>`.

## Documentation discipline

This file carries **rules, invariants, and pointers**; reference detail lives in `docs/` (index under Repository Layout) and in each command's `--help`. A PR documents itself in README, the relevant `docs/` file, and its `--help` text; it adds at most a few sentences of new RULES here, with a pointer. `make check-claude-md-size` (run by `make test` and release CI) fails when this file exceeds 100k characters; the Claude Code harness truncates it at 150k.
