package cli

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
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
	RunE:  runAISetup,
}

func init() {
	aiSetupCmd.Flags().StringVar(&setupProvider, "provider", "", "AI provider: bedrock, openrouter, ollama")
	aiSetupCmd.Flags().StringVar(&setupEmbeddingModel, "embedding-model", "", "Embedding model ID")
	aiSetupCmd.Flags().StringVar(&setupGenerationModel, "generation-model", "", "Generation model ID")
	aiCmd.AddCommand(aiSetupCmd)
}

func runAISetup(cmd *cobra.Command, args []string) error {
	scanner := bufio.NewScanner(os.Stdin)

	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	cfg := v.Config.AI
	ctx := context.Background()

	// Step 1: Pick provider.
	provider := setupProvider
	if provider == "" {
		fmt.Println("Select AI provider:")
		fmt.Println("  1) bedrock     — AWS Bedrock (Claude, Nova, Llama — uses AWS credentials)")
		fmt.Println("  2) openrouter  — OpenRouter API (many models, pay-per-token)")
		fmt.Println("  3) ollama      — Local models via Ollama (free, private)")
		choice := promptChoice(scanner, "Choice", 3)
		provider = []string{"bedrock", "openrouter", "ollama"}[choice-1]
	}

	switch provider {
	case "bedrock", "openrouter", "ollama":
	default:
		return fmt.Errorf("invalid provider %q (use: bedrock, openrouter, ollama)", provider)
	}
	cfg.Provider = provider
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
			dims = lookupCatalogDims(provider, embedID)
			fmt.Printf("\nEmbedding model: %s\n", embedID)
		} else {
			embedID, dims = pickModel(scanner, provider, "embedding")
		}
	}
	if genID == "" {
		if setupGenerationModel != "" {
			genID = setupGenerationModel
			fmt.Printf("Generation model: %s\n", genID)
		} else {
			genID, _ = pickModel(scanner, provider, "generation")
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

	// For Ollama, offer to pull models if not already pulled.
	if provider == "ollama" {
		ollamaPullIfNeeded(scanner, embedID)
		ollamaPullIfNeeded(scanner, genID)
	}

	// Step 4: Probe models.
	fmt.Printf("\nTesting embedding model %s...\n", embedID)
	probeWithRetry(ctx, scanner, &cfg, provider, "embedding", &embedID, &dims)
	cfg.EmbeddingModel = embedID
	if dims > 0 {
		cfg.Dimensions = dims
	}

	fmt.Printf("Testing generation model %s...\n", genID)
	probeWithRetry(ctx, scanner, &cfg, provider, "generation", &genID, &dims)
	cfg.GenerationModel = genID

	// Step 5: Save config.
	v.Config.AI = cfg
	if err := v.Config.Save(v.DotDir); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

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
		return fmt.Errorf("AWS credentials not valid for profile=%s region=%s\n  Run `aws configure` or set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY",
			cfg.Bedrock.Profile, cfg.Bedrock.Region)
	}
	fmt.Println("  AWS credentials validated.")
	return nil
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

func pickModel(scanner *bufio.Scanner, provider, modelType string) (string, int) {
	catalog := ai.BuiltinCatalog()
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
		price := "free"
		if m.PriceIn > 0 || m.PriceOut > 0 {
			price = fmt.Sprintf("$%.2f/$%.2f", m.PriceIn, m.PriceOut)
		}
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

func lookupCatalogDims(provider, modelID string) int {
	for _, m := range ai.BuiltinCatalog() {
		if m.Provider == provider && m.ID == modelID {
			return m.Dimensions
		}
	}
	return 0
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

// --- Probe with retry ---

func probeWithRetry(ctx context.Context, scanner *bufio.Scanner, cfg *ai.AIConfig, provider, modelType string, modelID *string, dims *int) {
	for {
		result, err := ai.TestProbeModel(ctx, *cfg, *modelID, provider, modelType)
		if err == nil && result.OK {
			fmt.Printf("  OK (%s)\n", result.Latency)
			return
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
			return
		}
		*modelID, *dims = pickModel(scanner, provider, modelType)
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
		return "amazon.nova-2-multimodal-embeddings-v1:0", "amazon.nova-micro-v1:0", 1024
	case "openrouter":
		return "nvidia/llama-nemotron-embed-vl-1b-v2:free", "google/gemma-4-31b-it:free", 1024
	case "ollama":
		return "nomic-embed-text", "qwen2.5:0.5b", 768
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
