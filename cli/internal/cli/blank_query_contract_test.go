package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI half of the blank-query contract, through the real command paths.
// Credential-free: with no provider configured the retrieval falls back to
// BM25, and the refusals fire before any provider is consulted.
func TestContract_BlankQueryIsRefusedButInlineFiltersStillEnumerate(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("decision.md", "---\ntitle: Decision\ntype: adr\nstatus: draft\n---\nwe decided on tokens\n")
	write("scratch.md", "---\ntitle: Scratch\ntype: note\nstatus: draft\n---\na scratch note\n")
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	t.Run("search with a blank query is refused", func(t *testing.T) {
		// `2nb search ""` is what an empty shell variable produces. It used to
		// return every document as ranked hits, exit 0.
		_, err := runCLIArgs(t, root, "search", "", "--json")
		if err == nil {
			t.Fatal("search \"\" succeeded; it should be refused rather than dump the vault")
		}
		if !strings.Contains(err.Error(), "query") {
			t.Errorf("refusal %q does not mention the query", err)
		}
	})

	t.Run("search with only an inline filter enumerates that type", func(t *testing.T) {
		// Documented form. The prefix extraction leaves the query empty with
		// Type set, and that must reach the engine's enumerate-by-filter path.
		out, err := runCLIArgs(t, root, "search", "type:adr", "--json")
		if err != nil {
			t.Fatalf("search \"type:adr\" was refused: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), `"decision.md"`) || strings.Contains(string(out), `"scratch.md"`) {
			t.Errorf("want only decision.md for type:adr, got:\n%s", out)
		}
	})

	t.Run("ask with a blank question is refused before any provider work", func(t *testing.T) {
		_, err := runCLIArgs(t, root, "ask", "   ")
		if err == nil {
			t.Fatal("ask \"   \" succeeded; a blank question must be refused")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "query") && !strings.Contains(strings.ToLower(err.Error()), "question") {
			t.Errorf("refusal %q names neither the query nor the question", err)
		}
	})
}
