package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	verifyProvider    string
	verifyVendor      string
	verifyRecommended bool
	verifyAll         bool
	verifyScope       string
	verifyCostCap     float64
	verifyYes         bool
	verifyEnabledOnly bool
	verifyEvents      bool
	verifyDiscover    bool
	verifyMaxDuration time.Duration
)

var modelsVerifyCmd = &cobra.Command{
	Use:   "verify [model-id...]",
	Short: "Batch-probe models to verify YOUR account can invoke them",
	Long: `Runs a real test probe against each candidate model and records every
result (pass AND fail) in the user catalog with a classified failure code, so
"which models can this account actually use?" has a durable answer.

This is the check that catches AWS's staged frontier-model rollout: Bedrock can
list a model as available (and the console can show access as granted) while
bedrock-runtime still returns 403 for your account. Only a real invoke probe
detects that; availability APIs cannot be trusted for it.

Candidates default to the curated recommended models plus your active models,
restricted to providers whose credentials resolve. Narrow with --provider /
--vendor / --recommended, broaden with --all, or name explicit model IDs.
Probes cost fractions of a cent; a cost preview gates the run.`,
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completeModelIDs,
	RunE:              runModelsVerify,
}

func init() {
	modelsVerifyCmd.Flags().StringVar(&verifyProvider, "provider", "", "Restrict to one provider: bedrock, openrouter, ollama, llama-local")
	modelsVerifyCmd.Flags().StringVar(&verifyVendor, "vendor", "", "Restrict to one vendor (e.g. anthropic, amazon, google)")
	modelsVerifyCmd.Flags().BoolVar(&verifyRecommended, "recommended", false, "Probe only the curated recommended models")
	modelsVerifyCmd.Flags().BoolVar(&verifyAll, "all", false, "Probe every catalog model (verified + user catalog)")
	modelsVerifyCmd.Flags().StringVar(&verifyScope, "scope", "vault", "Catalog scope for saved results: vault or global")
	modelsVerifyCmd.Flags().Float64Var(&verifyCostCap, "cost-cap", 0.50, "Abort if the estimated probe cost exceeds this many USD")
	modelsVerifyCmd.Flags().BoolVar(&verifyYes, "yes", false, "Skip the interactive confirmation")
	modelsVerifyCmd.Flags().BoolVar(&verifyEnabledOnly, "enabled-only", false, "Restrict candidates to effectively-enabled models (post-policy; explicit model IDs still win)")
	modelsVerifyCmd.Flags().BoolVar(&verifyEvents, "events", false, "Stream line-delimited JSON progress events to stdout (requires --yes; mutually exclusive with --json)")
	modelsVerifyCmd.Flags().BoolVar(&verifyDiscover, "discover", false, "Also probe vendor-discovered models (policy-enabled discoveries), not just the merged catalog")
	modelsVerifyCmd.Flags().DurationVar(&verifyMaxDuration, "max-duration", 30*time.Minute, "Wall-clock cap for the whole verify run (0 disables). Individual probes keep their transport-derived deadlines; this only stops a runaway batch")
	_ = modelsVerifyCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsVerifyCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)
	modelsCmd.AddCommand(modelsVerifyCmd)
}

// verifyReport is the --json envelope.
type verifyReport struct {
	Probe      string                `json:"probe"`
	Results    []*ai.TestProbeResult `json:"results"`
	Summary    map[string]int        `json:"summary"`
	SavedScope string                `json:"saved_scope"`
	// CutOff counts probes interrupted by --max-duration: reported in
	// Results (code timeout, summary bucket "cancelled") but their verdicts
	// were NOT persisted to the catalog. Additive.
	CutOff int `json:"cut_off,omitempty"`
}

// verifyEvent is one line-delimited JSON progress event on stdout in
// --events mode (same NDJSON pattern as benchEvent): a "start" header with
// the candidate count and cost estimate, one "result" per completed probe,
// and a final "done" carrying the outcome summary. The GUI streams these to
// render a live validation pass; the --json verifyReport envelope stays
// byte-compatible and mutually exclusive with this mode.
type verifyEvent struct {
	Event        string              `json:"event"`
	N            int                 `json:"n,omitempty"`
	Total        int                 `json:"total"`
	EstimatedUSD float64             `json:"estimated_usd,omitempty"`
	Result       *ai.TestProbeResult `json:"result,omitempty"`
	Summary      map[string]int      `json:"summary,omitempty"`
	SavedScope   string              `json:"saved_scope,omitempty"`
	// Regions is the included region set on the "start" event (additive;
	// primary first). Absent on single-region runs from older configs.
	Regions []string `json:"regions,omitempty"`
	// CutOff on the "done" event counts probes interrupted by
	// --max-duration (reported, never persisted). Additive.
	CutOff int `json:"cut_off,omitempty"`
}

func emitVerifyEvent(enc *json.Encoder, e verifyEvent) {
	if enc != nil {
		_ = enc.Encode(e)
	}
}

func runModelsVerify(cmd *cobra.Command, args []string) error {
	jsonMode := getFormat(cmd) != ""
	if verifyEvents && jsonMode {
		return exitWithError(ExitValidation, "--events and --json are mutually exclusive: --events already streams line-delimited JSON")
	}
	if verifyEvents && !verifyYes {
		// --events is a machine-streaming mode with no TTY to confirm on, so
		// the spend must be pre-authorized (same refusal convention as the
		// non-interactive-stdin gate below).
		return exitWithError(ExitValidation, "refusing to spend without confirmation: --events is non-interactive, pass --yes")
	}

	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	ctx := context.Background()
	scope := ai.UserCatalogScope(verifyScope)
	if _, err := ai.CatalogPathForScope(scope, v.Root); err != nil {
		return err
	}

	var enc *json.Encoder
	if verifyEvents {
		enc = json.NewEncoder(os.Stdout)
	}

	candidates, err := verifyCandidates(ctx, v.Config.AI, v.Root, args)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		if verifyEvents {
			// A zero-candidate set is a legitimate streamed outcome (e.g.
			// --enabled-only with everything disabled, or no resolvable
			// credentials), not an error: the GUI still gets a decodable
			// start+done pair and spends nothing.
			emitVerifyEvent(enc, verifyEvent{Event: "start", Total: 0})
			emitVerifyEvent(enc, verifyEvent{Event: "done", Total: 0, Summary: map[string]int{}, SavedScope: string(scope)})
			return nil
		}
		return fmt.Errorf("no candidate models to verify (check --provider/--vendor filters, or name model IDs)")
	}

	// Cost gate: estimate, enforce the cap, confirm interactively.
	estimates, totalUSD := ai.EstimateCosts(candidates, ai.ProbeTest)
	unknownPricing := 0
	for _, e := range estimates {
		if !e.KnownPricing {
			unknownPricing++
		}
	}
	if totalUSD > verifyCostCap {
		return fmt.Errorf("estimated probe cost $%.4f exceeds --cost-cap $%.4f (%d models); narrow the set or raise the cap",
			totalUSD, verifyCostCap, len(candidates))
	}
	regions := ai.ResolveBedrockRegions(v.Config.AI.Bedrock)
	humanMode := !jsonMode && !verifyEvents
	if humanMode {
		fmt.Printf("Verifying %d model(s) against your account — estimated cost $%.4f\n", len(candidates), totalUSD)
		if unknownPricing > 0 {
			fmt.Printf("  note: %d model(s) have unknown pricing and are excluded from the estimate (probes are ~50 tokens each)\n", unknownPricing)
		}
		if len(regions) > 1 {
			fmt.Printf("  probing across regions: %s (a refused probe retries in the next region; only a passing probe bills)\n",
				strings.Join(regions, ", "))
		}
	}
	if !verifyYes && humanMode {
		// A declined or unanswerable confirm exits non-zero (same convention
		// as the delete prompt) so scripts and agents can't misread an
		// aborted spend as success. Note /dev/null IS a char device, so the
		// EOF-scan path below is the real guard for redirected stdin.
		if fi, statErr := os.Stdin.Stat(); statErr != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return fmt.Errorf("refusing to spend without confirmation on a non-interactive stdin; pass --yes")
		}
		fmt.Print("Proceed? [y/N] ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			return fmt.Errorf("aborted: not confirmed (answer y, or pass --yes)")
		}
	}

	// Probe concurrently; persist every result, pass and fail, so the catalog
	// records this account's real access state.
	results := make([]*ai.TestProbeResult, 0, len(candidates))
	cutOff := 0 // probes interrupted by --max-duration; reported, never persisted
	total := len(candidates)
	startEvent := verifyEvent{Event: "start", Total: total, EstimatedUSD: totalUSD}
	if len(regions) > 1 {
		startEvent.Regions = regions
	}
	emitVerifyEvent(enc, startEvent)
	// --max-duration is the runaway wall clock for the WHOLE run, the missing
	// cost half of the timeout principle: individual probes keep their
	// strategy-aware deadlines (a slow model is never failed), but a wide
	// --discover run over many gated candidates must still have a ceiling the
	// caller can reason about. 0 disables it. A probe cut off by the cap is
	// reported in the transient stream but its verdict is NOT persisted (see
	// the onResult guard below): the cap stops the run, it must not rewrite
	// what the catalog knows about models it merely interrupted.
	probeCtx := ctx
	if verifyMaxDuration > 0 {
		var cancelProbes context.CancelFunc
		probeCtx, cancelProbes = context.WithTimeout(ctx, verifyMaxDuration)
		defer cancelProbes()
	}
	probeModelsConcurrentlyRegions(probeCtx, v.Config.AI, v.Root, candidates, regions, func(n int, m ai.ModelInfo, result *ai.TestProbeResult, err error) {
		if err != nil {
			// Hard errors (cannot infer provider etc.) become synthetic failed
			// results so the report stays complete.
			result = &ai.TestProbeResult{
				ModelID: m.ID, Provider: m.Provider, Type: m.Type,
				OK: false, Detail: err.Error(), Code: ai.TestErrUnknown,
			}
		}
		// A timeout that lands after the batch cap fired is the cap's doing,
		// not the model's: --max-duration stops the RUN, it must not poison
		// the catalog with durable working:false verdicts for candidates that
		// were merely cut off or never started (their prior verdicts stand).
		// The transient event stream still reports the row. A genuine
		// per-probe transport timeout that completed before the cap keeps
		// persisting; skipping a save is the safe direction for the rare
		// result racing the cap's edge.
		if probeCtx.Err() != nil && result.Code == ai.TestErrTimeout {
			cutOff++
			results = append(results, result)
			emitVerifyEvent(enc, verifyEvent{Event: "result", N: n, Total: total, Result: result})
			if humanMode {
				fmt.Printf("[%d/%d] %s: cut off by --max-duration (verdict not saved)\n", n, total, m.ID)
			}
			return
		}
		entry := catalogEntryFromTestResult(ctx, v.Config.AI, v.Root, result)
		entry.Recommended = m.Recommended // preserve curation on the saved entry
		entry.Enabled = preserveScopeEnabled(scope, v.Root, entry.Provider, entry.ID)
		preserveRoutingFields(scope, v.Root, &entry)
		adoptCandidateRouting(&entry, m)
		if saveErr := ai.SaveUserCatalogEntry(scope, v.Root, entry); saveErr != nil && humanMode {
			fmt.Printf("[%d/%d] warning: save %s failed: %v\n", n, total, m.ID, saveErr)
		}
		results = append(results, result)
		// onResult runs under the probe pool's mutex, so the encoder never
		// interleaves partial lines.
		emitVerifyEvent(enc, verifyEvent{Event: "result", N: n, Total: total, Result: result})
		if humanMode {
			if result.OK {
				regionNote := ""
				if result.Region != "" && result.Region != regions[0] {
					regionNote = fmt.Sprintf("  [%s]", result.Region)
				}
				fmt.Printf("[%d/%d] PASS  %s  (%s)%s\n", n, total, result.ModelID, result.Latency, regionNote)
			} else {
				code := string(result.Code)
				if code == "" {
					code = "failed"
				}
				fmt.Printf("[%d/%d] FAIL  %s  [%s]\n", n, total, result.ModelID, code)
			}
		}
	})

	// The machine-readable summary must tell a --max-duration cut-off apart
	// from a genuine per-probe transport timeout: cut-off rows classify as
	// timeout by construction (the pool context expired) but their verdicts
	// were NOT persisted, so leaving them in the timeout bucket would make a
	// JSON/--events consumer (the GUI's summary line) claim probes that never
	// really ran. They move to a distinct "cancelled" bucket, and the count
	// also rides as an additive cut_off field.
	summary := verifySummary(results)
	if cutOff > 0 {
		summary["timeout"] -= cutOff
		if summary["timeout"] <= 0 {
			delete(summary, "timeout")
		}
		summary["cancelled"] = cutOff
	}
	report := verifyReport{
		Probe:      string(ai.ProbeTest),
		Results:    results,
		Summary:    summary,
		SavedScope: string(scope),
		CutOff:     cutOff,
	}

	if verifyEvents {
		emitVerifyEvent(enc, verifyEvent{Event: "done", Total: total, Summary: report.Summary, SavedScope: report.SavedScope, CutOff: cutOff})
		return nil
	}
	if jsonMode {
		return output.Write(os.Stdout, getFormat(cmd), report)
	}

	printVerifyByVendor(results, cutOff)
	return nil
}

// verifyCandidates builds the probe set. Explicit IDs win; otherwise filter
// the merged catalog by --all / --recommended / --vendor / --provider, and
// with no selector at all default to recommended + active models restricted
// to providers whose credentials resolve. --enabled-only narrows the merged
// catalog to effectively-enabled models (post vendor policy, the same filter
// `models list --enabled-only` applies), so "validate what I just enabled"
// probes exactly the dropdown set; an explicitly named ID still wins.
//
// --discover widens the candidate pool to the vendor-discovered (Unverified)
// half of the list. Without it, "validate what I enabled" could not reach a
// model the user enabled through a vendor policy but that has no builtin
// catalog entry: the policy pre-enables future discoveries, and those only
// exist in merged.Unverified. Off by default so the CLI's candidate set is
// unchanged for existing callers.
func verifyCandidates(ctx context.Context, cfg ai.AIConfig, vaultRoot string, args []string) ([]ai.ModelInfo, error) {
	merged, err := ai.BuildModelList(ctx, ai.MergedListOptions{
		Config:      cfg,
		VaultRoot:   vaultRoot,
		EnabledOnly: verifyEnabledOnly,
		Discover:    verifyDiscover,
		// Verify runs on every GUI validation pass, so it reads discovery
		// through the 24h cache rather than re-walking the control plane.
		DiscoverCached: verifyDiscover,
	})
	if err != nil {
		return nil, err
	}
	catalog := verifyCandidatePool(merged, verifyDiscover)

	if len(args) > 0 {
		var out []ai.ModelInfo
		for _, id := range args {
			found := false
			for _, m := range catalog {
				if m.ID == id && (verifyProvider == "" || m.Provider == verifyProvider) {
					out = append(out, m)
					found = true
					break
				}
			}
			if !found {
				provider := verifyProvider
				if provider == "" {
					provider = ai.InferProvider(id)
				}
				if provider == "" {
					return nil, fmt.Errorf("cannot infer provider for %q — pass --provider", id)
				}
				out = append(out, ai.ModelInfo{ID: id, Provider: provider, Type: ai.InferModelType(id), Compatible: true})
			}
		}
		return skipUnprobeable(out), nil
	}

	// --discover counts as a selector, like --all/--recommended/--vendor: the
	// default set keeps only recommended-or-active models, and every
	// discovered model is by definition neither, so leaving the narrowing on
	// would silently drop the whole point of the flag.
	defaultSet := !verifyAll && !verifyRecommended && !verifyDiscover && verifyVendor == ""
	credOK := map[string]bool{}
	if defaultSet {
		for _, s := range probeProviderStatus(ctx, cfg) {
			credOK[s.Name] = s.Available
		}
		// llama-local isn't covered by the wizard's provider status; include
		// it only when it is the active provider.
		credOK["llama-local"] = cfg.Provider == "llama-local"
	}

	var out []ai.ModelInfo
	for _, m := range catalog {
		if verifyProvider != "" && m.Provider != verifyProvider {
			continue
		}
		if verifyVendor != "" && !strings.EqualFold(ai.VendorOf(m.ID, m.Provider).Vendor, verifyVendor) {
			continue
		}
		if verifyRecommended && !m.Recommended {
			continue
		}
		if defaultSet {
			if !credOK[m.Provider] {
				continue
			}
			if !m.Recommended && !m.Active {
				continue
			}
		}
		out = append(out, m)
	}
	return skipUnprobeable(out), nil
}

// verifyCandidatePool is the catalog verify filters over. Without discovery
// that is the merged (builtin + user) catalog. With discovery it also covers
// the vendor-discovered half, which is where a model the user enabled through
// a vendor policy lives until something probes it: BuildModelList has already
// applied the policy verdict and the --enabled-only filter to that slice, so
// a discovery from a non-chosen vendor never reaches here.
func verifyCandidatePool(merged *ai.MergedModelList, discover bool) []ai.ModelInfo {
	if !discover || len(merged.Unverified) == 0 {
		return merged.Verified
	}
	pool := make([]ai.ModelInfo, 0, len(merged.Verified)+len(merged.Unverified))
	pool = append(pool, merged.Verified...)
	pool = append(pool, merged.Unverified...)
	return pool
}

// skipUnprobeable drops rerank models (no probe exists) and entries the static
// compatibility check already rejects; probing them would only add noise.
func skipUnprobeable(models []ai.ModelInfo) []ai.ModelInfo {
	var out []ai.ModelInfo
	for _, m := range models {
		if m.Type == "rerank" || !m.Compatible {
			continue
		}
		out = append(out, m)
	}
	return out
}

// verifySummary buckets results by outcome: "ok", each distinct failure
// code, and "failed" for unclassified failures.
func verifySummary(results []*ai.TestProbeResult) map[string]int {
	summary := map[string]int{}
	for _, r := range results {
		switch {
		case r.OK:
			summary["ok"]++
		case r.Code != "":
			summary[string(r.Code)]++
		default:
			summary["failed"]++
		}
	}
	return summary
}

// printVerifyByVendor renders the per-account access report grouped by vendor
// and family, newest version first, with remediation printed once per
// distinct failure code.
func printVerifyByVendor(results []*ai.TestProbeResult, cutOff int) {
	byVendor := map[string][]*ai.TestProbeResult{}
	for _, r := range results {
		v := ai.VendorOf(r.ModelID, r.Provider)
		name := v.Display
		if name == "" {
			name = r.Provider
		}
		byVendor[name] = append(byVendor[name], r)
	}
	vendors := make([]string, 0, len(byVendor))
	for v := range byVendor {
		vendors = append(vendors, v)
	}
	sort.Strings(vendors)

	remediations := map[ai.TestErrorCode]string{}
	fmt.Println()
	for _, vendor := range vendors {
		fmt.Printf("%s\n", vendor)
		group := byVendor[vendor]
		sort.Slice(group, func(i, j int) bool { return group[i].ModelID < group[j].ModelID })
		for _, r := range group {
			if r.OK {
				fmt.Printf("  PASS  %s  (%s)\n", r.ModelID, r.Latency)
				continue
			}
			code := string(r.Code)
			if code == "" {
				code = "failed"
			}
			fmt.Printf("  FAIL  %s  [%s]\n", r.ModelID, code)
			if r.Code != "" && r.Remediation != "" {
				remediations[r.Code] = r.Remediation
			}
		}
	}
	if len(remediations) > 0 {
		fmt.Println()
		codes := make([]string, 0, len(remediations))
		for c := range remediations {
			codes = append(codes, string(c))
		}
		sort.Strings(codes)
		for _, c := range codes {
			fmt.Printf("fix %s: %s\n", c, remediations[ai.TestErrorCode(c)])
		}
	}
	// The footer must not claim a save for rows the --max-duration cap
	// interrupted: those verdicts are deliberately NOT persisted (the cap
	// stops the run, it does not rewrite the catalog), so an unconditional
	// "Results saved" would contradict the per-row "verdict not saved" lines.
	if cutOff > 0 {
		fmt.Printf("\n%d result(s) saved (%s catalog); %d cut off by --max-duration and NOT saved (prior verdicts stand, re-run to verify them). The STATE column of `2nb models list` reflects the saved results. Verified at %s.\n",
			len(results)-cutOff, verifyScope, cutOff, time.Now().UTC().Format(time.RFC3339))
		return
	}
	fmt.Printf("\nResults saved (%s catalog) — the STATE column of `2nb models list` now reflects your account. Verified at %s.\n",
		verifyScope, time.Now().UTC().Format(time.RFC3339))
}
