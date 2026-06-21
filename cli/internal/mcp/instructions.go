package mcp

// ServerInstructions is returned in the MCP initialize response (the top-level
// "instructions" field) so a connected client folds 2ndbrain into its
// session-start "MCP Server Instructions" summary and routes to the right
// tools. Without it the server sends no instructions, so a healthy
// connected-but-silent server can be misdiagnosed as absent or failed.
//
// Keep this to ONE line and accurate to the registered kb_* tools — every tool
// named below exists (see mcpToolRegistrations). A future `mcp doctor`
// self-test can report "instructions present" by checking this constant is
// non-empty.
const ServerInstructions = "2ndbrain: hybrid search, RAG Q&A, and structured editing over an Obsidian markdown vault. Call kb_info first; use kb_search (hybrid BM25+vector) and kb_ask (RAG answers with source citations) to find and answer; kb_list/kb_read to enumerate and fetch; kb_create/kb_append/kb_replace_section/kb_update_meta to write; kb_backlinks/kb_links/kb_related for the [[wikilink]] graph; kb_tags/kb_tasks/kb_structure and kb_git_* for metadata and history."
