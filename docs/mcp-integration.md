# MCP Integration Guide

2ndbrain exposes your vault as searchable resources to AI coding assistants via the Model Context Protocol (MCP).

## Available Tools

| Tool | Parameters | Description |
|------|-----------|-------------|
| `kb_info` | none | Show vault root, counts, schemas, provider readiness, and suggested next actions |
| `kb_search` | `query` (required), `type`, `status`, `tag`, `limit` | Hybrid BM25 search with structured filters |
| `kb_ask` | `question` (required) | RAG Q&A with source citations |
| `kb_read` | `path` (required), `chunk` | Read full document or specific heading section |
| `kb_list` | `type`, `status`, `tag`, `limit` | List documents with metadata filters |
| `kb_create` | `title` (required), `type` (required) | Create document from template with auto UUID |
| `kb_update_meta` | `path` (required), `fields` (required) | Update frontmatter with schema validation |
| `kb_append` | `path` (required), `text` (required) | Append text to the end of a document body (explicit body write; reindexes + re-embeds) |
| `kb_replace_section` | `path` (required), `section` (required), `text` (required) | Replace the content under one heading, leaving siblings untouched (explicit body write) |
| `kb_related` | `path` (required), `depth` | Find connected documents via wikilink graph |
| `kb_backlinks` | `path` (required) | List resolved inbound links (what links INTO this doc); check before delete/rename |
| `kb_links` | `path` (required) | List outbound links including broken ones (each carries a `resolved` flag) |
| `kb_structure` | `path` (required) | Get heading tree as JSON with chunk IDs |
| `kb_tags` | none | List every tag in the vault with its document count, descending |
| `kb_tasks` | `path` | List GFM checkbox tasks (`- [ ]` / `- [x]`) across the vault, one file, or a directory |
| `kb_delete` | `path` (required) | Delete document from vault and index |
| `kb_index` | none | Rebuild the vault index and refresh embeddings |
| `kb_suggest_links` | `path` (required), `limit` | Suggest semantic wikilinks for a document |
| `kb_polish` | `path` (required) | Generate a polished revision without writing it back |
| `kb_git_activity` | `since_days` | Summarize recent git commits for the vault |
| `kb_git_diff` | `path` (required) | Return a unified diff for one file versus HEAD |
| `kb_git_status` | none | Return porcelain-style git status for tracked and untracked files |

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
