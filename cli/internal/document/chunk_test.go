package document

import (
	"testing"
)

func TestChunkDocument_BasicHeadings(t *testing.T) {
	doc := &Document{
		ID:   "test-id",
		Body: "# Title\n\nIntro text.\n\n## Section A\n\nContent A.\n\n## Section B\n\nContent B.\n",
	}
	chunks := ChunkDocument(doc)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if chunks[0].Level != 1 {
		t.Errorf("first chunk level = %d, want 1", chunks[0].Level)
	}
	if chunks[1].HeadingPath != "# Title > ## Section A" {
		t.Errorf("second chunk heading = %q", chunks[1].HeadingPath)
	}
}

func TestChunkDocument_Preamble(t *testing.T) {
	doc := &Document{
		ID:   "test-id",
		Body: "Some preamble text.\n\n# Heading\n\nBody.\n",
	}
	chunks := ChunkDocument(doc)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0].HeadingPath != "(preamble)" {
		t.Errorf("preamble heading = %q, want (preamble)", chunks[0].HeadingPath)
	}
}

func TestChunkDocument_EmptyBody(t *testing.T) {
	doc := &Document{ID: "test-id", Body: ""}
	chunks := ChunkDocument(doc)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty body, got %d", len(chunks))
	}
}

func TestChunkDocument_DeterministicHash(t *testing.T) {
	doc := &Document{ID: "test-id", Body: "# Title\n\nContent.\n"}
	chunks1 := ChunkDocument(doc)
	chunks2 := ChunkDocument(doc)
	if chunks1[0].ContentHash != chunks2[0].ContentHash {
		t.Error("content hash should be deterministic")
	}
	if chunks1[0].ID != chunks2[0].ID {
		t.Error("chunk ID should be deterministic")
	}
}

func TestChunkDocument_SortOrder(t *testing.T) {
	doc := &Document{
		ID:   "test-id",
		Body: "# A\n\nText.\n\n## B\n\nText.\n\n## C\n\nText.\n",
	}
	chunks := ChunkDocument(doc)
	for i, c := range chunks {
		if c.SortOrder != i {
			t.Errorf("chunk %d SortOrder = %d, want %d", i, c.SortOrder, i)
		}
	}
}
