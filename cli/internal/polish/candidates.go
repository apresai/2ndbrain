package polish

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
)

// DefaultMaxCandidates caps how many link targets are offered to the model. The
// list goes into the prompt, so it must stay bounded regardless of vault size.
const DefaultMaxCandidates = 20

// queryWindowRunes mirrors ask.go / suggest-links: only the first N runes of the
// body seed the semantic query.
const queryWindowRunes = 2000

// minSubstringLen avoids over-matching very short titles ("AI", "Go") as whole
// words in unrelated prose.
const minSubstringLen = 3

// CandidateInput carries everything GatherCandidates needs without re-querying
// the embedding store (the MCP server keeps embeddings cached; the CLI loads
// them once). The caller computes semantic readiness and, when degraded, passes
// a Warning line through verbatim.
type CandidateInput struct {
	Source     *document.Document // parsed source note (Body used for query + scan)
	SourcePath string             // vault-relative path (to exclude self)
	DocIDs     []string           // preloaded embedding doc IDs (may be empty)
	Embeddings [][]float32        // preloaded embedding vectors (may be empty)
	Threshold  float64            // similarity threshold for semantic neighbors
	Max        int                // hard cap (<=0 → DefaultMaxCandidates)
	Warning    string             // loud-degradation line from the caller; passed through
}

// GatherCandidates returns a bounded, vault-grounded set of link targets for the
// source note: semantic neighbors (when embeddings are usable) merged with
// titles/aliases that literally appear in the prose. Every returned candidate
// resolves unambiguously to its own path, so linking [[Title]] can never
// mis-resolve. The second return is a loud-degradation message (passed through
// from the caller) suitable for stderr; it is never an error to lack embeddings.
func GatherCandidates(ctx context.Context, v *vault.Vault, embedder ai.EmbeddingProvider, in CandidateInput) ([]LinkCandidate, string, error) {
	max := in.Max
	if max <= 0 {
		max = DefaultMaxCandidates
	}

	var sourceID string
	if d, err := v.DB.GetDocumentByPath(in.SourcePath); err == nil && d != nil {
		sourceID = d.ID
	}
	linkedPaths := linkedTargetPaths(v, sourceID)

	byPath := make(map[string]LinkCandidate) // dedupe by path; semantic wins

	// 1. Semantic neighbors, only when embeddings are loaded and usable.
	if len(in.Embeddings) > 0 && len(in.Embeddings) == len(in.DocIDs) && embedder != nil && embedder.Available(ctx) {
		runes := []rune(in.Source.Body)
		if len(runes) > queryWindowRunes {
			runes = runes[:queryWindowRunes]
		}
		if vecs, err := embedder.Embed(ctx, []string{string(runes)}); err == nil && len(vecs) > 0 {
			scored := search.VectorSearchThreshold(vecs[0], in.DocIDs, in.Embeddings, max*3, in.Threshold)
			engine := search.NewEngine(v.DB.Conn())
			for _, s := range scored {
				lookup, ok := engine.GetDocumentByID(s.DocID)
				if !ok || lookup.Title == "" || lookup.Path == in.SourcePath || linkedPaths[lookup.Path] {
					continue
				}
				if _, exists := byPath[lookup.Path]; !exists {
					byPath[lookup.Path] = LinkCandidate{Title: lookup.Title, Path: lookup.Path, Score: s.Score, Source: "semantic"}
				}
			}
		}
	}

	// 2. Substring matches, titles/aliases that appear verbatim in the prose.
	addSubstringMatches(v, in, linkedPaths, byPath)

	out := finalizeCandidates(v, byPath, max)
	return out, in.Warning, nil
}

// linkedTargetPaths returns the vault-relative paths the source note already
// links to, so they are not re-suggested.
func linkedTargetPaths(v *vault.Vault, sourceID string) map[string]bool {
	out := make(map[string]bool)
	if sourceID == "" {
		return out
	}
	rows, err := v.DB.Conn().Query(
		`SELECT d.path FROM links l JOIN documents d ON d.id = l.target_id
		 WHERE l.source_id = ? AND l.target_id IS NOT NULL AND l.target_id != ''`,
		sourceID,
	)
	if err != nil {
		slog.Warn("polish: could not load existing links for exclusion", "err", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out[p] = true
		}
	}
	return out
}

// addSubstringMatches scans the (code-masked) source body for whole-word
// occurrences of every vault note's title and alias, adding a candidate per
// matched path. Code is masked so a title mentioned only inside a code sample is
// not matched. Semantic candidates already in byPath are not overwritten.
func addSubstringMatches(v *vault.Vault, in CandidateInput, linkedPaths map[string]bool, byPath map[string]LinkCandidate) {
	titles, err := v.DB.AllDocTitles()
	if err != nil {
		slog.Warn("polish: could not load doc titles for substring link matching", "err", err)
		return
	}
	titleByPath := make(map[string]string, len(titles))
	for _, t := range titles {
		titleByPath[t.Path] = t.Title
	}
	aliases, _ := v.DB.AllAliases()

	// Match titles against prose only: fenced blocks and inline code are removed
	// so a note title mentioned inside a code sample is never linked.
	haystack := strings.ToLower(proseOnly(in.Source.Body))

	consider := func(surface, path, title string) {
		if path == in.SourcePath || linkedPaths[path] || title == "" {
			return
		}
		if _, exists := byPath[path]; exists {
			return
		}
		if len(surface) < minSubstringLen {
			return
		}
		if containsWholeWord(haystack, strings.ToLower(surface)) {
			byPath[path] = LinkCandidate{Title: title, Path: path, Source: "substring"}
		}
	}

	for _, t := range titles {
		consider(t.Title, t.Path, t.Title)
	}
	for _, a := range aliases {
		consider(a.Alias, a.Path, titleByPath[a.Path])
	}
}

// finalizeCandidates attaches aliases, drops titles that do not resolve
// unambiguously back to their own path, sorts (semantic-by-score first, then
// substring alphabetically), and caps the list.
func finalizeCandidates(v *vault.Vault, byPath map[string]LinkCandidate, max int) []LinkCandidate {
	aliasByPath := make(map[string][]string)
	if aliases, err := v.DB.AllAliases(); err == nil {
		for _, a := range aliases {
			aliasByPath[a.Path] = append(aliasByPath[a.Path], a.Alias)
		}
	}

	out := make([]LinkCandidate, 0, len(byPath))
	for path, c := range byPath {
		// Ambiguity guard: only keep a candidate whose title resolves to exactly
		// this path, so [[Title]] in the polished note cannot mis-resolve.
		if resolved, err := v.DB.ResolveTarget(c.Title); err != nil || resolved != path {
			continue
		}
		c.Aliases = aliasByPath[path]
		out = append(out, c)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Source != b.Source {
			return a.Source == "semantic" // semantic before substring
		}
		if a.Source == "semantic" && a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Title < b.Title
	})

	if len(out) > max {
		out = out[:max]
	}
	return out
}

// proseOnly returns body with fenced code blocks and inline code spans removed
// (replaced by spaces/blank lines), so title matching never fires on a note name
// that only appears inside a code sample.
func proseOnly(body string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if isFenceLine(strings.TrimRight(line, " \t")) {
			inFence = !inFence
			b.WriteByte('\n')
			continue
		}
		if inFence {
			b.WriteByte('\n')
			continue
		}
		b.WriteString(inlineCodeRe.ReplaceAllString(line, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// containsWholeWord reports whether needle occurs in haystack bounded by
// non-alphanumeric characters (or string ends). Both args must be lowercased.
func containsWholeWord(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	from := 0
	for {
		i := strings.Index(haystack[from:], needle)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(needle)
		if !isWordByte(boundaryByte(haystack, start-1)) && !isWordByte(boundaryByte(haystack, end)) {
			return true
		}
		from = start + 1
		if from >= len(haystack) {
			return false
		}
	}
}

func boundaryByte(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return ' ' // treat string ends as boundaries
	}
	return s[i]
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}
