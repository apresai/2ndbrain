// Package ragctx assembles parent-document RAG context: it turns ranked search
// results into the note text fed to the generator. Following the small-to-big /
// parent-document retrieval pattern, it feeds each unique source note WHOLE when
// it fits a token (rune) budget, and windows around the matched heading section
// when it doesn't — so a long note's answer-bearing section is never silently
// head-truncated. Shared by `2nb ask` and the MCP kb_ask tool so the two paths
// can't diverge.
package ragctx

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/search"
)

// Budget bounds how much retrieved context reaches the generator. Runes are the
// codebase's token proxy (tokens ≈ runes/4, matching ai.MaxHistoryChars). A zero
// field falls back to the package default.
type Budget struct {
	TotalRunes int // total across all notes
	NoteRunes  int // per-note cap; a larger note is windowed around the match
	MaxNotes   int // hard cap on the number of notes included
}

// minNoteRunes: once the remaining total budget drops below this, stop — a
// sliver of a note is noise, not useful context. (Budget defaults are the
// single source of truth in internal/ai.)
const minNoteRunes = 800

func (b Budget) total() int {
	if b.TotalRunes > 0 {
		return b.TotalRunes
	}
	return ai.DefaultRAGContextBudgetRunes
}

func (b Budget) note() int {
	if b.NoteRunes > 0 {
		return b.NoteRunes
	}
	return ai.DefaultRAGNoteBudgetRunes
}

func (b Budget) maxNotes() int {
	if b.MaxNotes > 0 {
		return b.MaxNotes
	}
	return ai.DefaultRAGMaxNotes
}

// Build assembles parent-document context from ranked results (best first). It
// dedups by document path, reads each note's FULL body via document.ParseFile
// (uniform across .md/.canvas/.base — so synthetic views are markdown, not raw
// JSON/YAML), includes the whole body when it fits the per-note and
// remaining-total budget, and otherwise windows around the matched section
// (Result.HeadingPath, forward-first). Returns one ai.RAGChunk per note in rank
// order plus non-fatal warnings (unreadable sources).
func Build(results []search.Result, vaultRoot string, b Budget) ([]ai.RAGChunk, []string) {
	var chunks []ai.RAGChunk
	var warnings []string
	seen := make(map[string]bool)
	remaining := b.total()
	maxNotes := b.maxNotes()

	for _, r := range results {
		if len(chunks) >= maxNotes || remaining < minNoteRunes {
			break
		}
		if r.Path == "" || seen[r.Path] {
			continue
		}
		seen[r.Path] = true

		doc, err := document.ParseFile(filepath.Join(vaultRoot, r.Path))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped unreadable source %s: %v", r.Path, err))
			continue
		}
		// IndexableBody strips Obsidian %%comments%% — honors the same
		// "comments never leak" invariant every index/search/embed path holds
		// (the windowed branch already strips via ChunkDocument).
		body := doc.IndexableBody()
		if strings.TrimSpace(body) == "" {
			continue
		}

		noteCap := b.note()
		if remaining < noteCap {
			noteCap = remaining
		}
		content := fitNote(doc, body, r.HeadingPath, noteCap)
		if strings.TrimSpace(content) == "" {
			continue
		}
		chunks = append(chunks, ai.RAGChunk{Title: r.Title, Path: r.Path, Content: content})
		remaining -= len([]rune(content))
	}
	return chunks, warnings
}

// fitNote returns the whole body when it fits noteCap runes, otherwise a window
// of heading-bounded chunks around the matched section (headingPath), expanding
// FORWARD first (answers usually continue after the matched heading — exactly
// the deep-section case) then backward, with "..." markers where text is elided.
func fitNote(doc *document.Document, body, headingPath string, noteCap int) string {
	full := []rune(body)
	if len(full) <= noteCap {
		return body
	}

	chunks := document.ChunkDocument(doc)
	if len(chunks) == 0 {
		// Non-empty body but no chunks should not happen; truncate safely.
		return string(full[:noteCap]) + "\n..."
	}

	center := matchChunkIndex(chunks, headingPath)
	lo, hi := center, center
	used := len([]rune(chunks[center].Content))
	for hi+1 < len(chunks) { // forward first
		n := len([]rune(chunks[hi+1].Content)) + 2 // +2 for the "\n\n" join
		if used+n > noteCap {
			break
		}
		hi++
		used += n
	}
	for lo-1 >= 0 { // then backward with whatever budget remains
		n := len([]rune(chunks[lo-1].Content)) + 2
		if used+n > noteCap {
			break
		}
		lo--
		used += n
	}

	parts := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		parts = append(parts, chunks[i].Content)
	}
	out := strings.Join(parts, "\n\n")
	// A single matched chunk can itself exceed noteCap; clamp rune-safely.
	clamped := false
	if r := []rune(out); len(r) > noteCap {
		out = string(r[:noteCap])
		clamped = true
	}
	if lo > 0 {
		out = "...\n" + out
	}
	if hi < len(chunks)-1 || clamped {
		out += "\n..."
	}
	return out
}

// matchChunkIndex finds the chunk whose heading path equals headingPath
// (case-insensitive, trimmed). Returns 0 (the note head) when headingPath is
// empty or no chunk matches — a graceful fallback, never a drop.
func matchChunkIndex(chunks []document.Chunk, headingPath string) int {
	hp := strings.TrimSpace(headingPath)
	if hp == "" {
		return 0
	}
	for i, c := range chunks {
		if strings.EqualFold(strings.TrimSpace(c.HeadingPath), hp) {
			return i
		}
	}
	return 0
}
