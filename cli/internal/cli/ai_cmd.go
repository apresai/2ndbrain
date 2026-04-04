package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var aiCmd = &cobra.Command{
	Use:   "ai",
	Short: "AI provider management",
}

var aiStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current AI provider status",
	RunE:  runAIStatus,
}

var aiEmbedCmd = &cobra.Command{
	Use:   "embed <text>",
	Short: "Generate embedding for text (debug/testing)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runAIEmbed,
}

func init() {
	aiCmd.AddCommand(aiStatusCmd)
	aiCmd.AddCommand(aiEmbedCmd)
	rootCmd.AddCommand(aiCmd)
}

type AIStatus struct {
	Provider       string `json:"provider"`
	EmbeddingModel string `json:"embedding_model"`
	GenModel       string `json:"generation_model"`
	Dimensions     int    `json:"dimensions"`
	EmbedAvailable bool   `json:"embed_available"`
	GenAvailable   bool   `json:"gen_available"`
	EmbeddingCount int    `json:"embedding_count"`
	DocumentCount  int    `json:"document_count"`
}

func runAIStatus(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	status := AIStatus{
		Provider:       cfg.Provider,
		EmbeddingModel: cfg.EmbeddingModel,
		GenModel:       cfg.GenerationModel,
		Dimensions:     cfg.Dimensions,
	}

	// Check provider availability
	if emb, err := ai.DefaultRegistry.Embedder(cfg.Provider); err == nil {
		status.EmbedAvailable = emb.Available(ctx)
	}
	if gen, err := ai.DefaultRegistry.Generator(cfg.Provider); err == nil {
		status.GenAvailable = gen.Available(ctx)
	}

	// Count embeddings and documents
	status.EmbeddingCount, _ = v.DB.EmbeddingCount()
	var docCount int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)
	status.DocumentCount = docCount

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, status)
	}

	// Pretty output
	fmt.Printf("Provider:         %s\n", status.Provider)
	fmt.Printf("Embedding model:  %s\n", status.EmbeddingModel)
	fmt.Printf("Generation model: %s\n", status.GenModel)
	fmt.Printf("Dimensions:       %d\n", status.Dimensions)
	fmt.Printf("Embed ready:      %v\n", status.EmbedAvailable)
	fmt.Printf("Generation ready: %v\n", status.GenAvailable)
	fmt.Printf("Documents:        %d\n", status.DocumentCount)
	fmt.Printf("Embeddings:       %d/%d\n", status.EmbeddingCount, status.DocumentCount)

	if status.EmbeddingCount < status.DocumentCount {
		fmt.Fprintf(os.Stderr, "\nRun `2nb index` to generate missing embeddings.\n")
	}

	return nil
}

func runAIEmbed(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil {
		return fmt.Errorf("no embedding provider: %w", err)
	}

	text := strings.Join(args, " ")
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Embedding %d chars via %s...\n", len(text), cfg.Provider)
	}

	vecs, err := embedder.Embed(ctx, []string{text})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, vecs[0])
}
