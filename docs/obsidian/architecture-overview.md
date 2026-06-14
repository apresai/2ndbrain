# Architecture Overview: Obsidian-Native 2ndbrain

This document explains the architecture of the 2ndbrain 0.5.0 companion system. The system design prioritizes absolute vault safety and zero modifications to user Markdown notes.

## Architectural Philosophy

Unlike legacy versions, 2ndbrain 0.5.0 operates as a read-only sidecar companion rather than a standalone vault editor.

```
+-------------------------------------------------------------+
|                     Obsidian Vault Root                     |
|                                                             |
|   +-----------------------+     +-----------------------+   |
|   |    Markdown Notes     |     |   .obsidian/ Folder   |   |
|   |   (Source of Truth)   |     |   (Obsidian Config)   |   |
|   +-----------+-----------+     +-----------------------+   |
|               |                                             |
|               v Read-Only                                   |
|   +-----------+-----------+     +-----------------------+   |
|   |  .2ndbrain/ Directory |     |  2ndbrain Community   |   |
|   |  (Gitignored Sidecar) |     |     Plugin Assets     |   |
|   |                       |     |                       |   |
|   |  * index.db (SQLite)  |     |   * main.js           |   |
|   |  * config.yaml        |     |   * manifest.json     |   |
|   |  * logs/              |     |   * styles.css        |   |
|   +-----------+-----------+     +-----------+-----------+   |
|               ^                             |               |
+---------------|-----------------------------|---------------+
                | Read/Write                  | Exec File
                |                             v
        +-------+-------+             +-------+-------+
        | macOS App GUI |             |  Go CLI (2nb) |
        |  (Dashboard)  |             |  (MCP Server) |
        +---------------+             +---------------+
```

---

## 1. Vault Coexistence and Gitignored Sidecar

* Detection: The system recognizes the active directory as a vault if it contains a `.obsidian` configuration directory.
* Gitignored sidecar: 2ndbrain constructs its configuration files and SQLite indexes under a `.2ndbrain` subdirectory. This folder is added to the vault's `.gitignore` file automatically.
* Non-mutating guarantee: 2ndbrain performs zero modifications to your markdown notes during vault scans. It parses notes and builds internal metadata mappings completely in memory before writing to the database.

---

## 2. Path-Based Identity Model

Legacy versions required injecting unique UUID fields into the YAML frontmatter of markdown files to track documents across renames. The 0.5.0 architecture uses a path-based identity model:

* Relative Path as Stable Identity: Notes are uniquely identified by their relative file path from the vault root, stored as a `UNIQUE NOT NULL` column.
* SQLite Database Schema: The database maps that unique relative path to an internal database ID (a UUID surrogate that is the table's actual `PRIMARY KEY`, `documents.id`).
* Renames and Embeddings: When a note is renamed, the CLI updates the path record. Because vector embeddings are tied to the internal ID, the system preserves existing vector embeddings without re-running the embedding models.
* Title Fallbacks: If a note lacks a YAML frontmatter title, the system falls back to the file's basename.

---

## 3. Component Details

### Go CLI (2nb) and MCP Server
* Framework: Built with Cobra for CLI command definitions and mark3labs/mcp-go for Model Context Protocol integration.
* Storage: SQLite database using fts5 extensions for hybrid keyword ranking.
* AI Providers: Defaults to AWS Bedrock — Claude Haiku 4.5 for generation and Amazon Nova-2 for 1024-dimension embeddings — using your AWS credentials. Ollama (local) and OpenRouter are opt-in, disabled by default and enabled via `2nb ai setup` or the macOS AI Hub.

### macOS App (SecondBrain)
* Architecture: Written in SwiftUI using Swift 6.0 concurrency. It uses GRDB to read the shared SQLite index.
* Role: Repositioned as a configuration dashboard, not an editor. Obsidian remains the editing environment. The sidebar leads with **Home** (the default consolidated screen) and groups five power-user tabs under an **Advanced** section (Vault Status, AI Settings, MCP Server, Git Integration, Validation) to monitor indexing status, configure AI providers (AWS Bedrock by default; Ollama/OpenRouter opt-in), inspect git history, and track MCP server invocations.

### Obsidian Community Plugin (obsidian-2ndbrain)
* Integration: A thin TypeScript package built with esbuild.
* Process Execution: Uses node child_process to invoke the local `2nb` binary safely, parsing JSON stdout streams.
* CLI management: On macOS the plugin can download and manage the `2nb` binary itself — it resolves the latest GitHub release tag at runtime, extracts the binary into its own `bin/` folder, ad-hoc signs it, and strips the quarantine xattr (the CLI release is not notarized).
* First-run wizard: A setup wizard (also on the "2ndbrain AI: Setup wizard" command) walks new users through Download CLI → Connect AI (AWS Bedrock by default) → Index.
* UI Rendering: Plugs into the native Obsidian SuggestModal and MarkdownRenderer to present matching results.
