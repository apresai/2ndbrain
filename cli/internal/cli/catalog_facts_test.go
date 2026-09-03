package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// saveProbeVerdict runs the exact sequence every `--save` site runs (build the
// row from the probe result, then the four preserve/adopt helpers, then the
// wholesale save), so a test exercises the real write path rather than a
// hand-built row. No provider is called: the TestProbeResult is the shape a
// probe returns, not a stand-in for one.
func saveProbeVerdict(t *testing.T, scope ai.UserCatalogScope, vaultRoot string, result *ai.TestProbeResult) {
	t.Helper()
	entry := catalogEntryFromTestResult(context.Background(), ai.AIConfig{Provider: "bedrock"}, vaultRoot, result)
	entry.Enabled = preserveScopeEnabled(scope, vaultRoot, entry.Provider, entry.ID)
	preserveRoutingFields(scope, vaultRoot, &entry)
	preserveUserFacts(scope, vaultRoot, &entry)
	preserveUserThreshold(scope, vaultRoot, &entry)
	if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
		t.Fatalf("save probe verdict: %v", err)
	}
}

// mergedContextLen reads the context length `models list` would print for a
// model, straight from the command's own JSON, so the assertion is what a user
// sees rather than an internal call.
func mergedContextLen(t *testing.T, vaultRoot, modelID string) int {
	t.Helper()
	out, err := runCLIArgs(t, vaultRoot, "models", "list", "--json")
	if err != nil {
		t.Fatalf("models list --json: %v", err)
	}
	var rows []ai.ModelInfo
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("decode models list --json: %v\n%s", err, out)
	}
	for _, m := range rows {
		if m.Provider == "bedrock" && m.ID == modelID {
			return m.ContextLen
		}
	}
	t.Fatalf("%s is absent from models list --json:\n%s", modelID, out)
	return 0
}

// The failing-probe branch used to copy the merged base row WHOLESALE, which is
// the other half of the mirroring the promotion path had: a probe that could
// not even reach the model wrote the builtin's name, dimensions, context length
// and prices into the user file as if the user had typed them.
func TestCatalogEntryFromTestResultFailBranchCarriesNoBuiltinFacts(t *testing.T) {
	setupModelsAddHome(t)
	builtin := findBuiltinModel("bedrock", novaEmbeddingID)
	if builtin == nil {
		t.Fatalf("%s is not in the builtin catalog; this test cannot detect mirroring without it", novaEmbeddingID)
	}
	if builtin.ContextLen == 0 || builtin.Name == "" {
		t.Fatalf("the builtin %s row no longer carries the facts this test watches: %+v", novaEmbeddingID, builtin)
	}

	fail := &ai.TestProbeResult{
		ModelID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		Plane: ai.PlaneClassic, Region: "us-east-1",
		OK: false, Detail: "denied", Code: ai.TestErrAccessDenied,
	}
	got := catalogEntryFromTestResult(context.Background(), ai.AIConfig{Provider: "bedrock"}, "", fail)

	if got.ContextLen != 0 {
		t.Errorf("ContextLen = %d, want 0: a failing probe copied the builtin's context length", got.ContextLen)
	}
	if got.Name != "" {
		t.Errorf("Name = %q, want empty: a failing probe copied the builtin's name", got.Name)
	}
	if got.Dimensions != 0 {
		t.Errorf("Dimensions = %d, want 0", got.Dimensions)
	}
	if got.PriceIn != 0 || got.PriceOut != 0 || got.PriceSource != "" {
		t.Errorf("prices carried: in=%g out=%g source=%q", got.PriceIn, got.PriceOut, got.PriceSource)
	}
	if got.Notes != "" || got.Recommended || got.ConfigHint != "" {
		t.Errorf("notes/curation carried: notes=%q recommended=%v config_hint=%q", got.Notes, got.Recommended, got.ConfigHint)
	}
	// The verdict itself still lands, on the route that was actually probed.
	if got.TestErrorCode != string(ai.TestErrAccessDenied) {
		t.Errorf("TestErrorCode = %q, want %q", got.TestErrorCode, ai.TestErrAccessDenied)
	}
	if got.Plane != ai.PlaneClassic || got.Region != "us-east-1" {
		t.Errorf("route lost: plane=%q region=%q", got.Plane, got.Region)
	}
}

// `models add --context-length` is the only way to author a context length, so
// it stamps the row, and a later probe save must not lose the number the user
// typed. The stamped row is route-less (models add describes a MODEL), so what
// has to survive is the MERGED value the user reads back.
func TestModelsAddStampsTypedFactsAndAProbeSaveKeepsThem(t *testing.T) {
	_, root := newContractVault(t)
	resetModelsAddFlags(t)

	if _, err := runCLIArgs(t, root, "models", "add", novaEmbeddingID,
		"--provider", "bedrock", "--type", "embedding",
		"--context-length", "4096", "--scope", "vault"); err != nil {
		t.Fatalf("models add: %v", err)
	}

	var stamped bool
	for _, m := range ai.LoadUserCatalog(root) {
		if m.ID != novaEmbeddingID {
			continue
		}
		stamped = true
		if m.ContextLen != 4096 {
			t.Errorf("stored context_length = %d, want 4096", m.ContextLen)
		}
		if !ai.HasAuthoredFact(m, ai.FactContextLen) {
			t.Errorf("authored_facts = %v, want it to list %q: an unlisted fact is ignored by the merge", m.AuthoredFacts, ai.FactContextLen)
		}
		if ai.HasAuthoredFact(m, ai.FactName) || ai.HasAuthoredFact(m, ai.FactDimensions) {
			t.Errorf("authored_facts = %v: only the fact the user typed may be listed", m.AuthoredFacts)
		}
	}
	if !stamped {
		t.Fatal("models add wrote no row for the model")
	}
	if got := mergedContextLen(t, root, novaEmbeddingID); got != 4096 {
		t.Fatalf("models list context_len = %d, want the typed 4096", got)
	}

	saveProbeVerdict(t, ai.ScopeVault, root, &ai.TestProbeResult{
		ModelID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		Plane: ai.PlaneClassic, Region: "us-east-1",
		OK: true, Detail: "dims=1024",
	})

	// The probe writes a CONCRETE route row; the stamped row stays route-less,
	// so the value survives as the model-level template that
	// retireSupersededTemplates redistributes, not by being copied onto the new
	// row. Either way it is still what `models list` prints, which is the
	// contract that matters.
	if got := mergedContextLen(t, root, novaEmbeddingID); got != 4096 {
		t.Errorf("a probe save lost the user's typed context length: models list shows %d, want 4096", got)
	}
}

// preserveUserFacts' carry-forward branch, at a route its lookup can match.
// UserCatalogRouteToPreserve refuses to graft across endpoints, so it returns
// nothing when the probe names a full plane+region that no stored row has; this
// is the case where the stored row IS the probed route and the stamped value
// has to ride through a wholesale save that would otherwise delete it.
func TestProbeSaveKeepsAStampedFactOnTheProbedRoute(t *testing.T) {
	_, root := newContractVault(t)

	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		Plane: ai.PlaneClassic, Region: "us-east-1",
		ContextLen: 4096, AuthoredFacts: []string{ai.FactContextLen},
	}); err != nil {
		t.Fatalf("seed stamped row: %v", err)
	}

	saveProbeVerdict(t, ai.ScopeVault, root, &ai.TestProbeResult{
		ModelID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		Plane: ai.PlaneClassic, Region: "us-east-1",
		OK: true, Detail: "dims=1024",
	})

	stored, ok := ai.UserCatalogEntry(ai.ScopeVault, root, ai.RouteKey{
		Provider: "bedrock", ID: novaEmbeddingID, Plane: ai.PlaneClassic, Region: "us-east-1",
	})
	if !ok {
		t.Fatal("the probe save wrote no row for the probed route")
	}
	if stored.ContextLen != 4096 {
		t.Errorf("stored context_length = %d, want the stamped 4096", stored.ContextLen)
	}
	if !ai.HasAuthoredFact(stored, ai.FactContextLen) {
		t.Errorf("authored_facts = %v, want it to list %q: an unlisted survivor is ignored on the next read", stored.AuthoredFacts, ai.FactContextLen)
	}
	if stored.TestedAt == "" {
		t.Error("the probe verdict itself was not recorded")
	}
}

// The write-side half of the self-heal. A catalog contaminated by an older
// version carries an UNSTAMPED context_length copied off a builtin that has
// since changed; the next probe save drops it rather than rewriting it, and the
// merged view returns to the builtin's current value with nothing to clean up.
func TestProbeSaveDropsAnUnstampedContextLength(t *testing.T) {
	_, root := newContractVault(t)
	builtin := findBuiltinModel("bedrock", novaEmbeddingID)
	if builtin == nil || builtin.ContextLen == 0 {
		t.Fatalf("%s has no builtin context length; this test cannot detect the self-heal", novaEmbeddingID)
	}

	// Written as TEXT: the absence of a fact_source key is the contaminated
	// shape, and no ModelInfo literal can express "field never written".
	catalogPath := filepath.Join(root, ".2ndbrain", "models.yaml")
	if err := os.WriteFile(catalogPath, []byte(
		"version: 1\nmodels:\n"+
			"  - id: "+novaEmbeddingID+"\n    provider: bedrock\n    type: embedding\n"+
			"    plane: classic\n    region: us-east-1\n"+
			"    context_length: 2048\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mergedContextLen(t, root, novaEmbeddingID); got != 2048 {
		t.Fatalf("precondition: the contaminated catalog should read back as 2048, got %d", got)
	}

	saveProbeVerdict(t, ai.ScopeVault, root, &ai.TestProbeResult{
		ModelID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		Plane: ai.PlaneClassic, Region: "us-east-1",
		OK: true, Detail: "dims=1024",
	})

	stored, ok := ai.UserCatalogEntry(ai.ScopeVault, root, ai.RouteKey{
		Provider: "bedrock", ID: novaEmbeddingID, Plane: ai.PlaneClassic, Region: "us-east-1",
	})
	if !ok {
		t.Fatal("the probe save wrote no row for the probed route")
	}
	if stored.ContextLen != 0 {
		t.Errorf("stored context_length = %d, want 0: the unstamped copy must not be rewritten", stored.ContextLen)
	}
	// Replaced, not appended: a second row would leave the contaminated 2048 on
	// disk, and the merged read below would pass while the file stayed dirty.
	rows := ai.LoadUserCatalog(root)
	if len(rows) != 1 {
		t.Fatalf("the save appended instead of replacing: %d rows, want 1: %+v", len(rows), rows)
	}
	if got := mergedContextLen(t, root, novaEmbeddingID); got != builtin.ContextLen {
		t.Errorf("models list context_len = %d, want the builtin %d", got, builtin.ContextLen)
	}
}

// Dropping an unstamped threshold is deliberate, so it has to be SAID. The
// value 0.65 is the case that motivated the rule: 2nb's own Nova recommendation
// until June 2026, now 2.6x the current 0.25.
func TestUnstampedThresholdNoticeNamesTheFile(t *testing.T) {
	setupModelsAddHome(t)
	vaultRoot := t.TempDir()

	if got := unstampedThresholdNotice(vaultRoot, "bedrock", novaEmbeddingID); got != "" {
		t.Errorf("with nothing stored the notice must be empty, got %q", got)
	}

	// A STAMPED row is applied, not ignored, so it produces no notice.
	if err := ai.SaveUserCatalogEntry(ai.ScopeGlobal, "", ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		RecommendedSimilarityThreshold: 0.41, ThresholdSource: ai.ThresholdSourceUser,
	}); err != nil {
		t.Fatalf("seed stamped: %v", err)
	}
	if got := unstampedThresholdNotice(vaultRoot, "bedrock", novaEmbeddingID); got != "" {
		t.Errorf("a stamped calibration must produce no ignored-value notice, got %q", got)
	}

	if err := ai.SaveUserCatalogEntry(ai.ScopeGlobal, "", ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		RecommendedSimilarityThreshold: 0.65,
	}); err != nil {
		t.Fatalf("seed unstamped: %v", err)
	}
	globalPath, err := ai.CatalogPathForScope(ai.ScopeGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	got := unstampedThresholdNotice(vaultRoot, "bedrock", novaEmbeddingID)
	if !strings.Contains(got, globalPath) {
		t.Errorf("notice %q does not name the file that carries the value (%s)", got, globalPath)
	}
	if !strings.Contains(got, "0.65") {
		t.Errorf("notice %q does not name the value it ignored", got)
	}
	if !strings.Contains(got, "ignored") {
		t.Errorf("notice %q does not say the value is ignored", got)
	}
	if !strings.Contains(got, "models calibrate --save") || !strings.Contains(got, "--similarity-threshold") {
		t.Errorf("notice %q does not say how to keep the value", got)
	}
}

// `models add` describes a MODEL, so it writes a route-less row. The stored row
// it means to update has been upgraded to its builtin's plane on read
// (canonicalizeUserRoutes), so before the save canonicalized the incoming row
// too, the route keys missed and the add APPENDED a twin. The model then had two
// template rows, and the user-user overlay merged the stale one's facts back
// over the fresh one, so an edit appeared not to take.
func TestModelsAddUpdatesAPreRouteRowInPlace(t *testing.T) {
	_, root := newContractVault(t)
	resetModelsAddFlags(t)

	catalogPath := filepath.Join(root, ".2ndbrain", "models.yaml")
	// A row written before routes existed: no plane, no region.
	if err := os.WriteFile(catalogPath, []byte(
		"version: 1\nmodels:\n"+
			"  - id: "+novaEmbeddingID+"\n    provider: bedrock\n    type: embedding\n"+
			"    context_length: 2048\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLIArgs(t, root, "models", "add", novaEmbeddingID,
		"--provider", "bedrock", "--type", "embedding",
		"--context-length", "4096", "--scope", "vault"); err != nil {
		t.Fatalf("models add: %v", err)
	}

	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "- id: "+novaEmbeddingID); got != 1 {
		t.Errorf("the add wrote %d rows for the model, want 1 (it appended a twin instead of updating):\n%s", got, data)
	}
	rows := ai.LoadUserCatalog(root)
	if len(rows) != 1 {
		t.Fatalf("merged user catalog holds %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].ContextLen != 4096 {
		t.Errorf("context_length = %d, want the value just added, 4096", rows[0].ContextLen)
	}
}

// mergedFact reads one model fact as `models list` would print it, straight
// from the command's own JSON.
func mergedFact(t *testing.T, vaultRoot, modelID string) ai.ModelInfo {
	t.Helper()
	out, err := runCLIArgs(t, vaultRoot, "models", "list", "--json")
	if err != nil {
		t.Fatalf("models list --json: %v", err)
	}
	var rows []ai.ModelInfo
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("decode models list --json: %v\n%s", err, out)
	}
	for _, m := range rows {
		if m.Provider == "bedrock" && m.ID == modelID {
			return m
		}
	}
	t.Fatalf("%s is absent from models list --json:\n%s", modelID, out)
	return ai.ModelInfo{}
}

// Bugbot B1 (HIGH), reproduced by the challenger: one stamp for three facts
// meant `models add --context-length` claimed authorship of whatever name and
// dimensions the stored row happened to carry. Those came from an older probe
// save that had copied them off the builtin, so the add froze a mirror the
// merge could never self-heal again.
func TestModelsAddAuthorsOnlyTheFactsTyped(t *testing.T) {
	_, root := newContractVault(t)
	resetModelsAddFlags(t)

	builtin := findBuiltinModel("bedrock", novaEmbeddingID)
	if builtin == nil || builtin.Name == "" || builtin.Dimensions == 0 {
		t.Fatalf("the builtin %s row no longer carries the facts this test watches", novaEmbeddingID)
	}

	// The contaminated stored row: a stale name and dimensions an older save
	// copied off the builtin, with no provenance.
	if err := os.WriteFile(filepath.Join(root, ".2ndbrain", "models.yaml"), []byte(
		"version: 1\nmodels:\n"+
			"  - id: "+novaEmbeddingID+"\n    provider: bedrock\n    type: embedding\n"+
			"    name: Nova (stale copy)\n    dimensions: 384\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLIArgs(t, root, "models", "add", novaEmbeddingID,
		"--provider", "bedrock", "--type", "embedding",
		"--context-length", "4096", "--scope", "vault"); err != nil {
		t.Fatalf("models add: %v", err)
	}

	var found bool
	for _, m := range ai.LoadUserCatalog(root) {
		if m.ID != novaEmbeddingID {
			continue
		}
		found = true
		if !ai.HasAuthoredFact(m, ai.FactContextLen) {
			t.Errorf("authored_facts = %v, want it to list the typed context_length", m.AuthoredFacts)
		}
		if ai.HasAuthoredFact(m, ai.FactName) || ai.HasAuthoredFact(m, ai.FactDimensions) {
			t.Errorf("authored_facts = %v: the add claimed facts the user never typed", m.AuthoredFacts)
		}
		if m.Name != "" || m.Dimensions != 0 {
			t.Errorf("the stale mirror survived the add: name=%q dimensions=%d", m.Name, m.Dimensions)
		}
	}
	if !found {
		t.Fatal("models add wrote no row for the model")
	}

	merged := mergedFact(t, root, novaEmbeddingID)
	if merged.ContextLen != 4096 {
		t.Errorf("models list context_length = %d, want the typed 4096", merged.ContextLen)
	}
	if merged.Name != builtin.Name {
		t.Errorf("models list name = %q, want the builtin's %q back", merged.Name, builtin.Name)
	}
	if merged.Dimensions != builtin.Dimensions {
		t.Errorf("models list dimensions = %d, want the builtin's %d back", merged.Dimensions, builtin.Dimensions)
	}
}

// The clearing above is scoped to models the builtin catalog declares. For a
// model it has never seen, the stored row is the only copy of its facts, so an
// add that touches one fact must not wipe the other two.
func TestModelsAddKeepsANonBuiltinModelsFactsThroughAddAndProbe(t *testing.T) {
	_, root := newContractVault(t)
	resetModelsAddFlags(t)
	const id = "made.up.model"
	if findBuiltinModel("bedrock", id) != nil {
		t.Fatalf("%s is in the builtin catalog; pick an id that is not", id)
	}

	if _, err := runCLIArgs(t, root, "models", "add", id,
		"--provider", "bedrock", "--type", "generation",
		"--name", "My Model", "--scope", "vault"); err != nil {
		t.Fatalf("models add --name: %v", err)
	}
	resetModelsAddFlags(t)
	if _, err := runCLIArgs(t, root, "models", "add", id,
		"--provider", "bedrock", "--type", "generation",
		"--context-length", "9999", "--scope", "vault"); err != nil {
		t.Fatalf("models add --context-length: %v", err)
	}

	for _, m := range ai.LoadUserCatalog(root) {
		if m.ID != id {
			continue
		}
		if m.Name != "My Model" {
			t.Errorf("a later add wiped a non-builtin model's authored name: got %q", m.Name)
		}
		if m.ContextLen != 9999 {
			t.Errorf("context_length = %d, want 9999", m.ContextLen)
		}
		if !ai.HasAuthoredFact(m, ai.FactName) || !ai.HasAuthoredFact(m, ai.FactContextLen) {
			t.Errorf("authored_facts = %v, want both facts listed across the two adds", m.AuthoredFacts)
		}
	}

	saveProbeVerdict(t, ai.ScopeVault, root, &ai.TestProbeResult{
		ModelID: id, Provider: "bedrock", Type: "generation",
		Plane: ai.PlaneClassic, Region: "us-east-1",
		OK: true, Detail: "ok",
	})
	if got := mergedFact(t, root, id); got.Name != "My Model" {
		t.Errorf("a probe save lost a non-builtin model's name: got %q", got.Name)
	}
}

// The vault scope beats the global one for the same route, and the value it
// wins with is the one that then donates to the model's other routes. The
// scope question is settled by LoadUserCatalog before inheritance runs, which
// is why the donor tie break below it never has to ask about scope.
func TestAuthoredFactPrefersTheVaultScope(t *testing.T) {
	_, root := newContractVault(t)
	if err := ai.SaveUserCatalogEntry(ai.ScopeGlobal, "", ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding", Plane: ai.PlaneClassic,
		ContextLen: 1111, AuthoredFacts: []string{ai.FactContextLen},
	}); err != nil {
		t.Fatalf("seed global: %v", err)
	}
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding", Plane: ai.PlaneClassic,
		ContextLen: 2222, AuthoredFacts: []string{ai.FactContextLen},
	}); err != nil {
		t.Fatalf("seed vault: %v", err)
	}
	if got := mergedFact(t, root, novaEmbeddingID); got.ContextLen != 2222 {
		t.Errorf("models list context_length = %d, want the vault row's 2222", got.ContextLen)
	}
}

// saveVerifyVerdict runs the sequence `models verify` and `discover --validate`
// run, candidate row and all, so the test exercises the real order rather than
// a reconstruction of it.
func saveVerifyVerdict(t *testing.T, scope ai.UserCatalogScope, vaultRoot string, candidate ai.ModelInfo, result *ai.TestProbeResult) {
	t.Helper()
	entry := catalogEntryFromTestResult(context.Background(), ai.AIConfig{Provider: "bedrock"}, vaultRoot, result)
	entry.Enabled = preserveScopeEnabled(scope, vaultRoot, entry.Provider, entry.ID)
	preserveRoutingFields(scope, vaultRoot, &entry)
	adoptCandidateRouting(&entry, candidate)
	preserveUserFacts(scope, vaultRoot, &entry)
	preserveUserThreshold(scope, vaultRoot, &entry)
	if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
		t.Fatalf("save verify verdict: %v", err)
	}
}

// Go reviewer G1 (HIGH): the probe save calls adoptCandidateRouting with a row
// from the MERGED catalog, and AdoptRoutingHints filled ContextLen from it with
// no provenance gate. So the builtin's own context length was written into the
// raw user file, unlisted, on every verify, every discover --validate and every
// promote. The read side hides it; the file accumulates the mirror. Nothing
// downstream can undo it either, because preserveUserFacts only ever SETS.
func TestVerifySaveNeverWritesABuiltinContextLength(t *testing.T) {
	_, root := newContractVault(t)
	builtin := findBuiltinModel("bedrock", novaEmbeddingID)
	if builtin == nil || builtin.ContextLen == 0 {
		t.Fatalf("the builtin %s row no longer declares a context length", novaEmbeddingID)
	}

	// The candidate `models verify` iterates is a merged row, so it carries the
	// builtin's facts.
	candidate := *builtin
	candidate.Plane, candidate.Region = ai.PlaneClassic, "us-east-1"

	saveVerifyVerdict(t, ai.ScopeVault, root, candidate, &ai.TestProbeResult{
		ModelID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		Plane: ai.PlaneClassic, Region: "us-east-1",
		OK: true, Detail: "dims=1024",
	})

	stored, ok := ai.UserCatalogEntry(ai.ScopeVault, root, ai.RouteKey{
		Provider: "bedrock", ID: novaEmbeddingID, Plane: ai.PlaneClassic, Region: "us-east-1",
	})
	if !ok {
		t.Fatal("the verify save wrote no row for the probed route")
	}
	if stored.ContextLen != 0 {
		t.Errorf("stored context_length = %d, want it absent: the builtin's own value was mirrored into the user file", stored.ContextLen)
	}
	if stored.Name != "" || stored.Dimensions != 0 {
		t.Errorf("other builtin facts were mirrored too: name=%q dimensions=%d", stored.Name, stored.Dimensions)
	}
	// The merged view still shows the builtin's value, which is the point of
	// not persisting it.
	if got := mergedFact(t, root, novaEmbeddingID); got.ContextLen != builtin.ContextLen {
		t.Errorf("models list context_length = %d, want the builtin's %d", got.ContextLen, builtin.ContextLen)
	}
}

// The gate is scoped to models the builtin catalog declares. A DISCOVERED model
// it has never seen has no other source for its context length, so the
// discovery hint is still adopted and persisted.
func TestVerifySaveKeepsADiscoveredModelsContextLength(t *testing.T) {
	_, root := newContractVault(t)
	const id = "vendor.discovered-only"
	if findBuiltinModel("bedrock", id) != nil {
		t.Fatalf("%s is in the builtin catalog; pick an id that is not", id)
	}

	candidate := ai.ModelInfo{
		ID: id, Provider: "bedrock", Type: "generation",
		Plane: ai.PlaneMantle, Region: "us-west-2",
		InvokeStrategy: ai.StrategyBedrockMantleResponses,
		ContextLen:     128000,
	}
	saveVerifyVerdict(t, ai.ScopeVault, root, candidate, &ai.TestProbeResult{
		ModelID: id, Provider: "bedrock", Type: "generation",
		Plane: ai.PlaneMantle, Region: "us-west-2",
		OK: true, Detail: "ok",
	})

	stored, ok := ai.UserCatalogEntry(ai.ScopeVault, root, ai.RouteKey{
		Provider: "bedrock", ID: id, Plane: ai.PlaneMantle, Region: "us-west-2",
	})
	if !ok {
		t.Fatal("the verify save wrote no row for the probed route")
	}
	if stored.ContextLen != 128000 {
		t.Errorf("stored context_length = %d, want the discovery hint's 128000: nothing else knows it", stored.ContextLen)
	}
	if stored.InvokeStrategy != ai.StrategyBedrockMantleResponses {
		t.Errorf("the routing hint itself stopped being adopted: invoke_strategy = %q", stored.InvokeStrategy)
	}
}
