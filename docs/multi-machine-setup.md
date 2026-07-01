# Multi-machine setup: what ports, what stays local

2ndbrain state splits into two buckets. Only one travels with your notes, so a fresh machine
needs a few one-time steps even when the vault itself is already synced.

## The two buckets

### In-vault sidecar (`<vault>/.2ndbrain/`)

Holds `config.yaml` (provider/model preferences, thresholds), `models.yaml` (the user model
catalog plus any saved calibration), `index.db` (BM25 + embeddings), and the local-only
`bench.db` / `metrics.db` / `logs/` / `recovery/` / `mcp/`. `2nb vault create` adds
`.2ndbrain/` to the vault's `.gitignore`, so how it ports depends on how the vault syncs:

- **Vault synced by git:** the whole sidecar is excluded. A fresh clone has no 2nb config and
  no index, so you reconfigure and run `2nb index`.
- **Vault synced by iCloud, Dropbox, or Obsidian Sync (file-level):** `.gitignore` does not
  apply to those tools, so the entire `.2ndbrain/` copies verbatim. That includes the index and
  embeddings AND any stale saved calibration. Tell-tale sign: a second machine that already
  shows `Embeddings: N/N` populated without re-indexing received the sidecar as plain files.

### Machine-local (outside the vault, never ports automatically)

- The `2nb` binary, and the SecondBrain dashboard app. Install per machine via Homebrew.
- **AI credentials:** the Bedrock bearer token in the macOS Keychain (`2nb config set-key
  bedrock`), or `~/.aws` SigV4 credentials. Keychain items do not sync unless you enable iCloud
  Keychain.
- **MCP server wiring** in `~/.claude.json` and the equivalent client configs.
- **The agent skill** in `~/.claude/skills` and other agent skill directories.

## Fresh-machine checklist

```bash
# 1. Install the CLI (and, optionally, the dashboard app)
brew install apresai/tap/twonb
brew install --cask apresai/tap/secondbrain   # optional

# 2. Give it AI credentials (pick one)
2nb config set-key bedrock                     # stores a Bedrock bearer token in the Keychain
#   ...or configure ~/.aws SigV4 credentials

# 3. Wire up the skill + MCP server for your AI clients
2nb setup --all                                # or --client claude-code|warp|codex|...

# 4. Rebuild the index if the sidecar did not come along (git-synced vaults)
2nb index

# 5. Sanity check
2nb ai            # provider ready? embeddings present? threshold sane?
```

## Gotcha: a stale similarity threshold that ported

If a file-synced vault carries a pre-flip saved calibration (for Nova, the old symmetric
`0.65`), `2nb ai` warns and semantic search silently degrades to BM25-only. The asymmetric
query purpose collapsed the cosine scale (true-match cosine p50 is roughly `0.34`), so `0.65`
now rejects every real match. See the "Similarity Threshold" section of the repo `CLAUDE.md`
for the full rationale.

Fix it on the machine that inherited the stale value:

```bash
2nb config set ai.similarity_threshold 0.25    # vault config wins over the saved calibration
2nb models calibrate --save                     # re-derive a correct calibration, or
#   remove the RecommendedSimilarityThreshold line from <vault>/.2ndbrain/models.yaml
```

## Teaching an agent about 2nb on the new machine

Step 3 (`2nb setup --all`) installs a `SKILL.md` that Claude Code and other agents auto-load,
so the "when to use 2nb" guidance travels as one command per machine. For an always-loaded
pointer in a global instructions file, drop in the block from
[`claude-md-snippet.md`](claude-md-snippet.md).
