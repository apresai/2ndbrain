package mcp

import (
	"fmt"
	"os/exec"
	"strings"
)

// Codex stores MCP servers in ~/.codex/config.toml (TOML). Rather than add a
// TOML dependency and risk mangling the user's file, we shell out to Codex's own
// `codex mcp add`/`codex mcp remove`, which own that file and preserve its
// formatting. When the `codex` CLI isn't installed we degrade to printing the
// exact command + a config.toml snippet (InstallResult.Instructions) instead of
// erroring, so `--client all` keeps going.
//
// codexLookPath/codexRun/codexList are package vars so tests can inject them
// (the established DI pattern in skills/doctor.go) — no mocks, no external codex
// required to test the construction + fallback paths.
var (
	codexLookPath = exec.LookPath
	codexRun      = func(args ...string) error { return exec.Command("codex", args...).Run() }
	codexList     = func() (string, error) {
		out, err := exec.Command("codex", "mcp", "list").CombinedOutput()
		return string(out), err
	}
)

// codexAddArgs builds the `codex mcp add` argv registering the 2ndbrain MCP
// server. absCmd must be an absolute 2nb path (Codex may launch it without the
// shell PATH).
func codexAddArgs(absCmd, vaultRoot string) []string {
	return []string{"mcp", "add", serverKeyName, "--", absCmd, "mcp-server", "--vault", vaultRoot}
}

func codexRemoveArgs() []string {
	return []string{"mcp", "remove", serverKeyName}
}

// codexFallbackInstructions is shown when the `codex` CLI isn't on PATH: the
// exact command to run plus the equivalent ~/.codex/config.toml block.
func codexFallbackInstructions(absCmd, vaultRoot string) string {
	cmd := "codex " + strings.Join(codexAddArgs(absCmd, vaultRoot), " ")
	toml := fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = [\"mcp-server\", \"--vault\", %q]\n", serverKeyName, absCmd, vaultRoot)
	return "The `codex` CLI was not found on your PATH. To configure Codex, run:\n\n  " + cmd +
		"\n\nor add this to ~/.codex/config.toml:\n\n" + toml
}

func installCodex(command, vaultRoot string, dryRun bool) (InstallResult, error) {
	res := InstallResult{Client: "codex", ConfigPath: "~/.codex/config.toml", ServerKey: serverKeyName, Scope: "user"}
	abs, err := resolveAbsCommand(command)
	if err != nil {
		return res, err
	}
	if _, lookErr := codexLookPath("codex"); lookErr != nil {
		res.Instructions = codexFallbackInstructions(abs, vaultRoot) // degrade, not error
		return res, nil
	}
	if dryRun {
		res.Changed = true
		res.Configured = true
		return res, nil
	}
	// Idempotency: skip the add if Codex already lists our server.
	if out, lerr := codexList(); lerr == nil && codexListHasServer(out) {
		res.Configured = true
		return res, nil
	}
	if runErr := codexRun(codexAddArgs(abs, vaultRoot)...); runErr != nil {
		// Return the error so a single-client `mcp install --client codex` exits
		// non-zero; InstallAll's runAll captures it (res.Error) and keeps going.
		return res, fmt.Errorf("codex mcp add failed: %w", runErr)
	}
	res.Changed = true
	res.Configured = true
	return res, nil
}

// codexListHasServer reports whether `codex mcp list` output names our server as
// a standalone field. It deliberately does NOT substring-match the raw dump: a
// user's vault path can itself contain "2ndbrain" (e.g. ~/dev/2ndbrain), which
// would otherwise be a false positive that skips the real `codex mcp add`.
func codexListHasServer(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		for _, f := range strings.Fields(line) {
			if strings.Trim(f, "\"'`-•*:,") == serverKeyName {
				return true
			}
		}
	}
	return false
}

func uninstallCodex(dryRun bool) (InstallResult, error) {
	res := InstallResult{Client: "codex", ConfigPath: "~/.codex/config.toml", ServerKey: serverKeyName, Scope: "user"}
	if _, lookErr := codexLookPath("codex"); lookErr != nil {
		res.Instructions = "The `codex` CLI was not found on your PATH. Remove the [mcp_servers." + serverKeyName + "] table from ~/.codex/config.toml by hand."
		return res, nil
	}
	if dryRun {
		res.Changed = true
		return res, nil
	}
	if runErr := codexRun(codexRemoveArgs()...); runErr != nil {
		return res, fmt.Errorf("codex mcp remove failed: %w", runErr)
	}
	res.Changed = true
	return res, nil
}
