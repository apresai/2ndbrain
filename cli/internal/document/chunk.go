package document

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
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
