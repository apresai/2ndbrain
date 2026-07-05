package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContract_CoreDocumentCommandPaths(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	created, err := runCLIArgs(t, root, "create", "Core Command Note", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, created)
	}
	var createdDoc struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(created, &createdDoc); err != nil {
		t.Fatalf("create JSON: %v\n%s", err, created)
	}
	if createdDoc.Path == "" || createdDoc.Title != "Core Command Note" {
		t.Fatalf("unexpected create result: %+v", createdDoc)
	}

	commands := []struct {
		name string
		argv []string
		want string
	}{
		{"read", []string{"read", createdDoc.Path, "--json", "--porcelain"}, `"title": "Core Command Note"`},
		{"list", []string{"list", "--json", "--porcelain"}, `"title": "Core Command Note"`},
		{"meta", []string{"meta", createdDoc.Path, "--json", "--porcelain"}, `"title": "Core Command Note"`},
		{"meta set", []string{"meta", createdDoc.Path, "--set", "status=complete", "--json", "--porcelain"}, `"status": "complete"`},
		{"search", []string{"search", "Core", "--bm25-only", "--json", "--porcelain"}, `"results"`},
		{"graph", []string{"graph", createdDoc.Path, "--json", "--porcelain"}, `{`},
		{"related", []string{"related", createdDoc.Path, "--json", "--porcelain"}, `"nodes"`},
		{"lint", []string{"lint", "--json", "--porcelain"}, `"files_checked"`},
		{"stale", []string{"stale", "--json", "--porcelain"}, `null`},
		{"index doc", []string{"index", "--doc", createdDoc.Path, "--json", "--porcelain"}, `"embedded"`},
	}
	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCLIArgs(t, root, tc.argv...)
			if err != nil {
				t.Fatalf("%s error: %v\n%s", tc.name, err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("%s output missing %q:\n%s", tc.name, tc.want, out)
			}
		})
	}

	deleted, err := runCLIArgs(t, root, "delete", createdDoc.Path, "--force", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("delete: %v\n%s", err, deleted)
	}
	if !strings.Contains(string(deleted), `"deleted": true`) {
		t.Fatalf("delete output missing deleted=true:\n%s", deleted)
	}
}

// TestCreate_HumanOutputShowsTitle verifies the human-mode create line surfaces
// the title -> filename slug mapping, and that --porcelain stays machine-clean.
func TestCreate_HumanOutputShowsTitle(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	out, err := runCLIArgs(t, root, "create", "Test Space Naming Probe")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "test-space-naming-probe.md") {
		t.Errorf("human output missing slug filename:\n%s", s)
	}
	if !strings.Contains(s, `(title: "Test Space Naming Probe")`) {
		t.Errorf("human output missing title suffix:\n%s", s)
	}

	// Porcelain must not carry the title suffix (a script parses "Created <type>: <path>").
	out2, err := runCLIArgs(t, root, "create", "Another Probe", "--porcelain")
	if err != nil {
		t.Fatalf("create --porcelain: %v\n%s", err, out2)
	}
	if strings.Contains(string(out2), "(title:") {
		t.Errorf("porcelain output must omit the title suffix:\n%s", out2)
	}
}

// TestDelete_NonInteractiveReportsNotRemoved verifies a delete with no --force
// and a non-interactive stdin errors loudly (ExitValidation) and leaves the file
// on disk, instead of the old silent cancel or a hang.
func TestDelete_NonInteractiveReportsNotRemoved(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	created, err := runCLIArgs(t, root, "create", "Doomed Note", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, created)
	}
	var doc struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(created, &doc); err != nil {
		t.Fatalf("create JSON: %v\n%s", err, created)
	}

	// Point stdin at /dev/null so the confirm read hits EOF immediately (n=0)
	// rather than blocking on the 60s timeout.
	oldStdin := os.Stdin
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	os.Stdin = devnull
	t.Cleanup(func() { os.Stdin = oldStdin; devnull.Close() })

	_, err = runCLIArgs(t, root, "delete", doc.Path) // no --force, no --porcelain
	if code := exitCode(t, err); code != ExitValidation {
		t.Fatalf("non-interactive delete: want ExitValidation(%d), got %d (err=%v)", ExitValidation, code, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, doc.Path)); statErr != nil {
		t.Errorf("file must survive a refused delete: %v", statErr)
	}
}

// stdinFromString points os.Stdin at a temp file holding s, restoring it on
// cleanup, so a test can drive an interactive prompt read deterministically.
func stdinFromString(t *testing.T, s string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("temp stdin: %v", err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek stdin: %v", err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

// TestConfirmDelete_Answers covers the interactive answer branches: an explicit
// yes approves, and a deliberate "no" (or any non-y answer) declines without an
// error (exit 0, "Cancelled"). The no-answer/timeout path is covered by
// TestDelete_NonInteractiveReportsNotRemoved.
func TestConfirmDelete_Answers(t *testing.T) {
	cases := []struct {
		name    string
		stdin   string
		wantOK  bool
		wantErr bool
	}{
		{"yes lowercase", "y\n", true, false},
		{"yes uppercase", "Y\n", true, false},
		{"deliberate no", "n\n", false, false},
		{"other answer declines", "maybe\n", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdinFromString(t, tc.stdin)
			ok, err := confirmDelete("Title", "note.md")
			if ok != tc.wantOK {
				t.Errorf("confirmed = %v, want %v", ok, tc.wantOK)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestContract_CreateWithPath(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Positive: --path files the doc under a subdirectory (created on write).
	created, err := runCLIArgs(t, root, "create", "Subdir Note", "--path", "resources", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("create --path: %v\n%s", err, created)
	}
	var createdDoc struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(created, &createdDoc); err != nil {
		t.Fatalf("create JSON: %v\n%s", err, created)
	}
	if createdDoc.Path != filepath.Join("resources", "subdir-note.md") {
		t.Fatalf("expected path resources/subdir-note.md, got %q", createdDoc.Path)
	}
	if _, err := os.Stat(filepath.Join(root, "resources", "subdir-note.md")); err != nil {
		t.Fatalf("file not written under resources/: %v", err)
	}
	// The returned path must be usable by other commands.
	if out, err := runCLIArgs(t, root, "read", createdDoc.Path, "--json", "--porcelain"); err != nil {
		t.Fatalf("read subdir doc: %v\n%s", err, out)
	} else if !strings.Contains(string(out), `"title": "Subdir Note"`) {
		t.Fatalf("read output missing title:\n%s", out)
	}

	// Negative: a traversal escape is rejected and writes nothing.
	out, err := runCLIArgs(t, root, "create", "Escape Note", "--path", "../escape", "--json", "--porcelain")
	if err == nil {
		t.Fatalf("expected --path ../escape to fail, got success:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(root), "escape")); statErr == nil {
		t.Fatalf("traversal escape wrote a file outside the vault")
	}

	// Negative: an absolute path is rejected.
	if _, err := runCLIArgs(t, root, "create", "Abs Note", "--path", "/tmp/abs", "--json", "--porcelain"); err == nil {
		t.Fatalf("expected absolute --path to fail")
	}
}

func TestContract_InitAliasCreatesVault(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-vault")
	out, err := runCLIArgs(t, root, "init", root, "--porcelain")
	if err != nil {
		t.Fatalf("init alias: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".2ndbrain", "config.yaml")); err != nil {
		t.Fatalf("init did not create config: %v", err)
	}
}

func TestContract_BenchFavoriteCommands(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if out, err := runCLIArgs(t, root, "models", "bench", "--provider", "bedrock", "fav", "bench.model", "--porcelain"); err != nil {
		t.Fatalf("bench fav: %v\n%s", err, out)
	}
	if out, err := runCLIArgs(t, root, "models", "bench", "favs", "--json", "--porcelain"); err != nil {
		t.Fatalf("bench favs: %v\n%s", err, out)
	} else if !strings.Contains(string(out), `"model_id": "bench.model"`) {
		t.Fatalf("bench favs missing model:\n%s", out)
	}
	if out, err := runCLIArgs(t, root, "models", "bench", "history", "--json", "--porcelain"); err != nil {
		t.Fatalf("bench history: %v\n%s", err, out)
	}
	if out, err := runCLIArgs(t, root, "models", "bench", "compare", "--json", "--porcelain"); err != nil {
		t.Fatalf("bench compare: %v\n%s", err, out)
	}
	if out, err := runCLIArgs(t, root, "models", "bench", "--provider", "bedrock", "unfav", "bench.model", "--porcelain"); err != nil {
		t.Fatalf("bench unfav: %v\n%s", err, out)
	}
}
