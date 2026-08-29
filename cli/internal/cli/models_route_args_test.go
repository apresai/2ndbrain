package cli

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func seedDualRoutes(t *testing.T, root, id string) {
	t.Helper()
	for _, region := range []string{"us-east-1", "us-west-2"} {
		if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
			ID: id, Provider: "bedrock", Type: "generation",
			Tier: ai.TierUserVerified, Plane: ai.PlaneClassic, Region: region,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModelsRemoveQualifiedRouteLeavesSibling(t *testing.T) {
	_, root := newContractVault(t)
	id := "fake.dual-gen"
	seedDualRoutes(t, root, id)

	out, err := runCLIArgs(t, root, "models", "remove", id+"@classic/us-east-1",
		"--provider", "bedrock", "--scope", "vault")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	var east, west int
	for _, m := range ai.LoadUserCatalog(root) {
		if m.ID != id {
			continue
		}
		if m.Region == "us-east-1" {
			east++
		}
		if m.Region == "us-west-2" {
			west++
		}
	}
	if east != 0 {
		t.Errorf("classic/us-east-1 row survived the remove")
	}
	if west != 1 {
		t.Errorf("sibling classic/us-west-2 was removed; want it left intact (got %d)", west)
	}
}

func TestModelsRemoveMissingRouteExitsNonZero(t *testing.T) {
	_, root := newContractVault(t)
	seedDualRoutes(t, root, "fake.dual-gen")

	_, err := runCLIArgs(t, root, "models", "remove", "fake.dual-gen@classic/eu-central-1",
		"--provider", "bedrock", "--scope", "vault")
	if err == nil {
		t.Fatal("expected non-zero exit when nothing matched")
	}
	if !strings.Contains(err.Error(), "nothing matched") {
		t.Errorf("error = %v, want nothing matched", err)
	}
	n := 0
	for _, m := range ai.LoadUserCatalog(root) {
		if m.ID == "fake.dual-gen" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("a failed remove mutated the catalog: %d rows remain, want 2", n)
	}
}

func TestModelsAddRefusesQualifiedRoute(t *testing.T) {
	_, root := newContractVault(t)
	_, err := runCLIArgs(t, root, "models", "add", "xai.grok-4.6@mantle",
		"--provider", "bedrock", "--type", "generation", "--scope", "vault")
	if err == nil {
		t.Fatal("expected models add to refuse a route-qualified id")
	}
	if !strings.Contains(err.Error(), "bare model id") {
		t.Errorf("error = %v, want a pointer at the bare id", err)
	}
	for _, m := range ai.LoadUserCatalog(root) {
		if strings.Contains(m.ID, "@") {
			t.Fatalf("add minted a bogus id containing @: %q", m.ID)
		}
	}
}
