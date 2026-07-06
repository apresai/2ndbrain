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
