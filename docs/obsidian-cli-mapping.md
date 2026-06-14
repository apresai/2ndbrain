# Obsidian CLI compatibility

`2nb` is a **headless** Obsidian-native vault engine. It is a drop-in scripting
replacement for the *file/markdown* parts of Obsidian's official CLI and
NotesMD-style CLIs: reading, creating, editing, searching, and querying notes,
properties, tasks, links, and tags. It deliberately does **not** drive a running
Obsidian app (panes, themes, plugins, Sync/Publish, workspace, dev-tools).

This page maps common Obsidian-CLI invocations to their `2nb` equivalents and
documents the accepted argument forms. Every `2nb` command also takes
`--vault <path>` (or `vault=<path>`); when omitted, the active vault is resolved
from `2NB_VAULT`, then `~/.2ndbrain-active-vault`, then the current directory.
`2nb` never silently falls back to a different vault, and never writes to a
guessed file (an ambiguous target fails loudly with the candidate paths).

## Accepted argument forms

`2nb` accepts three interchangeable styles (the argv shim `preprocessArgs`
translates the Obsidian forms into native flags before cobra parses them):

| Style | Example | Notes |
|-------|---------|-------|
| Native cobra flags | `2nb read note.md --format raw` | The canonical form. |
| `key=value` arguments | `2nb read file="My Note" format=raw` | Obsidian-CLI style. |
| Boolean tokens | `2nb files total`, `2nb create "X" overwrite` | Bare words mapped to flags for the owning command only. |

Recognized `key=value` keys: `file=`, `path=`, `to=`, `content=`, `name=`,
`value=`, `query=`, `ref=`, `line=`, `vault=`, `format=`, `template=` (create),
`old=` / `new=` (tags:rename), `tag=` (tag:add/tag:remove). `vault=` is honored
even in first position. A
free-text `search` / `ask` / `chat` / `search-content` query is never parsed as
`key=value` (so a query containing `=` is preserved), and an unrecognized
`key=value` on any command passes through verbatim rather than being dropped.

Boolean tokens: `total` (list/files/tasks/unresolved), `append` / `overwrite`
(create), `done` / `todo` / `toggle` (task/tasks), `verbose` (any structured
command; a bare `verbose` inside a free-text `search`/`ask`/`chat` query stays
part of the query).

### Target resolution: `path=` vs `file=` vs a bare positional

| Form | Resolution |
|------|------------|
| `path=X` | **Strict exact** vault-relative path. Never fuzzy-matches. |
| `file=X` | **Fuzzy**: exact path → shortest-unique basename/suffix → title → alias. Fails loudly on ambiguity. |
| bare `X` | **Auto**: the exact path if it exists on disk, otherwise the fuzzy resolver (low-regression fallback). |

A `#heading` / `#^block` anchor on the target is stripped before matching.

### Output formats (`--format` / `format=`)

`json`, `csv`, `tsv`, `yaml`, `raw`, `md` (markdown body, same as raw for a
document), `text` (best-effort plain text). Listing commands (`list`/`files`,
`tasks`, `unresolved`) additionally accept `paths` (one vault-relative path per
line) and `tree` (an indented directory hierarchy), plus `--total` to print only
the count.

### `--copy`

`--copy` also writes a command's rendered output to the system clipboard
(macOS `pbcopy`; other platforms return a clear "unsupported" error rather than
silently doing nothing). It copies in the default output for `read`/`print` (the
body), `meta`/`property:read` (the value), and `daily`/`daily path` (the path).
For other commands (`search`/`search-content`, `unresolved`, `list`/`files`),
`--copy` copies the machine-format output, so pair it with a `--format`
(`--json`/`--csv`/`--format tsv`, etc.).

## Command mapping

| Obsidian-CLI | 2nb | Notes / limitations |
|--------------|-----|---------------------|
| `print` / `cat` a note | `2nb read file=…` (alias `2nb print`) | `--chunk "Heading"` reads one section. |
| read by title/alias | `2nb read file="Title"` | Fuzzy resolver; `path=` for an exact path. |
| `create` a note | `2nb create "Title" content=… template=…` | `template=` selects a built-in type (adr/runbook/note/postmortem/prd/prfaq). |
| create, append if exists | `2nb create "Title" content=… append` | Appends to the existing same-title note, else creates. |
| create, overwrite if exists | `2nb create "Title" content=… overwrite` | Replaces the existing same-title note in place (keeps its id). |
| append to a note | `2nb append file=… content=…` | Body-only write; frontmatter untouched. |
| prepend to a note | `2nb prepend file=… content=…` | |
| replace a note / section | `2nb replace file=… content=… [--section H]` | |
| read/list properties | `2nb meta file=…` (aliases `frontmatter`, `fm`, `properties`) | |
| `property:read` | `2nb property:read name=K file=…` → `2nb meta --get K` | |
| `property:set` | `2nb property:set name=K value=V file=…` → `2nb meta --set K=V` | Schema-validated. Array fields (`tags`, `aliases`, schema `list`/`tags` fields) are coerced to a YAML list, comma-split, replace semantics (`--set tags=a,b`); use `tag add`/`tag remove` for incremental tag edits. |
| `property:remove` | `2nb property:remove name=K file=…` → `2nb meta --remove K` | |
| list notes | `2nb list` / `2nb files` | `--type --status --tag --sort --limit`; `total`, `format=paths|tree`. |
| `daily:path` | `2nb daily` / `2nb daily path` | Resolves + creates today's note, prints the path. |
| `daily:read` | `2nb daily read` | |
| `daily:append` / `daily:prepend` | `2nb daily append|prepend content=…` | |
| keyword/content search | `2nb search-content "query"` → `2nb search --bm25-only` | Non-interactive; no AI provider required. |
| semantic / hybrid search | `2nb search "query"` | Needs an embedding provider for the vector channel. |
| `search:context` | `2nb search "query"` | Colon form of `search` (no GUI `search:open`; that is out of scope). |
| `tags:rename` | `2nb tags:rename old=A new=B` → `2nb tags rename A B` | Frontmatter tags (v1); `--dry-run` to preview. |
| add a tag to a note | `2nb tag:add file=N tag=a,b` → `2nb tag add N a,b` | Per-note frontmatter tags; merges + dedupes + reindexes. |
| remove a tag from a note | `2nb tag:remove file=N tag=a` → `2nb tag remove N a` | No-op if the tag is absent. |
| list tags | `2nb tags` | With per-tag counts. |
| tasks | `2nb tasks [done|todo] [total]` ; toggle: `2nb task ref=note.md:12 done` | GFM checkboxes. |
| broken links | `2nb unresolved [total]` (or `link:unresolved`) | Vault-wide. |
| orphans / dead-ends | `2nb link:orphans` / `2nb link:deadends` | |
| backlinks / outbound links | `2nb backlinks <path>` / `2nb links <path>` | |
| move / rename a note | `2nb move <src> <dst>` / `2nb rename <src> <name>` | Link-aware (rewrites wikilinks + markdown links); mandatory `--dry-run` preview. |
| list vaults | `2nb list-vaults` → `2nb vault list` | |
| set default vault | `2nb set-default-vault path=…` → `2nb vault set …` | |
| add vault | `2nb add-vault path=… --set-default` → `2nb vault create …` | `--set-default` is a no-op (create already activates). |

## Intentionally out of scope

These require a running Obsidian app and are **not** implemented in `2nb` (use
the Obsidian app or its plugin CLI instead):

- Opening notes/panes in the GUI, `search:open`, focusing the UI
- Workspace save/load, layout, bookmarks, tabs
- Themes, CSS snippets, plugin enable/reload, dev-tools, `eval`
- Sync / Publish
- File-recovery version history
- Bases *querying* (`.base` files are indexed read-only; `2nb` does not evaluate base views)
