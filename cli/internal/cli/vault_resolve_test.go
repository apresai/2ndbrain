package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

// newResolveTestVault creates an initialized vault and returns its root.
// The Init handle is closed so tests reopen it through the code under test.
func newResolveTestVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	v, err := vault.Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	v.Close()
	return root
}

// setVaultFlag sets the package-level --vault flag state and restores it.
func setVaultFlag(t *testing.T, val string) {
	t.Helper()
	old := flagVault
	flagVault = val
	t.Cleanup(func() { flagVault = old })
}

func TestResolveVaultDir_Precedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// --vault flag wins over env (and everything below it).
	setVaultFlag(t, "/flag/vault")
	t.Setenv("2NB_VAULT", "/env/vault")
	if dir, source := resolveVaultDir(); dir != "/flag/vault" || source != sourceFlag {
		t.Errorf("with flag+env: got (%q, %q), want (/flag/vault, %q)", dir, source, sourceFlag)
	}

	// Env wins when there is no flag.
	flagVault = ""
	if dir, source := resolveVaultDir(); dir != "/env/vault" || source != sourceEnv {
		t.Errorf("with env: got (%q, %q), want (/env/vault, %q)", dir, source, sourceEnv)
	}
}

func TestOpenVaultAndSetActive_VaultFlagIsOneShot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_VAULT", "")
	t.Chdir(t.TempDir()) // non-vault cwd, so the flag is the only resolution

	vaultB := newResolveTestVault(t)
	setVaultFlag(t, vaultB)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	v.Close()

	// A one-shot --vault override resolves vaultB but must NOT land in recents
	// (the GUI/plugin pin --vault on every call; recents tracks worked-in vaults).
	for _, p := range listRecentVaults() {
		if canonicalVaultPath(p) == canonicalVaultPath(vaultB) {
			t.Errorf("recents contains %q after a one-shot --vault command", vaultB)
		}
	}
}

func TestOpenVaultAndSetActive_EnvVarIsOneShot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setVaultFlag(t, "")
	t.Chdir(t.TempDir())

	vaultB := newResolveTestVault(t)
	t.Setenv("2NB_VAULT", vaultB)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	v.Close()

	// 2NB_VAULT is the override the Obsidian plugin's pinned calls and CI
	// scripts rely on — it must not land in recents either.
	for _, p := range listRecentVaults() {
		if canonicalVaultPath(p) == canonicalVaultPath(vaultB) {
			t.Errorf("recents contains %q after a one-shot 2NB_VAULT command", vaultB)
		}
	}
}

func TestOpenVaultAndSetActive_CwdResolutionRecordsRecent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_VAULT", "")
	setVaultFlag(t, "")

	root := newResolveTestVault(t)
	t.Chdir(root)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	v.Close()

	// A cwd-resolved write command records the vault in recents (display-only;
	// recents is never a resolution source, so there is no pointer to drift).
	want := canonicalVaultPath(root)
	recents := listRecentVaults()
	if len(recents) != 1 || canonicalVaultPath(recents[0]) != want {
		t.Errorf("recents = %v, want exactly [%q]", recents, want)
	}
}

func TestCanonicalVaultPath_EmptyStaysEmpty(t *testing.T) {
	// Abs("") resolves to the cwd; an empty path must never become a path
	// or `vault list` would mark a cwd row active with nothing recorded.
	if got := canonicalVaultPath(""); got != "" {
		t.Errorf("canonicalVaultPath(\"\") = %q, want \"\"", got)
	}
}

func TestVaultList_DedupesAcrossSpellings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := newResolveTestVault(t)
	link := filepath.Join(t.TempDir(), "vault-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Recents holds a non-canonical spelling (the symlink); the active vault is
	// resolved (here via the --vault runCLIArgs injects) to the canonical root.
	// The list must show ONE row, marked active, not an unmarked duplicate.
	if err := os.WriteFile(recentVaultsPath(), []byte(link+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "vault", "list", "--json")
	if err != nil {
		t.Fatalf("vault list: %v", err)
	}
	var entries []VaultListEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want one row (symlinked recents entry must match the canonical active vault)", entries)
	}
	if !entries[0].Active {
		t.Errorf("entry = %+v, want it marked active", entries[0])
	}
}

func TestVaultList_SynthesizesActiveRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := newResolveTestVault(t)
	// Recents file empty, but the active vault (resolved via the --vault
	// runCLIArgs injects) must still show up, marked, per the command's docs.

	out, err := runCLIArgs(t, root, "vault", "list", "--json")
	if err != nil {
		t.Fatalf("vault list: %v", err)
	}
	var entries []VaultListEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want exactly one synthesized active row", entries)
	}
	if !entries[0].Active || canonicalVaultPath(entries[0].Path) != canonicalVaultPath(root) {
		t.Errorf("entry = %+v, want active row for %q", entries[0], root)
	}
}
