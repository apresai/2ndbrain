# Getting Started with 2ndbrain + Obsidian

2ndbrain adds **AI** to your existing Obsidian vault: semantic search, RAG question-answering, and an indexer that understands wikilinks, aliases, tags, Canvas, and Bases. It defaults to AWS Bedrock and can run fully local (Ollama) if you prefer. Your notes stay plain Markdown on disk. 2ndbrain never rewrites them.

There are three pieces, and you only need the first two to get going:

1. **`2nb` CLI**: the engine. Indexes your vault and runs the AI. (Required.)
2. **Obsidian plugin**: adds Search / Ask AI buttons inside Obsidian. (The thing most people want.)
3. **macOS app**: an optional dashboard for status and AI configuration.

Total setup time: about 10 minutes.

> **Prefer fewer terminal steps?** Skip to [Step 4](#step-4-install-the-obsidian-plugin): install the plugin and its first-run wizard downloads the `2nb` CLI and indexes your vault for you. Connecting AI still needs AWS credentials (or `2nb ai setup`); the wizard checks readiness and points you to it. Steps 1–3 below are the full terminal route.

---

## Step 1: Install the `2nb` CLI

```bash
brew install apresai/tap/2nb
2nb --version
```

> Building from this repo instead? Run `cd cli && make build && sudo make install` (installs to `/usr/local/bin/2nb`).

---

## Step 2: Point it at your vault and index it

Your Obsidian vault is any folder that contains a `.obsidian/` directory. You do **not** need to "create" or convert anything, just index it:

```bash
cd /path/to/your/obsidian-vault
2nb index
```

The first time, 2nb creates a `.2ndbrain/` folder for its database and adds it to your vault's `.gitignore` (your Markdown is untouched). `index` scans every `.md`, `.canvas`, and `.base` file, and builds the search index.

Check it worked:

```bash
2nb vault status
2nb search "something you know is in your notes"
```

Search works immediately on keywords. The AI features (semantic ranking + Ask) need Step 3.

> Coming from an older 2ndbrain vault? Run `2nb migrate` once (it upgrades the database and never touches your notes), then `2nb index`.

---

## Step 3: Turn on AI (default: AWS Bedrock)

By default 2ndbrain uses **AWS Bedrock** (Claude **Haiku 4.5** for answers and **Nova-2** for embeddings) with your existing AWS credentials. Run the wizard:

```bash
2nb ai setup
```

It detects your AWS credentials (`~/.aws/credentials`, `AWS_PROFILE`, or `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`), confirms the region, and verifies the models. One-time AWS gotcha: enable access to the Claude + Nova models in the AWS console under **Bedrock → Model access** for your region.

**No AWS account?** The same wizard lets you opt into an alternative (these are off by default):

- **Ollama**: fully local + private, nothing leaves your machine. `brew install ollama`, then the wizard pulls the models for you.
- **OpenRouter**: one API key for many hosted models.

Then build embeddings and confirm:

```bash
2nb index         # also generates embeddings
2nb ai status     # provider + models should read "ready"
2nb ask "what did I decide about X?"
```

---

## Step 4: Install the Obsidian plugin

The plugin is a thin wrapper around the `2nb` CLI, and **it can download and manage the CLI for you**, so most of setup happens inside Obsidian. (If you already ran Steps 1–3, the plugin just detects your existing `2nb`.) Connecting AWS Bedrock still needs your AWS credentials, which the wizard verifies and guides you to set.

1. **Install the plugin.** Simplest if you already have the CLI (Steps 1–3): run `2nb plugin install` from inside the vault; it downloads the built `manifest.json` / `main.js` / `styles.css` from the latest release into `<vault>/.obsidian/plugins/obsidian-2ndbrain/`. Or via [BRAT](https://github.com/TfTHacker/obsidian42-brat) for auto-updates: BRAT → *Add beta plugin* → `apresai/2ndbrain`. Or manually: download those three files from the latest [release](https://github.com/apresai/2ndbrain/releases) into the same folder.

2. **Enable it:** Settings → Community plugins → (turn off Restricted mode if needed) → enable **2ndbrain AI**.

3. **Run the first-run wizard.** It opens automatically the first time (or Command Palette → *2ndbrain AI: Setup wizard*):
   - **Download 2nb CLI**: one click installs the binary into the plugin folder (macOS; it's ad-hoc signed and de-quarantined for you). Already installed via `brew install apresai/tap/2nb`? The wizard detects it.
   - **Connect AI**: confirms AWS Bedrock is ready, or shows how to set it up (you supply AWS credentials; or opt into Ollama/OpenRouter via `2nb ai setup`).
   - **Index now**: builds the search index.

> Want a custom binary location? Settings → 2ndbrain AI → **2nb CLI Path** (the default `2nb` checks the plugin's managed copy, then `/opt/homebrew/bin`, `/usr/local/bin`, `~/go/bin`, then PATH).

---

## Step 5: Use it inside Obsidian

Open the Command Palette (`Cmd/Ctrl+P`) and run:

| Command | What it does |
|---|---|
| **2ndbrain AI: Semantic Search** | Hybrid keyword + meaning search; pick a result to jump to that note/section. |
| **2ndbrain AI: Ask AI (RAG Q&A)** | Ask a question; get an answer grounded in your notes, with source links. |
| **2ndbrain AI: Find Similar Notes** | From the current note, find semantically related notes. |
| **2ndbrain AI: Rebuild AI Index** | Re-index after adding/editing a lot of notes. |

The status bar shows the indexing state. Re-run **Rebuild AI Index** (or `2nb index` in a terminal) whenever you've added many notes.

---

## (Optional) Step 6: Let your AI coding agent use the vault

`2nb` is also an **MCP server**, so tools like Claude Code or Cursor can search and ask your vault directly.

```bash
2nb mcp-setup          # prints ready-to-paste config for each client
2nb skills install     # writes a SKILL.md that teaches agents how to use 2nb
```

For Claude Code, add to `~/.claude.json`:

```json
{
  "mcpServers": {
    "2ndbrain": { "command": "2nb", "args": ["mcp-server"], "cwd": "/path/to/your/obsidian-vault" }
  }
}
```

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| Plugin says "Could not find 2nb CLI" | Set an absolute **2nb CLI Path** in plugin settings (run `which 2nb` to find it). |
| Search returns nothing | Run `2nb index` (or **Rebuild AI Index**): the vault isn't indexed yet. |
| Ask AI errors / no semantic results | `2nb ai status`: your provider/model isn't ready; re-run `2nb ai setup`. |
| Rebuild seems stuck on a big vault | Large vaults can take minutes to embed; run `2nb index` in a terminal to watch progress. |
| "not an Obsidian vault" | Make sure you're inside the folder that contains `.obsidian/`. |

## Where things live

- Your notes: plain `.md`/`.canvas`/`.base` files (never modified by 2nb).
- 2nb's index + config: `.2ndbrain/` in your vault root (gitignored automatically).
- Plugin: `<vault>/.obsidian/plugins/obsidian-2ndbrain/`.

See also: [user-guide.md](user-guide.md) (fuller reference), [README.md](README.md) (architecture), [ofm-syntax.md](ofm-syntax.md) (which Obsidian syntax is supported).
