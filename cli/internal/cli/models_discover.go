package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	discoverRefresh  bool
	discoverAdd      []string
	discoverValidate bool
	discoverYes      bool
	discoverScope    string
	discoverCostCap  float64
)

var modelsDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "List vendor-discovered models with cache age and a NEW/GONE diff",
	Long: `Discovery as a verb: enumerate every model the vendor planes list that is
not yet in your catalog, say how old each cached listing is, and call out
what is NEW (or gone) since the last check.

The default run reads through the 24h discovery cache (a cold or stale cache
triggers a live walk that re-warms it) and reports per-source ages: the
classic Bedrock control-plane listing per included region, and the mantle
plane's /v1/models listing per documented mantle region. --refresh drops the
cached listings first, forcing a live walk now.

The NEW/GONE diff compares against a machine-local seen baseline
(discovery-seen-bedrock-<profile>.json in the discovery cache dir; not the
vault, so a synced vault can never mis-badge models on another machine). The
first run seeds the baseline silently: it reports the pool with no NEW badge.

--add <id> persists a discovered row into the user catalog WITH its routing
(invoke strategy + region), so an explicit mantle-listed id stops silently
classic-probing: after --add, plain 2nb models test <id> and every invoke
route over the right plane. The entry stays tier=unverified until a probe
passes. --validate probes the added ids immediately (cost-gated like
2nb models verify; --yes for non-interactive).`,
	Args: cobra.NoArgs,
	RunE: runModelsDiscover,
}

func init() {
	modelsDiscoverCmd.Flags().BoolVar(&discoverRefresh, "refresh", false, "Drop the cached listings and perform a live discovery walk now")
	modelsDiscoverCmd.Flags().StringSliceVar(&discoverAdd, "add", nil, "Persist a discovered model id into the user catalog with its routing (repeatable)")
	modelsDiscoverCmd.Flags().BoolVar(&discoverValidate, "validate", false, "Probe the ids named by --add immediately (cost-gated like models verify)")
	modelsDiscoverCmd.Flags().BoolVar(&discoverYes, "yes", false, "Skip the interactive probe-cost confirmation")
	modelsDiscoverCmd.Flags().StringVar(&discoverScope, "scope", "vault", "Catalog scope for --add and --validate results: vault or global")
	modelsDiscoverCmd.Flags().Float64Var(&discoverCostCap, "cost-cap", 0.50, "Abort --validate if the estimated probe cost exceeds this many USD")
	_ = modelsDiscoverCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)
	modelsCmd.AddCommand(modelsDiscoverCmd)
}

// discoverReport is the --json envelope: per-source cache ages, the full
// discovered pool, the NEW/GONE diff, plus the ids --add persisted and the
// --validate probe results when those flags ran.
type discoverReport struct {
	Sources   []ai.DiscoverySourceAge `json:"sources"`
	Models    []ai.ModelInfo          `json:"models"`
	New       []ai.ModelInfo          `json:"new"`
	Gone      []string                `json:"gone"`
	FirstRun  bool                    `json:"first_run,omitempty"`
	Refreshed bool                    `json:"refreshed,omitempty"`
	Added     []string                `json:"added,omitempty"`
	Results   []*ai.TestProbeResult   `json:"results,omitempty"`
	Warnings  []string                `json:"warnings,omitempty"`
}

func runModelsDiscover(cmd *cobra.Command, args []string) error {
	jsonMode := getFormat(cmd) != ""
	humanMode := !jsonMode
	if discoverValidate && len(discoverAdd) == 0 {
		return exitWithError(ExitValidation, "--validate probes the ids named by --add; pass --add <id> (repeatable)")
	}

	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	scope := ai.UserCatalogScope(discoverScope)
	if _, err := ai.CatalogPathForScope(scope, v.Root); err != nil {
		return err
	}
	ctx := context.Background()

	if discoverRefresh {
		removed, invErr := ai.InvalidateDiscoveryCache(v.Config.AI.Bedrock)
		if invErr != nil {
			return fmt.Errorf("invalidate discovery cache: %w", invErr)
		}
		if humanMode {
			fmt.Printf("Refreshing: dropped %d cached listing(s); walking the vendor planes live...\n", len(removed))
		}
	}

	// Always the CACHED read-through: a fresh cache short-circuits, a cold or
	// stale one triggers the live walk AND re-warms the cache. After
	// --refresh's deletion the cached path IS a guaranteed live walk;
	// DiscoverCached:false would walk live too but leave the cache cold,
	// breaking the age report and forcing the next `verify --discover` to
	// re-walk the control plane.
	merged, err := ai.BuildModelList(ctx, ai.MergedListOptions{
		Config:         v.Config.AI,
		VaultRoot:      v.Root,
		Discover:       true,
		DiscoverCached: true,
	})
	if err != nil {
		return err
	}
	pool := merged.Unverified

	sources := ai.DiscoveryCacheAges(v.Config.AI.Bedrock)
	seen, seenErr := ai.LoadDiscoverySeen(v.Config.AI.Bedrock)
	if seenErr != nil {
		// The baseline is a convenience; a read failure degrades to first-run
		// seeding rather than failing the listing.
		fmt.Fprintf(os.Stderr, "warning: read discovery baseline: %v\n", seenErr)
		seen = nil
	}
	failed := ai.FailedDiscoveryProviders(merged.Warnings)
	diff := ai.DiffAgainstSeen(pool, merged.Verified, seen, failed)

	// Persist the baseline only after a successful listing. An empty pool
	// alongside warnings means discovery itself produced nothing (offline, no
	// credentials); saving then would blow away the baseline and re-announce
	// every model as NEW once discovery recovers.
	if len(pool) > 0 || len(merged.Warnings) == 0 {
		if saveErr := ai.SaveDiscoverySeen(v.Config.AI.Bedrock, ai.NextSeenKeys(pool, seen, failed)); saveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: save discovery baseline: %v\n", saveErr)
		}
	}

	report := discoverReport{
		Sources:   sources,
		Models:    pool,
		New:       diff.New,
		Gone:      diff.Gone,
		FirstRun:  diff.FirstRun,
		Refreshed: discoverRefresh,
		Warnings:  merged.Warnings,
	}
	if report.Models == nil {
		report.Models = []ai.ModelInfo{}
	}

	if humanMode {
		printDiscoverListing(report)
	}

	if len(discoverAdd) > 0 {
		added, addErr := discoverAddModels(v, scope, pool, merged.Verified, discoverAdd, humanMode)
		if addErr != nil {
			return addErr
		}
		for _, m := range added {
			report.Added = append(report.Added, m.ID)
		}
		if discoverValidate {
			results, valErr := discoverValidateModels(ctx, v, scope, added, humanMode)
			if valErr != nil {
				return valErr
			}
			report.Results = results
		}
	}

	if jsonMode {
		return output.Write(os.Stdout, getFormat(cmd), report)
	}
	return nil
}

// discoverAddModels persists each named discovered row into the user catalog
// WITH its routing hints, closing the "explicit mantle ids silently
// classic-probe" gap durably: after the save, catalog resolution routes every
// invoke (models test, generation) over the discovered plane, no --discover
// flag needed. An existing scope entry's authored fields win; only its empty
// routing fields adopt the hints (ai.AdoptRoutingHints, the same fill-only-
// empty rule the verify save path uses). Enabled is deliberately NOT copied
// from the pool row: BuildModelList applies vendor-policy verdicts to
// discovered rows, and persisting one would freeze the policy as a per-model
// override. Tier stays unverified until a probe passes.
func discoverAddModels(v *vault.Vault, scope ai.UserCatalogScope, pool, catalog []ai.ModelInfo, ids []string, humanMode bool) ([]ai.ModelInfo, error) {
	find := func(list []ai.ModelInfo, id string) *ai.ModelInfo {
		for i := range list {
			if list[i].ID == id {
				return &list[i]
			}
		}
		return nil
	}
	// Validate EVERY id before persisting ANY: a per-id validate-then-save
	// loop aborted on the first invalid id with earlier ids already durably
	// written and never mentioned in the error, an all-or-nothing command
	// silently becoming partial (review finding, confidence 92).
	resolved := make([]*ai.ModelInfo, 0, len(ids))
	for _, id := range ids {
		m := find(pool, id)
		if m == nil {
			if find(catalog, id) != nil {
				return nil, exitWithError(ExitValidation, fmt.Sprintf("%s is already in the catalog; probe it with `2nb models verify %s` instead (nothing was added)", id, id))
			}
			return nil, exitWithError(ExitValidation, fmt.Sprintf("%s is not in the discovered pool; run `2nb models discover` to list ids, or --refresh for a live listing (nothing was added)", id))
		}
		resolved = append(resolved, m)
	}
	var added []ai.ModelInfo
	for _, m := range resolved {
		entry, exists := ai.UserCatalogEntry(scope, v.Root, m.Provider, m.ID)
		if !exists {
			entry = ai.ModelInfo{
				ID:       m.ID,
				Name:     m.Name,
				Provider: m.Provider,
				Type:     m.Type,
				// Not the discovered row's note: that one says "verify with
				// --discover", advice this add just made obsolete by
				// persisting the routing.
				Notes: "added from discovery by 2nb models discover; listing proves existence, not entitlement; probe with 2nb models verify " + m.ID,
			}
		}
		ai.AdoptRoutingHints(&entry, *m)
		if entry.Tier == "" {
			entry.Tier = ai.TierUnverified
		}
		if err := ai.SaveUserCatalogEntry(scope, v.Root, entry); err != nil {
			return nil, fmt.Errorf("save %s: %w", m.ID, err)
		}
		added = append(added, *m)
		if humanMode {
			fmt.Printf("Added %s to the %s catalog (route: %s; unverified until a probe passes)\n",
				m.ID, scope, discoverRouteLabel(*m))
		}
	}
	return added, nil
}

// discoverValidateModels probes the just-added ids with the same cost gate as
// `models verify`: estimate, enforce --cost-cap, confirm interactively unless
// --yes, refuse to spend on a non-interactive stdin. Every result, pass and
// fail, persists to the user catalog through the verify save sequence, so
// the STATE column and the working set reflect the outcome immediately.
func discoverValidateModels(ctx context.Context, v *vault.Vault, scope ai.UserCatalogScope, candidates []ai.ModelInfo, humanMode bool) ([]*ai.TestProbeResult, error) {
	candidates = skipUnprobeable(candidates)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("nothing probeable among the added models (rerank and statically incompatible entries have no probe)")
	}

	estimates, totalUSD := ai.EstimateCosts(candidates, ai.ProbeTest)
	unknownPricing := 0
	for _, e := range estimates {
		if !e.KnownPricing {
			unknownPricing++
		}
	}
	if totalUSD > discoverCostCap {
		return nil, fmt.Errorf("estimated probe cost $%.4f exceeds --cost-cap $%.4f (%d models); narrow --add or raise the cap",
			totalUSD, discoverCostCap, len(candidates))
	}
	if humanMode {
		fmt.Printf("Validating %d added model(s): estimated cost $%.4f\n", len(candidates), totalUSD)
		if unknownPricing > 0 {
			fmt.Printf("  note: %d model(s) have unknown pricing and are excluded from the estimate (probes are ~50 tokens each)\n", unknownPricing)
		}
	}
	if !discoverYes && humanMode {
		// Same refusal convention as models verify: a declined or
		// unanswerable confirm exits non-zero so an aborted spend can never
		// read as success.
		if fi, statErr := os.Stdin.Stat(); statErr != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return nil, fmt.Errorf("refusing to spend without confirmation on a non-interactive stdin; pass --yes")
		}
		fmt.Print("Proceed? [y/N] ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			return nil, fmt.Errorf("aborted: not confirmed (answer y, or pass --yes)")
		}
	}

	regions := ai.ResolveBedrockRegions(v.Config.AI.Bedrock)
	results := make([]*ai.TestProbeResult, 0, len(candidates))
	total := len(candidates)
	probeModelsConcurrentlyRegions(ctx, v.Config.AI, v.Root, candidates, regions, func(n int, m ai.ModelInfo, result *ai.TestProbeResult, err error) {
		if err != nil {
			result = &ai.TestProbeResult{
				ModelID: m.ID, Provider: m.Provider, Type: m.Type,
				OK: false, Detail: err.Error(), Code: ai.TestErrUnknown,
			}
		}
		entry := catalogEntryFromTestResult(ctx, v.Config.AI, v.Root, result)
		entry.Recommended = m.Recommended
		entry.Enabled = preserveScopeEnabled(scope, v.Root, entry.Provider, entry.ID)
		preserveRoutingFields(scope, v.Root, &entry)
		adoptCandidateRouting(&entry, m)
		if saveErr := ai.SaveUserCatalogEntry(scope, v.Root, entry); saveErr != nil && humanMode {
			fmt.Printf("[%d/%d] warning: save %s failed: %v\n", n, total, m.ID, saveErr)
		}
		results = append(results, result)
		if humanMode {
			if result.OK {
				fmt.Printf("[%d/%d] PASS  %s  (%s)\n", n, total, result.ModelID, result.Latency)
			} else {
				code := string(result.Code)
				if code == "" {
					code = "failed"
				}
				fmt.Printf("[%d/%d] FAIL  %s  [%s]\n", n, total, result.ModelID, code)
				if result.Remediation != "" {
					fmt.Printf("       fix: %s\n", result.Remediation)
				}
			}
		}
	})
	return results, nil
}

// printDiscoverListing renders the human report: source ages, the discovered
// pool, and the NEW/GONE diff. Warnings go to stderr so piped stdout stays a
// clean listing.
func printDiscoverListing(report discoverReport) {
	for _, w := range report.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	fmt.Println("Discovery sources (24h cache):")
	for _, s := range report.Sources {
		fmt.Printf("  %s %s: %s\n", s.Source, s.Region, formatSourceAge(s))
	}
	if !report.Refreshed {
		fmt.Println("  refresh with: 2nb models discover --refresh")
	}
	fmt.Println()

	if len(report.Models) == 0 {
		fmt.Println("No discovered models outside your catalog.")
		return
	}

	fmt.Printf("Discovered models not yet in your catalog (%d):\n", len(report.Models))
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tROUTE")
	for _, m := range report.Models {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Provider, m.Type, m.ID, discoverRouteLabel(m))
	}
	w.Flush()
	fmt.Println()

	switch {
	case report.FirstRun:
		fmt.Printf("First run: recorded %d model(s) as the baseline; future runs flag NEW arrivals.\n", len(report.Models))
	case len(report.New) == 0 && len(report.Gone) == 0:
		fmt.Println("No changes since the last check.")
	default:
		if len(report.New) > 0 {
			fmt.Printf("NEW since last check (%d):\n", len(report.New))
			for _, m := range report.New {
				fmt.Printf("  %s  (%s, %s)\n", m.ID, m.Provider, discoverRouteLabel(m))
			}
			fmt.Println("  add one durably (persists routing so it probes on the right plane):")
			fmt.Println("    2nb models discover --add <id> --validate")
		}
		if len(report.Gone) > 0 {
			fmt.Printf("Gone from discovery since last check (%d):\n", len(report.Gone))
			for _, k := range report.Gone {
				fmt.Printf("  %s\n", k)
			}
		}
	}
}

// discoverRouteLabel names the plane a discovered row would invoke over:
// "mantle <region>" for hint-carrying mantle listings, "classic" for
// control-plane Bedrock rows, the provider's own path otherwise.
func discoverRouteLabel(m ai.ModelInfo) string {
	if m.InvokeStrategy == ai.StrategyBedrockMantleResponses {
		if m.Region != "" {
			return "mantle " + m.Region
		}
		return "mantle"
	}
	if m.Provider == "bedrock" {
		return "classic"
	}
	return m.Provider
}

// formatSourceAge renders one source's freshness: "just now", "3h ago",
// "stale (26h)" past the 24h TTL, or "not cached (live walk on next use)".
func formatSourceAge(a ai.DiscoverySourceAge) string {
	if !a.Exists {
		return "not cached (live walk on next use)"
	}
	age := a.Age()
	if a.Stale {
		return fmt.Sprintf("stale (%s)", compactAge(age))
	}
	if age < time.Minute {
		return "just now"
	}
	return compactAge(age) + " ago"
}

func compactAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
