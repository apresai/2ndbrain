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

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Manage AI models",
}

var modelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available models from all configured providers",
	RunE:  runModelsList,
}

var modelsTestCmd = &cobra.Command{
	Use:   "test <model-id>",
	Short: "Test if a model works with 2nb",
	Long:  "Sends a quick probe (embed or generate) to verify a model is callable. Useful for testing unverified models before switching.",
	Args:  cobra.ExactArgs(1),
	RunE:  runModelsTest,
}

func init() {
	modelsListCmd.Flags().StringVar(&modelsTypeFilt, "type", "", "Filter by type: embed or generation")
	modelsListCmd.Flags().BoolVar(&modelsFreeOnly, "free", false, "Show only free models")
	modelsListCmd.Flags().BoolVar(&modelsDiscover, "discover", false, "Query vendor APIs for full model catalogs")
	modelsListCmd.Flags().BoolVar(&modelsCheckStatus, "status", false, "Probe provider reachability and credentials")
	modelsListCmd.Flags().StringVar(&modelsProvider, "provider", "", "Filter by provider: bedrock, openrouter, ollama")

	modelsTestCmd.Flags().StringVar(&testProvider, "provider", "", "Provider: bedrock, openrouter, ollama (auto-detected if omitted)")
	modelsTestCmd.Flags().StringVar(&testModelType, "type", "", "Model type: embedding or generation (auto-detected if omitted)")

	modelsCmd.AddCommand(modelsListCmd)
	modelsCmd.AddCommand(modelsTestCmd)
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
		fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tPRICE\tCTX\tSTATUS")
	} else {
		fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tPRICE\tCTX")
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

	if showStatus {
		status := statusLabel(m)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, price, ctxLen, status)
	} else {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, price, ctxLen)
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

	fmt.Printf("Testing %s...\n", modelID)

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
