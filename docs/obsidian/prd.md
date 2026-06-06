---
id: 7c96df30-e798-4f79-9ba6-c7f6e96bd604
title: "Product Requirements: Obsidian-Native AI CLI (0.5.0)"
type: prd
status: draft
owner: "Chad Neal"
priority: p0
---

# Product Requirements: Obsidian-Native AI CLI (0.5.0)

## Problem Statement

Developers maintain extensive, valuable knowledge bases in plain markdown files using Obsidian. Currently, 2ndbrain operates as a standalone vault system that requires importing notes, injecting unique identifiers into frontmatter, and using its own editor. This creates friction, duplicate data, and vendor lock-in. Developers need 2ndbrain to act as a non-intrusive AI companion that reads their active Obsidian vault in place and exposes its contents semantically to their terminal and AI coding agents.

## Target Users

The primary target users are developers who use Obsidian as their personal knowledge base and work alongside AI assistants such as Claude Code, Cursor, and Windsurf in their daily coding activities.

## Personas

* **Alex, Software Architect:** Uses Obsidian to write architecture decision records (ADRs) and design specs. Wants Claude Code to automatically read these ADRs to verify that suggested code modifications align with established architecture decisions.
* **Taylor, Operations Engineer:** Maintains a vault of runbooks and postmortems. Needs to query this knowledge base during incident response using terminal-based search and RAG tools.

## User Journeys

1. **Vault Bootstrapping:** Alex runs `2nb index` in an existing Obsidian vault. 2ndbrain recognizes the vault, initializes a gitignored database sidecar, and indexes all documents without writing anything to the markdown files.
2. **AI Q&A:** During a coding session, Alex asks Claude Code to modify a service. Claude Code invokes `kb_search` and `kb_read` via the MCP server to check the vault's architectural guidelines and correctly implements the changes.

## Design Constraints

The 0.5.0 milestone must adhere to the following invariants:

1. **Path-Based Identity:** Documents are identified by their relative vault paths. Filename-based wikilinks are resolved dynamically. Stable UUIDs are optional and never required or injected.
2. **Non-Mutating Frontmatter:** Under no circumstances shall 2ndbrain modify, inject, or rewrite frontmatter fields in the user's markdown files. File writes must preserve comments, formatting, and key order.
3. **Coexistence Strategy:** A vault is defined by the presence of a `.obsidian/` directory. The `.2ndbrain/` directory is demoted to a gitignored, derived sidecar holding the index database, configs, and logs. It must be rebuildable from scratch at any time.

## Goals

* Turn 2ndbrain into an Obsidian-native CLI and MCP server.
* Ensure complete frontmatter and content safety.
* Support all core Obsidian Flavored Markdown (OFM) structures.
* Provide a command-line migration path for legacy 2ndbrain vaults.

## Non-Goals

* Rebuilding or maintaining a full markdown editor inside the 2ndbrain macOS app.
* Editing or modifying markdown file content from the CLI or MCP server (except during explicit user-initiated command operations).

## User Stories

* As a developer, I want to index my existing Obsidian vault without modifying any files so that I can keep my data clean and version-controlled.
* As a developer, I want my AI assistant to resolve wikilinks and read transcluded content so that it understands my knowledge graph.
* As a developer, I want to run queries directly from my terminal and get structured JSON outputs so that I can write custom automation scripts.

## Functional Requirements (P0 / P1)

The functional requirements are grouped by core capabilities. They build upon and reframe the legacy requirements defined in [reqs.md](../../reqs.md).

### 1. Vault Detection and Coexistence (P0)
* **OBN-DET-001:** The CLI shall detect an Obsidian vault by checking for the presence of a `.obsidian/` folder in the working directory or parent directories.
* **OBN-DET-002:** The system shall store all database and cache files in a `.2ndbrain/` subdirectory within the vault root. This subdirectory must be marked as gitignored.
* **OBN-DET-003:** The indexer must build the index database in a way that allows it to be deleted and fully reconstructed without loss of source data.

### 2. Obsidian Flavored Markdown Parsing (P0)
* **OBN-PAR-001:** The parser shall support standard Obsidian wikilinks (`[[target]]`), aliased links (`[[target|display]]`), and heading/block anchors (`[[target#heading]]`).
* **OBN-PAR-002:** The parser shall extract both wikilinks and markdown links (`[text](url)`) for index building.
* **OBN-PAR-003:** The parser shall extract embedded transclusions (`![[embed]]`) and treat them as document attachments/dependencies.

### 3. Wikilink Resolution (P0)
* **OBN-RES-001:** The system shall resolve wikilinks using a shortest-unique-path algorithm, matching Obsidian's resolution logic.
* **OBN-RES-002:** The system shall resolve wikilinks that refer to document aliases defined in the YAML frontmatter.

### 4. Non-Mutating YAML Editing (P0)
* **OBN-MUT-001:** The system shall guarantee that document indexing performs zero file writes.
* **OBN-MUT-002:** Command-line frontmatter modifications (e.g., `2nb meta set`) must preserve key order, comments, and spacing by utilizing AST-based YAML manipulation.

### 5. Semantic Search and RAG (P0)
* **OBN-SEA-001:** The search engine shall support hybrid search (BM25 keyword search and semantic cosine similarity) over vault documents.
* **OBN-SEA-002:** The `ask` command shall perform retrieval-augmented generation using vault chunks as context.

### 6. Canvas and Bases Support (P1)
* **OBN-CAN-001:** The indexer shall parse `.canvas` files to extract card content and link connections for graph indexing.
* **OBN-BAS-001:** The indexer shall index `.base` structured YAML documents.

### 7. Obsidian Community Plugin (P1)
* **OBN-PLU-001:** A thin TypeScript Obsidian plugin shall be provided to trigger indexing and execute search/ask queries from the Obsidian UI.

### 8. macOS Configuration GUI (P1)
* **OBN-GUI-001:** The macOS application shall be converted from an editor into a settings and monitoring dashboard.

### 9. Vault Migration (P0)
* **OBN-MIG-001:** The CLI shall provide a `migrate` command to safely transition legacy 2nb vaults to the native format.

## Cross-Reference Table

| Capability | reqs.md Legacy IDs | New OBN Requirement IDs |
| --- | --- | --- |
| Vault Coexistence | `OBS-EV-001`, `OBS-EV-002` | `OBN-DET-001`, `OBN-DET-002`, `OBN-DET-003` |
| OFM Parsing | `OBS-UW-002` | `OBN-PAR-001`, `OBN-PAR-002`, `OBN-PAR-003` |
| Wikilink Resolution | `OBS-EV-004`, `OBS-UW-001` | `OBN-RES-001`, `OBN-RES-002` |
| Format Coexistence | `OBS-EV-003`, `OBS-EV-005`, `OBS-UW-003` | `OBN-MUT-001`, `OBN-MUT-002`, `OBN-CAN-001`, `OBN-BAS-001` |
| CLI & MCP Operation | `OBS-EV-006`, `OBS-EV-007`, `OBS-ST-001` | `OBN-SEA-001`, `OBN-SEA-002`, `OBN-MIG-001` |

## Non-Functional Requirements

* **Performance:** Vault scanning and indexing must complete in under 5 seconds for a vault containing 1,000 documents.
* **Fidelity:** File metadata updates must be byte-level identical to the original file, except for the specific modified property keys.

## Success Metrics

* 100 percent of legacy test cases verified against the path-based model.
* Zero accidental file modifications reported during vault index operations.
* Sub-second latency for local semantic search queries.

## Risks

* **Path Collisions:** Multiple files with identical basenames in different subdirectories can confuse wikilink resolution. Mitigated by shortest-unique-path matching.
* **YAML Parsing Edge Cases:** Modifying YAML properties using standard libraries can strip comments or alter whitespace. Mitigated by using `yaml.Node` AST operations for edits.

## Milestones and Release Plan

* **Milestone 0.5.0-alpha:** Implementation of path-based database identity, non-mutating parser, and CLI search refactoring.
* **Milestone 0.5.0-beta:** Delivery of the macOS configuration GUI and the thin community plugin.
* **Milestone 0.5.0 (Final):** General availability release.

## Open Questions

* **How should we handle nested folders in the Obsidian community plugin setup?** If a user opens a subdirectory as their project root in Claude Code, the MCP server must traverse upwards to locate the true vault root `.obsidian/` folder.
