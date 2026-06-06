---
id: a6adea82-8579-44a2-9630-a94d201bbf47
title: "ADR: Path-Based Identity Model"
type: adr
status: accepted
deciders:
  - "Chad Neal"
---

# Architecture Decision Record: Path-Based Identity Model

## Status

accepted

## Context

Historically, 2ndbrain utilized a UUID-first identity model. Every document in the vault was required to carry a stable `id` field in its frontmatter. If a file did not have this field, the indexer rejected it. This requirement conflicted with the goal of operating on standard Obsidian vaults in a non-mutating manner. Inserting UUIDs into thousands of user files changes git hashes, modifies file timestamps, and disrupts existing user formatting.

Obsidian resolves links and identifies files by their relative path or file name. To integrate seamlessly, 2ndbrain needs to adopt the same path-based identity while still supporting database performance, renames, and link graph stability.

We considered two design options for the database implementation:

* **Option A: Literal Path Primary Key.** Use the relative file path as the database primary key. Under this option, any document rename deletes the old database row and inserts a new one. This triggers a complete re-embedding of the file, which is computationally expensive for large vaults.
* **Option B: Internal Surrogate Key.** Keep a self-generated, database-only unique identifier (which is never written to the markdown file) and add a unique constraint on the path column: `path NOT NULL UNIQUE`. Use the path as the public identity in the CLI, MCP, and search results. Renames update the path column in-place without invalidating chunks or embeddings.

## Decision

We chose **Option B**. The 2ndbrain database will use an internal, self-generated surrogate key for database row tracking, with a `path NOT NULL UNIQUE` column representing the document's identity. 

The CLI and MCP server interfaces will expose relative paths as the unique identifier. 2ndbrain will read and respect UUIDs if they exist in file frontmatter for backward compatibility, but it will never write, generate, or require them.

## Consequences

* **Data Integrity:** Vanilla Obsidian vaults work out of the box with zero modifications to markdown files. Files remain completely untouched by the indexer.
* **Rename Efficiency:** File renames map to simple SQL updates on the path column: `UPDATE documents SET path = ? WHERE id = ?`. Embeddings and chunks are preserved without re-running embedding models.
* **Configuration:** This decision supersedes the UUID-first descriptions found in the root [CLAUDE.md](../../CLAUDE.md) (Vault Portability) and [docs/vault-structure.md](../vault-structure.md).
