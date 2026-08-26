# Contributing to 2ndbrain

Thanks for your interest in contributing to 2ndbrain.

## For AI agents

Opening this repo in an agent (Warp, Claude Code, Cursor, ...) gives you the `2nb` skill automatically: it ships at `.agents/skills/2nb/SKILL.md` (plus `.warp/` and `.claude/` mirrors), the discovery paths agents walk up to find. The skill's source of truth is `cli/internal/skills/content/2ndbrain-skill.md`, embedded into the CLI and installable for any agent via `2nb skills install <agent>`. If you change the skill, edit that source file and run `make sync-skills` (release CI fails on mirror drift); see `CLAUDE.md` (rules and invariants, with pointers into `docs/` for the full reference) and `AGENTS.md` (condensed).

## Prerequisites

- **Go 1.26+** (the CLI is pure-Go via `modernc.org/sqlite`; no CGO required)
- **macOS 14+** (Sonoma) for the Swift dashboard app
- **Xcode Command Line Tools** (`xcode-select --install`)

Optional for AI features:
- **Ollama** for local AI (`brew install ollama`)
- **AWS CLI** configured with SSO for Bedrock
- **OPENROUTER_API_KEY** for OpenRouter

## Building

```bash
# Build everything (CLI + macOS app)
make build

# Build just the CLI
make build-cli

# Install CLI to /usr/local/bin and app to ~/Applications
make install
```

SQLite full-text search (FTS5) is compiled into the pure-Go `modernc.org/sqlite` driver, so no build tags or CGO are needed.

## Testing

```bash
# Go tests (all packages)
make test

# Golden-path E2E battery (vault, CRUD, index, MCP, skills)
make test-battery

# Swift app unit tests
make test-swift

# GUI tests (requires make install first)
make test-gui

# Everything
make test-all
```

### No Mock Tests

All tests must use real API endpoints. No `httptest.NewServer` or fake implementations.

- **Bedrock tests**: use real AWS credentials
- **OpenRouter tests**: use real `OPENROUTER_API_KEY`
- **Ollama tests**: use a real Ollama server

Tests skip on **capability, not configuration**: they run one real probe per test binary and skip when the provider cannot actually serve a request, rather than when a credential variable happens to be unset. An env var can be present while the provider is unusable (an expired token, a wrong region), and a configuration check would make those tests FAIL instead of skip. Use `requireEmbedding` / `requireEmbeddingHostHome` (`cli/capability_test.go`) or the in-package `requireEmbeddings`.

Pure logic tests (string parsing, classification heuristics) that don't call any API are fine.

## Pull Requests

1. Create a feature branch from `main`
2. Write tests for new functionality
3. Run `make test` and ensure all tests pass. CI runs `go vet` plus `make -C cli test` on macOS with **no credentials** (`.github/workflows/ci.yml`), so any test needing a provider must skip there rather than fail
4. Keep commits focused — one logical change per commit
5. Open a PR with a clear description of what and why

## Code Style

- Standard Go conventions (`gofmt`, `go vet`)
- CLI commands use cobra — one file per command in `cli/internal/cli/`
- AI providers implement the interfaces in `cli/internal/ai/provider.go`
- JSON output via `--json` flag on all commands using `output.Write()`

## Project Structure

```
cli/               Go CLI + MCP server
  internal/ai/     AI provider implementations
  internal/cli/    Cobra command definitions
  internal/store/  SQLite database layer
  internal/search/ BM25 + vector search engine
  internal/mcp/    MCP server tools
  internal/skills/ Skill file generation for AI coding agents
app/               Swift macOS dashboard
plugins/           Obsidian plugin (obsidian-2ndbrain)
docs/              Additional documentation
tests/             GUI test scripts
```

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
