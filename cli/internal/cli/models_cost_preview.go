package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	costProbeKind string
	costProvider  string
	costAll       bool
)

var modelsCostPreviewCmd = &cobra.Command{
	Use:   "cost-preview [model-id...]",
	Short: "Estimate the cost of running a test or benchmark probe",
	Long: `Projects how much a test / benchmark probe would cost for one or
more models, using current pricing. Use before running the wizard or
a bulk --promote so you don't accidentally rack up charges.

Without arguments, pass --all to estimate across every verified model.`,
	RunE: runModelsCostPreview,
}

func init() {
	modelsCostPreviewCmd.Flags().StringVar(&costProbeKind, "probe", string(ai.ProbeTest),
		"Probe scenario: test, bench_embed, bench_gen, bench_rag, retrieval")
	modelsCostPreviewCmd.Flags().StringVar(&costProvider, "provider", "",
		"Filter by provider: bedrock, openrouter, ollama")
	modelsCostPreviewCmd.Flags().BoolVar(&costAll, "all", false,
		"Estimate across every verified model (when no IDs given)")
	_ = modelsCostPreviewCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsCostPreviewCmd.RegisterFlagCompletionFunc("probe", completeProbeKinds)
	modelsCmd.AddCommand(modelsCostPreviewCmd)
}

// completeProbeKinds powers tab-completion on the --probe flag.
func completeProbeKinds(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return []string{
		string(ai.ProbeTest),
		string(ai.ProbeBenchEmbed),
		string(ai.ProbeBenchGen),
		string(ai.ProbeBenchRAG),
		string(ai.ProbeRetrievalQuality),
	}, cobra.ShellCompDirectiveNoFileComp
}

type costPreviewOutput struct {
	Probe     string            `json:"probe"`
	Estimates []ai.CostEstimate `json:"estimates"`
	TotalUSD  float64           `json:"total_usd"`
}

func runModelsCostPreview(cmd *cobra.Command, args []string) error {
	probe := ai.ProbeKind(costProbeKind)
	if ai.DefaultProbeSpec(probe) == (ai.ProbeSpec{}) {
		return fmt.Errorf("unknown probe %q; use one of: test, bench_embed, bench_gen, bench_rag, retrieval", probe)
	}
	if len(args) == 0 && !costAll {
		return fmt.Errorf("pass at least one model id, or --all to estimate across the full catalog")
	}

	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	list, err := ai.BuildModelList(context.Background(), ai.MergedListOptions{
		Config:    v.Config.AI,
		VaultRoot: v.Root,
	})
	if err != nil {
		return fmt.Errorf("load model catalog: %w", err)
	}

	// Build the candidate set: either the ids the user asked for, or the
	// whole verified catalog when --all is set.
	var candidates []ai.ModelInfo
	if len(args) > 0 {
		idSet := make(map[string]bool, len(args))
		for _, a := range args {
			idSet[a] = true
		}
		for _, m := range list.Verified {
			if idSet[m.ID] {
				candidates = append(candidates, m)
			}
		}
		// Report any IDs the user typed that we couldn't resolve, so they
		// don't silently get zero results and think their catalog is empty.
		missing := make([]string, 0)
		for _, a := range args {
			found := false
			for _, c := range candidates {
				if c.ID == a {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, a)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "warning: unknown model id(s): %s\n", strings.Join(missing, ", "))
		}
	} else {
		candidates = list.Verified
	}

	if costProvider != "" {
		filtered := candidates[:0]
		for _, m := range candidates {
			if m.Provider == costProvider {
				filtered = append(filtered, m)
			}
		}
		candidates = filtered
	}

	estimates, total := ai.EstimateCosts(candidates, probe)
	slog.Info("models cost-preview", "probe", probe, "models", len(estimates), "total_usd", total)

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, costPreviewOutput{
			Probe:     string(probe),
			Estimates: estimates,
			TotalUSD:  total,
		})
	}

	if len(estimates) == 0 {
		fmt.Fprintln(os.Stderr, "(no models matched the filter — nothing to estimate)")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "MODEL\tPROVIDER\tIN\tOUT\tREQ\tUSD\tPRICING")
	for _, e := range estimates {
		pricing := "known"
		if !e.KnownPricing {
			pricing = "unknown"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t$%.6f\t%s\n",
			e.ModelID, e.Provider, e.InputTokens, e.OutputTokens, e.Requests, e.USD, pricing)
	}
	fmt.Fprintf(w, "\t\t\t\tTotal\t$%.6f\t(probe: %s)\n", total, probe)
	return w.Flush()
}
