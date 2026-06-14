package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	tasksDone  bool
	tasksTodo  bool
	tasksPath  string
	tasksTotal bool

	taskState string
)

// TaskRow is the serializable payload for a single task in `2nb tasks` output.
// It pairs document.Task fields with the source document's path so a flat list
// across the vault stays addressable: the (path, line) pair is exactly what
// `2nb task <path> <line>` toggles.
type TaskRow struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Done bool   `json:"done"`
	Text string `json:"text"`
}

var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "List GFM checkbox tasks across the vault",
	Long: `List GFM checkbox tasks ("- [ ]" / "- [x]") found in document bodies.

By default every task in every indexed document is listed. Narrow the set with
--done (only completed), --todo (only open), or --path <file|dir> (only tasks
under a vault-relative file or directory).

v1 recognizes GFM open/done checkboxes only. Custom statuses such as [>] or [-]
(Obsidian Tasks plugin extensions) are not treated as tasks.`,
	Example: `  2nb tasks
  2nb tasks --todo
  2nb tasks --done --json
  2nb tasks --path projects/`,
	Args: cobra.NoArgs,
	RunE: runTasks,
}

var taskCmd = &cobra.Command{
	Use:   "task <path> <line>",
	Short: "Toggle a single GFM checkbox task by line",
	Long: `Toggle the GFM checkbox on a single line of a document.

<line> is the 1-based line number within the document body (frontmatter
excluded), matching the LINE column from "2nb tasks". By default the box is
toggled; pass --done to force it checked, --todo to force it unchecked, or
--toggle to invert it explicitly. The line must be a GFM checkbox or the
command errors without writing.

Only the checkbox marker changes; the task text, indentation, and the rest of
the document are left byte-for-byte intact (frontmatter untouched).`,
	Example: `  2nb task notes/todo.md 5
  2nb task notes/todo.md 5 --done
  2nb task notes/todo.md 5 --todo`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeDocPaths,
	RunE:              runTask,
}

func init() {
	tasksCmd.Flags().BoolVar(&tasksDone, "done", false, "Only completed tasks")
	tasksCmd.Flags().BoolVar(&tasksTodo, "todo", false, "Only open tasks")
	tasksCmd.Flags().StringVar(&tasksPath, "path", "", "Limit to tasks under a vault-relative file or directory")
	tasksCmd.Flags().BoolVar(&tasksTotal, "total", false, "Print only the count of matching tasks")
	tasksCmd.GroupID = "docs"
	rootCmd.AddCommand(tasksCmd)

	// --done/--todo/--toggle select the target state. They are mutually
	// exclusive; default (none set) is --toggle.
	taskCmd.Flags().Bool("done", false, "Force the box checked")
	taskCmd.Flags().Bool("todo", false, "Force the box unchecked")
	taskCmd.Flags().Bool("toggle", false, "Invert the box (default)")
	taskCmd.MarkFlagsMutuallyExclusive("done", "todo", "toggle")
	taskCmd.GroupID = "docs"
	rootCmd.AddCommand(taskCmd)
}

func runTasks(cmd *cobra.Command, args []string) error {
	if tasksDone && tasksTodo {
		return exitWithError(ExitValidation, "error: --done and --todo are mutually exclusive")
	}

	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	paths, err := v.DB.AllDocumentPaths()
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}

	// --path scopes to a single file or a directory subtree. Normalize the
	// scope to a vault-relative, slash-separated prefix so it matches the
	// stored documents.path values regardless of how the user typed it.
	scope := ""
	if tasksPath != "" {
		scope = filepath.ToSlash(v.RelPath(v.AbsPath(expandPath(tasksPath))))
	}

	rows := make([]TaskRow, 0)
	for _, p := range paths {
		if !pathInScope(p, scope) {
			continue
		}
		// Read-only synthetic views (.canvas/.base) don't carry GFM checkboxes
		// in their markdown body in any meaningful, editable way; skip them so
		// `task` never points at a non-writable line.
		doc, perr := document.ParseFile(v.AbsPath(p))
		if perr != nil {
			// A path in the index that no longer parses (deleted/renamed out of
			// band) is skipped rather than failing the whole listing.
			continue
		}
		if document.IsReadOnlyType(doc.Type) {
			continue
		}
		for _, tk := range document.ExtractTasks(doc.Body) {
			if tasksDone && !tk.Done {
				continue
			}
			if tasksTodo && tk.Done {
				continue
			}
			rows = append(rows, TaskRow{Path: p, Line: tk.Line, Done: tk.Done, Text: tk.Text})
		}
	}

	// Obsidian-compat listing modes (--total / format=paths / format=tree).
	if handled, err := renderList(cmd, rows, tasksTotal, func(r TaskRow) string { return r.Path }); handled || err != nil {
		return err
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, rows)
	}

	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "No tasks found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DONE\tLINE\tPATH\tTEXT")
	for _, r := range rows {
		mark := " "
		if r.Done {
			mark = "x"
		}
		fmt.Fprintf(w, "[%s]\t%d\t%s\t%s\n", mark, r.Line, r.Path, r.Text)
	}
	return w.Flush()
}

// pathInScope reports whether a vault-relative document path falls under the
// given scope. An empty scope matches everything. A scope that equals the path
// matches that single file; otherwise the scope is treated as a directory
// prefix (so "projects" matches "projects/foo.md" but not "projects-archive.md").
func pathInScope(p, scope string) bool {
	if scope == "" {
		return true
	}
	p = filepath.ToSlash(p)
	if p == scope {
		return true
	}
	prefix := strings.TrimSuffix(scope, "/") + "/"
	return strings.HasPrefix(p, prefix)
}

func runTask(cmd *cobra.Command, args []string) error {
	line, err := strconv.Atoi(args[1])
	if err != nil || line < 1 {
		return exitWithError(ExitValidation, fmt.Sprintf("error: <line> must be a positive integer, got %q", args[1]))
	}

	// Resolve the target state from the flags. Mutual exclusivity is enforced
	// by cobra (MarkFlagsMutuallyExclusive); default is toggle.
	taskState = "toggle"
	if d, _ := cmd.Flags().GetBool("done"); d {
		taskState = "done"
	}
	if td, _ := cmd.Flags().GetBool("todo"); td {
		taskState = "todo"
	}

	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()

	absPath, _, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}
	doc.Path = v.RelPath(absPath)

	bodyLines := strings.Split(doc.Body, "\n")
	if line > len(bodyLines) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: line %d is past the end of %s (body has %d lines)", line, doc.Path, len(bodyLines)))
	}

	idx := line - 1
	updated, ok := document.ToggleTaskLine(bodyLines[idx], taskState)
	if !ok {
		return exitWithError(ExitValidation, fmt.Sprintf("error: line %d of %s is not a GFM checkbox task: %q", line, doc.Path, bodyLines[idx]))
	}
	bodyLines[idx] = updated
	doc.Body = strings.Join(bodyLines, "\n")

	if err := writeBody(v, doc, absPath); err != nil {
		return err
	}

	if format := getFormat(cmd); format != "" {
		// Re-parse the flipped line so the reported done state reflects what was
		// written, not the requested state (a no-op toggle still reports truth).
		var done bool
		if tk := document.ExtractTasks(updated); len(tk) == 1 {
			done = tk[0].Done
		}
		return output.Write(os.Stdout, format, map[string]any{
			"path": doc.Path,
			"line": line,
			"done": done,
			"text": strings.TrimSpace(updated),
		})
	}
	fmt.Printf("task: %s:%d -> %s\n", doc.Path, line, strings.TrimSpace(updated))
	return nil
}
