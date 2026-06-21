package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeObsidianRegistryForTest writes an obsidian.json (under the temp HOME)
// whose single vault, flagged open, is openVaultPath.
func writeObsidianRegistryForTest(t *testing.T, openVaultPath string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	dir := filepath.Join(home, "Library", "Application Support", "obsidian")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	body := fmt.Sprintf(`{"vaults":{"x":{"path":%q,"ts":1,"open":true}}}`, openVaultPath)
	if err := os.WriteFile(filepath.Join(dir, "obsidian.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

func TestResolveVaultDir_FallsBackToObsidianOpenVault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "") // the registry rung is gated off under 2NB_TEST
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")
	// cwd is a plain dir, not inside any vault, so the cwd rung doesn't fire.
	t.Chdir(t.TempDir())

	obsVault := newResolveTestVault(t)
	writeObsidianRegistryForTest(t, obsVault)

	dir, source := resolveVaultDir()
	if dir != obsVault || source != sourceObsidian {
		t.Errorf("obsidian fallback: got (%q, %q), want (%q, %q)", dir, source, obsVault, sourceObsidian)
	}
}

func TestOpenVaultAndSetActive_ObsidianResolutionSetsActive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "") // rung active AND setActiveVault/addRecentVault write
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")
	t.Chdir(t.TempDir()) // non-vault cwd, so resolution reaches the registry rung

	obsVault := newResolveTestVault(t)
	writeObsidianRegistryForTest(t, obsVault)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	v.Close()

	// A write command resolved via Obsidian's registry self-heals the shared
	// pointer + recents, so the next bare command resolves without re-reading it.
	want := canonicalVaultPath(obsVault)
	if got := getActiveVault(); got != want {
		t.Errorf("active pointer after obsidian-resolved write = %q, want %q", got, want)
	}
	recents := listRecentVaults()
	if len(recents) != 1 || canonicalVaultPath(recents[0]) != want {
		t.Errorf("recents = %v, want exactly [%q]", recents, want)
	}
}

func TestResolveVaultDir_CwdVaultBeatsObsidian(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "")
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")

	cwdVault := newResolveTestVault(t)
	t.Chdir(cwdVault)

	// Obsidian reports a DIFFERENT open vault; standing inside a vault must win.
	writeObsidianRegistryForTest(t, newResolveTestVault(t))

	dir, source := resolveVaultDir()
	if dir != "." || source != sourceCwd {
		t.Errorf("cwd vault must beat obsidian: got (%q, %q), want (., %q)", dir, source, sourceCwd)
	}
}

func TestResolveVaultDir_ObsidianRungInertUnder2NBTest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "1")
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")
	t.Chdir(t.TempDir())

	writeObsidianRegistryForTest(t, newResolveTestVault(t))

	// Under the harness guard the registry is ignored: resolution falls through
	// to the (erroring) cwd, exactly as before this rung existed.
	dir, source := resolveVaultDir()
	if dir != "." || source != sourceCwd {
		t.Errorf("under 2NB_TEST: got (%q, %q), want (., %q)", dir, source, sourceCwd)
	}
}

func TestVaultNotFoundError_NamesStalePointer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dead := filepath.Join(t.TempDir(), "gone")
	writeActivePointer(t, dead)

	msg := vaultNotFoundError("/nowhere", sourceCwd).Error()
	for _, want := range []string{activeVaultFile, dead, "no longer a vault", "2nb vault set"} {
		if !strings.Contains(msg, want) {
			t.Errorf("stale-pointer error missing %q:\n%s", want, msg)
		}
	}
}

func TestVaultNotFoundError_StaleDiagnosticSuppressedForFlagSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeActivePointer(t, filepath.Join(t.TempDir(), "gone"))

	// A bad explicit --vault is the user's own path; don't blame the pointer.
	msg := vaultNotFoundError("/explicit", sourceFlag).Error()
	if strings.Contains(msg, "active vault pointer") {
		t.Errorf("stale-pointer diagnostic should be suppressed for --vault source:\n%s", msg)
	}
}

func TestVaultNotFoundError_ListsRecents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	v1 := newResolveTestVault(t)
	v2 := newResolveTestVault(t)
	if err := os.WriteFile(recentVaultsPath(), []byte(v1+"\n"+v2+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := vaultNotFoundError("/nowhere", sourceCwd).Error()
	for _, want := range []string{"Recent vaults", v1, v2, "--vault"} {
		if !strings.Contains(msg, want) {
			t.Errorf("recents error missing %q:\n%s", want, msg)
		}
	}
}

func TestVaultNotFoundError_GenericWhenNothingSet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	msg := vaultNotFoundError("/nowhere", sourceCwd).Error()
	if strings.Contains(msg, "Recent vaults") || strings.Contains(msg, "active vault pointer") {
		t.Errorf("expected no diagnostics when nothing is set:\n%s", msg)
	}
	for _, want := range []string{"To fix:", "--vault /path/to/vault", "2NB_VAULT="} {
		if !strings.Contains(msg, want) {
			t.Errorf("generic guidance missing %q:\n%s", want, msg)
		}
	}
}
