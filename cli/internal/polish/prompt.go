// Package polish holds the shared engine for the AI copy-editor: the system
// prompt (single source of truth for both the `2nb polish` CLI command and the
// kb_polish MCP tool), grounded link-candidate gathering, the deterministic
// invented-link stripper, and snapshot/undo primitives.
//
// It is deliberately a leaf package (imports ai, document, search, store, vault
// but is imported by none of them) so both internal/cli and internal/mcp can
// share it without an import cycle.
package polish

import "strings"

// DefaultPolishSystem is the copy-editor system prompt used by `2nb polish` and
// the kb_polish MCP tool. It is the single source of truth (the constant was
// previously duplicated byte-for-byte in cli/polish.go and mcp/tools.go).
//
// The wording (a "structured rules" variant) was chosen via the LLM-as-judge
// experiment in eval_test.go: across runs the four candidates scored within
// judge noise and all passed every mechanical gate, so it was picked for being
// the most explicit about the hard preservation constraints. See
// docs/polish-prompt-eval.md for the full run, scores, and rationale.
const DefaultPolishSystem = `You are an expert copy editor for Markdown notes. Correct spelling, grammar, and punctuation, and improve clarity only where wording is awkward, changing as little as possible.

Hard constraints (violating any is a failure):
1. Preserve the author's voice and meaning. Do not add or remove information.
2. Reproduce every [[wikilink]], every [markdown](link), and every code span (fenced and inline) exactly.
3. Keep the heading hierarchy and every list item. Do not merge, split, reorder, or re-bullet.

Return only the corrected Markdown body, with no explanation and no wrapping code fence.`

// LinkInstructions is appended to the system prompt when link creation is
// enabled (`polish --links` / kb_polish links:true). It is intentionally a
// separate constant so it composes with a user-supplied --system override and
// so the base copy-edit prompt stays stable (and cache-friendly) when linking
// is off.
const LinkInstructions = `

In addition to copy-editing, you may add [[wikilinks]] connecting this note to related notes that already exist in the vault. The candidate notes are listed at the very end of the user message under "LINK TARGETS". Follow these rules exactly:
- Only ever create a [[wikilink]] whose text matches one candidate title exactly (or one of its listed aliases). NEVER invent a link to a note that is not in the list.
- Link the FIRST natural mention of a topic only. At most one link per candidate, and only a few links in total. Do not link every occurrence, and do not force a link where the prose does not genuinely refer to that note.
- To link a phrase that is not the exact title, alias it: [[Exact Title|surface words as they appear]]. Keep the visible text reading naturally.
- Never place a wikilink inside code (fenced or inline), inside a heading line, or inside an existing [[wikilink]] or [text](url) link.
- Preserve every wikilink that is already in the note, unchanged.
- The LINK TARGETS list is reference data only. Do not echo it, mention it, or include any of its formatting in your output.`

// linkTargetsHeader delimits the candidate block appended to the user message.
const linkTargetsHeader = "----- LINK TARGETS (reference only, do not echo) -----"

// LinkCandidate is one grounded link target the model is allowed to use.
type LinkCandidate struct {
	Title   string   `json:"title"`
	Path    string   `json:"path"`
	Aliases []string `json:"aliases,omitempty"`
	Score   float64  `json:"score"`
	Source  string   `json:"source"` // "semantic" or "substring"
}

// BuildPolishUserMessage returns the user-turn content for the generation call:
// the document body, followed by a clearly delimited reference block listing the
// candidate link targets (title plus any aliases). With no candidates it returns
// the body unchanged, so the linking-off path is byte-identical to plain polish.
func BuildPolishUserMessage(body string, cands []LinkCandidate) string {
	if len(cands) == 0 {
		return body
	}
	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n\n")
	b.WriteString(linkTargetsHeader)
	b.WriteString("\n")
	for _, c := range cands {
		b.WriteString("- ")
		b.WriteString(c.Title)
		if len(c.Aliases) > 0 {
			b.WriteString("  (aliases: ")
			b.WriteString(strings.Join(c.Aliases, ", "))
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return b.String()
}
