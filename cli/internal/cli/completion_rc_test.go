package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runCompletionInstallForTest drives the real handler with the flag values a
// user would pass.
//
// Every caller sets HOME to its OWN t.TempDir() rather than relying on the
// package sandbox: this command writes a shell config, and the package sandbox
// is shared, so a leak here would edit the developer's real ~/.zshrc. PATH is
// pointed at an empty directory for the same reason, so warnIfMultiple2nbOnPath
// finds nothing to exec instead of shelling out to whatever 2nb is installed.
func runCompletionInstallForTest(t *testing.T, dir, rc string, noRC bool) string {
	t.Helper()
	prevDir, prevRC, prevNoRC := completionInstallDir, completionInstallRC, completionInstallNoRC
	t.Cleanup(func() {
		completionInstallDir, completionInstallRC, completionInstallNoRC = prevDir, prevRC, prevNoRC
	})
	completionInstallDir, completionInstallRC, completionInstallNoRC = dir, rc, noRC

	t.Setenv("PATH", t.TempDir())
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	if err := runCompletionInstall(cmd, nil); err != nil {
		t.Fatalf("completion install: %v", err)
	}
	return buf.String()
}

// --rc must redirect BOTH halves of the command: the config that is read for
// completion-directory candidates and the config that is written. Before this
// flag existed the path was hard-coded to ~/.zshrc, so there was no way to run
// the command against anything else, including in a test.
func TestCompletionInstall_RCFlagRedirectsTheBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")

	rcDir := t.TempDir()
	rc := filepath.Join(rcDir, "custom.zshrc")
	const original = "# my config\nexport EDITOR=vim\n"
	if err := os.WriteFile(rc, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	compDir := t.TempDir()

	out := runCompletionInstallForTest(t, compDir, rc, false)

	got, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read %s: %v", rc, err)
	}
	if !strings.Contains(string(got), zshrcBlockBegin) {
		t.Errorf("--rc file did not receive the managed block:\n%s", got)
	}
	if !strings.Contains(string(got), compDir) {
		t.Errorf("the block does not point at the --dir directory %s:\n%s", compDir, got)
	}
	if !strings.Contains(string(got), "export EDITOR=vim") {
		t.Errorf("the user's own config was lost:\n%s", got)
	}
	if !strings.Contains(out, rc) {
		t.Errorf("the command did not say which file it changed:\n%s", out)
	}

	// The default target must be left alone entirely.
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Errorf("$HOME/.zshrc was created despite --rc (stat err = %v)", err)
	}

	// A file 2nb did not write gets copied first: the insertion point is
	// heuristic, and a shell config that will not parse is a shell that will
	// not start.
	backup, err := os.ReadFile(rc + ".2nb-backup")
	if err != nil {
		t.Fatalf("no backup was written next to the rc file: %v", err)
	}
	if string(backup) != original {
		t.Errorf("backup content = %q, want the file exactly as it was: %q", backup, original)
	}
}

// --no-rc is for anyone who manages their shell config themselves: the script
// is still installed, the config is not touched at all, and the two lines to
// add are printed.
func TestCompletionInstall_NoRCLeavesTheRCUntouchedAndPrintsTheSnippet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")

	rc := filepath.Join(t.TempDir(), "custom.zshrc")
	const original = "# my config\nexport EDITOR=vim\n"
	if err := os.WriteFile(rc, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	compDir := t.TempDir()

	out := runCompletionInstallForTest(t, compDir, rc, true)

	got, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read %s: %v", rc, err)
	}
	if string(got) != original {
		t.Errorf("--no-rc modified the config file:\n%s", got)
	}
	if _, err := os.Stat(rc + ".2nb-backup"); !os.IsNotExist(err) {
		t.Errorf("--no-rc wrote a backup for a file it never changed (stat err = %v)", err)
	}
	// The completion script itself is still the point of the command.
	if _, err := os.Stat(filepath.Join(compDir, "_2nb")); err != nil {
		t.Errorf("--no-rc skipped the completion script: %v", err)
	}
	if !strings.Contains(out, "fpath=("+compDir) {
		t.Errorf("the snippet does not name the completion directory:\n%s", out)
	}
	if !strings.Contains(out, "compinit") {
		t.Errorf("the snippet does not include the compinit line:\n%s", out)
	}
}

// ZDOTDIR is where zsh itself looks for .zshrc. Ignoring it wrote a ~/.zshrc
// that the user's shell never sources, and read completion-directory candidates
// from a config that was not theirs.
func TestResolveZshrcPath_HonorsZDOTDIR(t *testing.T) {
	home := t.TempDir()
	zdot := t.TempDir()

	t.Setenv("ZDOTDIR", zdot)
	if got, want := resolveZshrcPath("", home), filepath.Join(zdot, ".zshrc"); got != want {
		t.Errorf("with ZDOTDIR set: got %q, want %q", got, want)
	}
	// An explicit --rc outranks ZDOTDIR.
	explicit := filepath.Join(t.TempDir(), "other.zshrc")
	if got := resolveZshrcPath(explicit, home); got != explicit {
		t.Errorf("--rc did not win over ZDOTDIR: got %q, want %q", got, explicit)
	}

	t.Setenv("ZDOTDIR", "")
	if got, want := resolveZshrcPath("", home), filepath.Join(home, ".zshrc"); got != want {
		t.Errorf("with ZDOTDIR unset: got %q, want %q", got, want)
	}
	// A blank-but-set ZDOTDIR is not a directory; it must not become "/.zshrc".
	t.Setenv("ZDOTDIR", "   ")
	if got, want := resolveZshrcPath("", home), filepath.Join(home, ".zshrc"); got != want {
		t.Errorf("with a blank ZDOTDIR: got %q, want %q", got, want)
	}

	// ~ expansion, so --rc accepts what a user would type.
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")
	if got, want := resolveZshrcPath("~/.config/zsh/.zshrc", home), filepath.Join(home, ".config", "zsh", ".zshrc"); got != want {
		t.Errorf("--rc with ~: got %q, want %q", got, want)
	}
}

// The rc file is also where the completion-directory search reads its
// candidates, so redirecting the write target without redirecting the read
// would install the block into a config whose directories were never
// consulted. This is the only case that exercises that half: the other two
// pass --dir, which short-circuits the search entirely.
func TestCompletionInstall_ReadsCompletionDirsFromTheResolvedRC(t *testing.T) {
	// HOME is an empty temp dir, so the ~/.zsh/completions default cannot win
	// by accident and nothing can reach the developer's real config.
	t.Setenv("HOME", t.TempDir())

	zdot := t.TempDir()
	zfuncs := filepath.Join(t.TempDir(), "zfuncs")
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"),
		[]byte("# my config\nfpath=("+zfuncs+" $fpath)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZDOTDIR", zdot)

	out := runCompletionInstallForTest(t, "", "", false)

	if _, err := os.Stat(filepath.Join(zfuncs, "_2nb")); err != nil {
		t.Errorf("the completion script did not land in the directory named by $ZDOTDIR/.zshrc (%s): %v\n%s", zfuncs, err, out)
	}
	got, err := os.ReadFile(filepath.Join(zdot, ".zshrc"))
	if err != nil {
		t.Fatalf("read $ZDOTDIR/.zshrc: %v", err)
	}
	if !strings.Contains(string(got), zshrcBlockBegin) {
		t.Errorf("the managed block did not land in $ZDOTDIR/.zshrc:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".zshrc")); !os.IsNotExist(err) {
		t.Errorf("$HOME/.zshrc was written despite ZDOTDIR (stat err = %v)", err)
	}
}
