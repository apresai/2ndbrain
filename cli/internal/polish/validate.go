package polish

import (
	"regexp"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
)

// wikilinkSpanRe matches a [[...]] wikilink body (without the optional leading
// '!' embed marker). Used for in-place stripping; a leading '!' is handled by
// the caller inspecting the preceding byte.
var wikilinkSpanRe = regexp.MustCompile(`\[\[[^\]]+\]\]`)

// NormalizeLinkKey canonicalizes a wikilink target or candidate title for
// case-insensitive membership checks: lowercased, trimmed, forward slashes, no
// leading "./", no trailing ".md". Both the allowed-set builder and
// StripInventedLinks must run targets through this so they agree.
func NormalizeLinkKey(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimSuffix(s, ".md")
	return strings.ToLower(strings.TrimSpace(s))
}

// linkTarget returns the resolution target of a wikilink inner (the text
// between [[ and ]]): everything before the first '#' anchor or '|' alias.
func linkTarget(inner string) string {
	t := inner
	if i := strings.IndexByte(t, '|'); i >= 0 {
		t = t[:i]
	}
	if i := strings.IndexByte(t, '#'); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

// linkDisplay returns the human-visible text for a wikilink inner: the alias
// when present, else the target portion (anchor stripped). Used as the
// replacement when stripping an invented link so the prose still reads.
func linkDisplay(inner string) string {
	if i := strings.IndexByte(inner, '|'); i >= 0 {
		return strings.TrimSpace(inner[i+1:])
	}
	t := inner
	if i := strings.IndexByte(t, '#'); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

// StripInventedLinks removes any [[wikilink]] in body whose target is not in
// allowed (keys must be NormalizeLinkKey-normalized), replacing it with its
// display text so the surrounding prose is unbroken. It is the deterministic
// backstop that guarantees `polish --links --write` can never persist a link to
// a note that does not exist, even if the model ignores its instructions.
//
// allowed should contain the candidate titles + aliases AND the targets of
// links already present in the document (so existing links are never stripped).
// Links inside code (fenced or inline) are ignored, code is never modified.
// Returns the cleaned body and the list of removed (raw) targets.
func StripInventedLinks(body string, allowed map[string]bool) (string, []string) {
	// Mask code so wikilink-looking text inside backticks is not matched; the
	// mask only blanks bracket bytes, so spans found here map onto body 1:1.
	masked := document.MaskCodeRegions(body)

	var removed []string
	var out strings.Builder
	last := 0
	for _, m := range wikilinkSpanRe.FindAllStringIndex(masked, -1) {
		start, end := m[0], m[1]
		inner := body[start+2 : end-2] // strip the [[ and ]]
		target := linkTarget(inner)
		if allowed[NormalizeLinkKey(target)] {
			continue // keep: leave this span untouched in the output
		}
		// Strip: also swallow a leading '!' embed marker if present.
		stripStart := start
		if stripStart > 0 && body[stripStart-1] == '!' {
			stripStart--
		}
		out.WriteString(body[last:stripStart])
		out.WriteString(linkDisplay(inner))
		removed = append(removed, target)
		last = end
	}
	if len(removed) == 0 {
		return body, nil
	}
	out.WriteString(body[last:])
	return out.String(), removed
}

// AllowedLinkSet returns the normalized set of link targets that
// StripInventedLinks is permitted to keep: every candidate title + alias, plus
// every wikilink target already present in originalBody (so the author's
// existing links are never stripped). Shared by the CLI and MCP polish paths.
func AllowedLinkSet(cands []LinkCandidate, originalBody string) map[string]bool {
	allowed := make(map[string]bool)
	for _, c := range cands {
		allowed[NormalizeLinkKey(c.Title)] = true
		for _, a := range c.Aliases {
			allowed[NormalizeLinkKey(a)] = true
		}
	}
	for _, l := range document.ExtractWikiLinks(originalBody) {
		allowed[NormalizeLinkKey(l.Target)] = true
	}
	return allowed
}

// NewLinks returns the wikilinks present in polished but absent from original,
// compared by resolution target (case-insensitive). It powers the "no invented
// links" judge gate and the LinksAdded report.
func NewLinks(original, polished string) []document.WikiLink {
	seen := make(map[string]bool)
	for _, l := range document.ExtractWikiLinks(original) {
		seen[NormalizeLinkKey(l.Target)] = true
	}
	var added []document.WikiLink
	for _, l := range document.ExtractWikiLinks(polished) {
		if !seen[NormalizeLinkKey(l.Target)] {
			added = append(added, l)
		}
	}
	return added
}

// ExistingLinksPreserved reports whether every wikilink target in original
// still appears (by normalized target) in polished. A copy-edit must not drop
// the author's existing links.
func ExistingLinksPreserved(original, polished string) bool {
	have := make(map[string]bool)
	for _, l := range document.ExtractWikiLinks(polished) {
		have[NormalizeLinkKey(l.Target)] = true
	}
	for _, l := range document.ExtractWikiLinks(original) {
		if !have[NormalizeLinkKey(l.Target)] {
			return false
		}
	}
	return true
}

// CodeSpansEqual reports whether the fenced and inline code content is identical
// between original and polished. A copy-edit must never alter code.
func CodeSpansEqual(original, polished string) bool {
	return extractCode(original) == extractCode(polished)
}

// HeadingStructureEqual reports whether the heading lines (level + text), in
// order, are identical between original and polished. Headings inside fenced
// code blocks are ignored.
func HeadingStructureEqual(original, polished string) bool {
	return strings.Join(extractHeadings(original), "\n") == strings.Join(extractHeadings(polished), "\n")
}

var headingRe = regexp.MustCompile(`^#{1,6}[ \t]`)

// extractHeadings returns the heading lines (trimmed) outside fenced code.
func extractHeadings(body string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if isFenceLine(trimmed) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if headingRe.MatchString(trimmed) {
			out = append(out, strings.TrimSpace(trimmed))
		}
	}
	return out
}

// extractCode returns a canonical concatenation of all code content (fenced
// blocks and inline spans) so two bodies can be compared for code equality.
func extractCode(body string) string {
	var out strings.Builder
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if isFenceLine(strings.TrimRight(line, " \t")) {
			inFence = !inFence
			out.WriteString("\x00FENCE\x00")
			continue
		}
		if inFence {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		for _, span := range inlineCodeRe.FindAllString(line, -1) {
			out.WriteString(span)
			out.WriteByte('\x00')
		}
	}
	return out.String()
}

var inlineCodeRe = regexp.MustCompile("`[^`]+`")

func isFenceLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}
