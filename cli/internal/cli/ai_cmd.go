package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
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

	SimilarityThreshold       float64                    `json:"similarity_threshold"`
	SimilarityThresholdSource ai.ResolvedThresholdSource `json:"similarity_threshold_source"`

	// Portability fields — observed DB state (source of truth) plus
	// a derived status that callers can render without re-implementing
	// the decision table. These are the vault's self-describing answer
	// to "is this vault portable, and does it need reindexing?".
	VaultEmbeddingModels []string `json:"vault_embedding_models"` // DISTINCT embedding_model in DB
	VaultEmbeddingDim    int      `json:"vault_embedding_dim"`    // sampled BLOB length / 4
	VaultTotalDocs       int      `json:"vault_total_docs"`
	VaultEmbeddedDocs    int      `json:"vault_embedded_docs"`
	VaultEmbeddableDocs  int      `json:"vault_embeddable_docs"` // docs with content (embedded + awaiting); excludes empty notes. Status denominator.
	VaultEmptyDocs       int      `json:"vault_empty_docs"`      // empty/whitespace-only notes the embed pass skips (no chunk)
	PortabilityStatus    string   `json:"portability_status"`    // see derivePortability
	PortabilityAction    string   `json:"portability_action"`    // one-line fix hint

	// Providers surfaces per-provider readiness for the GUI's AI Hub
	// and the `ai status` pretty output. Each entry reflects configured
	// credentials, user disable flag, and a cheap reachability probe.
	Providers []ProviderStatus `json:"providers,omitempty"`
}

// ProviderStatus is the GUI-facing shape of a single provider's health.
type ProviderStatus struct {
	Name          string `json:"name"`             // bedrock | openrouter | ollama
	ConfigPresent bool   `json:"config_present"`   // creds / endpoint configured
	Disabled      bool   `json:"disabled"`         // user explicitly silenced via ai.<provider>.disabled
	Reachable     bool   `json:"reachable"`        // cheap probe succeeded
	Reason        string `json:"reason,omitempty"` // human-readable "why not ready" when relevant
	Detail        string `json:"detail,omitempty"` // endpoint / region / env var name for UX
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

	threshold, thresholdSource := cfg.ResolveSimilarityThresholdFull(v.Root)
	status := AIStatus{
		Provider:                  cfg.Provider,
		EmbeddingModel:            cfg.EmbeddingModel,
		GenModel:                  cfg.GenerationModel,
		Dimensions:                cfg.Dimensions,
		SimilarityThreshold:       threshold,
		SimilarityThresholdSource: thresholdSource,
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
	totalDocs, embeddedDocs, embeddableUnembedded, errCounts := v.DB.EmbeddingCounts()
	if errCounts != nil {
		slog.Warn("embedding counts query failed", "err", errCounts)
	}
	status.VaultTotalDocs = totalDocs
	status.VaultEmbeddedDocs = embeddedDocs
	// Embeddable = docs with real content (embedded + still-awaiting). Empty
	// notes (no chunk) are excluded so status surfaces show coverage over what
	// can actually be embedded, not a misleading gap against blank notes.
	status.VaultEmbeddableDocs = embeddedDocs + embeddableUnembedded
	status.VaultEmptyDocs = totalDocs - embeddedDocs - embeddableUnembedded
	status.VaultEmbeddingDim, _ = v.DB.SampleEmbeddingDim()
	status.VaultEmbeddingModels, _ = v.DB.DistinctEmbeddingModels()

	embedder, _ := ai.DefaultRegistry.Embedder(cfg.Provider)
	status.PortabilityStatus, status.PortabilityAction = derivePortability(
		ctx, cfg, embedder,
		status.VaultEmbeddingDim, status.VaultEmbeddingModels, totalDocs, embeddedDocs, embeddableUnembedded,
		vault.CheckIndexFreshness(v.DB),
	)

	status.Providers = collectProviderStatus(ctx, cfg)

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, status)
	}

	models, err := loadVerifiedModelCatalog(ctx, cfg, v.Root)
	if err != nil {
		return err
	}
	embedModel, _ := lookupModelInfo(models, cfg.Provider, cfg.EmbeddingModel)
	genModel, _ := lookupModelInfo(models, cfg.Provider, cfg.GenerationModel)

	// Pretty output
	fmt.Printf("Provider:         %s\n", status.Provider)
	fmt.Printf("Embedding model:  %s\n", status.EmbeddingModel)
	fmt.Printf("  pricing:        %s\n", ai.VerbosePriceLabel(embedModel))
	fmt.Printf("Generation model: %s\n", status.GenModel)
	fmt.Printf("  pricing:        %s\n", ai.VerbosePriceLabel(genModel))
	fmt.Printf("Dimensions:       %d\n", status.Dimensions)
	fmt.Printf("Embed ready:      %v\n", status.EmbedAvailable)
	fmt.Printf("Generation ready: %v\n", status.GenAvailable)
	fmt.Printf("Documents:        %d\n", status.DocumentCount)
	// Denominator is embeddable docs (content-bearing); empty notes are hidden
	// so coverage reads cleanly against what can actually be embedded.
	fmt.Printf("Embeddings:       %d/%d\n", status.EmbeddingCount, status.VaultEmbeddableDocs)
	fmt.Printf("Search threshold: %g (%s)\n", status.SimilarityThreshold, status.SimilarityThresholdSource)
	// Loud degradation: an asymmetric model (Nova) embeds queries
	// GENERIC_RETRIEVAL, which collapses the cosine scale (true matches land
	// ~0.25-0.45). A threshold above ~0.45 is almost certainly carried over from
	// the old symmetric embedding and will silently reject every semantic match,
	// degrading search to BM25-only. Flag it with the one-line fix.
	if ai.IsAsymmetricEmbeddingModel(status.EmbeddingModel) && status.SimilarityThreshold > 0.45 {
		fmt.Printf("  ⚠ threshold %g looks calibrated for the old symmetric embedding; semantic search\n"+
			"    will reject real matches. Fix: `2nb config set ai.similarity_threshold 0.25` (or clear\n"+
			"    the saved calibration so the built-in ~0.25 applies).\n", status.SimilarityThreshold)
	}

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
		fmt.Printf("  As-embedded:    %s (%dd), %d of %d docs\n", modelStr, status.VaultEmbeddingDim, status.VaultEmbeddedDocs, status.VaultEmbeddableDocs)
	} else {
		fmt.Printf("  As-embedded:    (no embeddings yet), %d docs\n", status.VaultEmbeddableDocs)
	}
	fmt.Printf("  Current cfg:    %s / %s (%dd)\n", cfg.Provider, cfg.EmbeddingModel, cfg.Dimensions)
	fmt.Printf("  Status:         %s\n", strings.ToUpper(strings.ReplaceAll(status.PortabilityStatus, "_", " ")))
	if status.PortabilityAction != "" {
		fmt.Printf("  Action:         %s\n", status.PortabilityAction)
	}

	// Provider snapshot — useful to see which are enabled / reachable at a glance.
	if len(status.Providers) > 0 {
		fmt.Println()
		fmt.Println("Providers:")
		for _, p := range status.Providers {
			state := "ready"
			switch {
			case p.Disabled:
				state = "disabled"
			case !p.ConfigPresent:
				state = "not configured"
			case !p.Reachable:
				state = "unreachable"
			}
			detail := ""
			if p.Detail != "" {
				detail = "  " + p.Detail
			}
			reason := ""
			if p.Reason != "" {
				reason = " — " + p.Reason
			}
			fmt.Printf("  %-11s %s%s%s\n", p.Name, state, detail, reason)
		}
	}

	// Suggest local alternative for paid providers
	if cfg.Provider != "ollama" && ai.HasKnownPricing(embedModel) && !ai.IsExplicitlyFree(embedModel) {
		fmt.Fprintf(os.Stderr, "\nTip: run `2nb ai setup` for free local AI with Ollama\n")
	}

	return nil
}

// derivePortability inspects current config vs. observed DB state and
// returns a status label plus a one-line action hint. The labels are the
// stable public contract — Swift and automation consumers switch on
// these strings, so renames are a breaking change.
func derivePortability(ctx context.Context, cfg ai.AIConfig, embedder ai.EmbeddingProvider, vaultDim int, vaultModels []string, totalDocs, embeddedDocs, embeddableUnembedded int, freshness vault.IndexFreshness) (status, action string) {
	// Empty notes (no chunk) can never be embedded, so they're excluded from
	// every decision here and from the counts callers display (the embeddable
	// total = embeddedDocs+embeddableUnembedded). A vault whose only gap is
	// empty notes therefore reads clean, with no "stale" and no skipped-note
	// caveat. See DB.EmbeddingCounts.
	if totalDocs == 0 {
		return "empty_vault", "Create documents and run `2nb index` to build the search index."
	}
	if embeddedDocs == 0 {
		// No AI provider configured yet → keep the onboarding nudge, even when
		// the only docs so far are empty notes (saying "ok / fully embedded"
		// would be misleading when semantic search isn't set up at all).
		if cfg.Provider == "" {
			return "no_provider", "Run `2nb ai setup` to enable semantic search. Keyword search works today."
		}
		// Provider is set but nothing is embedded. If every doc is an empty
		// note there is nothing to embed — report healthy rather than sending
		// the user to `2nb index`, which would skip them again. Otherwise the
		// vault genuinely needs indexing.
		if embeddableUnembedded == 0 {
			return "ok", ""
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
	// A newer 2nb changed chunking/embedding LOGIC at the same model+dimension —
	// undetectable above (model/dim match), but the stored vectors are stale. A
	// re-embed also re-chunks + catches up unembedded docs, so it takes precedence
	// over the mild "stale" catch-up below.
	if freshness.ReembedRecommended {
		return "upgrade_reembed_recommended", "A newer 2nb improved chunking/embeddings for this vault. Run `2nb index --force-reembed` to apply it."
	}
	if embeddableUnembedded > 0 {
		// Only count docs that *can* be embedded; empty notes are excluded so
		// the denominator and the "catch up" advice are both actionable.
		embeddable := embeddedDocs + embeddableUnembedded
		return "stale", fmt.Sprintf("%d of %d docs are embedded. Run `2nb index` to catch up.", embeddedDocs, embeddable)
	}
	// A newer 2nb changed index-only logic (FTS/link/tag extraction). Milder than a
	// re-embed and fixed by a plain reindex; surfaced after the embed-coverage
	// catch-up above, before a clean OK.
	if freshness.ReindexRecommended {
		return "upgrade_reindex_recommended", "A newer 2nb improved indexing for this vault. Run `2nb index` to apply it."
	}
	// All content-bearing docs are embedded. Any remaining docs are empty notes,
	// which are hidden from the status counts, so this reads as a clean OK.
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

// collectProviderStatus returns a per-provider readiness snapshot for
// Bedrock, OpenRouter, and Ollama. Used by `ai status --json` and the
// GUI's AI Hub. The reachability probe is cheap — credentials check
// plus an endpoint ping for Ollama; no request is sent to the Bedrock
// or OpenRouter APIs here.
func collectProviderStatus(ctx context.Context, cfg ai.AIConfig) []ProviderStatus {
	out := []ProviderStatus{
		bedrockProviderStatus(ctx, cfg),
		openrouterProviderStatus(cfg),
		ollamaProviderStatus(ctx, cfg),
	}
	return out
}

func bedrockProviderStatus(ctx context.Context, cfg ai.AIConfig) ProviderStatus {
	s := ProviderStatus{
		Name:          "bedrock",
		Disabled:      cfg.Bedrock.Disabled,
		ConfigPresent: cfg.Bedrock.Region != "",
		Detail:        cfg.Bedrock.Region,
	}
	if s.Disabled {
		s.Reason = "disabled in vault config"
		return s
	}
	if !s.ConfigPresent {
		s.Reason = "region not set (ai.bedrock.region)"
		return s
	}
	// Reachability: the embedder probe covers "creds work + AWS API
	// reachable" in one cheap call (HeadObject-equivalent under the hood).
	if emb, err := ai.DefaultRegistry.Embedder("bedrock"); err == nil {
		s.Reachable = emb.Available(ctx)
		if !s.Reachable {
			s.Reason = "credentials missing or region unreachable"
		}
	} else {
		s.Reason = "bedrock not registered (check ai.provider setup)"
	}
	return s
}

func openrouterProviderStatus(cfg ai.AIConfig) ProviderStatus {
	s := ProviderStatus{
		Name:          "openrouter",
		Disabled:      cfg.OpenRouter.Disabled,
		ConfigPresent: ai.HasAPIKey("openrouter"),
		Detail:        cfg.OpenRouter.APIKeyEnv,
	}
	if s.Disabled {
		s.Reason = "disabled in vault config"
		return s
	}
	if !s.ConfigPresent {
		s.Reason = "no API key (set $OPENROUTER_API_KEY or run `2nb config set-key openrouter`)"
		return s
	}
	// Having a key implies we can hit the API; we don't ping live here
	// to keep `ai status` fast. The Hub's test action will surface real
	// network issues when the user runs a probe.
	s.Reachable = true
	return s
}

func ollamaProviderStatus(ctx context.Context, cfg ai.AIConfig) ProviderStatus {
	endpoint := cfg.Ollama.Endpoint
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	s := ProviderStatus{
		Name:          "ollama",
		Disabled:      cfg.Ollama.Disabled,
		ConfigPresent: true, // endpoint always has a default
		Detail:        endpoint,
	}
	if s.Disabled {
		s.Reason = "disabled in vault config"
		return s
	}
	// Cheap HTTP probe regardless of provider registration — users want
	// to see "Ollama is running" even if Bedrock is the currently-active
	// provider, so we don't gate on DefaultRegistry.
	reqCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "GET", endpoint+"/api/tags", nil)
	if err == nil {
		resp, reqErr := http.DefaultClient.Do(req)
		if reqErr == nil {
			resp.Body.Close()
			s.Reachable = resp.StatusCode < 500
		}
	}
	if !s.Reachable {
		s.Reason = "not running on " + endpoint
	}
	return s
}
