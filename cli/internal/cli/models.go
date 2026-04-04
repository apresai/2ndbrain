package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	modelsTypeFilt string
	modelsFreeOnly bool
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

func init() {
	modelsListCmd.Flags().StringVar(&modelsTypeFilt, "type", "", "Filter by type: embed or generation")
	modelsListCmd.Flags().BoolVar(&modelsFreeOnly, "free", false, "Show only free models")
	modelsCmd.AddCommand(modelsListCmd)
	rootCmd.AddCommand(modelsCmd)
}

func runModelsList(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	initAIProviders(v)

	ctx := context.Background()
	models := ai.DefaultRegistry.ListModels(ctx)

	// Apply filters
	var filtered []ai.ModelInfo
	for _, m := range models {
		if modelsTypeFilt != "" && m.Type != modelsTypeFilt {
			continue
		}
		if modelsFreeOnly && (m.PriceIn > 0 || m.PriceOut > 0) {
			continue
		}
		filtered = append(filtered, m)
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, filtered)
	}

	// Pretty table output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tTYPE\tMODEL\tPRICE\tDIMS\tCTX")
	for _, m := range filtered {
		price := "free"
		if m.PriceIn > 0 || m.PriceOut > 0 {
			price = fmt.Sprintf("$%.2f/$%.2f", m.PriceIn, m.PriceOut)
		}
		dims := "-"
		if m.Dimensions > 0 {
			dims = fmt.Sprintf("%d", m.Dimensions)
		}
		ctxLen := "-"
		if m.ContextLen > 0 {
			ctxLen = formatContext(m.ContextLen)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Provider, m.Type, m.ID, price, dims, ctxLen)
	}
	return w.Flush()
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
