package document

// OutlineNode is one entry in a document's heading outline: a chunk's heading
// path plus its level and line span. It is the serializable shape shared by
// the CLI `outline` command and the MCP `kb_structure` tool so the two never
// drift apart.
type OutlineNode struct {
	ID          string `json:"id"`
	HeadingPath string `json:"heading_path"`
	Level       int    `json:"level"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
}

// BuildOutline returns the heading outline of a document, one node per chunk.
// It reuses ChunkDocument (heading-boundary chunking with comments stripped)
// so the outline reflects exactly what indexing and reading see. Both the CLI
// `outline` command and the MCP kb_structure handler call this, keeping their
// heading sets identical.
func BuildOutline(doc *Document) []OutlineNode {
	chunks := ChunkDocument(doc)
	nodes := make([]OutlineNode, len(chunks))
	for i, c := range chunks {
		nodes[i] = OutlineNode{
			ID:          c.ID,
			HeadingPath: c.HeadingPath,
			Level:       c.Level,
			StartLine:   c.StartLine,
			EndLine:     c.EndLine,
		}
	}
	return nodes
}
