package retrieve

import (
	"context"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

// The blank-query guard lives here because this package exists so the CLI and
// MCP paths cannot drift, and they had: kb_search was fixed while `2nb search
// ""` still dumped the whole vault as ranked hits. Every entry point (2nb
// search, 2nb ask, kb_search, kb_ask) funnels through Retrieve.
//
// The guard is conditional on purpose. An empty query WITH a structured filter
// is the engine's enumerate-by-filter path, and `2nb search "type:adr"` reaches
// it legitimately: the CLI's prefix extraction consumes the whole string into
// Type and leaves Query empty. The first version of this guard refused that too,
// which was a regression against 0.21.1.
func TestRetrieve_BlankQueryRefusedOnlyWithoutFilters(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Decision", "adr", "we decided on tokens")
	testutil.CreateAndIndex(t, v, "Scratch", "note", "a scratch note")

	t.Run("blank with no filter is refused", func(t *testing.T) {
		for _, q := range []string{"", "   ", "\t\n"} {
			_, err := New(v).Retrieve(context.Background(), Options{Query: q, Limit: 10})
			if err == nil {
				t.Errorf("query %q with no filter returned no error; it would have listed every document as a search result", q)
				continue
			}
			if !strings.Contains(err.Error(), "query") {
				t.Errorf("refusal %q does not mention the query", err)
			}
		}
	})

	t.Run("blank with a type filter enumerates by filter", func(t *testing.T) {
		res, err := New(v).Retrieve(context.Background(), Options{Query: "", Type: "adr", Limit: 10})
		if err != nil {
			t.Fatalf("blank query with --type must enumerate, got error: %v", err)
		}
		if len(res.Results) != 1 || res.Results[0].DocType != "adr" {
			t.Errorf("want exactly the one adr, got %+v", res.Results)
		}
	})
}
