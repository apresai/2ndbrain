package document

import "strings"

// RewriteWikiLinks returns a copy of body in which every [[wikilink]] whose
// target resolves to oldTarget has had its target portion rewritten to point at
// newTarget, and the number of rewrites performed. Both oldTarget and newTarget
// are vault-relative paths (with or without the ".md" extension); the function
// preserves whichever form the author wrote (a bare basename stays a basename,
// a path stays a path) so the rewritten link reads as naturally as the original.
//
// Only standard double-bracket wikilinks are rewritten ([[target]],
// [[target#heading]], [[target#^block]], [[target|alias]], and the leading-!
// embed forms). Markdown-style links ([text](path)) are left untouched: they
// carry percent-encoding and relative-path semantics that a conservative
// rename should not silently rewrite.
//
// Three invariants make this safe to run across every note in a vault:
//
//   - Links inside fenced or inline code are never rewritten. We mask code
//     regions (the same maskCodeRegions ExtractWikiLinks uses), find link spans
//     on the masked copy, and apply edits to the REAL body at those offsets, so
//     documentation discussing [[wikilink]] syntax in a code span is preserved.
//   - The #heading / #^block / |alias suffix and any leading "!" embed marker
//     are preserved verbatim; only the target portion changes.
//   - Matching mirrors the resolution tiers in store.ResolveLinks: a link
//     matches oldTarget when its (slash-normalized, extension-insensitive)
//     target equals the old full path, the old basename, or a "/"-delimited
//     path suffix of the old path. The author's chosen form determines the
//     replacement form: a bare basename link becomes the new basename, a path
//     link becomes the new path.
func RewriteWikiLinks(body, oldTarget, newTarget string) (string, int) {
	return rewriteWikiLinks(body, oldTarget, newTarget, false)
}

// RewriteWikiLinksPathOnly is RewriteWikiLinks restricted to path-bearing link
// forms (the full path or a multi-segment suffix); it never touches a bare
// [[basename]] link. The move command uses it when the old basename is ambiguous
// (names more than one note in the vault): a bare-name link can't be safely
// attributed to the moved note, but a path-qualified link still can be, so the
// path-form links are rewritten and the bare ones are left for the operator to
// resolve.
func RewriteWikiLinksPathOnly(body, oldTarget, newTarget string) (string, int) {
	return rewriteWikiLinks(body, oldTarget, newTarget, true)
}

// rewriteWikiLinks is the shared engine for the two exported variants. When
// pathOnly is true, bare-basename matches are skipped.
func rewriteWikiLinks(body, oldTarget, newTarget string, pathOnly bool) (string, int) {
	oldForms := targetForms(oldTarget)
	if len(oldForms) == 0 {
		return body, 0
	}
	newBase := Basename(newTarget)
	newPath := normalizeTarget(newTarget)

	// Mask code regions so link spans inside code are skipped. Offsets in the
	// masked copy line up byte-for-byte with the real body (maskCodeRegions only
	// overwrites '[' / ']' bytes with spaces), so we read targets from the mask
	// and splice replacements into the real body at the same offsets.
	scan := maskCodeRegions(body)

	var out strings.Builder
	out.Grow(len(body))
	count := 0
	last := 0

	for _, m := range wikilinkRe.FindAllStringSubmatchIndex(scan, -1) {
		inner := scan[m[2]:m[3]]

		// Split off the |alias and #anchor suffixes; only the target portion is
		// eligible for rewriting. indexOf scans for the first byte, matching the
		// order ExtractWikiLinks uses (alias first, then anchor).
		target := inner
		if idx := indexOf(target, '|'); idx >= 0 {
			target = target[:idx]
		}
		if idx := indexOf(target, '#'); idx >= 0 {
			target = target[:idx]
		}

		form, ok := matchForm(target, oldForms)
		if !ok {
			continue
		}
		if pathOnly && form == formBasename {
			continue
		}

		// Replace using the SAME form the author wrote: a bare basename link
		// becomes the new basename, any path-bearing form becomes the new path.
		replacement := newPath
		if form == formBasename {
			replacement = newBase
		}
		if replacement == target {
			continue // no-op (target text already matches the replacement)
		}

		// Splice the real body: keep everything up to the inner target start,
		// write the replacement, then resume after the original target text.
		out.WriteString(body[last:m[2]])
		out.WriteString(replacement)
		// Re-emit the alias/anchor suffix from the real body to preserve its
		// exact bytes (anchors/aliases don't legally contain brackets, so the
		// masked copy equals the real body across this span).
		out.WriteString(body[m[2]+len(target) : m[3]])
		last = m[3]
		count++
	}

	if count == 0 {
		return body, 0
	}
	out.WriteString(body[last:])
	return out.String(), count
}

// matchKind records which resolution form a link's target matched, so the
// replacement can be written in the same shape.
type matchKind int

const (
	formNone matchKind = iota
	formPath
	formBasename
)

// matchForm reports whether target (the raw target portion of a wikilink, with
// alias/anchor already stripped) matches one of the old document's resolvable
// forms, and in which shape. A path-bearing match (full path or a multi-segment
// suffix) reports formPath; a bare-name match reports formBasename. Matching is
// extension-insensitive and slash-normalized, mirroring store.ResolveLinks.
func matchForm(target string, oldForms map[string]matchKind) (matchKind, bool) {
	key := normalizeTarget(target)
	if k, ok := oldForms[key]; ok {
		return k, true
	}
	return formNone, false
}

// targetForms builds the set of resolvable forms for the old document path,
// each mapped to whether it is the bare basename or a path-bearing form. This
// mirrors the nameIndex construction in store.ResolveLinks: the full path, every
// "/"-delimited suffix, and the basename, all extension-stripped and
// slash-normalized so a link in any of those shapes resolves to this doc.
//
// The basename maps to formBasename; every longer suffix and the full path map
// to formPath. When a suffix collides with the basename (single-segment path),
// formBasename wins so a root-level note rewrites as a bare name.
func targetForms(oldTarget string) map[string]matchKind {
	full := normalizeTarget(oldTarget)
	if full == "" {
		return nil
	}
	forms := make(map[string]matchKind)

	segs := strings.Split(full, "/")
	// A root-level note's full path IS its basename: tag it formBasename so a
	// bare [[name]] link stays bare on a move into a subfolder (the new full
	// path would otherwise replace it). Multi-segment paths register the full
	// path as formPath below and the trailing segment as formBasename.
	if len(segs) == 1 {
		forms[full] = formBasename
	} else {
		forms[full] = formPath
	}

	for i := 1; i < len(segs); i++ {
		suffix := strings.Join(segs[i:], "/")
		if suffix == "" {
			continue
		}
		if i == len(segs)-1 {
			// Last segment is the bare basename.
			forms[suffix] = formBasename
		} else if _, exists := forms[suffix]; !exists {
			forms[suffix] = formPath
		}
	}
	return forms
}

// normalizeTarget canonicalizes a wikilink target or a vault-relative path for
// matching: backslashes to forward slashes, a stripped leading slash, and a
// stripped ".md" extension. Mirrors the normalization in store.ResolveLinks.
func normalizeTarget(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, ".md")
	return s
}

// Basename returns the final "/"-delimited segment of a vault-relative path or
// wikilink target, extension-stripped and slash-normalized. Exported so the move
// command can report and compare the moved note's bare name.
func Basename(s string) string {
	n := normalizeTarget(s)
	if idx := strings.LastIndexByte(n, '/'); idx >= 0 {
		return n[idx+1:]
	}
	return n
}
