package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeObsidianRegistryForTest writes an obsidian.json (under the temp HOME)
// whose single vault, flagged open, is openVaultPath. macOS path — the suite
// runs on macOS (CI is macos-latest), where the Obsidian-native feature is used.
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

func TestResolveVaultDir_ResolvesObsidianOpenVault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "") // activate the Obsidian rung
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")
	t.Chdir(t.TempDir()) // cwd is not a vault

	obsVault := newResolveTestVault(t)
	writeObsidianRegistryForTest(t, obsVault)

	dir, source := resolveVaultDir()
	if dir != obsVault || source != sourceObsidian {
		t.Errorf("got (%q, %q), want (%q, %q)", dir, source, obsVault, sourceObsidian)
	}
}

func TestResolveVaultDir_ObsidianBeatsCwd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "")
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")

	// Standing INSIDE one vault, with Obsidian reporting a DIFFERENT open vault:
	// Obsidian wins. This is the headline behavior of making Obsidian's registry
	// the authoritative source of the active vault.
	cwdVault := newResolveTestVault(t)
	t.Chdir(cwdVault)
	obsVault := newResolveTestVault(t)
	writeObsidianRegistryForTest(t, obsVault)

	dir, source := resolveVaultDir()
	if dir != obsVault || source != sourceObsidian {
		t.Errorf("obsidian must beat cwd: got (%q, %q), want (%q, %q)", dir, source, obsVault, sourceObsidian)
	}
}

func TestResolveVaultDir_FlagBeatsObsidian(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "")
	t.Setenv("2NB_VAULT", "")
	t.Chdir(t.TempDir())

	writeObsidianRegistryForTest(t, newResolveTestVault(t))
	setVaultFlag(t, "/flag/vault")

	if dir, source := resolveVaultDir(); dir != "/flag/vault" || source != sourceFlag {
		t.Errorf("--vault must beat obsidian: got (%q, %q), want (/flag/vault, %q)", dir, source, sourceFlag)
	}
}

func TestResolveVaultDir_EnvBeatsObsidian(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "")
	setVaultFlag(t, "")
	t.Chdir(t.TempDir())

	writeObsidianRegistryForTest(t, newResolveTestVault(t))
	t.Setenv("2NB_VAULT", "/env/vault")

	if dir, source := resolveVaultDir(); dir != "/env/vault" || source != sourceEnv {
		t.Errorf("2NB_VAULT must beat obsidian: got (%q, %q), want (/env/vault, %q)", dir, source, sourceEnv)
	}
}

func TestResolveVaultDir_ObsidianInvalidPathFallsToCwd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "")
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")

	// Obsidian reports an open vault that is NOT a vault root (stale/moved):
	// IsVaultRoot rejects it and resolution falls to the cwd vault.
	writeObsidianRegistryForTest(t, filepath.Join(t.TempDir(), "not-a-vault"))
	cwdVault := newResolveTestVault(t)
	t.Chdir(cwdVault)

	if dir, source := resolveVaultDir(); dir != "." || source != sourceCwd {
		t.Errorf("invalid obsidian path: got (%q, %q), want (., %q)", dir, source, sourceCwd)
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
	// to the (erroring) cwd — the isolation that keeps a binary test under the
	// developer's real HOME from binding the live Obsidian vault.
	dir, source := resolveVaultDir()
	if dir != "." || source != sourceCwd {
		t.Errorf("under 2NB_TEST: got (%q, %q), want (., %q)", dir, source, sourceCwd)
	}
}

func TestOpenVaultAndSetActive_ObsidianResolutionRecordsRecent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("2NB_TEST", "")
	setVaultFlag(t, "")
	t.Setenv("2NB_VAULT", "")
	t.Chdir(t.TempDir())

	obsVault := newResolveTestVault(t)
	writeObsidianRegistryForTest(t, obsVault)

	v, err := openVaultAndSetActive()
	if err != nil {
		t.Fatalf("openVaultAndSetActive: %v", err)
	}
	v.Close()

	// A write command resolved via Obsidian records the vault in recents.
	want := canonicalVaultPath(obsVault)
	recents := listRecentVaults()
	if len(recents) != 1 || canonicalVaultPath(recents[0]) != want {
		t.Errorf("recents = %v, want exactly [%q]", recents, want)
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

func TestVaultNotFoundError_LeadsWithObsidianHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	msg := vaultNotFoundError("/nowhere", sourceCwd).Error()
	if !strings.Contains(msg, "Open a vault in Obsidian") {
		t.Errorf("error should lead with an Obsidian hint:\n%s", msg)
	}
	for _, want := range []string{"To fix:", "--vault /path/to/vault", "2NB_VAULT="} {
		if !strings.Contains(msg, want) {
			t.Errorf("generic guidance missing %q:\n%s", want, msg)
		}
	}
	// The removed pointer must no longer be referenced.
	if strings.Contains(msg, "active vault pointer") || strings.Contains(msg, "2ndbrain-active-vault") {
		t.Errorf("error should not mention the removed pointer:\n%s", msg)
	}
}
