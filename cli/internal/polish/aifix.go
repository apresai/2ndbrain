package polish

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

const aiParseRepairSystem = `You repair malformed YAML frontmatter in Obsidian Markdown notes.
Given the broken raw YAML frontmatter, optional body context, and schema definitions, output ONLY the repaired YAML frontmatter text (without --- fences and without markdown code fences).

Strict rules:
1. Preserve every existing key and value as faithfully as possible, and keep comments where present.
2. Fix YAML syntax errors (bad indentation, unquoted strings with ": " or "#" or "[", unclosed quotes/brackets, tabs -> 2 spaces).
3. NEVER add an "id" key if it was not already in the input.
4. NEVER invent sensitive keys (secret, password, token, key) or unrelated new keys.
5. NEVER drop existing keys from the input.`

const aiSchemaRepairSystem = `You repair Obsidian Markdown YAML frontmatter metadata to conform to .2ndbrain/schemas.yaml.
Given the note's current frontmatter, body excerpt, failing schema/date issues, and the allowed schema definition for its document type, return ONLY a compact JSON object:
{"updates": {"field_name": "new_value", ...}}

Strict rules:
1. Only include keys listed in the failing issues. Do NOT modify unrelated fields.
2. For enum fields (e.g. status, severity, priority), choose the single best-matching value from the exact allowed enum list.
3. For required fields (e.g. incident-date, owner, service), infer the value from the note title/body/frontmatter when clearly supported; omit any field if there is no plausible evidence.
4. For calendar date fields (type: date), format as YYYY-MM-DD. For datetime fields (created, modified, type: datetime), format as UTC RFC3339 (YYYY-MM-DDTHH:MM:SSZ).
5. For list fields, return a JSON array of strings. For scalar fields (including status), return a single JSON string.
6. NEVER include "id" or sensitive keys (secret, password, token, key) in updates.
7. Respond with ONLY valid JSON, no prose.`

type deferredBrokenLink struct {
	target         string
	isAmbiguous    bool
	seedCandidates []string
}

type fixNoteState struct {
	absPath       string
	relPath       string
	origBytes     []byte
	currentBytes  []byte
	doc           *document.Document
	currentBody   string
	parseError    bool
	unclosedFence bool
	parseErrMsg   string
	change        FixNoteChange
	deferredLinks []deferredBrokenLink
}

func collectRemainingAIIssues(states []*fixNoteState, schemas *vault.SchemaSet) []AIFixableIssue {
	issues := make([]AIFixableIssue, 0)
	for _, ns := range states {
		if ns.parseError {
			msg := "parse error: " + ns.parseErrMsg
			if ns.unclosedFence {
				msg = "parse error: unclosed YAML frontmatter fence"
			}
			issues = append(issues, AIFixableIssue{
				Path:    ns.relPath,
				Kind:    "parse_error",
				Message: msg,
			})
			continue
		}
		if ns.doc == nil {
			continue
		}

		issues = append(issues, noteSchemaAndDateAIIssues(ns.relPath, ns.doc, schemas)...)

		for _, dl := range ns.deferredLinks {
			msg := fmt.Sprintf("broken wikilink: [[%s]]", dl.target)
			if dl.isAmbiguous {
				msg = fmt.Sprintf("ambiguous wikilink: [[%s]]", dl.target)
			}
			issues = append(issues, AIFixableIssue{
				Path:    ns.relPath,
				Kind:    "broken_link",
				Message: msg,
				Target:  dl.target,
			})
		}
	}
	return issues
}

func noteSchemaAndDateAIIssues(relPath string, doc *document.Document, schemas *vault.SchemaSet) []AIFixableIssue {
	if doc == nil || schemas == nil {
		return nil
	}
	var issues []AIFixableIssue
	if schema, ok := schemas.Types[doc.Type]; ok {
		for _, req := range schema.Required {
			if noteFieldIsMissing(doc, schemas, schema, req) {
				issues = append(issues, AIFixableIssue{
					Path:    relPath,
					Kind:    "missing_required",
					Message: fmt.Sprintf("missing required field '%s' for type '%s'", req, doc.Type),
					Target:  req,
				})
			}
		}

		fieldNames := make([]string, 0, len(schema.Fields))
		for fn := range schema.Fields {
			fieldNames = append(fieldNames, fn)
		}
		sort.Strings(fieldNames)

		for _, fieldName := range fieldNames {
			fieldDef := schema.Fields[fieldName]
			val, exists := doc.Frontmatter[fieldName]
			if !exists || val == nil || len(fieldDef.Enum) == 0 {
				continue
			}
			if fieldName != "status" && schemas.IsListField(doc.Type, fieldName) {
				elems, ok := noteListTextItems(doc, fieldName, val)
				if !ok {
					issues = append(issues, AIFixableIssue{
						Path:    relPath,
						Kind:    "schema_enum",
						Message: fmt.Sprintf("field '%s' expects scalar values in %v", fieldName, fieldDef.Enum),
						Target:  fieldName,
					})
					continue
				}
				bad := false
				for _, el := range elems {
					if !containsStr(fieldDef.Enum, el) {
						bad = true
						break
					}
				}
				if bad {
					issues = append(issues, AIFixableIssue{
						Path:    relPath,
						Kind:    "schema_enum",
						Message: fmt.Sprintf("field '%s' has value not in %v", fieldName, fieldDef.Enum),
						Target:  fieldName,
					})
				}
				continue
			}

			strVal, ok := noteScalarText(doc, fieldName, val)
			if !ok {
				issues = append(issues, AIFixableIssue{
					Path:    relPath,
					Kind:    "schema_enum",
					Message: fmt.Sprintf("field '%s' expects a scalar string in %v", fieldName, fieldDef.Enum),
					Target:  fieldName,
				})
				continue
			}
			if !containsStr(fieldDef.Enum, strVal) {
				issues = append(issues, AIFixableIssue{
					Path:    relPath,
					Kind:    "schema_enum",
					Message: fmt.Sprintf("field '%s' value '%s' not in %v", fieldName, strVal, fieldDef.Enum),
					Target:  fieldName,
				})
			}
		}
	}

	fmKeys := make([]string, 0, len(doc.Frontmatter))
	for k := range doc.Frontmatter {
		fmKeys = append(fmKeys, k)
	}
	sort.Strings(fmKeys)

	for _, key := range fmKeys {
		if !schemas.IsDateField(doc.Type, key) {
			continue
		}
		val := doc.Frontmatter[key]
		switch t := val.(type) {
		case nil, time.Time, document.PlainDate:
			continue
		case string:
			if strings.TrimSpace(t) == "" {
				continue
			}
			if _, ok := schemas.CoerceDate(doc.Type, key, t); !ok {
				issues = append(issues, AIFixableIssue{
					Path:    relPath,
					Kind:    "formatting",
					Message: fmt.Sprintf("invalid date value %q for field '%s'", t, key),
					Target:  key,
				})
			}
		default:
			issues = append(issues, AIFixableIssue{
				Path:    relPath,
				Kind:    "formatting",
				Message: fmt.Sprintf("field '%s' holds %T, expected date", key, val),
				Target:  key,
			})
		}
	}

	return issues
}

func noteScalarText(doc *document.Document, field string, val any) (string, bool) {
	if doc != nil {
		if s, ok := doc.MetaText(field); ok {
			return s, true
		}
	}
	return document.ScalarText(val)
}

func noteListTextItems(doc *document.Document, field string, val any) ([]string, bool) {
	switch lv := val.(type) {
	case []any:
		out := make([]string, 0, len(lv))
		for i, item := range lv {
			if doc != nil {
				if s, ok := doc.MetaTextItem(field, i); ok {
					out = append(out, s)
					continue
				}
			}
			s, ok := document.ScalarText(item)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		return lv, true
	default:
		if s, ok := noteScalarText(doc, field, val); ok {
			return []string{s}, true
		}
		return nil, false
	}
}

func noteFieldIsMissing(doc *document.Document, schemas *vault.SchemaSet, schema vault.DocTypeSchema, field string) bool {
	val, exists := doc.Frontmatter[field]
	if !exists || val == nil {
		return true
	}
	if field != "status" && schemas != nil && schemas.IsListField(doc.Type, field) {
		switch lv := val.(type) {
		case []any:
			return len(lv) == 0
		case []string:
			return len(lv) == 0
		default:
			s, ok := noteScalarText(doc, field, val)
			return !ok || strings.TrimSpace(s) == ""
		}
	}
	str, ok := noteScalarText(doc, field, val)
	if !ok {
		return len(schema.Fields[field].Enum) == 0
	}
	return strings.TrimSpace(str) == ""
}

func containsStr(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func repairParseErrorNote(
	ctx context.Context,
	ns *fixNoteState,
	schemas *vault.SchemaSet,
	generator ai.GenerationProvider,
	reasoningEffort string,
	usage *FixAIUsage,
	warnings *[]string,
) bool {
	parts, ok := document.SplitRawFrontmatter(ns.currentBytes)
	if !ok {
		*warnings = append(*warnings, fmt.Sprintf("%s: could not isolate frontmatter for parse repair", ns.relPath))
		return false
	}

	origKeys := document.ExtractRawYAMLKeys(parts.RawYAML)
	proseLines := document.NonYAMLProseLines(parts.RawYAML)
	hadID := false
	for _, k := range origKeys {
		if strings.EqualFold(k, "id") {
			hadID = true
			break
		}
	}

	// Try mechanical repair first (closing an unclosed fence and/or replacing tab indentation).
	mechYAML := document.RepairTabsInYAML(parts.RawYAML)
	if parts.Unclosed || mechYAML != parts.RawYAML {
		candidate := document.ReassembleFrontmatter(parts, mechYAML)
		if newDoc, err := document.Parse(ns.relPath, candidate); err == nil && newDoc.Frontmatter != nil {
			ns.currentBytes = candidate
			ns.doc = newDoc
			ns.currentBody = newDoc.Body
			ns.parseError = false
			ns.unclosedFence = false
			ns.change.FrontmatterFixes = append(ns.change.FrontmatterFixes, FixFieldChange{
				Field:  "frontmatter",
				To:     "repaired YAML",
				Reason: "parse_error",
			})
			return true
		}
	}

	if generator == nil {
		return false
	}

	bodyCtx := truncateRunes(parts.Body, 600)
	var user strings.Builder
	fmt.Fprintf(&user, "File: %s\n", ns.relPath)
	fmt.Fprintf(&user, "Parse error: %s\n\n", ns.parseErrMsg)
	fmt.Fprintf(&user, "Broken YAML frontmatter:\n%s\n", mechYAML)
	if strings.TrimSpace(bodyCtx) != "" {
		fmt.Fprintf(&user, "\nNote body excerpt (context only, do not output):\n%s\n", bodyCtx)
	}
	if schemas != nil && len(schemas.Types) > 0 {
		typeNames := make([]string, 0, len(schemas.Types))
		for t := range schemas.Types {
			typeNames = append(typeNames, t)
		}
		sort.Strings(typeNames)
		fmt.Fprintf(&user, "\nKnown document types: %s\n", strings.Join(typeNames, ", "))
	}

	opts := ai.GenOpts{
		Temperature:     nil,
		MaxTokens:       32768,
		SystemPrompt:    aiParseRepairSystem,
		ReasoningEffort: reasoningEffort,
	}
	out, u, err := callGeneratorWithUsage(ctx, generator, user.String(), opts)
	if usage != nil {
		usage.GenCalls++
		usage.GenInTokens += u.InputTokens
		usage.GenOutTokens += u.OutputTokens
	}
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: AI YAML repair failed: %v", ns.relPath, err))
		return false
	}

	repairedYAML := stripYAMLCodeFences(out)
	if strings.TrimSpace(repairedYAML) == "" || strings.Contains(repairedYAML, "\t") {
		*warnings = append(*warnings, fmt.Sprintf("%s: AI YAML repair produced empty or tab-indented output", ns.relPath))
		return false
	}

	candidate := document.ReassembleFrontmatter(parts, repairedYAML)
	newDoc, perr := document.Parse(ns.relPath, candidate)
	if perr != nil || newDoc.Frontmatter == nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: AI YAML repair did not parse cleanly: %v", ns.relPath, perr))
		return false
	}

	if !hadID {
		if _, hasID := newDoc.Frontmatter["id"]; hasID {
			*warnings = append(*warnings, fmt.Sprintf("%s: rejected AI YAML repair that injected an id field", ns.relPath))
			return false
		}
	}
	origKeySet := make(map[string]bool, len(origKeys))
	for _, k := range origKeys {
		origKeySet[k] = true
	}
	for k := range newDoc.Frontmatter {
		if document.IsSensitiveKey(k) && !origKeySet[k] {
			*warnings = append(*warnings, fmt.Sprintf("%s: rejected AI YAML repair that added sensitive key %q", ns.relPath, k))
			return false
		}
	}
	for _, k := range origKeys {
		if _, ok := newDoc.Frontmatter[k]; !ok {
			*warnings = append(*warnings, fmt.Sprintf("%s: rejected AI YAML repair that dropped key %q", ns.relPath, k))
			return false
		}
	}
	if len(proseLines) > 0 {
		normalizedRepaired := strings.Join(strings.Fields(repairedYAML), " ")
		for _, pl := range proseLines {
			normalizedProse := strings.Join(strings.Fields(pl), " ")
			if normalizedProse != "" && !strings.Contains(normalizedRepaired, normalizedProse) {
				*warnings = append(*warnings, fmt.Sprintf("%s: rejected AI YAML repair that would drop non-YAML prose %q", ns.relPath, truncateRunes(pl, 60)))
				return false
			}
		}
	}

	ns.currentBytes = candidate
	ns.doc = newDoc
	ns.currentBody = newDoc.Body
	ns.parseError = false
	ns.unclosedFence = false
	ns.change.FrontmatterFixes = append(ns.change.FrontmatterFixes, FixFieldChange{
		Field:  "frontmatter",
		To:     "repaired YAML",
		Reason: "parse_error",
	})
	return true
}

func repairNoteSchemaAndDatesAI(
	ctx context.Context,
	ns *fixNoteState,
	schemas *vault.SchemaSet,
	generator ai.GenerationProvider,
	reasoningEffort string,
	usage *FixAIUsage,
	warnings *[]string,
) int {
	if ns.doc == nil || generator == nil || schemas == nil {
		return 0
	}
	issues := noteSchemaAndDateAIIssues(ns.relPath, ns.doc, schemas)
	if len(issues) == 0 {
		return 0
	}

	issueByField := make(map[string]AIFixableIssue, len(issues))
	for _, is := range issues {
		if is.Target != "" {
			if _, exists := issueByField[is.Target]; !exists {
				issueByField[is.Target] = is
			}
		}
	}
	if len(issueByField) == 0 {
		return 0
	}

	schema, hasSchema := schemas.Types[ns.doc.Type]
	var user strings.Builder
	fmt.Fprintf(&user, "File: %s\n", ns.relPath)
	fmt.Fprintf(&user, "Document type: %q\n", ns.doc.Type)
	if hasSchema {
		if schemaJSON, err := json.Marshal(schema); err == nil {
			fmt.Fprintf(&user, "Schema for %q: %s\n", ns.doc.Type, string(schemaJSON))
		}
	}
	if fmJSON, err := json.Marshal(ns.doc.Frontmatter); err == nil {
		fmt.Fprintf(&user, "Current frontmatter: %s\n", string(fmJSON))
	}
	user.WriteString("Failing issues:\n")
	for _, is := range issues {
		fmt.Fprintf(&user, "- [%s] field=%s: %s\n", is.Kind, is.Target, is.Message)
	}
	if bodyCtx := truncateRunes(ns.currentBody, 1500); strings.TrimSpace(bodyCtx) != "" {
		fmt.Fprintf(&user, "\nNote body excerpt:\n%s\n", bodyCtx)
	}

	opts := ai.GenOpts{
		Temperature:     nil,
		MaxTokens:       32768,
		SystemPrompt:    aiSchemaRepairSystem,
		ReasoningEffort: reasoningEffort,
	}
	out, u, err := callGeneratorWithUsage(ctx, generator, user.String(), opts)
	if usage != nil {
		usage.GenCalls++
		usage.GenInTokens += u.InputTokens
		usage.GenOutTokens += u.OutputTokens
	}
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: AI schema repair failed: %v", ns.relPath, err))
		return 0
	}

	updates, perr := parseAISchemaUpdates(out)
	if perr != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: AI schema repair returned invalid JSON: %v", ns.relPath, perr))
		return 0
	}
	if len(updates) == 0 {
		return 0
	}

	validated := make(map[string]any)
	var plannedChanges []FixFieldChange

	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		val := updates[key]
		if key == "id" || document.IsSensitiveKey(key) {
			continue
		}
		is, targeted := issueByField[key]
		if !targeted {
			continue
		}
		allowedBySchema := schemas.IsDateField(ns.doc.Type, key)
		if hasSchema {
			if _, ok := schema.Fields[key]; ok {
				allowedBySchema = true
			}
			if containsStr(schema.Required, key) {
				allowedBySchema = true
			}
		}
		if !allowedBySchema {
			continue
		}

		var oldDisplay string
		if cur, ok := ns.doc.Frontmatter[key]; ok && cur != nil {
			if s, sok := noteScalarText(ns.doc, key, cur); sok {
				oldDisplay = s
			} else {
				oldDisplay = fmt.Sprintf("%v", cur)
			}
		}

		if key != "status" && schemas.IsListField(ns.doc.Type, key) {
			elems, ok := coerceStringSlice(val)
			if !ok || len(elems) == 0 {
				continue
			}
			validAll := true
			for _, elem := range elems {
				if err := schemas.ValidateField(ns.doc.Type, key, elem); err != nil {
					validAll = false
					break
				}
			}
			if !validAll {
				continue
			}
			validated[key] = elems
			plannedChanges = append(plannedChanges, FixFieldChange{
				Field:  key,
				From:   oldDisplay,
				To:     strings.Join(elems, ", "),
				Reason: is.Kind,
			})
			continue
		}

		if schemas.IsDateField(ns.doc.Type, key) {
			strVal, ok := val.(string)
			if !ok || strings.TrimSpace(strVal) == "" {
				continue
			}
			pd, ok := schemas.CoerceDate(ns.doc.Type, key, strings.TrimSpace(strVal))
			if !ok {
				continue
			}
			validated[key] = pd
			plannedChanges = append(plannedChanges, FixFieldChange{
				Field:  key,
				From:   oldDisplay,
				To:     string(pd),
				Reason: is.Kind,
			})
			continue
		}

		strVal, ok := document.ScalarText(val)
		if !ok || strings.TrimSpace(strVal) == "" {
			continue
		}
		if err := schemas.ValidateField(ns.doc.Type, key, strVal); err != nil {
			continue
		}
		if key == "status" && ns.doc.Status != "" {
			if err := schemas.ValidateStatusTransition(ns.doc.Type, ns.doc.Status, strVal); err != nil {
				continue
			}
		}
		validated[key] = strVal
		plannedChanges = append(plannedChanges, FixFieldChange{
			Field:  key,
			From:   oldDisplay,
			To:     strVal,
			Reason: is.Kind,
		})
	}

	if len(validated) == 0 {
		return 0
	}

	updatedMeta := make(map[string]any, len(ns.doc.Frontmatter)+len(validated))
	for k, v := range ns.doc.Frontmatter {
		updatedMeta[k] = v
	}
	for k, v := range validated {
		updatedMeta[k] = v
	}

	astOut, astErr := document.UpdateDocumentFrontmatterAST(ns.currentBytes, updatedMeta, ns.currentBody)
	if astErr != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: AST frontmatter update failed: %v", ns.relPath, astErr))
		return 0
	}
	newDoc, perr := document.Parse(ns.relPath, astOut)
	if perr != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: re-parse after AI schema update failed: %v", ns.relPath, perr))
		return 0
	}

	ns.currentBytes = astOut
	ns.doc = newDoc
	ns.currentBody = newDoc.Body
	ns.change.FrontmatterFixes = append(ns.change.FrontmatterFixes, plannedChanges...)
	return len(plannedChanges)
}

func runStageCLinkRecovery(
	ctx context.Context,
	v *vault.Vault,
	states []*fixNoteState,
	docs []store.DocInfo,
	aliases map[string][]string,
	liveResolver *store.Resolver,
	repairIdx *RepairIndex,
	canonicalTargetForPath func(path string) (wikiTarget, mdDest string),
	providers *FixAIProviders,
	opts FixOptions,
	report *FixVaultReport,
) {
	livePathSet := make(map[string]bool, len(docs))
	for _, d := range docs {
		livePathSet[d.Path] = true
	}

	var docIDs []string
	var embeddings [][]float32
	var activeEmbedder ai.EmbeddingProvider
	embedReady := false
	if v != nil && v.DB != nil && providers != nil && providers.Embedder != nil && providers.EmbedderReady {
		if dim, derr := v.DB.SampleEmbeddingDim(); derr == nil && dim > 0 {
			if dims, derr2 := v.DB.DistinctEmbeddingDims(); derr2 == nil && len(dims) <= 1 && providers.Embedder.Dimensions() == dim {
				if ids, vecs, lerr := v.DB.AllEmbeddings(); lerr == nil && len(vecs) > 0 {
					docIDs = ids
					embeddings = vecs
					activeEmbedder = providers.Embedder
					embedReady = true
				}
			}
		}
	}

	for _, ns := range states {
		if ns.parseError || ns.doc == nil || len(ns.deferredLinks) == 0 {
			continue
		}

		remainingDeferred := make([]deferredBrokenLink, 0, len(ns.deferredLinks))
		for _, dl := range ns.deferredLinks {
			if _, rerr := liveResolver.Resolve(dl.target); rerr == nil {
				// Resolved cleanly after Stage B repaired a target note.
				if dl.isAmbiguous {
					report.Ambiguous = removeAmbiguousEntry(report.Ambiguous, ns.relPath, dl.target)
				}
				continue
			}

			results, contextSnippet, embedInTok := GatherTargetSuggestions(ctx, v, dl.target, ns.relPath, SuggestTargetOptions{
				Embedder:            activeEmbedder,
				EmbedderReady:       embedReady,
				LiveDocs:            docs,
				LiveAliases:         aliases,
				LiveResolver:        liveResolver,
				RepairIdx:           repairIdx,
				DocIDs:              docIDs,
				Embeddings:          embeddings,
				SimilarityThreshold: opts.Threshold,
				SourceBody:          ns.currentBody,
				SeedPaths:           dl.seedCandidates,
				PoolLimit:           LLMPoolCap,
			})
			if embedInTok > 0 {
				report.AIUsage.EmbedCalls++
				report.AIUsage.EmbedInTokens += embedInTok
			}

			llmOutcome := LLMOutcomeSkipped
			if !HasHighConfidence(results) && len(results) > 0 && providers != nil && providers.Generator != nil {
				reranked, picks, u, rerr := RerankTargetCandidatesLLM(ctx, providers.Generator, TargetRerankSystem, dl.target, contextSnippet, results)
				report.AIUsage.GenCalls++
				report.AIUsage.GenInTokens += u.InputTokens
				report.AIUsage.GenOutTokens += u.OutputTokens
				switch {
				case rerr != nil:
					llmOutcome = LLMOutcomeError
					report.Warnings = append(report.Warnings, fmt.Sprintf("%s: AI link recovery for [[%s]] failed: %v", ns.relPath, dl.target, rerr))
				case picks == 0:
					llmOutcome = LLMOutcomeDeclined
					results = reranked
				default:
					llmOutcome = LLMOutcomePromoted
					results = reranked
				}
			}

			pickedPath := ""
			if len(results) > 0 && results[0].Confidence == "high" {
				pickedPath = results[0].Path
			} else if llmOutcome == LLMOutcomePromoted && len(results) > 0 {
				pickedPath = results[0].Path
			}

			if pickedPath != "" && pickedPath != ns.relPath && livePathSet[pickedPath] {
				wikiDest, mdDest := safeWikiDestForPath(liveResolver, canonicalTargetForPath, pickedPath)
				if rewritten, n := document.RewriteExactLinkTarget(ns.currentBody, dl.target, wikiDest, mdDest); n > 0 {
					ns.currentBody = rewritten
					reason := "ai_suggest"
					if dl.isAmbiguous {
						reason = "ai_disambiguate"
						report.Ambiguous = removeAmbiguousEntry(report.Ambiguous, ns.relPath, dl.target)
					}
					ns.change.LinkActions = append(ns.change.LinkActions, FixLinkAction{
						Action: "relink",
						From:   dl.target,
						To:     wikiDest,
						Count:  n,
						Reason: reason,
					})
					report.LinksUpdated += n
					report.AIFixesApplied += n
				} else {
					remainingDeferred = append(remainingDeferred, dl)
				}
				continue
			}

			if llmOutcome == LLMOutcomeError {
				// Fail closed: leave link untouched and keep as remaining AI-fixable.
				remainingDeferred = append(remainingDeferred, dl)
				continue
			}

			if !dl.isAmbiguous && (len(results) == 0 || llmOutcome == LLMOutcomeDeclined) {
				if rewritten, n := document.UnlinkExactTarget(ns.currentBody, dl.target); n > 0 {
					ns.currentBody = rewritten
					ns.change.LinkActions = append(ns.change.LinkActions, FixLinkAction{
						Action: "unlink",
						From:   dl.target,
						Count:  n,
						Reason: "dead_link",
					})
					report.LinksUnlinked += n
					continue
				}
			}
			remainingDeferred = append(remainingDeferred, dl)
		}
		ns.deferredLinks = remainingDeferred

		if ns.currentBody != ns.doc.Body {
			outBytes, err := replaceBodyPreservingFrontmatter(ns.currentBytes, ns.doc, ns.absPath, ns.currentBody)
			if err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s: failed updating body after link recovery: %v", ns.relPath, err))
				continue
			}
			newDoc, perr := document.Parse(ns.relPath, outBytes)
			if perr != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s: re-parse after link recovery failed: %v", ns.relPath, perr))
				continue
			}
			ns.currentBytes = outBytes
			ns.doc = newDoc
		}
	}
}

func applyDeferredTier4Fallback(states []*fixNoteState, report *FixVaultReport) {
	for _, ns := range states {
		if ns.parseError || ns.doc == nil || len(ns.deferredLinks) == 0 {
			continue
		}
		remainingDeferred := make([]deferredBrokenLink, 0, len(ns.deferredLinks))
		for _, dl := range ns.deferredLinks {
			if dl.isAmbiguous {
				remainingDeferred = append(remainingDeferred, dl)
				continue
			}
			if rewritten, n := document.UnlinkExactTarget(ns.currentBody, dl.target); n > 0 {
				ns.currentBody = rewritten
				ns.change.LinkActions = append(ns.change.LinkActions, FixLinkAction{
					Action: "unlink",
					From:   dl.target,
					Count:  n,
					Reason: "dead_link",
				})
				report.LinksUnlinked += n
			} else {
				remainingDeferred = append(remainingDeferred, dl)
			}
		}
		ns.deferredLinks = remainingDeferred
		if ns.currentBody != ns.doc.Body {
			outBytes, err := replaceBodyPreservingFrontmatter(ns.currentBytes, ns.doc, ns.absPath, ns.currentBody)
			if err != nil {
				continue
			}
			if newDoc, perr := document.Parse(ns.relPath, outBytes); perr == nil {
				ns.currentBytes = outBytes
				ns.doc = newDoc
			}
		}
	}
}

func removeAmbiguousEntry(list []FixAmbiguousLink, path, target string) []FixAmbiguousLink {
	out := make([]FixAmbiguousLink, 0, len(list))
	for _, a := range list {
		if a.Path == path && a.Target == target {
			continue
		}
		out = append(out, a)
	}
	return out
}

func callGeneratorWithUsage(ctx context.Context, generator ai.GenerationProvider, userPrompt string, opts ai.GenOpts) (string, ai.GenUsage, error) {
	var out string
	var usage ai.GenUsage
	var err error
	if ug, ok := generator.(ai.UsageGenerator); ok {
		out, usage, err = ug.GenerateWithUsage(ctx, userPrompt, opts)
	} else {
		out, err = generator.Generate(ctx, userPrompt, opts)
	}
	if err != nil {
		return "", ai.GenUsage{}, err
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		usage = ai.GenUsage{
			InputTokens:  max(1, (len(opts.SystemPrompt)+len(userPrompt))/4),
			OutputTokens: max(1, len(out)/4),
		}
	}
	return out, usage, nil
}

func stripYAMLCodeFences(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		} else {
			s = ""
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "---" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func parseAISchemaUpdates(raw string) (map[string]any, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(s, "json")
		s = strings.TrimSpace(s)
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	if i := strings.IndexByte(s, '{'); i >= 0 {
		if j := strings.LastIndexByte(s, '}'); j > i {
			s = s[i : j+1]
		}
	}
	var payload struct {
		Updates map[string]any `json:"updates"`
	}
	if err := json.Unmarshal([]byte(s), &payload); err != nil {
		return nil, err
	}
	return payload.Updates, nil
}

func coerceStringSlice(v any) ([]string, bool) {
	switch tv := v.(type) {
	case []string:
		out := make([]string, 0, len(tv))
		for _, item := range tv {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out, len(out) > 0
	case []any:
		out := make([]string, 0, len(tv))
		for _, item := range tv {
			s, ok := document.ScalarText(item)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, false
			}
			out = append(out, strings.TrimSpace(s))
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes])
}
