package graph

import (
	"database/sql"
	"fmt"
)

type Node struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Title string `json:"title"`
	Type  string `json:"type"`
	Depth int    `json:"depth"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Label  string `json:"label,omitempty"`
}

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Traverse performs a BFS from the given document to the specified depth.
func Traverse(db *sql.DB, docID string, maxDepth int) (*Graph, error) {
	g := &Graph{}
	visited := make(map[string]bool)

	// Seed: get the starting document
	root, err := getNode(db, docID, 0)
	if err != nil {
		return nil, fmt.Errorf("get root node: %w", err)
	}
	g.Nodes = append(g.Nodes, *root)
	visited[docID] = true

	// BFS
	queue := []string{docID}
	depths := map[string]int{docID: 0}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		currentDepth := depths[current]

		if currentDepth >= maxDepth {
			continue
		}

		// Get outgoing links
		neighbors, edges, err := getNeighbors(db, current)
		if err != nil {
			continue
		}

		for i, neighbor := range neighbors {
			g.Edges = append(g.Edges, edges[i])

			if !visited[neighbor] {
				visited[neighbor] = true
				nextDepth := currentDepth + 1
				depths[neighbor] = nextDepth
				queue = append(queue, neighbor)

				node, err := getNode(db, neighbor, nextDepth)
				if err == nil {
					g.Nodes = append(g.Nodes, *node)
				}
			}
		}
	}

	return g, nil
}

// AdjacencyList returns the link graph for a document as an adjacency list.
func AdjacencyList(db *sql.DB, docID string) (map[string][]string, error) {
	adj := make(map[string][]string)

	rows, err := db.Query(`
		SELECT source_id, target_id FROM links
		WHERE (source_id = ? OR target_id = ?) AND target_id IS NOT NULL AND resolved = 1
	`, docID, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var src, tgt string
		if err := rows.Scan(&src, &tgt); err != nil {
			continue
		}
		adj[src] = append(adj[src], tgt)
	}

	return adj, rows.Err()
}

func getNode(db *sql.DB, docID string, depth int) (*Node, error) {
	row := db.QueryRow("SELECT id, path, title, doc_type FROM documents WHERE id = ?", docID)
	var n Node
	if err := row.Scan(&n.ID, &n.Path, &n.Title, &n.Type); err != nil {
		return nil, err
	}
	n.Depth = depth
	return &n, nil
}

func getNeighbors(db *sql.DB, docID string) ([]string, []Edge, error) {
	// Forward links (outgoing)
	rows, err := db.Query(`
		SELECT l.target_id, l.target_raw
		FROM links l
		WHERE l.source_id = ? AND l.target_id IS NOT NULL
		UNION
		SELECT l.source_id, l.target_raw
		FROM links l
		WHERE l.target_id = ?
	`, docID, docID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var neighbors []string
	var edges []Edge
	seen := make(map[string]bool)

	for rows.Next() {
		var neighborID, label string
		if err := rows.Scan(&neighborID, &label); err != nil {
			continue
		}
		if seen[neighborID] {
			continue
		}
		seen[neighborID] = true
		neighbors = append(neighbors, neighborID)
		edges = append(edges, Edge{Source: docID, Target: neighborID, Label: label})
	}

	return neighbors, edges, rows.Err()
}
