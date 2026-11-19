package polish

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
	"gopkg.in/yaml.v3"
)

const fixBatchVersion = 1

var (
	commonNoteStatuses   = []string{"active", "reference", "archived"}
	safeWidenNoteEnumSet = map[string]bool{
		"active":    true,
		"reference": true,
		"archived":  true,
		"published": true,
		"done":      true,
	}
)

// FixLinkAction records one link transformation performed (or planned) inside a note.
type FixLinkAction struct {
	Action string `json:"action"` // "relink" | "unlink" | "format"
	From   string `json:"from"`
	To     string `json:"to,omitempty"`
	Count  int    `json:"count"`
	Reason string `json:"reason"` // "drift" | "moved_path" | "obvious_match" | "dead_link" | "link_syntax"
}

// FixFieldChange records one surgical frontmatter fix performed (or planned) on a note.
type FixFieldChange struct {
	Field  string `json:"field"`
	From   string `json:"from,omitempty"`
	To     string `json:"to"`
	Reason string `json:"reason"` // "unquote_date" | "missing_required"
}

// FixNoteChange summarizes all fixes applied (or planned) for a single note.
type FixNoteChange struct {
	Path             string           `json:"path"`
	LinkActions      []FixLinkAction  `json:"link_actions"`
	FrontmatterFixes []FixFieldChange `json:"frontmatter_fixes"`
}

// FixAmbiguousLink identifies a broken link with multiple matching candidate notes
// that requires explicit human selection rather than automatic guessing.
type FixAmbiguousLink struct {
	Path       string   `json:"path"`
	Target     string   `json:"target"`
	Candidates []string `json:"candidates"`
}

// AIFixableIssue identifies an issue that deterministic Stage A rules cannot
// resolve and that AI-assisted repair (`2nb fix --ai` / `2nb fix --write`)
// targets.
type AIFixableIssue struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"` // "parse_error" | "schema_enum" | "missing_required" | "broken_link" | "formatting"
	Message string `json:"message"`
	Target  string `json:"target,omitempty"`
}

// FixAIUsage accumulates token and call counts across Stage B and Stage C for
// metering in `.2ndbrain/metrics.db`.
type FixAIUsage struct {
	GenCalls      int
	GenInTokens   int
	GenOutTokens  int
	EmbedCalls    int
	EmbedInTokens int
}

// FixAIProviders carries resolved AI providers for `FixVaultWithOptions`.
type FixAIProviders struct {
	Generator     ai.GenerationProvider
	Embedder      ai.EmbeddingProvider
	EmbedderReady bool
	ProviderName  string
	ModelName     string
	Warnings      []string
}

// FixOptions configures `FixVaultWithOptions`.
type FixOptions struct {
	Write           bool
	AI              bool
	NoAI            bool
	Providers       *FixAIProviders
	ResolveAI       func(ctx context.Context) (*FixAIProviders, string, error)
	ReasoningEffort string
	Threshold       float64
}

// FixVaultReport summarizes what `2nb fix` would change (preview) or wrote (`--write`).
type FixVaultReport struct {
	FilesScanned         int                `json:"files_scanned"`
	NotesChanged         int                `json:"notes_changed"`
	TotalFixes           int                `json:"total_fixes"`
	SchemaUpdated        bool               `json:"schema_updated"`
	SchemaStatusesAdded  []string           `json:"schema_statuses_added"`
	SchemaIssuesResolved int                `json:"schema_issues_resolved"`
	LinksUpdated         int                `json:"links_updated"`
	LinksUnlinked        int                `json:"links_unlinked"`
	FormattingFixed      int                `json:"formatting_fixed"`
	Written              bool               `json:"written"`
	UndoAvailable        bool               `json:"undo_available"`
	Notes                []FixNoteChange    `json:"notes"`
	Ambiguous            []FixAmbiguousLink `json:"ambiguous"`

	AIFixableCount  int              `json:"ai_fixable_count"`
	AIFixableIssues []AIFixableIssue `json:"ai_fixable_issues"`
	AIApplied       bool             `json:"ai_applied"`
	AIFixesApplied  int              `json:"ai_fixes_applied"`
	AIProvider      string           `json:"ai_provider,omitempty"`
	AIModel         string           `json:"ai_model,omitempty"`
	AISkippedReason string           `json:"ai_skipped_reason,omitempty"`
	Warnings        []string         `json:"warnings"`
	AIUsage         FixAIUsage       `json:"-"`
}

// FixUndoResult reports the outcome of `2nb fix --undo`.
type FixUndoResult struct {
	Reverted       bool     `json:"reverted"`
	FilesRestored  int      `json:"files_restored"`
	SchemaRestored bool     `json:"schema_restored"`
	Paths          []string `json:"paths"`
}

type fixSchemaSnapshot struct {
	Existed         bool   `json:"existed"`
	OriginalContent string `json:"original_content,omitempty"`
	PostWriteHash   string `json:"post_write_hash"`
}

type fixFileSnapshot struct {
	Path          string `json:"path"`
	OriginalFull  string `json:"original_full_content"`
	PostWriteHash string `json:"post_write_hash"`
}

type fixBatchSnapshot struct {
	Version   int                `json:"version"`
	Timestamp string             `json:"timestamp"`
	Schema    *fixSchemaSnapshot `json:"schema,omitempty"`
	Files     []fixFileSnapshot  `json:"files"`
}

func fixBatchPath(v *vault.Vault) string {
	return filepath.Join(v.Root, ".2ndbrain", "recovery", "fix-last.json")
}

// HasFixUndo reports whether a batch undo snapshot from `2nb fix --write` exists.
func HasFixUndo(v *vault.Vault) bool {
	if v == nil {
		return false
	}
	_, err := os.Stat(fixBatchPath(v))
	return err == nil
}

// BasenameOrTarget returns the normalized final segment of a link target without
// its .md extension or percent-encoding.
func BasenameOrTarget(target string) string {
	t := strings.ReplaceAll(strings.TrimSpace(target), "\\", "/")
	t = strings.TrimPrefix(t, "/")
	t = document.DecodeLinkTarget(t)
	if len(t) > 3 && strings.EqualFold(t[len(t)-3:], ".md") {
		t = t[:len(t)-3]
	}
	t = strings.TrimSpace(t)
	if strings.Contains(t, "/") {
		return basenameNoExt(t)
	}
	return t
}

// IsObviousTargetMatch reports whether a broken link target unambiguously
// reflects a leading slug/prefix or high-overlap multi-word rename of the
// candidate note's basename or title, while rejecting low-overlap tangential
// matches (e.g. a 2-word target matching a 9-word unrelated note).
func IsObviousTargetMatch(target, title, path string) bool {
	clean := BasenameOrTarget(target)
	if clean == "" || isNonNoteAsset(clean) {
		return false
	}
	targetWords := strings.Fields(NormalizeName(clean))
	if len(targetWords) == 0 {
		return false
	}

	if matchesObviousName(targetWords, basenameNoExt(path)) {
		return true
	}
	if title != "" && matchesObviousName(targetWords, title) {
		return true
	}
	return false
}

func matchesObviousName(targetWords []string, candName string) bool {
	candWords := strings.Fields(NormalizeName(candName))
	if len(candWords) == 0 {
		return false
	}
	if len(targetWords) == 1 {
		return len(targetWords[0]) >= 4 && candWords[0] == targetWords[0]
	}
	candSet := make(map[string]bool, len(candWords))
	for _, w := range candWords {
		candSet[w] = true
	}
	for _, w := range targetWords {
		if !candSet[w] {
			return false
		}
	}
	if len(targetWords)*2 >= len(candWords) {
		return true
	}
	if len(candWords) >= len(targetWords) {
		prefixMatch := true
		for i, w := range targetWords {
			if candWords[i] != w {
				prefixMatch = false
				break
			}
		}
		if prefixMatch {
			return true
		}
	}
	return false
}

func docMatchesWholeWords(targetWords []string, doc store.DocInfo, docAliases []string) bool {
	if len(targetWords) == 0 {
		return false
	}
	hasAllWords := func(name string) bool {
		if name == "" {
			return false
		}
		words := make(map[string]bool)
		for _, w := range strings.Fields(NormalizeName(name)) {
			words[w] = true
		}
		for _, tw := range targetWords {
			if !words[tw] {
				return false
			}
		}
		return true
	}
	if hasAllWords(basenameNoExt(doc.Path)) || hasAllWords(doc.Title) {
		return true
	}
	for _, a := range docAliases {
		if hasAllWords(a) {
			return true
		}
	}
	return false
}

// FixVault inspects and deterministically repairs vault-wide issues without AI.
// Previews by default; with write == true applies all changes atomically and
// saves a full reversible batch snapshot to `.2ndbrain/recovery/fix-last.json`.
func FixVault(v *vault.Vault, write bool) (*FixVaultReport, error) {
	return FixVaultWithOptions(context.Background(), v, FixOptions{Write: write, NoAI: true})
}

// FixVaultWithOptions runs deterministic Stage A repair and, when enabled by
// opts, AI-assisted Stage B (parse/schema/date) and Stage C (broken-link
// recovery + deferred Tier-4 unwrap).
func FixVaultWithOptions(ctx context.Context, v *vault.Vault, opts FixOptions) (*FixVaultReport, error) {
	if v == nil {
		return nil, errors.New("fix: nil vault")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	report := &FixVaultReport{
		UndoAvailable:       HasFixUndo(v),
		SchemaStatusesAdded: []string{},
		Notes:               []FixNoteChange{},
		Ambiguous:           []FixAmbiguousLink{},
		AIFixableIssues:     []AIFixableIssue{},
		Warnings:            []string{},
	}

	docs, aliases, err := vault.CollectLiveDocs(v.Root)
	if err != nil {
		return nil, fmt.Errorf("walk live vault docs: %w", err)
	}
	resolver := store.NewResolver(docs, aliases)
	repairIdx := NewRepairIndex(docs, aliases)
	canonicalTargetForPath := buildCanonicalTargetLookup(docs)

	docAliasesByPath := make(map[string][]string, len(docs))
	for alias, paths := range aliases {
		for _, p := range paths {
			docAliasesByPath[p] = append(docAliasesByPath[p], alias)
		}
	}

	mdFiles, err := collectFixMarkdownFiles(v)
	if err != nil {
		return nil, err
	}

	var noteStates []*fixNoteState
	for _, abs := range mdFiles {
		rel := v.RelPath(abs)
		orig, rerr := os.ReadFile(abs)
		if rerr != nil {
			continue
		}
		if vault.HasTemplatePlaceholders(orig) {
			continue
		}
		doc, perr := document.Parse(rel, orig)
		if perr != nil {
			report.FilesScanned++
			noteStates = append(noteStates, &fixNoteState{
				absPath:      abs,
				relPath:      rel,
				origBytes:    orig,
				currentBytes: orig,
				parseError:   true,
				parseErrMsg:  perr.Error(),
				change: FixNoteChange{
					Path:             rel,
					LinkActions:      []FixLinkAction{},
					FrontmatterFixes: []FixFieldChange{},
				},
			})
			continue
		}
		if doc.Frontmatter == nil && document.HasUnclosedFrontmatter(orig) {
			report.FilesScanned++
			noteStates = append(noteStates, &fixNoteState{
				absPath:       abs,
				relPath:       rel,
				origBytes:     orig,
				currentBytes:  orig,
				parseError:    true,
				unclosedFence: true,
				parseErrMsg:   "unclosed YAML frontmatter fence",
				change: FixNoteChange{
					Path:             rel,
					LinkActions:      []FixLinkAction{},
					FrontmatterFixes: []FixFieldChange{},
				},
			})
			continue
		}
		report.FilesScanned++
		if document.IsReadOnlyType(doc.Type) {
			continue
		}
		noteStates = append(noteStates, &fixNoteState{
			absPath:      abs,
			relPath:      rel,
			origBytes:    orig,
			currentBytes: orig,
			doc:          doc,
			currentBody:  doc.Body,
			change: FixNoteChange{
				Path:             rel,
				LinkActions:      []FixLinkAction{},
				FrontmatterFixes: []FixFieldChange{},
			},
		})
	}

	// Pass 1: clone schemas and plan `.2ndbrain/schemas.yaml` common note status widening.
	effectiveSchemas, err := cloneSchemaSet(v.Schemas)
	if err != nil {
		return nil, fmt.Errorf("clone schemas: %w", err)
	}
	var addedStatuses []string
	var schemaIssuesResolved int
	if noteSchema, ok := effectiveSchemas.Types["note"]; ok {
		if statusDef, hasStatus := noteSchema.Fields["status"]; hasStatus && len(statusDef.Enum) > 0 {
			existing := make(map[string]bool, len(statusDef.Enum))
			for _, e := range statusDef.Enum {
				existing[e] = true
			}
			sawNonStandard := false
			extraSeen := make(map[string]bool)
			for _, ns := range noteStates {
				if ns.parseError || ns.doc == nil || ns.doc.Type != "note" {
					continue
				}
				val, ok := document.ScalarText(ns.doc.Frontmatter["status"])
				if !ok || val == "" || existing[val] {
					continue
				}
				if safeWidenNoteEnumSet[val] {
					sawNonStandard = true
					extraSeen[val] = true
					schemaIssuesResolved++
				}
			}
			if sawNonStandard {
				candidates := append([]string{}, commonNoteStatuses...)
				for _, extra := range []string{"published", "done"} {
					if extraSeen[extra] {
						candidates = append(candidates, extra)
					}
				}
				for _, st := range candidates {
					if !existing[st] {
						statusDef.Enum = append(statusDef.Enum, st)
						addedStatuses = append(addedStatuses, st)
						existing[st] = true
					}
				}
				noteSchema.Fields["status"] = statusDef
				effectiveSchemas.Types["note"] = noteSchema
				report.SchemaUpdated = true
				report.SchemaStatusesAdded = addedStatuses
				report.SchemaIssuesResolved = schemaIssuesResolved
			}
		}
	}

	deferUnlink := !opts.NoAI && (opts.AI || opts.Write)
	keepUnlinkedDeferred := !opts.Write && !opts.AI && !opts.NoAI
	for _, ns := range noteStates {
		if ns.parseError || ns.doc == nil {
			continue
		}
		applyStageADeterministicToNote(
			ns, effectiveSchemas, docs, docAliasesByPath, resolver, repairIdx, canonicalTargetForPath, deferUnlink, keepUnlinkedDeferred, report,
		)
	}

	remainingIssues := collectRemainingAIIssues(noteStates, effectiveSchemas)

	if !opts.NoAI && (opts.AI || (opts.Write && len(remainingIssues) > 0)) {
		providers := opts.Providers
		skipReason := ""
		if providers == nil && opts.ResolveAI != nil {
			var rerr error
			providers, skipReason, rerr = opts.ResolveAI(ctx)
			if rerr != nil {
				return nil, rerr
			}
		}
		if providers == nil || providers.Generator == nil {
			if skipReason != "" {
				report.AISkippedReason = skipReason
				report.Warnings = append(report.Warnings, "AI repair skipped: "+skipReason)
			}
			if deferUnlink {
				applyDeferredTier4Fallback(noteStates, report)
			}
		} else {
			report.AIApplied = true
			report.AIProvider = providers.ProviderName
			report.AIModel = providers.ModelName
			if len(providers.Warnings) > 0 {
				report.Warnings = append(report.Warnings, providers.Warnings...)
			}

			// Stage B Path 1: repair parse errors & unclosed fences.
			stageBRepaired := false
			for _, ns := range noteStates {
				if !ns.parseError {
					continue
				}
				if repairParseErrorNote(ctx, ns, effectiveSchemas, providers.Generator, opts.ReasoningEffort, &report.AIUsage, &report.Warnings) {
					stageBRepaired = true
					report.AIFixesApplied++
					// Apply Stage A deterministic fixes (links + dates + required title/status) to newly parsed note.
					applyStageADeterministicToNote(
						ns, effectiveSchemas, docs, docAliasesByPath, resolver, repairIdx, canonicalTargetForPath, true, true, report,
					)
				}
			}

			// Stage B Path 2: repair remaining schema enum, missing required, and unparseable/non-scalar date issues.
			for _, ns := range noteStates {
				if ns.parseError || ns.doc == nil {
					continue
				}
				if n := repairNoteSchemaAndDatesAI(
					ctx, ns, effectiveSchemas, providers.Generator, opts.ReasoningEffort, &report.AIUsage, &report.Warnings,
				); n > 0 {
					stageBRepaired = true
					report.AIFixesApplied += n
				}
			}

			// Refresh live docs & resolver in memory from repaired noteStates so
			// Stage C immediately sees titles/aliases restored in Stage B (even
			// in --ai preview mode before any disk write occurs).
			if stageBRepaired {
				docs, aliases = refreshLiveDocsFromStates(docs, aliases, noteStates)
				resolver = store.NewResolver(docs, aliases)
				repairIdx = NewRepairIndex(docs, aliases)
				canonicalTargetForPath = buildCanonicalTargetLookup(docs)
			}

			// Stage C: AI broken-link recovery + deferred Tier-4 dead-link unwrap.
			runStageCLinkRecovery(
				ctx, v, noteStates, docs, aliases, resolver, repairIdx, canonicalTargetForPath, providers, opts, report,
			)
		}
	} else if opts.NoAI && len(remainingIssues) > 0 {
		report.AISkippedReason = "disabled via --no-ai"
	}

	// Recompute remaining unresolved AI-fixable issues after all stages.
	report.AIFixableIssues = collectRemainingAIIssues(noteStates, effectiveSchemas)
	report.AIFixableCount = len(report.AIFixableIssues)

	type pendingNoteWrite struct {
		absPath   string
		relPath   string
		origBytes []byte
		newBytes  []byte
		newBody   string
		change    FixNoteChange
	}
	var pendingWrites []pendingNoteWrite

	for _, ns := range noteStates {
		if len(ns.change.LinkActions) == 0 && len(ns.change.FrontmatterFixes) == 0 {
			continue
		}
		if string(ns.currentBytes) == string(ns.origBytes) {
			continue
		}
		report.Notes = append(report.Notes, ns.change)
		pendingWrites = append(pendingWrites, pendingNoteWrite{
			absPath:   ns.absPath,
			relPath:   ns.relPath,
			origBytes: ns.origBytes,
			newBytes:  ns.currentBytes,
			newBody:   ns.currentBody,
			change:    ns.change,
		})
	}

	report.NotesChanged = len(report.Notes)
	report.TotalFixes = report.LinksUpdated + report.LinksUnlinked + report.FormattingFixed
	for _, n := range report.Notes {
		report.TotalFixes += len(n.FrontmatterFixes)
	}
	if report.SchemaUpdated {
		if report.SchemaIssuesResolved > 0 {
			report.TotalFixes += report.SchemaIssuesResolved
		} else {
			report.TotalFixes++
		}
	}

	if !opts.Write || (report.TotalFixes == 0 && !report.SchemaUpdated) {
		return report, nil
	}

	// Capture snapshot & apply writes atomically.
	now := time.Now().UTC().Format(time.RFC3339)
	batch := fixBatchSnapshot{
		Version:   fixBatchVersion,
		Timestamp: now,
		Files:     make([]fixFileSnapshot, 0, len(pendingWrites)),
	}

	if report.SchemaUpdated {
		schemaFile := filepath.Join(v.Root, ".2ndbrain", "schemas.yaml")
		origSchema, readErr := os.ReadFile(schemaFile)
		schemaSnap := &fixSchemaSnapshot{Existed: readErr == nil}
		if readErr == nil {
			schemaSnap.OriginalContent = string(origSchema)
		}
		schemaBytes, merr := yaml.Marshal(effectiveSchemas)
		if merr != nil {
			return nil, fmt.Errorf("marshal widened schemas: %w", merr)
		}
		if werr := writeFileAtomic(schemaFile, schemaBytes); werr != nil {
			return nil, fmt.Errorf("write widened schemas.yaml: %w", werr)
		}
		schemaSnap.PostWriteHash = HashContent(schemaBytes)
		batch.Schema = schemaSnap
		v.Schemas = effectiveSchemas
	}

	for _, pw := range pendingWrites {
		if werr := writeFileAtomic(pw.absPath, pw.newBytes); werr != nil {
			return nil, fmt.Errorf("write %s: %w", pw.relPath, werr)
		}
		postHash := HashContent(pw.newBytes)
		batch.Files = append(batch.Files, fixFileSnapshot{
			Path:          pw.relPath,
			OriginalFull:  string(pw.origBytes),
			PostWriteHash: postHash,
		})
		_ = WriteSnapshot(v, PolishSnapshot{
			Path:          pw.relPath,
			OriginalFull:  string(pw.origBytes),
			PolishedBody:  pw.newBody,
			Provider:      "fix",
			Timestamp:     now,
			PostWriteHash: postHash,
		})
		if v.DB != nil {
			if ierr := vault.IndexSingleFile(v, pw.absPath); ierr != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("reindex %s: %v", pw.relPath, ierr))
			}
		}
	}
	if v.DB != nil && len(pendingWrites) > 0 {
		if rerr := v.DB.ResolveLinks(); rerr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("resolve links: %v", rerr))
		}
	}

	if berr := writeFixBatchSnapshot(v, batch); berr != nil {
		return nil, fmt.Errorf("write fix undo snapshot: %w", berr)
	}

	report.Written = true
	report.UndoAvailable = true
	return report, nil
}

func applyStageADeterministicToNote(
	ns *fixNoteState,
	effectiveSchemas *vault.SchemaSet,
	docs []store.DocInfo,
	docAliasesByPath map[string][]string,
	resolver *store.Resolver,
	repairIdx *RepairIndex,
	canonicalTargetForPath func(path string) (wikiTarget, mdDest string),
	deferUnlink bool,
	keepUnlinkedDeferred bool,
	report *FixVaultReport,
) {
	if ns.doc == nil {
		return
	}
	body, fmtChanges := document.NormalizeLinkFormatting(ns.currentBody, func(target string) bool {
		_, rerr := resolver.Resolve(target)
		return rerr == nil
	})
	for _, fc := range fmtChanges {
		ns.change.LinkActions = append(ns.change.LinkActions, FixLinkAction{
			Action: "format",
			From:   fc.From,
			To:     fc.To,
			Count:  fc.Count,
			Reason: "link_syntax",
		})
		report.FormattingFixed += fc.Count
	}

	body, linkActions, ambLinks, deferred := resolveNoteBrokenLinksWithDefer(
		ns.relPath, body, docs, docAliasesByPath, resolver, repairIdx, canonicalTargetForPath, deferUnlink, keepUnlinkedDeferred,
	)
	for _, la := range linkActions {
		ns.change.LinkActions = append(ns.change.LinkActions, la)
		switch la.Action {
		case "relink":
			report.LinksUpdated += la.Count
		case "unlink":
			report.LinksUnlinked += la.Count
		}
	}
	report.Ambiguous = append(report.Ambiguous, ambLinks...)
	ns.deferredLinks = deferred

	plannedMeta, fieldFixes := planFrontmatterFixes(ns.doc, ns.relPath, effectiveSchemas)

	bodyChanged := body != ns.doc.Body
	metaChanged := len(plannedMeta) > 0
	if !bodyChanged && !metaChanged {
		return
	}

	var outBytes []byte
	var err error
	if metaChanged {
		updated := make(map[string]any, len(ns.doc.Frontmatter)+len(plannedMeta))
		for k, val := range ns.doc.Frontmatter {
			updated[k] = val
		}
		for k, val := range plannedMeta {
			updated[k] = val
		}
		astOut, astErr := document.UpdateDocumentFrontmatterAST(ns.currentBytes, updated, body)
		if astErr == nil {
			if newDoc, perr := document.Parse(ns.relPath, astOut); perr == nil {
				outBytes = astOut
				ns.doc = newDoc
				ns.change.FrontmatterFixes = append(ns.change.FrontmatterFixes, fieldFixes...)
			}
		}
		if outBytes == nil {
			if !bodyChanged {
				return
			}
			outBytes, err = replaceBodyPreservingFrontmatter(ns.currentBytes, ns.doc, ns.absPath, body)
			if err != nil {
				return
			}
			if newDoc, perr := document.Parse(ns.relPath, outBytes); perr == nil {
				ns.doc = newDoc
			}
		}
	} else {
		outBytes, err = replaceBodyPreservingFrontmatter(ns.currentBytes, ns.doc, ns.absPath, body)
		if err != nil {
			return
		}
		if newDoc, perr := document.Parse(ns.relPath, outBytes); perr == nil {
			ns.doc = newDoc
		}
	}
	ns.currentBytes = outBytes
	ns.currentBody = body
}

// UndoLastFix reverts the most recent `2nb fix --write` batch, restoring every
// touched note and `.2ndbrain/schemas.yaml` (if modified) byte-for-byte.
// Refuses without force == true if any restored file was modified externally
// after `2nb fix --write` ran.
func UndoLastFix(v *vault.Vault, force bool) (*FixUndoResult, error) {
	if v == nil {
		return nil, errors.New("undo fix: nil vault")
	}
	batchPath := fixBatchPath(v)
	raw, err := os.ReadFile(batchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("nothing to undo: no `2nb fix --write` snapshot found")
		}
		return nil, fmt.Errorf("read fix snapshot: %w", err)
	}
	var batch fixBatchSnapshot
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("parse fix snapshot: %w", err)
	}

	// Pre-flight check every note and schemas.yaml before mutating disk.
	var conflicts []string
	schemaFile := filepath.Join(v.Root, ".2ndbrain", "schemas.yaml")
	if batch.Schema != nil && !force {
		cur, rerr := os.ReadFile(schemaFile)
		if batch.Schema.Existed {
			if rerr != nil {
				conflicts = append(conflicts, ".2ndbrain/schemas.yaml")
			} else {
				curHash := HashContent(cur)
				origHash := HashContent([]byte(batch.Schema.OriginalContent))
				if curHash != batch.Schema.PostWriteHash && curHash != origHash {
					conflicts = append(conflicts, ".2ndbrain/schemas.yaml")
				}
			}
		} else if rerr == nil && HashContent(cur) != batch.Schema.PostWriteHash {
			conflicts = append(conflicts, ".2ndbrain/schemas.yaml")
		}
	}

	if !force {
		for _, f := range batch.Files {
			abs := filepath.Join(v.Root, filepath.FromSlash(f.Path))
			cur, rerr := os.ReadFile(abs)
			if rerr != nil {
				conflicts = append(conflicts, f.Path)
				continue
			}
			curHash := HashContent(cur)
			origHash := HashContent([]byte(f.OriginalFull))
			if curHash != f.PostWriteHash && curHash != origHash {
				conflicts = append(conflicts, f.Path)
			}
		}
	}

	if len(conflicts) > 0 {
		return nil, fmt.Errorf("refusing undo: %s modified since last fix (pass --force to overwrite)", strings.Join(conflicts, ", "))
	}

	res := &FixUndoResult{
		Reverted: true,
		Paths:    make([]string, 0, len(batch.Files)),
	}

	if batch.Schema != nil {
		if batch.Schema.Existed {
			if werr := writeFileAtomic(schemaFile, []byte(batch.Schema.OriginalContent)); werr != nil {
				return nil, fmt.Errorf("restore schemas.yaml: %w", werr)
			}
		} else {
			if rerr := os.Remove(schemaFile); rerr != nil && !os.IsNotExist(rerr) {
				return nil, fmt.Errorf("remove generated schemas.yaml: %w", rerr)
			}
		}
		if reloaded, lerr := vault.LoadSchemas(filepath.Join(v.Root, ".2ndbrain")); lerr == nil {
			v.Schemas = reloaded
		}
		res.SchemaRestored = true
	}

	for _, f := range batch.Files {
		abs := filepath.Join(v.Root, filepath.FromSlash(f.Path))
		if werr := writeFileAtomic(abs, []byte(f.OriginalFull)); werr != nil {
			return nil, fmt.Errorf("restore %s: %w", f.Path, werr)
		}
		_ = DeleteSnapshot(v, f.Path)
		if v.DB != nil {
			if ierr := vault.IndexSingleFile(v, abs); ierr != nil {
				if errors.Is(ierr, vault.ErrUnparseable) {
					_ = v.DB.DeleteDocumentByPath(f.Path)
				}
			}
		}
		res.FilesRestored++
		res.Paths = append(res.Paths, f.Path)
	}
	if v.DB != nil && len(batch.Files) > 0 {
		_ = v.DB.ResolveLinks()
	}

	if rerr := os.Remove(batchPath); rerr != nil && !os.IsNotExist(rerr) {
		return nil, fmt.Errorf("remove fix undo manifest: %w", rerr)
	}
	return res, nil
}

func resolveNoteBrokenLinksWithDefer(
	relPath string,
	body string,
	docs []store.DocInfo,
	docAliasesByPath map[string][]string,
	resolver *store.Resolver,
	repairIdx *RepairIndex,
	canonicalTargetForPath func(path string) (wikiTarget, mdDest string),
	deferUnlink bool,
	keepUnlinkedDeferred bool,
) (string, []FixLinkAction, []FixAmbiguousLink, []deferredBrokenLink) {
	var actions []FixLinkAction
	var ambiguous []FixAmbiguousLink
	var deferred []deferredBrokenLink
	handled := make(map[string]bool)

	for _, link := range document.ExtractWikiLinks(body) {
		target := strings.TrimSpace(link.Target)
		if target == "" || handled[target] || isNonNoteAsset(target) {
			continue
		}
		_, rerr := resolver.Resolve(target)
		if rerr == nil {
			continue
		}
		var ambErr *store.AmbiguousTargetError
		if errors.As(rerr, &ambErr) {
			handled[target] = true
			cands := append([]string{}, ambErr.Candidates...)
			ambiguous = append(ambiguous, FixAmbiguousLink{
				Path:       relPath,
				Target:     target,
				Candidates: cands,
			})
			deferred = append(deferred, deferredBrokenLink{
				target:         target,
				isAmbiguous:    true,
				seedCandidates: cands,
			})
			continue
		}
		if !errors.Is(rerr, store.ErrTargetNotFound) {
			continue
		}
		handled[target] = true

		slashNorm := strings.TrimPrefix(strings.ReplaceAll(target, "\\", "/"), "/")
		slashNorm = strings.TrimSuffix(slashNorm, ".md")
		hasDir := strings.Contains(slashNorm, "/")

		// Tier 1: Exact normalized drift on bare targets.
		if !hasDir {
			driftCands := repairIdx.Lookup(target)
			normMatches := findDocsByNormName(foldKey(target), docs, docAliasesByPath)
			switch len(driftCands) {
			case 1:
				if wikiDest, mdDest, ok := resolveDriftTargetSafe(target, driftCands[0], docs, docAliasesByPath, resolver, canonicalTargetForPath); ok {
					if rewritten, n := document.RewriteExactLinkTarget(body, target, wikiDest, mdDest); n > 0 {
						body = rewritten
						actions = append(actions, FixLinkAction{
							Action: "relink", From: target, To: wikiDest, Count: n, Reason: "drift",
						})
					}
					continue
				}
				if len(normMatches) > 1 {
					ambiguous = append(ambiguous, FixAmbiguousLink{
						Path: relPath, Target: target, Candidates: normMatches,
					})
					deferred = append(deferred, deferredBrokenLink{
						target:         target,
						isAmbiguous:    true,
						seedCandidates: normMatches,
					})
					continue
				}
			case 0:
				if len(normMatches) == 1 {
					wikiDest, mdDest := safeWikiDestForPath(resolver, canonicalTargetForPath, normMatches[0])
					if rewritten, n := document.RewriteExactLinkTarget(body, target, wikiDest, mdDest); n > 0 {
						body = rewritten
						actions = append(actions, FixLinkAction{
							Action: "relink", From: target, To: wikiDest, Count: n, Reason: "drift",
						})
					}
					continue
				}
				if len(normMatches) > 1 {
					ambiguous = append(ambiguous, FixAmbiguousLink{
						Path: relPath, Target: target, Candidates: normMatches,
					})
					deferred = append(deferred, deferredBrokenLink{
						target:         target,
						isAmbiguous:    true,
						seedCandidates: normMatches,
					})
					continue
				}
			default:
				seedPaths := resolveCandidatePaths(resolver, driftCands)
				if len(normMatches) > len(seedPaths) {
					seedPaths = normMatches
				}
				ambiguous = append(ambiguous, FixAmbiguousLink{
					Path: relPath, Target: target, Candidates: driftCands,
				})
				deferred = append(deferred, deferredBrokenLink{
					target:         target,
					isAmbiguous:    true,
					seedCandidates: seedPaths,
				})
				continue
			}
		}

		// Tier 2: Moved-folder / stale path update (e.g. [[old/note]] -> new/note).
		matchQuery := target
		if hasDir {
			leaf := basenameNoExt(slashNorm)
			matchQuery = leaf
			if leaf != "" {
				destPath, lerr := resolver.Resolve(leaf)
				var leafAmb *store.AmbiguousTargetError
				switch {
				case lerr == nil:
					wikiDest, mdDest := safeWikiDestForPath(resolver, canonicalTargetForPath, destPath)
					if rewritten, n := document.RewriteExactLinkTarget(body, target, wikiDest, mdDest); n > 0 {
						body = rewritten
						actions = append(actions, FixLinkAction{
							Action: "relink", From: target, To: wikiDest, Count: n, Reason: "moved_path",
						})
					}
					continue
				case errors.As(lerr, &leafAmb):
					cands := append([]string{}, leafAmb.Candidates...)
					ambiguous = append(ambiguous, FixAmbiguousLink{
						Path: relPath, Target: target, Candidates: cands,
					})
					deferred = append(deferred, deferredBrokenLink{
						target:         target,
						isAmbiguous:    true,
						seedCandidates: cands,
					})
					continue
				default:
					leafCands := repairIdx.Lookup(leaf)
					leafNormMatches := findDocsByNormName(foldKey(leaf), docs, docAliasesByPath)
					switch len(leafCands) {
					case 1:
						if wikiDest, mdDest, ok := resolveDriftTargetSafe(leaf, leafCands[0], docs, docAliasesByPath, resolver, canonicalTargetForPath); ok {
							if rewritten, n := document.RewriteExactLinkTarget(body, target, wikiDest, mdDest); n > 0 {
								body = rewritten
								actions = append(actions, FixLinkAction{
									Action: "relink", From: target, To: wikiDest, Count: n, Reason: "moved_path",
								})
							}
							continue
						}
						if len(leafNormMatches) > 1 {
							ambiguous = append(ambiguous, FixAmbiguousLink{
								Path: relPath, Target: target, Candidates: leafNormMatches,
							})
							deferred = append(deferred, deferredBrokenLink{
								target:         target,
								isAmbiguous:    true,
								seedCandidates: leafNormMatches,
							})
							continue
						}
					case 0:
						if len(leafNormMatches) == 1 {
							wikiDest, mdDest := safeWikiDestForPath(resolver, canonicalTargetForPath, leafNormMatches[0])
							if rewritten, n := document.RewriteExactLinkTarget(body, target, wikiDest, mdDest); n > 0 {
								body = rewritten
								actions = append(actions, FixLinkAction{
									Action: "relink", From: target, To: wikiDest, Count: n, Reason: "moved_path",
								})
							}
							continue
						}
						if len(leafNormMatches) > 1 {
							ambiguous = append(ambiguous, FixAmbiguousLink{
								Path: relPath, Target: target, Candidates: leafNormMatches,
							})
							deferred = append(deferred, deferredBrokenLink{
								target:         target,
								isAmbiguous:    true,
								seedCandidates: leafNormMatches,
							})
							continue
						}
					default:
						seedPaths := resolveCandidatePaths(resolver, leafCands)
						if len(leafNormMatches) > len(seedPaths) {
							seedPaths = leafNormMatches
						}
						ambiguous = append(ambiguous, FixAmbiguousLink{
							Path: relPath, Target: target, Candidates: leafCands,
						})
						deferred = append(deferred, deferredBrokenLink{
							target:         target,
							isAmbiguous:    true,
							seedCandidates: seedPaths,
						})
						continue
					}
				}
			}
		}

		// Tier 3: Unique obvious slug-prefix or high-overlap multi-word rename match.
		cleanQuery := BasenameOrTarget(matchQuery)
		queryWords := strings.Fields(NormalizeName(cleanQuery))
		if len(queryWords) > 0 {
			var obviousPaths []string
			var wholeWordPaths []string
			for _, d := range docs {
				if IsObviousTargetMatch(cleanQuery, d.Title, d.Path) {
					obviousPaths = append(obviousPaths, d.Path)
				}
				if docMatchesWholeWords(queryWords, d, docAliasesByPath[d.Path]) {
					wholeWordPaths = append(wholeWordPaths, d.Path)
				}
			}
			if len(obviousPaths) == 1 && len(wholeWordPaths) == 1 && obviousPaths[0] == wholeWordPaths[0] {
				wikiDest, mdDest := safeWikiDestForPath(resolver, canonicalTargetForPath, obviousPaths[0])
				if rewritten, n := document.RewriteExactLinkTarget(body, target, wikiDest, mdDest); n > 0 {
					body = rewritten
					actions = append(actions, FixLinkAction{
						Action: "relink", From: target, To: wikiDest, Count: n, Reason: "obvious_match",
					})
				}
				continue
			}
			if len(obviousPaths) > 1 {
				sort.Strings(obviousPaths)
				ambiguous = append(ambiguous, FixAmbiguousLink{
					Path: relPath, Target: target, Candidates: obviousPaths,
				})
				deferred = append(deferred, deferredBrokenLink{
					target:         target,
					isAmbiguous:    true,
					seedCandidates: obviousPaths,
				})
				continue
			}
		}

		// Tier 4: Missing link failing Tiers 1-3.
		if deferUnlink {
			deferred = append(deferred, deferredBrokenLink{
				target:      target,
				isAmbiguous: false,
			})
		} else {
			rewritten, n := document.UnlinkExactTarget(body, target)
			if n > 0 {
				body = rewritten
				actions = append(actions, FixLinkAction{
					Action: "unlink", From: target, Count: n, Reason: "dead_link",
				})
			}
			if keepUnlinkedDeferred || n == 0 {
				deferred = append(deferred, deferredBrokenLink{
					target:      target,
					isAmbiguous: false,
				})
			}
		}
	}

	return body, actions, ambiguous, deferred
}

func safeWikiDestForPath(
	resolver *store.Resolver,
	canonicalTargetForPath func(path string) (wikiTarget, mdDest string),
	destPath string,
) (wikiDest, mdDest string) {
	wikiDest, mdDest = canonicalTargetForPath(destPath)
	if resolver != nil {
		if resolved, rerr := resolver.Resolve(wikiDest); rerr != nil || resolved != destPath {
			wikiDest = strings.TrimSuffix(destPath, ".md")
		}
		mdDest = MarkdownDestinationFor(destPath, resolver)
	}
	return wikiDest, mdDest
}

func resolveDriftTargetSafe(
	query string,
	cand string,
	docs []store.DocInfo,
	docAliasesByPath map[string][]string,
	resolver *store.Resolver,
	canonicalTargetForPath func(path string) (wikiTarget, mdDest string),
) (wikiDest, mdDest string, ok bool) {
	qNorm := foldKey(query)
	if qNorm != "" {
		if _, uniqueOK := findUniqueDocByNormName(qNorm, docs, docAliasesByPath); !uniqueOK {
			return "", "", false
		}
	}
	destPath, perr := resolver.Resolve(cand)
	if perr != nil || (qNorm != "" && !docMatchesNormName(destPath, qNorm, docs, docAliasesByPath)) {
		if qNorm != "" {
			if actualPath, found := findUniqueDocByNormName(qNorm, docs, docAliasesByPath); found {
				w, m := safeWikiDestForPath(resolver, canonicalTargetForPath, actualPath)
				return w, m, true
			}
		}
		return "", "", false
	}
	return cand, MarkdownDestinationFor(destPath, resolver), true
}

func docMatchesInfoNormName(d store.DocInfo, norm string, docAliasesByPath map[string][]string) bool {
	if NormalizeName(basenameNoExt(d.Path)) == norm {
		return true
	}
	if d.Title != "" && NormalizeName(d.Title) == norm {
		return true
	}
	for _, a := range docAliasesByPath[d.Path] {
		if NormalizeName(a) == norm {
			return true
		}
	}
	return false
}

func docMatchesNormName(path, norm string, docs []store.DocInfo, docAliasesByPath map[string][]string) bool {
	if NormalizeName(basenameNoExt(path)) == norm {
		return true
	}
	for _, d := range docs {
		if d.Path == path && d.Title != "" && NormalizeName(d.Title) == norm {
			return true
		}
	}
	for _, a := range docAliasesByPath[path] {
		if NormalizeName(a) == norm {
			return true
		}
	}
	return false
}

func findDocsByNormName(norm string, docs []store.DocInfo, docAliasesByPath map[string][]string) []string {
	if norm == "" {
		return nil
	}
	var matches []string
	seen := make(map[string]bool)
	for _, d := range docs {
		if seen[d.Path] {
			continue
		}
		if docMatchesInfoNormName(d, norm, docAliasesByPath) {
			seen[d.Path] = true
			matches = append(matches, d.Path)
		}
	}
	sort.Strings(matches)
	return matches
}

func findUniqueDocByNormName(norm string, docs []store.DocInfo, docAliasesByPath map[string][]string) (string, bool) {
	if norm == "" {
		return "", false
	}
	var match string
	for _, d := range docs {
		if docMatchesInfoNormName(d, norm, docAliasesByPath) {
			if match != "" && match != d.Path {
				return "", false
			}
			match = d.Path
		}
	}
	return match, match != ""
}

func refreshLiveDocsFromStates(
	origDocs []store.DocInfo,
	origAliases map[string][]string,
	states []*fixNoteState,
) ([]store.DocInfo, map[string][]string) {
	stateByPath := make(map[string]*fixNoteState, len(states))
	for _, ns := range states {
		if ns.parseError || ns.doc == nil {
			continue
		}
		stateByPath[ns.relPath] = ns
	}

	newDocs := make([]store.DocInfo, 0, len(origDocs))
	seen := make(map[string]bool, len(origDocs))
	for _, d := range origDocs {
		seen[d.Path] = true
		if ns, ok := stateByPath[d.Path]; ok {
			d.Title = ns.doc.Title
		}
		newDocs = append(newDocs, d)
	}
	for _, ns := range states {
		if ns.parseError || ns.doc == nil || seen[ns.relPath] {
			continue
		}
		newDocs = append(newDocs, store.DocInfo{
			ID:    ns.relPath,
			Path:  ns.relPath,
			Title: ns.doc.Title,
		})
	}

	newAliases := make(map[string][]string, len(origAliases))
	for alias, paths := range origAliases {
		for _, p := range paths {
			if stateByPath[p] == nil {
				newAliases[alias] = append(newAliases[alias], p)
			}
		}
	}
	for _, ns := range states {
		if ns.parseError || ns.doc == nil {
			continue
		}
		for _, a := range document.AliasesOf(ns.doc) {
			newAliases[a] = append(newAliases[a], ns.relPath)
		}
	}
	return newDocs, newAliases
}

func resolveCandidatePaths(resolver *store.Resolver, targets []string) []string {
	var paths []string
	seen := make(map[string]bool, len(targets))
	for _, t := range targets {
		if p, err := resolver.Resolve(t); err == nil && p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

func planFrontmatterFixes(doc *document.Document, relPath string, schemas *vault.SchemaSet) (map[string]any, []FixFieldChange) {
	planned := make(map[string]any)
	var fixes []FixFieldChange

	if schemas != nil {
		if schema, ok := schemas.Types[doc.Type]; ok {
			for _, req := range schema.Required {
				if req == "title" {
					val, ok := document.ScalarText(doc.Frontmatter["title"])
					if !ok || strings.TrimSpace(val) == "" {
						title := deriveNoteTitle(doc.Body, relPath)
						planned["title"] = title
						fixes = append(fixes, FixFieldChange{
							Field: "title", To: title, Reason: "missing_required",
						})
					}
				} else if req == "status" {
					rawVal, exists := doc.Frontmatter["status"]
					val, ok := document.ScalarText(rawVal)
					isEmptyScalar := ok && strings.TrimSpace(val) == ""
					isMissingOrEmpty := !exists || rawVal == nil || isEmptyScalar || (!ok && len(schema.Fields["status"].Enum) == 0)
					if isMissingOrEmpty {
						if def := defaultStatusForSchema(schema); def != "" {
							planned["status"] = def
							fixes = append(fixes, FixFieldChange{
								Field: "status", To: def, Reason: "missing_required",
							})
						}
					}
				}
			}
		}
	}

	// Sort frontmatter keys for deterministic reporting.
	keys := make([]string, 0, len(doc.Frontmatter))
	for k := range doc.Frontmatter {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if schemas == nil || !schemas.IsDateField(doc.Type, key) {
			continue
		}
		value := doc.Frontmatter[key]
		switch t := value.(type) {
		case time.Time:
			if t.Nanosecond() != 0 {
				pd := document.PlainDate(t.Truncate(time.Second).Format(time.RFC3339))
				planned[key] = pd
				fixes = append(fixes, FixFieldChange{
					Field: key, From: t.Format(time.RFC3339Nano), To: string(pd), Reason: "unquote_date",
				})
			}
		case string:
			if t == "" {
				continue
			}
			if parsed, ok := schemas.CoerceDate(doc.Type, key, t); ok {
				planned[key] = parsed
				fixes = append(fixes, FixFieldChange{
					Field: key, From: fmt.Sprintf("%q", t), To: string(parsed), Reason: "unquote_date",
				})
			}
		}
	}

	return planned, fixes
}

func defaultStatusForSchema(schema vault.DocTypeSchema) string {
	if schema.Status != nil && strings.TrimSpace(schema.Status.Initial) != "" {
		return schema.Status.Initial
	}
	if def, ok := schema.Fields["status"]; ok {
		if s, sok := document.ScalarText(def.Default); sok && strings.TrimSpace(s) != "" {
			return s
		}
		if len(def.Enum) > 0 {
			return def.Enum[0]
		}
	}
	return ""
}

func deriveNoteTitle(body, relPath string) string {
	masked := document.MaskCodeRegions(body)
	lines := strings.Split(masked, "\n")
	origLines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") && i < len(origLines) {
			raw := strings.TrimSpace(origLines[i])
			raw = strings.TrimPrefix(raw, "# ")
			raw = strings.TrimRight(raw, "# ")
			if raw = strings.TrimSpace(raw); raw != "" {
				return raw
			}
		}
	}
	stem := basenameNoExt(relPath)
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(stem))
	for i, w := range words {
		runes := []rune(w)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			words[i] = string(runes)
		}
	}
	if len(words) == 0 {
		return "Untitled"
	}
	return strings.Join(words, " ")
}

func buildCanonicalTargetLookup(docs []store.DocInfo) func(path string) (wikiTarget, mdDest string) {
	titleCount := make(map[string]int, len(docs))
	baseCount := make(map[string]int, len(docs))
	byPath := make(map[string]store.DocInfo, len(docs))
	for _, d := range docs {
		byPath[d.Path] = d
		if d.Title != "" {
			titleCount[d.Title]++
		}
		base := basenameNoExt(d.Path)
		baseCount[base]++
	}
	return func(path string) (wikiTarget, mdDest string) {
		d := byPath[path]
		base := basenameNoExt(path)
		switch {
		case d.Title != "" && titleCount[d.Title] == 1:
			wikiTarget = d.Title
		case baseCount[base] == 1:
			wikiTarget = base
		default:
			wikiTarget = strings.TrimSuffix(path, ".md")
		}
		if baseCount[base] == 1 {
			mdDest = filepath.Base(path)
		} else {
			mdDest = path
		}
		return wikiTarget, mdDest
	}
}

func collectFixMarkdownFiles(v *vault.Vault) ([]string, error) {
	excluded := vault.ObsidianTemplateFolders(v.Root)
	var files []string
	err := filepath.Walk(v.Root, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if path != v.Root && (strings.HasPrefix(base, ".") || base == "node_modules") {
				return filepath.SkipDir
			}
			if vault.IsExcludedFolderPath(v.RelPath(path), excluded) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		rel := v.RelPath(path)
		if vault.IsIgnored(rel) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk vault: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func cloneSchemaSet(src *vault.SchemaSet) (*vault.SchemaSet, error) {
	if src == nil {
		return vault.DefaultSchemas(), nil
	}
	b, err := yaml.Marshal(src)
	if err != nil {
		return nil, err
	}
	var out vault.SchemaSet
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if out.Types == nil {
		out.Types = make(map[string]vault.DocTypeSchema)
	}
	return &out, nil
}

func replaceBodyPreservingFrontmatter(origBytes []byte, doc *document.Document, absPath, newBody string) ([]byte, error) {
	origStr := string(origBytes)
	if strings.HasSuffix(origStr, doc.Body) {
		prefix := origStr[:len(origStr)-len(doc.Body)]
		return []byte(prefix + newBody), nil
	}
	prevPath := doc.Path
	prevBody := doc.Body
	doc.Path = absPath
	doc.Body = newBody
	out, err := doc.Serialize()
	doc.Path = prevPath
	doc.Body = prevBody
	return out, err
}

func writeFixBatchSnapshot(v *vault.Vault, batch fixBatchSnapshot) error {
	path := fixBatchPath(v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%x", path, sha256.Sum256(data))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func isNonNoteAsset(target string) bool {
	t := strings.TrimSpace(target)
	if t == "" {
		return true
	}
	switch strings.ToLower(filepath.Ext(t)) {
	case "", ".md", ".canvas", ".base":
		return false
	default:
		return true
	}
}
