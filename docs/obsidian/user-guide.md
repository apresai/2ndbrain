# User Guide: 2ndbrain AI native Knowledge Base

This guide covers setting up, configuring, and using the 2ndbrain companion ecosystem to add AI capabilities (semantic search and RAG Q&A) to your Obsidian vault. AI runs on AWS Bedrock by default, with fully-local Ollama and OpenRouter available as opt-in alternatives.

## Ecosystem Components

The 2ndbrain ecosystem consists of three parts:
* Go CLI: Command line tool and MCP server that indexes your vault and runs the AI (AWS Bedrock by default; Ollama/OpenRouter opt-in).
* macOS App: Companion status dashboard (Home, Models, Notes, Testing, Health, Activity, Settings) with configuration in the Settings window (Cmd+,) and the sidebar's Settings entry. It is not an editor and never modifies your notes. Obsidian is the editor.
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
open /Applications/SecondBrain.app
```

### Configuration and Status

Configuration lives in Settings: the Settings window (**Cmd+,**) and the sidebar's last entry host the same four tabs (General, AI, Advanced, Integrations). The rest of the sidebar holds the status panels:
* Home: Consolidated common-case screen with a vault card (an Obsidian-match badge and a plugin install/update row), an AI card (Bedrock + Claude Haiku 4.5 + Nova-2 with a ready dot and Test), an AI Clients summary (how many AI clients can reach this vault, linking to Settings → Integrations, where the per-client configuration lives), and an index card (doc/embedding counts, an "awaiting embedding" hint, and Sync / Re-embed All; notes edited in Obsidian re-index automatically).
* Models: Pick Bedrock vendors (sticky), **Validate** (list + invoke), then choose Answers and Search from models this account can actually call. Cost and last-test latency sit on each row. Thinking depth (Off/Low/Medium/High) appears only for mantle models (Grok, GPT-5.5). The old full catalog lives behind a disclosure.
* Notes: Scans for broken wikilinks and YAML frontmatter schema errors, with one-click repairs and guided link fixes.
* Testing: Everything measurable in one destination: **Validate** (the Models tab's validation flow plus model discovery) · **Benchmarks** (per-model probes, favorites, a compare matrix) · **Performance** (index and query timings) · **Quality** (retrieval scorecard, answer grades, tuning sweep).
* Health: **Vault** (path, document count, index coverage, embedding portability, stale notes) · **Updates** (app, CLI, and plugin versions against the latest release).
* Activity: **Git** (recent commits and uncommitted changes) · **MCP Server** (connected clients like Cursor or Claude Code, and their tool execution logs).

Settings (**Cmd+,**, or the sidebar's last entry) is where configuration lives:

* General: the active vault (it follows Obsidian) and the Obsidian plugin.
* AI: your provider, region, and API key — stored in `~/.config/2nb/bedrock.json`, which the dashboard and a terminal `2nb` both read, never in the vault. A new key is probed before it is saved (a rejected key does not replace a working one). **Test everything** calls the active models for real and tells you whether your key was accepted. Model *choice* lives on the Models tab after a vault is bound.
* Advanced: the tuning knobs (similarity threshold, hybrid weights, RAG budgets, embed concurrency, and more).
* Integrations: which AI tools can reach this vault, with a one-click Configure per client.

---

## 3. Installing the Obsidian Plugin

The plugin lets you search and query your knowledge base directly from Obsidian, and it can download and manage the `2nb` CLI for you.

### Installation

- **Via the CLI (simplest):** if you already have `2nb`, run `2nb plugin install` from inside your vault; it downloads the built `manifest.json` / `main.js` / `styles.css` from the latest release into `<vault>/.obsidian/plugins/obsidian-2ndbrain/`.
- **Via BRAT (auto-updating):** install the [BRAT](https://github.com/TfTHacker/obsidian42-brat) plugin, then *Add beta plugin* → `apresai/2ndbrain`. BRAT pulls the same three files and checks for new releases on its own.
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
* **AI Clients:** one row per supported client (Claude Code, Warp, Claude Desktop, Codex), each showing skill-installed status (where the client has a skill), MCP-configured status (from `2nb mcp configured --all --json`; "configured" is the durable check, since clients launch the server on demand), and, for claude-code/claude-desktop/codex, a Global instructions status (from `2nb instructions configured --all --json`). A per-client **Configure** button shells `2nb setup --client <key>` (vault-pinned), with a Copy-setup-snippet fallback (shell-quoted for the Codex `codex mcp add` line).
* **Components:** CLI, macOS app, and Obsidian plugin rows showing each one's installed version, whether it is in sync with the latest release, and the fix command for any gap, plus an **Update plugin** button that shells `2nb plugin install`. Sourced from `2nb doctor --versions --json` (the free, no-model-calls form; the plugin retries the legacy flagless form only when the CLI provably rejects `--versions`), and it degrades per-row with the resolved path plus `--version` when doctor is unavailable.

---

## 4. Querying and Searching in Obsidian

The plugin registers commands in your Obsidian Command Palette:

* Semantic Search: Perform fuzzy semantic search query matching. Suggestions include matching headings and vector similarity percentages. Selecting a suggestion opens the note and scrolls to the matched section.
* Ask AI (RAG Q&A): Enter questions about your vault data. 2ndbrain retrieves context chunks using hybrid search and streams the answer. Source note links appear as tags at the bottom.
* Find Similar Notes: Right click or open the Command Palette from a note to run a similarity search based on the active file name.
* Rebuild AI Index: Refresh the search index without leaving Obsidian.
* Open chat: toggle the right-sidebar chat panel (also via the ribbon icon). The panel holds a true multi-turn conversation: each message passes prior turns to `2nb ask --history -` over stdin, and answers render with degradation warnings and source chips (shared with the Ask AI modal). An older CLI without `--history` degrades to single-shot with an upgrade hint.
* Polish current note: available from the command palette, the sparkle ribbon icon, the note-header toolbar action, and the right-click editor menu. It runs `2nb polish <path> --write --json --links --repair-links` (apply-then-review, after flushing unsaved edits so the external write cannot clobber them), then opens a colored line-diff modal with **Keep** / **Undo**; Undo shells `polish --undo`, confirming before `--force` if the note changed since. A single-flight lock serializes the four trigger surfaces.
