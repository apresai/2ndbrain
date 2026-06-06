package document

import "testing"

func TestChunkDocument_BlockID_LastWins(t *testing.T) {
	doc := &Document{ID: "d1", Body: "Some content here. ^block-1\n\nMore content. ^block-2\n"}
	chunks := ChunkDocument(doc)
	if len(chunks) == 0 {
		t.Fatal("no chunks produced")
	}
	// No headings, so it's a single chunk holding both markers; last wins.
	if chunks[0].BlockID != "block-2" {
		t.Errorf("expected last block id block-2, got %q", chunks[0].BlockID)
	}
}

func TestChunkDocument_NoBlockID(t *testing.T) {
	doc := &Document{ID: "d1", Body: "plain content with a lone caret ^ in the middle\n"}
	chunks := ChunkDocument(doc)
	for _, c := range chunks {
		if c.BlockID != "" {
			t.Errorf("unexpected block id %q for content %q", c.BlockID, c.Content)
		}
	}
}

func TestChunkDocument_BlockIDPerHeadingChunk(t *testing.T) {
	doc := &Document{ID: "d1", Body: "# A\nalpha ^a1\n# B\nbeta ^b1\n"}
	chunks := ChunkDocument(doc)
	got := map[string]string{}
	for _, c := range chunks {
		got[c.HeadingPath] = c.BlockID
	}
	if got["# A"] != "a1" {
		t.Errorf("chunk A block id = %q, want a1", got["# A"])
	}
	if got["# B"] != "b1" {
		t.Errorf("chunk B block id = %q, want b1", got["# B"])
	}
}
