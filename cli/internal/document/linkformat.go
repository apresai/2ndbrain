package document

import (
	"path/filepath"
	"sort"
	"strings"
)

// LinkFormatChange describes one syntactic link normalization applied to a body.
type LinkFormatChange struct {
	From  string
	To    string
	Count int
}

type textEdit struct {
	start   int
	end     int
	newText string
	from    string
	to      string
}

// NormalizeLinkFormatting cleans up syntactic glitches in internal note links
// outside fenced/inline code blocks:
//   - [[wikilinks]]: trims leading/trailing whitespace around the target,
//     converts backslashes to forward slashes, and strips redundant ".md"
//     extensions while preserving #anchor / #^block and |alias suffixes.
//   - [markdown](links): converts backslashes/outer spaces and percent-encodes
//     unencoded spaces when the destination resolves to an existing note.
//
// Asset embeds and external URLs are never touched.
func NormalizeLinkFormatting(body string, isResolvable func(target string) bool) (string, []LinkFormatChange) {
	scan := maskCodeRegions(body)
	var edits []textEdit

	for _, m := range wikilinkRe.FindAllStringSubmatchIndex(scan, -1) {
		if m[0] > 0 && scan[m[0]-1] == '!' {
			continue
		}
		inner := scan[m[2]:m[3]]
		targetAndAnchor := inner
		aliasSuffix := ""
		hasEmptyPipe := false
		if idx := indexOf(inner, '|'); idx >= 0 {
			targetAndAnchor = inner[:idx]
			if strings.TrimSpace(inner[idx+1:]) == "" {
				hasEmptyPipe = true
			} else {
				aliasSuffix = inner[idx:]
			}
		}
		target := targetAndAnchor
		anchorSuffix := ""
		if idx := indexOf(targetAndAnchor, '#'); idx >= 0 {
			target = targetAndAnchor[:idx]
			anchorSuffix = targetAndAnchor[idx:]
		}
		clean := stripTrailingMD(strings.TrimSpace(strings.ReplaceAll(target, "\\", "/")))
		if clean == "" || isNonNoteAssetPath(clean) {
			continue
		}
		rebuiltInner := clean + anchorSuffix + aliasSuffix
		if rebuiltInner == inner && !hasEmptyPipe {
			continue
		}
		edits = append(edits, textEdit{
			start:   m[2],
			end:     m[3],
			newText: rebuiltInner,
			from:    inner,
			to:      rebuiltInner,
		})
	}

	for _, m := range mdLinkRe.FindAllStringSubmatchIndex(scan, -1) {
		if m[0] > 0 && scan[m[0]-1] == '!' {
			continue
		}
		rawDest := scan[m[4]:m[5]]
		if isExternalLink(rawDest) || strings.HasPrefix(rawDest, "#") || strings.HasPrefix(rawDest, "?") {
			continue
		}
		pathPart := rawDest
		if idx := strings.IndexAny(pathPart, "#?"); idx >= 0 {
			pathPart = pathPart[:idx]
		}
		trimmed := strings.TrimSpace(strings.ReplaceAll(pathPart, "\\", "/"))
		if trimmed == "" {
			continue
		}
		decoded := DecodeLinkTarget(trimmed)
		if isNonNoteAssetPath(decoded) {
			continue
		}
		needsEncoding := trimmed != pathPart || strings.ContainsAny(trimmed, " ")
		if !needsEncoding {
			continue
		}
		if isResolvable != nil && !isResolvable(decoded) {
			continue
		}
		encoded := EncodeLinkTarget(decoded)
		if encoded == pathPart {
			continue
		}
		edits = append(edits, textEdit{
			start:   m[4],
			end:     m[4] + len(pathPart),
			newText: encoded,
			from:    pathPart,
			to:      encoded,
		})
	}

	if len(edits) == 0 {
		return body, nil
	}

	out, applied := applyEdits(body, edits)
	if len(applied) == 0 {
		return body, nil
	}

	byKey := make(map[string]*LinkFormatChange)
	var order []string
	for _, e := range applied {
		key := e.from + "\x00" + e.to
		if ch, ok := byKey[key]; ok {
			ch.Count++
			continue
		}
		byKey[key] = &LinkFormatChange{From: e.from, To: e.to, Count: 1}
		order = append(order, key)
	}
	changes := make([]LinkFormatChange, 0, len(order))
	for _, key := range order {
		changes = append(changes, *byKey[key])
	}
	return out, changes
}

// RewriteExactLinkTarget replaces every non-code occurrence of authoredTarget
// with wikiDest (for [[wikilinks]]) or EncodeLinkTarget(mdDest) (for markdown
// links), matching ONLY the exact authored target rather than expanding path
// suffixes onto unrelated bare links. Preserves #anchor / #^block / |alias.
func RewriteExactLinkTarget(body, authoredTarget, wikiDest, mdDest string) (string, int) {
	wantKey := exactTargetKey(authoredTarget)
	if wantKey == "" {
		return body, 0
	}
	scan := maskCodeRegions(body)
	var edits []textEdit

	for _, m := range wikilinkRe.FindAllStringSubmatchIndex(scan, -1) {
		inner := scan[m[2]:m[3]]
		target := inner
		if idx := indexOf(target, '|'); idx >= 0 {
			target = target[:idx]
		}
		if idx := indexOf(target, '#'); idx >= 0 {
			target = target[:idx]
		}
		if exactTargetKey(target) != wantKey {
			continue
		}
		if wikiDest == target {
			continue
		}
		edits = append(edits, textEdit{
			start:   m[2],
			end:     m[2] + len(target),
			newText: wikiDest,
		})
	}

	if mdDest != "" {
		encodedMD := EncodeLinkTarget(mdDest)
		for _, m := range mdLinkRe.FindAllStringSubmatchIndex(scan, -1) {
			if m[0] > 0 && scan[m[0]-1] == '!' {
				continue
			}
			rawDest := scan[m[4]:m[5]]
			if isExternalLink(rawDest) || strings.HasPrefix(rawDest, "#") || strings.HasPrefix(rawDest, "?") {
				continue
			}
			pathPart := rawDest
			if idx := strings.IndexAny(pathPart, "#?"); idx >= 0 {
				pathPart = pathPart[:idx]
			}
			if pathPart == "" {
				continue
			}
			if exactTargetKey(pathPart) != wantKey && exactTargetKey(DecodeLinkTarget(pathPart)) != wantKey {
				continue
			}
			if encodedMD == pathPart || DecodeLinkTarget(encodedMD) == DecodeLinkTarget(pathPart) {
				continue
			}
			edits = append(edits, textEdit{
				start:   m[4],
				end:     m[4] + len(pathPart),
				newText: encodedMD,
			})
		}
	}

	if len(edits) == 0 {
		return body, 0
	}
	out, applied := applyEdits(body, edits)
	return out, len(applied)
}

// UnlinkExactTarget unwraps every non-embed, non-code wikilink [[target...]]
// and internal markdown note link [label](target...) whose target matches
// authoredTarget verbatim (after slash and .md normalization), keeping only
// the visible text.
func UnlinkExactTarget(body, authoredTarget string) (string, int) {
	wantKey := exactTargetKey(authoredTarget)
	if wantKey == "" {
		return body, 0
	}
	scan := maskCodeRegions(body)
	var edits []textEdit

	for _, m := range wikilinkRe.FindAllStringSubmatchIndex(scan, -1) {
		if m[0] > 0 && scan[m[0]-1] == '!' {
			continue
		}
		inner := scan[m[2]:m[3]]
		target := inner
		alias := ""
		if idx := indexOf(target, '|'); idx >= 0 {
			alias = target[idx+1:]
			target = target[:idx]
		}
		if idx := indexOf(target, '#'); idx >= 0 {
			target = target[:idx]
		}
		if exactTargetKey(target) != wantKey {
			continue
		}
		visible := strings.TrimSpace(alias)
		if visible == "" {
			visible = strings.TrimSpace(target)
		}
		edits = append(edits, textEdit{start: m[0], end: m[1], newText: visible})
	}

	for _, m := range mdLinkRe.FindAllStringSubmatchIndex(scan, -1) {
		if m[0] > 0 && scan[m[0]-1] == '!' {
			continue
		}
		label := body[m[2]:m[3]]
		rawDest := scan[m[4]:m[5]]
		if isExternalLink(rawDest) || strings.HasPrefix(rawDest, "#") || strings.HasPrefix(rawDest, "?") {
			continue
		}
		pathPart := rawDest
		if idx := strings.IndexAny(pathPart, "#?"); idx >= 0 {
			pathPart = pathPart[:idx]
		}
		if pathPart == "" {
			continue
		}
		if exactTargetKey(pathPart) != wantKey && exactTargetKey(DecodeLinkTarget(pathPart)) != wantKey {
			continue
		}
		edits = append(edits, textEdit{start: m[0], end: m[1], newText: label})
	}

	if len(edits) == 0 {
		return body, 0
	}
	out, applied := applyEdits(body, edits)
	return out, len(applied)
}

func applyEdits(body string, edits []textEdit) (string, []textEdit) {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out strings.Builder
	out.Grow(len(body))
	var applied []textEdit
	last := 0
	for _, e := range edits {
		if e.start < last {
			continue
		}
		out.WriteString(body[last:e.start])
		out.WriteString(e.newText)
		last = e.end
		applied = append(applied, e)
	}
	out.WriteString(body[last:])
	return out.String(), applied
}

func exactTargetKey(t string) string {
	t = strings.TrimSpace(t)
	t = strings.ReplaceAll(t, "\\", "/")
	t = strings.TrimPrefix(t, "/")
	return stripTrailingMD(t)
}

func stripTrailingMD(s string) string {
	if len(s) > 3 && strings.EqualFold(s[len(s)-3:], ".md") {
		return s[:len(s)-3]
	}
	return s
}

func isNonNoteAssetPath(target string) bool {
	if strings.TrimSpace(target) == "" {
		return true
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(target))) {
	case "", ".md", ".canvas", ".base":
		return false
	default:
		return true
	}
}
