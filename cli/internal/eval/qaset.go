package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

// QAItem is one ground-truth question paired with the note it was generated from.
// A retrieval config is "good" when it ranks SourceID high for Question, and an
// answer is "good" when the jury says it correctly answers Question using the
// source note. The source body is cached so the jury can grade faithfulness
// without re-reading the vault.
type QAItem struct {
	Question    string `json:"question"`
	SourceID    string `json:"source_id"`
	SourcePath  string `json:"source_path"`
	SourceTitle string `json:"source_title"`
	SourceBody  string `json:"source_body"`
}

// LoadOrGenerateQASet returns the cached QA set at path, or generates a fresh one
// (and caches it) when the cache is missing or has fewer than n items. The cache
// lives inside the vault's gitignored .2ndbrain sidecar so vault content is never
// committed to the 2ndbrain repo.
func LoadOrGenerateQASet(ctx context.Context, v *vault.Vault, gen ai.GenerationProvider, n int, seed int64, path string) ([]QAItem, error) {
	if data, err := os.ReadFile(path); err == nil {
		var items []QAItem
		if json.Unmarshal(data, &items) == nil && len(items) >= n {
			return items[:n], nil
		}
	}
	items, err := GenerateQASet(ctx, v, gen, n, seed)
	if err != nil {
		return nil, err
	}
	if data, err := json.MarshalIndent(items, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
	return items, nil
}

// GenerateQASet samples up to n substantial notes (deterministically by seed) and
// asks the generator to write one specific natural-language question each note
// answers. Short/template/index notes are skipped so the question has real
// content to bind to.
func GenerateQASet(ctx context.Context, v *vault.Vault, gen ai.GenerationProvider, n int, seed int64) ([]QAItem, error) {
	candidates, err := candidateDocs(v.DB, v.Root)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no substantial notes to build a QA set from")
	}
	// Deterministic spread: sort by id, then stride so the sample isn't clustered.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].SourceID < candidates[j].SourceID })
	picked := stride(candidates, n, seed)

	items := make([]QAItem, 0, len(picked))
	for _, c := range picked {
		q, err := generateQuestion(ctx, gen, c)
		if err != nil {
			continue // skip a note the model choked on; keep the set best-effort
		}
		q = cleanQuestion(q)
		if q == "" {
			continue
		}
		c.Question = q
		items = append(items, c)
	}
	if len(items) < 2 {
		return nil, fmt.Errorf("generated only %d usable QA items", len(items))
	}
	return items, nil
}

func generateQuestion(ctx context.Context, gen ai.GenerationProvider, c QAItem) (string, error) {
	body := c.SourceBody
	if len(body) > 4000 {
		body = body[:4000]
	}
	prompt := fmt.Sprintf(`Below is a note from a personal knowledge base.

TITLE: %s

CONTENT:
%s

Write ONE natural-language question that THIS note answers, phrased the way a user
who half-remembers the idea would ask it — CONCEPTUALLY, in their own words. Rules:
- Describe what you want by MEANING, not by keyword. Do NOT reuse the note's
  distinctive terms, product names, acronyms, or exact phrases, and do NOT quote
  the title. Paraphrase the concept instead (this tests semantic search, so a
  keyword match must not give it away).
- It must still be genuinely answerable from THIS note's content, and specific
  enough that this note is the right source (not a generic question).
- Phrase it as a real user question, not "According to the note...".

Output ONLY the question, nothing else.`, c.SourceTitle, body)

	return gen.Generate(ctx, prompt, ai.GenOpts{MaxTokens: 120, Temperature: ai.Ptr(0.3)})
}

// candidateDocs returns notes with enough body text to ground a question.
func candidateDocs(db *store.DB, root string) ([]QAItem, error) {
	rows, err := db.Conn().Query(`SELECT id, path, title FROM documents WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("load docs: %w", err)
	}
	defer rows.Close()

	var out []QAItem
	for rows.Next() {
		var id, path, title string
		if err := rows.Scan(&id, &path, &title); err != nil {
			return nil, err
		}
		if strings.TrimSpace(title) == "" {
			continue
		}
		doc, err := document.ParseFile(filepath.Join(root, path))
		if err != nil {
			continue
		}
		body := strings.TrimSpace(doc.IndexableBody())
		if len([]rune(body)) < 500 { // too short to ground a specific question
			continue
		}
		out = append(out, QAItem{SourceID: id, SourcePath: path, SourceTitle: title, SourceBody: body})
	}
	return out, rows.Err()
}

// stride picks up to n items evenly spread across the sorted slice, offset by seed
// so different seeds sample different notes without randomness (reproducible).
func stride(items []QAItem, n int, seed int64) []QAItem {
	if n >= len(items) {
		return items
	}
	step := len(items) / n
	if step < 1 {
		step = 1
	}
	offset := int(seed) % step
	if offset < 0 {
		offset += step
	}
	out := make([]QAItem, 0, n)
	for i := offset; i < len(items) && len(out) < n; i += step {
		out = append(out, items[i])
	}
	return out
}

// cleanQuestion trims model preamble/quotes and keeps a single question line.
func cleanQuestion(s string) string {
	s = strings.TrimSpace(s)
	// Some models prepend "Question:" or wrap in quotes; take the last non-empty line.
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		l = strings.TrimPrefix(l, "Question:")
		l = strings.TrimSpace(strings.Trim(l, `"'`))
		if l != "" {
			return l
		}
	}
	return ""
}
