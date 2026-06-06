# Integration Guide: Connecting AI Tools to 2ndbrain

This guide details how developer tools, IDE extensions, and scripting environments interface with the 2ndbrain CLI and MCP server.

---

## 1. MCP Server Integration

The Model Context Protocol (MCP) server lets AI assistants like Claude Code, Cursor, and Windsurf query your local vault context.

### Configuration

Add the following to your AI client's configuration file (for example, `~/.claude.json` for Claude Code or Cursor's MCP settings panel):

```json
{
  "mcpServers": {
    "2ndbrain": {
      "command": "/usr/local/bin/2nb",
      "args": ["mcp-server"],
      "cwd": "/path/to/your/obsidian-vault"
    }
  }
}
```

### Supported Tools

The MCP server exposes 16 tools to the LLM:

* `kb_info`: Returns a vault overview — name, document types, schemas, counts, and AI status.
* `kb_search`: Runs a hybrid search query with optional filters for document type, status, or tag.
* `kb_read`: Reads document body content or a specific chunk by its heading path.
* `kb_related`: Traverses the link graph to a depth of N and returns connected nodes.
* `kb_create`: Creates a new note in the vault using a document template.
* `kb_update_meta`: Updates YAML frontmatter properties using AST nodes to preserve comments. Refuses `.canvas` and `.base` files, which are indexed read-only.
* `kb_structure`: Returns the heading outline tree of a specific document.
* `kb_delete`: Deletes a note from the disk and removes it from the search index.
* `kb_list`: Lists all documents matching the specified filters.
* `kb_ask`: Performs RAG Q&A retrieval and returns an inline response with citations.
* `kb_index`: Rebuilds the search index and embeddings.
* `kb_suggest_links`: Suggests semantically related documents to link from a given note.
* `kb_polish`: Returns an AI copy-edited version of a document alongside the original for diffing.
* `kb_git_activity`: Lists recent git commits touching vault files.
* `kb_git_diff`: Returns the unified diff of a file versus HEAD.
* `kb_git_status`: Maps vault paths to their porcelain git status.

---

## 2. Shell and Scripting Integration

You can integrate `2nb` into scripts and automation tasks by calling the CLI directly.

### Structured Output Formats

Commands that return list or status data support format flags:
* `--json`: Returns a structured JSON payload.
* `--yaml`: Returns YAML output.
* `--csv`: Returns flat CSV data.
* `--porcelain`: Suppresses progress indicators and ANSI color codes for shell piping.

Example search invocation:
```bash
2nb search "authentication" --json
```

### Exit Codes

Shell scripts should check exit codes returned by `2nb`:
* `0`: Success.
* `1`: Entity not found or path error.
* `2`: Validation or schema parse failure.
* `3`: Stale documents detected.

---

## 3. Obsidian Plugin Execution Details

The Obsidian plugin delegates operations to the `2nb` binary.

### Binary Resolution Pipeline

When executing commands, the plugin resolves the path to the CLI using the following sequence:
1. Configured CLI Path: If the plugin's "2nb CLI Path" setting is not the default `2nb`, that value is used as-is.
2. Plugin-managed binary: A `2nb` the plugin downloaded into its own `bin/` folder wins over Homebrew/PATH probing.
3. macOS Homebrew ARM: Checks `/opt/homebrew/bin/2nb`.
4. macOS Homebrew Intel: Checks `/usr/local/bin/2nb`.
5. Go Binary Folder: Checks `~/go/bin/2nb` inside the user's home folder.
6. System PATH: Defaults to executing `2nb` via the shell environment.

### Safe Process Invocation

The plugin executes commands using `execFile` from Node's `child_process` library. Because `execFile` requires specifying arguments as a list rather than a single string, it eliminates risk of command injection from user inputs.

### Managed CLI Install and Setup Wizard

The plugin does not require a pre-installed CLI. On macOS it can download and manage the `2nb` binary itself: it resolves the latest published GitHub release tag at runtime, downloads the matching `Darwin_<arch>` archive into the plugin's `bin/` folder, ad-hoc signs the binary, and strips the `com.apple.quarantine` xattr (the CLI release is not notarized). A first-run setup wizard (also reachable via the "2ndbrain AI: Setup wizard" command) walks the user through Download CLI → Connect AI (AWS Bedrock by default) → Index. Install the plugin via BRAT (`apresai/2ndbrain`) or by copying `manifest.json`, `main.js`, and `styles.css` from a GitHub release — no npm build is required.

---

## 4. Developer Testing Policies

When writing tests or adding integration points for 2ndbrain:

* No Mock Tests Policy: All tests must run against real local database states or live API endpoints. Simulation mocks and stub implementations are not allowed.
* Temporary Vaults: Unit tests should create temporary vaults using the test utilities under `internal/testutil` to ensure database cleanups between test cases.
* CGO Constraints: SQLite features rely on `CGO_ENABLED=1` and `-tags fts5` during Go compilation. Ensure these flags are set in your local build script.
