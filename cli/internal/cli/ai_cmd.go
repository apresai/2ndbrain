package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var aiCmd = &cobra.Command{
	Use:   "ai",
	Short: "AI provider management",
	// Default action when invoked without a subcommand: show status.
	RunE: runAIStatus,
}

var aiStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current AI provider status",
	RunE:  runAIStatus,
}

var aiEmbedCmd = &cobra.Command{
	Use:   "embed <text>",
	Short: "Generate embedding for text (debug/testing)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runAIEmbed,
}

func init() {
	aiCmd.AddCommand(aiStatusCmd)
	aiCmd.AddCommand(aiEmbedCmd)
	aiCmd.GroupID = "ai"
	rootCmd.AddCommand(aiCmd)
}

type AIStatus struct {
	Provider       string `json:"provider"`
	EmbeddingModel string `json:"embedding_model"`
	GenModel       string `json:"generation_model"`
	Dimensions     int    `json:"dimensions"`
	EmbedAvailable bool   `json:"embed_available"`
	GenAvailable   bool   `json:"gen_available"`
	EmbeddingCount int    `json:"embedding_count"`
	DocumentCount  int    `json:"document_count"`

	// Portability fields — observed DB state (source of truth) plus
	// a derived status that callers can render without re-implementing
	// the decision table. These are the vault's self-describing answer
	// to "is this vault portable, and does it need reindexing?".
	VaultEmbeddingModels []string `json:"vault_embedding_models"` // DISTINCT embedding_model in DB
	VaultEmbeddingDim    int      `json:"vault_embedding_dim"`    // sampled BLOB length / 4
	VaultTotalDocs       int      `json:"vault_total_docs"`
	VaultEmbeddedDocs    int      `json:"vault_embedded_docs"`
	PortabilityStatus    string   `json:"portability_status"` // see derivePortability
	PortabilityAction    string   `json:"portability_action"` // one-line fix hint
}

func runAIStatus(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	status := AIStatus{
		Provider:       cfg.Provider,
		EmbeddingModel: cfg.EmbeddingModel,
		GenModel:       cfg.GenerationModel,
		Dimensions:     cfg.Dimensions,
	}

	// Check provider availability
	if emb, err := ai.DefaultRegistry.Embedder(cfg.Provider); err == nil {
		status.EmbedAvailable = emb.Available(ctx)
	}
	if gen, err := ai.DefaultRegistry.Generator(cfg.Provider); err == nil {
		status.GenAvailable = gen.Available(ctx)
	}

	// Count embeddings and documents
	status.EmbeddingCount, _ = v.DB.EmbeddingCount()
	var docCount int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)
	status.DocumentCount = docCount

	// Portability state: observe the DB directly so status always
	// reflects reality (not drift-prone config cache).
	totalDocs, embeddedDocs, _ := v.DB.EmbeddingCounts()
	status.VaultTotalDocs = totalDocs
	status.VaultEmbeddedDocs = embeddedDocs
	status.VaultEmbeddingDim, _ = v.DB.SampleEmbeddingDim()
	status.VaultEmbeddingModels, _ = v.DB.DistinctEmbeddingModels()

	embedder, _ := ai.DefaultRegistry.Embedder(cfg.Provider)
	status.PortabilityStatus, status.PortabilityAction = derivePortability(
		ctx, cfg, embedder,
		status.VaultEmbeddingDim, status.VaultEmbeddingModels, totalDocs, embeddedDocs,
	)

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, status)
	}

	// Look up pricing from the catalog (source of truth for metadata).
	var embedPrice, genPriceIn, genPriceOut float64
	for _, m := range ai.BuiltinCatalog() {
		if m.Provider == cfg.Provider && m.ID == cfg.EmbeddingModel {
			embedPrice = m.PriceIn
		}
		if m.Provider == cfg.Provider && m.ID == cfg.GenerationModel {
			genPriceIn = m.PriceIn
			genPriceOut = m.PriceOut
		}
	}

	// Pretty output
	fmt.Printf("Provider:         %s\n", status.Provider)
	fmt.Printf("Embedding model:  %s\n", status.EmbeddingModel)
	if embedPrice == 0 {
		fmt.Printf("  pricing:        free\n")
	} else {
		fmt.Printf("  pricing:        $%.2f per 1M tokens\n", embedPrice)
	}
	fmt.Printf("Generation model: %s\n", status.GenModel)
	if genPriceIn == 0 && genPriceOut == 0 {
		fmt.Printf("  pricing:        free\n")
	} else {
		fmt.Printf("  pricing:        $%.2f in / $%.2f out per 1M tokens\n", genPriceIn, genPriceOut)
	}
	fmt.Printf("Dimensions:       %d\n", status.Dimensions)
	fmt.Printf("Embed ready:      %v\n", status.EmbedAvailable)
	fmt.Printf("Generation ready: %v\n", status.GenAvailable)
	fmt.Printf("Documents:        %d\n", status.DocumentCount)
	fmt.Printf("Embeddings:       %d/%d\n", status.EmbeddingCount, status.DocumentCount)

	// Vault embedding state (portability) — the authoritative "is this
	// vault ready to use here?" section. Always shown so the user never
	// has to hunt through debug logs to find out why search degraded.
	fmt.Println()
	fmt.Println("Vault Embedding State:")
	if status.VaultEmbeddingDim > 0 {
		modelStr := "(no model recorded)"
		if len(status.VaultEmbeddingModels) == 1 {
			modelStr = status.VaultEmbeddingModels[0]
		} else if len(status.VaultEmbeddingModels) > 1 {
			modelStr = fmt.Sprintf("mixed: %s", strings.Join(status.VaultEmbeddingModels, ", "))
		}
		fmt.Printf("  As-embedded:    %s (%dd), %d of %d docs\n", modelStr, status.VaultEmbeddingDim, status.VaultEmbeddedDocs, status.VaultTotalDocs)
	} else {
		fmt.Printf("  As-embedded:    (no embeddings yet), %d docs\n", status.VaultTotalDocs)
	}
	fmt.Printf("  Current cfg:    %s / %s (%dd)\n", cfg.Provider, cfg.EmbeddingModel, cfg.Dimensions)
	fmt.Printf("  Status:         %s\n", strings.ToUpper(strings.ReplaceAll(status.PortabilityStatus, "_", " ")))
	if status.PortabilityAction != "" {
		fmt.Printf("  Action:         %s\n", status.PortabilityAction)
	}

	// Suggest local alternative for paid providers
	if cfg.Provider != "ollama" && embedPrice > 0 {
		fmt.Fprintf(os.Stderr, "\nTip: run `2nb ai setup` for free local AI with Ollama\n")
	}

	return nil
}

// derivePortability inspects current config vs. observed DB state and
// returns a status label plus a one-line action hint. The labels are the
// stable public contract — Swift and automation consumers switch on
// these strings, so renames are a breaking change.
func derivePortability(ctx context.Context, cfg ai.AIConfig, embedder ai.EmbeddingProvider, vaultDim int, vaultModels []string, totalDocs, embeddedDocs int) (status, action string) {
	if totalDocs == 0 {
		return "empty_vault", "Create documents and run `2nb index` to build the search index."
	}
	if embeddedDocs == 0 {
		if cfg.Provider == "" {
			return "no_provider", "Run `2nb ai setup` to enable semantic search. Keyword search works today."
		}
		return "unindexed", "Run `2nb index` to generate embeddings. Keyword search works today."
	}
	if cfg.Provider == "" {
		return "no_provider", "Run `2nb ai setup` to enable semantic search."
	}
	if embedder == nil {
		return "no_provider", fmt.Sprintf("Provider %q is configured but not registered. Run `2nb ai setup` to repair.", cfg.Provider)
	}
	if !embedder.Available(ctx) {
		return "provider_unavailable", fmt.Sprintf("Provider %q is unreachable. If using Ollama, start the daemon; if using Bedrock, check AWS credentials.", cfg.Provider)
	}
	providerDim := embedder.Dimensions()
	if providerDim != vaultDim {
		return "dimension_break", fmt.Sprintf("Vault was embedded with %dd vectors but current provider produces %dd. Run `2nb index --force-reembed` or switch provider back to the one that built this vault.", vaultDim, providerDim)
	}
	if len(vaultModels) > 1 {
		return "mixed", "Vault contains embeddings from multiple models. Run `2nb index --force-reembed` to normalize on the currently configured model."
	}
	if len(vaultModels) == 1 && vaultModels[0] != cfg.EmbeddingModel {
		return "model_mismatch", fmt.Sprintf("Vault was embedded with %q but config is %q (same dim, still usable). Run `2nb index` to re-embed on the next content change, or `--force-reembed` to refresh now.", vaultModels[0], cfg.EmbeddingModel)
	}
	if embeddedDocs < totalDocs {
		return "stale", fmt.Sprintf("%d of %d docs are embedded. Run `2nb index` to catch up.", embeddedDocs, totalDocs)
	}
	return "ok", ""
}

func runAIEmbed(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil {
		return fmt.Errorf("no embedding provider: %w", err)
	}

	text := strings.Join(args, " ")
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Embedding %d chars via %s...\n", len(text), cfg.Provider)
	}

	vecs, err := embedder.Embed(ctx, []string{text})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, vecs[0])
}
