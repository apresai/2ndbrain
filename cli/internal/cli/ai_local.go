package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var aiLocalCmd = &cobra.Command{
	Use:   "local",
	Short: "Check local AI readiness",
	Long:  "Performs a comprehensive check of Ollama, required models, disk space, memory, and embedding status.",
	RunE:  runAILocal,
}

func init() {
	aiCmd.AddCommand(aiLocalCmd)
}

func runAILocal(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Try to open vault for embedding counts (optional)
	var embedCount, docCount, staleCount int
	var vaultRoot, endpoint string
	var cfg ai.AIConfig

	v, err := openVault()
	if err == nil {
		defer v.Close()
		cfg = v.Config.AI
		vaultRoot = v.Root
		embedCount, _ = v.DB.EmbeddingCount()
		v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)
		stale, _ := v.DB.DocumentsNeedingEmbedding(cfg.EmbeddingModel)
		staleCount = len(stale)
	} else {
		cfg = ai.DefaultAIConfig()
	}

	// Use configured endpoint or default
	endpoint = cfg.Ollama.Endpoint
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}

	// Override models to local defaults if provider isn't ollama
	if cfg.Provider != "ollama" {
		cfg.EmbeddingModel = ai.DefaultLocalEmbedModel
		cfg.GenerationModel = ai.DefaultLocalGenModel
		cfg.Dimensions = 768
	}

	report, err := ai.BuildReadinessReport(ctx, cfg, endpoint, vaultRoot, embedCount, docCount, staleCount)
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, report)
	}

	renderReport(report)
	return nil
}

func renderReport(r *ai.ReadinessReport) {
	fmt.Println("Local AI Readiness")
	fmt.Println("==================")

	// Ollama status
	if r.Ollama.Installed && r.Ollama.Running {
		fmt.Println("Ollama:            ✓ installed, running")
	} else if r.Ollama.Installed {
		fmt.Println("Ollama:            ⚠ installed, not running")
	} else {
		fmt.Println("Ollama:            ✗ not installed")
	}

	// Disk
	if r.Disk.AvailHuman != "" {
		fmt.Printf("Disk available:    %s\n", r.Disk.AvailHuman)
	}
	fmt.Println()

	// Embedding model
	if r.EmbedModel != nil {
		m := r.EmbedModel
		dimStr := ""
		if m.Dimensions > 0 {
			dimStr = fmt.Sprintf(" (%dd)", m.Dimensions)
		}
		fmt.Printf("Embedding model:   %s%s\n", m.Name, dimStr)
		if m.Pulled {
			fmt.Printf("  Status:          ✓ pulled (%s)\n", m.SizeHuman)
		} else if m.DownloadHuman != "" {
			fmt.Printf("  Status:          ✗ not pulled\n")
			fmt.Printf("  Download size:   %s\n", m.DownloadHuman)
		} else {
			fmt.Printf("  Status:          ✗ not pulled\n")
		}
		fmt.Printf("  RAM estimate:    %s\n", m.RAMHuman)
		fmt.Println()
	}

	// Generation model
	if r.GenModel != nil {
		m := r.GenModel
		fmt.Printf("Generation model:  %s\n", m.Name)
		if m.Pulled {
			fmt.Printf("  Status:          ✓ pulled (%s)\n", m.SizeHuman)
		} else if m.DownloadHuman != "" {
			fmt.Printf("  Status:          ✗ not pulled\n")
			fmt.Printf("  Download size:   %s\n", m.DownloadHuman)
		} else {
			fmt.Printf("  Status:          ✗ not pulled\n")
		}
		fmt.Printf("  RAM estimate:    %s\n", m.RAMHuman)
		fmt.Println()
	}

	// RAM verdict
	if r.Memory.TotalHuman != "" {
		fmt.Printf("System RAM:        %s\n", r.Memory.TotalHuman)
		fmt.Printf("RAM verdict:       %s\n", r.RAMVerdict)
		fmt.Println()
	}

	// Embeddings
	if r.Embeddings != nil {
		fmt.Printf("Embeddings:        %d/%d documents", r.Embeddings.Indexed, r.Embeddings.Total)
		if r.Embeddings.NeedIndexing > 0 {
			fmt.Printf(" (%d need indexing)", r.Embeddings.NeedIndexing)
		}
		fmt.Println()
		fmt.Println()
	}

	// Upgrade recommendation
	if r.Upgrade != nil {
		fmt.Printf("Recommended:       ↑ %s\n", r.Upgrade.Reason)
		fmt.Printf("                     %s download\n", r.Upgrade.DownloadHuman)
		fmt.Printf("                     Run: ollama pull %s\n", r.Upgrade.Model)
		fmt.Println()
	}

	// Overall + actions
	if r.Overall == "ready" {
		fmt.Println("Overall:           ✓ Ready")
	} else {
		fmt.Println("Overall:           ⚠ Action needed")
		for _, a := range r.Actions {
			fmt.Printf("  → %s\n", a)
		}
	}
}
