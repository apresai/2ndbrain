package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain sandboxes every test in this package under a throwaway HOME (and
// matching XDG dirs) so the in-process CLI commands can never touch the
// developer's real home-dir state: ~/.2ndbrain-vaults recents,
// ~/.config/2nb/models.yaml, the pricing cache, or the developer's real
// Obsidian registry (~/Library/Application Support/obsidian/obsidian.json,
// which resolveVaultDir now reads — a sandbox HOME makes it absent in tests).
//
// This package's tests dispatch cobra commands in-process (see runCLIArgs).
// Commands like `vault create` / `init` / `vault set` call addRecentVault, which
// writes under $HOME. Most tests go through newContractVault, which already
// redirects $HOME per-test — but any test that skips that helper (e.g.
// TestContract_InitAliasCreatesVault) used to pollute the developer's real
// recents on every `make test`. Isolating at the package level covers every
// test, present and future, without each one having to remember.
//
// recentVaultsPath/obsidianRegistryPath resolve $HOME at call time (not at
// init), so setting it here before m.Run() takes effect for all tests. Tests
// that set their own $HOME via t.Setenv still work — t.Setenv layers on top and
// restores to this sandboxed baseline (never the real home) afterward.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "2nb-cli-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: create temp HOME:", err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	os.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	code := m.Run()

	os.RemoveAll(home)
	os.Exit(code)
}
