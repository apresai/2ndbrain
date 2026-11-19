package polish

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

// SchemaPolishResult holds the in-memory outcome of RepairNoteSchema.
type SchemaPolishResult struct {
	Bytes    []byte
	Doc      *document.Document
	Fixes    []FixFieldChange
	Repaired bool
	Usage    FixAIUsage
	Warnings []string
}

// OutgoingLinkRepairResult holds the in-memory outcome of RepairOutgoingLinks.
type OutgoingLinkRepairResult struct {
	Body     string
	Repaired []LinkRepair
	Skipped  []LinkRepair
	Usage    FixAIUsage
	Warnings []string
}

// RepairNoteSchema repairs malformed frontmatter, missing required fields,
// quoted/unparseable dates, and invalid schema enum values for a single note
// strictly in memory, reusing M1's AST-preserving helpers without touching disk
// or widening .2ndbrain/schemas.yaml.
func RepairNoteSchema(
	ctx context.Context,
	v *vault.Vault,
	relPath string,
	origBytes []byte,
	doc *document.Document,
	generator ai.GenerationProvider,
	reasoningEffort string,
) (SchemaPolishResult, error) {
	res := SchemaPolishResult{
		Bytes: origBytes,
		Doc:   doc,
	}
	if vault.HasTemplatePlaceholders(origBytes) || (doc != nil && document.IsReadOnlyType(doc.Type)) {
		return res, nil
	}

	effectiveSchemas := vault.DefaultSchemas()
	if v != nil && v.Schemas != nil {
		effectiveSchemas = v.Schemas
	}

	ns := &fixNoteState{
		relPath:      relPath,
		origBytes:    origBytes,
		currentBytes: origBytes,
		doc:          doc,
	}
	hasParseIssue := doc == nil || (doc.Frontmatter == nil && document.HasUnclosedFrontmatter(origBytes))
	if !hasParseIssue {
		ns.currentBody = doc.Body
	} else {
		ns.parseError = true
		if doc == nil {
			if _, perr := document.Parse(relPath, origBytes); perr != nil {
				ns.parseErrMsg = perr.Error()
			}
		} else {
			ns.parseErrMsg = "unclosed YAML frontmatter fence"
		}
		if parts, ok := document.SplitRawFrontmatter(origBytes); ok && parts.Unclosed {
			ns.unclosedFence = true
		}
		if !repairParseErrorNote(ctx, ns, effectiveSchemas, generator, reasoningEffort, &res.Usage, &res.Warnings) || ns.doc == nil {
			if ns.parseErrMsg == "" {
				ns.parseErrMsg = "malformed YAML frontmatter"
			}
			return res, fmt.Errorf("parse source: %s", ns.parseErrMsg)
		}
	}

	if ns.doc == nil {
		return res, nil
	}

	// Deterministic frontmatter fixes (missing required title/status, quoted/sub-second dates).
	plannedMeta, fieldFixes := planFrontmatterFixes(ns.doc, relPath, effectiveSchemas)
	if len(plannedMeta) > 0 {
		updated := make(map[string]any, len(ns.doc.Frontmatter)+len(plannedMeta))
		for k, val := range ns.doc.Frontmatter {
			updated[k] = val
		}
		for k, val := range plannedMeta {
			updated[k] = val
		}
		if astOut, astErr := document.UpdateDocumentFrontmatterAST(ns.currentBytes, updated, ns.doc.Body); astErr == nil {
			if newDoc, perr := document.Parse(relPath, astOut); perr == nil {
				ns.currentBytes = astOut
				ns.doc = newDoc
				ns.currentBody = newDoc.Body
				ns.change.FrontmatterFixes = append(ns.change.FrontmatterFixes, fieldFixes...)
			}
		} else {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: AST frontmatter update failed: %v", relPath, astErr))
		}
	}

	// AI-assisted schema enum, missing required field, and unparseable date repair.
	if generator != nil {
		repairNoteSchemaAndDatesAI(ctx, ns, effectiveSchemas, generator, reasoningEffort, &res.Usage, &res.Warnings)
	}

	res.Bytes = ns.currentBytes
	res.Doc = ns.doc
	res.Fixes = ns.change.FrontmatterFixes
	res.Repaired = len(ns.change.FrontmatterFixes) > 0
	return res, nil
}

// RepairOutgoingLinks repairs syntax-glitched, case/separator-drifted, moved,
// and (when useAI && generator != nil) semantically recoverable broken links in
// body strictly in memory. Unresolved/declined dead links are preserved and
// reported in Skipped so the Obsidian plugin and CLI polish contract stay intact.
func RepairOutgoingLinks(
	ctx context.Context,
	v *vault.Vault,
	relPath string,
	body string,
	generator ai.GenerationProvider,
	embedder ai.EmbeddingProvider,
	embedReady bool,
	threshold float64,
	useAI bool,
) (OutgoingLinkRepairResult, error) {
	normBody := strings.ReplaceAll(body, "\r\n", "\n")
	res := OutgoingLinkRepairResult{Body: normBody}

	docs, aliases, err := vault.CollectLiveDocs(v.Root)
	if err != nil {
		return res, err
	}
	liveResolver := store.NewResolver(docs, aliases)
	repairIdx := NewRepairIndex(docs, aliases)
	canonicalTargetForPath := buildCanonicalTargetLookup(docs)

	docAliasesByPath := make(map[string][]string)
	for alias, paths := range aliases {
		for _, p := range paths {
			docAliasesByPath[p] = append(docAliasesByPath[p], alias)
		}
	}
	livePathSet := make(map[string]bool, len(docs))
	for _, d := range docs {
		livePathSet[d.Path] = true
	}

	curBody, fmtChanges := document.NormalizeLinkFormatting(normBody, func(target string) bool {
		_, rerr := liveResolver.Resolve(target)
		return rerr == nil
	})
	for _, fc := range fmtChanges {
		res.Repaired = append(res.Repaired, LinkRepair{
			Raw:       fc.From,
			NewTarget: fc.To,
		})
	}

	curBody, actions, _, deferred := resolveNoteBrokenLinksWithDefer(
		relPath, curBody, docs, docAliasesByPath, liveResolver, repairIdx, canonicalTargetForPath, true, true,
	)
	for _, la := range actions {
		if la.Action == "relink" {
			res.Repaired = append(res.Repaired, LinkRepair{
				Raw:       la.From,
				NewTarget: la.To,
			})
		}
	}

	var docIDs []string
	var embeddings [][]float32
	var activeEmbedder ai.EmbeddingProvider
	canEmbed := false
	if v != nil && v.DB != nil && embedReady && embedder != nil {
		if dim, derr := v.DB.SampleEmbeddingDim(); derr == nil && dim > 0 {
			if dims, derr2 := v.DB.DistinctEmbeddingDims(); derr2 == nil && len(dims) <= 1 && embedder.Dimensions() == dim {
				if ids, vecs, lerr := v.DB.AllEmbeddings(); lerr == nil && len(vecs) > 0 {
					docIDs = ids
					embeddings = vecs
					activeEmbedder = embedder
					canEmbed = true
				}
			}
		}
	}

	for _, dl := range deferred {
		if _, rerr := liveResolver.Resolve(dl.target); rerr == nil {
			continue
		}

		relinked := false
		if useAI && generator != nil {
			results, contextSnippet, embedInTok := GatherTargetSuggestions(ctx, v, dl.target, relPath, SuggestTargetOptions{
				Embedder:            activeEmbedder,
				EmbedderReady:       canEmbed,
				LiveDocs:            docs,
				LiveAliases:         aliases,
				LiveResolver:        liveResolver,
				RepairIdx:           repairIdx,
				DocIDs:              docIDs,
				Embeddings:          embeddings,
				SimilarityThreshold: threshold,
				SourceBody:          curBody,
				SeedPaths:           dl.seedCandidates,
				PoolLimit:           LLMPoolCap,
			})
			if embedInTok > 0 {
				res.Usage.EmbedCalls++
				res.Usage.EmbedInTokens += embedInTok
			}

			llmPromoted := false
			llmErrored := false
			if !HasHighConfidence(results) && len(results) > 0 {
				reranked, picks, u, rerr := RerankTargetCandidatesLLM(ctx, generator, TargetRerankSystem, dl.target, contextSnippet, results)
				res.Usage.GenCalls++
				res.Usage.GenInTokens += u.InputTokens
				res.Usage.GenOutTokens += u.OutputTokens
				if rerr != nil {
					llmErrored = true
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: AI link recovery for [[%s]] failed: %v", relPath, dl.target, rerr))
				} else if picks > 0 {
					llmPromoted = true
					results = reranked
				}
			}

			if !llmErrored && len(results) > 0 {
				pickedPath := ""
				if results[0].Confidence == "high" || llmPromoted {
					pickedPath = results[0].Path
				}
				if pickedPath != "" && pickedPath != relPath && livePathSet[pickedPath] {
					wikiDest, mdDest := safeWikiDestForPath(liveResolver, canonicalTargetForPath, pickedPath)
					if rewritten, n := document.RewriteExactLinkTarget(curBody, dl.target, wikiDest, mdDest); n > 0 {
						curBody = rewritten
						res.Repaired = append(res.Repaired, LinkRepair{
							Raw:       dl.target,
							NewTarget: wikiDest,
						})
						relinked = true
					}
				}
			}
		}

		if !relinked {
			reason := "no_match"
			if dl.isAmbiguous {
				reason = "ambiguous"
			}
			res.Skipped = append(res.Skipped, LinkRepair{
				Raw:    dl.target,
				Reason: reason,
			})
		}
	}

	res.Body = curBody
	return res, nil
}

// FormatBodyLikeOriginal normalizes trimmedPolished to the line-ending
// convention of origBytes (CRLF when origBytes contains \r\n, else LF) and
// constructs the disk body preserving leading separator newlines and trailing
// EOF newline from origBody.
func FormatBodyLikeOriginal(origBody string, origBytes []byte, trimmedPolished string) (polishedDisplay string, diskBody string) {
	isCRLF := bytes.Contains(origBytes, []byte("\r\n"))
	nl := "\n"
	if isCRLF {
		nl = "\r\n"
	}

	norm := strings.TrimSpace(strings.ReplaceAll(trimmedPolished, "\r\n", "\n"))
	if isCRLF {
		polishedDisplay = strings.ReplaceAll(norm, "\n", "\r\n")
	} else {
		polishedDisplay = norm
	}

	trimmedLeft := strings.TrimLeft(origBody, "\r\n")
	leadRaw := origBody[:len(origBody)-len(trimmedLeft)]
	lead := strings.ReplaceAll(strings.ReplaceAll(leadRaw, "\r\n", "\n"), "\n", nl)

	trail := ""
	if strings.HasSuffix(origBody, "\n") && polishedDisplay != "" {
		trail = nl
	}
	diskBody = lead + polishedDisplay + trail
	return polishedDisplay, diskBody
}

// ApplyBodyWithFrontmatterPreservation writes diskBody over origBytes while
// keeping raw frontmatter bytes (comments, anchors, quoting, key order, CRLF)
// verbatim when doc.Body matches the suffix of origBytes, and otherwise using
// AST-surgical frontmatter updating (never falling back to lossy Serialize).
func ApplyBodyWithFrontmatterPreservation(origBytes []byte, doc *document.Document, absPath, diskBody string) ([]byte, error) {
	if doc != nil {
		origStr := string(origBytes)
		if strings.HasSuffix(origStr, doc.Body) {
			prefix := origStr[:len(origStr)-len(doc.Body)]
			return []byte(prefix + diskBody), nil
		}
		if doc.Frontmatter != nil {
			if out, err := document.UpdateDocumentFrontmatterAST(origBytes, doc.Frontmatter, diskBody); err == nil {
				return out, nil
			}
		}
	}
	return replaceBodyPreservingFrontmatter(origBytes, doc, absPath, diskBody)
}

// WriteFileAtomic writes data to path atomically via a unique temp file + rename.
func WriteFileAtomic(path string, data []byte) error {
	return writeFileAtomic(path, data)
}
