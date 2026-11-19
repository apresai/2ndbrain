package polish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

// PolishBacklinkChange describes a proposed (or applied) update to one note that
// references the polished target note. Applied intentionally omits `omitempty`
// so JSON previews always serialize `"applied": false`.
type PolishBacklinkChange struct {
	Path     string   `json:"path"`
	Original string   `json:"original"`
	Polished string   `json:"polished"`
	Changes  []string `json:"changes"`
	Applied  bool     `json:"applied"`
}

// BacklinkPlanInput configures PlanBacklinkUpdates.
type BacklinkPlanInput struct {
	TargetRel       string
	TargetDoc       *document.Document
	TargetOrigBody  string
	TargetNewBody   string
	Goal            string
	Generator       ai.GenerationProvider
	ReasoningEffort string
}

// BacklinkPlanItem holds both the JSON preview DTO and the full-file write
// inputs needed to apply the change atomically when --write --apply-backlinks
// is active.
type BacklinkPlanItem struct {
	Change    PolishBacklinkChange
	AbsPath   string
	OrigBytes []byte
	ParsedDoc *document.Document
	DiskBody  string
}

// BacklinkPlanResult aggregates planned incoming backlink updates and AI usage.
type BacklinkPlanResult struct {
	Items    []BacklinkPlanItem
	Usage    FixAIUsage
	Warnings []string
}

const backlinkAlignSystem = `You are updating an Obsidian Markdown note that links to a target note which was just edited or polished.
Given the target note's BEFORE and AFTER bodies, optional polish goal, and this referencing note's body, update ONLY stale prose or link labels in the referencing note that are directly invalidated by the target note's changes or goal.

Hard constraints (violating any is a failure):
1. If the referencing note is already consistent or needs no update, return ONLY: NO_CHANGE
2. Preserve EVERY link to the target note ([[...]] or [label](path)). Never replace the target name before | or # with an unresolvable name; when the polish goal asks to rename or rebrand the target note (while keeping its existing path/title), update an un-aliased [[Target]] display label via [[Target|New Name]] (or [New Name](path) for markdown links) so the link still resolves. Keep any existing |alias, #heading, or #^block anchor on target links unless a heading anchor was explicitly removed.
3. Reproduce every other [[wikilink]], every [markdown](link), and every code span (fenced and inline) verbatim. NEVER invent new wikilinks.
4. Keep the heading hierarchy and unrelated prose intact.
5. Return only the updated Markdown body (no YAML frontmatter, no wrapping code fence, no explanation), or NO_CHANGE.`

// PlanBacklinkUpdates discovers all vault notes referencing TargetRel (via DB
// backlinks/raw-name links unioned with a live vault walk, strictly excluding
// code-block-only matches, templates, and read-only files), repairs broken or
// drifted links pointing to TargetRel, wraps unambiguous unlinked first-prose
// mentions, and optionally aligns stale referencing context using the AI
// generator. It operates strictly in memory and never touches disk.
func PlanBacklinkUpdates(ctx context.Context, v *vault.Vault, in BacklinkPlanInput) (BacklinkPlanResult, error) {
	var res BacklinkPlanResult
	if v == nil {
		return res, nil
	}

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

	targetDoc := in.TargetDoc
	if targetDoc == nil {
		if raw, rerr := os.ReadFile(v.AbsPath(in.TargetRel)); rerr == nil {
			if pd, perr := document.Parse(in.TargetRel, raw); perr == nil {
				targetDoc = pd
			}
		}
	}
	targetTitle := ""
	var targetAliases []string
	var targetID string
	if targetDoc != nil {
		targetTitle = targetDoc.Title
		targetAliases = document.AliasesOf(targetDoc)
		targetID = targetDoc.ID
	}
	if targetTitle == "" {
		for _, d := range docs {
			if d.Path == in.TargetRel {
				targetTitle = d.Title
				if targetID == "" {
					targetID = d.ID
				}
				break
			}
		}
	}

	wikiDest, mdDest := safeWikiDestForPath(liveResolver, canonicalTargetForPath, in.TargetRel)
	targetBasename := basenameNoExt(in.TargetRel)

	tmplFolders := vault.ObsidianTemplateFolders(v.Root)

	// Collect candidate referencing files from live walk + DB Backlinks/LinksByRawName.
	relToAbs := make(map[string]string)
	if files, werr := collectFixMarkdownFiles(v); werr == nil {
		for _, abs := range files {
			rel := v.RelPath(abs)
			if rel != in.TargetRel {
				relToAbs[rel] = abs
			}
		}
	}
	if v.DB != nil {
		if targetID != "" {
			if bls, berr := v.DB.Backlinks(targetID); berr == nil {
				for _, b := range bls {
					if shouldIncludeBacklinkSourcePath(b.Path, in.TargetRel, tmplFolders) {
						relToAbs[b.Path] = v.AbsPath(b.Path)
					}
				}
			}
		}
		if rls, rerr := v.DB.LinksByRawName(in.TargetRel); rerr == nil {
			for _, r := range rls {
				if shouldIncludeBacklinkSourcePath(r.Path, in.TargetRel, tmplFolders) {
					relToAbs[r.Path] = v.AbsPath(r.Path)
				}
			}
		}
	}

	relPaths := make([]string, 0, len(relToAbs))
	for rel := range relToAbs {
		relPaths = append(relPaths, rel)
	}
	sort.Strings(relPaths)

	mentionSurfaces := filterUnambiguousMentionSurfaces(
		buildMentionSurfaces(targetTitle, targetAliases),
		in.TargetRel,
		liveResolver,
		docs,
		docAliasesByPath,
	)
	canResolveWikiDest := false
	if wikiDest != "" {
		if resolved, rerr := liveResolver.Resolve(wikiDest); rerr == nil && resolved == in.TargetRel {
			canResolveWikiDest = true
		}
	}

	targetChanged := strings.TrimSpace(strings.ReplaceAll(in.TargetOrigBody, "\r\n", "\n")) !=
		strings.TrimSpace(strings.ReplaceAll(in.TargetNewBody, "\r\n", "\n"))
	runAIAlign := in.Generator != nil && (strings.TrimSpace(in.Goal) != "" || targetChanged)

	for _, otherRel := range relPaths {
		otherAbs := relToAbs[otherRel]
		rawBytes, rerr := os.ReadFile(otherAbs)
		if rerr != nil || vault.HasTemplatePlaceholders(rawBytes) {
			continue
		}
		otherDoc, perr := document.Parse(otherRel, rawBytes)
		if perr != nil || otherDoc == nil || (otherDoc.Frontmatter == nil && document.HasUnclosedFrontmatter(rawBytes)) || document.IsReadOnlyType(otherDoc.Type) || hasUnresolvedBodyTemplatePlaceholders(otherDoc.Body) {
			continue
		}

		origBodyNorm := strings.ReplaceAll(otherDoc.Body, "\r\n", "\n")
		curBody := origBodyNorm
		var changes []string
		referencesTarget := false
		var targetLinks []document.WikiLink

		// Pass 1: Repair broken/drifted incoming links and canonicalize stem or whitespace-padded links pointing to TargetRel.
		handledTargets := make(map[string]bool)
		for _, l := range document.ExtractWikiLinks(origBodyNorm) {
			rawTarget := strings.TrimSpace(l.Target)
			hasPadding := l.Target != rawTarget
			if rawTarget == "" || isNonNoteAsset(rawTarget) {
				continue
			}
			pointsHere, isBroken := linkTargetsNote(rawTarget, in.TargetRel, targetTitle, docs, docAliasesByPath, liveResolver, repairIdx)
			if !pointsHere {
				continue
			}
			referencesTarget = true
			targetLinks = append(targetLinks, l)
			if handledTargets[l.Target] {
				continue
			}
			handledTargets[l.Target] = true

			isWiki := strings.HasPrefix(l.Raw, "[[") || strings.HasPrefix(l.Raw, "![[")
			if isBroken {
				if rewritten, n := document.RewriteExactLinkTarget(curBody, rawTarget, wikiDest, mdDest); n > 0 {
					curBody = rewritten
					changes = append(changes, "repaired_link")
				}
				continue
			}

			// Canonicalize stem-form wikilinks (e.g. [[core]] -> [[Core Engine]]) and
			// trim whitespace-padded wikilinks (e.g. [[ Core Engine ]] -> [[Core Engine]])
			// while preserving |alias / #heading.
			if isWiki && canResolveWikiDest && wikiDest != "" && (hasPadding || !strings.EqualFold(rawTarget, wikiDest)) {
				cleanStem := strings.TrimSuffix(strings.ReplaceAll(rawTarget, "\\", "/"), ".md")
				if hasPadding && strings.EqualFold(rawTarget, wikiDest) {
					if rewritten, n := document.RewriteExactLinkTarget(curBody, l.Target, wikiDest, mdDest); n > 0 {
						curBody = rewritten
						changes = append(changes, "repaired_link")
					}
				} else if strings.EqualFold(cleanStem, targetBasename) && !strings.EqualFold(targetBasename, wikiDest) {
					if rewritten, n := document.RewriteExactLinkTarget(curBody, rawTarget, wikiDest, mdDest); n > 0 {
						curBody = rewritten
						changes = append(changes, "canonicalized_link")
					}
				} else if hasPadding {
					if rewritten, n := document.RewriteExactLinkTarget(curBody, l.Target, rawTarget, mdDest); n > 0 {
						curBody = rewritten
						changes = append(changes, "repaired_link")
					}
				}
			} else if isWiki && hasPadding {
				if rewritten, n := document.RewriteExactLinkTarget(curBody, l.Target, rawTarget, mdDest); n > 0 {
					curBody = rewritten
					changes = append(changes, "repaired_link")
				}
			}
		}

		// Pass 2: Wrap unlinked first prose mention when note does not yet link to TargetRel.
		if !referencesTarget && canResolveWikiDest && len(mentionSurfaces) > 0 {
			if updated, linked := linkFirstProseMention(curBody, wikiDest, mentionSurfaces); linked {
				curBody = updated
				referencesTarget = true
				changes = append(changes, "linked_mention")
			}
		}

		if !referencesTarget {
			continue
		}

		// Pass 3: Grounded AI backlink alignment for stale descriptions or goal-driven changes.
		if runAIAlign {
			aligned, u, aerr := alignBacklinkNoteAI(
				ctx, in, otherRel, curBody, wikiDest, mdDest, targetTitle, targetAliases, targetLinks, liveResolver,
			)
			res.Usage.GenCalls += u.GenCalls
			res.Usage.GenInTokens += u.GenInTokens
			res.Usage.GenOutTokens += u.GenOutTokens
			if aerr != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("backlink align %s: %v", otherRel, aerr))
			} else if aligned != "" && strings.TrimSpace(aligned) != strings.TrimSpace(curBody) {
				// Preserve the caller's original whitespace framing when replacing trimmed body.
				curBody = aligned
				changes = append(changes, "aligned_context")
			}
		}

		if strings.TrimSpace(curBody) == strings.TrimSpace(origBodyNorm) {
			continue
		}

		polishedDisplay, diskBody := FormatBodyLikeOriginal(otherDoc.Body, rawBytes, strings.TrimSpace(curBody))
		origDisplay, _ := FormatBodyLikeOriginal(otherDoc.Body, rawBytes, strings.TrimSpace(origBodyNorm))
		if polishedDisplay == origDisplay {
			continue
		}

		res.Items = append(res.Items, BacklinkPlanItem{
			Change: PolishBacklinkChange{
				Path:     otherRel,
				Original: origDisplay,
				Polished: polishedDisplay,
				Changes:  dedupeNonEmptyStrings(changes),
				Applied:  false,
			},
			AbsPath:   otherAbs,
			OrigBytes: rawBytes,
			ParsedDoc: otherDoc,
			DiskBody:  diskBody,
		})
	}

	return res, nil
}

func linkTargetsNote(
	authoredTarget, targetRel, targetTitle string,
	docs []store.DocInfo,
	docAliasesByPath map[string][]string,
	liveResolver *store.Resolver,
	repairIdx *RepairIndex,
) (pointsHere bool, isBroken bool) {
	resolved, rerr := liveResolver.Resolve(authoredTarget)
	if rerr == nil {
		return resolved == targetRel, false
	}
	var ambErr *store.AmbiguousTargetError
	if errors.As(rerr, &ambErr) {
		return false, false
	}
	if !errors.Is(rerr, store.ErrTargetNotFound) {
		return false, false
	}

	slashNorm := strings.TrimPrefix(strings.ReplaceAll(authoredTarget, "\\", "/"), "/")
	slashNorm = strings.TrimSuffix(slashNorm, ".md")
	hasDir := strings.Contains(slashNorm, "/")

	if !hasDir {
		cands := repairIdx.Lookup(authoredTarget)
		if len(cands) == 1 {
			if resPath, err := liveResolver.Resolve(cands[0]); err == nil && resPath == targetRel {
				return true, true
			}
		}
		normMatches := findDocsByNormName(foldKey(authoredTarget), docs, docAliasesByPath)
		if len(normMatches) == 1 && normMatches[0] == targetRel {
			return true, true
		}
	} else {
		leaf := basenameNoExt(slashNorm)
		if leaf != "" {
			if resPath, err := liveResolver.Resolve(leaf); err == nil && resPath == targetRel {
				return true, true
			}
			leafCands := repairIdx.Lookup(leaf)
			if len(leafCands) == 1 {
				if resPath, err := liveResolver.Resolve(leafCands[0]); err == nil && resPath == targetRel {
					return true, true
				}
			}
			leafNorm := findDocsByNormName(foldKey(leaf), docs, docAliasesByPath)
			if len(leafNorm) == 1 && leafNorm[0] == targetRel {
				return true, true
			}
		}
	}

	cleanQuery := BasenameOrTarget(authoredTarget)
	if cleanQuery != "" && IsObviousTargetMatch(cleanQuery, targetTitle, targetRel) {
		var matches []string
		for _, d := range docs {
			if IsObviousTargetMatch(cleanQuery, d.Title, d.Path) {
				matches = append(matches, d.Path)
			}
		}
		if len(matches) == 1 && matches[0] == targetRel {
			return true, true
		}
	}

	return false, false
}

var (
	templaterBodyTagRe    = regexp.MustCompile(`(?s)<%.*?%>`)
	coreTemplateBodyTagRe = regexp.MustCompile(`(?i)\{\{\s*(?:date|time|title|yesterday|tomorrow)(?:\s*:[^}\n]*)?\s*\}\}`)
)

func shouldIncludeBacklinkSourcePath(srcRel, targetRel string, tmplFolders []string) bool {
	norm := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(srcRel), "\\", "/"), "/")
	normTarget := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(targetRel), "\\", "/"), "/")
	if norm == "" || norm == normTarget {
		return false
	}
	if vault.IsExcludedFolderPath(norm, tmplFolders) || vault.IsIgnored(norm) {
		return false
	}
	for _, seg := range strings.Split(norm, "/") {
		if seg != "" && (strings.HasPrefix(seg, ".") || seg == "node_modules") {
			return false
		}
	}
	return true
}

func hasUnresolvedBodyTemplatePlaceholders(body string) bool {
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.Contains(norm, "{{") && !strings.Contains(norm, "<%") {
		return false
	}
	var prose strings.Builder
	var fence document.CodeFenceTracker
	for _, line := range strings.Split(norm, "\n") {
		isDelim, inCode := fence.Step(line)
		if isDelim || inCode {
			continue
		}
		prose.WriteString(stripInlineCodeSpans(line))
		prose.WriteByte('\n')
	}
	text := prose.String()
	return templaterBodyTagRe.MatchString(text) || coreTemplateBodyTagRe.MatchString(text)
}

func stripInlineCodeSpans(line string) string {
	return document.MaskInlineCodeSpans(line)
}

func filterUnambiguousMentionSurfaces(
	surfaces []string,
	targetRel string,
	liveResolver *store.Resolver,
	docs []store.DocInfo,
	docAliasesByPath map[string][]string,
) []string {
	out := make([]string, 0, len(surfaces))
	for _, s := range surfaces {
		resolved, rerr := liveResolver.Resolve(s)
		if rerr != nil || resolved != targetRel {
			continue
		}
		matches := findDocsByNormName(foldKey(s), docs, docAliasesByPath)
		if len(matches) != 1 || matches[0] != targetRel {
			continue
		}
		out = append(out, s)
	}
	return out
}

func buildMentionSurfaces(title string, aliases []string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len([]rune(s)) < minSubstringLen {
			return
		}
		key := strings.ToLower(s)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, s)
	}
	add(title)
	for _, a := range aliases {
		add(a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i]) > len(out[j])
	})
	return out
}

// linkFirstProseMention wraps the first whole-word prose occurrence of any
// surface in surfaces with [[wikiDest]] (or [[wikiDest|matched]] if surface
// casing/text differs), strictly skipping fenced code, inline code, heading
// lines, and existing [[...]] / [...](...) links.
func linkFirstProseMention(body, wikiDest string, surfaces []string) (string, bool) {
	masked := document.MaskCodeRegions(body)
	lines := strings.Split(body, "\n")
	maskedLines := strings.Split(masked, "\n")
	if len(lines) != len(maskedLines) {
		return body, false
	}

	var fence document.CodeFenceTracker
	for li, line := range lines {
		mline := maskedLines[li]
		isDelim, inCode := fence.Step(mline)
		if isDelim || inCode || headingRe.MatchString(strings.TrimLeft(mline, " \t")) {
			continue
		}
		// Find existing link spans on this line so we never nest a link inside a link.
		excluded := existingLinkSpans(mline)
		for _, surface := range surfaces {
			if start, end, ok := findWholeWordOutsideSpans(line, mline, surface, excluded); ok {
				matched := line[start:end]
				replacement := "[[" + wikiDest + "]]"
				if matched != wikiDest {
					replacement = "[[" + wikiDest + "|" + matched + "]]"
				}
				lines[li] = line[:start] + replacement + line[end:]
				return strings.Join(lines, "\n"), true
			}
		}
	}
	return body, false
}

func existingLinkSpans(maskedLine string) [][2]int {
	var spans [][2]int
	for _, m := range wikilinkSpanRe.FindAllStringIndex(maskedLine, -1) {
		s := m[0]
		if s > 0 && maskedLine[s-1] == '!' {
			s--
		}
		spans = append(spans, [2]int{s, m[1]})
	}
	// Exclude markdown link spans [text](dest), [text][ref], [text]: url, and
	// standalone [label] bracketed tokens so reference definitions/links never
	// get wrapped into [[[Title]]].
	for i := 0; i < len(maskedLine); i++ {
		if maskedLine[i] != '[' {
			continue
		}
		if (i+1 < len(maskedLine) && maskedLine[i+1] == '[') || (i > 0 && maskedLine[i-1] == '[') {
			continue
		}
		closeBracket := strings.IndexByte(maskedLine[i+1:], ']')
		if closeBracket < 0 {
			continue
		}
		s := i
		if s > 0 && maskedLine[s-1] == '!' {
			s--
		}
		end := i + 1 + closeBracket + 1
		if end < len(maskedLine) {
			switch maskedLine[end] {
			case '(':
				if closeParen := strings.IndexByte(maskedLine[end+1:], ')'); closeParen >= 0 {
					end = end + 1 + closeParen + 1
				}
			case '[':
				if closeRef := strings.IndexByte(maskedLine[end+1:], ']'); closeRef >= 0 {
					end = end + 1 + closeRef + 1
				}
			case ':':
				end++
			}
		}
		spans = append(spans, [2]int{s, end})
		i = end - 1
	}
	return spans
}

func findWholeWordOutsideSpans(origLine, maskedLine, surface string, excluded [][2]int) (int, int, bool) {
	if len(origLine) != len(maskedLine) || surface == "" {
		return 0, 0, false
	}
	searchLine := document.MaskInlineCodeSpans(maskedLine)
	if len(searchLine) != len(origLine) {
		return 0, 0, false
	}
	surfaceRunes := []rune(surface)
	if len(surfaceRunes) == 0 {
		return 0, 0, false
	}

	// Collect rune byte offsets in searchLine so case-folding length changes
	// (e.g. 'İ', 'K') never shift byte slices relative to origLine.
	byteStarts := make([]int, 0, len(searchLine)+1)
	for pos := range searchLine {
		byteStarts = append(byteStarts, pos)
	}
	byteStarts = append(byteStarts, len(searchLine))

	runeCount := len(byteStarts) - 1
	surfLen := len(surfaceRunes)
	for ri := 0; ri+surfLen <= runeCount; ri++ {
		start := byteStarts[ri]
		end := byteStarts[ri+surfLen]
		if !strings.EqualFold(searchLine[start:end], surface) || !strings.EqualFold(origLine[start:end], surface) {
			continue
		}
		if start > 0 {
			prevRune, _ := utf8.DecodeLastRuneInString(origLine[:start])
			if isIdentChar(prevRune) {
				continue
			}
		}
		if end < len(origLine) {
			nextRune, _ := utf8.DecodeRuneInString(origLine[end:])
			if isIdentChar(nextRune) {
				continue
			}
		}
		inExcluded := false
		for _, sp := range excluded {
			if start < sp[1] && end > sp[0] {
				inExcluded = true
				break
			}
		}
		if inExcluded {
			continue
		}
		return start, end, true
	}
	return 0, 0, false
}

func isIdentChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func alignBacklinkNoteAI(
	ctx context.Context,
	in BacklinkPlanInput,
	otherRel string,
	curBody string,
	wikiDest string,
	mdDest string,
	targetTitle string,
	targetAliases []string,
	targetLinks []document.WikiLink,
	liveResolver *store.Resolver,
) (string, FixAIUsage, error) {
	var usage FixAIUsage
	var user strings.Builder
	fmt.Fprintf(&user, "Target note path: %s\n", in.TargetRel)
	if targetTitle != "" {
		fmt.Fprintf(&user, "Target note title: %s\n", targetTitle)
	}
	if strings.TrimSpace(in.Goal) != "" {
		fmt.Fprintf(&user, "Polish goal applied to target: %s\n", strings.TrimSpace(in.Goal))
	}
	fmt.Fprintf(&user, "\n----- TARGET BEFORE -----\n%s\n", truncateRunes(strings.TrimSpace(in.TargetOrigBody), 2000))
	fmt.Fprintf(&user, "\n----- TARGET AFTER -----\n%s\n", truncateRunes(strings.TrimSpace(in.TargetNewBody), 2000))
	fmt.Fprintf(&user, "\n----- REFERENCING NOTE (%s) -----\n%s\n", otherRel, strings.TrimSpace(curBody))

	opts := ai.GenOpts{
		Temperature:     ai.Ptr(0.2),
		MaxTokens:       4096,
		SystemPrompt:    backlinkAlignSystem,
		ReasoningEffort: in.ReasoningEffort,
	}
	out, u, err := callGeneratorWithUsage(ctx, in.Generator, user.String(), opts)
	usage.GenCalls = 1
	usage.GenInTokens = u.InputTokens
	usage.GenOutTokens = u.OutputTokens
	if err != nil {
		return "", usage, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || strings.EqualFold(trimmed, "NO_CHANGE") {
		return "", usage, nil
	}

	allowed := AllowedLinkSet(nil, curBody)
	allowed[NormalizeLinkKey(wikiDest)] = true
	if targetTitle != "" {
		allowed[NormalizeLinkKey(targetTitle)] = true
	}
	allowed[NormalizeLinkKey(strings.TrimSuffix(in.TargetRel, ".md"))] = true
	allowed[NormalizeLinkKey(basenameNoExt(in.TargetRel))] = true
	for _, a := range targetAliases {
		allowed[NormalizeLinkKey(a)] = true
	}
	if mdDest != "" {
		allowed[NormalizeLinkKey(mdDest)] = true
	}

	cleaned, _, violations := ValidateBacklinkAlignedBody(curBody, trimmed, allowed, in.TargetRel, liveResolver)
	if len(violations) > 0 {
		return "", usage, nil
	}
	if !targetLinkAndSuffixesPreserved(cleaned, in.TargetRel, targetLinks, liveResolver) {
		return "", usage, nil
	}
	return cleaned, usage, nil
}

func targetLinkAndSuffixesPreserved(
	cleaned string,
	targetRel string,
	origTargetLinks []document.WikiLink,
	liveResolver *store.Resolver,
) bool {
	links := document.ExtractWikiLinks(cleaned)
	hasTargetLink := false
	for _, l := range links {
		if resolved, err := liveResolver.Resolve(strings.TrimSpace(l.Target)); err == nil && resolved == targetRel {
			hasTargetLink = true
			break
		}
	}
	if !hasTargetLink {
		return false
	}

	// Ensure existing #heading, #^block, and |alias suffixes on target links survive.
	for _, orig := range origTargetLinks {
		if orig.Heading != "" {
			if !strings.Contains(cleaned, "#"+orig.Heading) {
				return false
			}
		}
		if orig.Block != "" {
			if !strings.Contains(cleaned, "#^"+orig.Block) {
				return false
			}
		}
		if orig.Alias != "" && (strings.HasPrefix(orig.Raw, "[[") || strings.HasPrefix(orig.Raw, "![[")) {
			if !strings.Contains(cleaned, "|"+orig.Alias) {
				return false
			}
		}
	}
	return true
}

func dedupeNonEmptyStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
