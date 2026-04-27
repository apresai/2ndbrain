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
| `kb_related` | `path` (required), `depth` | Find connected documents via wikilink graph |
| `kb_structure` | `path` (required) | Get heading tree as JSON with chunk IDs |
| `kb_delete` | `path` (required) | Delete document from vault and index |
| `kb_index` | none | Rebuild the vault index and refresh embeddings |
| `kb_suggest_links` | `path` (required), `limit` | Suggest semantic wikilinks for a document |
| `kb_polish` | `path` (required) | Generate a polished revision without writing it back |
| `kb_git_activity` | `since` | Summarize recent git commits for the vault |
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

Or use the CLI:

```bash
claude mcp add 2ndbrain -- 2nb mcp-server
```

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
