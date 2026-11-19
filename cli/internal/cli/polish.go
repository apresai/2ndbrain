package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/retrieve"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var polishCmd = &cobra.Command{
	Use:   "polish <path>",
	Short: "AI copy-edit a document and return the proposed revision",
	Long: `Runs the configured AI generation provider over the document body to fix
spelling, grammar, and awkward phrasing while preserving voice, wikilinks,
code spans, and heading structure. Emits JSON with both the original and
polished text so the editor can present a diff and let the user accept, edit,
or reject.

With --goal, prose tone, conciseness, or style can be steered while keeping
links, backlinks, schema conformance, and code preservation primary. With
--schema (default on; disable with --no-schema), frontmatter and date fields are
repaired to match .2ndbrain/schemas.yaml. With --links, grounded [[wikilinks]]
to existing vault notes are woven into prose. With --repair-links, broken
[[wikilinks]] are repaired deterministically and with AI recovery. With
--backlinks, incoming links and referencing notes across the vault are previewed
before any disk write; pass --write --backlinks --apply-backlinks to apply the
multi-note batch atomically with full --undo support.`,
	Args: cobra.ExactArgs(1),
	RunE: runPolish,
}

var (
	polishSystemFlag     string
	polishGoalFlag       string
	polishLensFlag       string
	polishMaxTokens      int
	polishWrite          bool
	polishLinks          bool
	polishRepairLinks    bool
	polishSchema         bool
	polishNoSchema       bool
	polishBacklinks      bool
	polishApplyBacklinks bool
	polishUndo           bool
	polishForce          bool
)

func init() {
	polishCmd.GroupID = "ai"
	polishCmd.Flags().StringVar(&polishSystemFlag, "system", "", "Override the default copy-editor system prompt")
	polishCmd.Flags().StringVar(&polishGoalFlag, "goal", "", "Optional goal/lens prompt steering prose tone or focus while preserving links, schema, and code")
	polishCmd.Flags().StringVar(&polishLensFlag, "lens", "", "Alias for --goal")
	_ = polishCmd.Flags().MarkHidden("lens")
	polishCmd.Flags().IntVar(&polishMaxTokens, "max-tokens", 4096, "Maximum tokens for the generated response")
	polishCmd.Flags().BoolVar(&polishWrite, "write", false, "Apply the polished body (and schema fixes) to the document in place (opt-in; default previews only)")
	polishCmd.Flags().BoolVar(&polishLinks, "links", false, "Also add grounded [[wikilinks]] to existing vault notes (never invents targets)")
	polishCmd.Flags().BoolVar(&polishRepairLinks, "repair-links", false, "Repair broken [[wikilinks]] to existing notes (case/whitespace/alias drift + AI recovery)")
	polishCmd.Flags().BoolVar(&polishSchema, "schema", false, "Repair frontmatter and schema conformance against .2ndbrain/schemas.yaml (enabled by default)")
	polishCmd.Flags().BoolVar(&polishNoSchema, "no-schema", false, "Disable frontmatter/schema conformance repair during polish")
	polishCmd.Flags().BoolVar(&polishBacklinks, "backlinks", false, "Discover and preview incoming backlink updates in other notes (writes nothing without --write --apply-backlinks)")
	polishCmd.Flags().BoolVar(&polishApplyBacklinks, "apply-backlinks", false, "With --write: apply planned incoming backlink updates to referencing notes and record a batch undo snapshot")
	polishCmd.Flags().BoolVar(&polishUndo, "undo", false, "Restore the pre-polish version of <path> (and any backlink batch notes) from the latest polish snapshot")
	polishCmd.Flags().BoolVar(&polishForce, "force", false, "With --undo: restore even if files changed since polish")
	rootCmd.AddCommand(polishCmd)
}

// PolishResult is the JSON payload returned by `2nb polish`.
type PolishResult struct {
	Path               string                        `json:"path"`
	Original           string                        `json:"original"`
	Polished           string                        `json:"polished"`
	Provider           string                        `json:"provider"`
	Model              string                        `json:"model"`
	DurationMs         int64                         `json:"duration_ms"`
	Goal               string                        `json:"goal,omitempty"`
	LinksAdded         []string                      `json:"links_added,omitempty"`
	LinksRepaired      []polish.LinkRepair           `json:"links_repaired,omitempty"`
	LinksSkipped       []polish.LinkRepair           `json:"links_skipped,omitempty"`
	FrontmatterFixes   []polish.FixFieldChange       `json:"frontmatter_fixes,omitempty"`
	SchemaRepaired     bool                          `json:"schema_repaired,omitempty"`
	BacklinkPreviews   []polish.PolishBacklinkChange `json:"backlink_previews,omitempty"`
	BacklinksApplied   int                           `json:"backlinks_applied,omitempty"`
	UndoBatchAvailable bool                          `json:"undo_batch_available,omitempty"`
	Warning            string                        `json:"warning,omitempty"`
}

// PolishUndoResult is the JSON payload returned by `2nb polish <path> --undo`.
type PolishUndoResult struct {
	Path          string   `json:"path"`
	Reverted      bool     `json:"reverted"`
	RestoredPaths []string `json:"restored_paths"`
}

func runPolish(cmd *cobra.Command, args []string) error {
	systemFlag := polishSystemFlag
	goalFlag := polishGoalFlag
	lensFlag := polishLensFlag
	maxTokens := polishMaxTokens
	write := polishWrite
	links := polishLinks
	repairLinks := polishRepairLinks
	schemaOpt := polishSchema
	noSchema := polishNoSchema
	backlinksOpt := polishBacklinks
	applyBacklinks := polishApplyBacklinks
	undo := polishUndo
	force := polishForce

	defer func() {
		polishSystemFlag = ""
		polishGoalFlag = ""
		polishLensFlag = ""
		polishMaxTokens = 4096
		polishWrite = false
		polishLinks = false
		polishRepairLinks = false
		polishSchema = false
		polishNoSchema = false
		polishBacklinks = false
		polishApplyBacklinks = false
		polishUndo = false
		polishForce = false
		for _, name := range []string{
			"system", "goal", "lens", "max-tokens", "write", "links", "repair-links",
			"schema", "no-schema", "backlinks", "apply-backlinks", "undo", "force",
		} {
			if f := cmd.Flags().Lookup(name); f != nil {
				f.Changed = false
			}
		}
	}()

	// A polish result is a report, not a body, and the run is a paid generation
	// call. Refuse the formats that cannot render it before spending anything.
	// This is the ONLY raw/md gate on either polish path, --undo included: it
	// runs before the vault open, so nothing downstream ever sees those two.
	if err := refuseBodylessFormat(cmd, "polish"); err != nil {
		return err
	}
	if goalFlag != "" && lensFlag != "" && goalFlag != lensFlag {
		return exitWithError(ExitValidation, "error: --goal and --lens cannot be combined with different values")
	}
	if goalFlag == "" && lensFlag != "" {
		goalFlag = lensFlag
	}
	if schemaOpt && noSchema {
		return exitWithError(ExitValidation, "error: --schema and --no-schema cannot be combined")
	}
	if force && !undo {
		return exitWithError(ExitValidation, "error: --force is only valid with --undo")
	}
	if applyBacklinks && !write {
		return exitWithError(ExitValidation, "error: --apply-backlinks requires --write")
	}
	if undo {
		if write || links || repairLinks || systemFlag != "" || goalFlag != "" || schemaOpt || noSchema || backlinksOpt || applyBacklinks {
			return exitWithError(ExitValidation, "error: --undo cannot be combined with --write, --links, --repair-links, --system, --goal, --schema, --no-schema, --backlinks, or --apply-backlinks")
		}
		return runPolishUndo(cmd, args, force)
	}

	useBacklinks := backlinksOpt || applyBacklinks
	runSchema := !noSchema

	// --write mutates the document on disk, so it opens the write path that also
	// pins the active vault (matching meta/append). The default preview path is
	// read-only.
	var v *vault.Vault
	var err error
	if write {
		v, err = openVaultAndSetActive()
	} else {
		v, err = openVault()
	}
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	absPath, rel, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}
	origBytes, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("resolve doc path: %w", err)
	}

	lowerAbs := strings.ToLower(absPath)
	parsed, parseErr := document.ParseFile(absPath)
	if parseErr == nil && (write || applyBacklinks) && document.IsReadOnlyType(parsed.Type) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: cannot apply --write to a read-only %s file (%s); .canvas/.base files are indexed read-only", parsed.Type, rel))
	}
	if parseErr != nil {
		if errors.Is(parseErr, document.ErrRead) || !runSchema || strings.HasSuffix(lowerAbs, ".canvas") || strings.HasSuffix(lowerAbs, ".base") {
			return fmt.Errorf("parse source: %w", parseErr)
		}
		parsed = nil
	} else if parsed != nil && parsed.Frontmatter == nil && document.HasUnclosedFrontmatter(origBytes) {
		if !runSchema || strings.HasSuffix(lowerAbs, ".canvas") || strings.HasSuffix(lowerAbs, ".base") {
			return fmt.Errorf("parse source: unclosed YAML frontmatter fence")
		}
	}

	var warnings []string
	var repaired, skippedRepairs []polish.LinkRepair

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI
	genProvider := cfg.EffectiveGenerationProvider()

	generator, err := ai.DefaultRegistry.Generator(genProvider)
	if err != nil {
		return fmt.Errorf("no generation provider: %w\nRun `2nb ai status` to check provider configuration", err)
	}
	if ready, code := ai.Availability(ctx, generator); !ready {
		return generatorNotReadyError(genProvider, code)
	}

	start := time.Now()
	var totalGenCalls, totalGenIn, totalGenOut int
	var totalEmbedCalls, totalEmbedIn int

	workingBytes := origBytes
	activeDoc := parsed
	var frontmatterFixes []polish.FixFieldChange
	schemaRepaired := false

	if runSchema {
		sres, serr := polish.RepairNoteSchema(ctx, v, rel, origBytes, parsed, generator, cfg.ResolveReasoningEffort())
		totalGenCalls += sres.Usage.GenCalls
		totalGenIn += sres.Usage.GenInTokens
		totalGenOut += sres.Usage.GenOutTokens
		warnings = append(warnings, sres.Warnings...)
		if serr != nil {
			if totalGenCalls > 0 {
				op := metrics.Operation{
					Operation:   metrics.OpPolish,
					DurationMs:  time.Since(start).Milliseconds(),
					OK:          false,
					Error:       serr.Error(),
					DocsIndexed: 1,
				}
				attachUsageLegs(&op, buildUsageLeg(v.Root, genProvider, cfg.GenerationModel, "generation", metrics.OpPolish, totalGenCalls, totalGenIn, totalGenOut))
				recordMetric(v, op)
			}
			return serr
		}
		workingBytes = sres.Bytes
		activeDoc = sres.Doc
		frontmatterFixes = sres.Fixes
		schemaRepaired = sres.Repaired
	}
	if activeDoc == nil {
		return fmt.Errorf("parse source: unable to parse %s", rel)
	}

	originalBody := activeDoc.Body
	systemPrompt := polish.BuildPolishSystemPrompt(systemFlag, goalFlag, links)
	userMessage := originalBody
	var candidates []polish.LinkCandidate
	var embedInTok int
	if links {
		candidates, _, userMessage, warnings, embedInTok = preparePolishLinksTracked(ctx, v, activeDoc, rel, systemPrompt, originalBody, warnings)
		if embedInTok > 0 {
			totalEmbedCalls++
			totalEmbedIn += embedInTok
		}
	}

	opts := ai.GenOpts{
		Temperature:     ai.Ptr(0.2),
		MaxTokens:       maxTokens,
		SystemPrompt:    systemPrompt,
		ReasoningEffort: cfg.ResolveReasoningEffort(),
	}

	gctx, stopNotice := slowCallNotice(ctx, "polishing note")
	var (
		polished string
		genUsage ai.GenUsage
	)
	if ug, ok := generator.(ai.UsageGenerator); ok {
		polished, genUsage, err = ug.GenerateWithUsage(gctx, userMessage, opts)
	} else {
		polished, err = generator.Generate(gctx, userMessage, opts)
	}
	stopNotice()
	if err != nil {
		op := metrics.Operation{
			Operation:   metrics.OpPolish,
			DurationMs:  time.Since(start).Milliseconds(),
			OK:          false,
			Error:       err.Error(),
			DocsIndexed: 1,
		}
		var legs []metrics.UsageLeg
		if totalGenCalls > 0 {
			legs = append(legs, buildUsageLeg(v.Root, genProvider, cfg.GenerationModel, "generation", metrics.OpPolish, totalGenCalls, totalGenIn, totalGenOut))
		}
		if totalEmbedCalls > 0 && totalEmbedIn > 0 {
			legs = append(legs, buildUsageLeg(v.Root, cfg.EffectiveEmbeddingProvider(), cfg.EmbeddingModel, "embedding", metrics.OpPolish, totalEmbedCalls, totalEmbedIn, 0))
		}
		if len(legs) > 0 {
			attachUsageLegs(&op, legs...)
		}
		recordMetric(v, op)
		return fmt.Errorf("polish generation failed: %w", err)
	}
	if genUsage.InputTokens == 0 && genUsage.OutputTokens == 0 {
		genUsage.InputTokens = max(1, (len(systemPrompt)+len(userMessage))/4)
		genUsage.OutputTokens = max(1, len(strings.TrimSpace(polished))/4)
	}
	totalGenCalls++
	totalGenIn += genUsage.InputTokens
	totalGenOut += genUsage.OutputTokens

	allowed := polish.AllowedLinkSet(candidates, originalBody)
	cleaned, removed, violations := polish.ValidatePolishedBody(originalBody, polished, allowed)
	if len(violations) > 0 {
		var origRawLinks []string
		for _, l := range document.ExtractWikiLinks(strings.ReplaceAll(originalBody, "\r\n", "\n")) {
			origRawLinks = append(origRawLinks, l.Raw)
		}
		retryUser := userMessage + "\n\nCRITICAL CORRECTION: Your previous draft violated hard constraints (" +
			strings.Join(violations, ", ") +
			"). Return ONLY the Markdown body reproducing EVERY existing link verbatim (do not drop, shorten, or add |aliases to: " +
			strings.Join(origRawLinks, ", ") +
			"), every fenced/inline code span, and every heading line exactly."
		var retryOut string
		var retryUsage ai.GenUsage
		var retryErr error
		if ug, ok := generator.(ai.UsageGenerator); ok {
			retryOut, retryUsage, retryErr = ug.GenerateWithUsage(ctx, retryUser, opts)
		} else {
			retryOut, retryErr = generator.Generate(ctx, retryUser, opts)
		}
		if retryErr == nil {
			if retryUsage.InputTokens == 0 && retryUsage.OutputTokens == 0 {
				retryUsage.InputTokens = max(1, (len(systemPrompt)+len(retryUser))/4)
				retryUsage.OutputTokens = max(1, len(strings.TrimSpace(retryOut))/4)
			}
			totalGenCalls++
			totalGenIn += retryUsage.InputTokens
			totalGenOut += retryUsage.OutputTokens
			cleanedRetry, removedRetry, violationsRetry := polish.ValidatePolishedBody(originalBody, retryOut, allowed)
			if len(violationsRetry) == 0 {
				cleaned = cleanedRetry
				removed = removedRetry
				violations = nil
			} else {
				violations = violationsRetry
			}
		}
	}
	if len(violations) > 0 {
		cleaned = strings.TrimSpace(strings.ReplaceAll(originalBody, "\r\n", "\n"))
		removed = nil
		warnings = append(warnings, fmt.Sprintf("kept original body prose because polished output violated hard preservation invariants (%s)", strings.Join(violations, ", ")))
	}
	polished = cleaned

	if len(removed) > 0 {
		warnings = append(warnings, fmt.Sprintf("dropped %d link(s) to notes that don't exist", len(removed)))
	}
	var linksAdded []string
	if links {
		normOrig := strings.ReplaceAll(originalBody, "\r\n", "\n")
		for _, l := range polish.NewLinks(normOrig, polished) {
			linksAdded = append(linksAdded, l.Target)
		}
	}

	// Repair broken [[wikilinks]] after generation (deterministic + AI Stage C).
	if repairLinks {
		embedder, embErr := ai.DefaultRegistry.Embedder(cfg.EffectiveEmbeddingProvider())
		vecReady, vecWarn := retrieve.VectorCompat(ctx, v, embedder, embErr)
		if !vecReady {
			embedder = nil
			if vecWarn != "" {
				warnings = append(warnings, vecWarn)
			}
		}
		threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)
		rr, rerr := polish.RepairOutgoingLinks(ctx, v, rel, polished, generator, embedder, vecReady, threshold, true)
		totalGenCalls += rr.Usage.GenCalls
		totalGenIn += rr.Usage.GenInTokens
		totalGenOut += rr.Usage.GenOutTokens
		totalEmbedCalls += rr.Usage.EmbedCalls
		totalEmbedIn += rr.Usage.EmbedInTokens
		warnings = append(warnings, rr.Warnings...)
		if rerr != nil {
			warnings = append(warnings, fmt.Sprintf("link repair skipped: %v", rerr))
		} else {
			polished = rr.Body
			repaired = rr.Repaired
			skippedRepairs = rr.Skipped
		}
	}

	if len(skippedRepairs) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d broken link(s) left unrepaired (no confident target)", len(skippedRepairs)))
	}

	polishedDisplay, diskBody := polish.FormatBodyLikeOriginal(originalBody, workingBytes, polished)

	var blPlan polish.BacklinkPlanResult
	var backlinkPreviews []polish.PolishBacklinkChange
	if useBacklinks {
		var berr error
		blPlan, berr = polish.PlanBacklinkUpdates(ctx, v, polish.BacklinkPlanInput{
			TargetRel:       rel,
			TargetDoc:       activeDoc,
			TargetOrigBody:  originalBody,
			TargetNewBody:   polishedDisplay,
			Goal:            goalFlag,
			Generator:       generator,
			ReasoningEffort: cfg.ResolveReasoningEffort(),
		})
		totalGenCalls += blPlan.Usage.GenCalls
		totalGenIn += blPlan.Usage.GenInTokens
		totalGenOut += blPlan.Usage.GenOutTokens
		warnings = append(warnings, blPlan.Warnings...)
		if berr != nil {
			warnings = append(warnings, fmt.Sprintf("backlinks preview skipped: %v", berr))
		} else if len(blPlan.Items) > 0 {
			backlinkPreviews = make([]polish.PolishBacklinkChange, len(blPlan.Items))
			for i, item := range blPlan.Items {
				backlinkPreviews[i] = item.Change
			}
		}
	}

	backlinksApplied := 0
	undoBatchAvailable := false

	// --write applies the polished text (and any schema repairs) to the target
	// document in place via AST/raw-frontmatter-preserving write, and applies
	// incoming backlink updates only when --apply-backlinks is also set.
	if write {
		canSnapshot := len(origBytes) > 0
		if !canSnapshot {
			warnings = append(warnings, "could not snapshot the original; undo will be unavailable for this polish")
		}

		targetWrittenBytes, werr := polish.ApplyBodyWithFrontmatterPreservation(workingBytes, activeDoc, absPath, diskBody)
		if werr != nil {
			return fmt.Errorf("serialize document: %w", werr)
		}
		if err := polish.WriteFileAtomic(absPath, targetWrittenBytes); err != nil {
			return fmt.Errorf("write target document: %w", err)
		}
		if err := vault.IndexSingleFile(v, absPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to index document: %v\n", err)
			slog.Warn("failed to index document after polish write", "err", err)
		}
		if updatedDoc, perr := document.Parse(rel, targetWrittenBytes); perr == nil {
			updatedDoc.Path = rel
			updatedDoc.ComputeContentHash()
			embedNewDocument(v, updatedDoc)
		}

		var batchFiles []polish.PolishBatchFile
		if applyBacklinks && len(blPlan.Items) > 0 {
			for i, item := range blPlan.Items {
				blBytes, berr := polish.ApplyBodyWithFrontmatterPreservation(item.OrigBytes, item.ParsedDoc, item.AbsPath, item.DiskBody)
				if berr != nil {
					warnings = append(warnings, fmt.Sprintf("backlink write %s: %v", item.Change.Path, berr))
					continue
				}
				if werr := polish.WriteFileAtomic(item.AbsPath, blBytes); werr != nil {
					warnings = append(warnings, fmt.Sprintf("backlink write %s: %v", item.Change.Path, werr))
					continue
				}
				if ierr := vault.IndexSingleFile(v, item.AbsPath); ierr != nil {
					slog.Warn("failed to index backlink note after polish write", "path", item.Change.Path, "err", ierr)
				}
				if blDoc, perr := document.Parse(item.Change.Path, blBytes); perr == nil {
					blDoc.Path = item.Change.Path
					blDoc.ComputeContentHash()
					embedNewDocument(v, blDoc)
				}
				backlinkPreviews[i].Applied = true
				backlinksApplied++
				batchFiles = append(batchFiles, polish.PolishBatchFile{
					Path:          item.Change.Path,
					OriginalFull:  string(item.OrigBytes),
					PostWriteHash: polish.HashContent(blBytes),
				})
			}
			if v.DB != nil && backlinksApplied > 0 {
				_ = v.DB.ResolveLinks()
			}
		}

		if canSnapshot {
			written, _ := os.ReadFile(absPath)
			snap := polish.PolishSnapshot{
				Path:          rel,
				OriginalFull:  string(origBytes),
				PolishedBody:  polishedDisplay,
				Provider:      genProvider,
				Model:         cfg.GenerationModel,
				Links:         links,
				Timestamp:     time.Now().UTC().Format(time.RFC3339),
				PostWriteHash: polish.HashContent(written),
				BatchFiles:    batchFiles,
			}
			if err := polish.WriteSnapshot(v, snap); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to write undo snapshot: %v", err))
			} else if len(batchFiles) > 0 {
				undoBatchAvailable = true
			}
		}
		fmt.Fprintf(os.Stderr, "Wrote polished body to %s (%s / %s)\n", rel, genProvider, cfg.GenerationModel)
	}

	// Meter all generation and embedding legs made during this polish invocation.
	{
		op := metrics.Operation{
			Operation:   metrics.OpPolish,
			DurationMs:  time.Since(start).Milliseconds(),
			OK:          true,
			DocsIndexed: 1 + backlinksApplied,
		}
		var legs []metrics.UsageLeg
		if totalGenCalls > 0 {
			legs = append(legs, buildUsageLeg(v.Root, genProvider, cfg.GenerationModel, "generation", metrics.OpPolish, totalGenCalls, totalGenIn, totalGenOut))
		}
		if totalEmbedCalls > 0 && totalEmbedIn > 0 {
			legs = append(legs, buildUsageLeg(v.Root, cfg.EffectiveEmbeddingProvider(), cfg.EmbeddingModel, "embedding", metrics.OpPolish, totalEmbedCalls, totalEmbedIn, 0))
		}
		if len(legs) > 0 {
			attachUsageLegs(&op, legs...)
		}
		recordMetric(v, op)
	}

	result := PolishResult{
		Path:               rel,
		Original:           originalBody,
		Polished:           polishedDisplay,
		Provider:           genProvider,
		Model:              cfg.GenerationModel,
		DurationMs:         time.Since(start).Milliseconds(),
		Goal:               strings.TrimSpace(goalFlag),
		LinksAdded:         linksAdded,
		LinksRepaired:      repaired,
		LinksSkipped:       skippedRepairs,
		FrontmatterFixes:   frontmatterFixes,
		SchemaRepaired:     schemaRepaired,
		BacklinkPreviews:   backlinkPreviews,
		BacklinksApplied:   backlinksApplied,
		UndoBatchAvailable: undoBatchAvailable,
		Warning:            strings.Join(warnings, "; "),
	}

	if result.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", result.Warning)
	}

	if done, err := emitStructured(cmd, result); done {
		return err
	}

	fmt.Printf("Polished %s in %dms using %s / %s\n", result.Path, result.DurationMs, result.Provider, result.Model)
	if len(linksAdded) > 0 {
		fmt.Printf("Added links: %s\n", strings.Join(linksAdded, ", "))
	}
	for _, r := range repaired {
		fmt.Printf("Repaired link: [[%s]] -> [[%s]]\n", r.Raw, r.NewTarget)
	}
	if len(frontmatterFixes) > 0 {
		fmt.Printf("Schema fixes: %d field(s)\n", len(frontmatterFixes))
	}
	if len(backlinkPreviews) > 0 {
		fmt.Printf("Backlinks: %d previewed, %d applied\n", len(backlinkPreviews), backlinksApplied)
	}
	fmt.Println()
	fmt.Println(result.Polished)
	return nil
}

// preparePolishLinks gathers grounded link candidates and returns the augmented
// system prompt (with link instructions) and user message (with the candidate
// block), plus any degradation warnings.
func preparePolishLinks(ctx context.Context, v *vault.Vault, parsed *document.Document, rel, systemPrompt, originalBody string, warnings []string) ([]polish.LinkCandidate, string, string, []string) {
	cands, _, user, warns, _ := preparePolishLinksTracked(ctx, v, parsed, rel, systemPrompt, originalBody, warnings)
	return cands, systemPrompt + polish.LinkInstructions, user, warns
}

func preparePolishLinksTracked(ctx context.Context, v *vault.Vault, parsed *document.Document, rel, systemPrompt, originalBody string, warnings []string) ([]polish.LinkCandidate, string, string, []string, int) {
	cfg := v.Config.AI
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.EffectiveEmbeddingProvider())
	if embErr != nil {
		embedder = nil
	}

	var docIDs []string
	var embeddings [][]float32
	warnMsg := ""
	if ready, msg := retrieve.VectorCompat(ctx, v, embedder, embErr); ready {
		docIDs, embeddings, _ = v.DB.AllEmbeddings()
	} else {
		warnMsg = msg // empty for a zero-embedding vault; substring matching still runs
		embedder = nil
	}

	embedInTok := 0
	if embedder != nil && len(strings.TrimSpace(originalBody)) > 0 {
		r := []rune(originalBody)
		if len(r) > 2000 {
			r = r[:2000]
		}
		embedInTok = max(1, len(string(r))/4)
	}

	threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)
	cands, warn, _ := polish.GatherCandidates(ctx, v, embedder, polish.CandidateInput{
		Source:     parsed,
		SourcePath: rel,
		DocIDs:     docIDs,
		Embeddings: embeddings,
		Threshold:  threshold,
		Warning:    warnMsg,
	})
	if warn != "" {
		warnings = append(warnings, warn)
	}

	return cands, systemPrompt, polish.BuildPolishUserMessage(originalBody, cands), warnings, embedInTok
}

func runPolishUndo(cmd *cobra.Command, args []string, force bool) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	absPath, rel, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}

	snap, err := polish.LoadSnapshot(v, rel)
	if err != nil {
		return err
	}
	if snap == nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("no polish snapshot to undo for %s", rel))
	}

	current, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read current file: %w", err)
	}

	targetDecision := polish.ClassifyUndo(current, snap)
	var conflicted []string
	if targetDecision == polish.UndoConflict {
		conflicted = append(conflicted, rel)
	}

	batchDecisions := make([]polish.UndoDecision, len(snap.BatchFiles))
	allNoop := targetDecision == polish.UndoNoop
	for i, bf := range snap.BatchFiles {
		bfAbs := v.AbsPath(bf.Path)
		bfCur, rerr := os.ReadFile(bfAbs)
		if rerr != nil {
			batchDecisions[i] = polish.UndoConflict
			conflicted = append(conflicted, bf.Path)
			allNoop = false
			continue
		}
		d := polish.ClassifyUndoFile(bfCur, bf.OriginalFull, bf.PostWriteHash)
		batchDecisions[i] = d
		if d == polish.UndoConflict {
			conflicted = append(conflicted, bf.Path)
		}
		if d != polish.UndoNoop {
			allNoop = false
		}
	}

	if len(conflicted) > 0 && !force {
		return exitWithError(ExitValidation, fmt.Sprintf("error: %s changed since it was polished; re-run with `--undo --force` to discard those edits and restore the pre-polish version", strings.Join(conflicted, ", ")))
	}
	if allNoop {
		_ = polish.DeleteSnapshot(v, rel)
		return emitUndoBatchResult(cmd, rel, false, []string{})
	}

	var restoredPaths []string
	if targetDecision != polish.UndoNoop || force {
		if err := restorePolishOriginal(v, absPath, rel, []byte(snap.OriginalFull)); err != nil {
			return err
		}
		restoredPaths = append(restoredPaths, rel)
	}
	for i, bf := range snap.BatchFiles {
		if batchDecisions[i] == polish.UndoNoop && !force {
			continue
		}
		bfAbs := v.AbsPath(bf.Path)
		if err := restorePolishOriginal(v, bfAbs, bf.Path, []byte(bf.OriginalFull)); err != nil {
			return err
		}
		restoredPaths = append(restoredPaths, bf.Path)
	}
	if v.DB != nil && len(restoredPaths) > 0 {
		_ = v.DB.ResolveLinks()
	}

	_ = polish.DeleteSnapshot(v, rel)
	return emitUndoBatchResult(cmd, rel, true, restoredPaths)
}

// restorePolishOriginal writes the snapshot's original bytes back verbatim
// (atomic temp+rename, no Serialize round-trip → byte-exact frontmatter), then
// reindexes and re-embeds the restored document.
func restorePolishOriginal(v *vault.Vault, absPath, rel string, content []byte) error {
	if err := polish.WriteFileAtomic(absPath, content); err != nil {
		return fmt.Errorf("restore %s: %w", rel, err)
	}
	if err := vault.IndexSingleFile(v, absPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index document: %v\n", err)
	}
	if doc, err := document.Parse(absPath, content); err == nil {
		doc.Path = rel
		doc.ComputeContentHash()
		embedNewDocument(v, doc)
	}
	return nil
}

// emitUndoResult keeps the 3-arg signature used by format_coverage_test.go and
// delegates to emitUndoBatchResult.
func emitUndoResult(cmd *cobra.Command, rel string, reverted bool) error {
	return emitUndoBatchResult(cmd, rel, reverted, nil)
}

func emitUndoBatchResult(cmd *cobra.Command, rel string, reverted bool, restoredPaths []string) error {
	if restoredPaths == nil {
		if reverted {
			restoredPaths = []string{rel}
		} else {
			restoredPaths = []string{}
		}
	}
	if reverted {
		if len(restoredPaths) > 1 {
			fmt.Fprintf(os.Stderr, "Reverted %s and %d linked note(s) to their pre-polish versions\n", rel, len(restoredPaths)-1)
		} else {
			fmt.Fprintf(os.Stderr, "Reverted %s to its pre-polish version\n", rel)
		}
	} else {
		fmt.Fprintf(os.Stderr, "%s is already at its pre-polish version; nothing to undo\n", rel)
	}
	slog.Info("polish undo", "path", rel, "reverted", reverted, "restored_paths", restoredPaths, "format", string(getFormat(cmd)))
	if done, err := emitStructured(cmd, PolishUndoResult{
		Path:          rel,
		Reverted:      reverted,
		RestoredPaths: restoredPaths,
	}); done {
		return err
	}
	return nil
}
