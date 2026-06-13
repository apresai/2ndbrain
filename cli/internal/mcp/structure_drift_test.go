package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestKBStructure_MatchesBuildOutline is the drift guard: the MCP kb_structure
// handler and the CLI `outline` command both serialize document.BuildOutline,
// so the heading set kb_structure returns must equal what BuildOutline produces
// for the same document. If someone reintroduces an inline tree-builder in
// either place, this fails.
func TestKBStructure_MatchesBuildOutline(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	body := "# My Doc\n\nPreamble text.\n\n## Context\n\nSome context.\n\n### Detail\n\nNested.\n\n## Decision\n\nThe decision.\n"
	doc := testutil.CreateAndIndex(t, v, "My Doc", "note", body)

	// What the shared helper produces directly. ParseFile + Path normalization
	// mirrors what the handler does before calling BuildOutline.
	parsed, err := document.ParseFile(v.AbsPath(doc.Path))
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}
	parsed.Path = doc.Path
	wantNodes := document.BuildOutline(parsed)

	res, err := h.handleKBStructure(ctx, makeRequest(map[string]any{"path": doc.Path}))
	if err != nil {
		t.Fatalf("handleKBStructure: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("kb_structure returned an error: %s", text)
	}

	var result struct {
		Sections []document.OutlineNode `json:"sections"`
	}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("kb_structure output is not valid JSON: %v\n%s", err, text)
	}

	if len(result.Sections) != len(wantNodes) {
		t.Fatalf("kb_structure returned %d sections, BuildOutline produced %d", len(result.Sections), len(wantNodes))
	}
	for i := range wantNodes {
		got := result.Sections[i]
		want := wantNodes[i]
		if got.HeadingPath != want.HeadingPath || got.Level != want.Level ||
			got.StartLine != want.StartLine || got.EndLine != want.EndLine || got.ID != want.ID {
			t.Errorf("section %d drift:\n  kb_structure = %+v\n  BuildOutline = %+v", i, got, want)
		}
	}
}
