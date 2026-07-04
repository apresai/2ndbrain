package document

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestSplitLongChunks_UnderCapUnchanged(t *testing.T) {
	in := []Chunk{{ID: "a", DocID: "d", HeadingPath: "# H", Content: "short content", ContentHash: "x", SortOrder: 0}}
	got := SplitLongChunks(in, maxEmbedChunkChars)
	if len(got) != 1 || got[0].ID != "a" || got[0].Content != "short content" {
		t.Fatalf("under-cap chunk should pass through unchanged, got %+v", got)
	}
}

func TestSplitLongChunks_DisabledWhenMaxNonPositive(t *testing.T) {
	big := strings.Repeat("x", maxEmbedChunkChars*3)
	in := []Chunk{{ID: "a", DocID: "d", HeadingPath: "# H", Content: big}}
	if got := SplitLongChunks(in, 0); len(got) != 1 {
		t.Fatalf("maxChars<=0 must disable splitting, got %d chunks", len(got))
	}
}

func TestSplitLongChunks_OversizedSplitsBoundedDistinctIDs(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "Paragraph %d with several words of filler text to add length.\n\n", i)
	}
	orig := Chunk{ID: "sec", DocID: "doc1", HeadingPath: "# Big", Level: 1, Content: b.String(), SortOrder: 3}
	got := SplitLongChunks([]Chunk{orig}, maxEmbedChunkChars)
	if len(got) < 2 {
		t.Fatalf("expected multiple sub-chunks, got %d", len(got))
	}
	ids := map[string]bool{}
	for i, c := range got {
		if rc := utf8.RuneCountInString(c.Content); rc > maxEmbedChunkChars {
			t.Errorf("sub-chunk %d has %d runes, over cap %d", i, rc, maxEmbedChunkChars)
		}
		if c.HeadingPath != "# Big" || c.DocID != "doc1" || c.Level != 1 || c.SortOrder != 3 {
			t.Errorf("sub-chunk %d lost section metadata: %+v", i, c)
		}
		if ids[c.ID] {
			t.Fatalf("duplicate sub-chunk ID %q — the last-wins dedup would drop parts", c.ID)
		}
		ids[c.ID] = true
	}
}

func TestChunkForStorage_HugeSectionTailPreservedAndBounded(t *testing.T) {
	// One giant heading-less section > 50,000 chars, which Nova would REJECT as a
	// single inline value. Markers at start/mid/end must all survive the split
	// (no silent tail loss), and every produced chunk must be under the cap.
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 4000) // ~108k chars
	body := "AASTART " + filler[:len(filler)/2] + " MMMID " + filler[len(filler)/2:] + " ZZEND"
	doc := &Document{ID: "huge", Body: body}
	chunks := ChunkForStorage(doc)
	if len(chunks) < 2 {
		t.Fatalf("expected the huge section to split, got %d chunks", len(chunks))
	}
	var joined strings.Builder
	for i, c := range chunks {
		if rc := utf8.RuneCountInString(c.Content); rc > maxEmbedChunkChars {
			t.Errorf("chunk %d has %d runes, over the %d cap", i, rc, maxEmbedChunkChars)
		}
		joined.WriteString(c.Content)
		joined.WriteByte(' ')
	}
	for _, marker := range []string{"AASTART", "MMMID", "ZZEND"} {
		if !strings.Contains(joined.String(), marker) {
			t.Errorf("marker %q lost after split — content dropped from the embedding", marker)
		}
	}
}

func TestSplitTextRanges_CoversAllBoundedTerminates(t *testing.T) {
	text := []rune(strings.Repeat("abcde fghij ", 2000)) // 24000 runes, spaced for boundaries
	ranges := splitTextRanges(text, maxEmbedChunkChars, chunkOverlapChars)
	if len(ranges) < 2 {
		t.Fatalf("expected multiple ranges, got %d", len(ranges))
	}
	if ranges[0][0] != 0 {
		t.Errorf("first range must start at 0, got %d", ranges[0][0])
	}
	if last := ranges[len(ranges)-1]; last[1] != len(text) {
		t.Errorf("last range must end at %d (full coverage), got %d", len(text), last[1])
	}
	for i, r := range ranges {
		if r[0] >= r[1] {
			t.Fatalf("range %d empty/inverted: [%d,%d)", i, r[0], r[1])
		}
		if r[1]-r[0] > maxEmbedChunkChars {
			t.Errorf("range %d length %d over cap %d", i, r[1]-r[0], maxEmbedChunkChars)
		}
		if i > 0 && r[0] > ranges[i-1][1] {
			t.Errorf("gap before range %d — content between %d and %d is lost", i, ranges[i-1][1], r[0])
		}
	}
}

func TestSplitTextRanges_OverlapApplied(t *testing.T) {
	text := []rune(strings.Repeat("alpha bravo charlie delta echo ", 800)) // ~24k runes, word boundaries
	ranges := splitTextRanges(text, maxEmbedChunkChars, chunkOverlapChars)
	sawOverlap := false
	for i := 1; i < len(ranges); i++ {
		gap := ranges[i][0] - ranges[i-1][1] // negative = overlap
		if gap > 0 {
			t.Fatalf("range %d leaves a gap of %d runes after range %d", i, gap, i-1)
		}
		if overlap := ranges[i-1][1] - ranges[i][0]; overlap > 0 {
			sawOverlap = true
			if overlap > chunkOverlapChars {
				t.Errorf("overlap %d exceeds chunkOverlapChars %d", overlap, chunkOverlapChars)
			}
		}
	}
	if !sawOverlap {
		t.Error("adjacent sub-chunks never overlapped — a boundary fact could be orphaned")
	}
}

func TestSplitTextRanges_SingleLongTokenNoBoundary(t *testing.T) {
	// One unbroken token (URL / base64 / log line) with no whitespace: findCut
	// finds no boundary and must hard-cut, still covering everything and terminating.
	text := []rune(strings.Repeat("x", maxEmbedChunkChars*3+17))
	ranges := splitTextRanges(text, maxEmbedChunkChars, chunkOverlapChars)
	if len(ranges) < 3 {
		t.Fatalf("expected the long token to split, got %d ranges", len(ranges))
	}
	if ranges[0][0] != 0 || ranges[len(ranges)-1][1] != len(text) {
		t.Fatalf("full coverage broken: [%d..%d), want [0..%d)", ranges[0][0], ranges[len(ranges)-1][1], len(text))
	}
	for i, r := range ranges {
		if r[1]-r[0] > maxEmbedChunkChars || r[0] >= r[1] {
			t.Errorf("range %d invalid/over cap: [%d,%d)", i, r[0], r[1])
		}
	}
}

func TestSplitLongChunks_MultibyteBoundedByRunes(t *testing.T) {
	// Rune-based design: a CJK/emoji body must be bounded by RUNE count, not bytes,
	// and no sub-chunk may exceed the rune cap even though each rune is 3-4 bytes.
	body := strings.Repeat("日本語のテキスト。", 2000) // ~18k runes, ~54k bytes
	got := SplitLongChunks([]Chunk{{ID: "u", DocID: "d", HeadingPath: "# 見出し", Content: body}}, maxEmbedChunkChars)
	if len(got) < 2 {
		t.Fatalf("expected multibyte body to split, got %d", len(got))
	}
	for i, c := range got {
		if rc := utf8.RuneCountInString(c.Content); rc > maxEmbedChunkChars {
			t.Errorf("sub-chunk %d has %d runes, over cap %d", i, rc, maxEmbedChunkChars)
		}
		if !utf8.ValidString(c.Content) {
			t.Errorf("sub-chunk %d is not valid UTF-8 (a rune was split)", i)
		}
	}
}

func TestChunkForStorage_DeterministicIDs(t *testing.T) {
	// The load-bearing invariant: the indexer (chunks table) and embed.Document
	// (vec_chunks) each call ChunkForStorage independently, so it MUST be a pure
	// deterministic function — same doc in, identical chunk ids out — or the vec
	// search's vec_chunks.chunk_id -> chunks.id join breaks.
	doc := &Document{ID: "d1", Body: "# H\n\n" + strings.Repeat("sentence of filler words here. ", 900)}
	a := ChunkForStorage(doc)
	b := ChunkForStorage(doc)
	if len(a) != len(b) || len(a) < 2 {
		t.Fatalf("nondeterministic chunk count: %d vs %d (want stable, >1)", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Content != b[i].Content {
			t.Errorf("chunk %d differs between calls: id %q vs %q", i, a[i].ID, b[i].ID)
		}
	}
}
