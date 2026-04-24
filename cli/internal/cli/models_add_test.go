package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/spf13/cobra"
)

func resetModelsAddState() {
	addProvider = ""
	addType = ""
	addName = ""
	addDimensions = 0
	addContextLen = 0
	addPriceIn = 0
	addPriceOut = 0
	addPriceRequest = 0
	addThreshold = 0
	addNotes = ""
	addScope = "global"
}

func newTestModelsAddCommand() *cobra.Command {
	resetModelsAddState()
	cmd := &cobra.Command{}
	flags := cmd.Flags()
	flags.StringVar(&addProvider, "provider", "", "")
	flags.StringVar(&addType, "type", "", "")
	flags.StringVar(&addName, "name", "", "")
	flags.IntVar(&addDimensions, "dimensions", 0, "")
	flags.IntVar(&addContextLen, "context-length", 0, "")
	flags.Float64Var(&addPriceIn, "price-in", 0, "")
	flags.Float64Var(&addPriceOut, "price-out", 0, "")
	flags.Float64Var(&addPriceRequest, "price-request", 0, "")
	flags.Float64Var(&addThreshold, "similarity-threshold", 0, "")
	flags.StringVar(&addNotes, "notes", "", "")
	flags.StringVar(&addScope, "scope", "global", "")
	return cmd
}

func setupModelsAddHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
}

func TestRunModelsAddWithoutPriceFlagsLeavesPricingUnspecified(t *testing.T) {
	setupModelsAddHome(t)
	cmd := newTestModelsAddCommand()
	if err := cmd.ParseFlags([]string{"--provider", "bedrock", "--type", "generation"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if err := runModelsAdd(cmd, []string{"deepseek.v3.2"}); err != nil {
		t.Fatalf("runModelsAdd: %v", err)
	}

	models := ai.LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected 1 saved model, got %d", len(models))
	}
	if models[0].PriceSource != "" {
		t.Fatalf("expected price_source to remain empty without explicit price flags, got %q", models[0].PriceSource)
	}
	if models[0].PriceOverride {
		t.Fatal("expected price_override=false without explicit price flags")
	}
}

func TestRunModelsAddExplicitZeroPriceSetsOverride(t *testing.T) {
	setupModelsAddHome(t)
	cmd := newTestModelsAddCommand()
	if err := cmd.ParseFlags([]string{"--provider", "bedrock", "--type", "generation", "--price-in", "0"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if err := runModelsAdd(cmd, []string{"qwen.qwen3-coder-next"}); err != nil {
		t.Fatalf("runModelsAdd: %v", err)
	}

	models := ai.LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected 1 saved model, got %d", len(models))
	}
	if models[0].PriceSource != "user" {
		t.Fatalf("expected explicit zero price to be tagged as user pricing, got %q", models[0].PriceSource)
	}
	if !models[0].PriceOverride {
		t.Fatal("expected explicit zero price to set price_override=true")
	}
}

// newTestEnableDisableCommand builds a minimal cobra.Command wired to the
// package-level enable/disable flag variables. The caller must set the vars
// AFTER calling this function because cobra.StringVar initialises them to the
// default value when the flag is registered.
func newTestEnableDisableCommand(providerVar *string, scopeVar *string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().StringVar(providerVar, "provider", "", "")
	cmd.Flags().StringVar(scopeVar, "scope", "global", "")
	return cmd
}

func TestRunModelsEnable_SetsEnabledTrue(t *testing.T) {
	setupModelsAddHome(t)
	// Start with a disabled entry.
	disabled := false
	_ = ai.SaveUserCatalogEntry(ai.ScopeGlobal, "", ai.ModelInfo{
		ID:       "some.model.v1",
		Provider: "bedrock",
		Tier:     ai.TierUserVerified,
		Enabled:  &disabled,
	})

	// Build command first — cobra.StringVar resets vars to defaults.
	cmd := newTestEnableDisableCommand(&enableProvider, &enableScope)
	// Set provider/scope after registration so Cobra's default doesn't clobber them.
	enableProvider = "bedrock"
	enableScope = "global"

	if err := runModelsEnable(cmd, []string{"some.model.v1"}); err != nil {
		t.Fatalf("runModelsEnable: %v", err)
	}

	models := ai.LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Enabled == nil || !*models[0].Enabled {
		t.Fatalf("expected Enabled=true after enable, got %v", models[0].Enabled)
	}
}

func TestRunModelsDisable_SetsEnabledFalse(t *testing.T) {
	setupModelsAddHome(t)

	cmd := newTestEnableDisableCommand(&disableProvider, &disableScope)
	disableProvider = "bedrock"
	disableScope = "global"

	// Disable a model that doesn't yet have a user-catalog entry (builtin-only).
	if err := runModelsDisable(cmd, []string{"builtin.only.model"}); err != nil {
		t.Fatalf("runModelsDisable: %v", err)
	}

	models := ai.LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected 1 model created for builtin-only model, got %d", len(models))
	}
	m := models[0]
	if m.ID != "builtin.only.model" || m.Provider != "bedrock" {
		t.Fatalf("unexpected entry: %+v", m)
	}
	if m.Enabled == nil || *m.Enabled != false {
		t.Fatalf("expected Enabled=false after disable, got %v", m.Enabled)
	}
}

func TestRunModelsDisable_ThenEnable_RoundTrip(t *testing.T) {
	setupModelsAddHome(t)

	disCmd := newTestEnableDisableCommand(&disableProvider, &disableScope)
	disableProvider = "bedrock"
	disableScope = "global"

	if err := runModelsDisable(disCmd, []string{"round.trip.model"}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	enCmd := newTestEnableDisableCommand(&enableProvider, &enableScope)
	enableProvider = "bedrock"
	enableScope = "global"

	if err := runModelsEnable(enCmd, []string{"round.trip.model"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	models := ai.LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Enabled == nil || !*models[0].Enabled {
		t.Fatalf("expected Enabled=true after round-trip, got %v", models[0].Enabled)
	}
}
