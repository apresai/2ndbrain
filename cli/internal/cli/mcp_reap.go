package cli

import (
	"fmt"
	"os"
	"time"

	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var mcpReapCmd = &cobra.Command{
	Use:   "reap",
	Short: "Terminate stale/orphaned 2nb mcp-server processes",
	Long: `Sends SIGTERM to mcp-server processes for this vault that have been idle
longer than --older-than (default 6h), so an orphaned server left by a closed
AI session stops holding the index open.

Safety: it never kills the reaping process itself, never an actively-used server
(recent tool activity), and uses SIGTERM only — the 2nb mcp-server handles
SIGTERM cleanly (removing its sidecar and exiting), and avoiding SIGKILL
sidesteps the risk of a recycled PID. The idle self-exit on mcp-server makes
this a rarely-needed backstop. Use --dry-run to preview.`,
	Example: `  2nb mcp reap --dry-run
  2nb mcp reap --older-than 1h`,
	RunE: runMCPReap,
}

var reapOlderThan time.Duration
var reapDryRun bool

func init() {
	mcpReapCmd.Flags().DurationVar(&reapOlderThan, "older-than", 6*time.Hour, "Only reap servers whose last activity is older than this")
	mcpReapCmd.Flags().BoolVar(&reapDryRun, "dry-run", false, "List what would be reaped without terminating anything")
	mcpCmd.AddCommand(mcpReapCmd)
}

func runMCPReap(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	res, err := mcppkg.Reap(v, reapOlderThan, reapDryRun)
	if err != nil {
		return fmt.Errorf("reap: %w", err)
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, res)
	}

	verb := "Reaped"
	if res.DryRun {
		verb = "Would reap"
	}
	if len(res.Reaped) == 0 {
		fmt.Printf("No stale mcp-server processes (older than %s) to reap.\n", res.Threshold)
	} else {
		fmt.Printf("%s %d stale mcp-server process(es) (older than %s):\n", verb, len(res.Reaped), res.Threshold)
		for _, r := range res.Reaped {
			fmt.Printf("  PID %d (idle %s)\n", r.PID, r.Age)
		}
	}
	for _, s := range res.Skipped {
		fmt.Printf("  skipped PID %d: %s\n", s.PID, s.Reason)
	}
	return nil
}
