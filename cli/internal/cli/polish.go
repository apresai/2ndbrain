package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var polishCmd = &cobra.Command{
	Use:   "polish <path>",
	Short: "AI copy-edit a document and return the proposed revision",
	Long: `Runs the configured AI generation provider over the document body to fix
spelling, grammar, and awkward phrasing while preserving voice, wikilinks,
and structure. Emits JSON with both the original and polished text so the
editor can present a diff and let the user accept, edit, or reject.

With --links, it also weaves [[wikilinks]] to existing vault notes where the
prose warrants it (grounded to real notes, it never invents a target). With
--write, the polished body is applied in place and a snapshot of the original
is kept so the change can be reverted with --undo.`,
	Args: cobra.ExactArgs(1),
	RunE: runPolish,
}

var polishSystemFlag string
var polishMaxTokens int
var polishWrite bool
var polishLinks bool
var polishRepairLinks bool
var polishUndo bool
var polishForce bool

func init() {
	polishCmd.GroupID = "ai"
	polishCmd.Flags().StringVar(&polishSystemFlag, "system", "", "Override the default copy-editor system prompt")
	polishCmd.Flags().IntVar(&polishMaxTokens, "max-tokens", 4096, "Maximum tokens for the generated response")
	polishCmd.Flags().BoolVar(&polishWrite, "write", false, "Apply the polished body to the document in place (opt-in; default prints a diff preview only)")
	polishCmd.Flags().BoolVar(&polishLinks, "links", false, "Also add grounded [[wikilinks]] to existing vault notes (never invents targets)")
	polishCmd.Flags().BoolVar(&polishRepairLinks, "repair-links", false, "Repair broken [[wikilinks]] to existing notes (case/whitespace/alias drift; never guesses an ambiguous target)")
	polishCmd.Flags().BoolVar(&polishUndo, "undo", false, "Restore the pre-polish version of <path> from the latest polish snapshot")
	polishCmd.Flags().BoolVar(&polishForce, "force", false, "With --undo: restore even if the file changed since it was polished")
	rootCmd.AddCommand(polishCmd)
}

// PolishResult is the JSON payload returned by `2nb polish`.
type PolishResult struct {
	Path          string              `json:"path"`
	Original      string              `json:"original"`
	Polished      string              `json:"polished"`
	Provider      string              `json:"provider"`
	Model         string              `json:"model"`
	DurationMs    int64               `json:"duration_ms"`
	LinksAdded    []string            `json:"links_added,omitempty"`
	LinksRepaired []polish.LinkRepair `json:"links_repaired,omitempty"`
	LinksSkipped  []polish.LinkRepair `json:"links_skipped,omitempty"`
	Warning       string              `json:"warning,omitempty"`
}

// PolishUndoResult is the JSON payload returned by `2nb polish <path> --undo`.
type PolishUndoResult struct {
	Path     string `json:"path"`
	Reverted bool   `json:"reverted"`
}

func runPolish(cmd *cobra.Command, args []string) error {
	if polishUndo {
		if polishWrite || polishLinks || polishRepairLinks || polishSystemFlag != "" {
			return exitWithError(ExitValidation, "error: --undo cannot be combined with --write, --links, --repair-links, or --system")
		}
		return runPolishUndo(cmd, args)
	}

	// --write mutates the document on disk, so it opens the write path that also
	// pins the active vault (matching meta/append). The default preview path is
	// read-only.
	var v *vault.Vault
	var err error
	if polishWrite {
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
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("resolve doc path: %w", err)
	}

	parsed, err := document.ParseFile(absPath)
	if err != nil {
		return fmt.Errorf("parse source: %w", err)
	}
	originalBody := parsed.Body

	// Fail early on read-only synthetic types when --write is set: writeBody
	// would refuse anyway, but spending an AI call first would be wasteful and
	// the error should be clear before any provider work.
	if polishWrite && document.IsReadOnlyType(parsed.Type) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: cannot apply --write to a read-only %s file (%s); .canvas/.base files are indexed read-only", parsed.Type, rel))
	}

	var warnings []string
	var repaired, skippedRepairs []polish.LinkRepair

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
		systemPrompt = polish.DefaultPolishSystem
	}

	userMessage := originalBody
	var candidates []polish.LinkCandidate
	if polishLinks {
		candidates, systemPrompt, userMessage, warnings = preparePolishLinks(ctx, v, parsed, rel, systemPrompt, originalBody, warnings)
	}

	opts := ai.GenOpts{
		Temperature:  ai.Ptr(0.2),
		MaxTokens:    polishMaxTokens,
		SystemPrompt: systemPrompt,
	}

	start := time.Now()
	polished, err := generator.Generate(ctx, userMessage, opts)
	if err != nil {
		return fmt.Errorf("polish generation failed: %w", err)
	}
	polished = strings.TrimSpace(polished)

	var linksAdded []string
	if polishLinks {
		// Deterministic backstop: drop any link the model produced to a target
		// that is not a real, offered candidate (or an already-present link).
		allowed := polish.AllowedLinkSet(candidates, originalBody)
		var removed []string
		polished, removed = polish.StripInventedLinks(polished, allowed)
		if len(removed) > 0 {
			warnings = append(warnings, fmt.Sprintf("dropped %d link(s) to notes that don't exist", len(removed)))
		}
		for _, l := range polish.NewLinks(originalBody, polished) {
			linksAdded = append(linksAdded, l.Target)
		}
	}

	// Repair broken [[wikilinks]] as the deterministic LAST step, on the
	// copy-edited body. Running it after generation (rather than before)
	// guarantees the fixes actually land regardless of how the model reproduced
	// the text, and makes the reported repairs reflect the body that gets written.
	// Repair only ever rewrites genuinely-broken bare links to existing notes, so
	// it won't touch the grounded links --links just added (those resolve).
	if polishRepairLinks {
		rr, rerr := polish.RepairBrokenLinks(v, polished)
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

	result := PolishResult{
		Path:          rel,
		Original:      originalBody,
		Polished:      polished,
		Provider:      cfg.Provider,
		Model:         cfg.GenerationModel,
		DurationMs:    time.Since(start).Milliseconds(),
		LinksAdded:    linksAdded,
		LinksRepaired: repaired,
		LinksSkipped:  skippedRepairs,
		Warning:       strings.Join(warnings, "; "),
	}

	// --write applies the polished text to the document body in place via the
	// shared body-write path (atomic temp+rename, reindex, re-embed), after
	// snapshotting the original so the change can be reverted with --undo. The
	// PolishResult is still emitted so JSON consumers keep original + polished
	// for audit; without --write the behavior is unchanged (preview only).
	if polishWrite {
		// Capture the original BEFORE overwriting so --undo can restore it. If we
		// can't read a non-empty original, skip the snapshot rather than record an
		// empty one: a later --undo would otherwise classify the file as clean and
		// restore the empty content over the note, losing it.
		originalFull, readErr := os.ReadFile(absPath)
		canSnapshot := readErr == nil && len(originalFull) > 0
		if !canSnapshot {
			warnings = append(warnings, "could not snapshot the original; undo will be unavailable for this polish")
		}
		parsed.Body = polished
		parsed.Path = rel
		if err := writeBody(v, parsed, absPath); err != nil {
			return err
		}
		if canSnapshot {
			written, _ := os.ReadFile(absPath)
			snap := polish.PolishSnapshot{
				Path:          rel,
				OriginalFull:  string(originalFull),
				PolishedBody:  polished,
				Provider:      cfg.Provider,
				Model:         cfg.GenerationModel,
				Links:         polishLinks,
				Timestamp:     time.Now().UTC().Format(time.RFC3339),
				PostWriteHash: polish.HashContent(written),
			}
			if err := polish.WriteSnapshot(v, snap); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to write undo snapshot: %v", err))
			}
		}
		fmt.Fprintf(os.Stderr, "Wrote polished body to %s (%s / %s)\n", rel, cfg.Provider, cfg.GenerationModel)
	}

	// Recompute the warning summary so snapshot/undo issues raised during --write
	// reach the JSON envelope (and the Obsidian plugin), not just stderr.
	result.Warning = strings.Join(warnings, "; ")
	if result.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", result.Warning)
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
	if len(linksAdded) > 0 {
		fmt.Printf("Added links: %s\n", strings.Join(linksAdded, ", "))
	}
	for _, r := range repaired {
		fmt.Printf("Repaired link: [[%s]] -> [[%s]]\n", r.Raw, r.NewTarget)
	}
	fmt.Println()
	fmt.Println(result.Polished)
	return nil
}

// preparePolishLinks gathers grounded link candidates and returns the augmented
// system prompt (with link instructions) and user message (with the candidate
// block), plus any degradation warnings.
func preparePolishLinks(ctx context.Context, v *vault.Vault, parsed *document.Document, rel, systemPrompt, originalBody string, warnings []string) ([]polish.LinkCandidate, string, string, []string) {
	cfg := v.Config.AI
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	if embErr != nil {
		embedder = nil
	}

	var docIDs []string
	var embeddings [][]float32
	warnMsg := ""
	if ready, msg := VectorCompat(ctx, v, embedder); ready {
		docIDs, embeddings, _ = v.DB.AllEmbeddings()
	} else {
		warnMsg = msg // empty for a zero-embedding vault; substring matching still runs
		embedder = nil
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

	return cands, systemPrompt + polish.LinkInstructions, polish.BuildPolishUserMessage(originalBody, cands), warnings
}

func runPolishUndo(cmd *cobra.Command, args []string) error {
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

	switch polish.ClassifyUndo(current, snap) {
	case polish.UndoConflict:
		if !polishForce {
			return exitWithError(ExitValidation, fmt.Sprintf("error: %s changed since it was polished; re-run with `--undo --force` to discard those edits and restore the pre-polish version", rel))
		}
	case polish.UndoNoop:
		_ = polish.DeleteSnapshot(v, rel)
		fmt.Fprintf(os.Stderr, "%s is already at its pre-polish version; nothing to undo\n", rel)
		return emitUndoResult(cmd, rel, false)
	}

	if err := restorePolishOriginal(v, absPath, rel, []byte(snap.OriginalFull)); err != nil {
		return err
	}
	_ = polish.DeleteSnapshot(v, rel)
	fmt.Fprintf(os.Stderr, "Reverted %s to its pre-polish version\n", rel)
	return emitUndoResult(cmd, rel, true)
}

// restorePolishOriginal writes the snapshot's original bytes back verbatim
// (atomic temp+rename, no Serialize round-trip → byte-exact frontmatter), then
// reindexes and re-embeds the restored document.
func restorePolishOriginal(v *vault.Vault, absPath, rel string, content []byte) error {
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, absPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
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

func emitUndoResult(cmd *cobra.Command, rel string, reverted bool) error {
	if getFormat(cmd) == output.FormatJSON {
		data, _ := json.Marshal(PolishUndoResult{Path: rel, Reverted: reverted})
		fmt.Println(string(data))
	}
	return nil
}
