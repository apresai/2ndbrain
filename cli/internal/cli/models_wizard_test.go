package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// TestWriteActiveModelConfig_WritesAllSlots drives the wizard's config-write
// step in isolation (no discovery, no live provider) and asserts the chosen
// provider + embedding + generation models land in the vault config. This is
// the load-bearing assertion for `models wizard --set-active`: it proves the
// write reuses the config-set path (provider validation, the dimension resync)
// without depending on any API being reachable.
func TestWriteActiveModelConfig_WritesAllSlots(t *testing.T) {
	v, _ := newContractVault(t)

	// Start from a clean, deliberately-mismatched dimension so the resync is
	// observable: picking the 1024-dim Nova-2 embedder must move dimensions to
	// 1024 via the same resync runConfigSet uses.
	v.Config.AI.Dimensions = 512

	d := ai.DefaultAIConfig() // bedrock + Nova-2 + Haiku 4.5
	events := newWizardEventSink(false, &strings.Builder{}, &strings.Builder{})

	if err := writeActiveModelConfig(v, d.Provider, d.EmbeddingModel, d.GenerationModel, events); err != nil {
		t.Fatalf("writeActiveModelConfig: %v", err)
	}

	if got := v.Config.AI.Provider; got != d.Provider {
		t.Errorf("Provider = %q, want %q", got, d.Provider)
	}
	if got := v.Config.AI.EmbeddingModel; got != d.EmbeddingModel {
		t.Errorf("EmbeddingModel = %q, want %q", got, d.EmbeddingModel)
	}
	if got := v.Config.AI.GenerationModel; got != d.GenerationModel {
		t.Errorf("GenerationModel = %q, want %q", got, d.GenerationModel)
	}
	// Dimension resync must have run (the shared path), not left the stale 512.
	if got := v.Config.AI.Dimensions; got != d.Dimensions {
		t.Errorf("Dimensions = %d, want %d (embedding-model resync did not run)", got, d.Dimensions)
	}

	// The write must be persisted to disk, not just held in memory: reopening
	// the config from the dot dir must show the same models.
	reloaded, err := vault.LoadConfig(v.DotDir)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.AI.EmbeddingModel != d.EmbeddingModel || reloaded.AI.GenerationModel != d.GenerationModel {
		t.Errorf("persisted config = (%q, %q), want (%q, %q)",
			reloaded.AI.EmbeddingModel, reloaded.AI.GenerationModel, d.EmbeddingModel, d.GenerationModel)
	}
}

// TestWriteActiveModelConfig_RejectsUnknownProvider proves the write reuses
// setConfigValue's provider validation: a bogus provider name is refused, the
// same way `config set ai.provider <typo>` is.
func TestWriteActiveModelConfig_RejectsUnknownProvider(t *testing.T) {
	v, _ := newContractVault(t)
	events := newWizardEventSink(false, &strings.Builder{}, &strings.Builder{})
	err := writeActiveModelConfig(v, "bedrok", "some-embed", "some-gen", events)
	if err == nil {
		t.Fatal("writeActiveModelConfig with unknown provider = nil, want error")
	}
	if !strings.Contains(err.Error(), "ai.provider") {
		t.Errorf("error = %q, want it to mention ai.provider", err.Error())
	}
}

// TestShouldSetActive covers the gating logic: nothing selected → no write;
// the --set-active flag forces a write; a non-interactive run without the flag
// stays hands-off.
func TestShouldSetActive(t *testing.T) {
	// JSON-mode sink is non-interactive.
	jsonSink := newWizardEventSink(true, &strings.Builder{}, &strings.Builder{})

	prev := wizardSetActive
	t.Cleanup(func() { wizardSetActive = prev })

	wizardSetActive = false
	if shouldSetActive(jsonSink, "", "") {
		t.Error("shouldSetActive with no models selected = true, want false")
	}
	if shouldSetActive(jsonSink, "embed", "gen") {
		t.Error("shouldSetActive non-interactive without --set-active = true, want false")
	}

	wizardSetActive = true
	if !shouldSetActive(jsonSink, "embed", "") {
		t.Error("shouldSetActive with --set-active and an embedding model = false, want true")
	}
	if shouldSetActive(jsonSink, "", "") {
		t.Error("shouldSetActive with --set-active but no models = true, want false (nothing to set)")
	}
}

// TestModelsWizardSetActive_EndToEnd runs the full `models wizard --set-active`
// CLI path against a real provider and asserts the config keys were written.
// It is gated on Bedrock being reachable (the default provider) and skips when
// no credentials are configured, per the no-mock policy.
func TestModelsWizardSetActive_EndToEnd(t *testing.T) {
	if !ai.CheckBedrockCredentials(context.Background(), ai.DefaultAIConfig().Bedrock) {
		t.Skip("no Bedrock credentials configured; skipping live wizard test")
	}

	v, root := newContractVault(t)
	// Wipe the active models so the assertion proves the wizard set them, not
	// that they were already the defaults.
	v.Config.AI.EmbeddingModel = ""
	v.Config.AI.GenerationModel = ""
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save cleared config: %v", err)
	}

	// --json runs non-interactively with easy-mode defaults; --skip-discover
	// keeps it to the builtin catalog so it stays fast and offline-stable.
	if _, err := runCLIArgs(t, root, "models", "wizard", "--json", "--skip-discover", "--set-active"); err != nil {
		t.Fatalf("models wizard --set-active: %v", err)
	}

	reloaded, err := vault.LoadConfig(v.DotDir)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.AI.EmbeddingModel == "" && reloaded.AI.GenerationModel == "" {
		t.Error("models wizard --set-active wrote neither an embedding nor a generation model")
	}
}
