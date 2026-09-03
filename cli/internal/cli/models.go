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
	modelsWorkingSet   bool
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
	Use:   "test <model-id>",
	Short: "Test if a model works with 2nb",
	Long: `Sends a quick probe (embed or generate) to verify a model is callable.
The model-id argument accepts the route form id@plane/region, which probes
exactly that endpoint (so a mantle-discovered model can be tested without a
prior catalog save).`,
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
add models 2nb doesn't ship yet without editing source.

The argument is a bare model id. A route-qualified form (id@plane/region) is
refused: models add describes a model, not one endpoint.`,
	Args: cobra.ExactArgs(1),
	RunE: runModelsAdd,
}

var modelsRemoveCmd = &cobra.Command{
	Use:   "remove <model-id>",
	Short: "Remove a model from your personal catalog",
	Long: `Removes catalog rows for a model. A bare id removes every route of
that model. A route-qualified id (id@plane/region) removes only that route.
Exits non-zero when nothing matched.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsRemove,
}

var modelsEnableCmd = &cobra.Command{
	Use:   "enable [model-id]",
	Short: "Mark a model (or every model from a vendor with --vendor) as enabled so it appears in selection dropdowns",
	Long: `Enable is intent about the MODEL: a route-qualified id is accepted and
applied to every route of that model, not just the named endpoint.`,
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsEnable,
}

var modelsDisableCmd = &cobra.Command{
	Use:   "disable [model-id]",
	Short: "Mark a model (or every model from a vendor with --vendor) as disabled so it is hidden from selection dropdowns",
	Long: `Disable is intent about the MODEL: a route-qualified id is accepted and
applied to every route of that model, not just the named endpoint.`,
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsDisable,
}

var modelsEnableStateCmd = &cobra.Command{
	Use:   "enable-state <model-id>",
	Short: "Set a model's enabled tri-state: default, enabled, or disabled",
	Long: `Enable-state is intent about the MODEL: a route-qualified id is accepted
and applied to every route of that model, not just the named endpoint.`,
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
	modelsListCmd.Flags().BoolVar(&modelsWorkingSet, "working-set", false, "Show only models with a passing probe on record (plus untested active models); a failed active probe is excluded")
	modelsListCmd.Flags().StringVar(&modelsSort, "sort", "", "Sort order: best (bench quality, then tested, recommended, tier, latency). Default keeps provider/type/ID order")
	_ = modelsListCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsListCmd.RegisterFlagCompletionFunc("type", completeModelTypes)
	_ = modelsListCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsTestCmd.Flags().StringVar(&testProvider, "provider", "", "Provider: bedrock, openrouter, ollama (auto-detected if omitted)")
	modelsTestCmd.Flags().StringVar(&testModelType, "type", "", "Model type: embedding or generation (auto-detected if omitted)")
	modelsTestCmd.Flags().BoolVar(&testSave, "save", false, "Save the probe result to the user catalog, pass or fail (a pass sets tier=user_verified; a failure records the classified test error)")
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

	probeModelsConcurrently(ctx, v.Config.AI, v.Root, merged.Unverified, func(n int, m ai.ModelInfo, result *ai.TestProbeResult, err error) {
		if err == nil && result != nil && result.OK {
			entry := promotedEntry(&m, result)
			preserveRoutingFields(scope, v.Root, &entry)
			adoptCandidateRouting(&entry, m)
			// Stamp the probed route LAST, so the save lands on the endpoint
			// that was actually called. promotedEntry copies no route, and
			// preserveRoutingFields' unique-row fallback would otherwise
			// supply a SIBLING's: a classic promote of a dual-plane id saved
			// under the stored mantle route, overwriting that row's real
			// verdict with a false positive that even carried the mantle
			// strategy. adoptCandidateRouting cannot correct it (fill-only-empty).
			persistProbedRegion(&entry, result, ai.ResolveBedrockConfig(v.Config.AI.Bedrock).Region)
			preserveUserFacts(scope, v.Root, &entry)
			preserveUserThreshold(scope, v.Root, &entry)
			// Wholesale: a passing probe records a complete fresh verdict.
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
// from) with n as the 1-based completion counter. vaultRoot scopes the
// user-catalog invoke-strategy lookups inside each probe.
func probeModelsConcurrently(ctx context.Context, cfg ai.AIConfig, vaultRoot string, models []ai.ModelInfo, onResult func(n int, m ai.ModelInfo, result *ai.TestProbeResult, err error)) {
	probeModelsConcurrentlyRegions(ctx, cfg, vaultRoot, models, nil, onResult)
}

// probeModelsConcurrentlyRegions is probeModelsConcurrently with a region set:
// each failing classic-Bedrock probe retries sequentially in the next included
// region inside its own worker slot (see ai.TestProbeModelInRegions). A nil or
// single-region set is exactly the old behavior.
func probeModelsConcurrentlyRegions(ctx context.Context, cfg ai.AIConfig, vaultRoot string, models []ai.ModelInfo, regions []string, onResult func(n int, m ai.ModelInfo, result *ai.TestProbeResult, err error)) {
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
			// The hinted variant: batch candidates are full ModelInfo rows, so
			// a mantle-DISCOVERED model (no catalog entry) carries its own
			// InvokeStrategy/Region and probes over the right plane. Catalog
			// resolution still wins inside.
			result, err := ai.TestProbeModelInfoInRegions(ctx, cfg, m, vaultRoot, regions)

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
		if modelsWorkingSet && !m.Working {
			continue
		}
		out = append(out, m)
	}
	return out
}

func printModelHeader(w *tabwriter.Writer, showStatus bool) {
	if showStatus {
		fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tROUTE\tPRICE\tCTX\tTHRESHOLD\tBENCH\tSTATE\tSTATUS")
	} else {
		fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tROUTE\tPRICE\tCTX\tTHRESHOLD\tBENCH\tSTATE")
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, routeLabel(m), price, ctxLen, threshold, benchLabel(m), stateLabel(m), status)
	} else {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, routeLabel(m), price, ctxLen, threshold, benchLabel(m), stateLabel(m))
	}
}

// routeLabel renders the endpoint a row invokes: "<plane> <region>", or the
// plane alone for an unpinned template, or "-" for a provider with no routes.
//
// Without this column, a model served in three regions printed three
// byte-identical lines, which reads as a duplicate bug and hides the only
// thing that distinguishes them. `models discover` already names the route
// for the same reason; leaving `models list` without it made the two commands
// contradict each other.
func routeLabel(m ai.ModelInfo) string {
	if m.Provider != "bedrock" || m.Plane == "" {
		return "-"
	}
	if m.Region == "" {
		return string(m.Plane)
	}
	return string(m.Plane) + " " + m.Region
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

	ref, err := parseModelRef(args[0], testProvider)
	if err != nil {
		return err
	}
	ctx := context.Background()

	if !flagPorcelain && getFormat(cmd) == "" {
		fmt.Printf("Testing %s...\n", args[0])
	}

	// Region-aware like verify: a pinned model re-checks primary (self-heal)
	// and the included regions apply, so `models test --save` can never
	// freeze a stale pin that verify would have cleared. A route-qualified
	// argument names the endpoint; TestProbeModelInfoInRegions honors a
	// candidate that already has Plane/Region set.
	m := ai.ModelInfo{
		ID:       ref.ID,
		Provider: ref.Provider,
		Type:     testModelType,
		Plane:    ref.Plane,
		Region:   ref.Region,
	}
	result, err := ai.TestProbeModelInfoInRegions(ctx, v.Config.AI, m, v.Root, ai.ResolveBedrockRegions(v.Config.AI.Bedrock))
	if err != nil {
		return err
	}
	slog.Info("models test", "provider", result.Provider, "model", result.ModelID, "type", result.Type, "ok", result.OK, "code", string(result.Code), "save", testSave)

	if testSave {
		scope := ai.UserCatalogScope(testSaveScope)
		entry := catalogEntryFromTestResult(ctx, v.Config.AI, v.Root, result)
		entry.Enabled = preserveScopeEnabled(scope, v.Root, entry.Provider, entry.ID)
		preserveRoutingFields(scope, v.Root, &entry)
		preserveUserFacts(scope, v.Root, &entry)
		preserveUserThreshold(scope, v.Root, &entry)
		// Wholesale: a probe records a complete fresh verdict (pass or fail).
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
	ref, err := parseModelRef(args[0], addProvider)
	if err != nil {
		return err
	}
	if ref.Plane != "" || ref.Region != "" {
		return fmt.Errorf("models add takes a bare model id, not a route; add %s (then pick a route with models discover or config set)", ref.ID)
	}
	modelID := ref.ID
	if ref.Provider != "" {
		addProvider = ref.Provider
	}
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
	if thresholdChanged {
		// Stamp the provenance on the fresh-row path too. mergeAddCatalogEntry
		// stamps the merge path, but a first `models add` for this model never
		// reaches it, and an unstamped value that happened to equal the builtin
		// would then read back as the builtin's own recommendation.
		entry.ThresholdSource = ai.ThresholdSourceUser
	}
	if factsTyped(cmd) {
		// Same rule for the model facts: the flags are the only way a user can
		// author a name, dimension or context length, so they are the only
		// thing that stamps one. Without the stamp the value is
		// indistinguishable from a copy some probe save took off the builtin,
		// and the merge deliberately ignores those.
		entry.FactSource = ai.FactSourceUser
	}
	if existing, ok := findCurrentCatalogEntry(vaultRoot, addProvider, modelID); ok {
		entry = mergeAddCatalogEntry(cmd, existing, entry, priceOverride)
	} else if entry.Name == "" && findBuiltinModel(addProvider, modelID) == nil {
		// The id-as-name fallback keeps a user-only row from rendering nameless.
		// A BUILTIN model already has a name, and writing the id over it is the
		// mirroring in reverse: the stamp says "the user typed these facts", so
		// a fallback the user never typed must not ride along under it.
		entry.Name = modelID
	}

	// Wholesale for a new row; mergeAddCatalogEntry already merged when a
	// stored row existed. models add describes a MODEL, not one endpoint.
	if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	slog.Info("models add", "provider", entry.Provider, "model", entry.ID, "type", entry.Type, "scope", scope)
	fmt.Fprintf(cmd.ErrOrStderr(), "Added %s/%s to %s catalog\n", entry.Provider, entry.ID, scope)
	return nil
}

// factsTyped reports whether this `models add` invocation authored a model
// FACT. Only these three flags can, so only they stamp ai.FactSourceUser; an
// add that touches none of them leaves whatever stamp the stored row carried,
// because mergeAddCatalogEntry starts from that row.
func factsTyped(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("name") ||
		cmd.Flags().Changed("dimensions") ||
		cmd.Flags().Changed("context-length")
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
	// `models add` describes a MODEL, not one of its endpoints: it takes no
	// route argument and its fields (name, prices, dimensions, context length,
	// notes) are all model-level. Writing it route-less makes it a template,
	// whose facts retireSupersededTemplates distributes to every concrete
	// route. Inheriting `existing`'s route instead landed the edit on whichever
	// row sorted first, so a price override applied to one region and not its
	// siblings.
	out.Plane, out.Region = "", ""
	if patch.Name != "" {
		out.Name = patch.Name
	} else if out.Name == "" && findBuiltinModel(patch.Provider, patch.ID) == nil {
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
		// The user typed the number, so stamp it. A later `models add` without
		// the flag leaves both fields alone: `out` starts from the raw stored
		// user row, so an earlier stamp survives untouched.
		out.ThresholdSource = ai.ThresholdSourceUser
	}
	if factsTyped(cmd) {
		out.FactSource = ai.FactSourceUser
	}
	if out.Tier == "" {
		out.Tier = ai.TierUserVerified
	}
	return out
}

func runModelsRemove(cmd *cobra.Command, args []string) error {
	ref, err := parseModelRef(args[0], removeProvider)
	if err != nil {
		return err
	}
	scope, vaultRoot, err := resolveCatalogScope(removeScope)
	if err != nil {
		return err
	}
	n, err := ai.RemoveUserCatalogEntry(scope, vaultRoot, ai.RouteKey{
		Provider: ref.Provider, ID: ref.ID, Plane: ref.Plane, Region: ref.Region,
	})
	if err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("nothing matched %s in %s catalog", args[0], scope)
	}
	slog.Info("models remove", "provider", ref.Provider, "model", ref.ID, "plane", ref.Plane, "region", ref.Region, "removed", n, "scope", scope)
	fmt.Fprintf(cmd.ErrOrStderr(), "Removed %d matching catalog row(s) for %s from %s catalog\n", n, args[0], scope)
	return nil
}

// parseModelRef parses a positional model-id argument as a route ref.
// An omitted provider is filled from flagProvider (the command's --provider).
func parseModelRef(raw, flagProvider string) (ai.RouteRef, error) {
	ref, err := ai.ParseRouteRef(raw)
	if err != nil {
		return ref, err
	}
	if ref.Provider == "" {
		ref.Provider = flagProvider
	}
	return ref, nil
}

func runModelsEnable(cmd *cobra.Command, args []string) error {
	return runModelsEnableDisable(cmd, args, enableProvider, enableScope, enableVendor, true)
}

func runModelsDisable(cmd *cobra.Command, args []string) error {
	return runModelsEnableDisable(cmd, args, disableProvider, disableScope, disableVendor, false)
}

func runModelsEnableState(cmd *cobra.Command, args []string) error {
	ref, err := parseModelRef(args[0], enableStateProvider)
	if err != nil {
		return err
	}
	return setModelEnabledState(cmd, ref.ID, ref.Provider, enableStateScope, enableStateValue)
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
		ref, err := parseModelRef(args[0], provider)
		if err != nil {
			return err
		}
		return setModelEnabled(cmd, ref.ID, ref.Provider, scopeStr, enabled)
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
		for _, raw := range explicitIDs {
			ref, err := parseModelRef(raw, provider)
			if err != nil {
				return err
			}
			modelIDs = append(modelIDs, ref.ID)
		}
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

	// Preload the user catalog so we merge rather than fetching per model.
	// Grouped by (provider, id) but keeping EVERY route: a map keyed by
	// (provider, id) would retain only the last row per model, so the batch
	// would flag one of N routes. filterEnabled is per-row, so a model
	// "disabled" that way stays in dropdowns via its other routes — the same
	// silently-ineffective command setModelEnabledPointer was fixed for, and
	// this is the path the macOS app's vendor toggles call.
	userByModel := make(map[string][]ai.ModelInfo)
	for _, m := range ai.LoadUserCatalog(vaultRoot) {
		k := m.Provider + "|" + m.ID
		userByModel[k] = append(userByModel[k], m)
	}

	count := 0
	for _, id := range modelIDs {
		targets := userByModel[provider+"|"+id]
		if len(targets) == 0 {
			targets = []ai.ModelInfo{{
				ID:       id,
				Provider: provider,
				Tier:     ai.TierUserVerified,
			}}
		}
		for _, entry := range targets {
			entry.Enabled = ai.Ptr(enabled)
			preserveRoutingFields(scope, vaultRoot, &entry)
			// RMW: copy every stored route of the model, then stamp Enabled.
			if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
				return fmt.Errorf("save %s: %w", id, err)
			}
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

	// Enable/disable is intent about the MODEL, so it applies to every route
	// of that model, matching `models remove`.
	//
	// Taking the first (provider, id) match set the flag on 1 of N rows, and
	// filterEnabled is per-row, so a disabled model stayed in dropdowns via its
	// other routes — a silently ineffective command, reachable from the GUI.
	user := ai.LoadUserCatalog(vaultRoot)
	var targets []ai.ModelInfo
	for _, m := range user {
		if m.Provider == provider && m.ID == modelID {
			targets = append(targets, m)
		}
	}
	if len(targets) == 0 {
		// Purely-builtin model: a minimal row carries the flag.
		targets = []ai.ModelInfo{{
			ID:       modelID,
			Provider: provider,
			Tier:     ai.TierUserVerified,
		}}
	}

	for _, entry := range targets {
		entry.Enabled = enabled
		// RMW: copy every stored route of the model, then stamp Enabled.
		if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
			return fmt.Errorf("save: %w", err)
		}
	}

	slog.Info("models enable-state", "provider", provider, "model", modelID, "state", label, "scope", scope, "routes", len(targets))
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %s/%s in %s catalog (every route)\n", label, provider, modelID, scope)
	return nil
}

func enabledStateLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func catalogEntryFromTestResult(ctx context.Context, cfg ai.AIConfig, vaultRoot string, result *ai.TestProbeResult) ai.ModelInfo {
	// The base row must be the one for the ROUTE that was probed.
	//
	// findModelInfo matches (provider, id) and returns the FIRST row, which
	// among a model's routes is arbitrary (sortModels sorts by provider,
	// type, id with a non-stable sort). A wrong base carries a non-empty
	// Region, and since persistProbedRegion and AdoptRoutingHints are both
	// fill-only-empty, neither corrects it — so the verdict was written under
	// a route key that was never probed, destroying that endpoint's good
	// result and leaving the endpoint that actually failed still reading as
	// last-known-good, which PreferRoutes then ranks first.
	//
	// Harmless before routes (one row per model); destructive after.
	probed := ai.RouteKey{
		Provider: result.Provider,
		ID:       result.ModelID,
		Plane:    result.Plane,
		Region:   result.Region,
	}
	var base *ai.ModelInfo
	if current, ok := findModelInfoForRoute(ctx, cfg, vaultRoot, probed); ok {
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
		// Built from the result, NOT copied from `base`. The old wholesale copy
		// (`entry = *base`) carried every field of a MERGED row into the user
		// file: the builtin's similarity threshold, its name, its dimensions,
		// its context length, its notes and its curation. A failing probe must
		// mint none of that. Only the ROUTE is taken from the base row, and
		// only as a starting point: persistProbedRegion overwrites plane and
		// region from the result below, and preserveRoutingFields restores the
		// stored invoke strategy and endpoint at the save site.
		entry = ai.ModelInfo{
			ID:             result.ModelID,
			Provider:       result.Provider,
			Type:           result.Type,
			Tier:           ai.TierUnverified,
			Plane:          base.Plane,
			Region:         base.Region,
			InvokeStrategy: base.InvokeStrategy,
			Endpoint:       base.Endpoint,
		}
		carryNonBuiltinFacts(&entry, base)
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
	persistProbedRegion(&entry, result, ai.ResolveBedrockConfig(cfg.Bedrock).Region)
	return entry
}

// preserveRoutingFields carries user-authored routing metadata (invoke
// strategy, endpoint, mantle region pin) from the existing scope entry through
// a probe save. It used to rescue ContextLen too, which meant rescuing whatever
// an older save had copied off the builtin; the context length now has exactly
// one owner on this path, preserveUserFacts, and only when the user stamped it. SaveUserCatalogEntry replaces wholesale,
// and the merged row a save is built from can lose these fields — observed
// live 2026-08-20 when `models test --save` stripped a hand-added mantle
// entry's invoke_strategy, leaving a row that had just PASSED its probe
// classified statically incompatible. Region and Plane are preserved like the
// rest now: they are the row's IDENTITY rather than a mutable pin, and
// persistProbedRegion overwrites them from the probe result anyway, which is
// the authoritative statement of which endpoint was actually called.
// preserveUserThreshold is the one place the rule lives: a row written by a
// probe, a benchmark, or a promotion carries the USER's similarity threshold or
// none at all, never the builtin catalog's recommendation.
//
// Every such row is seeded from the merged catalog (builtin overlaid by the user
// file), so before this the field rode along into the user file. `models bench`
// defaults to --summary-scope global, so one bench run in one vault made every
// vault on the machine report "(user calibration)" for a number nobody measured,
// and the frozen copy shadowed any later builtin correction. `models calibrate
// --save` and `models add --similarity-threshold` remain the only authors.
//
// The lookup is scope-local and route-tolerant (UserCatalogRouteToPreserve falls
// back to a unique row for the model), so a calibration stored on a pre-route
// row, or under a sibling region, is carried forward when the stored row is
// unique, and is NEVER erased when it is not. With two stored routes the
// fallback deliberately returns nothing rather than grafting one endpoint's
// calibration onto another, and SaveUserCatalogEntry replaces only on an exact
// route match, so the ambiguous case appends a row with no threshold and leaves
// every calibrated row exactly as it was.
func preserveUserThreshold(scope ai.UserCatalogScope, vaultRoot string, entry *ai.ModelInfo) {
	entry.RecommendedSimilarityThreshold, entry.ThresholdSource = 0, ""
	existing, ok := ai.UserCatalogRouteToPreserve(scope, vaultRoot, entry.Route())
	if !ok || !ai.IsUserThreshold(existing) {
		return
	}
	entry.RecommendedSimilarityThreshold = existing.RecommendedSimilarityThreshold
	entry.ThresholdSource = existing.ThresholdSource
	slog.Debug("preserve user threshold: carried the stored calibration",
		"provider", entry.Provider, "model", entry.ID, "scope", scope,
		"threshold", entry.RecommendedSimilarityThreshold, "source", entry.ThresholdSource)
}

// preserveUserFacts carries, from the RAW stored user row for this route, the
// things a probe save must not invent and cannot rebuild: the model facts the
// user typed (stamped ai.FactSourceUser by `models add --name/--dimensions/
// --context-length`), their price override, their notes, and the benchmark
// summary already recorded against the route.
//
// It is the fact-side twin of preserveUserThreshold, and exists for the same
// reason: SaveUserCatalogEntry replaces WHOLESALE, so a field the probe row
// does not carry is deleted from the user file. Building the row from the
// merged catalog instead is what made a builtin fact look authored, so the
// only base a save may inherit from is the stored row itself. The lookup is
// UserCatalogRouteToPreserve, so a value stored on a pre-route row or under a
// sibling region is carried forward when the stored row is UNIQUE, and is never
// grafted from one endpoint onto another when it is not.
//
// An UNSTAMPED stored fact is carried only for a model the builtin catalog has
// never declared. For a BUILTIN model it is a copy an older save took off the
// merged view, so letting it survive would re-freeze the very snapshot this
// exists to clear: dropping it is the write-side half of the self-heal, and the
// builtin supplies the value again on read. For a model no builtin declares
// there is nothing to fall back to, so the stored row is the only copy and
// dropping it would destroy a value the user cannot get back.
func preserveUserFacts(scope ai.UserCatalogScope, vaultRoot string, entry *ai.ModelInfo) {
	existing, ok := ai.UserCatalogRouteToPreserve(scope, vaultRoot, entry.Route())
	if !ok {
		return
	}
	var carried []string
	stamped := existing.FactSource == ai.FactSourceUser
	if stamped || findBuiltinModel(entry.Provider, entry.ID) == nil {
		if existing.Name != "" {
			entry.Name = existing.Name
			carried = append(carried, "name")
		}
		if existing.Dimensions != 0 {
			entry.Dimensions = existing.Dimensions
			carried = append(carried, "dimensions")
		}
		if existing.ContextLen != 0 {
			entry.ContextLen = existing.ContextLen
			carried = append(carried, "context_len")
		}
		if stamped && len(carried) > 0 {
			entry.FactSource = ai.FactSourceUser
		}
	}
	// Prices only when the user OVERRODE them. A vendor or builtin price is
	// re-derived on read, and carrying it forward is the same mirroring.
	if existing.PriceSource == "user" {
		entry.PriceIn, entry.PriceOut, entry.PriceRequest = existing.PriceIn, existing.PriceOut, existing.PriceRequest
		entry.PriceSource, entry.PriceOverride = existing.PriceSource, existing.PriceOverride
		carried = append(carried, "prices")
	}
	if entry.Notes == "" && existing.Notes != "" {
		entry.Notes = existing.Notes
		carried = append(carried, "notes")
	}
	// A benchmark is a measurement against THIS route. A probe that failed must
	// not erase it; only a newer bench run replaces it.
	if entry.Benchmark == nil && existing.Benchmark != nil {
		b := *existing.Benchmark
		entry.Benchmark = &b
		carried = append(carried, "benchmark")
	}
	if len(carried) > 0 {
		slog.Debug("preserve user facts: carried from the stored scope entry",
			"provider", entry.Provider, "model", entry.ID, "scope", scope,
			"fields", strings.Join(carried, ","))
	}
}

func preserveRoutingFields(scope ai.UserCatalogScope, vaultRoot string, entry *ai.ModelInfo) {
	// Resolve by route where the entry knows it, else by a UNIQUE row for the
	// model. A probe result turned into a save often carries no plane yet, so
	// an exact-route lookup would find nothing and the routing this function
	// exists to rescue would be lost. Where several routes are stored the
	// resolver deliberately returns nothing rather than grafting one
	// endpoint's routing onto another.
	existing, ok := ai.UserCatalogRouteToPreserve(scope, vaultRoot, entry.Route())
	if !ok {
		slog.Debug("preserve routing fields: no unique existing scope entry, nothing to carry",
			"provider", entry.Provider, "model", entry.ID, "scope", scope)
		return
	}
	var carried []string
	if entry.Plane == "" && existing.Plane != "" {
		entry.Plane = existing.Plane
		carried = append(carried, "plane")
	}
	if entry.InvokeStrategy == "" && existing.InvokeStrategy != "" {
		entry.InvokeStrategy = existing.InvokeStrategy
		carried = append(carried, "invoke_strategy")
	}
	if entry.Endpoint == "" && existing.Endpoint != "" {
		entry.Endpoint = existing.Endpoint
		carried = append(carried, "endpoint")
	}
	if entry.Region == "" && existing.Region != "" && entry.InvokeStrategy == ai.StrategyBedrockMantleResponses {
		entry.Region = existing.Region
		carried = append(carried, "region")
	}
	if len(carried) > 0 {
		slog.Debug("preserve routing fields: carried from existing scope entry",
			"provider", entry.Provider, "model", entry.ID, "scope", scope,
			"fields", strings.Join(carried, ","))
	}
}

// adoptCandidateRouting fills discovery-time routing hints from the probed
// candidate into the entry being saved, so a passing probe of a
// mantle-DISCOVERED model persists invoke_strategy + region and the ordinary
// resolvers route every future invoke. It must run AFTER
// preserveRoutingFields at each save site: an existing user-catalog entry
// always beats the discovery hint. Region is adopted only for
// mantle-strategy entries — classic Region is owned by persistProbedRegion
// (a primary-region pass must clear a stale pin), and the two owners must
// never fight over the same field.
func adoptCandidateRouting(entry *ai.ModelInfo, candidate ai.ModelInfo) {
	// Thin wrapper over the shared rule (ai.AdoptRoutingHints), which the
	// discovery merge graft also uses, so the save path and the merge can
	// never disagree about what "adopt the hints" means.
	ai.AdoptRoutingHints(entry, candidate)
}

// persistProbedRegion stamps the row with the region the probe actually used,
// so the verdict is recorded against the endpoint that produced it.
//
// It no longer clears the region on a primary-region pass. That clearing was
// the old self-heal: region was a mutable pin, so blanking it returned the
// model to the default route. Under route identity it does the opposite of
// what it says. Region is part of the key, so a save with the region cleared
// writes a SECOND row rather than replacing the pinned one, and the stale
// pinned row then WINS, because dropSupersededUnpinned retires the unpinned
// one as a template. A user regaining primary-region access would have been
// permanently stuck on the fallback region with no command to clear it.
//
// The self-heal still happens, one level up: every verify re-probes the
// primary region first (regionAttempts), and a pass there now records a
// verdict on the primary route's own row, which PreferRoutes ranks above its
// siblings. Recording the truth about each endpoint replaces mutating one
// shared field.
//
// Mantle rows keep their endpoint region, which the probe reports unchanged.
//
// The PLANE is stamped alongside the region, because the two together are the
// route key the verdict is saved under. Stamping only the region would leave a
// row whose key differs from the endpoint that was actually probed.
func persistProbedRegion(entry *ai.ModelInfo, result *ai.TestProbeResult, primaryRegion string) {
	if result.Provider != "bedrock" {
		return
	}
	// AUTHORITATIVE, not fill-only-empty. The probe result is by definition
	// the truth about which endpoint was called, so it must overwrite whatever
	// route the base row carried.
	//
	// Fill-only-empty was wrong here in a way that destroyed state. The base
	// row comes from a lookup that falls back to (provider, id) first-match
	// when the exact route is absent, and on a FAILED probe
	// catalogEntryFromTestResult copies that row wholesale (`entry = *base`),
	// route fields included. A non-empty stale Region then blocked its own
	// correction, so a failed probe of us-east-1 was saved onto the
	// us-east-2 row: the good endpoint's pass was overwritten with a failure
	// it never had, no row was created for the endpoint that actually failed,
	// and since access_denied is region-retryable, routeDemoted then sorted
	// the previously-working route last.
	if result.Region != "" {
		entry.Region = result.Region
	}
	if result.Plane != "" {
		entry.Plane = result.Plane
	}
	// The envelope too. preserveRoutingFields can graft a SIBLING's
	// invoke_strategy onto the row (it is fill-only-empty, and the sibling is
	// the only match when the exact route is absent), which left classic rows
	// carrying bedrock_mantle_responses. Harmless for dispatch today, since
	// NewBedrockGenerationForRoute switches on plane and ignores Strategy, but
	// it is a lie in the catalog that never self-heals.
	if result.Strategy != "" {
		entry.InvokeStrategy = result.Strategy
	} else if result.Plane == ai.PlaneClassic && entry.InvokeStrategy == ai.StrategyBedrockMantleResponses {
		entry.InvokeStrategy = ""
	}
}

func findModelInfo(ctx context.Context, cfg ai.AIConfig, vaultRoot, provider, id string) (ai.ModelInfo, bool) {
	return findModelInfoForRoute(ctx, cfg, vaultRoot, ai.RouteKey{Provider: provider, ID: id})
}

// findModelInfoForRoute returns the catalog row for a specific ROUTE, falling
// back to any row of that model when the route names no plane or region.
//
// The exact-route pass has to come first and has to be exact. Matching on
// (provider, id) and taking the first hit is arbitrary among a model's routes
// (sortModels uses a non-stable sort keyed on provider/type/id), so a caller
// saving a probe verdict could pick up a sibling endpoint's row and write the
// result under a route that was never probed.
func findModelInfoForRoute(ctx context.Context, cfg ai.AIConfig, vaultRoot string, route ai.RouteKey) (ai.ModelInfo, bool) {
	list, err := ai.BuildModelList(ctx, ai.MergedListOptions{
		Config:    cfg,
		VaultRoot: vaultRoot,
	})
	var pools [][]ai.ModelInfo
	if err == nil {
		pools = append(pools, list.Verified, list.Unverified)
	}
	pools = append(pools, ai.LoadUserCatalog(vaultRoot))

	if route.Plane != "" || route.Region != "" {
		want := route
		for _, pool := range pools {
			for _, m := range pool {
				if m.Route() == want {
					return m, true
				}
			}
		}
	}
	for _, pool := range pools {
		for _, m := range pool {
			if m.Provider == route.Provider && m.ID == route.ID {
				return m, true
			}
		}
	}
	return ai.ModelInfo{}, false
}

// promotedEntry builds a user-catalog ModelInfo from a passing probe result.
// Tier and TestedAt always come from the promotion; base supplies model facts
// only for a model the builtin catalog has never declared (see
// carryNonBuiltinFacts). What the USER authored is restored at the save site by
// preserveUserFacts and preserveUserThreshold, which know the target scope.
func promotedEntry(base *ai.ModelInfo, result *ai.TestProbeResult) ai.ModelInfo {
	entry := ai.ModelInfo{
		ID:            result.ModelID,
		Provider:      result.Provider,
		Type:          result.Type,
		Tier:          ai.TierUserVerified,
		TestedAt:      time.Now().UTC().Format(time.RFC3339),
		TestLatencyMs: latencyMs(result.Latency),
	}
	carryNonBuiltinFacts(&entry, base)
	// For embedding models with no dimension info, parse actual dims from the
	// probe result detail ("dims=1024") so promoted entries carry accurate
	// metadata. For a BUILTIN model entry.Dimensions is deliberately left at
	// zero above and the builtin fills it back in at read time, so this only
	// fires where the probe really is the only source.
	if result.Type == "embedding" && entry.Dimensions == 0 && findBuiltinModel(result.Provider, result.ModelID) == nil {
		if d := parseDimsFromDetail(result.Detail); d > 0 {
			entry.Dimensions = d
		}
	}
	return entry
}

// carryNonBuiltinFacts copies the model facts off the probe's base row, but
// ONLY for a model the builtin catalog has never declared.
//
// `base` comes from the MERGED catalog, so for a builtin model those facts ARE
// the builtin's. Persisting them freezes a snapshot in the user file that
// shadows every later builtin correction: a context_length of 2048 outlived the
// builtin's own 8192, kept `models list` reporting the stale number, and spread
// to every per-region row discovery found. It is the same lesson
// RecommendedSimilarityThreshold taught, which is why that field has never been
// carried here. Leaving them zero loses nothing, because the builtin supplies
// them again on every read. For a model no builtin declares there is no other
// source, so dropping them would leave the row nameless and unpriced.
func carryNonBuiltinFacts(entry *ai.ModelInfo, base *ai.ModelInfo) {
	if base == nil || findBuiltinModel(entry.Provider, entry.ID) != nil {
		return
	}
	entry.Name = base.Name
	entry.Dimensions = base.Dimensions
	entry.ContextLen = base.ContextLen
	entry.PriceIn, entry.PriceOut, entry.PriceRequest = base.PriceIn, base.PriceOut, base.PriceRequest
	entry.Notes = base.Notes
	switch {
	case base.PriceSource != "":
		entry.PriceSource = base.PriceSource
	case base.PriceIn > 0 || base.PriceOut > 0 || base.PriceRequest > 0:
		entry.PriceSource = "vendor"
	}
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
