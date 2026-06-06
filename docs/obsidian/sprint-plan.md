---
id: efc145b6-afaa-446c-9ffb-8a60316a0b6d
title: "Sprint Plan: Pivot 2nb to Obsidian-Native"
type: note
status: complete
---

# Sprint Plan: Pivot 2nb to Obsidian-Native

## Overview

This document details the development sprint plan to pivot 2nb from a standalone vault model to an Obsidian-native companion CLI and MCP server. The 0.5.0 milestone represents a clean break from the UUID-first, file-mutating paradigm.

## Progress Tracker

| Sprint | Name | Status | Target |
| --- | --- | --- | --- |
| 1 | Database Identity and SchemaV3 Integration | Complete | Milestone 0.5.0-alpha |
| 2 | Obsidian Wikilink Resolver and Parser Gaps | Complete | Milestone 0.5.0-alpha |
| 3 | Order and Comment-Preserving Frontmatter | Complete | Milestone 0.5.0-alpha |
| 4 | Coexistence, Detection, and Migration Command | Complete | Milestone 0.5.0-alpha |
| 5 | Search, RAG, and MCP Refactoring (M-Freeze) | Complete | Milestone 0.5.0-alpha |
| 6 | macOS App Repositioning to Configuration GUI | Complete | Milestone 0.5.0-beta |
| 7 | Thin TypeScript Obsidian Community Plugin | Complete | Milestone 0.5.0-beta |
| 8 | Canvas/Bases Indexing and Release Engineering | Complete | Milestone 0.5.0 |

**Critical Path:** Sprint 1 -> Sprint 2 -> Sprint 3 -> Sprint 4 -> Sprint 5 (M-Freeze) -> {Sprint 6, Sprint 7} -> Sprint 8.

---

## Sprint 1: Database Identity and SchemaV3 Integration

**Goal:** Establish path-based identity using an internal surrogate database ID. Enable migrations for SQLite schema v3 to support aliases and block references without requiring UUIDs in markdown frontmatter.

* **Estimated effort:** 1 session
* **Status:** Not Started

### Tasks

* [ ] **Update Database Schema in [cli/internal/store/db.go](../../cli/internal/store/db.go)**
  * Increase `MaxSchemaVersion` to 3.
  * Wire in `schemaV3Statements` from [cli/internal/store/migrations.go](../../cli/internal/store/migrations.go) into the migration runner.
  * Update the documents table constraints: add `path UNIQUE` and ensure the internal surrogate ID is self-generated.
* [ ] **Refactor Document Insertion in [cli/internal/store/store.go](../../cli/internal/store/store.go)**
  * Allow insertions without frontmatter UUIDs.
  * Implement path-identity resolution where the relative file path acts as the unique user-facing identifier.
* [ ] **Add Database Migration Tests in [cli/internal/store/store_test.go](../../cli/internal/store/store_test.go)**
  * Test v2 to v3 SQLite upgrades.
  * Verify that document renames update the path field without invalidating existing vector embeddings.

### Definition of Done
* Database schema migrates to version 3 on launch.
* CLI compile succeeds with `CGO_ENABLED=1` and `-tags fts5`.
* SQLite migrations run cleanly without deleting existing database records.

---

## Sprint 2: Obsidian Wikilink Resolver and Parser Gaps

**Goal:** Implement the Obsidian wikilink resolution algorithm (shortest unique path, aliases, anchors) and expand markdown link and embed extraction.

* **Estimated effort:** 2 sessions
* **Prerequisite:** Sprint 1
* **Status:** Not Started

### Tasks

* [ ] **Create Resolution Algorithm in `cli/internal/document/resolver.go`**
  * Resolve by basename, short relative path, or aliases.
  * Support `#heading` and `#^block` anchors.
* [ ] **Update Parser in [cli/internal/document/document.go](../../cli/internal/document/document.go)**
  * Extract standard markdown links `[text](path/to/note.md)`.
  * Extract embedded files and transclusions `![[note]]`.
* [ ] **Read Settings from `.obsidian/app.json`**
  * Check the value of `useMarkdownLinks` and configure resolution behavior accordingly.

### Definition of Done
* Resolver passes unit tests for colliding filenames (shortest unique path matching).
* Link graph indexes markdown-style links and embeds.

---

## Sprint 3: Order and Comment-Preserving Frontmatter

**Goal:** Implement AST-based YAML editing using `yaml.Node` to prevent comment stripping, key reordering, and formatting alterations. Support plain markdown documents by dropping required fields.

* **Estimated effort:** 1.5 sessions
* **Prerequisite:** Sprint 1
* **Status:** Not Started

### Tasks

* [ ] **Create Surgical YAML Editor in [cli/internal/document/frontmatter.go](../../cli/internal/document/frontmatter.go)**
  * Use `yaml.Node` to search and replace values in-place.
  * Ensure key order and comments remain unchanged.
* [ ] **Relax Parser Constraints in [cli/internal/document/document.go](../../cli/internal/document/document.go)**
  * Stop rejecting markdown files that do not have `id`, `type`, or `status` frontmatter fields.
  * Default missing `type` to `note` internally during indexing without modifying the source file.

### Definition of Done
* Writing to frontmatter leaves YAML comments intact.
* `2nb lint` does not fail on plain markdown notes that lack frontmatter blocks.

---

## Sprint 4: Coexistence, Detection, and Migration Command

**Goal:** Establish vault detection using the `.obsidian/` directory, demote `.2ndbrain/` to a gitignored sidecar, and build the `2nb migrate` CLI command.

* **Estimated effort:** 1 session
* **Prerequisites:** Sprint 1, Sprint 3
* **Status:** Not Started

### Tasks

* [ ] **Implement Vault Detection in [cli/internal/vault/vault.go](../../cli/internal/vault/vault.go)**
  * Search parent directories for `.obsidian/` to identify the vault root.
* [ ] **Create `.gitignore` Management Logic**
  * Automatically append `.2ndbrain/` to the vault's `.gitignore` if not already present.
* [ ] **Implement `2nb migrate` in `cli/internal/cli/migrate.go`**
  * Convert legacy vaults to the path-identity format by consolidating `.2ndbrain/` metadata.
  * Do not edit the source markdown files unless explicitly requested.

### Definition of Done
* `2nb index` auto-detects the vault and initializes the `.2ndbrain/` directory.
* `2nb migrate` command transitions a legacy database successfully.

---

## Sprint 5: Search, RAG, and MCP Refactoring (M-Freeze)

**Goal:** Refactor search, RAG chat, and MCP tools to use path-based identity. Establish the M-Freeze baseline.

* **Estimated effort:** 2 sessions
* **Prerequisites:** Sprint 1, Sprint 2, Sprint 3
* **Status:** Not Started

### Tasks

* [ ] **Refactor CLI Commands**
  * Update `search`, `read`, `meta`, and `ask` to accept path inputs instead of UUIDs.
* [ ] **Update MCP Server in [cli/internal/mcp/server.go](../../cli/internal/mcp/server.go)**
  * Adapt `kb_read`, `kb_update_meta`, `kb_list`, and `kb_search` contracts to return paths.
* [ ] **Implement Fallback Titles**
  * If a note does not specify a `title` in its frontmatter, fall back to its file basename.

### Definition of Done (M-Freeze)
* MCP contract tests pass using path identifiers.
* AI agents can query the vault using paths.

---

## Sprint 6: macOS App Repositioning to Configuration GUI

**Goal:** Remove editor panels from the Swift application. Reposition the app as a status, indexing, and configuration manager.

* **Estimated effort:** 2 sessions
* **Prerequisite:** M-Freeze
* **Status:** Not Started

### Tasks

* [ ] **Modify App Layout in Swift UI**
  * Remove `EditorArea.swift` and preview panes.
  * Implement configuration panels for AI providers and vault indexing status.

### Definition of Done
* macOS app compiles and launches via `open`.
* App successfully displays indexing status and configurations without offering editing options.

---

## Sprint 7: Thin TypeScript Obsidian Community Plugin

**Goal:** Build the Obsidian community integration plugin to execute search and RAG queries by calling the CLI.

* **Estimated effort:** 1 session
* **Prerequisite:** M-Freeze
* **Status:** Not Started

### Tasks

* [ ] **Create Plugin Skeleton**
  * Set up `main.ts` and `manifest.json`.
  * Implement commands to shell out to the local `2nb` binary.

### Definition of Done
* Plugin loads in Obsidian.
* Queries trigger command execution and show results in the editor UI.

---

## Sprint 8: Canvas/Bases Indexing and Release Engineering

**Goal:** Implement read-only indexing for JSON Canvas and YAML Bases. Reframe requirements and prepare 0.5.0 release templates.

* **Estimated effort:** 1.5 sessions
* **Prerequisites:** S1–S7
* **Status:** Not Started

### Tasks

* [ ] **Add Canvas Parser in `cli/internal/document/canvas.go`**
  * Parse `.canvas` files for card text and links.
* [ ] **Add Bases Parser in `cli/internal/document/base.go`**
  * Index key-value configurations inside `.base` files.
* [ ] **Reframe [reqs.md](../../reqs.md)**
  * Formally mark legacy requirements as superseded or surviving.
* [ ] **Bump Version to 0.5.0**
  * Execute `make bump-minor` to update version files.

### Definition of Done
* Canvas files are parsed and represented in search results.
* `2nb lint docs/obsidian/` passes.
