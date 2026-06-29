---
name: 2nb
description: 2ndbrain (2nb) knowledge base for Obsidian-native vaults. Use to semantically search, recall, fetch, or save notes; to run the 2nb CLI or its kb_* MCP tools; or when working with markdown that has YAML frontmatter and [[wikilinks]]. Covers hybrid BM25 plus vector search, RAG ask, document create/read/update, and the search-before-create workflow that avoids duplicates.
---

# 2ndbrain Knowledge Base

This project uses **2ndbrain** (`2nb`), an AI companion for Obsidian-native markdown vaults with a Go CLI, a native macOS dashboard, and an MCP server. All documents live as plain `.md` files with YAML frontmatter in a **vault** directory. The CLI and the macOS app share a SQLite index at `<vault>/.2ndbrain/index.db` via WAL mode.

> **About this skill.** It is maintained in the **2ndbrain repo** — the source of truth is `cli/internal/skills/content/2ndbrain-skill.md`, mirrored to `.agents/skills/2nb/SKILL.md` (+ `.warp/skills/2nb/SKILL.md`, `.claude/skills/2nb/SKILL.md`) so any agent that opens the repo discovers it with zero install. The repo's full project reference is `CLAUDE.md`; the condensed agent reference is `AGENTS.md`. `2nb skills install <agent>` installs this same content for Claude Code, Cursor, Warp, and other agents; `make sync-skills` regenerates the in-repo mirrors and CI fails on drift.

## First — orient yourself in the vault (the 5-command drill)

**Run these before any create / write / index action.** They tell you which vault you're working with and whether AI is available. (You don't have to babysit the write target: `2nb` writes to the vault you have open in Obsidian by default — see "Which vault gets written" below — so a misplaced `cd` can't split your notes.) Each answers a specific question, in order:

```bash
2nb vault status              # Is this a vault? Is it healthy? How many docs?
2nb ai status                 # Can I use semantic search? Which provider?
2nb config show               # Full config (vault paths, ai.* settings)
2nb list --json --limit 5     # Sample of content — what's actually here?
2nb skills show claude-code   # Self-referential: what am I supposed to know?
```

### Which vault gets written (read this before any write)

The **active vault is the one you have open in Obsidian** — 2nb reads Obsidian's own registry, independent of your current working directory. Write commands (`create`, `append`, `prepend`, `replace`, `meta --set`, `move`, `index`, the `kb_*` write tools) resolve the target in this fixed order:

1. `--vault <path>` or `2NB_VAULT` — an explicit override (use this only when you mean a *different* vault).
2. Otherwise, **the vault Obsidian has open** (or, if Obsidian is closed, your most-recently-opened vault). This is the default and needs no flags.

The current directory is **not** an implicit write target. If you run a write from a folder that isn't a vault, 2nb does **not** walk up the tree or create a stray vault — it writes to your open Obsidian vault, or refuses with an actionable error. So a wrong `cd` can never split your notes. `kb_info` or `2nb vault show` report which vault is active.

**Don't run `2nb vault create .`** to "make the current directory work" — that mints a second vault and is exactly how notes get split. To write somewhere other than your Obsidian vault, pass `--vault <path>`; to write to a folder Obsidian doesn't know (rare; the note won't show in Obsidian or your 2nb index), add `--unconfigured`.

## Semantic-search playbook (the core loop)

> In Claude Code: if the 2ndbrain MCP server is configured you will see `kb_*` tools. Prefer them. They cache the resolved similarity threshold and the loaded embeddings per session, so repeated calls skip a DB round-trip. With no MCP server, shell out to `2nb` instead. Every step below gives both.

Three intents cover almost every request. Pick the path; do not improvise a one-off shell pipeline.

**1. Find / recall / "what do we know about X" → search, then fetch.**
- Search: `kb_search` (query) or `2nb search "X" --json`.
- Rank by `vector_score` (raw cosine), NOT `score`. `score` is RRF rank-fusion: useful for ordering, meaningless as an absolute relevance signal (a strong vector-only hit can show `score` near 0.016). Cosine bands: 0.6+ strong, 0.35 to 0.6 related, below the active threshold it is dropped entirely. A result with no `vector_score` was a BM25-only match (still valid).
- Fetch the winners: `kb_read` / `2nb read <path>`. For a long document call `kb_structure` first, then `kb_read` with `chunk:"<heading>"` so you pull one section instead of the whole body.

**2. Save / capture / "write up X" → search FIRST, then create.**
- Search the topic before creating, every time: `kb_search` / `2nb search`. Vaults accumulate duplicates fast. A near-match (cosine 0.6+) usually means you want to edit or extend that doc, not add a new one.
- Genuinely new: `kb_create` / `2nb create --type <type> --title "..."` mints the UUID and type-appropriate frontmatter. Then write the body and add `[[wikilinks]]` to the docs search surfaced. To file the doc under a subdirectory, pass `--path <subdir>` (CLI) or the `path` argument (`kb_create`); the directory is created if missing, otherwise the doc lands in the vault root. Titles allow only letters, numbers, spaces, and basic punctuation (no em-dashes or slashes) since the title becomes the filename slug.
- `kb_suggest_links <path>` ranks docs you SHOULD link but have not yet (semantic similarity), which is different from `kb_related` (docs you already link via the wikilink graph).

**3. Answer a question from the notes → ask, then verify.**
- `kb_ask` / `2nb ask "..."` searches and synthesizes an answer with a source list. It feeds the FULL text of the top-ranked matching notes (parent-document context, bounded by `ai.rag_context_budget`), so an answer deep in a long note isn't truncated away. `ask` is STRICTER than `search`: matches are gated by the same threshold, so a borderline hit that clears `search` may be too weak to ground an `ask` answer. If `ask` returns "no relevant documents," drop to `kb_search` with broader terms.
- RAG can hallucinate a specific detail out of a real chunk. `kb_read` each cited source before repeating an exact claim, number, or name.

**Know which vault you're writing to.** `kb_info` or `2nb vault show --json` answers "which vault?". The default write target is the vault you have open in Obsidian (see "Which vault gets written" above) — the cwd is never an implicit target. Pass `--vault <path>` (or set `2NB_VAULT`) only to override to a different vault on purpose.

**Watch for degraded search.** If `mode` comes back `keyword` when you expected semantic ranking, the vector channel is off (provider down, dimension mismatch, or an unindexed vault). Check `mode` on every `--json` result and read `warnings` when it is present. Never report "no matches" without first confirming `mode` was `hybrid` (see "Search scoring" and the recovery playbook below).

## Copy-paste recipes

Exact invocations for the common tasks. Substitute real titles/paths; add `--json` for machine output. **Build your command from these, don't probe `--help`.** Each line says what it does.

**Create & write** (the only commands that change a note's body are `create`, `append`, `prepend`, `replace`, `polish --write`, and `move`/`rename`)
```bash
2nb create --type note --title "JWT Auth" --path architecture          # new note at architecture/jwt-auth.md (UUID + frontmatter)
2nb create --type adr --title "Use JWT" --content "$(cat draft.md)"      # create with a body in one shot (instead of the type template)
2nb create --type note --title "Daily Log" --append --content "- did X"  # append to an existing same-title note, else create it
2nb create --type note --title "Spec" --overwrite --content "$(cat spec.md)"  # replace an existing same-title note in place (keeps its id)
2nb create --type note --title "Meeting" --allow-duplicate              # skip the content-hash dedupe guard and create anyway
2nb append daily-log.md --text "- another line"                         # append to a doc body (frontmatter untouched)
2nb prepend inbox.md --file snippet.md                                  # insert at the top of the body, after the frontmatter
2nb replace notes.md --section "## Status" --text "Shipped."            # replace one heading's section (first match wins)
echo "from stdin" | 2nb append notes.md                                # --text / --file / stdin are the three input sources
```

**Read & query** (rank search by `vector_score`, raw cosine, not `score`, the RRF rank-fusion)
```bash
2nb search "jwt expiry" --json --limit 10                  # hybrid BM25 + vector
2nb search "jwt expiry" --threshold 0.15 --bm25-only       # widen a too-narrow search / skip the vector channel
2nb ask "when do tokens expire?" --json                    # RAG answer + sources; read each source before repeating a claim
2nb outline jwt-auth.md --json                             # heading tree, so you can then pull ONE section:
2nb read jwt-auth.md --chunk "Implementation"              # just that heading's section (cheaper than the whole doc)
2nb list --type adr --status accepted --json               # filtered listing
2nb list --tag security --format paths                     # one vault-relative path per line
```

**Metadata, tags, tasks**
```bash
2nb meta jwt-auth.md --get status                          # read one frontmatter field
2nb meta jwt-auth.md --set status=accepted                 # write (schema + status-transition validated); reindexes the file
2nb meta jwt-auth.md --set tags=security,auth              # array fields: comma-split, REPLACE semantics
2nb tag add jwt-auth.md security auth                      # incremental tag add (merge/dedupe); use this, not --set, to add
2nb tag remove jwt-auth.md draft
2nb tasks --todo --path projects/                          # open GFM checkboxes under a dir
2nb task projects/plan.md 12 --done                        # toggle the checkbox on 1-based body line 12
```

**Links & graph**
```bash
2nb links jwt-auth.md          # outbound links incl. broken ones (each has resolved:bool); per-file broken-link view
2nb backlinks jwt-auth.md      # who links TO this note (check before delete/rename)
2nb unresolved                 # every broken wikilink across the whole vault
2nb suggest-links jwt-auth.md  # notes you SHOULD link but don't yet (semantic; excludes already-linked)
2nb related jwt-auth.md --depth 2   # graph neighbors via [[wikilinks]]
```

**Move / rename: rewrites every `[[link]]` to the note across the vault. ALWAYS dry-run first.**
```bash
2nb move old.md archive/old.md --dry-run    # preview: the rename, per-note link rewrites, and ambiguous links it would skip
2nb move old.md archive/old.md              # apply (refused if a bare [[name]] is ambiguous, unless --force)
2nb rename old.md new-name                  # same folder; .md appended if omitted; delegates to move
```

**Polish (copy-edit + fix links, in place)**
```bash
2nb polish notes.md --json                                   # preview only: original + polished body for a diff (no write)
2nb polish notes.md --write --links --repair-links --json    # apply: copy-edit, repair broken links, add grounded links, re-embed; snapshot kept
2nb polish notes.md --undo                                   # restore the pre-polish snapshot (refuses if changed since, unless --force)
```

**Index & AI** (after a DIRECT file write, not via a 2nb write command, reindex so search/embeddings see it)
```bash
2nb index --doc jwt-auth.md     # re-index + re-embed ONE file (cheap, hash-gated)
2nb index                       # whole vault; only re-embeds changed docs and purges deleted ones
2nb ai status --json            # provider, models, embedding coverage, threshold + source, portability_status
```

**Complete example, author a design note and wire it into the graph:**
```bash
2nb search "rate limiting" --json          # 1. search FIRST (avoid duplicates); a 0.6+ cosine hit means edit, don't create
2nb create --type adr --title "Token Bucket Rate Limiting" --path architecture   # 2. create if genuinely new
2nb meta architecture/token-bucket-rate-limiting.md --set status=accepted        # 3. set status (validated)
2nb append architecture/token-bucket-rate-limiting.md --file decision.md         # 4. add the body
2nb suggest-links architecture/token-bucket-rate-limiting.md                     # 5. find notes to [[link]], then append those links
```

**Obsidian-CLI forms (drop-in)**, if you're used to the `obsidian` CLI, map onto the same commands. Note the colon-commands take `key=value` args (`name=`/`value=`/`file=`/`content=`/`old=`/`new=`), not positionals:
```bash
2nb print file=jwt-auth                                   # == 2nb read jwt-auth.md  (file= is FUZZY: basename/title/alias/suffix)
2nb read path=architecture/jwt-auth.md                    # path= is a STRICT exact vault-relative path
2nb property:read name=status file=jwt-auth               # == meta --get status
2nb property:set name=status value=accepted file=jwt-auth # == meta --set status=accepted
2nb daily:append content="- a note"                       # == daily append --text "- a note"
2nb tags:rename old=wip new=in-progress                   # == tags rename wip in-progress
2nb files                                                 # == list  (filters are CLI flags: 2nb list --type adr)
```
`file=` fails loudly with candidates on ambiguity; a free-text `search`/`ask`/`chat` query is never parsed as `key=value` (so a query containing `=` is preserved).

## Flags by command

Build the command from this table instead of probing `--help`. **Global flags on every command:** `--json` `--yaml` `--csv` `--format <fmt>` `--porcelain` `--vault <path>` `--copy` `--verbose`. Per-command flags worth knowing:

| Command | Flags |
|---|---|
| `create` | `--type` `--title` `--path <subdir>` `--content` `--overwrite` `--append` `--allow-duplicate` |
| `search` | `--type` `--status` `--tag` `--limit` `--threshold` `--bm25-only` |
| `list` | `--type` `--status` `--tag` `--sort` `--limit` `--total` `--format paths\|tree` |
| `read` | `--chunk "<heading>"` |
| `ask` | `--history <path\|->` |
| `meta` | `--get <key>` `--set key=value` `--remove <key>` |
| `tag add` / `tag remove` | `<note> <tag>...` (space- or comma-separated) |
| `tags rename` | `<old> <new>` `--dry-run` |
| `append` / `prepend` | `--text` `--file` (or stdin) |
| `replace` | `--text` `--file` (or stdin) `--section "<heading>"` |
| `task` | `<path> <line>` `--done` `--todo` `--toggle` |
| `tasks` | `--done` `--todo` `--path <file\|dir>` `--total` |
| `move` / `rename` | `--dry-run` `--force` |
| `polish` | `--write` `--links` `--repair-links` `--undo` `--force` `--system` `--max-tokens` |
| `index` | `--doc <path>` `--force-reembed` |
| `stale` | `--since <days>` (an integer number of days, e.g. `--since 7`; NOT a duration string) |
| `related` | `--depth N` |
| `git activity` | `--since <duration>` (a duration string, e.g. `--since 7d`) |

## Parent-command defaults

Every command group has a useful default action when called without a subcommand. `--help` still works everywhere because Cobra intercepts it before the default runs.

| Shortcut | Equivalent to | Useful for |
|---|---|---|
| `2nb vault` | `2nb vault status` | Vault health: docs, embeddings, portability, AI reachability, stale count |
| `2nb ai` | `2nb ai status` | "Is my embedding/generation provider ready?" |
| `2nb models` | `2nb models list` | Browse verified model catalog |
| `2nb git` | `2nb git status` | Uncommitted files in a git-backed vault |
| `2nb mcp` | `2nb mcp status` | Which MCP clients are live? |
| `2nb plugin` | `2nb plugin status` | Is the Obsidian plugin installed, and at what version? |
| `2nb skills` | `2nb skills list` | Which agents have this skill installed? |
| `2nb config` | `2nb config show` | Full config including vault paths |

## CLI Commands

All commands support `--json`, `--yaml`, `--csv`, `--format` (also `tsv`/`raw`/`md`/`text`; listings add `paths`/`tree` and `--total`), `--porcelain`, `--vault <path>`, `--copy`, and `--verbose`. Prefer `--json` in scripts and agent pipelines. **You should not need to shell out to `--help` to use these commands.** The "[Copy-paste recipes](#copy-paste-recipes)" section gives the exact invocation for every common task, "[Flags by command](#flags-by-command)" lists the flags each command takes, and "[Worked JSON examples](#worked-json-examples)" show the output shapes. (`2nb <command> --help` and `2nb --help` still exist to confirm a detail, but build the command from the recipes first rather than invoking it, reading the help, and retrying.)

**Obsidian-CLI compatibility.** `2nb` accepts `obsidian`-CLI-style invocations as a drop-in: `key=value` args (`file=`, `path=`, `content=`, `template=`, `query=`, `vault=`, `old=`/`new=`), boolean tokens (`total`, `append`, `overwrite`, `done`/`todo`), colon-commands (`daily:path`/`daily:append`, `property:set` → `meta`, `tags:rename`, `link:unresolved`), and aliases (`print` → `read`; `fm`/`frontmatter`/`properties` → `meta`; `files` → `list`; `search-content` → keyword search; `list-vaults`/`set-default-vault`/`add-vault` → the `vault` subcommands). `file=` resolves a note by exact path → basename/title/alias/shortest-unique suffix (fails loudly on ambiguity); `path=` is strict-exact. Out of scope (needs the running app): GUI panes, themes, plugins, Sync/Publish, workspace, dev-tools.

### Read & query

| Command | Purpose |
|---------|---------|
| `2nb list` | List documents with `--type`, `--status`, `--tag`, `--sort`, `--limit` filters |
| `2nb read <path>` | Read full document or a specific heading chunk (`--chunk "Heading"`) |
| `2nb meta <path>` | View frontmatter. `--get <key>` reads one field (exits 1 if absent); `--set key=value` writes; `--remove <key>` deletes a field in place (preserves comments/order; refuses id/path/title/type and schema-required keys) |
| `2nb search <query>` | Hybrid BM25 + vector search. Shows `(rrf=X.XXX, cos=Y.YYY)` per result. `--threshold` overrides `ai.similarity_threshold` per-query. `--bm25-only` skips vector search. |
| `2nb ask "<question>"` | RAG Q&A — searches the vault, synthesizes an answer with source citations. Multi-turn: `--history <path\|->` takes a JSON array of `{role, content}` turns (`-` = stdin); follow-ups are rewritten into standalone retrieval queries (`rewritten_query` in `--json`) |
| `2nb chat` | Interactive multi-turn REPL over the same pipeline as `ask --history` (human terminal use; agents should prefer `ask --history`, which has `--json`) |
| `2nb related <path>` | Find docs connected via `[[wikilink]]` graph traversal (`--depth N`) |
| `2nb backlinks <path>` | List resolved inbound links to a document: which docs link to it (source path/title + the link's heading/alias/raw form) |
| `2nb links <path>` | List outbound links from a document, including unresolved ones (each row carries a `resolved` bool), so it doubles as a per-file broken-link view |
| `2nb orphans` | List documents nothing links to (no resolved inbound link) — candidates to wire into the graph |
| `2nb deadends` | List documents that link to nothing real in the vault (no resolved outbound link; a note with only broken links still counts) |
| `2nb unresolved` | List every broken wikilink across the whole vault (source doc + the raw `[[target]]` that resolves to no note). Vault-wide complement to the per-file view in `2nb links` |
| `2nb graph` | Output the full link graph as JSON adjacency list |
| `2nb suggest-links <path>` | Rank semantically related documents that would make good wikilink targets (excludes docs already linked) |
| `2nb stale --since 7` | Docs not modified within N days (`--since` is an integer day count) |
| `2nb outline <path>` | Heading tree of a document (heading path, level, line span). Same chunking as `read`; shared with the MCP `kb_structure` tool |
| `2nb wordcount <path>` | Word, character, and heading counts over the indexable body (comments stripped). Alias: `2nb wc` |
| `2nb folders` | List folders (directory prefixes of doc paths) with counts; root docs bucket under `(root)` |
| `2nb tasks` | List GFM checkbox tasks (`- [ ]` / `- [x]`) across the vault. Filters: `--done`, `--todo`, `--path <file\|dir>`. v1 = GFM open/done only (custom statuses like `[>]`/`[-]` ignored) |
| `2nb tags` | List all tags vault-wide with counts (parent command: bare `tags` lists, `tags list` is explicit) |
| `2nb aliases` | List frontmatter aliases mapped to their document (alias to path/title) |

### Write

`2nb` writes only the gitignored `.2ndbrain/` sidecar and never rewrites a note's body on its own. The body-write commands below (`append`, `prepend`, `replace`, and `polish --write`) are the explicit, opt-in exceptions: they rewrite the body in place only when you invoke them. `meta --set` and `tags rename` rewrite the frontmatter in place. Everything else here either creates or deletes whole files, or returns text for you to apply yourself.

| Command | Purpose |
|---------|---------|
| `2nb create --type <type> --title "Title" [--path <subdir>] [--content <body>]` | Create document from template. Generates UUID, timestamps, and type-appropriate frontmatter. `--path` files it under a vault-relative subdirectory (created if missing; default vault root). `--content "$(cat file.md)"` sets the body in one shot instead of the template. On a same-title collision: default makes a `<slug>-1.md`; `--overwrite` replaces in place (reuses the id); `--append` appends to the existing note; `--allow-duplicate` skips the content-hash dedupe guard. |
| `2nb append <path> [--text \| --file \| stdin]` | Append content to the end of a document's body. Frontmatter is left untouched. Explicit, opt-in body write. |
| `2nb prepend <path> [--text \| --file \| stdin]` | Insert content at the start of the body, after the frontmatter. Explicit, opt-in body write. |
| `2nb replace <path> [--section <heading>] [--text \| --file \| stdin]` | Replace the whole body, or just one heading's section content with `--section`. First match wins on duplicate headings. Explicit, opt-in body write. |
| `2nb daily` / `2nb daily path` / `2nb daily read` / `2nb daily append [--text \| --file \| stdin]` / `2nb daily prepend [...]` | Resolve today's daily note from Obsidian's core daily-notes config (`.obsidian/daily-notes.json`: folder, filename format, optional template). Bare `daily` (and the explicit `daily path`) resolves + creates + prints the path; `read` prints its body; `append`/`prepend` add to the body (explicit, opt-in body write). Falls back to Obsidian defaults (root folder, `YYYY-MM-DD`) when the plugin is disabled; the date format honors Moment `[literal]` escaping. |
| `2nb tasks [--done \| --todo] [--path <file\|dir>]` | List GFM checkbox tasks (`- [ ]` / `- [x]`) across the vault, with file + 1-based line + done state. v1 = GFM open/done only. `--json` |
| `2nb task <path> <line> [--done \| --todo \| --toggle]` | Toggle a single GFM checkbox at a 1-based body line (from the `2nb tasks` LINE column). Default toggles; `--done`/`--todo` force a state. Errors if the line is not a checkbox. Explicit, opt-in body write; frontmatter untouched. |
| `2nb meta <path> --set key=value` | Update one or more frontmatter fields in place, with schema + status-transition validation. Rewrites the file's YAML frontmatter; the body is preserved. Array fields (`tags`, `aliases`, schema `list`/`tags` fields) are coerced to a YAML list, comma-split, replace semantics (`--set tags=a,b`); use `tag add`/`tag remove` for incremental tag edits. |
| `2nb tag add <note> <tag>...` / `2nb tag remove <note> <tag>...` | Add/remove frontmatter tags on a single note (per-note counterpart to `tags`). Merges/dedupes (add) or drops (remove), schema-validates, and reindexes so the change is immediately `list --tag`-searchable. Tags may be separate args or comma-separated. |
| `2nb tags rename <old> <new> [--dry-run]` | Rename a frontmatter tag across every doc that carries it; rewrites each doc's frontmatter `tags` array (dedupes when `<new>` is already present) and reindexes. FRONTMATTER-ONLY in v1 (inline body `#old` tags are not rewritten; such docs are skipped). `--dry-run` previews without writing; per-file atomic, non-zero exit on any failure with no rollback. |
| `2nb delete <path> [--force]` | Delete from disk and index |
| `2nb move <src> <dst> [--dry-run] [--force]` | Move/rename a note to a new vault-relative path AND rewrite every `[[wikilink]]` AND markdown-style `[text](path.md)` link across the vault that points at it (wikilinks keep `#heading`/`#^block`/`\|alias`/`!`-embed suffixes; markdown links keep the `[label]` text, any `#anchor`/`?query` suffix, and the `.md` extension; both keep the bare-vs-path form. External-URL and anchor-only markdown links are skipped; links inside code are never touched). The strongest write surface: it edits OTHER notes. **Always `--dry-run` first** to preview the rename, per-note rewrites, and the ambiguous links it would skip. Without `--force`, a move is refused when a bare `[[name]]` link is ambiguous (the name matches more than one note); `--force` then rewrites only the unambiguous path-qualified links. The target file is moved LAST (after referencing notes), so a crash leaves links pointing at the still-present old name. JSON: `{moved, rewritten, skipped_ambiguous, failed}`. |
| `2nb rename <src> <newname> [--dry-run] [--force]` | Rename a note in place (same folder; `.md` appended if omitted), delegating to `move` with all its behavior. |
| `2nb polish <path> [--write] [--links] [--repair-links] [--undo]` | AI copy-edit (spelling/grammar/clarity) — returns JSON with `original` and `polished` body for diff review. Default is preview only (**does not write to disk**). `--write` applies the polished body in place (opt-in) and snapshots the original. `--links` adds grounded `[[wikilinks]]` to existing notes (never invents a target). `--repair-links` repairs broken `[[wikilinks]]` to existing notes (bare-name case/whitespace/alias drift; ambiguous or path-qualified targets reported, never guessed). `--undo` restores the pre-polish snapshot. |

### Index & housekeeping

| Command | Purpose |
|---------|---------|
| `2nb index` | Rebuild the search index and regenerate embeddings for changed docs |
| `2nb index --doc <path>` | Re-index + re-embed only one document (fast, skips unchanged hash) |
| `2nb lint [glob]` | Validate schemas, check broken wikilinks (ignores wikilinks inside code spans) |
| `2nb export-context --types <types>` | Generate a CLAUDE.md-compatible context bundle |

### Git (read-only, vault must be a git repo)

| Command | Purpose |
|---------|---------|
| `2nb git activity --since 7d` | Recent commits that touched vault files |
| `2nb git diff <path>` | Unified diff of a file against HEAD |
| `2nb git status` | Uncommitted + untracked files in the vault |

### Vault lifecycle

| Command | Purpose |
|---------|---------|
| `2nb vault create <path>` | Initialize a new vault at `<path>` (writes `.2ndbrain/` + `.gitignore`, records it in recents). Open it in Obsidian or pass `--vault` to use it — 2nb follows your open Obsidian vault. Replaces the deprecated `2nb init`. |
| `2nb vault set <path>` | Register an existing vault in recents (the active vault follows Obsidian's open vault) |
| `2nb vault list` | Recently used vaults (`*` marks active); reads `~/.2ndbrain-vaults` |
| `2nb vault status` | Health report: docs, embedding coverage, portability state, AI reachability, stale count |
| `2nb vault show` | Terse summary (path, source, name, doc count) — pipe `--json` to scripts |

### Config, AI, MCP, skills

| Command | Purpose |
|---------|---------|
| `2nb config show` | Full config with `vault_root`, `vault_dir`, `vault_name` |
| `2nb config get <key>` | Read one key (e.g. `ai.provider`, `ai.similarity_threshold`). `--effective` on `ai.similarity_threshold` prints the resolved value + source (chain: vault > calibration > model > default) instead of the often-zero raw value |
| `2nb config set <key> <value>` | Write one key. Setting `ai.embedding_model` also resyncs `ai.dimensions` from the catalog; setting `ai.provider` validates the name, re-enables that provider, and warns if an active model can't be served. Search-tuning keys: `ai.similarity_threshold` (drop vector hits below this cosine), `ai.bm25_weight` / `ai.vector_weight` (bias the RRF fusion toward keyword or semantic recall; each defaults to 1.0 — raise `ai.vector_weight` to lean on the vector channel), `ai.dimensions` (Matryoshka width, validated against the model's supported set) |
| `2nb config set-key <provider>` | Store a provider API key in macOS Keychain |
| `2nb config doctor` | Diagnose AI-config problems (provider known/enabled, no orphaned model slot, `ai.dimensions` matches the model, DB embeddings match the selection, threshold resolves), each with a fix hint. A genuine config defect exits 2 (so it can gate a script); an unreachable provider is a non-failing warning, so it stays usable offline/in CI. Run it when search degrades or after editing config by hand |
| `2nb ai status` / `ai setup` / `ai local` / `ai embed <text>` | Provider status, setup wizard (a model that passes its probe is saved to the user catalog as `user_verified`), readiness check, debug embedding |
| `2nb models list` / `models test <id>` / `models bench` / `models wizard [--set-active]` | Verified catalog, smoke test, benchmark favorites, end-to-end discover→test→save wizard (`--set-active` also writes the chosen models into the vault config) |
| `2nb mcp status` | List live MCP servers via `.2ndbrain/mcp/<pid>.json` sidecar files (servers running *right now*) |
| `2nb mcp configured` | Report whether the 2ndbrain MCP server is wired into the AI client config (`~/.claude.json`) for this vault. The durable "is it set up?" check: answers "will my AI tool find this vault?" even when the client is closed, unlike `mcp status`. If not configured, run `2nb mcp-setup`. |
| `2nb mcp-server` | Start the MCP server on stdio (this is what AI clients invoke) |
| `2nb skills install <agent> [--all] [--user]` | Install this SKILL.md for any supported agent — Claude Code, Cursor, Warp, the cross-tool `agents` (`.agents/skills`) standard, and more; run `2nb skills list` for the full set |
| `2nb plugin status` / `plugin install` | Inspect or install/update the Obsidian plugin in the open vault (downloads the latest release assets into `.obsidian/plugins/obsidian-2ndbrain/`; enabling in Obsidian stays manual) |
| `2nb import-obsidian <path>` / `export-obsidian` | Convert between 2nb and Obsidian vault formats |

## MCP Server Tools (22)

The MCP server (`2nb mcp-server`, started as a stdio subprocess by the client) exposes these tools. Use these instead of shell-outs when working through an MCP client — they're faster, atomic, and return structured JSON.

**Orientation**

| Tool | When to call it |
|---|---|
| `kb_info` | **Call this first** when starting a session in a new vault. Returns doc types, schemas, counts, and AI status. |
| `kb_list` | Discover what documents exist with metadata filters. Follow with `kb_read` to get content. |

**Query**

| Tool | When to call it |
|---|---|
| `kb_search` | Hybrid BM25 + semantic search. **Check the `vector_score` field** on each result — it's the raw cosine similarity, which is a better relevance signal than `score` (the RRF fusion score). Above ~0.4 = strong match; 0.2–0.4 = related; below 0.2 is filtered out entirely. |
| `kb_ask` | RAG Q&A — synthesizes an answer from the full text of the top-ranked matching notes (parent-document context). **Fall back to `kb_search`** if `kb_ask` returns "no relevant documents" — both use the same threshold, but a borderline match that surfaces in `kb_search` may be too weak to ground a `kb_ask` answer. |
| `kb_read` | Full document or a specific heading chunk. Call after `kb_search`/`kb_list` to fetch content for the paths you found. |
| `kb_structure` | Heading tree as JSON. Use to pick a chunk name before calling `kb_read` with `chunk:`. |
| `kb_related` | BFS over the `[[wikilink]]` graph to depth N. Use for "what connects to this?" questions. |
| `kb_backlinks` | Resolved INBOUND links to a doc (who links to it). Call before deleting/renaming to see what would dangle. |
| `kb_links` | OUTBOUND links from a doc, including broken ones (each carries `resolved`). Doubles as a per-file broken-link view. |
| `kb_suggest_links` | Given a source doc, returns semantically related docs that aren't already linked from it. Useful while drafting to find existing context you should cite. |
| `kb_tags` | Vault-wide tag list with per-tag document counts. Use to discover the tag vocabulary before filtering or adding a tag. |
| `kb_tasks` | GFM checkbox tasks (`- [ ]` / `- [x]`) across the vault or one file/dir, with `done`/`todo` filters. Each row is `(path, line, done, text)`. |

**Write**

| Tool | When to call it |
|---|---|
| `kb_create` | Create a document from a type template. Auto-generates UUID + timestamps. Optional `path` files it under a vault-relative subdirectory (created if missing). **Search first** (`kb_search` or `kb_list`) to avoid duplicating existing content. |
| `kb_update_meta` | Change frontmatter without touching the body. Enforces schema/state-machine rules (e.g., `adr` status must follow `proposed → accepted → deprecated`). |
| `kb_append` | Append text to the END of a doc's body (frontmatter untouched), then reindex + re-embed. Explicit body write; rejects read-only `.canvas`/`.base`. |
| `kb_replace_section` | Replace the content under ONE heading (siblings untouched, first match wins), then reindex + re-embed. Call `kb_structure` first to confirm heading names. Errors if the heading isn't found; rejects read-only `.canvas`/`.base`. |
| `kb_delete` | Delete from disk + index. Irreversible. Confirm the path is correct before calling. |
| `kb_polish` | AI copy-edit. Returns both `original` and `polished` — **you decide** whether to apply the changes with a follow-up edit. The server doesn't write the polished text anywhere. |
| `kb_index` | Force a full reindex + embedding rebuild. Most operations auto-index; only call this after bulk external edits or imports. |

> Note: `move`/`rename` (the wikilink-rewriting vault mutation) is intentionally **CLI-only**: it is the highest-blast-radius write surface (it rewrites links across every note), so it stays behind `2nb move`/`2nb rename` with their mandatory `--dry-run` preview rather than an MCP tool.

**Git (read-only, only when the vault is a git repo)**

| Tool | When to call it |
|---|---|
| `kb_git_activity` | Recent commits that touched vault files. Use for "what's been changing?" |
| `kb_git_diff` | Unified diff of one file against HEAD. Use before suggesting edits to avoid conflicts with uncommitted changes. |
| `kb_git_status` | Porcelain map of modified/untracked files. |

All three return `{"git_repo": false}` when the vault isn't git-backed — don't retry, just skip.

## Which surface should I use? MCP vs CLI

Both surfaces share the same vault, schemas, and SQLite index. The differences are:

| Task | Prefer MCP when… | Prefer CLI when… |
|---|---|---|
| Search, ask, read, list, structure, related | Long agent session with repeated calls — MCP caches embeddings + threshold per session, saving a DB roundtrip per call | One-shot, scripted, piping into other tools |
| Frontmatter edit | **MCP-only**: `kb_update_meta` does atomic schema-validated updates | `2nb meta --set` works for single keys but doesn't match `kb_update_meta`'s validation |
| Body edit (append / replace one section) | `kb_append` / `kb_replace_section`: structured JSON result, auto reindex + re-embed | `2nb append` / `2nb replace --section`: same write path, plus stdin/`--file` input and whole-body `replace` |
| Backlinks / outbound links / tags / tasks | `kb_backlinks` / `kb_links` / `kb_tags` / `kb_tasks`: structured JSON in a cached session | `2nb backlinks` / `links` / `tags` / `tasks`: piping, plus `orphans`/`deadends` health views |
| Move / rename a note (rewrites `[[wikilinks]]`) | (not available) | **CLI-only**: highest-blast-radius write; `2nb move`/`rename` with mandatory `--dry-run` |
| Create / delete | Either — semantically identical | Either — CLI has human-readable stderr hints |
| Suggest links, polish | Agent wants structured JSON with scores and snippets | Piping to diff/patch or human review in terminal |
| Git read operations | Either — output is identical | Either |
| **Vault create / set / list / status** | — | **CLI-only** — MCP is scoped to an already-open vault |
| **Skills install, models bench/calibrate, config set, import/export-obsidian** | — | **CLI-only** — session-setup operations that don't belong in an MCP session |

**Rule of thumb:** if you're in an MCP-capable client and the tool exists, prefer MCP for latency and structured output. For vault management, skills install, or anything that manipulates the 2nb installation itself, drop to CLI.

## Workflow recipes

### Answer a question from the vault

1. `kb_ask` with the question → get the synthesized answer + source list.
2. If the answer cites sources, `kb_read` each one to verify the claim (RAG can hallucinate details from retrieved chunks).
3. If `kb_ask` returns "no relevant documents", drop to `kb_search` with broader terms or fewer filters.

### Create a new linked note

1. `kb_search` with the topic to check for duplicates. If something exists, maybe you want `kb_update_meta` or an edit, not a new doc.
2. `kb_list --tag <related-tag>` to find the cluster this note belongs to.
3. `kb_create` with the title and type.
4. `kb_read` the new doc to see the template body.
5. Edit the body with `[[wikilinks]]` to the docs you found in step 2. The editor/CLI will re-index automatically on save.

### Review what changed recently

1. `kb_git_activity --since_days 7` (vault must be a git repo) for commit-level view.
2. `kb_list --sort modified --limit 20` for mtime-based view (works without git).
3. For any doc that looks interesting: `kb_git_diff` for the uncommitted delta or `kb_read` for the full content.

### Suggest related documents to link

1. `kb_suggest_links` with the current doc's path → ranked candidates with `score` (RRF), `snippet`, and already-linked docs filtered out.
2. For each candidate you want to use, insert `[[Title]]` at the appropriate spot in the body.
3. On save, the incremental re-embed picks up the new links and `kb_related` will show the connection next time.

### Polish a document's prose

1. `kb_polish` with the path → get `original` and `polished`.
2. Diff the two in your head (or with a diff tool). Check that wikilinks, code blocks, and frontmatter are preserved.
3. If you like the changes, write the polished body back with a normal file edit (polish itself doesn't touch disk).

## Search scoring, explained

`2nb search` and `kb_search` display two numbers per result:

- **`rrf`** — Reciprocal Rank Fusion score. Combines BM25 rank + vector rank. Good for *ordering* results; bad as an absolute relevance signal. A doc that matched only in the vector channel at rank 1 gets `rrf ≈ 0.016` even if the cosine is 0.9.
- **`cos`** — raw cosine similarity from the vector channel. This is what you actually want to look at for "is this relevant?". Rules of thumb (tune per embedding model):
  - ≥ 0.6 — strong semantic match
  - 0.35 – 0.6 — related
  - 0.20 – 0.35 — weakly related
  - < 0.20 — filtered out entirely by `ai.similarity_threshold`

If legitimate matches are being cut, lower the threshold: `2nb config set ai.similarity_threshold 0.15`. If noise is slipping through, raise it. Per-query overrides: `2nb search "foo" --threshold 0.35`.

## Error recovery playbook

When semantic search falls back to BM25, the CLI prints a warning to stderr and the `--json` envelope includes it in `warnings[]`. Match on the stable prefix `"semantic search disabled:"` — the tail varies with provider/dim details.

`2nb ai status --json` is the fastest triage. It exposes `portability_status` (e.g. `ok`, `provider_unavailable`, `dimension_break`, `model_mismatch`, `mixed`, `unindexed`), the active `similarity_threshold` with its `similarity_threshold_source`, the embedding/embeddable/embedded doc counts, and a `providers[]` array carrying per-provider `reachable` / `disabled` / `reason`. Read it before guessing why a search degraded.

| Warning or state | What's wrong | Fix |
|---|---|---|
| `"semantic search disabled: vault was embedded with Nd vectors but current provider X produces Md"` | Dimension mismatch — you switched providers and existing embeddings are the wrong size | `2nb index --force-reembed` OR switch the provider back to the one that built this vault |
| `"semantic search disabled: provider X unavailable — falling back to keyword search"` | The configured provider isn't reachable right now (creds missing, service down, network) | Check `2nb ai status`. BM25 still works — results still return, just without vector ranking. |
| `"semantic search disabled: no AI provider configured"` | Nothing set up yet | `2nb ai setup` |
| `"semantic search disabled: embedder X not registered"` | Config names a provider that isn't compiled in | `2nb config show` — check `ai.provider` |
| Search returns `mode: keyword` with no warnings | Vault has no embeddings yet | `2nb index` — BM25 works immediately, embeddings backfill during the run |
| Search returns empty results | Usually a threshold issue, not a content gap | Try `2nb search "foo" --threshold 0.15` or `--bm25-only` |
| `kb_ask` returns "no relevant documents" | The top-ranked results all got threshold-filtered (see note above — `ask` and `search` share thresholds) | Drop to `kb_search` with the same query |
| `"schema version N newer than supported"` on open | Vault opened by a newer `2nb` than the one installed | `brew upgrade apresai/tap/twonb` |

## Worked JSON examples

`2nb search --json` returns an envelope. Decode `{mode, warnings?, results}`, not a raw array. `warnings` is omitted when empty (`omitempty`), and a result's `vector_score` is omitted for a BM25-only hit, so branch on field presence, not on a zero value:

```bash
$ 2nb search "authentication" --json --limit 2
{
  "mode": "hybrid",
  "results": [
    {
      "doc_id": "0e2c8f1a-…",
      "path": "use-jwt-for-auth.md",
      "title": "Use JWT for Auth",
      "chunk_id": "a52ae4a7d7eadd17",
      "heading_path": "# Use JWT for Auth > ## Decision",
      "content": "...",
      "score": 0.0163,
      "vector_score": 0.72,
      "type": "adr",
      "status": "accepted"
    }
  ]
}
```

Degraded state (provider swap without re-embed):

```bash
$ 2nb search "authentication" --json
{
  "mode": "keyword",
  "warnings": ["semantic search disabled: vault was embedded with 1024d vectors but current provider \"openrouter\" produces 768d — run '2nb index --force-reembed' or switch provider back to the one that built this vault"],
  "results": [...]
}
```

`2nb ask --json` uses the same envelope shape (`warnings` likewise omitted when empty). With `--history`, the standalone query the follow-up was rewritten into appears as `rewritten_query` (omitted on single-shot asks):

```bash
$ printf '[{"role":"user","content":"tell me about auth"},{"role":"assistant","content":"Auth uses JWT..."}]' \
  | 2nb ask --history - "when do they expire?" --json
{
  "mode": "hybrid",
  "answer": "JWT tokens expire after...",
  "sources": ["use-jwt-for-auth.md", "debug-auth-failures.md"],
  "rewritten_query": "When do the JWT authentication tokens expire?"
}
```

**Always check `mode`, and `warnings` when present,** before assuming hybrid search ran. An agent that proceeds on empty results without checking `mode` will report "no matches" when the real problem is a broken provider (the results came back keyword-only).

## Document Format

Documents are plain `.md` files with YAML frontmatter:

```yaml
---
id: <UUID>
title: Document Title
type: note
status: draft
tags: [tag1, tag2]
created: 2026-01-01T00:00:00Z
modified: 2026-01-01T00:00:00Z
---
# Document Title

Body content with [[wikilinks]] to other documents.
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `id` | Yes | Stable UUID (survives renames, used for graph edges) |
| `title` | Yes | Document title |
| `type` | Yes | Document type: note, adr, runbook, postmortem, prd, prfaq |
| `status` | Varies | Type-specific status (see state machines below) |
| `tags` | No | Array of tags |
| `created` | Auto | Creation timestamp (ISO 8601) |
| `modified` | Auto | Last modification timestamp (ISO 8601) |

### Document Types and Status State Machines

| Type | Valid Statuses | Legal Transitions |
|------|---------------|-------------------|
| `note` | draft, complete | any → any |
| `adr` | proposed, accepted, deprecated, superseded | proposed → accepted/deprecated; accepted → deprecated/superseded |
| `runbook` | draft, active, archived | draft → active → archived |
| `postmortem` | draft, reviewed, published | draft → reviewed → published |
| `prd` | draft, review, approved, shipped, archived | draft → review → approved → shipped → archived; review/approved can return to draft |
| `prfaq` | draft, review, final | draft → review → final; review can return to draft |

`kb_update_meta` and `2nb meta --set` enforce these transitions. `kb_create` picks the initial status for the type.

### Wikilink Syntax

- `[[target]]` — Link by title or filename stem
- `[[target#heading]]` — Link to a specific heading
- `[[target|display text]]` — Aliased link

Wikilinks inside fenced code blocks or inline backticks are ignored by the extractor, so prose about wikilink syntax won't produce lint warnings.

## Key Conventions and Pitfalls

- **Writes go to your open Obsidian vault by default** — the cwd is never an implicit target and 2nb won't mint a stray vault; pass `--vault` only to override on purpose. `2nb vault show --json` confirms "which vault?".
- **Every document has a UUID `id`** — use it for stable references, and never rewrite it during an edit.
- **Don't hand-edit `modified` timestamps** — the save path does this automatically; a manual edit can desync with `content_hash` and force a spurious re-embed.
- **Search before create** — the vault accumulates duplicates fast otherwise. `kb_search` + `kb_list --tag` are cheap.
- **Paths are vault-relative** — always. `2nb read foo.md` works; `2nb read /abs/path/foo.md` does not.
- **External file edits need a re-index** — if you use `Write` directly instead of `kb_update_meta`, follow up with `2nb index --doc <path>` or expect stale search results.
- **The `kb_polish` and `kb_suggest_links` MCP tools don't write to disk** — they return suggestions; apply them with a subsequent edit. (At the CLI, `2nb polish --write` is the explicit opt-in that does write the polished body in place.)
- **`status` transitions are enforced** — if you try to jump `adr` straight from proposed to superseded, `kb_update_meta` will reject it. Go through accepted first.
- **Foreign vaults** (Obsidian dir with no `.2ndbrain/`) — `2nb create` won't touch them. Use direct file writes with the frontmatter template above, OR run `2nb vault create <dir>` to convert it into a 2nb vault first.

## Vault Structure

```
vault-root/
├── .2ndbrain/
│   ├── config.yaml          # Vault config (name, embedding, ai.*)
│   ├── schemas.yaml         # Document type schemas
│   ├── index.db             # SQLite search index (shared with the macOS dashboard)
│   ├── bench.db             # Model benchmark history (optional)
│   ├── mcp/<pid>.json       # One sidecar status file per running mcp-server
│   ├── recovery/            # Pre-write crash snapshots
│   └── logs/cli.log         # Structured slog output
├── document-1.md
├── document-2.md
└── subdirectory/
    └── document-3.md
```

The `.2ndbrain/` directory is the signal that a directory is a 2nb vault. If it's missing, the directory is just markdown files — 2nb won't index or write to it until `2nb vault create` creates the directory.
