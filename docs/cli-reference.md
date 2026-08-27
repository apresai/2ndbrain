# 2nb CLI Reference

The full reference for every `2nb` command: what it does, its flags, its output shapes, and the invariants that connect it to the rest of the toolchain. Commands are grouped the way `2nb --help` groups them (Getting Started, Documents, Search & AI, Quality, Integration, Import / Export, Configuration); use `--help` on any command for full flag detail. `2nb` also accepts obsidian-CLI-style invocations (`key=value` arguments, colon-commands like `daily:append`, command aliases): the complete compatibility mapping and accepted argument forms live in [obsidian-cli-mapping.md](obsidian-cli-mapping.md).

## Getting Started

### init

Deprecated alias for `vault create`.

### vault

`vault [path]` prints the health report (same as `vault status`). A legacy positional path acts like `vault set`.

### vault status

Unified health report: vault info, index coverage, portability, AI reachability, and stale docs.

### vault show

Terse summary of the active vault: path, source, name, doc count. Supports `--json`.

### vault create

`vault create <path>` initializes a new vault and records it in recents (this replaces `init`). It does NOT make the new vault active: 2nb follows the vault Obsidian has open, so open the new folder as a vault in Obsidian (or pass `--vault`) to use it.

### vault set

`vault set <path>` registers an existing vault in recents (for `vault list`). 2nb's active vault follows Obsidian's open vault, so this does not switch the active vault: open it in Obsidian, or pass `--vault`.

### vault list

List recently used vaults. Reads `~/.2ndbrain-vaults`.

### vault checkpoint

Collapse and truncate the index WAL (`PRAGMA wal_checkpoint` PASSIVE then TRUNCATE, via `store.DB.Checkpoint`). SQLite's auto-checkpoint flushes but never truncates the `-wal` file, so a busy vault's `index.db-wal` can park at its high-water mark; this command shrinks it. It is GUI-safe: an active reader makes TRUNCATE report `busy` rather than forcing it. `--json` reports `{wal_bytes_before, wal_bytes_after, db_bytes, pages_total, pages_checkpointed, busy}`.

## Documents

### create

Create a document from a template (`--type`, `--title`, `--path`, `--content`). `--path <subdir>` files the doc under a vault-relative subdirectory (created if missing); the default is the vault root. `--content` sets the initial body instead of the type template.

Same-title collision handling: `--overwrite` replaces an existing same-title note in place (reusing its id, so the index stays consistent); `--append` appends the content to an existing same-title note (else creates it). `--allow-duplicate` is the orthogonal content-hash guard. The default with neither flag keeps the collision-free `<slug>-1.md` dedupe.

The filename is a kebab-case slug of the title (lowercased, spaces to dashes, other punctuation dropped, an empty result falls back to a UUID). Human output echoes `Created <type>: <slug>.md (title: "<title>")`, and `--json` returns `path` plus `title`.

### read

Read a full document or a specific section (`--chunk`). Alias: `print`.

### append

Append content to a document's body (`--text`, `--file`, or stdin). An explicit, opt-in body write; leaves frontmatter untouched.

### prepend

Insert content at the start of a document's body, after the frontmatter (`--text`, `--file`, or stdin).

### replace

Replace a document's body, or just one heading's section content with `--section <heading>` (`--text`, `--file`, or stdin). First match wins on duplicate headings.

### daily

Resolve today's daily note from Obsidian's core daily-notes plugin config (`.obsidian/daily-notes.json`: folder, format, optional template). Bare `daily` resolves, creates the note if missing, and prints the vault-relative path. `daily path` is an explicit subcommand for the same resolve-and-print (it backs the obsidian-CLI `daily:path` form). `daily read` prints the note's body; `daily append` and `daily prepend` (`--text`, `--file`, or stdin) add to the body via the shared body-write path. A missing or disabled daily-notes plugin falls back to Obsidian defaults (root folder, `YYYY-MM-DD`); the command never hard-errors. The date format honors Moment's `[literal]` bracket-escaping.

### meta

View or update frontmatter with schema validation. Aliases: `frontmatter`, `fm`, `properties`.

- `--set key=value` writes a field. Array-typed fields (`tags`, `aliases`, or any schema `list`/`tags` field) are coerced to a YAML list, comma-split, with replace semantics: `--set tags=a,b` becomes `[a, b]`, and `--set tags=` clears the field. Use `tag add`/`tag remove` for incremental edits.
- `--get <key>` reads one field (exit `ExitNotFound` if absent).
- `--remove <key>` (repeatable) deletes a field in place, preserving comments and order. It refuses identity keys (id, path, title, type) and schema-required fields.

Writes re-index the whole file (chunks, tags, links via `IndexSingleFile`), so a frontmatter tag change is reflected in `list --tag` immediately; re-embedding stays gated on the body content hash, so a metadata-only edit does not re-embed.

The obsolete positional form `meta set/get/remove <path> ...` is rewritten to the flag form by the argv preprocessor; a malformed variant it cannot rewrite errors with a copy-pasteable flag-form hint (exit `ExitValidation`) instead of Cobra's terse arg-count message.

### list

List documents with filters (`--type --status --tag --limit --sort`). Alias: `files`. `--total` prints only the count; `--format paths` prints one vault-relative path per line; `--format tree` prints an indented directory hierarchy.

### tasks

List GFM checkbox tasks (`- [ ]` / `- [x]`) across the vault. Filters: `--done`, `--todo`, `--path <file|dir>`. `--total` prints only the count. v1 covers GFM open/done only (custom statuses like `[>]`/`[-]` are ignored). Supports `--json`.

### task

`task <path> <line>` toggles a single GFM checkbox at a 1-based body line. `--done`/`--todo`/`--toggle` (default toggle); errors if the line is not a checkbox. Writes the body via the shared body-write path (frontmatter untouched).

### tags

List all tags vault-wide with counts. A parent command: bare `tags` lists, and `tags list` is the explicit subcommand.

### tags rename

`tags rename <old> <new>` renames a frontmatter tag across every document that carries it: it rewrites each doc's frontmatter `tags` array (deduping when `<new>` is already present) and reindexes. FRONTMATTER-ONLY in v1: inline body `#old` tags are not rewritten, and such docs are skipped. `--dry-run` previews the affected docs without writing. The operation is per-file atomic with a collected `{renamed, skipped, failed}` summary; it exits non-zero on any failure, with no rollback of already-written files.

### tag add

`tag add <note> <tag>...` adds one or more frontmatter tags to a single note, the per-note counterpart to the vault-wide `tags` (mirroring the `task`/`tasks` split). It merges into the note's `tags` array (dedupe, order preserved), schema-validates each tag, and reindexes via the shared write path so the change is immediately `list --tag`-searchable. Tags may be separate args or comma-separated; the note resolves via `file=`, `path=`, or a bare positional. Frontmatter-only; rejects read-only `.canvas`/`.base` files.

### tag remove

`tag remove <note> <tag>...` removes one or more frontmatter tags from a single note (a no-op if absent), with the same resolution, validation, and reindex behavior as `tag add`.

### delete

Delete a document from disk and the index. Prompts `[y/N]` interactively; pass `--force` (or `--porcelain`) to skip the prompt for non-interactive or agent use. Without `--force`, an unanswered prompt times out after 60s (or errors immediately on a closed stdin) and reports that the note was NOT removed rather than hanging (exit `ExitValidation`).

### move

`move <src> <dst>` moves or renames a note to a new vault-relative path, rewriting every `[[wikilink]]` AND markdown-style `[text](path.md)` link across the vault that points at it. Wikilinks preserve `#heading`/`#^block`/`|alias`/`!`-embed suffixes; markdown links preserve the `[label]` text, any `#anchor`/`?query` suffix, and the `.md` extension; both preserve the author's bare-vs-path form. Markdown links to external URLs (http, mailto, etc.) and anchor-only targets are skipped, and links inside code are never touched. Percent-encoded markdown targets (the `[x](My%20Note.md)` form Obsidian generates for paths with spaces) are matched by their decoded form (after the anchor split, so a literal `%23` in a filename is never mis-split); a rewritten destination is percent-encoded as needed (spaces, `%`, `#`, `?`, parentheses), and a rewrite that would only respell the same destination is skipped as a no-op.

`--dry-run` previews the rename, the per-note rewrites, and the ambiguous links it would skip, without writing anything. Without `--force`, a move is refused when a bare `[[name]]` link is ambiguous (the name matches more than one note); `--force` rewrites only the unambiguous path-qualified links and leaves the bare ones. The target file is moved LAST, after referencing notes are rewritten, so a crash leaves links pointing at the still-present old name. JSON result: `{moved, rewritten, skipped_ambiguous, failed}`.

### rename

`rename <src> <newname>` is a thin wrapper over `move`: the destination is the source's folder plus `<newname>` (`.md` appended if omitted; path separators are rejected). Same `[[wikilink]]` and markdown-link rewriting and same `--dry-run`/`--force` behavior.

## Search & AI

### index

Rebuild the index. `--doc <path>` indexes a single doc; `--force-reembed` invalidates every stored embedding. The embed pass runs concurrently through a bounded worker pool; the cap is `ai.embed_concurrency`, defaulting to 4 for Bedrock (see the Embedding Concurrency notes in the project docs, and `2nb ai embed-probe` to find a safe cap for your account).

### search

Hybrid BM25 plus semantic search. Filters: `--type --status --tag --limit`. `--threshold` overrides the cosine cutoff; `--bm25-only` disables the vector channel.

### suggest-links

Suggest semantically related documents to link from a given document (`--limit`).

### suggest-target

`suggest-target <target>`: given ONE broken `[[wikilink]]` target, return ranked existing notes it might have meant. These are the "did you mean?" candidates behind the GUI link-fix sheet. Three tiers:

1. **Drift**: the same normalized-name index `repair-links` uses (case, hyphen, underscore, and whitespace folded), INCLUDING the ambiguous matches repair refuses to guess (via `polish.SuggestRepairTargets`).
2. **Semantic**: nearest notes by embedding (skipped, not errored, when no embedder is available).
3. **Keyword**: BM25 over the target words, so word-reorder or typo misses (`models-apresai` matching `apresai-*`) surface offline.

Candidates are ordered best-first by confidence tier then score, so `candidates[0]` is always the pipeline's best claim.

`--source <path>` names the note containing the broken link, excludes it from candidates, AND seeds context-aware search (the target plus surrounding prose for the semantic and BM25 tiers, not the bare target alone). The source is resolved leniently: one that no longer resolves (a just-deleted note) falls back to the cleaned raw path instead of erroring.

`--llm` optionally re-ranks the grounded shortlist with the active generation model when no candidate is already high-confidence, using the eval-selected `strict_plausibility` prompt ([link-prompt-eval.md](link-prompt-eval.md)). It is fail-closed, attaches a one-line `reason`, never invents paths, and caps confidence at medium so LLM picks stay recommendations, never silent auto-fixes: the 0.95-or-better auto-fix precision bar was measured and NOT met.

`--verdict` wraps the output in `{recommendation, llm, candidates}`: exactly one machine recommendation, `relink` to the top candidate when it is high (one-click tier) or medium (confirm tier), else `unlink`. An explicit model decline (`llm: "declined"`, measured trustworthy: 0 false promotions, 6 of 6 judge-confirmed) recommends `unlink` even over a medium candidate. Create-the-note is never machine-recommended.

The command is read-only. Default output is `[]SuggestLinkResult` (`[]`, never null), each candidate carrying a `confidence` of `high`, `medium`, or `low`: `high` when the normalized target is a whole-word subset of the candidate's title or basename AND the candidate is the sole match or dominates the runner-up score (so a GUI can offer it as a one-click / Fix-all fix); `medium` for one of those conditions or an LLM promotion; `low` otherwise. Pair with `relink --from <target> --to <pick>` to apply a suggestion, or `unlink --target <target>` to remove the link.

### polish

AI copy-edit (`--system`, `--max-tokens`) that returns original plus polished for diff preview.

- `--write` applies the polished body in place via the shared body-write path (opt-in; never the default), still emitting original plus polished for audit, and first writes a snapshot of the original under `.2ndbrain/recovery/polish/` so the change is reversible.
- `--links` also weaves grounded `[[wikilinks]]` to existing vault notes (semantic plus substring candidates, ambiguous titles dropped; a deterministic `StripInventedLinks` pass guarantees no link to a nonexistent note).
- `--repair-links` deterministically REPAIRS broken `[[wikilinks]]` to existing notes (`polish.RepairBrokenLinks`): a broken target is rewritten only when its normalized form (lower-cased, with hyphen and underscore folded to space and whitespace collapsed) maps to exactly one note via basename, title, or alias. The common case is case or separator drift, since the resolver is case- and separator-sensitive but Obsidian is not. Ambiguous or unmatched targets are left untouched and reported, never guessed; asset embeds are skipped; `#heading` and `|alias` suffixes are preserved; markdown occurrences get a path-based destination while wikilinks keep the pretty candidate (see repair-links). Repair runs before the copy-edit so the AI preserves the corrected links, and the snapshot is the true original so `--undo` reverts repairs and edits together.
- `--undo` restores the latest snapshot (with reindex and re-embed) and refuses if the file changed since polishing unless `--force`.

The default prompt is the LLM-judge-selected `polish.DefaultPolishSystem` (shared with the MCP `kb_polish` tool); [polish-prompt-eval.md](polish-prompt-eval.md) documents how it was chosen.

### ask

`ask <question>` is RAG Q&A: search the vault, generate an answer with sources. It feeds the full matching note(s) as parent-document context (windowed around the matched section only when a note exceeds the budget), bounded by `ai.rag_context_budget`/`ai.rag_note_budget`, so an answer deep in a long note is not head-truncated away. `--history <path|->` (JSON `[{role, content}]`, with `-` meaning stdin) makes it multi-turn: the history condenses follow-ups into standalone retrieval queries (reported as `rewritten_query` in `--json`) and grounds the answer.

### chat

Interactive multi-turn REPL over the same pipeline as `ask --history`. The conversation lives in-process only; no `--json`.

### eval

The vault search-quality scorecard. It generates a ground-truth Q&A set from the user's OWN notes (one question per substantial note, cached in the gitignored `.2ndbrain/eval/qa.json` so re-runs are free), then scores how well the current hybrid config ranks the right note: Recall@10, R@1, and MRR@10, via `internal/eval.RunRetrievalSweep` over a single `SweepConfig` built from the live `ai.*` settings.

A cost preview, `--cost-cap` (default $0.25), and a TTY confirm gate the one-time QA generation (the only cost; scoring is local). `--yes` skips the prompt, `--n` sizes the QA set (default 20), `--regenerate` refreshes it, and `--seed` fixes sampling. `--json` emits `EvalReport{n, k, config, recall_at_k, recall_at_1, mrr_at_k, qa_cached, generated_at}`. The command self-heals the vault `.gitignore` to exclude `.2ndbrain/eval/` (the cache embeds note bodies), even in a vault created before that entry existed. It measures the base hybrid path (no rerank).

Subcommands:

- `eval tune`: a threshold-by-weight sweep over the cached QA set (one embed batch, scored locally) that suggests `config set` commands when a config beats the current one beyond a noise margin. Suggest-only, never auto-applied; BM25-only winning is reported as a diagnosis.
- `eval answers`: the answer-quality jury. Real RAG answers over the cached QA set are graded 1-5 on correctness, completeness, and grounding, self-judged by the active generation model by default (labeled as relative signal) or by a `--judges` panel. Cost-gated by the shared `--cost-cap`. `--json` emits `AnswersReport`.
- `--estimate` (a persistent flag, so it also applies to `eval answers` and `eval tune`) prints the projected cost and exits without calling any model. `--json` emits `EvalEstimateReport{command, n, qa_cached, generation_usd, embed_usd, answers_usd, total_usd, cost_cap}`, the same estimator the interactive confirm gates on. It works on vaults the real run would refuse (no embeddings, no reachable provider), and it is what the macOS Quality pane prices its confirm from.

## Quality

### lint

`lint [glob]` validates schemas and checks for broken wikilinks. Each broken-wikilink finding carries additive `--json` fields (the `message` string is unchanged for back-compat):

- `target`: the raw authored target.
- `fix`: `drift` when exactly one existing note matches after case/separator folding; `ambiguous` when more than one matches; `missing` when none does, including path-qualified targets.
- `drift_target`: the canonical target when `fix` is `drift`.
- `candidates`: the repair index's matches when `fix` is `ambiguous`, each usable as a `relink --to` (when the pick resolves to a single note, relink derives the resolving markdown form from it, so a title candidate then works for markdown occurrences too), so a UI can offer the pick list without a suggest-target round-trip.

Together these let a UI show "N one-click fixable / M need a decision" before any click. Classification uses the same live-filesystem walk (`vault.CollectLiveDocs`) and repair index lint already builds.

### stale

List documents not modified within N days (`--since`).

### metrics

The vault performance observatory: reads the local `.2ndbrain/metrics.db` and reports the last index build (duration, docs/sec, throughput), live vault gauges (doc/chunk/embedded counts, coverage, index.db plus WAL size, stale count, embedding model and dims), token usage (input/output across the window), recent operations, and per-operation aggregates (count, avg, p50, avg docs per second, tokens in/out).

Metrics are recorded automatically (best-effort, never failing the op) by `index`, `index --doc`, `--force-reembed`, `search`, and `ask`. Token semantics: `ask` records the provider's ACTUAL generation usage when the provider reports it (Bedrock Converse, via the optional `ai.UsageGenerator`) plus a query-embed estimate; a provider that does not report usage (OpenRouter and Ollama, the non-`UsageGenerator` path) falls back to a chars/4 estimate, and `index`/`reembed` and `search` always estimate at chars/4 (Nova embeddings report no usage).

The parent default is `metrics show`; `metrics clear` wipes history. `--json` emits `{last_build, gauges, recent, aggregates, total_input_tokens, total_output_tokens}`, where each recent op and aggregate also carries `input_tokens`/`output_tokens` and `tokens_in`/`tokens_out`; `--limit` bounds the recent window.

### related

Find related docs via the link graph (`--depth`).

### backlinks

`backlinks <path>` lists resolved inbound links to a document: which docs link to it, with the source path and title and the link's heading, alias, and raw form.

### links

`links <path>` lists outbound links from a document, including unresolved ones (each carries a `resolved` bool), so it doubles as a per-file broken-link view.

### orphans

List documents with no resolved inbound link (nothing in the vault links to them).

### deadends

List documents with no resolved outbound link (they link to nothing real in the vault).

### unresolved

List every unresolved (broken) wikilink across the vault: each source doc path paired with the raw `[[target]]` that resolves to no note. The vault-wide complement to `links <path>`, which is per-file. `--total` prints only the count.

### repair-links

`repair-links <path>` deterministically repairs broken `[[wikilinks]]` in a note. It is the AI-free sibling of `polish --repair-links`: it runs `polish.RepairBrokenLinksFiltered` with no generation provider, works offline, and never touches prose. Candidates come from the same LIVE filesystem walk `lint` uses (`vault.CollectLiveDocs`), not the SQLite index, so repair, suggest-target tier 1, and relink's `--to` check can never disagree with lint over a note added or deleted since the last `2nb index`.

A broken target is canonicalized only when its normalized form (lower-cased, with hyphen and underscore folded to space and whitespace collapsed) maps to exactly one note by basename, title, or alias. The common case is case or separator drift, for example a spaced `[[Claude Code Skills Reference and Index]]` link matching the kebab `claude-code-skills-reference-and-index.md` basename. Ambiguous or unmatched targets are reported, never guessed. The emission is syntax-aware: a wikilink occurrence gets the pretty candidate (titles resolve at the resolver's title tier), while a markdown occurrence gets a PATH-based destination derived from the chosen note (bare basename when it uniquely resolves, else the full vault-relative path), because a title+`.md` markdown destination resolves through no tier. (One corner keeps today's behavior: a candidate whose title byte-collides with two or more notes' basenames cannot be resolved to a single path, so the markdown occurrence keeps its authored form.)

`--target <raw>` (repeatable) scopes the repair to specific authored targets (the `T` from `broken wikilink: [[T]]`), so a per-finding GUI button fixes exactly the clicked link. Previews by default; `--write` applies in place and snapshots the original so `polish <path> --undo` reverts it (the shared snapshot slot). Emits the `PolishResult` shape (`provider: "repair-links"`); rejects read-only `.canvas`/`.base` files.

### relink

`relink <path>` repoints a broken link (wikilink or markdown) to a chosen EXISTING note: it rewrites every link whose authored target equals `--from` to point at `--to` instead, preserving any `#heading`/`#^block`/`|alias` suffix (and, for wikilinks, the author's bare-vs-path form). When `--to` resolves to a note on the live filesystem, markdown occurrences get a PATH-based destination derived from that note (bare basename when it uniquely resolves, else the full path), since a title-form markdown destination would resolve through no tier; an unresolvable `--to` falls back to the verbatim rewrite and warns. This is the "apply a Did-you-mean suggestion" action, paired with `suggest-target`. Matching is EXACT (case- and separator-sensitive), so it only touches the named link. Previews by default; `--write` applies and snapshots (reversible via `polish <path> --undo`). Emits the `PolishResult` shape (`provider: "relink"`); rejects read-only `.canvas`/`.base` files.

### unlink

`unlink <path>` removes a broken `[[wikilink]]` while keeping its visible text (`document.UnlinkWikiLink`): `[[083477d]]` becomes `083477d`, `[[page|the page]]` becomes `the page`, `[[note#Setup]]` becomes `note`. This is the "remove the link, keep the words" resolution for a target that names no real note (a stray id, an abbreviation, an external ref). Matching is EXACT (case- and separator-sensitive) and scoped to `--target`; embeds (`![[...]]`) and links inside code are never touched. Previews by default; `--write` applies and snapshots (reversible via `polish <path> --undo`). Emits the `PolishResult` shape (`provider: "unlink"`, `new_target` empty); rejects read-only `.canvas`/`.base` files.

### graph

Output the link graph as a JSON adjacency list.

### outline

`outline <path>` prints the heading tree of a document (heading path, level, line span). It shares `document.BuildOutline` with the MCP `kb_structure` tool.

### wordcount

`wordcount <path>` reports word, character, and heading counts over the indexable body (comments stripped). Alias: `wc`.

### folders

List folders (directory prefixes of `documents.path`) with doc counts; root docs bucket under `(root)`.

### aliases

List frontmatter aliases mapped to their document (alias to path and title).

## Integration

### export-context

Generate a CLAUDE.md-compatible context bundle (`--types --status --limit`).

### git activity

Recent commits touching vault files (`--since 7d`, `--json`).

### git show

`git show <hash>` prints full commit detail: metadata, stats, per-file diffs.

### git diff

`git diff <path>` prints the unified diff of a file vs HEAD.

### git status

Uncommitted and untracked files in the vault.

## Import / Export

### import-obsidian

Import an Obsidian vault: generates a UUID `id` for docs missing one, sets `type: note` when absent, normalizes inline `#tag` tags into the frontmatter `tags` array, preserves all existing frontmatter, maps Obsidian `aliases` into wikilink resolution, preserves `.canvas` files, initializes `.2ndbrain/`, and builds the index.

### export-obsidian

Export to Obsidian format: copies markdown, creates `.obsidian/` with a default config, and converts UUID-based references to filename-based wikilinks. `--strip-ids` removes the `id` and `type` frontmatter fields.

### migrate

Migrate a legacy 2ndbrain vault to the Obsidian-native format (the current index schema; v4 as of 0.19.x); `--dry-run` previews without modifying. Non-mutating: source markdown is never changed.
## MCP & Agent Integration

### mcp-server

Starts the MCP server on stdio transport. The server exits with its client (a parent-death watchdog reaps orphans promptly); the idle self-exit is opt-in and OFF by default (`--idle-timeout <dur>` or `$2NB_MCP_IDLE_TIMEOUT`; `0` means never). Lifecycle detail, sidecar status files, and metrics recording live in [mcp-integration.md](mcp-integration.md) (Server internals).

### mcp-setup

Shows MCP setup instructions for all AI tools.

### mcp status

Lists live MCP server processes and recent tool invocations (`--json`).

### mcp reap

Terminates stale or orphaned `mcp-server` processes for this vault; a rarely-needed backstop now that the parent-death watchdog reaps orphans promptly. `--older-than` (default 6h), `--dry-run`; JSON: `{reaped[], skipped[], threshold, dry_run}`. Safety detail (SIGTERM-only, PID-reuse guard): [mcp-integration.md](mcp-integration.md) (Server internals).

### mcp configured

Reports whether the 2ndbrain MCP server is configured in the AI client config for this vault (`--json`). This is the durable "is it set up?" check, unlike `mcp status`, which reports "is it running right now?". `--client <name>` checks one client (default `claude-code`); `--all` returns a per-client array across `claude-code`/`claude-desktop`/`warp`/`agents`/`codex` (claude-desktop, warp, and agents read their flat `mcpServers` JSON; codex is a dependency-free `[mcp_servers.2ndbrain]` presence scan of `~/.codex/config.toml`). Output is always a JSON array; the default stays a slice of one claude-code entry.

### mcp doctor

End-to-end self-test of the MCP engine, run in-process (`internal/mcp.Engine` over the same `mcpToolRegistrations` the stdio server serves): it counts tools (22), runs real `kb_info`/`kb_list`/`kb_search` round-trips, and folds in AI readiness, `mcp configured`, the `instructions` string, and reliability signals (WAL size, alive and stale server counts). It proves the engine works, not just that it is configured. Engine checks are hard failures (exit 2); readiness, wiring, and reliability checks are warnings.

The `kb_search` check inspects the payload, not just the error: `retrieve` degrades to BM25 whenever the vector channel is unusable (expired credentials, dimension break) and reports that only via the per-result `search_mode` field, so a check that read the error alone passed cheerfully on a vault whose semantic search was dead. A vault that has embeddings and still answers in `keyword` mode is a hard failure; a vault with zero embeddings is a warning pointing at `2nb index`; an empty result set is not judged either way.

JSON reuses `config doctor`'s `DoctorCheck` shape (`checks[]`) plus top-level `tool_count`/`configured`/`wal_bytes`/`stale_servers`/`instructions_present` and related fields. The run is bounded by `mcp.DoctorExercisedBudget()` (the exercised tools' own per-tool timeouts plus slack, replacing a flat 15s cap that made the 60s search budget dead code), so deadlines here bound hangs, and an expired check renders as an explicit inconclusive-timeout, never as a subsystem failure.

### mcp install / mcp uninstall

Writes or removes the 2ndbrain server entry in an AI client config (the write-side inverse of `mcp configured`). Idempotent, backup-first (`<config>.bak`), and preserves every unrelated key: it parses the config to `map[string]json.RawMessage` and mutates only the `mcpServers` sub-map, so `numStartups`, `oauthAccount`, your other servers, and everything else survive byte-for-byte; a malformed config is refused, not clobbered. Flags: `--scope user|project`, `--command <path>` (the app passes its bundled CLI), `--client claude-code|claude-desktop|warp|agents|codex|all`, `--dry-run`.

Per-client behavior:

- `warp` and `agents` write `~/.warp/.mcp.json` and the cross-tool `~/.agents/.mcp.json` (which Warp also auto-reads), pinning via `--vault` plus `working_directory`.
- `claude-desktop` writes `~/Library/Application Support/Claude/claude_desktop_config.json` with an absolute `2nb` path and no `cwd`/`working_directory`/`url` (it is a GUI app, and a `url` field corrupts that file); restart Claude Desktop to apply.
- `codex` shells `codex mcp add` so Codex owns its `~/.codex/config.toml` (prints the command plus a TOML snippet if the `codex` CLI is absent).
- `all` configures every client (one client's failure is captured, not fatal).

`claude-desktop` and `codex` are user-scope only. JSON: `{client, config_path, configured, changed, backup_path, server_key, scope, instructions?, error?}`.

### setup

The one-command front door: installs the 2nb skill, the MCP server entry, and the global-instructions block for an AI client (`--client claude-code|claude-desktop|warp|agents|codex`) or `--all`, each step idempotent and backup-safe. Flags: `--scope user|project`, `--command`, `--dry-run`, `--force`, `--json` (an array of per-client `{client, skill_path, skill_backup, mcp_config_path, mcp_backup, configured, instructions, instructions_file_path, instructions_written, error}`). Claude Desktop shares Claude Code's `~/.claude/skills` (MCP-only) and its `~/.claude/CLAUDE.md` instructions; Codex MCP is wired via `codex mcp add`. Refuses to stamp the repo's committed `.agents`/`.warp`/`.claude` skill mirrors when run at project scope from the 2ndbrain source tree.

### instructions install / configured / uninstall

Manages a small managed "2ndbrain" reference block in an AI client's global agent memory file (`~/.claude/CLAUDE.md`): the always-loaded, lightweight complement to the installable skill. The block is delimited by `<!-- BEGIN/END 2nb managed instructions -->` markers with a `version`/`sha` stamp, so it updates in place, is idempotent (no `.bak` churn on unchanged content), preserves surrounding user content, and removes cleanly.

- `install` refuses a hand-edited block without `--force`.
- `configured` (alias `status`; `--all` or `--client`) reports `{client, file_path, installed, up_to_date, modified, installed_version}` as a JSON array.
- `uninstall` strips the block.

Supported clients: `claude-code` and `claude-desktop` (both `~/.claude/CLAUDE.md`) and `codex` (`~/.codex/AGENTS.md`, the Codex CLI global memory file). warp and agents remain deferred: Warp Drive rules are cloud-managed with no on-disk global file, and the cross-tool AGENTS.md standard is project-scoped. Also run by `2nb setup`. Canonical block: `cli/internal/instructions/content/instructions.md`.

### plugin status

Reports the installed Obsidian plugin version vs this CLI (`--json`).

### plugin install

Installs or updates the Obsidian plugin: downloads `manifest.json`/`main.js`/`styles.css` from the latest GitHub release into `<vault>/.obsidian/plugins/obsidian-2ndbrain/` (the manifest is written last, so a partial install never looks complete). Alias: `plugin update`. No-downgrade guard: the command refuses (no write) when the installed plugin is newer than the latest release, for example during a prerelease or promotion lag, so an install can never silently downgrade; override with `--force`. Enabling the plugin in Obsidian stays manual (Obsidian has no API for it).

### skills list / install / uninstall / show

Generates SKILL.md for AI coding agents (`--user`, `--all`, `--force`). Supported agents include `claude-code` (which also serves Claude Desktop, since Claude Desktop reads the same `~/.claude/skills`), `cursor`, `windsurf`, `github-copilot`, `kiro`, `cline`, `roo-code`, `junie`, `warp` (`~/.warp/skills/2nb/SKILL.md`; Warp also reads `~/.claude/skills`), `codex` (`~/.codex/skills/2nb/SKILL.md`; Codex also reads `~/.agents/skills`), and the cross-tool `agents` (`.agents/skills/2nb/SKILL.md`, Warp's recommended primary, also honored by other agents). A force-overwrite of a differing SKILL.md backs it up to `SKILL.md.bak` first; installs are version-stamped; `skills list` auto-refreshes a stale, unmodified managed copy (no `.bak`), so a `brew upgrade` keeps the skill current. A project-scope `--all` from the 2ndbrain source tree skips the committed mirror slugs (use `make sync-skills` for those).

### skills doctor [slug]

Verifies that an agent's skill works (the slug defaults to `claude-code`): the SKILL.md is installed, non-empty, and has frontmatter, and the `2nb` it shells to resolves on PATH (`exec.LookPath`, the way the agent's shell finds it, deliberately not `os.Executable`) and runs `--version`. It honestly reports "installed and dependencies resolve", not "the agent invoked it". The on-PATH check is the common real failure (a cask bump leaves a stale terminal `2nb`). It also reports SKILL.md freshness (`Freshness{stamped, installed_version, up_to_date, modified}`): installs are stamped with `x-2nb-version`/`x-2nb-content-sha`, so a stale managed copy is flagged "out of date"; `skills list` self-heals an unmodified, stamped, out-of-date managed install in place (it never clobbers a hand-edited one). JSON embeds `InstallStatus` plus `binary_ok`/`binary_version`/`parses`/`file_nonempty`/`self_path`/`freshness` plus `checks[]`.

## Models & AI

### ai status

Reports provider, models, readiness, embedding count, and vault portability state with one-line fix hints. Includes a Model access line (additive `model_access` in `--json`: `{verified, access_denied, other_failures, last_verified_at}`) summarizing the active provider's persisted `models verify`/`models test --save` outcomes.

### ai embed

Generates an embedding vector for the given text (debug).

### ai embed-probe

Finds a safe `ai.embed_concurrency` for the account by ramping it: embeds a sample of the vault's chunks (discarded, never stored) at escalating concurrency `--levels` (default `4,8,16,32`), measures per-level throughput and errors, and recommends the lowest level reaching at least 90% of peak throughput before throttling (the first level that errors caps the scan). AWS does not publish per-account Bedrock RPM quotas, so this discovers the real ceiling empirically. Flags: `--sample` (default 64), `--yes` (skip the cost confirm), `--json`. Prints the `config set ai.embed_concurrency N` command to apply the recommendation.

### ai setup

Multi-provider setup wizard (`--provider`, `--embedding-model`, `--generation-model`; providers: bedrock, openrouter, ollama, llama-local). A model that passes its probe is persisted to the per-vault user catalog as `tier=user_verified`, so it shows up in `2nb models list` afterward (failed probes are never persisted). The `llama-local` branch enables the provider, defaults to EmbeddingGemma plus Gemma 4 E2B, and offers to download each missing model.

### ai local

Checks local AI readiness (Ollama, models, disk, RAM, embeddings).

### ai engine

Manages the bundled llama.cpp engine (the `llama-local` provider):

- `pull <id>...` downloads and sha256-verifies GGUF weights into `~/Library/Caches/2nb/models/` (progress bar; `--json` emits line-delimited `{model,status,done,total}` progress for the GUI; a 60s idle watchdog aborts a stalled transfer).
- `rm <id>...` (aliases `remove`/`delete`) deletes cached weights to free disk (`--json` returns `{removed[], freed_bytes}`; it refuses a path-separator id so it can never escape the cache).
- `serve` runs `llama-server` for the configured roles.
- `install`/`bootout` wire and remove the launchd agent.

Model pins and licenses live in `internal/llama/models.go`.

### models list

The verified catalog. Flags: `--type`, `--free`, `--discover`, `--status`, `--provider`, `--promote`, `--scope`, `--enabled-only`, `--recommended`, `--working-set`, `--sort best`.

- `--discover --promote` tests unverified models concurrently and adds the passing ones.
- `--discover` enumerates BOTH Bedrock planes: the classic control plane (ListInferenceProfiles plus ListFoundationModels) and the mantle plane's own `/v1/models` listing per documented mantle region, merging unverified generation rows that carry `invoke_strategy` plus a listing-region pin. On an exact-id collision across planes, the classic row wins.
- `--enabled-only` drops user-disabled models (selection dropdowns pass this; CLI use does not).
- `--recommended` shows only the curated short list (`ModelInfo.Recommended`, add-only through the user-catalog merge).
- `--working-set` shows only the working set: the models this account has PROVEN it can invoke, from the `working` field on every `--json` row (`ModelInfo.Working`, derived at list time like `vendor`/`compatible`, never persisted to `models.yaml`; always serialized both true and false, matching `compatible`). A model is working when it carries a passing probe (`tested_at` set, `test_error` empty) and is neither explicitly disabled nor statically incompatible. A builtin `tier: verified` entry alone is deliberately NOT enough, since verified means 2nb has a harness for the model, not that AWS has entitled this account (the staged frontier rollout lists models that still 403). Untested active embedding and generation models are members, so a picker is never empty on a freshly bound vault; a FAILED probe on the active slot is `working:false` (the GUI keeps it selectable as current, separately from working).
- The BENCH column renders the latest benchmark summary (for example `q=0.87 412ms`; retrieval quality exists only for embedding models), and `--sort best` ranks by measured evidence (bench quality, then tested-passing, recommended, tier, latency; JSON follows the sort), so bench data influences the visible ranking without changing default-model selection.
- The STATE column renders curation plus per-account test state (`★`, `ok 3d`, or the classified `test_error_code`).

Builtin Bedrock Anthropic line: Haiku 4.5 (the tested default) plus Sonnet 4.6, Sonnet 5, Opus 4.6, and Opus 4.8. Sonnet 5 and Opus 4.8 ship unpinned (live offer-file pricing) with a staged-rollout note; Opus 4.7 and Fable 5 are deliberately non-curated (discovery still surfaces them). `us.xai.grok-4.6` is a builtin classic-plane entry (Converse via the cross-region profile, 500K context, geo pricing pinned; the first xAI model on the classic plane, 2026-08-19, and per-account gated like the Anthropic frontier; the probe budget covers its always-on reasoning, which bills roughly 180 output tokens for a trivial answer). Catalog freshness is guarded by cred-gated tests (`TestBuiltinBedrockAnthropicModelsStillListed`, `TestLivePricing_ResolvesUnpinnedBuiltinAnthropic`); run them before releases.

### models test

Smoke-tests a model by id. `--save` writes to the user catalog regardless of pass or fail: success sets `tier=user_verified`; failure records `test_error` plus a classified `test_error_code`. Failures are classified via `ai.ClassifyProbeError` into a stable code vocabulary (`access_denied`, `bad_credentials`, `throttled`, `not_found`, `provider_unreachable`, `invalid_request`, `incompatible`, `timeout`, `unknown`) with a remediation hint (`--json`: `code`/`remediation`; human output: `cause:`/`fix:` lines).

The `access_denied` code is how the AWS staged frontier-rollout gate surfaces: a runtime 403 on the classic bedrock-runtime plane, or a runtime 401 disambiguated by the response body's `error.code` on the mantle plane, on a model the console lists as available. Only a real invoke probe detects it; availability APIs report AUTHORIZED regardless.

The command is region-aware like `verify`: it rides the same included-region fallback, and a model carrying a catalog Region pin always re-checks the primary region first (a primary pass clears the pin, a self-heal). Default `--scope vault`. The probe's output budget is 1024 tokens (`probeGenMaxTokens`), so cost estimates scale from that, not from a real answer's typical length. The probe deadline is strategy-aware (`ai.ProbeDeadline`, `cli/internal/ai/timeouts.go`): it contains the resolved route's full transport worst case (attempts x per-attempt timeout, plus backoff) plus slack, so a slow cold-starting reasoning model is never failed, only a hang. The old flat 30s cap sat inside the mantle client's own retry budget and timed out working models; `TestTimeoutBudgetsNested` pins the nesting.

### models verify [ids...]

Batch access probe: runs a real test probe per candidate and persists EVERY result, pass and fail, with `test_error_code`, so `models list`'s STATE column and `ai status`'s Model access summary reflect what THIS account can invoke.

**One route per model.** Candidates are catalog ROWS, and a row is a route, so a model served on both planes or in three regions would otherwise cost three BILLED probes where one was wanted (enough to trip the default `--cost-cap`). Verify collapses each model to its best route via `PreferRoutes` (configured route, then last known good, then primary region). `--all-routes` probes the whole matrix instead, for when the best route fails and you need to know which endpoints your account can actually reach. Probing the best route is safe in a way that choosing an INVOKE route is not: a wrong guess costs one probe and reports it.

**Candidates.** The default set is recommended ∪ active models on providers whose credentials resolve. `--provider`/`--vendor`/`--recommended` narrow it, `--all` probes the whole catalog, and explicit IDs win. `--enabled-only` restricts candidates to effectively-enabled models (post vendor policy, the same filter as `models list --enabled-only`; explicit IDs still win), so "validate what I just enabled" is exact. Rerank and statically incompatible entries are skipped.

**Discovery.** `--discover` (default off, so the CLI's candidate set is unchanged) widens the pool to the vendor-discovered half of `BuildModelList`: the only place a model the user enabled through a vendor policy lives until something probes it, since a policy pre-enables future discoveries. It counts as a selector (like `--all`/`--vendor`) because every discovered model is by definition neither recommended nor active, and the default narrowing would otherwise make the flag a silent no-op. Bedrock discovery is read through a 24h disk cache (`internal/ai/discovery_cache.go`, XDG cache dir, keyed by region plus profile, stale entry served on live failure), so a repeating GUI validation pass does not re-walk the control plane.

`--discover` also covers the mantle plane's `/v1/models` listing (cached as `bedrock-mantle-<region>-<profile>.json`): discovered mantle rows carry routing hints, the probe dispatches over the mantle plane via them (catalog resolution still wins), and a result, pass AND fail, persists `invoke_strategy` plus region into the user catalog (`adoptCandidateRouting`, applied after `preserveRoutingFields`, so an existing user entry's authored fields beat the hint while its empty fields adopt it), after which every invoke routes normally. Explicit mantle-discovered IDs need `--discover` too (it widens the lookup pool; without it a pinned discovered id falls to the hint-less fallback and classic-probes). The mantle builtins (`openai.gpt-5.5`, `xai.grok-4.3`) are candidates without it: they are catalog entries, invisible to `ListFoundationModels`.

**Cost gate.** `--cost-cap` (default $0.50) plus a y/N confirm; a declined confirm exits non-zero; pass `--yes` for non-interactive runs. Estimates scale from the probe's 1024-token output budget, so a wide `--discover` run against many candidates may deliberately trip the default cap (that is the cap working, not a bug).

**Output.** Grouped by vendor with one remediation per distinct code; `--json` returns `{probe, results[], summary{ok, access_denied, ...}, saved_scope}`. `--events` instead streams line-delimited JSON progress on stdout for the GUI: `start` with the total plus `estimated_usd`, one `result` per probe, and `done` with the total plus summary plus `saved_scope` (the zero-candidate `done` omits `summary`). `--events` requires `--yes`, is mutually exclusive with `--json` (the envelope stays byte-compatible), and a zero-candidate set emits `start(total=0)` plus `done` rather than erroring.

This is the per-account complement to the catalog: on a staged-rollout-gated account the Anthropic line reports `access_denied` for Sonnet 5 and Opus 4.8; on a provisioned account they pass.

**Multi-region.** With additional included regions configured (`config bedrock --set --regions`), a classic-Bedrock probe that fails with a region-shaped code (`not_found`, `invalid_request`, `access_denied`; Bedrock entitlement is per-region) retries sequentially in the next included region, stopping at the first pass. It is never a full model x region matrix, and refused probes bill nothing, so the cost gate is unchanged. Each result carries the probed `region` (additive JSON; also on the `--events` `start` header as `regions`). A pass in a non-primary region persists that region onto the user-catalog entry so future invokes route there (`ai.EffectiveBedrockRegion`; generation and embedding honor catalog Region pins the way mantle and rerank always did), while a pass in the primary region clears any stale pin (self-heal). Mantle models never fan out (each is endpoint-pinned per model).

**Wall clock.** `--max-duration` (default 30m; `0` disables) is the whole-run wall clock, the runaway-cost bound: individual probes keep their strategy-aware transport-derived deadlines (a slow model is never failed, only a hang); the flag only stops a runaway batch.

### models add

Adds or updates a model by id. The default scope is the per-vault `.2ndbrain/models.yaml`; `--scope global` writes `~/.config/2nb/models.yaml`. Updates merge: `Enabled`, `TestedAt`, `TestLatencyMs`, and `Benchmark` are preserved unless explicitly re-set. `--similarity-threshold` is embedding-only; `--price-request` is for per-request priced models.

### models remove

Removes a model from the user catalog (`--provider`, `--scope`).

### models enable

Marks a model enabled. With `--vendor <name>` (for example `anthropic`/`amazon`/`google`) it toggles every model from that vendor, which is the GUI's bulk toggle. `--vendor` and an explicit `<id>` are mutually exclusive.

### models disable

Hides a model from selection dropdowns (it is still listed by `models list`). Same `--vendor` bulk mode as `models enable`.

### models enable-state

Tri-state pointer for one model: `--state default|enabled|disabled`. `default` clears the override so tier defaults apply. Used by the GUI Enable State menu.

### models policy set / show / clear

Persistent enable-only vendor policy per provider (`models policy set --provider bedrock --enable-only anthropic,deepseek`): at list time, every nil-tri-state model on the policied provider gets an explicit verdict. Vendors in the list stay enabled; everything else is disabled, INCLUDING future discoveries, which arrive pre-disabled. The policy is applied inside `BuildModelList` (one hook), so `--enabled-only` dropdowns, the Hub catalog, and `models verify` all honor it.

Storage: a dedicated `models-policy.yaml` (vault `.2ndbrain/` or `~/.config/2nb/`; a vault policy fully overrides global per provider). It never lives inside `models.yaml`/`config.yaml`, which older CLIs would round-trip and silently strip. Precedence: explicit per-model Enabled beats policy beats tier default. The ACTIVE embedding, generation, and rerank models are never policy-disabled (they are kept enabled, with a warning).

`set` clears same-scope per-model Enabled overrides by default so a stale bulk-disable cannot shadow the policy (`--keep-model-overrides` opts out; other-scope overrides are reported, never touched), validates vendor slugs against the merged catalog plus the static Bedrock vendor vocabulary (an unknown slug exits 2 listing the known slugs), and `--dry-run` previews the per-vendor effect without writing.

JSON: `{provider, mode, vendors, known_vendors, scope, dry_run, effect{enabled, disabled, overridden, by_vendor}, warnings, cleared_model_overrides}` (`show` emits an array). `known_vendors` is the checkbox vocabulary a UI renders from: every slug the policy MAY name (merged-catalog vendors ∪ the provider's static vocabulary), which is why xAI and OpenAI are offerable on an account whose catalog shows no Grok or GPT row. A policy states intent about FUTURE models, so a vendor with zero rows today must still be checkable. `known_vendors` is reported on both `set` and `show`, and on a policy whose mode this binary does not support (that is precisely when the user needs to replace it). `show --json` emits a synthetic per-provider row with `known_vendors` and empty `vendors`/`mode` (not `enable_only`) for EVERY provider missing a configured policy row, not only when zero policies exist, so a policy on one provider can never suppress another provider's checkbox vocabulary (a UI renders the full slug set without hardcoding); human output stays "No vendor policies configured." Bare `models policy` is `show`.

Persistence paths that start from a `BuildModelList`-derived entry (`models test --save`, `models verify`, bench summaries) route `Enabled` through `preserveScopeEnabled`, so a policy verdict is never frozen into the user catalog as a per-model override.

### models cost-preview [ids...]

Estimates USD cost across one or more models. `--probe test|bench_embed|bench_gen|bench_rag|retrieval`. `--discover` resolves IDs against the vendor-discovered pool too: this is needed for mantle-plane discoveries, which have no catalog entry until verified. Without it, a discovered id warns "unknown model id" and contributes $0, under-stating the spend a matching `verify --discover` run is about to make. Discovery is read through the 24h cache, so a cold cache triggers one discovery walk. Otherwise the command is fully local: no API calls, and no model is ever invoked.

### models discover

Discovery as a verb: reports the vendor-discovered pool (both Bedrock planes plus the other providers' listings) with per-source cache ages and a NEW/GONE diff.

`models list` carries a ROUTE column (`classic us-east-1`), because a model served in three regions is three rows and would otherwise print three byte-identical lines.

**Rows are ROUTES, one per `(plane, region)`.** Both planes are walked across `us-east-1`, `us-east-2`, and `us-west-2`, so a model served in three regions is three rows and the ROUTE column names each one (`classic us-east-1`). Bedrock entitlement is per-region, so those endpoints succeed and fail independently; collapsing them by id (as this did before) silently discarded real routes. A failure in a region the user has not configured via `config bedrock --regions` is logged rather than warned, because the GONE shield keys off discovery warnings and a permanent warning would permanently disable GONE detection. The NEW/GONE diff stays keyed by MODEL, so a newly-listed model is announced once, not once per route. Ages read the discovery cache files' mtime against the 24h TTL ("classic us-east-1: 3h ago", "mantle us-west-2: stale (26h)"); a mantle region with no token and no cached file is omitted, not reported as "missing". The default is the cached read-through; `--refresh` deletes the cache files first (`ai.InvalidateDiscoveryCache`), so the same read-through IS a live walk that re-warms them.

The diff baseline is a dedicated machine-local `discovery-seen-bedrock-<profile>.json` in the XDG discovery cache dir: deliberately NOT the vault (a synced sidecar would mis-badge models on another machine, the same rationale as the GUI's UserDefaults snapshot) and not the cache files (`--refresh` deletes those). The first run seeds silently: the pool is reported, nothing is badged NEW, and the baseline saves. The baseline updates only after a successful listing; a failed source's keys are carried forward (unknown, not gone), and a model adopted into the catalog is never badged GONE (it graduated, it did not vanish).

`--add <id>` (repeatable; a bare id that two providers both list is refused, qualify it as `provider|id`) persists a discovered row into the user catalog WITH its routing via `ai.AdoptRoutingHints` (the tier stays unverified; no Enabled override is frozen in). This is the durable fix for "explicit mantle ids silently classic-probe": after `--add`, a bare `models test <id>` and every invoke route over the listed plane with no `--discover` flag. `--validate` probes the added ids immediately with verify's exact cost gate (estimate, `--cost-cap` default $0.50, y/N confirm, `--yes` for non-interactive) and persists pass AND fail like `models verify`. `--scope vault|global`. `--json` returns `{sources, models, new, gone, first_run?, refreshed?, added?, results?, warnings?}`.

### models wizard

Interactive end-to-end flow: providers → discover → easy-mode → cost preview → test → save. `--json` emits line-delimited events; the wizard aborts non-interactively if the estimated cost exceeds `--cost-cap` (default $0.50, sized so the honest 1024-token test-probe estimates never refuse a normal easy-mode run). `--set-active` writes the chosen embedding and generation models (and their provider) into the vault config via the same path `config set` uses (provider validation, disabled-flag clear, `ai.dimensions` resync), emitting a `set_active` event. An interactive run without the flag offers a y/N prompt (defaulting to no); a non-interactive run does nothing unless `--set-active` is passed.

### models bench

Benchmarks a model against the vault. `--probe embed|generate|retrieval|search|rag`. The `retrieval` and `search` probes are zero-API (they score stored embeddings and the local index). History lives in `.2ndbrain/bench.db`; a per-model summary is written at `--summary-scope` (default `global`) and surfaced in `models list`'s BENCH column and `--sort best`. `--json` emits line-delimited events.

### models bench fav / unfav / favs / history / compare

Manages benchmark favorites and views history. `compare` reports the latest run per (model, probe) pair; its `--json` is the same `[]bench.Run` shape as `history --json` (the macOS compare matrix decodes it), while the human view groups by probe.

### models calibrate

Samples the baseline cosine distribution and recommends a similarity threshold. Flags: `--samples`, `--save`, `--scope`, `--seed`.

## Configuration

### config show / get / set / set-key / bedrock / doctor

Reads and writes config.

**Model slots name a ROUTE.** Each of the three slots carries a plane and a region alongside its model: `ai.{generation,embedding}_{plane,region}` and `ai.rerank.{plane,region}`. `config set ai.<slot>_model` accepts either a bare id or a full route (`xai.grok-4.6@mantle/us-west-2`) and writes all three keys as one unit, so a slot is never left half-routed.

- A bare id whose model has exactly one route resolves, and the file still ends up explicit.
- A bare id with SEVERAL routes is **refused** with `ExitValidation`, printing each qualified form to paste; nothing is written. Silently picking one endpoint is how a mantle-only model ended up dispatched over classic Converse.
- An unknown model is still settable: a model can exist before 2nb's catalog knows it.
- Plane is validated against the closed set `{classic, mantle}`, and rejected outright for the embedding and rerank slots, where the mantle plane has no client. Region must be a bare label; that check is a security control, since the region is interpolated into the mantle host the bearer token is sent to.

At invoke time nothing is inferred: a slot whose model has several routes and names none of them is refused with the `config set` commands to run, rather than falling through to classic Converse. See [ai-providers.md](ai-providers.md#model-routes-provider-id-plane-region).

`config bedrock` shows and sets the machine-local `~/.config/2nb/bedrock.json` (no vault needed):

- `--json` redacts the token but carries a `token_suffix` (the last 4 characters, suppressed entirely for a token under 12 characters), so a GUI can render a masked value the user can actually recognize and tell two keys apart, standard BYOK practice.
- `--set --regions us-west-2,us-east-2` and `--clear-regions` manage the additional included regions for multi-region verify and discovery, validated as bare region labels.
- `--set --prefer-stored-token` and `--no-prefer-stored-token` toggle the saved-key-wins precedence: for 2nb only, the stored key (file, then Keychain) wins over the `AWS_BEARER_TOKEN_BEDROCK` env var, which keeps serving every other tool in the shell. The setting is reported as `prefer_stored_token` in the JSON, and it suppresses `env_overrides_stored`: the divergence then renders as an informational note instead of the split-brain warning, in human output and in doctor tier 1 alike.
- Status JSON also carries `regions`; `token_updated_at`, stamped only on token writes, so a UI can flag model-access verdicts that predate the current key; and `env_overrides_stored` plus `stored_token_suffix`, the split-brain warning that `AWS_BEARER_TOKEN_BEDROCK` in the environment overrides a DIFFERENT saved key (the app uses the file while every terminal and MCP process keeps the env token). Human output prints an explicit WARNING, and `doctor` tier 1 carries the same zero-network warn check.

`config set-key <provider>` stores an API key (bedrock writes the file and, on macOS, the Keychain). `config get --effective` resolves `ai.similarity_threshold` through its full chain (vault, then calibration, then model, then default). `config set ai.reasoning_effort none|low|medium|high` (empty clears) is the thinking depth for mantle generation models; the Models tab Thinking picker writes it only on user change. `config set ai.dimensions <N>` validates `N` against the active model's declared `SupportedDimensions` set (`ai.SupportedDimensionsFor`) and refuses a width the provider would reject at embed time; a model that declares no set is unconstrained. Changing the dimension needs `2nb index --force-reembed` (content hashes are unchanged, so a bare reindex will not re-embed).

`config doctor` diagnoses AI-config problems (provider known and enabled, no orphaned model slot, `ai.dimensions` matching the model, DB embeddings matching the selection, the threshold resolving) with fix hints. Genuine config defects fail (exit 2); an environmental condition like an unreachable provider is a non-failing warning, so `config doctor` stays usable offline and in CI.

### completion

Emits a shell completion script (`zsh|bash|fish|powershell`). Shell completion dispatches to the built binary, so it stays fresh across upgrades. Homebrew installs the scripts via GoReleaser; non-brew users run `completion install`.

### completion install

Installs zsh completion idempotently into an existing completion dir referenced from `.zshrc` (or `~/.zsh/completions/_2nb`, or `--dir`). compinit runs unconditionally, and the command warns when multiple `2nb` binaries are on PATH.

## Diagnostics & Updates

### doctor (alias: verify)

The one command that proves the setup actually works, and the only one that exits non-zero when it does not. It runs a real end-to-end self-test (`internal/cli/doctor_selftest.go`) in two tiers, then prints the version-parity report below it.

**Tier 1 needs no vault.** It calls the ACTIVE embedding and generation models for real via `ai.TestProbeModel` and derives a plain credential verdict (`accepted`/`rejected`/`unreachable`/`unknown`). This is what makes it usable during first-run setup from the macOS Settings window, the one surface that works with no vault bound.

**Tier 2 needs a vault** and folds in `config doctor` plus `mcp doctor`'s checks wholesale (so they can never drift), including the retrieval assertion. Tier 2 is skipped, never failed, when no 2nb-indexed vault resolves.

**The side-effect guarantee is precise, not blanket.** Tier 1 opens no vault at all (it resolves the root via `resolveVaultRootReadOnly`, never `vault.Open`), and tier 2 runs only when a `.2ndbrain/` sidecar already exists, so a diagnostic can never mint a sidecar or a `.gitignore` line in a vault 2nb has not indexed, which is the guarantee the Settings-window button needs. It is NOT read-only beyond that: tier 2's `vault.Open` still self-heals a missing or corrupt `config.yaml` and creates an empty `index.db` if one is gone (`cli/internal/vault/vault.go:49-98`), exactly as every other vault command does.

**Each tier is bounded separately**, not by one shared deadline: `doctorModelTierTimeout` is 2 x `ai.MaxProbeDeadline()` + 30s, and `doctorVaultTierTimeout` is `mcp.DoctorExercisedBudget()` + 10s. Both are derived from the budgets that run inside them, so a slow model is never failed, only a hang. Tier 2 reuses `mcp doctor`'s engine checks, which standalone `mcp doctor` caps at `mcp.DoctorExercisedBudget()` and which would otherwise run unbounded against a hung provider. But a single shared budget is also wrong, because tier 1's two sequential probes each cap at the strategy-aware `ai.ProbeDeadline`, and a degraded provider could eat both caps, leaving tier 2 to expire mid-check and report "the search index is unusable" when the truth is that the self-test ran out of time. Per-tier floors prevent that starvation, and `deadlineCheck` additionally renders any expired check as an explicit inconclusive-timeout rather than under the static "index unusable" fix text: a diagnostic must never misattribute its own timeout to the subsystem it was inspecting.

**Credential precedence is deliberate.** Any successful call means `accepted`; any `bad_credentials` means `rejected`; `access_denied` ALONE means `unknown`, never `accepted`. A bad bearer token 403s on the classic Bedrock plane; `ClassifyProbeError` reads that 403's message body, so the known bad-key phrasings ("Invalid API Key format…", "Authentication failed…") now resolve to `bad_credentials` there. A 403 whose body does not name the key stays ambiguous between "not entitled" and "bad key", which is why a lone `access_denied` is `unknown` rather than `accepted`: trusting it would tell a user with a dead key to go chase an entitlement problem.

`--json` returns a `DoctorReport`: `SuiteStatus` embedded (so `{latest, checked, detail, in_sync, cli, app, plugin}` stay top-level and old decoders keep working) plus additive `ok` and `selftest{ok, vault_bound, vault_path, provider, credentials, checks[]}`.

`--versions` restores the historical parity-only behavior: free, no model calls, always exits 0, unchanged `SuiteStatus` JSON. It is what any automatic or repeating caller must use (the Obsidian plugin's Components section does).

Version parity itself is unchanged: a shared 24h release cache with a refetch when the cache is behind an install, never showing a component a "latest" below its own version; the plugin version is read from the open vault (or `--vault`), degrading to "unknown"; the app version is read from `SecondBrain.app`'s `Info.plist` (macOS only).

### update

Checks whether a newer 2ndbrain release is available: compares the installed versions against the latest published GitHub release (`api.github.com/repos/apresai/2ndbrain/releases/latest`, cached 24h under `~/Library/Caches/2nb/updates`) and lists every component that is behind, not just the CLI (a release that shipped the CLI but not the app or cask is still flagged). It never hard-errors: offline falls back to the cache, then reports "couldn't check". The 24h cache is refetched when it is behind an install (the installed version is proof a newer release exists), so a just-released version is not shown as stale, and a component is never displayed with a "latest" below its own version. `--json` returns `{current, latest, update_available, checked, detail, app, plugin}`, where `app`/`plugin` are `ProductState` objects (additive and back-compatible; `current`/`latest`/`update_available` remain the CLI's own). Use `2nb doctor` for the full presence-aware breakdown.

## Global flags and output formats

Global flags: `--format` (json/csv/tsv/yaml/raw/md/text; listings also accept `paths`/`tree`), `--porcelain`, `--json`, `--csv`, `--yaml`, `--vault`, `--unconfigured` (permit a write to a vault Obsidian doesn't know; without it such a write is refused), `--verbose` / `-v`, `--copy`.

`--format raw` (and `md`) emits a value's `Serialize()` output (or the raw string/bytes) with no JSON wrapping, for piping a document body verbatim; `tsv` is tab-separated CSV; `text` is best-effort plain text.

`--copy` also writes a command's rendered output to the clipboard (macOS `pbcopy`; a clear unsupported error elsewhere): `read`/`print` (body), `meta`/`property:read` (value), and `daily`/`daily path` (path) copy in their default output, and any command run with a machine format (`--json`/`--csv`/`--format ...`, including `search`/`unresolved`/`list`) copies that rendered output.

Parent-command defaults: `2nb ai` → `ai status`, `2nb models` → `models list`, `2nb git` → `git status`, `2nb mcp` → `mcp status`, `2nb plugin` → `plugin status`, `2nb skills` → `skills list`, `2nb config` → `config show`, `2nb metrics` → `metrics show`, `2nb instructions` → `instructions configured`. `--help` still works on every parent (Cobra intercepts before `RunE`).
