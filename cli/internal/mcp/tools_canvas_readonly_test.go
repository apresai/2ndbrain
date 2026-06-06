package mcp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestHandleKBUpdateMeta_RefusesCanvasAndBase proves the MCP path enforces the
// same read-only guarantee as the CLI: kb_update_meta on a .canvas/.base file
// returns an error and never rewrites the file.
func TestHandleKBUpdateMeta_RefusesCanvasAndBase(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		content string
	}{
		{"board.canvas", `{"nodes":[{"id":"n1","type":"text","text":"hi"}],"edges":[]}`},
		{"cfg.base", "root:\n  key: value\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(v.Root, tc.name)
			if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(p)

			res, err := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{
				"path":   tc.name,
				"fields": map[string]any{"status": "changed"},
			}))
			if err != nil {
				t.Fatalf("handler returned a transport error: %v", err)
			}
			if !res.IsError {
				t.Errorf("expected an error result for %s, got: %s", tc.name, resultText(t, res))
			}

			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Errorf("%s modified despite refusal:\nbefore=%q\nafter=%q", tc.name, before, after)
			}
		})
	}
}
