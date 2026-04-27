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
