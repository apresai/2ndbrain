package cli

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/llama"
	"github.com/spf13/cobra"
)

var (
	setupProvider        string
	setupEmbeddingModel  string
	setupGenerationModel string
)

var aiSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Guided AI provider setup wizard",
	Long:  "Interactive wizard to configure Bedrock, OpenRouter, or Ollama. Validates credentials, helps pick models, and tests connectivity. Use flags to skip prompts.",
	Example: `  2nb ai setup                                      # interactive
  2nb ai setup --provider ollama                    # skip provider prompt
  2nb ai setup --provider bedrock --embedding-model amazon.titan-embed-text-v2:0`,
	RunE: runAISetup,
}

func init() {
	aiSetupCmd.Flags().StringVar(&setupProvider, "provider", "", "AI provider: bedrock, openrouter, ollama")
	aiSetupCmd.Flags().StringVar(&setupEmbeddingModel, "embedding-model", "", "Embedding model ID")
	aiSetupCmd.Flags().StringVar(&setupGenerationModel, "generation-model", "", "Generation model ID")
	_ = aiSetupCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = aiSetupCmd.RegisterFlagCompletionFunc("embedding-model", completeModelIDs)
	_ = aiSetupCmd.RegisterFlagCompletionFunc("generation-model", completeModelIDs)
	aiCmd.AddCommand(aiSetupCmd)
}

func runAISetup(cmd *cobra.Command, args []string) error {
	scanner := bufio.NewScanner(os.Stdin)

	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	// Mirror runConfigSet: route slog to .2ndbrain/logs/cli.log so the
	// active-model writes this wizard makes leave the same durable trail a
	// terminal `config set` does (without --verbose, only the file logger sees
	// them).
	setupFileLogging(v)

	cfg := v.Config.AI
	ctx := context.Background()

	// Step 1: Pick provider.
	provider := setupProvider
	if provider == "" {
		fmt.Println("Select AI provider:")
		fmt.Println("  1) bedrock     — AWS Bedrock: Claude Haiku 4.5 + Nova embeddings (default, recommended)")
		fmt.Println("  2) openrouter  — OpenRouter API (opt-in; many models, pay-per-token)")
		fmt.Println("  3) ollama      — Local models via Ollama (opt-in; free + private, requires install)")
		fmt.Println("  4) llama-local — Bundled Gemma models via llama.cpp (opt-in; fully offline, no API key; downloads ~3-8 GB)")
		choice := promptChoice(scanner, "Choice", 4)
		provider = []string{"bedrock", "openrouter", "ollama", "llama-local"}[choice-1]
	}

	switch provider {
	case "bedrock", "openrouter", "ollama", "llama-local":
	default:
		return fmt.Errorf("invalid provider %q (use: bedrock, openrouter, ollama, llama-local)", provider)
	}
	cfg.Provider = provider
	// Enable the chosen provider: the opt-in providers ship disabled, so
	// selecting one here makes its models visible in selection UIs.
	switch provider {
	case "bedrock":
		cfg.Bedrock.Disabled = false
	case "openrouter":
		cfg.OpenRouter.Disabled = false
	case "ollama":
		cfg.Ollama.Disabled = false
	case "llama-local":
		cfg.Llama.Disabled = false
	}
	fmt.Printf("\nProvider: %s\n", provider)

	// Step 2: Validate credentials.
	switch provider {
	case "bedrock":
		if err := setupBedrock(ctx, scanner, &cfg); err != nil {
			return err
		}
	case "openrouter":
		if err := setupOpenRouter(scanner); err != nil {
			return err
		}
	case "ollama":
		if err := setupOllama(ctx, &cfg); err != nil {
			return err
		}
	case "llama-local":
		fmt.Println("No API key needed — models run locally via the bundled llama.cpp engine (2nb ai engine).")
	}

	verifiedModels, err := loadVerifiedModelCatalog(ctx, cfg, v.Root)
	if err != nil {
		return err
	}

	// Step 3: Pick models — easy mode or custom.
	var embedID, genID string
	var dims int

	flagsProvided := setupEmbeddingModel != "" || setupGenerationModel != ""

	if !flagsProvided {
		defEmbed, defGen, defDims := easyModeDefaults(provider)
		fmt.Println("\nSetup mode:")
		fmt.Printf("  1) Easy   — use recommended defaults (%s + %s)\n", defEmbed, defGen)
		fmt.Println("  2) Custom — pick your own models from the catalog")
		choice := promptChoice(scanner, "Choice", 2)

		if choice == 1 {
			embedID, genID, dims = defEmbed, defGen, defDims
			fmt.Printf("\n  Embedding:  %s (%dd)\n", embedID, dims)
			fmt.Printf("  Generation: %s\n", genID)

			if provider == "ollama" {
				ollamaPullIfNeeded(scanner, embedID)
				ollamaPullIfNeeded(scanner, genID)
			}
		}
	}

	// Custom mode or flag overrides.
	if embedID == "" {
		if setupEmbeddingModel != "" {
			embedID = setupEmbeddingModel
			m, _ := lookupModelInfo(verifiedModels, provider, embedID)
			dims = m.Dimensions
			fmt.Printf("\nEmbedding model: %s\n", embedID)
		} else {
			embedID, dims = pickModel(scanner, verifiedModels, provider, "embedding")
		}
	}
	if genID == "" {
		if setupGenerationModel != "" {
			genID = setupGenerationModel
			fmt.Printf("Generation model: %s\n", genID)
		} else {
			genID, _ = pickModel(scanner, verifiedModels, provider, "generation")
		}
	}

	cfg.EmbeddingModel = embedID
	cfg.GenerationModel = genID
	if dims > 0 {
		// Warn if dimensions are changing with existing embeddings.
		if cfg.Dimensions > 0 && cfg.Dimensions != dims {
			embCount, _ := v.DB.EmbeddingCount()
			if embCount > 0 {
				fmt.Printf("\nWarning: changing dimensions from %d to %d will require re-indexing %d documents.\n",
					cfg.Dimensions, dims, embCount)
			}
		}
		cfg.Dimensions = dims
	}

	// For local providers, offer to pull the selected models if not present.
	// This runs after model selection (not just in easy mode), so it covers the
	// easy, custom, and --flag-driven paths uniformly with one prompt each.
	if provider == "ollama" {
		ollamaPullIfNeeded(scanner, embedID)
		ollamaPullIfNeeded(scanner, genID)
	}
	if provider == "llama-local" {
		llamaPullIfNeeded(scanner, embedID)
		llamaPullIfNeeded(scanner, genID)
	}

	// Step 4: Probe models. A passing probe is persisted to the per-vault user
	// catalog (as user_verified) so a model that passed setup is not thrown
	// away: it shows up in `2nb models list` afterward.
	fmt.Printf("\nTesting embedding model %s...\n", embedID)
	embedProbe := probeWithRetry(ctx, scanner, &cfg, verifiedModels, provider, "embedding", &embedID, &dims)
	cfg.EmbeddingModel = embedID
	if dims > 0 {
		cfg.Dimensions = dims
	}
	persistProbe(v.Root, embedProbe)

	fmt.Printf("Testing generation model %s...\n", genID)
	genProbe := probeWithRetry(ctx, scanner, &cfg, verifiedModels, provider, "generation", &genID, &dims)
	cfg.GenerationModel = genID
	persistProbe(v.Root, genProbe)

	// Step 5: Save config.
	v.Config.AI = cfg
	if err := v.Config.Save(v.DotDir); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	// Log the active-model writes with the SAME "config set" message + key/value
	// attrs runConfigSet uses (plus a source attr) so "what changed my active
	// model?" is answerable from cli.log whether the change came via `config set`
	// or this setup wizard. Logged only after Save succeeds so the log never
	// claims a write that didn't persist.
	slog.Info("config set", "key", "ai.provider", "value", cfg.Provider, "source", "ai setup")
	slog.Info("config set", "key", "ai.embedding_model", "value", cfg.EmbeddingModel, "source", "ai setup")
	slog.Info("config set", "key", "ai.generation_model", "value", cfg.GenerationModel, "source", "ai setup")

	fmt.Println("\nConfiguration saved:")
	fmt.Printf("  Provider:         %s\n", cfg.Provider)
	fmt.Printf("  Embedding model:  %s\n", cfg.EmbeddingModel)
	fmt.Printf("  Generation model: %s\n", cfg.GenerationModel)
	fmt.Printf("  Dimensions:       %d\n", cfg.Dimensions)

	// Step 6: Offer to run index.
	if promptYN(scanner, "\nRun 2nb index to generate embeddings now?", true) {
		fmt.Println()
		indexCmd := exec.Command(os.Args[0], "index")
		indexCmd.Stdout = os.Stdout
		indexCmd.Stderr = os.Stderr
		if err := indexCmd.Run(); err != nil {
			return fmt.Errorf("index: %w", err)
		}
	}

	fmt.Println("\nSetup complete!")
	fmt.Println("  Run `2nb ai status` to verify")
	fmt.Println("  Run `2nb ask \"your question\"` to query your vault")
	return nil
}

// --- Provider-specific credential setup ---

func setupBedrock(ctx context.Context, scanner *bufio.Scanner, cfg *ai.AIConfig) error {
	fmt.Println("\nChecking AWS credentials...")

	if ai.CheckBedrockCredentials(ctx, cfg.Bedrock) {
		fmt.Printf("  Found credentials (profile: %s, region: %s)\n", cfg.Bedrock.Profile, cfg.Bedrock.Region)
		if promptYN(scanner, "Use these?", true) {
			printBedrockModelAccessHint(cfg.Bedrock.Region)
			return nil
		}
	}

	// Prompt for profile and region.
	profile := promptLine(scanner, fmt.Sprintf("AWS profile [%s]: ", cfg.Bedrock.Profile))
	if profile != "" {
		cfg.Bedrock.Profile = profile
	}
	region := promptLine(scanner, fmt.Sprintf("AWS region [%s]: ", cfg.Bedrock.Region))
	if region != "" {
		cfg.Bedrock.Region = region
	}

	fmt.Println("  Validating credentials...")
	if !ai.CheckBedrockCredentials(ctx, cfg.Bedrock) {
		return fmt.Errorf("AWS credentials not found for profile=%s region=%s.\n"+
			"  Fix one of:\n"+
			"    • run `aws configure` (or `aws configure --profile %s`)\n"+
			"    • set AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY (and AWS_REGION)\n"+
			"    • set AWS_PROFILE to an existing profile",
			cfg.Bedrock.Profile, cfg.Bedrock.Region, cfg.Bedrock.Profile)
	}
	fmt.Println("  AWS credentials validated.")
	printBedrockModelAccessHint(cfg.Bedrock.Region)
	return nil
}

// printBedrockModelAccessHint reminds the user about the most common Bedrock
// gotcha: credentials can be valid while model access is still locked. If the
// model probe later returns AccessDenied, this is why.
func printBedrockModelAccessHint(region string) {
	fmt.Printf("  Note: Bedrock requires per-model access. If a model test fails with\n"+
		"  \"AccessDenied\", enable Claude + Nova in the AWS console:\n"+
		"    Bedrock → Model access (region: %s)\n", region)
}

func setupOpenRouter(scanner *bufio.Scanner) error {
	fmt.Println("\nChecking OpenRouter API key...")

	if ai.HasAPIKey("openrouter") {
		fmt.Println("  API key found.")
		if promptYN(scanner, "Use existing key?", true) {
			return nil
		}
	}

	fmt.Println("  Get your key from https://openrouter.ai/keys")
	key := promptLine(scanner, "Enter OpenRouter API key: ")
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("no API key provided")
	}
	if err := ai.SetAPIKey("openrouter", key); err != nil {
		return fmt.Errorf("store API key: %w", err)
	}
	fmt.Println("  API key stored in macOS Keychain.")
	return nil
}

func setupOllama(ctx context.Context, cfg *ai.AIConfig) error {
	fmt.Println("\nChecking Ollama...")

	endpoint := cfg.Ollama.Endpoint
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}

	status := ai.CheckOllamaStatus(ctx, endpoint)

	if !status.Installed {
		fmt.Println("  Ollama is not installed.")
		fmt.Println("  Install with: brew install ollama")
		return fmt.Errorf("ollama not installed")
	}
	fmt.Printf("  Installed: %s\n", status.BinaryPath)

	if !status.Running {
		fmt.Println("  Ollama is not running.")
		fmt.Println("  Start with: ollama serve")
		return fmt.Errorf("ollama not running")
	}
	fmt.Printf("  Running at %s\n", endpoint)
	return nil
}

// --- Model selection ---

func pickModel(scanner *bufio.Scanner, catalog []ai.ModelInfo, provider, modelType string) (string, int) {
	var models []ai.ModelInfo
	for _, m := range catalog {
		if m.Provider == provider && m.Type == modelType {
			models = append(models, m)
		}
	}

	if len(models) == 0 {
		fmt.Printf("  No verified %s models for %s. Enter model ID manually.\n", modelType, provider)
		id := promptLine(scanner, "Model ID: ")
		return id, 0
	}

	if len(models) == 1 {
		fmt.Printf("\nUsing %s model: %s\n", modelType, models[0].ID)
		return models[0].ID, models[0].Dimensions
	}

	fmt.Printf("\nSelect %s model:\n", modelType)
	for i, m := range models {
		price := ai.CompactPriceLabel(m)
		extra := ""
		if m.Dimensions > 0 {
			extra = fmt.Sprintf("  %dd", m.Dimensions)
		}
		if m.ContextLen > 0 {
			extra += fmt.Sprintf("  %s", formatContext(m.ContextLen))
		}
		if m.Notes != "" {
			extra += fmt.Sprintf("  (%s)", m.Notes)
		}
		fmt.Printf("  %d) %-45s %-8s%s\n", i+1, m.ID, price, extra)
	}

	choice := promptChoice(scanner, "Choice", len(models))
	selected := models[choice-1]
	return selected.ID, selected.Dimensions
}

func ollamaPullIfNeeded(scanner *bufio.Scanner, modelID string) {
	ctx := context.Background()
	models, err := ai.ListOllamaModels(ctx, http.DefaultClient, "http://localhost:11434")
	if err != nil {
		// Can't check — skip.
		return
	}
	for _, m := range models {
		if m.ID == modelID || strings.HasPrefix(m.ID, modelID+":") {
			return // already pulled
		}
	}

	fmt.Printf("\nModel %s is not pulled.\n", modelID)
	if promptYN(scanner, "Pull now?", true) {
		runOllamaPull(modelID)
	}
}

// llamaPullIfNeeded downloads a bundled local model if it isn't already cached,
// after an opt-in prompt (weights are 100s of MB to GB — never downloaded
// silently, matching re:Gist's on-device flow). Verifies the SHA256 pinned in
// the manifest and shows a progress bar via enginePullProgress.
func llamaPullIfNeeded(scanner *bufio.Scanner, modelID string) {
	art, ok := llama.ArtifactFor(modelID)
	if !ok {
		return // not a manifest model (e.g. a custom id) — nothing to pull
	}
	if llama.ModelStatus(modelID).Present {
		return // already downloaded + on disk
	}
	fmt.Printf("\nModel %s is not downloaded (~%.1f GB).\n", modelID, float64(art.SizeBytes)/1e9)
	if !promptYN(scanner, "Download now?", true) {
		fmt.Printf("Skipped. Download later with: 2nb ai engine pull %s\n", modelID)
		return
	}
	if _, err := llama.EnsureModelProgress(context.Background(), modelID, enginePullProgress(modelID)); err != nil {
		fmt.Fprintf(os.Stderr, "  download failed: %v\n", err)
	}
}

// --- Probe with retry ---

// probeWithRetry tests the model, offering to pick a different one on failure.
// It returns the passing *ai.TestProbeResult so the caller can persist it to
// the user catalog (so a model that passed setup shows as user_verified in
// `2nb models list`). Returns nil when the user opts to continue without a
// passing probe.
func probeWithRetry(ctx context.Context, scanner *bufio.Scanner, cfg *ai.AIConfig, models []ai.ModelInfo, provider, modelType string, modelID *string, dims *int) *ai.TestProbeResult {
	for {
		result, err := ai.TestProbeModel(ctx, *cfg, *modelID, provider, modelType)
		if err == nil && result.OK {
			fmt.Printf("  OK (%s)\n", result.Latency)
			return result
		}
		detail := ""
		if result != nil {
			detail = result.Detail
		} else if err != nil {
			detail = err.Error()
		}
		fmt.Printf("  Failed: %s\n", detail)
		if !promptYN(scanner, "Try a different model?", true) {
			fmt.Println("  Continuing without validation.")
			return nil
		}
		*modelID, *dims = pickModel(scanner, models, provider, modelType)
	}
}

// persistProbe writes a passing probe result to the per-vault user catalog as a
// user_verified entry, so a model that passed `ai setup` is no longer thrown
// away: it shows up in `2nb models list` afterward. This reuses the SAME
// promotion path (promotedEntry + SaveUserCatalogEntry) that `models wizard`
// uses. A nil result (probe skipped/failed) is a no-op; we never persist a
// failed probe. Persistence failures are surfaced as a non-fatal warning since
// the config save is the primary outcome of setup.
func persistProbe(vaultRoot string, result *ai.TestProbeResult) {
	if result == nil || !result.OK {
		return
	}
	base := findBuiltinModel(result.Provider, result.ModelID)
	entry := promotedEntry(base, result)
	entry.InvokeStrategy = ai.ResolveInvokeStrategy(entry.Provider, entry.ID, vaultRoot)
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, vaultRoot, entry); err != nil {
		// Keep the stderr warning (the interactive user needs to see it) and add a
		// durable slog line so the failure is recoverable from cli.log later.
		fmt.Fprintf(os.Stderr, "Warning: could not save %s to the model catalog: %v\n", entry.ID, err)
		slog.Warn("catalog persist failed", "model", entry.ID, "err", err)
	}
}

// --- Input helpers ---

func promptLine(scanner *bufio.Scanner, prompt string) string {
	fmt.Print(prompt)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

func promptChoice(scanner *bufio.Scanner, prompt string, n int) int {
	for {
		fmt.Printf("%s [1-%d]: ", prompt, n)
		if !scanner.Scan() {
			return 1
		}
		text := strings.TrimSpace(scanner.Text())
		choice, err := strconv.Atoi(text)
		if err == nil && choice >= 1 && choice <= n {
			return choice
		}
		fmt.Printf("  Please enter a number between 1 and %d.\n", n)
	}
}

func promptYN(scanner *bufio.Scanner, prompt string, defaultYes bool) bool {
	suffix := " [Y/n]: "
	if !defaultYes {
		suffix = " [y/N]: "
	}
	fmt.Print(prompt + suffix)
	if !scanner.Scan() {
		return defaultYes
	}
	text := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if text == "" {
		return defaultYes
	}
	return text == "y" || text == "yes"
}

func easyModeDefaults(provider string) (embedID, genID string, dims int) {
	switch provider {
	case "bedrock":
		// Single source of truth: the same Bedrock default the vault ships with
		// (Nova-2 embeddings + Claude Haiku 4.5), not a divergent nova-micro.
		d := ai.DefaultAIConfig()
		return d.EmbeddingModel, d.GenerationModel, d.Dimensions
	case "openrouter":
		return "nvidia/llama-nemotron-embed-vl-1b-v2:free", "google/gemma-4-31b-it:free", 1024
	case "ollama":
		return "nomic-embed-text", "qwen2.5:0.5b", 768
	case "llama-local":
		// The bundled stack: EmbeddingGemma 300M + Gemma 4 E2B (the smaller,
		// ~3.1 GB generation model). Users can switch to E4B for higher quality.
		return "embeddinggemma-300m", "gemma4-e2b", 768
	default:
		return "", "", 0
	}
}

func runOllamaPull(model string) error {
	cmd := exec.Command("ollama", "pull", model)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
