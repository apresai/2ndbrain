package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var configDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose AI configuration problems and print fix hints",
	Long: `Run a series of checks over the vault's AI configuration and report any
problem that would silently break semantic search or generation, each with a
one-line fix hint.

Checks: the active provider is known and not disabled; the active embedding and
generation models are not registered under a different provider (the orphaned-
slot bug); ai.dimensions matches the active embedding model; the stored
embeddings in the DB match the current selection (dimension / model); and the
similarity threshold resolves to a usable value.

Exits non-zero (2) if any check fails, so it can gate a script or CI step.
This is a read-only diagnostic: it never writes config or touches notes.`,
	RunE: runConfigDoctor,
}

func init() {
	configCmd.AddCommand(configDoctorCmd)
}

// DoctorCheck is one diagnostic result. OK=false means the check found a
// genuine config problem (trips the non-zero exit); Warn=true flags an
// environmental condition that is not a config defect (e.g. the provider is
// momentarily unreachable): surfaced with a hint but it does NOT fail the run,
// so `config doctor` stays usable as an offline/CI gate. Fix carries the
// actionable one-line remedy. This shape is a JSON consumer contract (the GUI /
// scripts decode it), so field names are stable.
type DoctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// ConfigDoctorReport is the full `config doctor` result: the per-check list
// plus a roll-up OK that is true only when every check passed.
type ConfigDoctorReport struct {
	OK     bool          `json:"ok"`
	Checks []DoctorCheck `json:"checks"`
}

func runConfigDoctor(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	// Register providers so the DB-vs-selection check can probe the active
	// embedder's dimension/availability, exactly as `ai status` does.
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	// Observe DB embedding facts directly (source of truth), mirroring
	// runAIStatus, so the portability check reflects reality not config cache.
	// Unlike a plain status command, doctor's whole job is catching problems,
	// so we slog.Warn on a DB read error rather than silently degrading to
	// zero counts (which would read as a misleading "empty_vault" / clean run).
	totalDocs, embeddedDocs, embeddableUnembedded, errCounts := v.DB.EmbeddingCounts()
	if errCounts != nil {
		slog.Warn("config doctor: embedding-counts query failed; portability check may be inaccurate", "err", errCounts)
	}
	vaultDim, errDim := v.DB.SampleEmbeddingDim()
	if errDim != nil {
		slog.Warn("config doctor: sample-embedding-dim query failed", "err", errDim)
	}
	vaultModels, errModels := v.DB.DistinctEmbeddingModels()
	if errModels != nil {
		slog.Warn("config doctor: distinct-embedding-models query failed", "err", errModels)
	}
	st := doctorVaultState{
		totalDocs: totalDocs, embeddedDocs: embeddedDocs, embeddableUnembedded: embeddableUnembedded,
		vaultDim: vaultDim, vaultModels: vaultModels,
		freshness: vault.CheckIndexFreshness(v.DB),
	}

	embedder, _ := ai.DefaultRegistry.Embedder(cfg.Provider)
	report := ConfigDoctorReport{Checks: buildDoctorChecks(ctx, v.Root, cfg, embedder, st)}
	report.OK = true
	for _, c := range report.Checks {
		if !c.OK {
			report.OK = false
			break
		}
	}

	if format := getFormat(cmd); format != "" {
		if err := output.Write(os.Stdout, format, report); err != nil {
			return err
		}
		if !report.OK {
			return exitWithError(ExitValidation, "config doctor found problems")
		}
		return nil
	}

	// Human output: one line per check. ✗ = config defect (fails the run),
	// ! = warning (environmental, non-failing), ✓ = healthy. ✗ and ! checks
	// carry their fix/remedy hint.
	for _, c := range report.Checks {
		mark := "✓"
		switch {
		case !c.OK:
			mark = "✗"
		case c.Warn:
			mark = "!"
		}
		fmt.Printf("%s %s: %s\n", mark, c.Name, c.Detail)
		if (!c.OK || c.Warn) && c.Fix != "" {
			fmt.Printf("    %s\n", c.Fix)
		}
	}
	if report.OK {
		fmt.Println("\nAll checks passed.")
		return nil
	}
	return exitWithError(ExitValidation, "config doctor found problems")
}

// doctorVaultState holds the observed DB embedding facts the portability check
// needs, read once in runConfigDoctor so buildDoctorChecks stays a pure
// function over its inputs (and is unit-testable without a live DB).
type doctorVaultState struct {
	totalDocs            int
	embeddedDocs         int
	embeddableUnembedded int
	vaultDim             int
	vaultModels          []string
	freshness            vault.IndexFreshness
}

// buildDoctorChecks runs every diagnostic and returns the results in display
// order. Each check is a thin wrapper over an existing validator so the doctor
// stays a reporting shell, not a second source of truth. embedder is the active
// provider's embedder (nil if unregistered); it is passed in rather than
// resolved here so the function is pure and unit-testable with a stub.
func buildDoctorChecks(ctx context.Context, vaultRoot string, cfg ai.AIConfig, embedder ai.EmbeddingProvider, st doctorVaultState) []DoctorCheck {
	var checks []DoctorCheck

	// 1. Provider is a known name.
	if ai.IsKnownProvider(cfg.Provider) {
		checks = append(checks, DoctorCheck{Name: "provider known", OK: true, Detail: cfg.Provider})
	} else {
		checks = append(checks, DoctorCheck{
			Name:   "provider known",
			OK:     false,
			Detail: fmt.Sprintf("ai.provider is %q", cfg.Provider),
			Fix:    fmt.Sprintf("set ai.provider to one of: %s (e.g. `2nb config set ai.provider bedrock`)", strings.Join(ai.KnownProviders, ", ")),
		})
	}

	// 2. Active provider is not disabled (an active-but-disabled provider runs
	//    in the CLI but is hidden everywhere in the GUI, a contradiction).
	if cfg.ProviderDisabled(cfg.Provider) {
		checks = append(checks, DoctorCheck{
			Name:   "active provider enabled",
			OK:     false,
			Detail: fmt.Sprintf("ai.%s.disabled is true while %s is the active provider", cfg.Provider, cfg.Provider),
			Fix:    fmt.Sprintf("`2nb config set ai.%s.disabled false` (or re-select it; `config set ai.provider` clears the flag)", cfg.Provider),
		})
	} else {
		checks = append(checks, DoctorCheck{Name: "active provider enabled", OK: true, Detail: "not disabled"})
	}

	// 3. No orphaned slot: an embedding/generation model registered under a
	//    different provider than ai.provider can never be served.
	if issues := cfg.Validate(vaultRoot); len(issues) > 0 {
		for _, issue := range issues {
			checks = append(checks, DoctorCheck{
				Name:   "model belongs to active provider",
				OK:     false,
				Detail: issue,
				Fix:    "switch the model to one served by the active provider, or change ai.provider to match (see the detail).",
			})
		}
	} else {
		checks = append(checks, DoctorCheck{Name: "model belongs to active provider", OK: true, Detail: "embedding + generation models match ai.provider"})
	}

	// 4. ai.dimensions is valid for the active embedding model — its declared
	//    default OR any of its Matryoshka widths (a deliberate 256/384/3072 on
	//    Nova is a valid, supported choice, not a defect). A dim of 0 means the
	//    model isn't in any catalog (a user's own discovered model): not a
	//    failure, just unverifiable here.
	if dim := ai.EmbeddingDimensionsFor(vaultRoot, cfg.Provider, cfg.EmbeddingModel); dim > 0 {
		supported := ai.SupportedDimensionsFor(vaultRoot, cfg.Provider, cfg.EmbeddingModel)
		if cfg.Dimensions == dim || containsInt(supported, cfg.Dimensions) {
			checks = append(checks, DoctorCheck{Name: "ai.dimensions matches model", OK: true, Detail: fmt.Sprintf("%dd", cfg.Dimensions)})
		} else {
			valid := fmt.Sprintf("%dd", dim)
			if len(supported) > 0 {
				valid = intsCSV(supported)
			}
			checks = append(checks, DoctorCheck{
				Name:   "ai.dimensions matches model",
				OK:     false,
				Detail: fmt.Sprintf("ai.dimensions is %d but %s supports %s", cfg.Dimensions, cfg.EmbeddingModel, valid),
				Fix:    fmt.Sprintf("`2nb config set ai.dimensions <%s>`, then `2nb index --force-reembed`", valid),
			})
		}
	} else {
		checks = append(checks, DoctorCheck{Name: "ai.dimensions matches model", OK: true, Detail: "model not in catalog; dimension unverifiable (ok)"})
	}

	// 5. Stored embeddings match the current selection (dimension / model).
	//    Reuse derivePortability so doctor and `ai status` never disagree.
	status, action := derivePortability(ctx, cfg, embedder,
		st.vaultDim, st.vaultModels, st.totalDocs, st.embeddedDocs, st.embeddableUnembedded, st.freshness)
	switch status {
	case "ok", "unindexed", "empty_vault", "stale", "no_provider",
		"upgrade_reindex_recommended", "upgrade_reembed_recommended":
		// These are healthy or merely "needs indexing" states, not config
		// breakage; doctor reports them as OK with the status as detail.
		checks = append(checks, DoctorCheck{Name: "embeddings match selection", OK: true, Detail: portabilityLabel(status)})
	case "provider_unavailable":
		// Environmental, NOT a config defect: the config is correct but the
		// provider is momentarily unreachable (offline, Ollama not started,
		// expired creds). Warn rather than fail so `config doctor` doesn't exit
		// 2 in offline/CI runs where the config itself is sound.
		checks = append(checks, DoctorCheck{
			Name:   "embeddings match selection",
			OK:     true,
			Warn:   true,
			Detail: portabilityLabel(status),
			Fix:    action,
		})
	default:
		// Genuine config breakage: dimension_break / model_mismatch / mixed.
		checks = append(checks, DoctorCheck{
			Name:   "embeddings match selection",
			OK:     false,
			Detail: portabilityLabel(status),
			Fix:    action,
		})
	}

	// 6. Similarity threshold resolves to a usable value (informational:
	//    always OK, surfaced so the user can trace a noisy/empty search).
	threshold, src := cfg.ResolveSimilarityThresholdFull(vaultRoot)
	checks = append(checks, DoctorCheck{
		Name:   "similarity threshold resolves",
		OK:     true,
		Detail: fmt.Sprintf("%g (%s)", threshold, src),
	})

	return checks
}

// portabilityLabel renders a derivePortability status string in the same
// human form `ai status` uses (UPPER, underscores → spaces).
func portabilityLabel(status string) string {
	return strings.ToUpper(strings.ReplaceAll(status, "_", " "))
}
