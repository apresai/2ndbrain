package document

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CanvasNode struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Text   string  `json:"text,omitempty"`
	File   string  `json:"file,omitempty"`
	Label  string  `json:"label,omitempty"`
	URL    string  `json:"url,omitempty"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type CanvasEdge struct {
	ID       string `json:"id"`
	FromNode string `json:"fromNode"`
	ToNode   string `json:"toNode"`
	FromSide string `json:"fromSide,omitempty"`
	ToSide   string `json:"toSide,omitempty"`
	Label    string `json:"label,omitempty"`
}

type Canvas struct {
	Nodes []CanvasNode `json:"nodes"`
	Edges []CanvasEdge `json:"edges"`
}

// ParseCanvas parses a .canvas file from JSON content into a Document representation.
func ParseCanvas(path string, content []byte) (*Document, error) {
	var canvas Canvas
	if err := json.Unmarshal(content, &canvas); err != nil {
		return nil, fmt.Errorf("unmarshal canvas: %w", err)
	}

	// Create node lookup to build friendly edge descriptions
	nodeMap := make(map[string]CanvasNode)
	for _, n := range canvas.Nodes {
		nodeMap[n.ID] = n
	}

	var bodyBuilder strings.Builder
	bodyBuilder.WriteString("# Canvas Nodes\n\n")

	// Formulate body text from nodes
	for _, n := range canvas.Nodes {
		switch n.Type {
		case "text":
			bodyBuilder.WriteString(fmt.Sprintf("## Node %s (text)\n", n.ID))
			bodyBuilder.WriteString(n.Text)
			bodyBuilder.WriteString("\n\n")
		case "file":
			bodyBuilder.WriteString(fmt.Sprintf("## Node %s (file)\n", n.ID))
			bodyBuilder.WriteString(fmt.Sprintf("[[%s]]\n\n", n.File))
		case "group":
			bodyBuilder.WriteString(fmt.Sprintf("## Node %s (group)\n", n.ID))
			if n.Label != "" {
				bodyBuilder.WriteString(fmt.Sprintf("Label: %s\n\n", n.Label))
			} else {
				bodyBuilder.WriteString("Label: (unnamed group)\n\n")
			}
		case "link":
			bodyBuilder.WriteString(fmt.Sprintf("## Node %s (link)\n", n.ID))
			bodyBuilder.WriteString(fmt.Sprintf("URL: %s\n\n", n.URL))
		}
	}

	bodyBuilder.WriteString("# Canvas Edges\n\n")
	for _, e := range canvas.Edges {
		bodyBuilder.WriteString(fmt.Sprintf("## Edge %s\n", e.ID))
		fromDesc := describeNodeForEdge(nodeMap[e.FromNode])
		toDesc := describeNodeForEdge(nodeMap[e.ToNode])
		bodyBuilder.WriteString(fmt.Sprintf("From: %s\n", fromDesc))
		bodyBuilder.WriteString(fmt.Sprintf("To: %s\n", toDesc))
		if e.Label != "" {
			bodyBuilder.WriteString(fmt.Sprintf("Label: %s\n", e.Label))
		}
		bodyBuilder.WriteString("\n")
	}

	title := filepath.Base(path)
	title = strings.TrimSuffix(title, filepath.Ext(title))

	// Get file modification time as default created/modified
	modTime := time.Now().UTC().Format(time.RFC3339)
	if info, err := os.Stat(path); err == nil {
		modTime = info.ModTime().UTC().Format(time.RFC3339)
	}

	doc := &Document{
		Path:       path,
		Title:      title,
		Type:       "canvas",
		Status:     "complete",
		CreatedAt:  modTime,
		ModifiedAt: modTime,
		Body:       bodyBuilder.String(),
		Frontmatter: map[string]any{
			"title":    title,
			"type":     "canvas",
			"status":   "complete",
			"created":  modTime,
			"modified": modTime,
		},
	}

	return doc, nil
}

// truncateRunes shortens s to at most limit runes, appending "..." when it
// trims. Operating on runes (not bytes) guarantees a multibyte UTF-8 character
// is never split mid-sequence.
func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-3]) + "..."
}

func describeNodeForEdge(n CanvasNode) string {
	switch n.Type {
	case "text":
		// Truncate (rune-safe, so a multibyte character is never split).
		t := truncateRunes(n.Text, 30)
		t = strings.ReplaceAll(t, "\n", " ")
		return fmt.Sprintf("Card %q (%s)", t, n.ID)
	case "file":
		return fmt.Sprintf("[[%s]]", n.File)
	case "group":
		return fmt.Sprintf("Group %q (%s)", n.Label, n.ID)
	case "link":
		return fmt.Sprintf("Link %q (%s)", n.URL, n.ID)
	default:
		return fmt.Sprintf("Node %s", n.ID)
	}
}
