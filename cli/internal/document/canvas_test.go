package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCanvas(t *testing.T) {
	tempDir := t.TempDir()
	canvasPath := filepath.Join(tempDir, "test.canvas")

	canvasJSON := `{
		"nodes": [
			{
				"id": "node-1",
				"type": "text",
				"text": "Core auth strategy",
				"x": -100,
				"y": -50,
				"width": 250,
				"height": 100
			},
			{
				"id": "node-2",
				"type": "file",
				"file": "engineering/auth-model.md",
				"x": 200,
				"y": -50,
				"width": 250,
				"height": 150
			}
		],
		"edges": [
			{
				"id": "edge-1",
				"fromNode": "node-1",
				"toNode": "node-2"
			}
		]
	}`

	if err := os.WriteFile(canvasPath, []byte(canvasJSON), 0o644); err != nil {
		t.Fatalf("write temp canvas file: %v", err)
	}

	doc, err := ParseFile(canvasPath)
	if err != nil {
		t.Fatalf("unexpected error parsing canvas: %v", err)
	}

	if doc.Type != "canvas" {
		t.Errorf("expected doc type 'canvas', got %q", doc.Type)
	}
	if doc.Title != "test" {
		t.Errorf("expected title 'test', got %q", doc.Title)
	}
	if doc.Status != "complete" {
		t.Errorf("expected status 'complete', got %q", doc.Status)
	}

	// Verify text content is in the body
	if !strings.Contains(doc.Body, "Core auth strategy") {
		t.Errorf("expected body to contain text node content, got %q", doc.Body)
	}

	// Verify file reference is in the body as a wikilink
	if !strings.Contains(doc.Body, "[[engineering/auth-model.md]]") {
		t.Errorf("expected body to contain wikilink to file, got %q", doc.Body)
	}

	// Verify edge details are represented in the body
	if !strings.Contains(doc.Body, "From: Card \"Core auth strategy\" (node-1)") {
		t.Errorf("expected edge from description, got %q", doc.Body)
	}
	if !strings.Contains(doc.Body, "To: [[engineering/auth-model.md]]") {
		t.Errorf("expected edge to description, got %q", doc.Body)
	}
}
