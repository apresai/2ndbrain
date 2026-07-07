package polish

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

// LinkRepair records one broken [[wikilink]] the repair pass acted on or left
// alone, for the audit surface (CLI JSON + the Obsidian plugin's diff modal).
type LinkRepair struct {
	Raw       string `json:"raw"`                  // the broken target as authored (no #anchor/|alias)
	NewTarget string `json:"new_target,omitempty"` // the resolvable target it was rewritten to
	Reason    string `json:"reason,omitempty"`     // why skipped: "no_match" or "ambiguous"
}

// RepairResult is the outcome of RepairBrokenLinks over one document body.
type RepairResult struct {
	Body     string       // the body with repairs applied (== input when nothing was repaired)
	Repaired []LinkRepair // links rewritten to an existing note
	Skipped  []LinkRepair // broken links left untouched (no confident target)
}

// RepairBrokenLinks deterministically repairs broken [[wikilinks]] in a
// markdown body, returning the rewritten body plus what it repaired/skipped. It
// NEVER guesses: a broken target is rewritten only when it is a BARE name (no
// path separator) whose normalized (lower-cased, whitespace-collapsed) form maps
// to exactly ONE existing note via that note's basename, title, or alias, and
// that note has an unambiguous bare resolvable name to point at. Every other
// broken link (a path-qualified target, no match, or a name that matches more
// than one note) is left exactly as written and reported in Skipped, so the pass
// can only ever turn a broken link into a working one, never silently retarget it
// to the wrong note.
//
// This is the half of "polish fixes links" that complements --links: --links
// ADDS grounded links to related notes; this REPAIRS the ones already in the
// note that no longer resolve. It is especially useful because 2nb's wikilink
// resolver is case-sensitive while Obsidian's is not, so a case-drifted link
// that works in Obsidian shows as broken here and is canonicalized to the form
// 2nb resolves.
//
// Asset embeds (![[image.png]] and any target with a non-.md extension) and
// links inside code are never touched. #heading / #^block anchors and |alias
// suffixes on a repaired link are preserved verbatim by document.RewriteWikiLinks.
// It takes the body directly (not a *Document) so callers can repair an
// in-memory, post-copy-edit body that isn't on disk yet.
func RepairBrokenLinks(v *vault.Vault, body string) (RepairResult, error) {
	return RepairBrokenLinksFiltered(v, body, nil)
}

// RepairBrokenLinksFiltered is RepairBrokenLinks scoped to a set of authored
// targets: only broken links whose authored target (after TrimSpace) appears in
// `only` are considered; every other link is left untouched and not reported.
// `only == nil` (or empty) repairs every confidently-repairable broken link,
// reproducing RepairBrokenLinks exactly. This lets a per-finding GUI "Repair
// link" button fix exactly the clicked broken target (the TARGET from lint's
// `broken wikilink: [[TARGET]]`) without sweeping the whole document.
func RepairBrokenLinksFiltered(v *vault.Vault, body string, only []string) (RepairResult, error) {
	res := RepairResult{Body: body}

	// Resolve against the LIVE FILESYSTEM (the same walk lint reports from),
	// not the index DB, so lint and repair can never disagree on a stale DB:
	// a note created in Obsidian but not yet indexed is repairable immediately
	// (the DB-backed index used to report it no_match), and a note deleted on
	// disk but still in the DB is never offered as a repair target (the
	// DB-backed index used to "fix" a link to that ghost).
	docs, aliases, err := vault.CollectLiveDocs(v.Root)
	if err != nil {
		return res, err
	}
	idx := buildRepairIndex(docs, aliases)
	resolver := store.NewResolver(docs, aliases)

	var filter map[string]bool
	if len(only) > 0 {
		filter = make(map[string]bool, len(only))
		for _, t := range only {
			filter[strings.TrimSpace(t)] = true
		}
	}

	handled := make(map[string]bool) // dedupe by authored target so one distinct link is rewritten once
	for _, link := range document.ExtractWikiLinks(body) {
		target := strings.TrimSpace(link.Target)
		if target == "" || handled[target] {
			continue
		}
		if filter != nil && !filter[target] {
			continue // not one of the targets the caller asked to repair
		}
		if isLikelyAsset(target) {
			continue // ![[image.png]] and friends are not note links
		}

		// Only attempt repair on a genuinely unresolvable target. A target that
		// resolves (possibly to one note) is fine; an already-ambiguous one is
		// the author's to disambiguate, not ours to guess.
		if _, rerr := resolver.Resolve(target); rerr == nil || !errors.Is(rerr, store.ErrTargetNotFound) {
			continue
		}
		handled[target] = true

		candidates := idx.lookup(target)
		switch len(candidates) {
		case 1:
			newTarget := candidates[0]
			rewritten, n := document.RewriteWikiLinks(body, target, newTarget)
			if n > 0 {
				body = rewritten
				res.Repaired = append(res.Repaired, LinkRepair{Raw: target, NewTarget: newTarget})
			}
		case 0:
			res.Skipped = append(res.Skipped, LinkRepair{Raw: target, Reason: "no_match"})
		default:
			res.Skipped = append(res.Skipped, LinkRepair{Raw: target, Reason: "ambiguous"})
		}
	}

	res.Body = body
	return res, nil
}

// repairIndex maps a normalized name (note basename, title, or alias) to the set
// of resolvable bare target strings for the notes that carry it. A normalized
// name with exactly one distinct target is safe to repair to.
type repairIndex struct {
	byNorm map[string]map[string]struct{}
}

// lookup returns the sorted distinct resolvable targets a broken BARE name maps
// to. A path-qualified authored target (containing "/") returns nothing: it must
// be fixed by hand, because matching it by its leaf could retarget the link to an
// unrelated note that merely shares the basename, and Obsidian does not resolve
// path-qualified links by leaf either. For a bare name this is just the
// normalized-name lookup into the basename/title/alias index.
func (r *repairIndex) lookup(authored string) []string {
	authored = strings.TrimSuffix(strings.ReplaceAll(authored, "\\", "/"), ".md")
	if strings.Contains(authored, "/") {
		return nil
	}
	set := r.byNorm[normalizeName(authored)]
	targets := make([]string, 0, len(set))
	for t := range set {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	return targets
}

// buildRepairIndex builds the normalized-name -> resolvable-target index from
// the live filesystem's documents and aliases (vault.CollectLiveDocs, the same
// walk lint resolves against, so repair sees exactly the notes lint sees). The
// resolvable target chosen per note is the prettiest form that is UNAMBIGUOUS
// on its own: a unique title, else a unique basename. A note whose title and
// basename are both shared is omitted (a bare [[name]] could not be rewritten
// to it without staying ambiguous), so repairs never produce a still-broken
// link.
func buildRepairIndex(docs []store.DocInfo, aliases map[string][]string) *repairIndex {
	titleCount := make(map[string]int)
	baseCount := make(map[string]int)
	titleByPath := make(map[string]string, len(docs))
	for _, d := range docs {
		titleByPath[d.Path] = d.Title
		if d.Title != "" {
			titleCount[normalizeName(d.Title)]++
		}
		baseCount[normalizeName(basenameNoExt(d.Path))]++
	}

	// canonicalFor picks the unambiguous bare target for a note, or "" when none
	// exists (both title and basename are shared by another note).
	canonicalFor := func(path, title string) string {
		if title != "" && titleCount[normalizeName(title)] == 1 {
			return title
		}
		base := basenameNoExt(path)
		if baseCount[normalizeName(base)] == 1 {
			return base
		}
		return ""
	}

	idx := &repairIndex{byNorm: make(map[string]map[string]struct{})}
	add := func(norm, target string) {
		if norm == "" || target == "" {
			return
		}
		set := idx.byNorm[norm]
		if set == nil {
			set = make(map[string]struct{})
			idx.byNorm[norm] = set
		}
		set[target] = struct{}{}
	}

	for _, d := range docs {
		canonical := canonicalFor(d.Path, d.Title)
		if canonical == "" {
			continue
		}
		add(normalizeName(basenameNoExt(d.Path)), canonical)
		if d.Title != "" {
			add(normalizeName(d.Title), canonical)
		}
	}
	for alias, paths := range aliases {
		for _, p := range paths {
			add(normalizeName(alias), canonicalFor(p, titleByPath[p]))
		}
	}
	return idx
}

// SuggestRepairTargets returns the distinct existing-note targets a broken bare
// name fuzzily maps to via the SAME normalized-name index repair uses (case,
// hyphen/underscore, and whitespace folded). It takes the live-filesystem doc
// set + alias index from vault.CollectLiveDocs, so callers suggest from the
// same universe lint reports on (and can reuse the one walk for a resolver).
// Unlike RepairBrokenLinks it does NOT require a unique match: it returns 0, 1,
// or many candidates, so a "did you mean?" picker can surface the ambiguous
// matches repair itself refuses to guess. Each returned value is a canonical
// bare target (a unique title or basename) that the caller resolves to a path
// with store.Resolver.Resolve.
func SuggestRepairTargets(docs []store.DocInfo, aliases map[string][]string, target string) []string {
	return buildRepairIndex(docs, aliases).lookup(target)
}

// normalizeName lower-cases, folds '-'/'_' to spaces, and collapses internal
// whitespace, so a link whose only drift from an existing note is case, spacing,
// or hyphen-vs-space matches it. 2nb's resolver is case- and separator-sensitive
// (it treats "claude-code" and "claude code" as distinct); Obsidian's is not, so
// case and separator drift are the common breakage. The same fold runs on both
// the index side (basenames/titles/aliases in buildRepairIndex) and the lookup
// side (the authored target), so a kebab basename
// "claude-code-skills-reference-and-index" and a spaced target
// "Claude Code Skills Reference and Index" both become
// "claude code skills reference and index". Folding can only MERGE a drifted
// target onto its single note, or — if it newly collides two distinct notes —
// fall to the ambiguity guard (lookup returns >1, repair skips), so it never
// silently retargets.
func normalizeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return ' '
		}
		return r
	}, s)
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// basenameNoExt returns the final path segment with a trailing .md stripped.
func basenameNoExt(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	return strings.TrimSuffix(filepath.Base(s), ".md")
}

// isLikelyAsset reports whether a wikilink target names a non-markdown file (an
// image, pdf, etc.) rather than a note, so the repair pass leaves it alone.
func isLikelyAsset(target string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(target)))
	return ext != "" && ext != ".md"
}
