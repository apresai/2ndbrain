package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ContainsPath symlink-containment tests. The pre-fix lexical guard let an
// in-vault symlink redirect a write outside the vault; these lock the resolved
// behavior. No mocks: real dirs + real symlinks on disk.

func newVault(t *testing.T) *Vault {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func TestContainsPath_RejectsInVaultSymlinkEscape(t *testing.T) {
	v := newVault(t)

	// An external directory entirely outside the vault.
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "secret.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A symlink INSIDE the vault that points at the external dir.
	link := filepath.Join(v.Root, "escape")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	// A write through the symlink lands outside the vault and must be refused.
	if v.ContainsPath(filepath.Join(link, "pwned.md")) {
		t.Error("ContainsPath allowed a write through an in-vault symlink to an external dir")
	}
	// Even targeting the symlink dir itself must be refused (it resolves out).
	if v.ContainsPath(link) {
		t.Error("ContainsPath allowed the in-vault symlink dir itself (resolves outside)")
	}
}

func TestContainsPath_AllowsLegitimatePaths(t *testing.T) {
	v := newVault(t)

	// An existing nested dir + a not-yet-created file under it.
	if err := os.MkdirAll(filepath.Join(v.Root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"root itself", v.Root, true},
		{"existing nested dir", filepath.Join(v.Root, "notes"), true},
		{"not-yet-created file under existing dir", filepath.Join(v.Root, "notes", "new.md"), true},
		{"not-yet-created nested dir+file", filepath.Join(v.Root, "a", "b", "c.md"), true},
		{"parent climb", filepath.Join(v.Root, "..", "outside.md"), false},
		{"sibling vault", v.Root + "-sibling/x.md", false},
	}
	for _, tc := range cases {
		if got := v.ContainsPath(tc.path); got != tc.want {
			t.Errorf("%s: ContainsPath(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}

func TestResolveSymlinksLenient_NonExistentTailPreserved(t *testing.T) {
	v := newVault(t)
	got := resolveSymlinksLenient(filepath.Join(v.Root, "nope", "deep.md"))
	if !strings.HasSuffix(got, filepath.Join("nope", "deep.md")) {
		t.Errorf("resolveSymlinksLenient dropped the non-existent tail: %q", got)
	}
	// The resolved prefix must be the canonicalized root (symlinks evaluated).
	wantRoot, _ := filepath.EvalSymlinks(v.Root)
	if !strings.HasPrefix(got, wantRoot) {
		t.Errorf("resolved path %q not under canonical root %q", got, wantRoot)
	}
}
