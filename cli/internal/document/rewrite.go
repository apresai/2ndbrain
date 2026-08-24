package document

import (
	"sort"
	"strings"
)

// RewriteWikiLinks returns a copy of body in which every [[wikilink]] whose
// target resolves to oldTarget has had its target portion rewritten to point at
// newTarget, and the number of rewrites performed. Both oldTarget and newTarget
// are vault-relative paths (with or without the ".md" extension); the function
// preserves whichever form the author wrote (a bare basename stays a basename,
// a path stays a path) so the rewritten link reads as naturally as the original.
//
// Both double-bracket wikilinks ([[target]], [[target#heading]], [[target#^block]],
// [[target|alias]], and the leading-! embed forms) AND markdown-style links
// ([text](target), ![alt](target)) are rewritten. Markdown links carry their
// own subtleties (percent-encoding, anchor-only targets, external URLs); the
// markdown pass is deliberately conservative: it skips external URLs and
// anchor-only targets, and any path it cannot confidently match is left
// untouched (a missed rewrite is far safer than a corrupted path).
//
// Three invariants make this safe to run across every note in a vault:
//
//   - Links inside fenced or inline code are never rewritten. We mask code
//     regions (the same maskCodeRegions ExtractWikiLinks uses), find link spans
//     on the masked copy, and apply edits to the REAL body at those offsets, so
//     documentation discussing [[wikilink]] syntax in a code span is preserved.
//   - The #heading / #^block / |alias suffix (wikilinks) and the [label] text +
//     any #anchor / ?query suffix (markdown links) and any leading "!" embed
//     marker are preserved verbatim; only the target portion changes.
//   - Matching mirrors the resolution tiers in store.ResolveLinks: a link
//     matches oldTarget when its (slash-normalized, extension-insensitive)
//     target equals the old full path, the old basename, or a "/"-delimited
//     path suffix of the old path. The author's chosen form determines the
//     replacement form: a bare basename link becomes the new basename, a path
//     link becomes the new path. A markdown link additionally keeps its ".md"
//     extension since the file-path form is the markdown convention.
//     (RewriteLinksSyntaxAware overrides the markdown derivation with a
//     caller-supplied destination; this function never does.)
func RewriteWikiLinks(body, oldTarget, newTarget string) (string, int) {
	return rewriteWikiLinks(body, oldTarget, newTarget, false, "")
}

// RewriteLinksSyntaxAware is RewriteWikiLinks with a per-syntax destination:
// wikilink occurrences are rewritten to wikiTarget exactly as RewriteWikiLinks
// would, while EVERY matched markdown occurrence gets the caller-supplied
// mdDest substituted verbatim (percent-encoded as needed), ignoring the
// bare-vs-path form derivation. Repair and relink use it because their chosen
// destination may be a note TITLE: titles resolve for wikilinks (title tier)
// but a title+".md" markdown destination resolves through no tier, so the
// markdown emission must be a path-based form the caller has verified resolves
// (see polish.MarkdownDestinationFor). Move/rename keep RewriteWikiLinks: they
// always rewrite path-to-path, where the derived form is already resolvable.
func RewriteLinksSyntaxAware(body, oldTarget, wikiTarget, mdDest string) (string, int) {
	return rewriteWikiLinks(body, oldTarget, wikiTarget, false, mdDest)
}

// RewriteWikiLinksPathOnly is RewriteWikiLinks restricted to path-bearing link
// forms (the full path or a multi-segment suffix); it never touches a bare
// [[basename]] (or [label](basename.md)) link. The move command uses it when the
// old basename is ambiguous (names more than one note in the vault): a bare-name
// link can't be safely attributed to the moved note, but a path-qualified link
// still can be, so the path-form links are rewritten and the bare ones are left
// for the operator to resolve.
func RewriteWikiLinksPathOnly(body, oldTarget, newTarget string) (string, int) {
	return rewriteWikiLinks(body, oldTarget, newTarget, true, "")
}

// UnlinkWikiLink returns a copy of body in which every [[wikilink]] whose target
// matches the authored target has its brackets removed while the visible text is
// kept, and the number of links unlinked. It is the "remove the link, keep the
// words" resolution for a broken wikilink that names no real note:
// [[083477d]] -> 083477d, [[note#Setup]] -> note, [[page|the page]] -> the page.
//
// Matching mirrors RewriteWikiLinks (and store.ResolveLinks): a link matches
// when its slash-normalized, extension-insensitive target equals the authored
// target's full path, basename, or a path suffix. Matching is case- and
// separator-SENSITIVE (unlike repair's fuzzy normalizeName) because unlink acts
// on the EXACT broken link the caller named, never a near-miss.
//
// The same safety invariants as rewriteWikiLinks hold: links inside fenced or
// inline code are never touched (we scan a code-masked copy and splice the real
// body at the same offsets), and embed/transclusion forms (![[...]]) are skipped
// so asset embeds stay intact. Only the [[ ]] wikilink form is handled; markdown
// [text](path) links are left alone (broken-link findings are always the [[ ]]
// form, and assets are excluded upstream by lint).
//
// The kept visible text is the alias when the link has one ([[X|alias]] ->
// alias), else the bare target text with any #heading / #^block suffix dropped
// (the target no longer resolves, so its anchor is meaningless).
func UnlinkWikiLink(body, target string) (string, int) {
	forms := targetForms(target)
	if len(forms) == 0 {
		return body, 0
	}
	scan := maskCodeRegions(body)

	type edit struct {
		start, end int
		newText    string
	}
	var edits []edit

	for _, m := range wikilinkRe.FindAllStringSubmatchIndex(scan, -1) {
		// Skip embeds/transclusions (![[...]]): never a broken note link, and
		// asset embeds must stay intact.
		if m[0] > 0 && scan[m[0]-1] == '!' {
			continue
		}
		inner := scan[m[2]:m[3]]

		// Split off |alias and #anchor; the target is what we match on, the alias
		// (if any) is the text we keep. Mirrors ExtractWikiLinks' order.
		linkTarget := inner
		alias := ""
		if idx := indexOf(linkTarget, '|'); idx >= 0 {
			alias = linkTarget[idx+1:]
			linkTarget = linkTarget[:idx]
		}
		if idx := indexOf(linkTarget, '#'); idx >= 0 {
			linkTarget = linkTarget[:idx]
		}

		if _, ok := matchForm(linkTarget, forms); !ok {
			continue
		}

		visible := alias
		if visible == "" {
			visible = linkTarget
		}
		// Replace the whole [[...]] span (m[0]..m[1]) with the visible text.
		edits = append(edits, edit{start: m[0], end: m[1], newText: visible})
	}

	if len(edits) == 0 {
		return body, 0
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })

	var out strings.Builder
	out.Grow(len(body))
	count := 0
	last := 0
	for _, e := range edits {
		if e.start < last {
			continue // overlaps a prior edit, skip (defensive)
		}
		out.WriteString(body[last:e.start])
		out.WriteString(e.newText)
		last = e.end
		count++
	}
	out.WriteString(body[last:])
	return out.String(), count
}

// rewriteWikiLinks is the shared engine for the exported variants. When
// pathOnly is true, bare-basename matches are skipped. It rewrites both
// [[wikilink]] and markdown [label](target) forms that resolve to oldTarget.
// A non-empty mdOverride replaces the markdown pass's derived destinations
// (both the bare and path forms) with EncodeLinkTarget(mdOverride) verbatim;
// the wikilink pass is unaffected by it.
func rewriteWikiLinks(body, oldTarget, newTarget string, pathOnly bool, mdOverride string) (string, int) {
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

	// Collect the rewrite edits from both link forms. An edit replaces the byte
	// span [start,end) of the body with newText. Wikilinks and markdown links
	// are scanned independently, then merged and applied left-to-right; on the
	// rare overlap (a wikilink and a markdown match touching the same bytes) the
	// earlier-starting edit wins and the overlapping one is dropped.
	type edit struct {
		start, end int
		newText    string
	}
	var edits []edit

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
		// Edit only the target portion (the inner start through the end of the
		// raw target text); the alias/anchor suffix is left in place by ending
		// the span at m[2]+len(target).
		edits = append(edits, edit{start: m[2], end: m[2] + len(target), newText: replacement})
	}

	// Markdown replacements are loop-invariant: encode them once. Encoding is
	// an identity on clean paths, so applying it unconditionally is safe; it
	// keeps a rewritten destination a valid bare CommonMark destination when
	// the new path contains a space, %, #, ?, or parenthesis. An mdOverride
	// (RewriteLinksSyntaxAware) substitutes the caller's pre-verified
	// destination for BOTH forms: the caller has already chosen the shortest
	// resolvable form, so the authored bare-vs-path shape no longer drives it.
	mdNewPath := EncodeLinkTarget(newPath + ".md")
	mdNewBase := EncodeLinkTarget(newBase + ".md")
	if mdOverride != "" {
		enc := EncodeLinkTarget(mdOverride)
		mdNewPath, mdNewBase = enc, enc
	}

	for _, m := range mdLinkRe.FindAllStringSubmatchIndex(scan, -1) {
		// Group 2 (m[4]:m[5]) is the (target) portion inside the parentheses.
		rawTarget := scan[m[4]:m[5]]

		// Skip external URLs (http/https/mailto/file/ftp) and anchor-only or
		// query-only targets ("#foo", "?bar"): those never name a vault note.
		if isExternalLink(rawTarget) || strings.HasPrefix(rawTarget, "#") || strings.HasPrefix(rawTarget, "?") {
			continue
		}

		// Split off a #anchor or ?query suffix; only the path part is matched
		// and rewritten, the suffix is preserved verbatim. Whichever delimiter
		// appears first bounds the path (Obsidian writes the anchor as #heading;
		// a ?query is rare but kept intact if present).
		pathPart := rawTarget
		if idx := strings.IndexAny(pathPart, "#?"); idx >= 0 {
			pathPart = pathPart[:idx]
		}
		if pathPart == "" {
			continue
		}

		// Obsidian percent-encodes spaces when it generates markdown links, so
		// match the raw form first (a literal-% filename wins exactly), then
		// retry the decoded form on a miss. Decode happens AFTER the #? split
		// above so a literal %23 in a filename can never be mis-split as an
		// anchor.
		form, ok := matchForm(pathPart, oldForms)
		if !ok {
			if decoded := DecodeLinkTarget(pathPart); decoded != pathPart {
				form, ok = matchForm(decoded, oldForms)
			}
		}
		if !ok {
			continue
		}
		if pathOnly && form == formBasename {
			continue
		}

		// Markdown links keep the ".md" extension (the file-path convention),
		// unlike wikilinks which drop it. A bare basename match stays a bare
		// basename; a path match becomes the new path. We never invent a folder
		// prefix a bare authored link didn't have.
		replacement := mdNewPath
		if form == formBasename {
			replacement = mdNewBase
		}
		// No-op when the destination is unchanged modulo percent-encoding: a
		// rewrite whose only effect would be respelling the same destination
		// (raw spaces to %20 or back) must not count as a change, or a
		// folder-only move would churn referencing notes, and repair-links
		// would "repair" a link to its own spelling, burning the note's single
		// polish --undo snapshot without fixing anything.
		if replacement == pathPart || DecodeLinkTarget(replacement) == DecodeLinkTarget(pathPart) {
			continue
		}
		// Edit only the path part inside the parentheses (m[4] through
		// m[4]+len(pathPart)); the #anchor/?query suffix and the closing ")"
		// stay untouched, as does the [label] text before the "(".
		edits = append(edits, edit{start: m[4], end: m[4] + len(pathPart), newText: replacement})
	}

	if len(edits) == 0 {
		return body, 0
	}

	// Apply edits left-to-right, dropping any whose span overlaps an already
	// applied one (defensive: the two regexes can in theory both claim adjacent
	// or nested bytes).
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })

	var out strings.Builder
	out.Grow(len(body))
	count := 0
	last := 0
	for _, e := range edits {
		if e.start < last {
			continue // overlaps a prior edit, skip
		}
		out.WriteString(body[last:e.start])
		out.WriteString(e.newText)
		last = e.end
		count++
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
// Percent-decoding is deliberately NOT part of this normalization: markdown
// comparison sites retry through DecodeLinkTarget (linkencode.go) on a raw
// miss, so wikilink matching stays byte-exact.
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
