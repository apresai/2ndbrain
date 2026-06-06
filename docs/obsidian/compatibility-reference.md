---
id: 7ecbabcd-6df5-4899-81a0-84bd6a05be3c
title: "Obsidian Compatibility Reference"
type: note
status: complete
---

# Obsidian Compatibility Reference

This document serves as the central compatibility matrix comparing Obsidian behaviors with 2ndbrain version 0.4.x and the 0.5.0 target architecture. Detailed specification files for each area are linked below.

## Topic Files

* [vault-coexistence.md](vault-coexistence.md): Vault detection rules, sidecar structures, and ignored path patterns.
* [ofm-syntax.md](ofm-syntax.md): Syntax guidelines for wikilinks, embeds, callouts, block references, and metadata.
* [wikilink-resolution.md](wikilink-resolution.md): Exact path matching and lookup rules.
* [canvas-and-bases.md](canvas-and-bases.md): Specification for JSON Canvas and YAML Bases integration.

## Compatibility Matrix

| Feature | Obsidian Behavior | 2ndbrain 0.4.x Behavior | 2ndbrain 0.5.0 Target | Reference File |
| --- | --- | --- | --- | --- |
| **Vault Marker** | Recognizes `.obsidian/` folder | Recognizes `.2ndbrain/` folder | Recognizes `.obsidian/` with gitignored `.2ndbrain/` sidecar | [vault-coexistence.md](vault-coexistence.md) |
| **Document Identity** | Relative path or filename | Frontmatter UUID (`id`) | Path-based relative identity | [identity-model.md](identity-model.md) |
| **File Mutation** | Reads/writes markdown files | Automatically inserts UUID and timestamp properties | Guarantees non-mutating indexing operations | [vault-coexistence.md](vault-coexistence.md) |
| **YAML Editing** | Preserves comments and key ordering | Reorders keys alphabetically; strips comments | Preserves exact file format, comments, and spacing | [vault-coexistence.md](vault-coexistence.md) |
| **Wikilink Syntax** | Resolves `[[note]]`, `[[note\|alias]]`, `[[note#anchor]]` | Resolves `[[note]]` only | Resolves full wikilink anchors and aliases | [ofm-syntax.md](ofm-syntax.md) |
| **Markdown Links** | Resolves standard links `[text](path.md)` | Ignored by indexer | Fully parsed and represented in link graph | [ofm-syntax.md](ofm-syntax.md) |
| **Embeds / Transclusions** | Renders and indexes content of `![[note]]` | Ignored by indexer | Parsed as document dependencies | [ofm-syntax.md](ofm-syntax.md) |
| **Canvas** | Visual node graph mapping | Ignored by indexer | Read-only index representation of text cards | [canvas-and-bases.md](canvas-and-bases.md) |
| **Bases** | Configuration profile management | Ignored by indexer | Read-only index of structured data blocks | [canvas-and-bases.md](canvas-and-bases.md) |

> [!NOTE]
> All specification details are modeled after standard Obsidian documentation schemas. Version-dependent capabilities noted below must be verified against current specifications during implementation.
