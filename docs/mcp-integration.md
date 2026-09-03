# MCP Integration Guide

2ndbrain exposes your vault as searchable resources to AI coding assistants via the Model Context Protocol (MCP).

## Available Tools

| Tool | Parameters | Description |
|------|-----------|-------------|
| `kb_info` | none | Vault overview: name, root, types/schemas, counts, AI readiness. Call first. |
| `kb_search` | `query` (required), `type`, `status`, `tag`, `limit` | Hybrid BM25 plus vector search. Rank by `vector_score` (cosine), not `score` (RRF). Each row also carries `frontmatter`, the note's parsed frontmatter map (additive in 0.22.1; omitted when empty), so metadata is available without a second `kb_read`. |
| `kb_ask` | `question` (required) | RAG answer plus source paths. Verify cites with `kb_read`. If no hits, drop to `kb_search`. |
| `kb_read` | `path` (required), `chunk` | Read a vault-relative path, or one heading via `chunk`. |
| `kb_list` | `type`, `status`, `tag`, `limit` | Enumerate docs by type/status/tag without content. No query. Follow with `kb_read`. |
| `kb_create` | `title` (required), `type` (required), `path` (optional vault-relative subdirectory, created if missing) | Create from template (UUID plus frontmatter). Types: adr, runbook, prd, prfaq, postmortem, note. Search first. |
| `kb_update_meta` | `path` (required), `fields` (required) | Schema-validated frontmatter only. Status transitions are enforced. |
| `kb_append` | `path` (required), `text` (required) | Append text to body end, then reindex. Frontmatter untouched. |
| `kb_replace_section` | `path` (required), `section` (required), `text` (required) | Replace one heading body. Call `kb_structure` first for heading names. |
| `kb_related` | `path` (required), `depth` | Wikilink graph from path, depth N (default 2). For semantic "should link", use `kb_suggest_links`. |
| `kb_backlinks` | `path` (required) | Resolved inbound links to path. Check before delete/rename. |
| `kb_links` | `path` (required) | Outbound links from path, including broken (`resolved=false`). |
| `kb_structure` | `path` (required) | Heading tree for path. Then `kb_read` with `chunk`. |
| `kb_tags` | none | Vault tag vocabulary with counts. |
| `kb_tasks` | `path`, `done`, `todo` | GFM checkboxes (`- [ ]` / `- [x]`) vault-wide or under path. |
| `kb_delete` | `path` (required) | Delete file plus index. Irreversible except via git. |
| `kb_index` | none | Full reindex plus re-embed. Only after bulk external edits or model switch. |
| `kb_suggest_links` | `path` (required), `limit` | Semantic wikilink candidates for path, excluding already-linked. |
| `kb_polish` | `path` (required), `links`, `system` | Copy-edit preview; does not write. |
| `kb_git_activity` | `since_days` | Recent vault commits (default 7 days). No-op if not a git repo. |
| `kb_git_diff` | `path` (required) | Unified diff of path vs HEAD. |
| `kb_git_status` | none | Porcelain map of dirty/untracked vault files. |

## Setup

### Claude Code

Add to `~/.claude.json`:

```json
{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": "/path/to/your/vault"
    }
  }
}
```

Or use the CLI. Pin the vault with `--vault` so the server always serves the
intended vault regardless of where the client launched from:

```bash
claude mcp add 2ndbrain -- 2nb mcp-server --vault /path/to/your/vault
```

**Verify it stuck.** After adding the server, confirm 2nb sees it without
starting the client:

```bash
2nb mcp configured --vault /path/to/your/vault
# Claude Code MCP server: configured (user scope) in ~/.claude.json
```

`mcp configured` reads the client config (`~/.claude.json`) and reports whether
a 2ndbrain server is wired up for this vault. It is the durable "is it set up?"
signal, distinct from `2nb mcp status`, which only sees a server process that is
running right now (the client launches the server on demand, so `status` reads
empty whenever the client is closed). A server pinned to a different vault via
`--vault` or `cwd` correctly reports *not* configured for this one.

The JSON-config clients below pin the vault with `cwd`; the `claude mcp add`
example above uses `--vault` because that CLI doesn't take a `cwd`. Both are
honored equally (`--vault` wins if you set both), so use whichever your client
supports.

### Cursor

Add to `.cursor/mcp.json` in your project:

```json
{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": "/path/to/your/vault"
    }
  }
}
```

### Grok

Grok prefixes each tool as `<server>__<name>` and keeps only names matching
`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`. A server key that starts with a digit
(`2ndbrain`, which Grok also imports from `~/.claude.json`) makes every
prefixed name illegal, so a session records `mcp_server_connected` with
`tool_count` 0 even though `grok mcp doctor 2ndbrain` lists 22 tools (doctor
does not prefix). Use `twonb` (same stem as the Homebrew formula):

```toml
[mcp_servers.twonb]
command = "2nb"
args = ["mcp-server", "--vault", "/path/to/your/vault"]
```

Paste that into `~/.grok/config.toml`. Do not name the server `2ndbrain`.

### Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "2ndbrain": {
      "command": "/usr/local/bin/2nb",
      "args": ["mcp-server"],
      "cwd": "/path/to/your/vault"
    }
  }
}
```

## Usage Examples

Once connected, your AI assistant can use these natural language prompts:

- "Search my vault for authentication decisions" -> triggers `kb_search`
- "Read the JWT ADR" -> triggers `kb_read`
- "Answer this from my vault: how does auth work?" -> triggers `kb_ask`
- "List all runbooks" -> triggers `kb_list`
- "What documents are related to the auth strategy?" -> triggers `kb_related`
- "Create a new ADR for choosing DynamoDB" -> triggers `kb_create`
- "Update the auth ADR status to accepted" -> triggers `kb_update_meta`
- "Show me the structure of the debug runbook" -> triggers `kb_structure`
- "Suggest links for this note" -> triggers `kb_suggest_links`
- "Polish this postmortem draft" -> triggers `kb_polish`
- "What changed recently in this vault?" -> triggers `kb_git_activity`

## Security

- **Path traversal protection**: All tools reject paths containing `..`
- **Vault boundary**: All file operations are restricted to the vault root
- **Sensitive fields**: Frontmatter fields named `secret`, `password`, `token`, or `key` are excluded from search results and MCP responses
- **Local only**: The MCP server runs on stdio transport with no network exposure

## Server internals

### Sidecar status files

Each `2nb mcp-server` process writes a status file to `.2ndbrain/mcp/<pid>.json`
holding its PID, start time, parent PID, and the last 50 tool invocations (tool
name, timestamp, duration, ok/error). mark3labs/mcp-go has no client-connected
hook, so these sidecar files are the only way to enumerate running servers:
`2nb mcp status --json` reads them (filtering entries whose PID is no longer
alive), and the macOS dashboard polls that command every 5 seconds. The file is
removed on graceful shutdown; stale files left by a crash are cleaned up by the
next server start.

### Metrics recording

The server records performance-relevant tool calls to the vault metrics
observatory (`.2ndbrain/metrics.db`, rows tagged `source=mcp`). The op mapping
(`mcpMetricOp` in `cli/internal/mcp/server.go`): `kb_search` records as
`search`, `kb_ask` as `ask`, `kb_index` as `index`, and the reindexing write
tools (`kb_append`, `kb_replace_section`, `kb_create`, `kb_update_meta`) as
`index_doc`. Read-only metadata, graph, and git tools are not metered.

The server holds ONE `*metrics.DB` for its lifetime: opened in `newMCPServer`,
reused across calls so the hot path never opens and closes a database per
invocation, and closed on shutdown. The innermost `wrapMCPMetric` wrapper
records op, latency, and ok best-effort; a metrics failure never affects the
tool result. `wrapMCPMetric` also seeds an `mcpMetricDetail` into the request
context, and the `kb_ask`/`kb_search`/`kb_index` handlers attach their real
token usage and counts (`result_count`, `docs_indexed`, `embedded`,
`total_chars`, `mode`) via `recordMCPDetail`, matching the CLI path, so
agent-driven rows carry tokens and result/doc counts rather than zeros.
`recordMCPDetail` is a no-op when the context carries no detail, so handlers
call it unconditionally without caring whether they are metered.

### Initialize self-announcement

The server self-announces via a one-line `instructions` string in the MCP
initialize response (`mcp.ServerInstructions`, wired through `newMCPServer`,
the single source of truth for server construction shared by `Start`, tests,
and in-process self-tests such as `mcp doctor`). Clients fold it into their
session-start "MCP Server Instructions" summary, so a connected-but-idle
server is not misread as absent.

### Lifecycle: parent-death watchdog and opt-in idle timeout

The server stays alive while its client is connected. It exits instantly when
the client closes the connection (stdin EOF) and promptly when the client
process dies without closing the pipe, so a closed or crashed session never
leaves an orphan holding the index open, without ever killing a
live-but-quiet session. The orphan reaper is a parent-death watchdog
(`cli/internal/mcp/parent.go`): on stdio transport the parent process IS the
MCP client, so a `getppid()` poll that exits when the parent goes away (the OS
reparents the process, changing the value captured at startup) is the precise
orphan signal.

The activity-based idle self-exit (`cli/internal/mcp/idle.go`, a lock-free
atomic last-activity clock plus an in-flight request counter) is opt-in and
OFF by default. Enable an inactivity cap with `--idle-timeout <dur>` or
`$2NB_MCP_IDLE_TIMEOUT` (e.g. `1h`; `0` = never; the flag wins over the env
var).

### `mcp reap`: the backstop

`2nb mcp reap` terminates stale or orphaned `mcp-server` processes for this
vault, using SIGTERM only (never SIGKILL; the server handles SIGTERM cleanly,
and the reaper waits up to ~3 seconds per process for the exit). It reaps
servers whose last activity (or start time, if the server never served a tool)
is older than `--older-than` (default 6h), never the current process and never
an active server, and it re-verifies the sidecar's recorded start time against
the live process before signaling, so a PID reused by an unrelated process is
never signaled. `--dry-run` previews without signaling. JSON output:
`{reaped[], skipped[], threshold, dry_run}`. With the parent-death watchdog
reaping orphans promptly, this command is a rarely-needed backstop.
