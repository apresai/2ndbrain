package cli

import (
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestPromotedEntry verifies the promotedEntry helper builds the right ModelInfo.
func TestPromotedEntry(t *testing.T) {
	result := &ai.TestProbeResult{
		ModelID:  "my-model",
		Provider: "openrouter",
		Type:     "generation",
		OK:       true,
		Detail:   "hello",
		Latency:  "123ms",
	}

	t.Run("nil base uses result fields only", func(t *testing.T) {
		before := time.Now().UTC().Truncate(time.Second)
		entry := promotedEntry(nil, result)
		after := time.Now().UTC().Add(time.Second).Truncate(time.Second)

		if entry.ID != "my-model" {
			t.Errorf("ID = %q, want my-model", entry.ID)
		}
		if entry.Provider != "openrouter" {
			t.Errorf("Provider = %q, want openrouter", entry.Provider)
		}
		if entry.Type != "generation" {
			t.Errorf("Type = %q, want generation", entry.Type)
		}
		if entry.Tier != ai.TierUserVerified {
			t.Errorf("Tier = %q, want %q", entry.Tier, ai.TierUserVerified)
		}
		if entry.TestedAt == "" {
			t.Error("TestedAt is empty")
		}
		tested, err := time.Parse(time.RFC3339, entry.TestedAt)
		if err != nil {
			t.Fatalf("TestedAt not RFC3339: %v", err)
		}
		if tested.Before(before) || tested.After(after) {
			t.Errorf("TestedAt %v outside [%v, %v]", tested, before, after)
		}
		// no base → zero-value enrichment fields
		if entry.Name != "" {
			t.Errorf("Name = %q, want empty", entry.Name)
		}
		if entry.Dimensions != 0 {
			t.Errorf("Dimensions = %d, want 0", entry.Dimensions)
		}
	})

	// A model the BUILTIN catalog declares owns its own facts. `base` is a row
	// from the merged catalog, so copying its name, dimensions, context length,
	// prices or notes into the user file freezes a snapshot that shadows every
	// later builtin correction: that is how a stale context_length outlived the
	// builtin's own value and spread to freshly discovered per-region rows. The
	// builtin supplies all of it again on read, so leaving the row bare loses
	// nothing.
	t.Run("a builtin model carries nothing from the merged base", func(t *testing.T) {
		builtin := findBuiltinModel("bedrock", novaEmbeddingID)
		if builtin == nil {
			t.Fatalf("%s is not in the builtin catalog; this test cannot detect mirroring without it", novaEmbeddingID)
		}
		if builtin.Name == "" || builtin.ContextLen == 0 || builtin.Dimensions == 0 || builtin.PriceIn == 0 {
			t.Fatalf("the builtin %s row no longer carries the facts this test watches: %+v", novaEmbeddingID, builtin)
		}
		// A merged row: the builtin's facts, as every save path used to see them.
		base := *builtin
		embedResult := &ai.TestProbeResult{
			ModelID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
			OK: true, Detail: "dims=1024", Latency: "123ms",
		}

		entry := promotedEntry(&base, embedResult)

		if entry.Name != "" {
			t.Errorf("Name = %q, want empty: the builtin owns it", entry.Name)
		}
		if entry.Dimensions != 0 {
			t.Errorf("Dimensions = %d, want 0: the builtin owns it, and the probe detail must not backfill it either", entry.Dimensions)
		}
		if entry.ContextLen != 0 {
			t.Errorf("ContextLen = %d, want 0: the builtin owns it", entry.ContextLen)
		}
		if entry.PriceIn != 0 || entry.PriceOut != 0 || entry.PriceRequest != 0 || entry.PriceSource != "" {
			t.Errorf("prices carried from the merged base: in=%g out=%g req=%g source=%q",
				entry.PriceIn, entry.PriceOut, entry.PriceRequest, entry.PriceSource)
		}
		if entry.Notes != "" {
			t.Errorf("Notes = %q, want empty: the builtin owns it", entry.Notes)
		}
		if entry.Recommended || entry.ConfigHint != "" {
			t.Errorf("curation carried from the merged base: recommended=%v config_hint=%q", entry.Recommended, entry.ConfigHint)
		}
		// Same rule the threshold has followed since 0.22.1.
		if entry.RecommendedSimilarityThreshold != 0 || entry.ThresholdSource != "" {
			t.Errorf("Threshold = %g (source %q), want 0 and empty",
				entry.RecommendedSimilarityThreshold, entry.ThresholdSource)
		}
		// What the promotion itself establishes is still written.
		if entry.Tier != ai.TierUserVerified || entry.TestedAt == "" {
			t.Errorf("promotion fields lost: tier=%q tested_at=%q", entry.Tier, entry.TestedAt)
		}
	})

	// A model NO builtin declares has no other source for these facts, so the
	// probe's base row (discovery or the user's own row) is what keeps the
	// saved row from being nameless and unpriced.
	t.Run("a model outside the builtin catalog keeps its authored facts", func(t *testing.T) {
		base := &ai.ModelInfo{
			ID:                             "my-model",
			Provider:                       "openrouter",
			Name:                           "My Model",
			Type:                           "generation",
			Dimensions:                     1024,
			ContextLen:                     32000,
			PriceIn:                        0.5,
			PriceOut:                       1.5,
			PriceRequest:                   0.02,
			PriceSource:                    "vendor",
			Notes:                          "fast model",
			RecommendedSimilarityThreshold: 0.6,
		}
		if findBuiltinModel(base.Provider, base.ID) != nil {
			t.Fatalf("%s/%s is in the builtin catalog; pick an id that is not", base.Provider, base.ID)
		}

		entry := promotedEntry(base, result)

		if entry.Name != "My Model" {
			t.Errorf("Name = %q, want My Model", entry.Name)
		}
		if entry.Dimensions != 1024 {
			t.Errorf("Dimensions = %d, want 1024", entry.Dimensions)
		}
		if entry.ContextLen != 32000 {
			t.Errorf("ContextLen = %d, want 32000", entry.ContextLen)
		}
		if entry.PriceIn != 0.5 {
			t.Errorf("PriceIn = %g, want 0.5", entry.PriceIn)
		}
		if entry.PriceOut != 1.5 {
			t.Errorf("PriceOut = %g, want 1.5", entry.PriceOut)
		}
		if entry.PriceRequest != 0.02 {
			t.Errorf("PriceRequest = %g, want 0.02", entry.PriceRequest)
		}
		if entry.PriceSource != "vendor" {
			t.Errorf("PriceSource = %q, want vendor", entry.PriceSource)
		}
		if entry.Notes != "fast model" {
			t.Errorf("Notes = %q, want fast model", entry.Notes)
		}
		// The threshold is deliberately NOT an enrichment field. `base` is a row
		// from the merged catalog, so copying it wrote the builtin catalog's own
		// recommendation into the user file, where it read back as a calibration
		// nobody took. Only the user's own value survives, and it is restored at
		// the save site by preserveUserThreshold, which knows the target scope.
		if entry.RecommendedSimilarityThreshold != 0 || entry.ThresholdSource != "" {
			t.Errorf("Threshold = %g (source %q), want 0 and empty: promotion must not mirror a catalog threshold",
				entry.RecommendedSimilarityThreshold, entry.ThresholdSource)
		}
		// Tier and TestedAt still come from promotion logic, not base
		if entry.Tier != ai.TierUserVerified {
			t.Errorf("Tier = %q, want %q", entry.Tier, ai.TierUserVerified)
		}
	})

	t.Run("empty PriceSource defaults to vendor when prices present", func(t *testing.T) {
		base := &ai.ModelInfo{
			PriceIn:  0.1,
			PriceOut: 0.2,
			// PriceSource intentionally empty
		}
		entry := promotedEntry(base, result)
		if entry.PriceSource != "vendor" {
			t.Errorf("PriceSource = %q, want vendor", entry.PriceSource)
		}
	})

	t.Run("explicit free PriceSource is preserved", func(t *testing.T) {
		base := &ai.ModelInfo{
			PriceIn:     0,
			PriceOut:    0,
			PriceSource: "vendor",
		}
		entry := promotedEntry(base, result)
		if entry.PriceSource != "vendor" {
			t.Errorf("PriceSource = %q, want vendor for known-free model", entry.PriceSource)
		}
	})
}

// TestFindBuiltinModel verifies the builtin catalog lookup helper.
func TestFindBuiltinModel(t *testing.T) {
	catalog := ai.BuiltinCatalog()
	if len(catalog) == 0 {
		t.Skip("builtin catalog is empty")
	}

	// Pick the first entry and look it up.
	first := catalog[0]
	found := findBuiltinModel(first.Provider, first.ID)
	if found == nil {
		t.Fatalf("findBuiltinModel(%q, %q) = nil, want entry", first.Provider, first.ID)
	}
	if found.ID != first.ID {
		t.Errorf("found.ID = %q, want %q", found.ID, first.ID)
	}

	// A nonexistent model returns nil.
	if got := findBuiltinModel("openrouter", "no-such-model/v99"); got != nil {
		t.Errorf("expected nil for unknown model, got %+v", got)
	}
}

// TestPromoteRequiresDiscover validates the --promote requires --discover guard
// without making any API calls.
func TestPromoteRequiresDiscover(t *testing.T) {
	// Reset flag state before test.
	saved := modelsPromote
	savedDiscover := modelsDiscover
	defer func() {
		modelsPromote = saved
		modelsDiscover = savedDiscover
	}()

	modelsPromote = true
	modelsDiscover = false

	err := runModelsList(nil, nil)
	if err == nil {
		t.Fatal("expected error when --promote used without --discover, got nil")
	}
	const want = "--promote requires --discover"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
