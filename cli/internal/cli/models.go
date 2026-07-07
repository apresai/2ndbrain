package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	modelsTypeFilt     string
	modelsFreeOnly     bool
	modelsDiscover     bool
	modelsCheckStatus  bool
	modelsProvider     string
	modelsPromote      bool
	modelsPromoteScope string
	modelsEnabledOnly  bool
	modelsRecommended  bool
	modelsSort         string
)

var (
	testProvider  string
	testModelType string
	testSave      bool
	testSaveScope string
)

var (
	addProvider     string
	addType         string
	addName         string
	addDimensions   int
	addContextLen   int
	addPriceIn      float64
	addPriceOut     float64
	addPriceRequest float64
	addThreshold    float64
	addNotes        string
	addScope        string

	removeProvider string
	removeScope    string

	enableProvider string
	enableScope    string
	enableVendor   string

	disableProvider string
	disableScope    string
	disableVendor   string

	enableStateProvider string
	enableStateScope    string
	enableStateValue    string
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

var modelsEnableCmd = &cobra.Command{
	Use:               "enable [model-id]",
	Short:             "Mark a model (or every model from a vendor with --vendor) as enabled so it appears in selection dropdowns",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsEnable,
}

var modelsDisableCmd = &cobra.Command{
	Use:               "disable [model-id]",
	Short:             "Mark a model (or every model from a vendor with --vendor) as disabled so it is hidden from selection dropdowns",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsDisable,
}

var modelsEnableStateCmd = &cobra.Command{
	Use:               "enable-state <model-id>",
	Short:             "Set a model's enabled tri-state: default, enabled, or disabled",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsEnableState,
}

func init() {
	modelsListCmd.Flags().StringVar(&modelsTypeFilt, "type", "", "Filter by type: embed or generation")
	modelsListCmd.Flags().BoolVar(&modelsFreeOnly, "free", false, "Show only free models")
	modelsListCmd.Flags().BoolVar(&modelsDiscover, "discover", false, "Query vendor APIs for full model catalogs")
	modelsListCmd.Flags().BoolVar(&modelsCheckStatus, "status", false, "Probe provider reachability and credentials")
	modelsListCmd.Flags().StringVar(&modelsProvider, "provider", "", "Filter by provider: bedrock, openrouter, ollama")
	modelsListCmd.Flags().BoolVar(&modelsPromote, "promote", false, "Test unverified discovered models and add those that pass (requires --discover)")
	modelsListCmd.Flags().StringVar(&modelsPromoteScope, "scope", "vault", "Catalog scope for --promote: vault or global")
	modelsListCmd.Flags().BoolVar(&modelsEnabledOnly, "enabled-only", false, "Exclude models explicitly disabled by the user (use for GUI dropdowns)")
	modelsListCmd.Flags().BoolVar(&modelsRecommended, "recommended", false, "Show only the curated recommended models")
	modelsListCmd.Flags().StringVar(&modelsSort, "sort", "", "Sort order: best (bench quality, then tested, recommended, tier, latency). Default keeps provider/type/ID order")
	_ = modelsListCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsListCmd.RegisterFlagCompletionFunc("type", completeModelTypes)
	_ = modelsListCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsTestCmd.Flags().StringVar(&testProvider, "provider", "", "Provider: bedrock, openrouter, ollama (auto-detected if omitted)")
	modelsTestCmd.Flags().StringVar(&testModelType, "type", "", "Model type: embedding or generation (auto-detected if omitted)")
	modelsTestCmd.Flags().BoolVar(&testSave, "save", false, "Add model to user catalog if probe passes")
	modelsTestCmd.Flags().StringVar(&testSaveScope, "scope", "vault", "Catalog scope when --save is set: vault or global")
	_ = modelsTestCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsTestCmd.RegisterFlagCompletionFunc("type", completeModelTypes)
	_ = modelsTestCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsAddCmd.Flags().StringVar(&addProvider, "provider", "", "Provider: bedrock, openrouter, ollama (required)")
	modelsAddCmd.Flags().StringVar(&addType, "type", "", "Type: embedding or generation (required)")
	modelsAddCmd.Flags().StringVar(&addName, "name", "", "Human-readable name")
	modelsAddCmd.Flags().IntVar(&addDimensions, "dimensions", 0, "Embedding dimensions (embedding models only)")
	modelsAddCmd.Flags().IntVar(&addContextLen, "context-length", 0, "Max context length in tokens")
	modelsAddCmd.Flags().Float64Var(&addPriceIn, "price-in", 0, "Input price per million tokens (USD)")
	modelsAddCmd.Flags().Float64Var(&addPriceOut, "price-out", 0, "Output price per million tokens (USD)")
	modelsAddCmd.Flags().Float64Var(&addPriceRequest, "price-request", 0, "Per-request price (USD)")
	modelsAddCmd.Flags().StringVar(&addNotes, "notes", "", "Freeform notes")
	modelsAddCmd.Flags().Float64Var(&addThreshold, "similarity-threshold", 0, "Recommended min cosine for semantic search (embedding models only, 0..1)")
	modelsAddCmd.Flags().StringVar(&addScope, "scope", "vault", "Scope: vault (.2ndbrain/models.yaml) or global (~/.config/2nb/models.yaml)")
	_ = modelsAddCmd.MarkFlagRequired("provider")
	_ = modelsAddCmd.MarkFlagRequired("type")
	_ = modelsAddCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsAddCmd.RegisterFlagCompletionFunc("type", completeModelTypes)
	_ = modelsAddCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsRemoveCmd.Flags().StringVar(&removeProvider, "provider", "", "Provider: bedrock, openrouter, ollama (required)")
	modelsRemoveCmd.Flags().StringVar(&removeScope, "scope", "vault", "Scope: vault or global")
	_ = modelsRemoveCmd.MarkFlagRequired("provider")
	_ = modelsRemoveCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsRemoveCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsEnableCmd.Flags().StringVar(&enableProvider, "provider", "", "Provider: bedrock, openrouter, ollama (required)")
	modelsEnableCmd.Flags().StringVar(&enableScope, "scope", "vault", "Scope: vault or global")
	modelsEnableCmd.Flags().StringVar(&enableVendor, "vendor", "", "Apply to every model whose VendorOf() matches (e.g. anthropic, amazon, google). Omits <model-id>.")
	_ = modelsEnableCmd.MarkFlagRequired("provider")
	_ = modelsEnableCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsEnableCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsDisableCmd.Flags().StringVar(&disableProvider, "provider", "", "Provider: bedrock, openrouter, ollama (required)")
	modelsDisableCmd.Flags().StringVar(&disableScope, "scope", "vault", "Scope: vault or global")
	modelsDisableCmd.Flags().StringVar(&disableVendor, "vendor", "", "Apply to every model whose VendorOf() matches (e.g. anthropic, amazon, google). Omits <model-id>.")
	_ = modelsDisableCmd.MarkFlagRequired("provider")
	_ = modelsDisableCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsDisableCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsEnableStateCmd.Flags().StringVar(&enableStateProvider, "provider", "", "Provider: bedrock, openrouter, ollama (required)")
	modelsEnableStateCmd.Flags().StringVar(&enableStateScope, "scope", "vault", "Scope: vault or global")
	modelsEnableStateCmd.Flags().StringVar(&enableStateValue, "state", "", "State: default, enabled, disabled")
	_ = modelsEnableStateCmd.MarkFlagRequired("provider")
	_ = modelsEnableStateCmd.MarkFlagRequired("state")
	_ = modelsEnableStateCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsEnableStateCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsCmd.AddCommand(modelsListCmd)
	modelsCmd.AddCommand(modelsTestCmd)
	modelsCmd.AddCommand(modelsAddCmd)
	modelsCmd.AddCommand(modelsRemoveCmd)
	modelsCmd.AddCommand(modelsEnableCmd)
	modelsCmd.AddCommand(modelsDisableCmd)
	modelsCmd.AddCommand(modelsEnableStateCmd)
	modelsCmd.GroupID = "ai"
	rootCmd.AddCommand(modelsCmd)
}

func runModelsList(cmd *cobra.Command, args []string) error {
	if modelsPromote && !modelsDiscover {
		return fmt.Errorf("--promote requires --discover")
	}

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
		EnabledOnly: modelsEnabledOnly,
	})
	if err != nil {
		return err
	}

	// Apply filters to both slices.
	merged.Verified = filterModels(merged.Verified)
	merged.Unverified = filterModels(merged.Unverified)

	// Opt-in presentation sort; JSON follows it so GUI callers can reuse
	// the same ranking. Default order (provider/type/ID) is unchanged.
	switch modelsSort {
	case "":
	case "best":
		ai.SortModelsBest(merged.Verified)
		ai.SortModelsBest(merged.Unverified)
	default:
		return fmt.Errorf("unknown --sort %q (supported: best)", modelsSort)
	}

	format := getFormat(cmd)
	if format != "" {
		// Without --discover, emit flat array for backward compat.
		if !modelsDiscover {
			return output.Write(os.Stdout, format, merged.Verified)
		}
		return output.Write(os.Stdout, format, merged)
	}

	// Pretty table output.
	for _, warning := range merged.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
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
	if !anyBenchmarked(merged.Verified) {
		fmt.Println("     Run '2nb models bench' to add quality/latency data to the BENCH column.")
	}
	if len(merged.Unverified) > 0 && !modelsPromote {
		fmt.Println("     Models in UNVERIFIED may not work — 2nb hasn't built a harness for them yet.")
		fmt.Println("     Run with --promote to test and auto-add the ones that work.")
	}

	if !modelsPromote || len(merged.Unverified) == 0 {
		return nil
	}

	// --promote: test all unverified models concurrently and save passing ones.
	total := len(merged.Unverified)
	fmt.Printf("\nPromoting %d unverified model(s) — testing concurrently (max %d)...\n\n", total, probeConcurrency)

	var passed int
	scope := ai.UserCatalogScope(modelsPromoteScope)

	probeModelsConcurrently(ctx, v.Config.AI, merged.Unverified, func(n int, m ai.ModelInfo, result *ai.TestProbeResult, err error) {
		if err == nil && result != nil && result.OK {
			entry := promotedEntry(&m, result)
			if saveErr := ai.SaveUserCatalogEntry(scope, v.Root, entry); saveErr == nil {
				passed++
				fmt.Printf("[%d/%d] PASS  %s/%s  (%s)  → saved\n",
					n, total, result.Provider, result.Type, result.ModelID)
			} else {
				fmt.Printf("[%d/%d] PASS  %s/%s  (%s)  → save failed: %v\n",
					n, total, result.Provider, result.Type, result.ModelID, saveErr)
			}
		} else {
			detail := m.ID
			if result != nil && result.Detail != "" {
				detail = result.Detail
			} else if err != nil {
				detail = err.Error()
			}
			if result != nil && result.Code != "" && result.Code != ai.TestErrUnknown {
				detail = fmt.Sprintf("[%s] %s", result.Code, detail)
			}
			fmt.Printf("[%d/%d] FAIL  %s/%s  (%s)  %s\n",
				n, total, m.Provider, m.Type, m.ID, detail)
		}
	})

	fmt.Printf("\nPromoted %d of %d models to %s catalog.\n", passed, total, modelsPromoteScope)
	return nil
}

// probeConcurrency bounds the worker pool for batch model probes
// (--promote and models verify).
const probeConcurrency = 5

// probeModelsConcurrently runs TestProbeModel over models with a bounded
// worker pool. onResult runs under a shared mutex (safe to print and save
// from) with n as the 1-based completion counter.
func probeModelsConcurrently(ctx context.Context, cfg ai.AIConfig, models []ai.ModelInfo, onResult func(n int, m ai.ModelInfo, result *ai.TestProbeResult, err error)) {
	sem := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var counter atomic.Int32

	for _, m := range models {
		wg.Add(1)
		go func(m ai.ModelInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			n := int(counter.Add(1))
			result, err := ai.TestProbeModel(ctx, cfg, m.ID, m.Provider, m.Type)

			mu.Lock()
			defer mu.Unlock()
			onResult(n, m, result, err)
		}(m)
	}
	wg.Wait()
}

func filterModels(models []ai.ModelInfo) []ai.ModelInfo {
	var out []ai.ModelInfo
	for _, m := range models {
		if modelsTypeFilt != "" && m.Type != modelsTypeFilt {
			continue
		}
		if modelsFreeOnly && !ai.IsExplicitlyFree(m) {
			continue
		}
		if modelsProvider != "" && m.Provider != modelsProvider {
			continue
		}
		if modelsRecommended && !m.Recommended {
			continue
		}
		out = append(out, m)
	}
	return out
}

func printModelHeader(w *tabwriter.Writer, showStatus bool) {
	if showStatus {
		fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tPRICE\tCTX\tTHRESHOLD\tBENCH\tSTATE\tSTATUS")
	} else {
		fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tPRICE\tCTX\tTHRESHOLD\tBENCH\tSTATE")
	}
}

func printModelRow(w *tabwriter.Writer, m ai.ModelInfo, showStatus bool) {
	price := ai.CompactPriceLabel(m)
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, price, ctxLen, threshold, benchLabel(m), stateLabel(m), status)
	} else {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, price, ctxLen, threshold, benchLabel(m), stateLabel(m))
	}
}

// benchLabel renders the latest benchmark summary compactly: retrieval
// quality (embedding models only produce one) plus average latency, latency
// alone, or "-" for never-benchmarked. Bench history lives in bench.db; this
// is the inline summary the catalog carries.
func benchLabel(m ai.ModelInfo) string {
	b := m.Benchmark
	if b == nil {
		return "-"
	}
	switch {
	case b.QualityScore > 0 && b.AvgLatencyMs > 0:
		return fmt.Sprintf("q=%.2f %dms", b.QualityScore, b.AvgLatencyMs)
	case b.QualityScore > 0:
		return fmt.Sprintf("q=%.2f", b.QualityScore)
	case b.AvgLatencyMs > 0:
		return fmt.Sprintf("%dms", b.AvgLatencyMs)
	default:
		return "-"
	}
}

// anyBenchmarked reports whether at least one model carries bench data, so
// the empty BENCH column can carry an honest how-to-fill-it tip.
func anyBenchmarked(models []ai.ModelInfo) bool {
	for _, m := range models {
		if m.Benchmark != nil {
			return true
		}
	}
	return false
}

// stateLabel renders curation + per-account test state compactly: a leading
// ★ for recommended models, then the last test outcome ("ok 3d" for a pass,
// the classified test_error_code for a failure), or "-" when untested.
func stateLabel(m ai.ModelInfo) string {
	var state string
	switch {
	case m.TestedAt != "" && m.TestError == "":
		state = "ok"
		if age := testAge(m.TestedAt); age != "" {
			state += " " + age
		}
	case m.TestErrorCode != "":
		state = m.TestErrorCode
	case m.TestError != "":
		state = "failed"
	default:
		state = "-"
	}
	if m.Recommended {
		return "★ " + state
	}
	return state
}

// testAge renders how long ago a test ran, in the largest sensible unit.
func testAge(testedAt string) string {
	t, err := time.Parse(time.RFC3339, testedAt)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return "now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
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
	setupFileLogging(v)

	modelID := args[0]
	ctx := context.Background()

	if !flagPorcelain && getFormat(cmd) == "" {
		fmt.Printf("Testing %s...\n", modelID)
	}

	result, err := ai.TestProbeModel(ctx, v.Config.AI, modelID, testProvider, testModelType)
	if err != nil {
		return err
	}
	slog.Info("models test", "provider", result.Provider, "model", result.ModelID, "type", result.Type, "ok", result.OK, "code", string(result.Code), "save", testSave)

	if testSave {
		scope := ai.UserCatalogScope(testSaveScope)
		entry := catalogEntryFromTestResult(ctx, v.Config.AI, v.Root, result)
		entry.Enabled = preserveScopeEnabled(scope, v.Root, entry.Provider, entry.ID)
		if err := ai.SaveUserCatalogEntry(scope, v.Root, entry); err != nil {
			if getFormat(cmd) != "" {
				return fmt.Errorf("save test result: %w", err)
			}
			fmt.Printf("  warning: failed to save: %v\n", err)
		} else {
			slog.Info("models test saved", "provider", entry.Provider, "model", entry.ID, "type", entry.Type, "ok", result.OK, "code", entry.TestErrorCode, "scope", testSaveScope)
		}
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
		if testSave {
			fmt.Printf("  → saved to %s catalog\n", testSaveScope)
		}
	} else {
		fmt.Printf("FAIL  %s/%s  (%s, %s)\n", result.Provider, result.Type, result.Latency, result.ModelID)
		fmt.Printf("  error: %s\n", result.Detail)
		if result.Code != "" && result.Code != ai.TestErrUnknown {
			fmt.Printf("  cause: %s\n", result.Code)
		}
		if result.Remediation != "" {
			fmt.Printf("  fix: %s\n", result.Remediation)
		}
		if testSave {
			fmt.Printf("  → saved failure to %s catalog\n", testSaveScope)
		}
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
	priceOverride := cmd.Flags().Changed("price-in") || cmd.Flags().Changed("price-out") || cmd.Flags().Changed("price-request")

	if addType != "embedding" && addType != "generation" {
		return fmt.Errorf("--type must be embedding or generation, got %q", addType)
	}
	thresholdChanged := cmd.Flags().Changed("similarity-threshold")
	if thresholdChanged {
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
		PriceRequest:                   addPriceRequest,
		Notes:                          addNotes,
		Tier:                           ai.TierUserVerified,
		PriceOverride:                  priceOverride,
		RecommendedSimilarityThreshold: addThreshold,
	}
	if priceOverride {
		entry.PriceSource = "user"
		warnSuspiciousPerMillionPrice(cmd, "price-in", addPriceIn)
		warnSuspiciousPerMillionPrice(cmd, "price-out", addPriceOut)
	}
	if existing, ok := findCurrentCatalogEntry(vaultRoot, addProvider, modelID); ok {
		entry = mergeAddCatalogEntry(cmd, existing, entry, priceOverride)
	} else if entry.Name == "" {
		entry.Name = modelID
	}

	if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	slog.Info("models add", "provider", entry.Provider, "model", entry.ID, "type", entry.Type, "scope", scope)
	fmt.Fprintf(cmd.ErrOrStderr(), "Added %s/%s to %s catalog\n", entry.Provider, entry.ID, scope)
	return nil
}

func warnSuspiciousPerMillionPrice(cmd *cobra.Command, flagName string, value float64) {
	if !cmd.Flags().Changed(flagName) || value <= 0 || value >= 0.001 {
		return
	}
	msg := fmt.Sprintf("--%s is interpreted as USD per million tokens; %.8g looks unusually low", flagName, value)
	slog.Warn("suspicious per-million token price", "flag", flagName, "value", value)
	fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s. If you meant per-token pricing, multiply by 1,000,000.\n", msg)
}

func findCurrentCatalogEntry(vaultRoot, provider, modelID string) (ai.ModelInfo, bool) {
	for _, m := range ai.LoadUserCatalog(vaultRoot) {
		if m.Provider == provider && m.ID == modelID {
			return m, true
		}
	}
	return ai.ModelInfo{}, false
}

func mergeAddCatalogEntry(cmd *cobra.Command, existing, patch ai.ModelInfo, priceOverride bool) ai.ModelInfo {
	out := existing
	out.ID = patch.ID
	out.Provider = patch.Provider
	out.Type = patch.Type
	if patch.Name != "" {
		out.Name = patch.Name
	} else if out.Name == "" {
		out.Name = patch.ID
	}
	if cmd.Flags().Changed("dimensions") || patch.Dimensions != 0 {
		out.Dimensions = patch.Dimensions
	}
	if cmd.Flags().Changed("context-length") || patch.ContextLen != 0 {
		out.ContextLen = patch.ContextLen
	}
	if priceOverride {
		out.PriceIn = patch.PriceIn
		out.PriceOut = patch.PriceOut
		out.PriceRequest = patch.PriceRequest
		out.PriceSource = "user"
		out.PriceOverride = true
	}
	if patch.Notes != "" {
		out.Notes = patch.Notes
	}
	if cmd.Flags().Changed("similarity-threshold") {
		out.RecommendedSimilarityThreshold = patch.RecommendedSimilarityThreshold
	}
	if out.Tier == "" {
		out.Tier = ai.TierUserVerified
	}
	return out
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
	slog.Info("models remove", "provider", removeProvider, "model", modelID, "scope", scope)
	fmt.Fprintf(cmd.ErrOrStderr(), "Removed %s/%s from %s catalog\n", removeProvider, modelID, scope)
	return nil
}

func runModelsEnable(cmd *cobra.Command, args []string) error {
	return runModelsEnableDisable(cmd, args, enableProvider, enableScope, enableVendor, true)
}

func runModelsDisable(cmd *cobra.Command, args []string) error {
	return runModelsEnableDisable(cmd, args, disableProvider, disableScope, disableVendor, false)
}

func runModelsEnableState(cmd *cobra.Command, args []string) error {
	return setModelEnabledState(cmd, args[0], enableStateProvider, enableStateScope, enableStateValue)
}

// runModelsEnableDisable dispatches three call shapes:
//  1. One positional arg, no --vendor: single-model toggle.
//  2. --vendor with positional args: batch-by-ids (caller pre-resolved
//     the model list — used by the GUI to cover discovered-only entries
//     the server's catalog lookup would miss).
//  3. --vendor alone: batch-by-catalog-lookup (terminal users who don't
//     want to enumerate IDs; only matches what's already in the merged
//     catalog).
func runModelsEnableDisable(cmd *cobra.Command, args []string, provider, scopeStr, vendor string, enabled bool) error {
	if len(args) == 0 && vendor == "" {
		return fmt.Errorf("pass either a <model-id> or --vendor <name>")
	}
	if len(args) == 1 && vendor == "" {
		return setModelEnabled(cmd, args[0], provider, scopeStr, enabled)
	}
	// Vendor batch: with or without explicit IDs.
	return setVendorEnabled(cmd, vendor, provider, scopeStr, enabled, args)
}

// setVendorEnabled persists enable/disable for a vendor group. When
// explicitIDs is non-empty those are used directly (caller already
// knows the full set — the GUI path). Otherwise we resolve matching
// models from the merged user+builtin catalog.
func setVendorEnabled(cmd *cobra.Command, vendor, provider, scopeStr string, enabled bool, explicitIDs []string) error {
	scope, vaultRoot, err := resolveCatalogScope(scopeStr)
	if err != nil {
		return err
	}

	var modelIDs []string
	if len(explicitIDs) > 0 {
		modelIDs = explicitIDs
	} else {
		// Catalog lookup: only finds verified + user-saved entries.
		// Discovered-only models need the explicit-IDs path.
		list, err := ai.BuildModelList(cmd.Context(), ai.MergedListOptions{VaultRoot: vaultRoot})
		if err != nil {
			return fmt.Errorf("load model catalog: %w", err)
		}
		for _, m := range list.Verified {
			if m.Provider != provider {
				continue
			}
			if ai.VendorOf(m.ID, provider).Vendor != vendor {
				continue
			}
			modelIDs = append(modelIDs, m.ID)
		}
	}
	if len(modelIDs) == 0 {
		return fmt.Errorf("no models found for vendor=%s provider=%s (tip: pass model IDs as positional args to cover discovered-only entries)", vendor, provider)
	}

	// Preload the user catalog so we merge rather than fetching per
	// model. Entries keyed by (provider, id).
	userByKey := make(map[string]ai.ModelInfo)
	for _, m := range ai.LoadUserCatalog(vaultRoot) {
		userByKey[m.Provider+"|"+m.ID] = m
	}

	count := 0
	for _, id := range modelIDs {
		key := provider + "|" + id
		entry, found := userByKey[key]
		if !found {
			entry = ai.ModelInfo{
				ID:       id,
				Provider: provider,
				Tier:     ai.TierUserVerified,
			}
		}
		entry.Enabled = ai.Ptr(enabled)
		if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
			return fmt.Errorf("save %s: %w", id, err)
		}
		count++
	}

	verb := "enabled"
	if !enabled {
		verb = "disabled"
	}
	slog.Info("models vendor enable-state", "provider", provider, "vendor", vendor, "state", verb, "scope", scope, "count", count)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %d %s model(s) from %s in %s catalog\n", verb, count, provider, vendor, scope)
	return nil
}

// setModelEnabled writes an Enabled pointer into the user-catalog entry for
// (provider, modelID). When no entry exists yet (builtin-only models), a
// minimal entry is created so the flag persists without a prior `models add`.
func setModelEnabled(cmd *cobra.Command, modelID, provider, scopeStr string, enabled bool) error {
	return setModelEnabledPointer(cmd, modelID, provider, scopeStr, ai.Ptr(enabled), enabledStateLabel(enabled))
}

func setModelEnabledState(cmd *cobra.Command, modelID, provider, scopeStr, state string) error {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "default", "unset", "auto":
		return setModelEnabledPointer(cmd, modelID, provider, scopeStr, nil, "default")
	case "enabled", "enable", "true", "on":
		return setModelEnabledPointer(cmd, modelID, provider, scopeStr, ai.Ptr(true), "enabled")
	case "disabled", "disable", "false", "off":
		return setModelEnabledPointer(cmd, modelID, provider, scopeStr, ai.Ptr(false), "disabled")
	default:
		return fmt.Errorf("--state must be default, enabled, or disabled, got %q", state)
	}
}

func setModelEnabledPointer(cmd *cobra.Command, modelID, provider, scopeStr string, enabled *bool, label string) error {
	scope, vaultRoot, err := resolveCatalogScope(scopeStr)
	if err != nil {
		return err
	}

	// Load user catalog and find an existing entry; fall back to a minimal one
	// so enable/disable work against purely-builtin models too.
	user := ai.LoadUserCatalog(vaultRoot)
	var entry ai.ModelInfo
	found := false
	for _, m := range user {
		if m.Provider == provider && m.ID == modelID {
			entry = m
			found = true
			break
		}
	}
	if !found {
		entry = ai.ModelInfo{
			ID:       modelID,
			Provider: provider,
			Tier:     ai.TierUserVerified,
		}
	}

	entry.Enabled = enabled
	if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	slog.Info("models enable-state", "provider", provider, "model", modelID, "state", label, "scope", scope)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %s/%s in %s catalog\n", label, provider, modelID, scope)
	return nil
}

func enabledStateLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func catalogEntryFromTestResult(ctx context.Context, cfg ai.AIConfig, vaultRoot string, result *ai.TestProbeResult) ai.ModelInfo {
	var base *ai.ModelInfo
	if current, ok := findModelInfo(ctx, cfg, vaultRoot, result.Provider, result.ModelID); ok {
		base = &current
	} else {
		base = findBuiltinModel(result.Provider, result.ModelID)
	}

	var entry ai.ModelInfo
	if result.OK {
		entry = promotedEntry(base, result)
		entry.TestError = ""
		entry.TestErrorCode = ""
	} else if base != nil {
		entry = *base
		if entry.Tier == "" {
			entry.Tier = ai.TierUnverified
		}
	} else {
		entry = ai.ModelInfo{
			ID:       result.ModelID,
			Provider: result.Provider,
			Type:     result.Type,
			Tier:     ai.TierUnverified,
		}
	}

	entry.ID = result.ModelID
	entry.Provider = result.Provider
	entry.Type = result.Type
	entry.TestedAt = time.Now().UTC().Format(time.RFC3339)
	entry.TestLatencyMs = latencyMs(result.Latency)
	if !result.OK {
		entry.TestError = result.Detail
		entry.TestErrorCode = string(result.Code)
	}
	return entry
}

func findModelInfo(ctx context.Context, cfg ai.AIConfig, vaultRoot, provider, id string) (ai.ModelInfo, bool) {
	list, err := ai.BuildModelList(ctx, ai.MergedListOptions{
		Config:    cfg,
		VaultRoot: vaultRoot,
	})
	if err == nil {
		for _, m := range list.Verified {
			if m.Provider == provider && m.ID == id {
				return m, true
			}
		}
	}
	for _, m := range ai.LoadUserCatalog(vaultRoot) {
		if m.Provider == provider && m.ID == id {
			return m, true
		}
	}
	return ai.ModelInfo{}, false
}

// promotedEntry builds a user-catalog ModelInfo from a passing probe result.
// base (from the builtin catalog or discovery data) supplies name, pricing, and
// dimensions when available; Tier and TestedAt are always set from the promotion.
func promotedEntry(base *ai.ModelInfo, result *ai.TestProbeResult) ai.ModelInfo {
	entry := ai.ModelInfo{
		ID:            result.ModelID,
		Provider:      result.Provider,
		Type:          result.Type,
		Tier:          ai.TierUserVerified,
		TestedAt:      time.Now().UTC().Format(time.RFC3339),
		TestLatencyMs: latencyMs(result.Latency),
	}
	if base != nil {
		entry.Name = base.Name
		entry.Dimensions = base.Dimensions
		entry.ContextLen = base.ContextLen
		entry.PriceIn = base.PriceIn
		entry.PriceOut = base.PriceOut
		entry.PriceRequest = base.PriceRequest
		entry.RecommendedSimilarityThreshold = base.RecommendedSimilarityThreshold
		entry.Notes = base.Notes
		if base.PriceSource != "" {
			entry.PriceSource = base.PriceSource
		} else if base.PriceIn > 0 || base.PriceOut > 0 || base.PriceRequest > 0 {
			entry.PriceSource = "vendor"
		}
	}
	// For embedding models with no dimension info, parse actual dims from the
	// probe result detail ("dims=1024") so promoted entries carry accurate metadata.
	if result.Type == "embedding" && entry.Dimensions == 0 {
		if d := parseDimsFromDetail(result.Detail); d > 0 {
			entry.Dimensions = d
		}
	}
	return entry
}

// parseDimsFromDetail extracts the embedding dimension from a probe result
// detail string of the form "dims=1024 ...".
func parseDimsFromDetail(detail string) int {
	if i := strings.Index(detail, "dims="); i >= 0 {
		var d int
		fmt.Sscanf(detail[i+5:], "%d", &d)
		return d
	}
	return 0
}

// findBuiltinModel returns the builtin catalog entry for (provider, id), or nil.
func findBuiltinModel(provider, id string) *ai.ModelInfo {
	for _, m := range ai.BuiltinCatalog() {
		if m.Provider == provider && m.ID == id {
			cp := m
			return &cp
		}
	}
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
		setupFileLogging(v)
		return ai.ScopeVault, v.Root, nil
	default:
		return "", "", fmt.Errorf("--scope must be %q or %q, got %q", ai.ScopeGlobal, ai.ScopeVault, scope)
	}
}
