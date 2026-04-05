# Contributing to 2ndbrain

Thanks for your interest in contributing to 2ndbrain.

## Prerequisites

- **Go 1.24+** with `CGO_ENABLED=1` (SQLite requires it)
- **macOS 14+** (Sonoma) for the Swift editor
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

The CLI requires `-tags fts5` for SQLite full-text search. The Makefiles handle this automatically.

## Testing

```bash
# Go tests (all packages)
make test

# GUI tests (requires make install first)
make test-gui

# Everything
make test-all
```

### No Mock Tests

All tests must use real API endpoints. No `httptest.NewServer` or fake implementations.

- **Bedrock tests**: use real AWS credentials, skip if not configured
- **OpenRouter tests**: use real `OPENROUTER_API_KEY`, skip if not set
- **Ollama tests**: use real Ollama server, skip if not running

Pure logic tests (string parsing, classification heuristics) that don't call any API are fine.

## Pull Requests

1. Create a feature branch from `main`
2. Write tests for new functionality
3. Run `make test` and ensure all tests pass
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
app/               Swift macOS editor
docs/              Additional documentation
tests/             GUI test scripts
```

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
