---
id: d6186c26-0331-428c-9f7c-9e2edf6dfbbc
title: "Vault Coexistence and Directory Layout"
type: note
status: complete
---

# Vault Coexistence and Directory Layout

This document describes how 2ndbrain coexists with native Obsidian vaults without file modifications.

## Vault Markers

A directory is recognized as an active vault based on the following indicators:

* **Obsidian marker:** The presence of a `.obsidian/` folder at the root directory.
* **2ndbrain sidecar:** A gitignored `.2ndbrain/` folder generated in the same directory to house databases and configurations.

## Sidecar Directory Structure

The `.2ndbrain/` folder is a derived cache sidecar that can be deleted at any time and reconstructed using `2nb index`.

```
vault-root/
├── .obsidian/              # Native Obsidian configuration (never modified by 2ndbrain)
│   ├── app.json
│   └── appearance.json
├── .2ndbrain/              # Derived sidecar directory (gitignored)
│   ├── index.db            # SQLite database containing search index and metadata
│   ├── config.yaml         # AI provider and local configuration profile
│   ├── schemas.yaml        # Schema templates
│   └── logs/               # Application log folder
├── note.md                 # User files
└── subfolder/
    └── architectural-record.md
```

## Read, Write, and Touch Rules

To maintain formatting and ensure data safety, 2ndbrain adheres to strict file interaction boundaries:

### 2ndbrain Reads
* All markdown files (`*.md`) inside the vault folder tree (excluding ignored paths).
* `.canvas` and `.base` files, read-only, to build a synthetic index view (never written back).
* Frontmatter blocks of all scanned documents.

The `.obsidian/` directory is used only as the vault marker; its configuration files are not parsed to resolve links or formatting (wikilink resolution uses 2ndbrain's own shortest-unique-path matching).

### 2ndbrain Writes
* Only `.2ndbrain/` configuration files and databases.
* In-place frontmatter modifications when explicitly triggered by CLI metadata write commands (e.g., `2nb meta set`). These operations utilize AST-based parsing to preserve comments and layout.
* The root `.gitignore` file to add `.2ndbrain/` to the ignore list automatically.

### 2ndbrain Never Touches
* Native `.obsidian/` configuration files.
* Original body content of markdown files.
* Files matching system or user-defined ignore patterns.

## Ignore-Rule Reconciliation

The indexer applies a fixed set of built-in ignore rules. It does not currently read native Obsidian or git ignore patterns:

1. **System Defaults:** The indexer skips hidden directories (any directory whose name starts with `.`, including `.obsidian`) and `node_modules`, and excludes files with security-sensitive basenames (`.env` / `.env.*`, `credentials*`, or any name containing `secret`).
2. **Obsidian Ignored Files (not currently honored):** Exclusion lists defined in `.obsidian/app.json` under `userIgnoreFilters` are not read by the indexer; matching paths are still indexed.
3. **Gitignore Rules (not currently honored):** The indexer does not parse `.gitignore` when walking the vault. Separately, 2ndbrain appends `.2ndbrain/` to the vault-root `.gitignore` so the derived sidecar stays out of version control.
