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
