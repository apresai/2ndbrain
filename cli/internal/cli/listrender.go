package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// renderList handles the obsidian-compat listing modes that layer on top of the
// standard machine formats: `--total` (print the count only), `format=paths`
// (one vault-relative path per line), and `format=tree` (an indented directory
// hierarchy). pathOf extracts the path used by paths/tree.
//
// It returns (handled, error): handled=true means the output was produced here;
// handled=false means none of these modes applied and the caller should fall
// back to its own rendering (json/csv/tsv/yaml via getFormat, or a pretty
// default). Call it before any empty-state hint so a count/paths output stays
// clean and scriptable even for an empty result.
func renderList[T any](cmd *cobra.Command, items []T, total bool, pathOf func(T) string) (bool, error) {
	if total {
		_, err := fmt.Fprintln(os.Stdout, len(items))
		return true, err
	}
	switch flagFormat {
	case "paths":
		for _, it := range items {
			if _, err := fmt.Fprintln(os.Stdout, pathOf(it)); err != nil {
				return true, err
			}
		}
		return true, nil
	case "tree":
		paths := make([]string, 0, len(items))
		for _, it := range items {
			paths = append(paths, pathOf(it))
		}
		return true, writePathTree(os.Stdout, paths)
	}
	return false, nil
}

// treeNode is one directory/file node in the format=tree renderer.
type treeNode struct {
	children map[string]*treeNode
}

func newTreeNode() *treeNode { return &treeNode{children: map[string]*treeNode{}} }

// writePathTree renders vault-relative paths as an indented tree, grouping by
// directory. Paths are sorted so the output is deterministic.
func writePathTree(w io.Writer, paths []string) error {
	root := newTreeNode()
	for _, p := range paths {
		cur := root
		for _, part := range strings.Split(p, "/") {
			if part == "" {
				continue
			}
			child, ok := cur.children[part]
			if !ok {
				child = newTreeNode()
				cur.children[part] = child
			}
			cur = child
		}
	}
	return renderTreeNode(w, root, 0)
}

func renderTreeNode(w io.Writer, n *treeNode, depth int) error {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := fmt.Fprintf(w, "%s%s\n", strings.Repeat("  ", depth), name); err != nil {
			return err
		}
		if err := renderTreeNode(w, n.children[name], depth+1); err != nil {
			return err
		}
	}
	return nil
}
