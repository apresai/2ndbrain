package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	modelsTypeFilt    string
	modelsFreeOnly    bool
	modelsDiscover    bool
	modelsCheckStatus bool
	modelsProvider    string
)

var (
	testProvider  string
	testModelType string
)

var (
	addProvider    string
	addType        string
	addName        string
	addDimensions  int
	addContextLen  int
	addPriceIn     float64
	addPriceOut    float64
	addThreshold   float64
	addNotes       string
	addScope       string

	removeProvider string
	removeScope    string
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Manage AI models",
	// Default action when invoked without a subcommand: list the catalog.
	RunE: runModelsList,
}

var modelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available models from all configured providers",
	RunE:  runModelsList,
}

var modelsTestCmd = &cobra.Command{
	Use:               "test <model-id>",
	Short:             "Test if a model works with 2nb",
	Long:              "Sends a quick probe (embed or generate) to verify a model is callable. Useful for testing unverified models before switching.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsTest,
}

var modelsAddCmd = &cobra.Command{
	Use:   "add <model-id>",
	Short: "Add a model to your personal catalog",
	Long: `Adds a model to the user catalog at ~/.config/2nb/models.yaml (global)
or <vault>/.2ndbrain/models.yaml (vault). Subsequent calls to 2nb models list
will include the entry alongside the built-in verified catalog. Use this to
add models 2nb doesn't ship yet without editing source.`,
	Args: cobra.ExactArgs(1),
	RunE: runModelsAdd,
}

var modelsRemoveCmd = &cobra.Command{
	Use:               "remove <model-id>",
	Short:             "Remove a model from your personal catalog",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsRemove,
}

func init() {
	modelsListCmd.Flags().StringVar(&modelsTypeFilt, "type", "", "Filter by type: embed or generation")
	modelsListCmd.Flags().BoolVar(&modelsFreeOnly, "free", false, "Show only free models")
	modelsListCmd.Flags().BoolVar(&modelsDiscover, "discover", false, "Query vendor APIs for full model catalogs")
	modelsListCmd.Flags().BoolVar(&modelsCheckStatus, "status", false, "Probe provider reachability and credentials")
	modelsListCmd.Flags().StringVar(&modelsProvider, "provider", "", "Filter by provider: bedrock, openrouter, ollama")
	_ = modelsListCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsListCmd.RegisterFlagCompletionFunc("type", completeModelTypes)

	modelsTestCmd.Flags().StringVar(&testProvider, "provider", "", "Provider: bedrock, openrouter, ollama (auto-detected if omitted)")
	modelsTestCmd.Flags().StringVar(&testModelType, "type", "", "Model type: embedding or generation (auto-detected if omitted)")
	_ = modelsTestCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsTestCmd.RegisterFlagCompletionFunc("type", completeModelTypes)

	modelsAddCmd.Flags().StringVar(&addProvider, "provider", "", "Provider: bedrock, openrouter, ollama (required)")
	modelsAddCmd.Flags().StringVar(&addType, "type", "", "Type: embedding or generation (required)")
	modelsAddCmd.Flags().StringVar(&addName, "name", "", "Human-readable name")
	modelsAddCmd.Flags().IntVar(&addDimensions, "dimensions", 0, "Embedding dimensions (embedding models only)")
	modelsAddCmd.Flags().IntVar(&addContextLen, "context-length", 0, "Max context length in tokens")
	modelsAddCmd.Flags().Float64Var(&addPriceIn, "price-in", 0, "Input price per million tokens (USD)")
	modelsAddCmd.Flags().Float64Var(&addPriceOut, "price-out", 0, "Output price per million tokens (USD)")
	modelsAddCmd.Flags().StringVar(&addNotes, "notes", "", "Freeform notes")
	modelsAddCmd.Flags().Float64Var(&addThreshold, "similarity-threshold", 0, "Recommended min cosine for semantic search (embedding models only, 0..1)")
	modelsAddCmd.Flags().StringVar(&addScope, "scope", "global", "Scope: global (~/.config/2nb/models.yaml) or vault (.2ndbrain/models.yaml)")
	_ = modelsAddCmd.MarkFlagRequired("provider")
	_ = modelsAddCmd.MarkFlagRequired("type")
	_ = modelsAddCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsAddCmd.RegisterFlagCompletionFunc("type", completeModelTypes)
	_ = modelsAddCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsRemoveCmd.Flags().StringVar(&removeProvider, "provider", "", "Provider: bedrock, openrouter, ollama (required)")
	modelsRemoveCmd.Flags().StringVar(&removeScope, "scope", "global", "Scope: global or vault")
	_ = modelsRemoveCmd.MarkFlagRequired("provider")
	_ = modelsRemoveCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsRemoveCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsCmd.AddCommand(modelsListCmd)
	modelsCmd.AddCommand(modelsTestCmd)
	modelsCmd.AddCommand(modelsAddCmd)
	modelsCmd.AddCommand(modelsRemoveCmd)
	modelsCmd.GroupID = "ai"
	rootCmd.AddCommand(modelsCmd)
}

func runModelsList(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	ctx := context.Background()
	merged, err := ai.BuildModelList(ctx, ai.MergedListOptions{
		Config:      v.Config.AI,
		Discover:    modelsDiscover,
		CheckStatus: modelsCheckStatus,
		VaultRoot:   v.Root,
	})
	if err != nil {
		return err
	}

	// Apply filters to both slices.
	merged.Verified = filterModels(merged.Verified)
	merged.Unverified = filterModels(merged.Unverified)

	format := getFormat(cmd)
	if format != "" {
		// Without --discover, emit flat array for backward compat.
		if !modelsDiscover {
			return output.Write(os.Stdout, format, merged.Verified)
		}
		return output.Write(os.Stdout, format, merged)
	}

	// Pretty table output.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Verified section.
	fmt.Fprintln(w, "VERIFIED MODELS (tested with 2nb)")
	printModelHeader(w, modelsCheckStatus)
	for _, m := range merged.Verified {
		printModelRow(w, m, modelsCheckStatus)
	}

	// Unverified section.
	if len(merged.Unverified) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "UNVERIFIED (available from vendor, not tested with 2nb)")
		printModelHeader(w, false)
		for _, m := range merged.Unverified {
			printModelRow(w, m, false)
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}

	// Tips.
	fmt.Println()
	fmt.Println("Tip: switch models with: 2nb config set ai.generation_model <model-id>")
	if len(merged.Unverified) > 0 {
		fmt.Println("     Models in UNVERIFIED may not work — 2nb hasn't built a harness for them yet.")
	}
	return nil
}

func filterModels(models []ai.ModelInfo) []ai.ModelInfo {
	var out []ai.ModelInfo
	for _, m := range models {
		if modelsTypeFilt != "" && m.Type != modelsTypeFilt {
			continue
		}
		if modelsFreeOnly && (m.PriceIn > 0 || m.PriceOut > 0) {
			continue
		}
		if modelsProvider != "" && m.Provider != modelsProvider {
			continue
		}
		out = append(out, m)
	}
	return out
}

func printModelHeader(w *tabwriter.Writer, showStatus bool) {
	if showStatus {
		fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tPRICE\tCTX\tTHRESHOLD\tSTATUS")
	} else {
		fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tPRICE\tCTX\tTHRESHOLD")
	}
}

func printModelRow(w *tabwriter.Writer, m ai.ModelInfo, showStatus bool) {
	price := "free"
	if m.PriceIn > 0 || m.PriceOut > 0 {
		outPart := "--"
		if m.PriceOut > 0 {
			outPart = fmt.Sprintf("$%.2f", m.PriceOut)
		}
		price = fmt.Sprintf("$%.2f/%s", m.PriceIn, outPart)
	}
	ctxLen := "-"
	if m.ContextLen > 0 {
		ctxLen = formatContext(m.ContextLen)
	}
	// THRESHOLD column is meaningful only for embedding models. Generation
	// models show "-" so the column still aligns. "-" also covers embedding
	// models without a catalog recommendation.
	threshold := "-"
	if m.Type == "embedding" && m.RecommendedSimilarityThreshold > 0 {
		threshold = fmt.Sprintf("%.2f", m.RecommendedSimilarityThreshold)
	}

	if showStatus {
		status := statusLabel(m)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, price, ctxLen, threshold, status)
	} else {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, price, ctxLen, threshold)
	}
}

func statusLabel(m ai.ModelInfo) string {
	var parts []string
	if m.Active {
		parts = append(parts, "* active")
	}
	if m.Reachable != nil {
		if *m.Reachable {
			parts = append(parts, "reachable")
		} else {
			parts = append(parts, "unreachable")
		}
	}
	if m.CredsOK != nil {
		if *m.CredsOK {
			parts = append(parts, "creds ok")
		} else {
			parts = append(parts, "no creds")
		}
	}
	return strings.Join(parts, ", ")
}

func runModelsTest(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	modelID := args[0]
	ctx := context.Background()

	if !flagPorcelain && getFormat(cmd) == "" {
		fmt.Printf("Testing %s...\n", modelID)
	}

	result, err := ai.TestProbeModel(ctx, v.Config.AI, modelID, testProvider, testModelType)
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, result)
	}

	if result.OK {
		fmt.Printf("PASS  %s/%s  (%s, %s)\n", result.Provider, result.Type, result.Latency, result.ModelID)
		if result.Detail != "" {
			// Truncate long responses.
			detail := result.Detail
			if len(detail) > 100 {
				detail = detail[:100] + "..."
			}
			fmt.Printf("  response: %s\n", detail)
		}
	} else {
		fmt.Printf("FAIL  %s/%s  (%s, %s)\n", result.Provider, result.Type, result.Latency, result.ModelID)
		fmt.Printf("  error: %s\n", result.Detail)
	}
	return nil
}

func formatContext(tokens int) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%dM", tokens/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%dK", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// runModelsAdd persists a user-defined model entry to the global or per-vault
// catalog. The vault scope requires an open vault; global works from anywhere.
func runModelsAdd(cmd *cobra.Command, args []string) error {
	modelID := args[0]
	scope, vaultRoot, err := resolveCatalogScope(addScope)
	if err != nil {
		return err
	}

	if addType != "embedding" && addType != "generation" {
		return fmt.Errorf("--type must be embedding or generation, got %q", addType)
	}
	if addThreshold != 0 {
		if addThreshold < 0 || addThreshold > 1 {
			return fmt.Errorf("--similarity-threshold must be between 0 and 1, got %g", addThreshold)
		}
		if addType != "embedding" {
			return fmt.Errorf("--similarity-threshold is only meaningful for embedding models")
		}
	}

	entry := ai.ModelInfo{
		ID:                             modelID,
		Name:                           addName,
		Provider:                       addProvider,
		Type:                           addType,
		Dimensions:                     addDimensions,
		ContextLen:                     addContextLen,
		PriceIn:                        addPriceIn,
		PriceOut:                       addPriceOut,
		Notes:                          addNotes,
		Tier:                           ai.TierUserVerified,
		PriceSource:                    "user",
		RecommendedSimilarityThreshold: addThreshold,
	}
	if entry.Name == "" {
		entry.Name = modelID
	}

	if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Added %s/%s to %s catalog\n", entry.Provider, entry.ID, scope)
	return nil
}

func runModelsRemove(cmd *cobra.Command, args []string) error {
	modelID := args[0]
	scope, vaultRoot, err := resolveCatalogScope(removeScope)
	if err != nil {
		return err
	}
	if err := ai.RemoveUserCatalogEntry(scope, vaultRoot, removeProvider, modelID); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Removed %s/%s from %s catalog\n", removeProvider, modelID, scope)
	return nil
}

// resolveCatalogScope parses the --scope flag and, for vault scope, resolves
// the vault root. Global scope works without an open vault.
func resolveCatalogScope(scope string) (ai.UserCatalogScope, string, error) {
	switch ai.UserCatalogScope(scope) {
	case ai.ScopeGlobal:
		return ai.ScopeGlobal, "", nil
	case ai.ScopeVault:
		v, err := openVault()
		if err != nil {
			return "", "", fmt.Errorf("vault scope: %w", err)
		}
		defer v.Close()
		return ai.ScopeVault, v.Root, nil
	default:
		return "", "", fmt.Errorf("--scope must be %q or %q, got %q", ai.ScopeGlobal, ai.ScopeVault, scope)
	}
}
