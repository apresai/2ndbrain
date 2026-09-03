package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// writeVaultCatalog writes .2ndbrain/models.yaml verbatim. The contaminated
// shapes below are written as TEXT because the point is which keys are ABSENT
// (no authored_facts, no threshold_source), and no ModelInfo literal can
// express "this field was never written".
func writeVaultCatalog(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".2ndbrain", "models.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write vault catalog: %v", err)
	}
}

// mergedRows returns every `models list --json` row for one model, which is
// also how the test notices a duplicate.
func mergedRows(t *testing.T, vaultRoot, provider, modelID string) []ai.ModelInfo {
	t.Helper()
	out, err := runCLIArgs(t, vaultRoot, "models", "list", "--json")
	if err != nil {
		t.Fatalf("models list --json: %v", err)
	}
	var rows []ai.ModelInfo
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("decode models list --json: %v\n%s", err, out)
	}
	var hits []ai.ModelInfo
	for _, m := range rows {
		if m.Provider == provider && m.ID == modelID {
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 {
		t.Fatalf("%s is absent from models list --json:\n%s", modelID, out)
	}
	return hits
}

// TestPinnedRouteRowShowsTheBuiltinFactsNotItsOwnUnauthoredCopies is the shape
// a released 0.22.2 vault was actually in: an August `models test --save` with
// a region fallback left the Nova row pinned to classic/us-east-1 carrying an
// unauthored context_length of 2048 and an unstamped threshold of 0.65.
//
// The builtin Nova row is authored route-less, so the two never share a route
// key: mergeFields (which owns these rules) never ran for that row, the pinned
// row superseded the builtin template instead, and template retirement only
// fills EMPTY facts. So `models list --json` printed 2048 and 0.65 while
// `ai status` correctly ignored the 0.65 and named the file it came from. Two
// commands, one model, two answers.
func TestPinnedRouteRowShowsTheBuiltinFactsNotItsOwnUnauthoredCopies(t *testing.T) {
	_, root := newContractVault(t)
	builtin := findBuiltinModel("bedrock", novaEmbeddingID)
	if builtin == nil || builtin.ContextLen == 0 || builtin.RecommendedSimilarityThreshold == 0 {
		t.Fatalf("the builtin %s row no longer carries the facts this test watches: %+v", novaEmbeddingID, builtin)
	}

	writeVaultCatalog(t, root, "version: 1\nmodels:\n"+
		"  - id: "+novaEmbeddingID+"\n    provider: bedrock\n    type: embedding\n"+
		"    plane: classic\n    region: us-east-1\n"+
		"    context_length: 2048\n"+
		"    recommended_similarity_threshold: 0.65\n")

	rows := mergedRows(t, root, "bedrock", novaEmbeddingID)
	if len(rows) != 1 {
		t.Fatalf("models list shows %d rows for %s, want 1", len(rows), novaEmbeddingID)
	}
	m := rows[0]
	if m.ContextLen != builtin.ContextLen {
		t.Errorf("context_length = %d, want the builtin %d: an unauthored copy is not authorship", m.ContextLen, builtin.ContextLen)
	}
	if m.RecommendedSimilarityThreshold != builtin.RecommendedSimilarityThreshold {
		t.Errorf("recommended_similarity_threshold = %v, want the builtin %v: this is the number the resolver uses, so it is the number to show",
			m.RecommendedSimilarityThreshold, builtin.RecommendedSimilarityThreshold)
	}
	if m.ThresholdSource != "" {
		t.Errorf("threshold_source = %q, want empty: nobody authored this threshold", m.ThresholdSource)
	}
	if len(m.AuthoredFacts) != 0 {
		t.Errorf("authored_facts = %v, want empty: the user typed none of these", m.AuthoredFacts)
	}
	if m.Name != builtin.Name {
		t.Errorf("name = %q, want the builtin %q", m.Name, builtin.Name)
	}
	if m.Dimensions != builtin.Dimensions {
		t.Errorf("dimensions = %d, want the builtin %d", m.Dimensions, builtin.Dimensions)
	}
}

// TestPinnedRouteRowKeepsAnAuthoredContextLength is the other half: the rule is
// about AUTHORSHIP, not about the route. A value the user typed survives on a
// pinned row exactly as it does on a route-less one.
func TestPinnedRouteRowKeepsAnAuthoredContextLength(t *testing.T) {
	_, root := newContractVault(t)
	builtin := findBuiltinModel("bedrock", novaEmbeddingID)
	if builtin == nil || builtin.ContextLen == 4096 {
		t.Fatalf("the builtin %s context length collides with this test's value: %+v", novaEmbeddingID, builtin)
	}

	writeVaultCatalog(t, root, "version: 1\nmodels:\n"+
		"  - id: "+novaEmbeddingID+"\n    provider: bedrock\n    type: embedding\n"+
		"    plane: classic\n    region: us-east-1\n"+
		"    context_length: 4096\n"+
		"    authored_facts:\n      - context_length\n"+
		"    recommended_similarity_threshold: 0.42\n"+
		"    threshold_source: user\n")

	rows := mergedRows(t, root, "bedrock", novaEmbeddingID)
	m := rows[0]
	if m.ContextLen != 4096 {
		t.Errorf("context_length = %d, want the authored 4096", m.ContextLen)
	}
	if !ai.HasAuthoredFact(m, ai.FactContextLen) {
		t.Errorf("authored_facts = %v, want it to still list %q", m.AuthoredFacts, ai.FactContextLen)
	}
	if m.RecommendedSimilarityThreshold != 0.42 || m.ThresholdSource != ai.ThresholdSourceUser {
		t.Errorf("threshold = %v (source %q), want the stamped 0.42 from the user",
			m.RecommendedSimilarityThreshold, m.ThresholdSource)
	}
	// The facts the user did NOT type still come from the builtin.
	if m.Dimensions != builtin.Dimensions {
		t.Errorf("dimensions = %d, want the builtin %d: authoring one fact does not claim the others", m.Dimensions, builtin.Dimensions)
	}
}

// TestPinnedRouteRowForANonBuiltinModelKeepsItsFacts: a model no builtin
// declares has no owner to defer to, so its row keeps everything it carries.
func TestPinnedRouteRowForANonBuiltinModelKeepsItsFacts(t *testing.T) {
	_, root := newContractVault(t)
	const unknownID = "acme.not-a-builtin-embedder-v9"
	if findBuiltinModel("bedrock", unknownID) != nil {
		t.Fatalf("%s is in the builtin catalog; pick an id that is not", unknownID)
	}

	writeVaultCatalog(t, root, "version: 1\nmodels:\n"+
		"  - id: "+unknownID+"\n    provider: bedrock\n    type: embedding\n"+
		"    plane: classic\n    region: us-east-1\n"+
		"    name: Acme Embedder\n    dimensions: 512\n    context_length: 2048\n"+
		"    recommended_similarity_threshold: 0.65\n")

	m := mergedRows(t, root, "bedrock", unknownID)[0]
	if m.Name != "Acme Embedder" {
		t.Errorf("name = %q, want the row's own %q", m.Name, "Acme Embedder")
	}
	if m.ContextLen != 2048 {
		t.Errorf("context_length = %d, want the row's own 2048", m.ContextLen)
	}
	if m.Dimensions != 512 {
		t.Errorf("dimensions = %d, want the row's own 512", m.Dimensions)
	}
	if m.RecommendedSimilarityThreshold != 0.65 {
		t.Errorf("recommended_similarity_threshold = %v, want the row's own 0.65: no builtin declares this model", m.RecommendedSimilarityThreshold)
	}
}
