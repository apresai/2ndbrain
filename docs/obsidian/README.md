---
id: 4af6267b-2904-4ea9-a015-89db9233a87a
title: Obsidian-Native Integration Hub
type: note
status: complete
---

# Obsidian-Native Integration Hub

This directory contains the design and specification files for the pivot of 2nb to an Obsidian-native AI CLI. Instead of functioning as a standalone, UUID-first vault system that requires importing and exporting, 2nb operates directly on a standard Obsidian vault in place. The CLI adds the AI layer (semantic search, RAG chat, and link intelligence) on top of the user's existing vault while Obsidian remains the core editing environment.

> [!IMPORTANT]
> The documentation in this directory supersedes the legacy import/export and UUID-first models described in the root [press-release.md](../../press-release.md) and [docs/vault-structure.md](../vault-structure.md).

> [!TIP]
> Just want to set it up? Start with **[getting-started.md](getting-started.md)** — install the CLI, index your vault, turn on AI, and add the Obsidian plugin in ~10 minutes.

## Reading Order

Start with the practical quick start, then read the design documents in the following order:

1. [getting-started.md](getting-started.md): The 10-minute setup — install the CLI, index your vault, enable AI (AWS Bedrock by default), and add the Obsidian plugin.
2. [press-release.md](press-release.md): The product vision and AI value proposition.
3. [prd.md](prd.md): The product requirements for the 0.5.0 milestone.
4. [identity-model.md](identity-model.md): The architecture decision record (ADR) for path-based identity.
5. [compatibility-reference.md](compatibility-reference.md): The master reference matrix for Obsidian format support.
6. [sprint-plan.md](sprint-plan.md): The developmental timeline and dependencies to deliver the 0.5.0 release.
7. [migration-guide.md](migration-guide.md): The user-facing instructions to migrate legacy 2nb vaults to the new format.
8. [user-guide.md](user-guide.md): Guide for vault setup and plugin configuration.
9. [architecture-overview.md](architecture-overview.md): Internal design of the read-only companion system.
10. [integration-guide.md](integration-guide.md): Developer guide for MCP integrations.

## Document Set Directory

| File | Purpose | Status |
| --- | --- | --- |
| [README.md](README.md) | Index and overview hub | Complete |
| [getting-started.md](getting-started.md) | 10-minute quick start (CLI install, index, AI, plugin) | Complete |
| [press-release.md](press-release.md) | PR/FAQ for the Obsidian-native AI CLI | Complete |
| [prd.md](prd.md) | Product Requirements Document for 0.5.0 | Complete |
| [sprint-plan.md](sprint-plan.md) | Sprint breakdown and task checklists | Complete |
| [identity-model.md](identity-model.md) | ADR: Path-based identity decision | Complete |
| [compatibility-reference.md](compatibility-reference.md) | Reference hub for format compatibility | Complete |
| [vault-coexistence.md](vault-coexistence.md) | Vault detection, sidecar layout, and ignore rules | Complete |
| [ofm-syntax.md](ofm-syntax.md) | Obsidian Flavored Markdown support spec | Complete |
| [wikilink-resolution.md](wikilink-resolution.md) | Path resolution and wikilink parsing rules | Complete |
| [canvas-and-bases.md](canvas-and-bases.md) | JSON Canvas and YAML Bases specifications | Complete |
| [migration-guide.md](migration-guide.md) | CLI vault migration instructions | Complete |
| [user-guide.md](user-guide.md) | Ecosystem setup and usage | Complete |
| [architecture-overview.md](architecture-overview.md) | Platform details and model interactions | Complete |
| [integration-guide.md](integration-guide.md) | MCP and process execution integrations | Complete |
