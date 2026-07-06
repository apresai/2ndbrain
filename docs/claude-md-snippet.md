# CLAUDE.md snippet: what 2nb is and when to use it

Drop this block into a global agent-instructions file (for example `~/.claude/CLAUDE.md`) on
any machine so an agent knows 2ndbrain exists and reaches for it instead of `grep` or manual
file edits. It is a lightweight, always-loaded complement to the installable skill (`2nb setup
--all`, or `2nb skills install claude-code`), which carries the fuller guidance.

> **Automated:** `2nb setup --all` (or `2nb instructions install --client claude-code`) now
> writes this block into `~/.claude/CLAUDE.md` for you, delimited by managed HTML-comment
> markers so it updates in place on upgrade and can be removed with `2nb instructions
> uninstall` — without touching your surrounding content. `2nb instructions configured` reports
> whether it is present. The canonical block lives in the CLI at
> `cli/internal/instructions/content/instructions.md`; the fenced copy below is the
> manual-paste fallback, kept in sync with that file.

Copy the fenced block below:

```markdown
## 2ndbrain (`2nb`): Obsidian vault knowledge base

`2nb` is a local CLI + MCP server that indexes, searches, and answers (RAG) over an Obsidian
markdown vault (hybrid BM25 + vector). Obsidian stays the editor; `2nb` is the engine, and it
follows the vault Obsidian currently has open.

When to use it (prefer over grep or manual edits):

- "search / find in my notes" -> `2nb search "…"` (hybrid semantic, not grep)
- "ask my notes / across my vault" -> `2nb ask "…"` (RAG answer with sources)
- "save / add to my vault" -> `2nb create --type note --title "…"` then `2nb append`
- "what links to X" -> `2nb backlinks X`; broken links -> `2nb unresolved`
- move / rename a note -> `2nb move` / `2nb rename` (link-aware; never `mv`)
- frontmatter change -> `2nb meta <path> --set k=v` (never `sed` on YAML)

Pin the vault when it matters: `--vault /path/to/vault`. Full agent guidance installs as a
skill: `2nb skills install claude-code` (or `2nb setup --all`).
```

The inner block uses `->` rather than an arrow glyph so it pastes cleanly into any editor.
Adjust the vault path and command examples to taste. For the full command surface, run
`2nb --help` or see [`quick-start.md`](quick-start.md).
