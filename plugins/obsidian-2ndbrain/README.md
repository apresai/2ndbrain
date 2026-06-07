# 2ndbrain AI Obsidian Plugin

This Obsidian plugin is a thin wrapper that shells out to the local `2nb` CLI engine. It brings semantic search, RAG Q&A, and quick indexing directly into the Obsidian workspace. Your markdown is never rewritten; `2nb` indexes your existing vault into a gitignored `.2ndbrain/` sidecar.

All commands appear in the command palette under the **2ndbrain AI:** prefix.

AI defaults to **AWS Bedrock** (Claude Haiku for generation, Amazon Nova for embeddings) via your AWS credentials. Keyword (BM25) search works even before AI is configured. This plugin is **desktop-only**.

## Features

* **2ndbrain AI: Setup wizard:** Guided first-run flow: Download CLI → Connect AI (AWS Bedrock) → Index the vault. Opens automatically on first launch and is re-runnable any time from the command palette.
* **2ndbrain AI: Semantic Search:** Execute hybrid search queries (BM25 keyword + semantic similarity) across all vault markdown notes.
* **2ndbrain AI: Ask AI (RAG Q&A):** Ask questions about your vault and receive answers generated from retrieved context with source citations.
* **2ndbrain AI: Find Similar Notes:** Run a semantic search seeded from the active note's name to surface related documents.
* **2ndbrain AI: Rebuild AI Index:** Build or refresh the search index directly from the command palette.
* **Managed CLI download:** The plugin can fetch and manage the `2nb` binary for you. The settings "Download / update CLI" button (and the wizard) resolve the latest GitHub release, download the matching binary into the plugin folder, ad-hoc sign it, and strip the quarantine attribute. macOS-only; on other platforms install via Homebrew.
* **Status Bar Indicator:** Tracks the indexing state in real time.

## Installation

No npm build is required; users install prebuilt assets.

**Via BRAT (recommended):** Install the [BRAT](https://github.com/TfTHacker/obsidian42-brat) community plugin, then add the beta plugin `apresai/2ndbrain`.

**Manual:** Download `manifest.json`, `main.js`, and `styles.css` from a [GitHub release](https://github.com/apresai/2ndbrain/releases) and copy them into your vault under `.obsidian/plugins/obsidian-2ndbrain/`. Then enable "2ndbrain AI" under Settings → Community plugins.

On first launch the **Setup wizard** opens to walk you through installing the CLI, connecting AI, and indexing. You do not need to run `2nb init` on an existing Obsidian vault. The wizard's "Index now" (or `2nb index`) is all that's needed.

## Configuration

Open the plugin settings tab to configure:

* **2nb CLI binary / Download / update CLI:** Status of the detected `2nb` binary, with a button to download or update a plugin-managed copy (macOS). If you prefer Homebrew, run `brew install apresai/tap/2nb`.
* **2nb CLI Path:** Path to the `2nb` binary. Defaults to `2nb`, which probes a managed copy plus standard locations (`/opt/homebrew/bin/2nb`, `/usr/local/bin/2nb`, `~/go/bin/2nb`) and your PATH automatically.
* **Vault:** A read-only line showing the vault `2nb` operates on (always the open Obsidian vault) and its index state (e.g. `embedded (113 / 113 documents)`). The plugin pins every command to the open vault via `--vault`, so the Obsidian vault and the 2nb vault can never diverge.
