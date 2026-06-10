package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// writeActivePointer writes the active-vault file directly (bypassing
// setActiveVault's canonicalization) so tests control the exact stored bytes.
func writeActivePointer(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(activeVaultPath(), []byte(dir+"\n"), 0o644); err != nil {
		t.Fatalf("write active pointer: %v", err)
	}
}

func TestResolveVaultDir_Precedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	activeDir := newResolveTestVault(t)
	writeActivePointer(t, activeDir)

	// --vault flag wins over everything.
	setVaultFlag(t, "/flag/vault")
	t.Setenv("2NB_VAULT", "/env/vault")
	if dir, source := resolveVaultDir(); dir != "/flag/vault" || source != sourceFlag {
		t.Errorf("with flag+env+active: got (%q, %q), want (/flag/vault, %q)", dir, source, sourceFlag)
	}

	// Env wins over active pointer.
	flagVault = ""
	if dir, source := resolveVaultDir(); dir != "/env/vault" || source != sourceEnv {
		t.Errorf("with env+active: got (%q, %q), want (/env/vault, %q)", dir, source, sourceEnv)
	}

	// Valid active pointer wins over cwd.
	t.Setenv("2NB_VAULT", "")
	if dir, source := resolveVaultDir(); dir != activeDir || source != sourceActive {
		t.Errorf("with active: got (%q, %q), want (%q, %q)", dir, source, activeDir, sourceActive)
	}
}

func TestResolveVaultDir_StaleActiveFallsBackToCwd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")

	// Pointer at a deleted vault: must fall through to cwd, exactly like
	// openVault always did — and now `vault show`/`status` share the rule.
	dead := filepath.Join(t.TempDir(), "deleted-vault")
	writeActivePointer(t, dead)

	if dir, source := resolveVaultDir(); dir != "." || source != sourceCwd {
		t.Errorf("stale pointer: got (%q, %q), want (., %q)", dir, source, sourceCwd)
	}
}

func TestResolveVaultDir_ObsidianOnlyVaultKeepsPointer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")

	// A vault whose .2ndbrain sidecar was deleted but still has .obsidian is
	// valid (Open recreates the sidecar). The old guard checked .2ndbrain
	// only and silently discarded the pointer.
	obsVault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(obsVault, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeActivePointer(t, obsVault)

	if dir, source := resolveVaultDir(); dir != obsVault || source != sourceActive {
		t.Errorf("obsidian-only vault: got (%q, %q), want (%q, %q)", dir, source, obsVault, sourceActive)
	}
}

func TestOpenVaultAndSetActive_VaultFlagIsOneShot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_VAULT", "")

	vaultA := newResolveTestVault(t)
	vaultB := newResolveTestVault(t)
	writeActivePointer(t, vaultA)
	setVaultFlag(t, vaultB)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	v.Close()

	// The explicit --vault override must NOT repoint the shared pointer.
	if got := getActiveVault(); got != vaultA {
		t.Errorf("active pointer after --vault write command = %q, want untouched %q", got, vaultA)
	}
	// Nor should it appear in recents.
	for _, p := range listRecentVaults() {
		if canonicalVaultPath(p) == canonicalVaultPath(vaultB) {
			t.Errorf("recents contains %q after a one-shot --vault command", vaultB)
		}
	}
}

func TestOpenVaultAndSetActive_EnvVarIsOneShot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setVaultFlag(t, "")

	vaultA := newResolveTestVault(t)
	vaultB := newResolveTestVault(t)
	writeActivePointer(t, vaultA)
	t.Setenv("2NB_VAULT", vaultB)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	v.Close()

	// 2NB_VAULT is the override the Obsidian plugin's pinned calls and CI
	// scripts rely on — it must not repoint the shared pointer either.
	if got := getActiveVault(); got != vaultA {
		t.Errorf("active pointer after 2NB_VAULT write command = %q, want untouched %q", got, vaultA)
	}
}

func TestOpenVaultAndSetActive_ActiveResolutionRewritesCanonically(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_VAULT", "")
	setVaultFlag(t, "")

	root := newResolveTestVault(t)
	link := filepath.Join(t.TempDir(), "vault-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// A pointer written in a non-canonical spelling (e.g. by an older
	// version or by hand) must be rewritten canonically and land in recents
	// when a write command resolves through it.
	writeActivePointer(t, link)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	v.Close()

	want := canonicalVaultPath(root)
	if got := getActiveVault(); got != want {
		t.Errorf("active pointer after active-resolved write = %q, want canonical %q", got, want)
	}
	recents := listRecentVaults()
	if len(recents) != 1 || canonicalVaultPath(recents[0]) != want {
		t.Errorf("recents = %v, want exactly [%q]", recents, want)
	}
}

func TestOpenVaultAndSetActive_CwdResolutionSetsActive(t *testing.T) {
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

	want := canonicalVaultPath(root)
	if got := getActiveVault(); got != want {
		t.Errorf("active pointer after cwd-resolved write command = %q, want %q", got, want)
	}
	recents := listRecentVaults()
	if len(recents) != 1 || canonicalVaultPath(recents[0]) != want {
		t.Errorf("recents = %v, want exactly [%q]", recents, want)
	}
}

func TestSetActiveVault_CanonicalAtomic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := newResolveTestVault(t)
	link := filepath.Join(t.TempDir(), "vault-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := setActiveVault(link); err != nil {
		t.Fatalf("setActiveVault: %v", err)
	}

	// Stored canonically: the symlink spelling and the real path must
	// resolve to the same stored value, so string compares can't split them.
	if got, want := getActiveVault(), canonicalVaultPath(root); got != want {
		t.Errorf("pointer = %q, want canonical %q", got, want)
	}
	// Atomic write leaves no temp file behind.
	if _, err := os.Stat(activeVaultPath() + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind at %s.tmp", activeVaultPath())
	}
	// Trailing newline preserved (the file format other readers expect).
	data, err := os.ReadFile(activeVaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Errorf("pointer file missing trailing newline: %q", data)
	}
}

func TestCanonicalVaultPath_EmptyStaysEmpty(t *testing.T) {
	// Abs("") resolves to the cwd; an empty pointer must never become a path
	// or `vault list` would mark a cwd row active with no pointer set.
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

	// Recents holds a non-canonical spelling (symlink) while the pointer is
	// canonical — written by hand to simulate pre-canonicalization state.
	// The list must show ONE row, marked active, not an unmarked duplicate.
	if err := os.WriteFile(recentVaultsPath(), []byte(link+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setActiveVault(root); err != nil {
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
		t.Fatalf("entries = %+v, want one row (symlinked recents entry must match the canonical pointer)", entries)
	}
	if !entries[0].Active {
		t.Errorf("entry = %+v, want it marked active", entries[0])
	}
}

func TestVaultList_SynthesizesActiveRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := newResolveTestVault(t)
	// Active pointer set, but recents file empty — the active vault must
	// still show up, marked, per the command's own docs.
	if err := setActiveVault(root); err != nil {
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
		t.Fatalf("entries = %+v, want exactly one synthesized active row", entries)
	}
	if !entries[0].Active || canonicalVaultPath(entries[0].Path) != canonicalVaultPath(root) {
		t.Errorf("entry = %+v, want active row for %q", entries[0], root)
	}
}
