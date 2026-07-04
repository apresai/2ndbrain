package document

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// blockRefRe matches a trailing Obsidian block-reference marker, e.g. a line
// ending in " ^abc-123". The id is letters, digits, and hyphens.
var blockRefRe = regexp.MustCompile(`(?m)(?:^|\s)\^([A-Za-z0-9-]+)[ \t]*$`)

type Chunk struct {
	ID          string `json:"id"`
	DocID       string `json:"doc_id"`
	HeadingPath string `json:"heading_path"`
	Level       int    `json:"level"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	SortOrder   int    `json:"sort_order"`
	// BlockID is the last Obsidian block-reference id (^id) found in the chunk,
	// best-effort: a heading-bounded chunk can hold several, so the last wins.
	BlockID string `json:"block_id,omitempty"`
}

// ChunkDocument splits a document into chunks at heading boundaries. Obsidian
// comments are stripped first so they don't leak into FTS/search/read; newline
// count is preserved so chunk line numbers stay accurate.
func ChunkDocument(doc *Document) []Chunk {
	lines := strings.Split(StripComments(doc.Body), "\n")
	var chunks []Chunk
	var headingStack []string

	currentContent := strings.Builder{}
	currentLevel := 0
	startLine := 1
	order := 0

	flushChunk := func(endLine int) {
		content := strings.TrimSpace(currentContent.String())
		if content == "" {
			return
		}
		headingPath := strings.Join(headingStack, " > ")
		if headingPath == "" {
			headingPath = "(preamble)"
		}
		chunkID := makeChunkID(doc.ID, headingPath)
		chunks = append(chunks, Chunk{
			ID:          chunkID,
			DocID:       doc.ID,
			HeadingPath: headingPath,
			Level:       currentLevel,
			Content:     content,
			ContentHash: hash(content),
			StartLine:   startLine,
			EndLine:     endLine,
			SortOrder:   order,
			BlockID:     lastBlockID(content),
		})
		order++
	}

	for i, line := range lines {
		lineNum := i + 1
		level := headingLevel(line)

		if level > 0 {
			flushChunk(lineNum - 1)

			title := strings.TrimSpace(line[level:])

			// Adjust heading stack: pop to parent level
			for len(headingStack) > 0 && getStackLevel(headingStack, level) {
				headingStack = headingStack[:len(headingStack)-1]
			}
			headingStack = append(headingStack, strings.Repeat("#", level)+" "+title)

			currentLevel = level
			currentContent.Reset()
			startLine = lineNum
		}

		currentContent.WriteString(line)
		currentContent.WriteString("\n")
	}

	flushChunk(len(lines))
	return chunks
}

// maxEmbedChunkChars caps a chunk's Content (in runes) before it is stored or
// embedded. Heading-boundary chunking has no size bound, but Amazon Nova-2
// silently END-truncates any input past its 8192-token context (dropping the
// tail from the vector) and HARD-REJECTS an inline value over 50,000 characters
// with a ValidationException that fails the whole note's embedding. So a long
// heading-less section (an imported article, a transcript, a big code block, a
// daily note grown by repeated `daily append`) is a real content-loss risk.
// 6000 runes keeps English and code sections well under 8192 tokens while
// staying a solid retrieval unit; only oversized chunks are split, so the
// common case is untouched. (Very token-dense scripts like CJK could still
// exceed 8192 tokens at 6000 runes; that tail-truncation is now observable via
// embedNova's warning and is non-catastrophic — unlike the >50k reject this cap
// prevents outright.)
const maxEmbedChunkChars = 6000

// chunkOverlapChars is the rune overlap carried between adjacent sub-chunks so a
// fact spanning a split boundary isn't orphaned from both sides.
const chunkOverlapChars = 500

// ChunkForStorage is the chunking used on the two persistence paths (the indexer
// that writes the chunks table and embed.Document that writes vec_chunks): the
// heading-level ChunkDocument output with any oversized chunk sub-split via
// SplitLongChunks. Both paths must produce the SAME chunk set so the vec search's
// chunk_id -> heading_path join stays valid. The live display/read/RAG-windowing
// paths keep calling ChunkDocument directly (heading-level granularity).
func ChunkForStorage(doc *Document) []Chunk {
	return SplitLongChunks(ChunkDocument(doc), maxEmbedChunkChars)
}

// SplitLongChunks returns chunks with any whose Content exceeds maxChars runes
// split into overlapping sub-chunks at paragraph/line/word boundaries. Sub-chunks
// of one section share its HeadingPath but get DISTINCT ids (a "#part-N" suffix
// folded into the id hash) so they don't collide under the chunks table's
// ON CONFLICT(id) upsert or vec_chunks' chunk_id PRIMARY KEY — a naive split that
// reused the section's single id would let the last-wins dedup keep only the last
// part and silently drop the rest, reintroducing the exact content loss the cap
// exists to fix. maxChars <= 0 disables splitting.
func SplitLongChunks(chunks []Chunk, maxChars int) []Chunk {
	if maxChars <= 0 {
		return chunks
	}
	out := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		if utf8.RuneCountInString(c.Content) <= maxChars {
			out = append(out, c)
			continue
		}
		out = append(out, splitChunk(c, maxChars, chunkOverlapChars)...)
	}
	return out
}

func splitChunk(c Chunk, maxChars, overlap int) []Chunk {
	runes := []rune(c.Content)
	ranges := splitTextRanges(runes, maxChars, overlap)
	if len(ranges) <= 1 {
		return []Chunk{c}
	}
	out := make([]Chunk, 0, len(ranges))
	for i, r := range ranges {
		content := strings.TrimSpace(string(runes[r[0]:r[1]]))
		if content == "" {
			continue
		}
		startLine := c.StartLine + strings.Count(string(runes[:r[0]]), "\n")
		out = append(out, Chunk{
			ID:          makeChunkID(c.DocID, fmt.Sprintf("%s#part-%d", c.HeadingPath, i)),
			DocID:       c.DocID,
			HeadingPath: c.HeadingPath,
			Level:       c.Level,
			Content:     content,
			ContentHash: hash(content),
			StartLine:   startLine,
			EndLine:     startLine + strings.Count(content, "\n"),
			SortOrder:   c.SortOrder,
			BlockID:     lastBlockID(content),
		})
	}
	if len(out) == 0 {
		return []Chunk{c}
	}
	return out
}

// splitTextRanges partitions text into [start,end) rune ranges each at most
// maxChars long, preferring to cut at a paragraph break, then a line break, then
// whitespace, within the back half of each window; adjacent ranges overlap by up
// to `overlap` runes. It always makes forward progress, so it terminates.
func splitTextRanges(text []rune, maxChars, overlap int) [][2]int {
	n := len(text)
	if n <= maxChars {
		return [][2]int{{0, n}}
	}
	var ranges [][2]int
	for pos := 0; pos < n; {
		if pos+maxChars >= n {
			ranges = append(ranges, [2]int{pos, n})
			break
		}
		end := pos + maxChars
		cut := findCut(text, pos+maxChars/2, end)
		if cut <= pos {
			cut = end
		}
		ranges = append(ranges, [2]int{pos, cut})
		next := cut - overlap
		if next <= pos {
			next = cut // guarantee forward progress
		}
		pos = next
	}
	return ranges
}

// findCut returns the exclusive end for the current piece: just after the last
// paragraph break ("\n\n") in (lo, hi), else the last line break, else the last
// whitespace run; hi when none is found. Preferring higher-level boundaries keeps
// sub-chunks semantically whole.
func findCut(text []rune, lo, hi int) int {
	para, line, space := -1, -1, -1
	for i := lo; i < hi; i++ {
		switch text[i] {
		case '\n':
			if i+1 < hi && text[i+1] == '\n' {
				para = i + 1
			}
			line = i + 1
		case ' ', '\t':
			space = i + 1
		}
	}
	switch {
	case para > lo:
		return para
	case line > lo:
		return line
	case space > lo:
		return space
	default:
		return hi
	}
}

// lastBlockID returns the id of the last trailing "^block-id" marker in the
// content, or "" if there is none.
func lastBlockID(content string) string {
	matches := blockRefRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
}

func headingLevel(line string) int {
	trimmed := strings.TrimLeft(line, "#")
	level := len(line) - len(trimmed)
	if level >= 1 && level <= 6 && len(trimmed) > 0 && trimmed[0] == ' ' {
		return level
	}
	return 0
}

func getStackLevel(stack []string, targetLevel int) bool {
	if len(stack) == 0 {
		return false
	}
	last := stack[len(stack)-1]
	level := headingLevel(last)
	return level >= targetLevel
}

func makeChunkID(docID, headingPath string) string {
	h := sha256.Sum256([]byte(docID + ":" + headingPath))
	return fmt.Sprintf("%x", h[:8])
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}
