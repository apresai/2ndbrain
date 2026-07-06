package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnippetDocMatchesEmbedded enforces the claim in docs/claude-md-snippet.md
// that its fenced ```markdown block is "kept in sync" with the embedded
// content/instructions.md — the block this package installs. Without this guard
// a manual-paste copy would silently drift from what `2nb instructions install`
// writes.
func TestSnippetDocMatchesEmbedded(t *testing.T) {
	docPath := filepath.Join("..", "..", "..", "docs", "claude-md-snippet.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		// A different checkout layout (e.g. the package vendored elsewhere) is not
		// a drift failure; only enforce when the doc is actually present.
		t.Skipf("snippet doc not found at %s (%v); skipping drift check", docPath, err)
	}
	fenced := extractFencedMarkdown(string(data))
	if fenced == "" {
		t.Fatalf("no ```markdown fenced block found in %s", docPath)
	}
	if strings.TrimSpace(fenced) != strings.TrimSpace(blockBody) {
		t.Errorf("docs/claude-md-snippet.md fenced block drifted from the embedded content/instructions.md.\nUpdate the doc to match the embedded source.\n--- doc fenced block ---\n%s\n--- embedded ---\n%s", fenced, blockBody)
	}
}

// extractFencedMarkdown returns the contents of the first ```markdown ... ```
// fenced block in s, or "" if none is found.
func extractFencedMarkdown(s string) string {
	const open = "```markdown\n"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, "\n```")
	if j < 0 {
		return ""
	}
	return rest[:j]
}
