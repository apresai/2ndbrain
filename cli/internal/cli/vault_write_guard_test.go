package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

// writeObsidianRegistryOpenFlag writes an obsidian.json (under the temp HOME)
// with a single vault whose `open` flag is set as given — so a test can model
// both "Obsidian is open on X" and the Obsidian-closed most-recent fallback.
func writeObsidianRegistryOpenFlag(t *testing.T, vaultPath string, open bool) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	dir := filepath.Join(home, "Library", "Application Support", "obsidian")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	body := fmt.Sprintf(`{"vaults":{"x":{"path":%q,"ts":1,"open":%t}}}`, vaultPath, open)
	if err := os.WriteFile(filepath.Join(dir, "obsidian.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

// newStrayVault builds a 2nb-only vault (a .2ndbrain/ sidecar but NO .obsidian/),
// i.e. a vault Obsidian doesn't know — the "unconfigured" target.
func newStrayVault(t *testing.T) string {
	t.Helper()
	root := newResolveTestVault(t) // has .obsidian + .2ndbrain
	if err := os.RemoveAll(filepath.Join(root, ".obsidian")); err != nil {
		t.Fatalf("strip .obsidian: %v", err)
	}
	return root
}

func setUnconfigured(t *testing.T, val bool) {
	t.Helper()
	old := flagUnconfigured
	flagUnconfigured = val
	t.Cleanup(func() { flagUnconfigured = old })
}

// clearWriteEnv resets the shared resolution inputs for a write test.
func clearWriteEnv(t *testing.T, twoNBTest string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", twoNBTest)
	t.Setenv("2NB_VAULT", "")
	t.Setenv("2NB_UNCONFIGURED", "")
	setVaultFlag(t, "")
	setUnconfigured(t, false)
}

// The headline fix: a write from a non-vault directory targets the vault Obsidian
// has open — NOT the cwd, and never a vault found by walking up.
func TestWrite_DefaultsToObsidianOpenVault(t *testing.T) {
	clearWriteEnv(t, "")
	t.Chdir(t.TempDir()) // cwd is not a vault

	obs := newResolveTestVault(t)
	writeObsidianRegistryForTest(t, obs)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	defer v.Close()
	if canonicalVaultPath(v.Root) != canonicalVaultPath(obs) {
		t.Errorf("wrote to %q, want the open Obsidian vault %q", v.Root, obs)
	}
}

func TestWrite_ObsidianBeatsCwdVault(t *testing.T) {
	clearWriteEnv(t, "")

	cwdVault := newResolveTestVault(t)
	t.Chdir(cwdVault) // standing inside a DIFFERENT real vault
	obs := newResolveTestVault(t)
	writeObsidianRegistryForTest(t, obs)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	defer v.Close()
	if canonicalVaultPath(v.Root) != canonicalVaultPath(obs) {
		t.Errorf("wrote to %q, want the open Obsidian vault %q (not the cwd vault)", v.Root, obs)
	}
}

func TestWrite_ObsidianClosedUsesMostRecent(t *testing.T) {
	clearWriteEnv(t, "")
	t.Chdir(t.TempDir())

	recent := newResolveTestVault(t)
	writeObsidianRegistryOpenFlag(t, recent, false) // nothing flagged open

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	defer v.Close()
	if canonicalVaultPath(v.Root) != canonicalVaultPath(recent) {
		t.Errorf("wrote to %q, want the most-recent vault %q", v.Root, recent)
	}
}

// --vault pointing at the SAME vault Obsidian has open is honored silently (the
// macOS app and Obsidian plugin always pin --vault to the open vault, so this is
// the hot path — it must never trip the unconfigured/notice branches).
func TestWrite_VaultFlagMatchesConfigured(t *testing.T) {
	clearWriteEnv(t, "")
	t.Chdir(t.TempDir())

	vlt := newResolveTestVault(t)
	writeObsidianRegistryForTest(t, vlt) // configured = vlt
	setVaultFlag(t, vlt)                 // --vault = the same vlt

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("--vault matching the configured vault must be honored: %v", err)
	}
	defer v.Close()
	if canonicalVaultPath(v.Root) != canonicalVaultPath(vlt) {
		t.Errorf("wrote to %q, want %q", v.Root, vlt)
	}
}

// --vault to another real Obsidian vault is honored (a notice, not a refusal).
func TestWrite_VaultFlagToAnotherRealVault(t *testing.T) {
	clearWriteEnv(t, "")
	t.Chdir(t.TempDir())

	writeObsidianRegistryForTest(t, newResolveTestVault(t)) // configured = some other vault
	target := newResolveTestVault(t)                        // a real Obsidian vault
	setVaultFlag(t, target)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	defer v.Close()
	if canonicalVaultPath(v.Root) != canonicalVaultPath(target) {
		t.Errorf("wrote to %q, want the --vault target %q", v.Root, target)
	}
}

// --vault to a vault Obsidian doesn't know is refused without --unconfigured,
// and nothing is written.
func TestWrite_VaultFlagUnconfiguredRefused(t *testing.T) {
	clearWriteEnv(t, "")
	t.Chdir(t.TempDir())

	writeObsidianRegistryForTest(t, newResolveTestVault(t)) // a configured vault exists
	stray := newStrayVault(t)
	setVaultFlag(t, stray)

	v, err := openVaultAndSetActive()
	if err == nil {
		v.Close()
		t.Fatalf("expected refusal writing to unconfigured vault %q", stray)
	}
	if !strings.Contains(err.Error(), "unconfigured") {
		t.Errorf("error should explain the unconfigured vault, got: %v", err)
	}
}

func TestWrite_VaultFlagUnconfiguredAllowed(t *testing.T) {
	clearWriteEnv(t, "")
	t.Chdir(t.TempDir())

	writeObsidianRegistryForTest(t, newResolveTestVault(t))
	stray := newStrayVault(t)
	setVaultFlag(t, stray)
	setUnconfigured(t, true) // acknowledge

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("with --unconfigured the write should proceed: %v", err)
	}
	defer v.Close()
	if canonicalVaultPath(v.Root) != canonicalVaultPath(stray) {
		t.Errorf("wrote to %q, want %q", v.Root, stray)
	}
}

// 2NB_UNCONFIGURED is the MCP-server (flagless) equivalent of --unconfigured.
func TestWrite_UnconfiguredEnvAck(t *testing.T) {
	clearWriteEnv(t, "")
	t.Chdir(t.TempDir())

	writeObsidianRegistryForTest(t, newResolveTestVault(t))
	stray := newStrayVault(t)
	setVaultFlag(t, stray)
	t.Setenv("2NB_UNCONFIGURED", "1")

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("2NB_UNCONFIGURED should permit the write: %v", err)
	}
	v.Close()
}

// No override, no Obsidian vault: a cwd that resolves only by walking UP to a
// parent vault is refused, and no .2ndbrain is minted in the cwd.
func TestWrite_CwdWalkUpRefused(t *testing.T) {
	clearWriteEnv(t, "1") // 2NB_TEST disables the Obsidian rung
	t.Chdir(t.TempDir())

	parent := newResolveTestVault(t)
	sub := filepath.Join(parent, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	v, err := openVaultAndSetActive()
	if err == nil {
		v.Close()
		t.Fatalf("expected refusal for a walked-up write")
	}
	if !strings.Contains(err.Error(), "walking up") {
		t.Errorf("error should explain the walk-up, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(sub, vault.DotDirName)); !os.IsNotExist(statErr) {
		t.Errorf("a refused write must not mint a vault in %q", sub)
	}
}

// Standing in the vault root (no override, no Obsidian) is allowed.
func TestWrite_CwdExactAllowed(t *testing.T) {
	clearWriteEnv(t, "1")
	root := newResolveTestVault(t)
	t.Chdir(root)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("standing in the vault root should be allowed: %v", err)
	}
	defer v.Close()
	if canonicalVaultPath(v.Root) != canonicalVaultPath(root) {
		t.Errorf("wrote to %q, want %q", v.Root, root)
	}
}
