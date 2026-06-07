# User Guide: 2ndbrain AI native Knowledge Base

This guide covers setting up, configuring, and using the 2ndbrain companion ecosystem to add AI capabilities (semantic search and RAG Q&A) to your Obsidian vault. AI runs on AWS Bedrock by default, with fully-local Ollama and OpenRouter available as opt-in alternatives.

## Ecosystem Components

The 2ndbrain ecosystem consists of three parts:
* Go CLI: Command line tool and MCP server that indexes your vault and runs the AI (AWS Bedrock by default; Ollama/OpenRouter opt-in).
* macOS App: Companion status and configuration dashboard (Vault Status, AI Settings, MCP Server, Git Integration, Validation). It is not an editor and never modifies your notes. Obsidian is the editor.
* Obsidian Plugin: Thin community plugin that connects the Obsidian UI with the CLI.

---

## 1. Setting up the Go CLI

The Go CLI is the core engine. It manages your local SQLite database index under `.2ndbrain/index.db`.

### Installation

Install via Homebrew (recommended):
```bash
brew install apresai/tap/2nb
```
Or build from this repo:
```bash
cd cli && make build && sudo make install   # installs to /usr/local/bin/2nb
```

### Initializing and Indexing your Vault

To start using 2ndbrain with an existing Obsidian vault (any folder containing a `.obsidian/` directory):
1. Navigate to the root directory of your vault:
   ```bash
   cd /path/to/my/obsidian-vault
   ```
2. Build the first index:
   ```bash
   2nb index
   ```
   The first run auto-creates a gitignored `.2ndbrain/` subdirectory for the database (your Markdown is never modified), then scans all Markdown, Canvas, and Base files, parses links and content, and builds the SQLite index. You do **not** need to run `2nb init` on an existing Obsidian vault. `init`/`vault create` is only for scaffolding a brand-new vault from scratch.

---

## 2. Using the macOS Dashboard

The macOS app acts as a companion control center. It does not edit your markdown files.

### Launching the Dashboard

Open the app using the Finder or terminal:
```bash
open ~/Applications/SecondBrain.app
```

### Configuration and Status

Once launched, use the sidebar to switch between panels:
* Vault Status: Shows the loaded vault path, document count, and index updates.
* AI Settings: Connects to your AI provider (AWS Bedrock by default: Claude Haiku 4.5 + Nova-2 embeddings; Ollama/OpenRouter are opt-in) and configures embedding and generation models.
* MCP Server: Tracks connected clients (like Cursor or Claude Code) and lists tool execution logs.
* Git Integration: Displays recent commit histories and uncommitted modifications.
* Validation: Scans for broken wikilinks and YAML frontmatter schema errors.

---

## 3. Installing the Obsidian Plugin

The plugin lets you search and query your knowledge base directly from Obsidian, and it can download and manage the `2nb` CLI for you.

### Installation

- **Via BRAT (recommended):** install the [BRAT](https://github.com/TfTHacker/obsidian42-brat) plugin, then *Add beta plugin* → `apresai/2ndbrain`. BRAT pulls the built `manifest.json` / `main.js` / `styles.css` from the latest release, no local build needed.
- **Manual:** download those three files from the latest [release](https://github.com/apresai/2ndbrain/releases) into `<vault>/.obsidian/plugins/obsidian-2ndbrain/`.

Then enable it: Settings → Community plugins → **2ndbrain AI**.

### First-run wizard

On first enable, the plugin opens a setup wizard (also available via Command Palette → *2ndbrain AI: Setup wizard*):
1. **Download 2nb CLI**: installs the binary into the plugin folder (macOS), or detects an existing `2nb` (e.g. from `brew install apresai/tap/2nb`).
2. **Connect AI**: checks AWS Bedrock readiness; if your AWS credentials aren't set, it points you to `2nb ai setup`.
3. **Index now**: builds the search index.

### Adjusting Settings

In the plugin settings tab:
* **Download / update CLI:** fetch or refresh the managed `2nb` binary.
* **2nb CLI Path:** absolute path to your `2nb` binary if you aren't using the managed copy or PATH.
* **Vault:** read-only — shows the vault `2nb` is bound to (always the open Obsidian vault) and its index state. The plugin pins every command to the open vault, so it can never operate on a different one.

---

## 4. Querying and Searching in Obsidian

The plugin registers commands in your Obsidian Command Palette:

* Semantic Search: Perform fuzzy semantic search query matching. Suggestions include matching headings and vector similarity percentages. Selecting a suggestion opens the note and scrolls to the matched section.
* Ask AI (RAG Q&A): Enter questions about your vault data. 2ndbrain retrieves context chunks using hybrid search and streams the answer. Source note links appear as tags at the bottom.
* Find Similar Notes: Right click or open the Command Palette from a note to run a similarity search based on the active file name.
* Rebuild AI Index: Refresh the search index without leaving Obsidian.
