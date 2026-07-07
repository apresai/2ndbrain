package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	wizardScope        string
	wizardProvider     string
	wizardSkipDiscover bool
	wizardCostCap      float64
	wizardJSON         bool
	wizardSetActive    bool
)

var modelsWizardCmd = &cobra.Command{
	Use:   "wizard",
	Short: "Discover, test, and save models interactively",
	Long: `Walks through provider discovery, model selection, cost estimation,
and verification, then writes passing models to your catalog. The same
flow powers the GUI wizard when invoked with --json.`,
	RunE: runModelsWizard,
}

func init() {
	modelsWizardCmd.Flags().StringVar(&wizardScope, "scope", "vault",
		"Catalog scope to save verified models to: global or vault")
	modelsWizardCmd.Flags().StringVar(&wizardProvider, "provider", "",
		"Limit to a single provider: bedrock, openrouter, ollama")
	modelsWizardCmd.Flags().BoolVar(&wizardSkipDiscover, "skip-discover", false,
		"Use only the builtin catalog; don't query vendor APIs")
	modelsWizardCmd.Flags().Float64Var(&wizardCostCap, "cost-cap", 0.10,
		"Abort if estimated test cost exceeds this USD value")
	modelsWizardCmd.Flags().BoolVar(&wizardJSON, "json", false,
		"Emit JSON-line events on stdout (non-interactive; uses defaults)")
	modelsWizardCmd.Flags().BoolVar(&wizardSetActive, "set-active", false,
		"After saving passing models, write the chosen embedding + generation models into the vault config (provider, ai.embedding_model, ai.generation_model)")
	_ = modelsWizardCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsWizardCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)
	modelsCmd.AddCommand(modelsWizardCmd)
}

// wizardEvent is the JSON-line shape for GUI consumption. Fields are
// omitempty so each event carries only its relevant payload — the GUI
// switches on Step and renders accordingly.
type wizardEvent struct {
	Step      string            `json:"step"`
	Message   string            `json:"message,omitempty"`
	Providers []providerStatus  `json:"providers,omitempty"`
	Count     int               `json:"count,omitempty"`
	Models    []ai.ModelInfo    `json:"models,omitempty"`
	Estimates []ai.CostEstimate `json:"estimates,omitempty"`
	TotalUSD  float64           `json:"total_usd,omitempty"`
	ModelID   string            `json:"model_id,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Type      string            `json:"type,omitempty"`
	OK        *bool             `json:"ok,omitempty"`
	LatencyMs int64             `json:"latency_ms,omitempty"`
	Error     string            `json:"error,omitempty"`
	// Code classifies a test_result failure (ai.TestErrorCode vocabulary).
	Code string `json:"code,omitempty"`
	Scope     string            `json:"scope,omitempty"`
	Tested    int               `json:"tested,omitempty"`
	Passed    int               `json:"passed,omitempty"`
	Saved     int               `json:"saved,omitempty"`
	// Keys names the config keys written by a set_active event (e.g.
	// ["ai.provider", "ai.embedding_model", "ai.generation_model"]).
	Keys []string `json:"keys,omitempty"`
}

type providerStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

func runModelsWizard(cmd *cobra.Command, args []string) error {
	scope := ai.UserCatalogScope(wizardScope)
	if scope != ai.ScopeGlobal && scope != ai.ScopeVault {
		return fmt.Errorf("invalid --scope %q (use global or vault)", wizardScope)
	}

	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	// Mirror runConfigSet: route slog to .2ndbrain/logs/cli.log so the
	// --set-active config writes below leave the same durable trail a terminal
	// `config set` does (without --verbose, only the file logger sees them).
	setupFileLogging(v)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	events := newWizardEventSink(wizardJSON, cmd.OutOrStdout(), cmd.ErrOrStderr())

	// Step 1: provider reachability.
	status := probeProviderStatus(ctx, v.Config.AI)
	events.emit(wizardEvent{Step: "providers", Providers: status})
	if !hasAnyAvailable(status) {
		return fmt.Errorf("no providers available — configure at least one before running the wizard")
	}

	// Step 2: build the candidate list. IncludeDisabledProviders so the wizard
	// can surface opt-in providers (Ollama/OpenRouter ship disabled) — it's the
	// place to enable them.
	list, err := ai.BuildModelList(ctx, ai.MergedListOptions{
		Config:                   v.Config.AI,
		VaultRoot:                v.Root,
		Discover:                 !wizardSkipDiscover,
		IncludeDisabledProviders: true,
	})
	if err != nil {
		return fmt.Errorf("build model list: %w", err)
	}
	candidates := append([]ai.ModelInfo{}, list.Verified...)
	candidates = append(candidates, list.Unverified...)
	candidates = filterForWizard(candidates, wizardProvider, status)

	events.emit(wizardEvent{
		Step:   "discovered",
		Count:  len(candidates),
		Models: candidates,
	})

	if len(candidates) == 0 {
		return fmt.Errorf("no reachable models to set up. Connect a provider first: run `2nb ai setup` (AWS Bedrock by default), or enable a local/opt-in provider, e.g. `2nb config set ai.ollama.disabled false` after installing Ollama")
	}

	// Step 3: pick models. Easy-mode pre-selects the recommended embedding
	// and generation models; user can change via interactive prompt or
	// accept the defaults (also the only path under --json).
	defaults := easyModeIndices(candidates, v.Config.AI)
	var picked []ai.ModelInfo
	if events.interactive() {
		picked = promptPick(candidates, defaults, events.stderr)
	} else {
		picked = subset(candidates, defaults)
	}
	if len(picked) == 0 {
		return fmt.Errorf("nothing selected; aborting")
	}

	// Step 4: cost preview (gen probe uses the full test budget per model).
	estimates, total := ai.EstimateCosts(picked, ai.ProbeTest)
	events.emit(wizardEvent{
		Step:      "cost_preview",
		Estimates: estimates,
		TotalUSD:  total,
	})
	if total > wizardCostCap {
		return fmt.Errorf("estimated cost $%.4f exceeds --cost-cap $%.4f; pass --cost-cap=<higher> to override",
			total, wizardCostCap)
	}
	if events.interactive() && !promptYesNo(events.stderr, fmt.Sprintf("Test %d models for ~$%.4f?", len(picked), total), true) {
		return fmt.Errorf("aborted by user")
	}

	// Step 5: run tests.
	results := make([]*ai.TestProbeResult, 0, len(picked))
	for _, m := range picked {
		events.emit(wizardEvent{
			Step:     "test_start",
			ModelID:  m.ID,
			Provider: m.Provider,
			Type:     m.Type,
		})
		r, err := ai.TestProbeModel(ctx, v.Config.AI, m.ID, m.Provider, m.Type, v.Root)
		if err != nil {
			events.emit(wizardEvent{
				Step:     "test_result",
				ModelID:  m.ID,
				Provider: m.Provider,
				OK:       ptrBool(false),
				Error:    err.Error(),
			})
			continue
		}
		ok := r.OK
		code := ""
		if !r.OK {
			code = string(r.Code)
		}
		events.emit(wizardEvent{
			Step:      "test_result",
			ModelID:   r.ModelID,
			Provider:  r.Provider,
			Type:      r.Type,
			OK:        &ok,
			LatencyMs: latencyMs(r.Latency),
			Error:     errorDetailOnFail(r),
			Code:      code,
		})
		results = append(results, r)
	}

	// Step 6: save the ones that passed. Track the last passing embedding and
	// generation model (and its provider) so --set-active can promote them to
	// the active config slots below.
	passed := 0
	saved := 0
	var activeEmbed, activeGen, activeProvider string
	for _, r := range results {
		if !r.OK {
			continue
		}
		passed++
		base := findBuiltinModel(r.Provider, r.ModelID)
		entry := promotedEntry(base, r)
		entry.InvokeStrategy = ai.ResolveInvokeStrategy(entry.Provider, entry.ID, v.Root)
		entry.TestLatencyMs = latencyMs(r.Latency)
		if err := ai.SaveUserCatalogEntry(scope, v.Root, entry); err != nil {
			events.emit(wizardEvent{
				Step:    "save_error",
				ModelID: entry.ID,
				Scope:   string(scope),
				Error:   err.Error(),
			})
			continue
		}
		saved++
		switch r.Type {
		case "embedding":
			activeEmbed, activeProvider = r.ModelID, r.Provider
		case "generation":
			activeGen, activeProvider = r.ModelID, r.Provider
		}
		events.emit(wizardEvent{
			Step:     "saved",
			ModelID:  entry.ID,
			Provider: entry.Provider,
			Scope:    string(scope),
		})
	}

	// Step 7: optionally write the chosen models into the active vault config.
	// Opt-in via --set-active; in an interactive TTY we may also prompt. This
	// reuses the EXACT config-write path that `config set ai.provider` /
	// `ai.embedding_model` / `ai.generation_model` takes (provider validation,
	// disabled-flag clear, dimension resync), so the wizard cannot leave the
	// config in a state a manual `config set` couldn't produce.
	if shouldSetActive(events, activeEmbed, activeGen) {
		if err := writeActiveModelConfig(v, activeProvider, activeEmbed, activeGen, events); err != nil {
			// Non-fatal: the catalog saves already succeeded. Surface the failure
			// as an event/warning but still report done.
			events.emit(wizardEvent{Step: "set_active_error", Error: err.Error()})
		}
	}

	events.emit(wizardEvent{
		Step:   "done",
		Tested: len(results),
		Passed: passed,
		Saved:  saved,
	})
	return nil
}

// shouldSetActive decides whether to write the active config. It is gated on
// at least one model having passed (nothing to set otherwise), then: --set-active
// forces it; an interactive TTY (no flag) offers a y/N prompt that defaults to
// no; a non-interactive run without the flag does nothing.
func shouldSetActive(events *wizardEventSink, embed, gen string) bool {
	if embed == "" && gen == "" {
		return false
	}
	if wizardSetActive {
		return true
	}
	if events.interactive() {
		return promptYesNo(events.stderr, "Set these as the active embedding/generation models?", false)
	}
	return false
}

// writeActiveModelConfig writes provider + embedding + generation into the vault
// config via setConfigValue (the same path config set uses), resyncs the
// embedding dimension, saves, then runs Validate and surfaces any issues as
// warnings. It emits a set_active event naming the keys it wrote.
func writeActiveModelConfig(v *vault.Vault, provider, embed, gen string, events *wizardEventSink) error {
	// Track the key/value pairs written so we can both emit the set_active event
	// and slog each one (after a successful Save) the way runConfigSet does.
	type kv struct{ key, value string }
	var written []kv
	if provider != "" {
		if err := setConfigValue(&v.Config.AI, "ai.provider", provider); err != nil {
			return fmt.Errorf("set ai.provider: %w", err)
		}
		written = append(written, kv{"ai.provider", provider})
	}
	if embed != "" {
		if err := setConfigValue(&v.Config.AI, "ai.embedding_model", embed); err != nil {
			return fmt.Errorf("set ai.embedding_model: %w", err)
		}
		written = append(written, kv{"ai.embedding_model", embed})
		// Mirror runConfigSet: a model switch must resync ai.dimensions or it is
		// a silent DIMENSION BREAK.
		resyncEmbeddingDimensions(v, embed, events.stderr)
	}
	if gen != "" {
		if err := setConfigValue(&v.Config.AI, "ai.generation_model", gen); err != nil {
			return fmt.Errorf("set ai.generation_model: %w", err)
		}
		written = append(written, kv{"ai.generation_model", gen})
	}
	if len(written) == 0 {
		return nil
	}
	if err := v.Config.Save(v.DotDir); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	// Log each write with the SAME "config set" message + key/value attrs as
	// runConfigSet, adding a source attr so "what changed my active model?" is
	// answerable from cli.log whether the change came via `config set` or the
	// wizard. Logged only after Save succeeds so the log never claims a write
	// that didn't persist.
	keys := make([]string, 0, len(written))
	for _, w := range written {
		slog.Info("config set", "key", w.key, "value", w.value, "source", "models wizard")
		keys = append(keys, w.key)
	}
	events.emit(wizardEvent{
		Step:     "set_active",
		Provider: v.Config.AI.Provider,
		Keys:     keys,
	})
	// Surface any inconsistency the write introduced (e.g. only an embedding
	// model passed, leaving a generation slot pointing at a different provider).
	// Warn, do not fail: the catalog saves already happened and the partial
	// write is still an improvement.
	for _, issue := range v.Config.AI.Validate(v.Root) {
		slog.Warn("ai config inconsistency", "issue", issue, "source", "models wizard")
		events.emit(wizardEvent{Step: "set_active_warning", Message: issue})
	}
	return nil
}

// probeProviderStatus returns reachability for the three supported
// providers. The wizard uses this both for filtering candidates and
// for the providers step event.
func probeProviderStatus(ctx context.Context, cfg ai.AIConfig) []providerStatus {
	// Quick, cheap probes: check for creds/endpoint configuration, not
	// live API health. Live reachability is exercised by the test step.
	out := []providerStatus{
		// Bedrock has no API key — readiness is whether AWS credentials actually
		// resolve, not HasAPIKey("bedrock") (which always returns true).
		{Name: "bedrock", Available: ai.CheckBedrockCredentials(ctx, cfg.Bedrock)},
		{Name: "openrouter", Available: ai.HasAPIKey("openrouter")},
		{Name: "ollama", Available: true}, // local; reachability deferred to test
	}
	for i := range out {
		if !out[i].Available {
			out[i].Reason = "credentials or endpoint not configured"
		}
	}
	return out
}

func hasAnyAvailable(status []providerStatus) bool {
	for _, s := range status {
		if s.Available {
			return true
		}
	}
	return false
}

func filterForWizard(models []ai.ModelInfo, provider string, status []providerStatus) []ai.ModelInfo {
	available := make(map[string]bool, len(status))
	for _, s := range status {
		available[s.Name] = s.Available
	}
	out := make([]ai.ModelInfo, 0, len(models))
	for _, m := range models {
		if !available[m.Provider] {
			continue
		}
		if provider != "" && m.Provider != provider {
			continue
		}
		out = append(out, m)
	}
	return out
}

// easyModeIndices returns indices into `candidates` that represent the
// wizard's recommended picks. We pick at most one embedding and one
// generation model — matching the existing AI setup wizard's "easy
// mode" philosophy — preferring entries that already carry TierVerified.
func easyModeIndices(candidates []ai.ModelInfo, cfg ai.AIConfig) []int {
	embed := -1
	gen := -1
	for i, m := range candidates {
		if m.Tier != ai.TierVerified {
			continue
		}
		switch m.Type {
		case "embedding":
			if embed == -1 || isPreferredProvider(m.Provider, cfg) {
				embed = i
			}
		case "generation":
			if gen == -1 || isPreferredProvider(m.Provider, cfg) {
				gen = i
			}
		}
	}
	out := make([]int, 0, 2)
	if embed >= 0 {
		out = append(out, embed)
	}
	if gen >= 0 {
		out = append(out, gen)
	}
	return out
}

func isPreferredProvider(p string, cfg ai.AIConfig) bool {
	return cfg.Provider != "" && p == cfg.Provider
}

func subset(all []ai.ModelInfo, indices []int) []ai.ModelInfo {
	out := make([]ai.ModelInfo, 0, len(indices))
	for _, i := range indices {
		if i >= 0 && i < len(all) {
			out = append(out, all[i])
		}
	}
	return out
}

// promptPick prints the candidate list with the default selections marked
// and asks the user to accept, clear, or replace the set with a space-
// separated list of indices (1-based).
func promptPick(candidates []ai.ModelInfo, defaults []int, stderr io.Writer) []ai.ModelInfo {
	def := make(map[int]bool, len(defaults))
	for _, i := range defaults {
		def[i] = true
	}
	fmt.Fprintln(stderr, "\nAvailable models:")
	for i, m := range candidates {
		marker := "[ ]"
		if def[i] {
			marker = "[x]"
		}
		fmt.Fprintf(stderr, "  %2d. %s %s / %s  (%s)\n", i+1, marker, m.Provider, m.ID, m.Type)
	}
	fmt.Fprintln(stderr, "\nEnter indices to test (e.g. '1 3 5'), or hit <Enter> to accept defaults:")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return subset(candidates, defaults)
	}
	chosen := make([]int, 0)
	for _, tok := range strings.Fields(line) {
		i, err := strconv.Atoi(tok)
		if err != nil || i < 1 || i > len(candidates) {
			fmt.Fprintf(stderr, "warning: ignoring invalid index %q\n", tok)
			continue
		}
		chosen = append(chosen, i-1)
	}
	return subset(candidates, chosen)
}

func promptYesNo(stderr io.Writer, question string, defaultYes bool) bool {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}
	fmt.Fprintf(stderr, "%s %s ", question, suffix)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

// --- event sink abstraction ---

type wizardEventSink struct {
	jsonMode bool
	stdout   io.Writer
	stderr   io.Writer
	enc      *json.Encoder
}

func newWizardEventSink(jsonMode bool, stdout, stderr io.Writer) *wizardEventSink {
	s := &wizardEventSink{jsonMode: jsonMode, stdout: stdout, stderr: stderr}
	if jsonMode {
		s.enc = json.NewEncoder(stdout)
	}
	return s
}

// interactive reports whether the wizard should prompt for input.
// JSON mode is automation-friendly and uses defaults throughout.
func (s *wizardEventSink) interactive() bool {
	return !s.jsonMode
}

func (s *wizardEventSink) emit(e wizardEvent) {
	if s.jsonMode {
		_ = s.enc.Encode(e)
		return
	}
	renderWizardEventText(s.stderr, e)
}

// renderWizardEventText writes an event as a human-readable line on the
// TTY. Keep this free of external dependencies so the wizard stays fast
// and terminal-agnostic.
func renderWizardEventText(w io.Writer, e wizardEvent) {
	switch e.Step {
	case "providers":
		fmt.Fprintln(w, "\n=== Providers ===")
		for _, p := range e.Providers {
			mark := "✓"
			suffix := ""
			if !p.Available {
				mark = "✗"
				suffix = "  (" + p.Reason + ")"
			}
			fmt.Fprintf(w, "  %s  %s%s\n", mark, p.Name, suffix)
		}
	case "discovered":
		fmt.Fprintf(w, "\n=== Discovered %d candidate model(s) ===\n", e.Count)
	case "cost_preview":
		fmt.Fprintf(w, "\n=== Cost preview ===\nEstimated test cost: $%.6f\n", e.TotalUSD)
	case "test_start":
		fmt.Fprintf(w, "Testing %s (%s/%s)... ", e.ModelID, e.Provider, e.Type)
	case "test_result":
		if e.OK != nil && *e.OK {
			fmt.Fprintf(w, "PASS (%d ms)\n", e.LatencyMs)
		} else {
			fmt.Fprintf(w, "FAIL — %s\n", e.Error)
		}
	case "saved":
		fmt.Fprintf(w, "  → saved %s to %s catalog\n", e.ModelID, e.Scope)
	case "save_error":
		fmt.Fprintf(w, "  × save failed for %s: %s\n", e.ModelID, e.Error)
	case "set_active":
		fmt.Fprintf(w, "  → set active config: %s\n", strings.Join(e.Keys, ", "))
	case "set_active_warning":
		fmt.Fprintf(w, "  Warning: %s\n", e.Message)
	case "set_active_error":
		fmt.Fprintf(w, "  × set-active failed: %s\n", e.Error)
	case "done":
		fmt.Fprintf(w, "\n=== Done ===\nTested: %d   Passed: %d   Saved: %d\n", e.Tested, e.Passed, e.Saved)
	}
}

func ptrBool(b bool) *bool { return &b }

func errorDetailOnFail(r *ai.TestProbeResult) string {
	if r.OK {
		return ""
	}
	return r.Detail
}

// latencyMs parses the string latency (e.g. "420ms") into milliseconds.
// Returns 0 on parse error so callers don't need to branch.
func latencyMs(s string) int64 {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d.Milliseconds()
}
