package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/retrieve"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var fixCmd = &cobra.Command{
	Use:   "fix",
	Short: "Fix broken links, formatting, YAML parse errors, and schema issues across the vault",
	Long: `Scans the entire vault and resolves broken links, formatting issues, YAML parse
errors, and schema mismatches:

  Stage A (deterministic):
    1. Widens .2ndbrain/schemas.yaml note status enums when common statuses
       (active, reference, archived, published, done) appear on type: note files.
    2. Populates missing required title/status fields and coerces quoted/zone-less
       Obsidian date properties while preserving frontmatter comments and keys.
    3. Normalizes internal link syntax (backslashes, whitespace, empty alias
       pipes [[note|]], redundant .md in wikilinks, unencoded spaces in local
       markdown note links) outside code blocks.
    4. Repoints broken links and backlinks on unique normalized drift, moved/stale
       folder paths (by unique basename), and obvious slug/high-overlap matches.

  Stage B & C (AI-assisted with --ai, or automatically during --write unless --no-ai):
    5. Repairs malformed YAML frontmatter and unclosed frontmatter fences.
    6. Repairs schema enum mismatches, missing required fields, and unparseable
       dates using AST-surgical updates validated against .2ndbrain/schemas.yaml.
    7. Recovers remaining broken/ambiguous wikilinks via grounded retrieval and
       LLM reranking, unwrapping only confirmed dead links to plain text.

By default this previews what deterministic rules fix and classifies remaining
AI-fixable issues without calling an AI model or modifying any file.
Pass --ai to preview AI-assisted repairs in memory, --write to apply fixes and
save an undo snapshot to .2ndbrain/recovery/fix-last.json, or --undo to restore
the previous state byte-for-byte (with --force to overwrite external edits).`,
	Args: cobra.NoArgs,
	RunE: runFix,
}

var (
	fixWrite bool
	fixUndo  bool
	fixForce bool
	fixAI    bool
	fixNoAI  bool
)

func init() {
	fixCmd.GroupID = "quality"
	fixCmd.Flags().BoolVar(&fixWrite, "write", false, "Apply vault fixes in place and snapshot originals for --undo")
	fixCmd.Flags().BoolVar(&fixUndo, "undo", false, "Restore all notes and .2ndbrain/schemas.yaml from the last `2nb fix --write`")
	fixCmd.Flags().BoolVar(&fixForce, "force", false, "With --undo, overwrite files modified externally since the last fix")
	fixCmd.Flags().BoolVar(&fixAI, "ai", false, "Use the configured AI model for parse, schema, and broken-link repair (fails closed if unavailable)")
	fixCmd.Flags().BoolVar(&fixNoAI, "no-ai", false, "Disable automatic AI repair during --write (deterministic rules only)")
	rootCmd.AddCommand(fixCmd)
}

func runFix(cmd *cobra.Command, args []string) error {
	write := fixWrite
	undo := fixUndo
	force := fixForce
	useAI := fixAI
	noAI := fixNoAI
	defer func() {
		fixWrite = false
		fixUndo = false
		fixForce = false
		fixAI = false
		fixNoAI = false
	}()

	if write && undo {
		return exitWithError(ExitValidation, "error: --write and --undo cannot be combined")
	}
	if force && !undo {
		return exitWithError(ExitValidation, "error: --force is only valid with --undo")
	}
	if useAI && noAI {
		return exitWithError(ExitValidation, "error: --ai and --no-ai cannot be combined")
	}
	if undo && (useAI || noAI) {
		return exitWithError(ExitValidation, "error: --ai and --no-ai cannot be combined with --undo")
	}
	if err := refuseBodylessFormat(cmd, "fix"); err != nil {
		return err
	}

	var v *vault.Vault
	var err error
	if write || undo {
		v, err = openVaultAndSetActive()
	} else {
		v, err = openVault()
	}
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	if undo {
		res, uerr := polish.UndoLastFix(v, force)
		if uerr != nil {
			return exitWithError(ExitValidation, uerr.Error())
		}
		if done, emitErr := emitStructured(cmd, res); done {
			return emitErr
		}
		printFixUndoResult(res)
		return nil
	}

	ctx := context.Background()
	cfg := v.Config.AI
	threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)

	opts := polish.FixOptions{
		Write:           write,
		AI:              useAI,
		NoAI:            noAI,
		ReasoningEffort: cfg.ResolveReasoningEffort(),
		Threshold:       threshold,
	}

	if useAI {
		providers, reason := resolveFixAIProviders(ctx, v)
		if providers == nil || providers.Generator == nil {
			return errors.New(reason)
		}
		opts.Providers = providers
	} else if write && !noAI {
		opts.ResolveAI = func(rctx context.Context) (*polish.FixAIProviders, string, error) {
			p, reason := resolveFixAIProviders(rctx, v)
			return p, reason, nil
		}
	}

	fixStart := time.Now()
	report, ferr := polish.FixVaultWithOptions(ctx, v, opts)
	if ferr != nil {
		return fmt.Errorf("fix vault: %w", ferr)
	}

	if report.AIUsage.GenCalls > 0 || report.AIUsage.EmbedCalls > 0 {
		op := metrics.Operation{
			Operation:    metrics.OpFix,
			DurationMs:   time.Since(fixStart).Milliseconds(),
			OK:           true,
			FilesScanned: report.FilesScanned,
			DocsIndexed:  report.NotesChanged,
		}
		var legs []metrics.UsageLeg
		if report.AIUsage.GenCalls > 0 {
			legs = append(legs, buildUsageLeg(v.Root, cfg.EffectiveGenerationProvider(), cfg.GenerationModel, "generation", metrics.OpFix, report.AIUsage.GenCalls, report.AIUsage.GenInTokens, report.AIUsage.GenOutTokens))
		}
		if report.AIUsage.EmbedCalls > 0 {
			legs = append(legs, buildUsageLeg(v.Root, cfg.EffectiveEmbeddingProvider(), cfg.EmbeddingModel, "embedding", metrics.OpFix, report.AIUsage.EmbedCalls, report.AIUsage.EmbedInTokens, 0))
		}
		attachUsageLegs(&op, legs...)
		recordMetric(v, op)
	}

	if done, emitErr := emitStructured(cmd, report); done {
		return emitErr
	}
	printFixVaultReport(report)
	return nil
}

func resolveFixAIProviders(ctx context.Context, v *vault.Vault) (*polish.FixAIProviders, string) {
	initAIProviders(v)
	cfg := v.Config.AI
	genProvider := cfg.EffectiveGenerationProvider()
	if genProvider == "" {
		return nil, "no generation provider configured — run `2nb ai setup` to enable"
	}
	gen, gerr := ai.DefaultRegistry.Generator(genProvider)
	if gerr != nil {
		return nil, fmt.Sprintf("no generation provider: %v — run `2nb ai status` to check provider configuration", gerr)
	}
	if ready, code := ai.Availability(ctx, gen); !ready {
		return nil, ai.NotReadyMessage("generation", genProvider, code)
	}

	embedProvider := cfg.EffectiveEmbeddingProvider()
	embedder, eerr := ai.DefaultRegistry.Embedder(embedProvider)
	vecReady, vecWarn := retrieve.VectorCompat(ctx, v, embedder, eerr)
	var warnings []string
	if !vecReady {
		embedder = nil
		if vecWarn != "" {
			warnings = append(warnings, vecWarn)
		}
	}

	return &polish.FixAIProviders{
		Generator:     gen,
		Embedder:      embedder,
		EmbedderReady: vecReady,
		ProviderName:  genProvider,
		ModelName:     cfg.GenerationModel,
		Warnings:      warnings,
	}, ""
}

func printFixUndoResult(res *polish.FixUndoResult) {
	if res == nil {
		return
	}
	if res.SchemaRestored {
		fmt.Printf("Reverted %d file(s) and restored .2ndbrain/schemas.yaml.\n", res.FilesRestored)
		return
	}
	fmt.Printf("Reverted %d file(s).\n", res.FilesRestored)
}

func printFixVaultReport(r *polish.FixVaultReport) {
	if r == nil {
		return
	}
	mode := "Preview"
	if r.Written {
		mode = "Applied"
	}
	fmt.Printf("%s across %d file(s): %d note(s) affected, %d fix(es) (%d link(s) updated, %d dead link(s) unwrapped, %d formatting fix(es), %d schema issue(s) resolved).\n",
		mode, r.FilesScanned, r.NotesChanged, r.TotalFixes, r.LinksUpdated, r.LinksUnlinked, r.FormattingFixed, r.SchemaIssuesResolved)
	if r.AIApplied {
		fmt.Printf("AI repair (%s · %s): %d AI fix(es) applied.\n", r.AIProvider, r.AIModel, r.AIFixesApplied)
	} else if r.AISkippedReason != "" {
		fmt.Printf("AI repair skipped: %s\n", r.AISkippedReason)
	}
	if r.SchemaUpdated && len(r.SchemaStatusesAdded) > 0 {
		fmt.Printf("Schema: widen note status enum (+%s)\n", strings.Join(r.SchemaStatusesAdded, ", "))
	}
	for _, n := range r.Notes {
		fmt.Printf("  %s\n", n.Path)
		for _, la := range n.LinkActions {
			switch la.Action {
			case "relink":
				fmt.Printf("    relink [[%s]] -> [[%s]] (%s, %dx)\n", la.From, la.To, la.Reason, la.Count)
			case "unlink":
				fmt.Printf("    unlink [[%s]] (%s, %dx)\n", la.From, la.Reason, la.Count)
			case "format":
				fmt.Printf("    format %s -> %s (%dx)\n", la.From, la.To, la.Count)
			}
		}
		for _, ff := range n.FrontmatterFixes {
			if ff.From != "" {
				fmt.Printf("    frontmatter %s: %s -> %s (%s)\n", ff.Field, ff.From, ff.To, ff.Reason)
			} else {
				fmt.Printf("    frontmatter %s = %s (%s)\n", ff.Field, ff.To, ff.Reason)
			}
		}
	}
	if len(r.Ambiguous) > 0 {
		fmt.Printf("Skipped %d ambiguous link(s) needing human choice:\n", len(r.Ambiguous))
		for _, a := range r.Ambiguous {
			fmt.Printf("  %s: [[%s]] -> %s\n", a.Path, a.Target, strings.Join(a.Candidates, ", "))
		}
	}
	if r.AIFixableCount > 0 {
		fmt.Printf("AI-fixable issue(s) remaining: %d\n", r.AIFixableCount)
		for _, is := range r.AIFixableIssues {
			fmt.Printf("  %s [%s]: %s\n", is.Path, is.Kind, is.Message)
		}
	}
	for _, w := range r.Warnings {
		fmt.Printf("Warning: %s\n", w)
	}
	if !r.Written && (r.TotalFixes > 0 || r.AIFixableCount > 0) {
		if r.AIFixableCount > 0 && !r.AIApplied {
			fmt.Println("Run `2nb fix --write` (or `2nb fix --ai` to preview AI repairs) to resolve these issues.")
		} else {
			fmt.Println("Run `2nb fix --write` to apply these fixes.")
		}
	} else if r.Written && r.UndoAvailable {
		fmt.Println("Run `2nb fix --undo` to revert this fix batch.")
	}
}
