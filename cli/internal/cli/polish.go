package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

const defaultPolishSystem = `You are a copy editor. Fix spelling, grammar, and punctuation errors in the markdown below. Improve clarity where wording is awkward, but preserve the author's voice, all wikilinks like [[foo]], all code blocks (fenced and inline), and the heading structure exactly. Do not add or remove sections. Do not reformat lists. Return ONLY the corrected markdown body with no explanation, no commentary, and no surrounding code fences.`

var polishCmd = &cobra.Command{
	Use:   "polish <path>",
	Short: "AI copy-edit a document and return the proposed revision",
	Long: `Runs the configured AI generation provider over the document body to fix
spelling, grammar, and awkward phrasing while preserving voice, wikilinks,
and structure. Emits JSON with both the original and polished text so the
editor can present a diff and let the user accept, edit, or reject.`,
	Args: cobra.ExactArgs(1),
	RunE: runPolish,
}

var polishSystemFlag string
var polishMaxTokens int

func init() {
	polishCmd.GroupID = "ai"
	polishCmd.Flags().StringVar(&polishSystemFlag, "system", "", "Override the default copy-editor system prompt")
	polishCmd.Flags().IntVar(&polishMaxTokens, "max-tokens", 4096, "Maximum tokens for the generated response")
	rootCmd.AddCommand(polishCmd)
}

// PolishResult is the JSON payload returned by `2nb polish`.
type PolishResult struct {
	Path       string `json:"path"`
	Original   string `json:"original"`
	Polished   string `json:"polished"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	DurationMs int64  `json:"duration_ms"`
}

func runPolish(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	relArg := args[0]
	absPath := relArg
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(v.Root, relArg)
	}
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("resolve doc path: %w", err)
	}

	parsed, err := document.ParseFile(absPath)
	if err != nil {
		return fmt.Errorf("parse source: %w", err)
	}

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	generator, err := ai.DefaultRegistry.Generator(cfg.Provider)
	if err != nil {
		return fmt.Errorf("no generation provider: %w\nRun `2nb ai status` to check provider configuration", err)
	}
	if !generator.Available(ctx) {
		return fmt.Errorf("generation provider %q not available", cfg.Provider)
	}

	systemPrompt := polishSystemFlag
	if systemPrompt == "" {
		systemPrompt = defaultPolishSystem
	}

	opts := ai.GenOpts{
		Temperature:  0.2,
		MaxTokens:    polishMaxTokens,
		SystemPrompt: systemPrompt,
	}

	start := time.Now()
	polished, err := generator.Generate(ctx, parsed.Body, opts)
	if err != nil {
		return fmt.Errorf("polish generation failed: %w", err)
	}
	polished = strings.TrimSpace(polished)

	result := PolishResult{
		Path:       v.RelPath(absPath),
		Original:   parsed.Body,
		Polished:   polished,
		Provider:   cfg.Provider,
		Model:      cfg.GenerationModel,
		DurationMs: time.Since(start).Milliseconds(),
	}

	if getFormat(cmd) == output.FormatJSON {
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("Polished %s in %dms using %s / %s\n", result.Path, result.DurationMs, result.Provider, result.Model)
	fmt.Println()
	fmt.Println(result.Polished)
	return nil
}
