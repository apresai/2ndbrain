package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	moveDryRun bool
	moveForce  bool
)

var moveCmd = &cobra.Command{
	Use:   "move <src> <dst>",
	Short: "Move or rename a note, rewriting every [[wikilink]] that points at it",
	Long: `Move (or rename) a note to a new vault-relative path and rewrite every
[[wikilink]] across the vault that points at it, so links stay valid.

This is the strongest write surface in 2nb: it is the only command that edits
OTHER notes' bodies. Use --dry-run first to preview the rename, the exact links
that would be rewritten, and any links it would skip as ambiguous. Without
--force, a move is refused when the pre-scan finds a blocking ambiguity (a bare
[[name]] link whose name matches more than one note, so we can't be sure it
points at the one being moved).

Links inside fenced or inline code are never touched. The #heading / #^block /
|alias suffix and any leading "!" embed marker on a link are preserved; only the
target portion is rewritten, in the same shape the author used (a bare basename
stays a basename, a path stays a path).

The target file is moved LAST, after every referencing note is rewritten, so an
interruption mid-run leaves links pointing at the still-present old name rather
than orphaning them.`,
	Example: `  2nb move old.md archive/old.md
  2nb move notes/draft.md notes/final.md --dry-run
  2nb move a.md b.md --force`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeDocPaths,
	RunE:              runMove,
}

func init() {
	moveCmd.Flags().BoolVar(&moveDryRun, "dry-run", false, "Preview the move and rewrites without modifying anything")
	moveCmd.Flags().BoolVar(&moveForce, "force", false, "Overwrite an existing destination and proceed despite ambiguous links")
	moveCmd.GroupID = "docs"
	rootCmd.AddCommand(moveCmd)
}

// moveRewrite is the per-file plan and outcome for a referencing note.
type moveRewrite struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// moveAmbiguous records a referencing note holding a bare-name link that could
// not be safely rewritten because the old name resolves to more than one doc.
type moveAmbiguous struct {
	Path   string `json:"path"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// moveFailure records a referencing note (or the moved file) that errored during
// the write phase, so the result reports partial success accurately.
type moveFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type moveResult struct {
	Moved struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"moved"`
	Rewritten        []moveRewrite   `json:"rewritten"`
	SkippedAmbiguous []moveAmbiguous `json:"skipped_ambiguous"`
	Failed           []moveFailure   `json:"failed"`
	DryRun           bool            `json:"dry_run"`
}

func runMove(cmd *cobra.Command, args []string) error {
	return moveImpl(cmd, args[0], args[1])
}

// moveImpl is the shared implementation behind `move` and `rename`. src and dst
// are user-supplied (possibly with ~ expansion); both are resolved relative to
// the vault.
func moveImpl(cmd *cobra.Command, src, dst string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	// (a) Resolve src doc.
	srcRel := expandPath(src)
	srcAbs := v.AbsPath(srcRel)
	srcRel = v.RelPath(srcAbs)
	srcDoc, err := v.DB.GetDocumentByPath(srcRel)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("document not found: %s\n\nRun `2nb list` to see available documents", srcRel))
	}
	if document.IsReadOnlyType(srcDoc.Type) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: cannot move a read-only %s file (%s); .canvas/.base files are indexed read-only", srcDoc.Type, srcRel))
	}

	// (b) Validate dst.
	if filepath.IsAbs(dst) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: destination must be a vault-relative path, not absolute: %s", dst))
	}
	dstRel := expandPath(dst)
	dstAbs := v.AbsPath(dstRel)
	dstRel = v.RelPath(dstAbs)
	if !v.ContainsPath(dstAbs) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: destination escapes the vault: %s", dst))
	}
	if !strings.HasSuffix(strings.ToLower(dstRel), ".md") {
		return exitWithError(ExitValidation, fmt.Sprintf("error: destination must be a markdown (.md) path: %s", dstRel))
	}
	if srcRel == dstRel {
		return exitWithError(ExitValidation, "error: source and destination are the same path")
	}
	if _, statErr := os.Stat(dstAbs); statErr == nil && !moveForce {
		return exitWithError(ExitValidation, fmt.Sprintf("error: destination already exists: %s (use --force to overwrite)", dstRel))
	}

	// (c) Find referencing notes: resolved backlinks PLUS unresolved links whose
	// raw target names the moved doc. Dedupe by source path.
	backlinks, err := v.DB.Backlinks(srcDoc.ID)
	if err != nil {
		return fmt.Errorf("backlinks: %w", err)
	}
	rawRefs, err := v.DB.LinksByRawName(srcRel)
	if err != nil {
		return fmt.Errorf("scan unresolved links: %w", err)
	}
	refPaths := dedupeRefPaths(backlinks, rawRefs, srcRel)

	// Detect ambiguity once: a bare basename link to the old name is ambiguous
	// when the vault holds more than one doc with that basename, so we can't be
	// sure it points at the one being moved.
	bareAmbiguous, err := basenameIsAmbiguous(v, srcRel)
	if err != nil {
		return fmt.Errorf("check ambiguity: %w", err)
	}

	result := moveResult{Rewritten: []moveRewrite{}, SkippedAmbiguous: []moveAmbiguous{}, Failed: []moveFailure{}, DryRun: moveDryRun}
	result.Moved.From = srcRel
	result.Moved.To = dstRel

	// (d) PLAN pass: compute the rewritten body for every referencing note
	// without touching disk. We separate planning from applying so the ambiguity
	// gate below can refuse a real move BEFORE any write happens. Writing then
	// refusing would partially rewrite path-form links yet leave the file in
	// place, pointing those links at a path that does not exist.
	type pendingWrite struct {
		abs     string
		doc     *document.Document
		path    string
		newBody string
	}
	var pending []pendingWrite
	for _, refPath := range refPaths {
		refAbs := v.AbsPath(refPath)
		refDoc, perr := document.ParseFile(refAbs)
		if perr != nil {
			result.Failed = append(result.Failed, moveFailure{Path: refPath, Error: perr.Error()})
			continue
		}
		refDoc.Path = refPath

		var rewritten string
		var count int
		if bareAmbiguous {
			// The old basename names more than one note, so a bare [[name]] link
			// can't be attributed to the moved note. Rewrite only path-qualified
			// forms and report each bare occurrence as a skipped ambiguity.
			rewritten, count = document.RewriteWikiLinksPathOnly(refDoc.Body, srcRel, dstRel)
			if bare := bareNameOccurrences(refDoc.Body, srcRel); bare > 0 {
				base := document.Basename(srcRel)
				result.SkippedAmbiguous = append(result.SkippedAmbiguous, moveAmbiguous{
					Path:   refPath,
					Target: base,
					Reason: fmt.Sprintf("bare link [[%s]] is ambiguous: %s names more than one note in the vault", base, base),
				})
			}
		} else {
			rewritten, count = document.RewriteWikiLinks(refDoc.Body, srcRel, dstRel)
		}

		if count == 0 {
			continue
		}
		result.Rewritten = append(result.Rewritten, moveRewrite{Path: refPath, Count: count})
		pending = append(pending, pendingWrite{abs: refAbs, doc: refDoc, path: refPath, newBody: rewritten})
	}

	// (g) Refuse a real (non-force) move when the pre-scan found blocking
	// ambiguity, BEFORE any write. The dry-run still reports it below.
	if !moveDryRun && len(result.SkippedAmbiguous) > 0 && !moveForce {
		printMoveResult(cmd, &result)
		return exitWithError(ExitValidation, fmt.Sprintf("error: %d ambiguous link(s) cannot be safely rewritten; re-run with --dry-run to inspect, or --force to move anyway (rewriting only the unambiguous path-qualified links and leaving the bare ones untouched)", len(result.SkippedAmbiguous)))
	}

	if moveDryRun {
		printMoveResult(cmd, &result)
		return nil
	}

	// APPLY pass: write each planned rewrite to disk. Done before the file move
	// so an interruption leaves links pointing at the still-present old name.
	for _, p := range pending {
		p.doc.Body = p.newBody
		if werr := writeBody(v, p.doc, p.abs); werr != nil {
			result.Failed = append(result.Failed, moveFailure{Path: p.path, Error: werr.Error()})
		}
	}

	// (e) Move the target file LAST, after referencing notes are rewritten.
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		result.Failed = append(result.Failed, moveFailure{Path: dstRel, Error: err.Error()})
		printMoveResult(cmd, &result)
		return exitWithError(ExitValidation, fmt.Sprintf("error: create destination directory: %v", err))
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		result.Failed = append(result.Failed, moveFailure{Path: dstRel, Error: err.Error()})
		printMoveResult(cmd, &result)
		return exitWithError(ExitValidation, fmt.Sprintf("error: move file: %v", err))
	}

	// (e2) Rewrite the moved note's OWN self-links. A note that links to itself
	// (e.g. body holds [[old]] or [[old#section]]) is deliberately excluded from
	// the referencing-note set above, because a referencing-note rewrite would
	// target the OLD on-disk path which no longer exists after the rename. Its
	// self-links must instead be rewritten in the moved file at its NEW path:
	// IndexSingleFile only updates the index, never the body, so without this the
	// self-links are left pointing at the old name and lint flags them broken.
	if movedDoc, perr := document.ParseFile(dstAbs); perr == nil {
		if rewritten, count := document.RewriteWikiLinks(movedDoc.Body, srcRel, dstRel); count > 0 {
			movedDoc.Body = rewritten
			movedDoc.Path = v.RelPath(dstAbs)
			if werr := writeBody(v, movedDoc, dstAbs); werr != nil {
				slog.Warn("rewrite moved note self-links failed", "path", dstRel, "err", werr)
				fmt.Fprintf(os.Stderr, "warning: rewrite self-links in %s: %v\n", dstRel, werr)
			} else {
				result.Rewritten = append(result.Rewritten, moveRewrite{Path: dstRel, Count: count})
			}
		}
	}

	// (f) Reindex. First purge any pre-existing index row at the destination
	// path: with --force the destination file was just clobbered on disk, and
	// documents.path is UNIQUE, so leaving the clobbered file's row would make
	// the upsert of the moved file (a different id at the same path) fail the
	// UNIQUE constraint. This is a no-op when the destination was empty.
	if err := v.DB.DeleteDocumentByPath(dstRel); err != nil {
		slog.Warn("purge clobbered destination row failed", "path", dstRel, "err", err)
	}
	// Index the moved file at its new path, then purge the stale old-path row.
	// IndexSingleFile keys on the document id, and the moved file keeps its
	// frontmatter id, so the upsert UPDATES the existing row's path to the new
	// location; the old-path row is then gone and the by-path purge is a harmless
	// no-op. If the moved file had NO frontmatter id, IndexSingleFile mints a
	// fresh one and the old-path row would linger, so the by-path purge is what
	// removes it. (Purging by srcDoc.ID would be a bug in the id-reuse case: it
	// would delete the row we just indexed.)
	if err := vault.IndexSingleFile(v, dstAbs); err != nil {
		slog.Warn("index moved file failed", "path", dstRel, "err", err)
		fmt.Fprintf(os.Stderr, "warning: index moved file %s: %v\n", dstRel, err)
	}
	if err := v.DB.DeleteDocumentByPath(srcRel); err != nil {
		slog.Warn("purge old index row failed", "path", srcRel, "err", err)
		fmt.Fprintf(os.Stderr, "warning: purge old index row %s: %v\n", srcRel, err)
	}
	// The referencing notes (and the moved note's own self-link rewrite in e2)
	// were written via writeBody, which already runs IndexSingleFile + inline
	// embed per file, so they need no second pass here. A single final
	// ResolveLinks re-points every rewritten link to the moved doc and
	// re-resolves the moved doc's own outbound links from its new location.
	if err := v.DB.ResolveLinks(); err != nil {
		slog.Warn("resolve links after move failed", "err", err)
	}

	slog.Info("moved document", "from", srcRel, "to", dstRel, "rewritten", len(result.Rewritten))
	printMoveResult(cmd, &result)

	if len(result.Failed) > 0 {
		return exitWithError(ExitValidation, fmt.Sprintf("error: %d file(s) failed during the move (see output)", len(result.Failed)))
	}
	return nil
}

// dedupeRefPaths merges resolved backlink sources and unresolved-by-name link
// sources into a sorted, de-duplicated list of referencing note paths, excluding
// the moved document itself. The moved doc's own self-links cannot be rewritten
// here (a referencing-note rewrite targets the OLD path, which no longer exists
// after the rename); they are handled separately in step (e2) of moveImpl, which
// rewrites the moved file's body at its NEW path.
func dedupeRefPaths(backlinks, rawRefs []store.LinkRef, srcRel string) []string {
	seen := make(map[string]struct{})
	for _, b := range backlinks {
		if b.Path != srcRel {
			seen[b.Path] = struct{}{}
		}
	}
	for _, r := range rawRefs {
		if r.Path != srcRel {
			seen[r.Path] = struct{}{}
		}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// basenameIsAmbiguous reports whether the vault holds more than one document
// whose basename equals srcRel's basename. When true, a bare [[name]] link can't
// be attributed to the moved doc with certainty, so such links are skipped.
func basenameIsAmbiguous(v *vault.Vault, srcRel string) (bool, error) {
	base := document.Basename(srcRel)
	rows, err := v.DB.Conn().Query("SELECT path FROM documents")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	matches := 0
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return false, err
		}
		if document.Basename(p) == base {
			matches++
		}
	}
	return matches > 1, rows.Err()
}

// bareNameOccurrences counts the bare-basename wikilinks in body that name
// srcRel (links written as [[name]] / [[name#h]] / [[name|a]] without any folder
// prefix). It is the complement of RewriteWikiLinksPathOnly's coverage: the total
// rewritable occurrences minus the path-qualified ones equals the bare ones.
// Probing with a sentinel destination keeps it side-effect-free.
func bareNameOccurrences(body, srcRel string) int {
	const sentinel = "\x00probe-dst"
	_, total := document.RewriteWikiLinks(body, srcRel, sentinel)
	_, path := document.RewriteWikiLinksPathOnly(body, srcRel, sentinel)
	return total - path
}

// printMoveResult emits the move result as JSON/CSV/YAML when a machine format
// is requested, otherwise a human summary on stderr (so stdout stays clean for
// piping). The summary lists the rename, per-file rewrite counts, ambiguous
// skips, and failures.
func printMoveResult(cmd *cobra.Command, r *moveResult) {
	if format := getFormat(cmd); format != "" {
		_ = output.Write(os.Stdout, format, r)
		return
	}

	w := os.Stderr
	verb := "Moved"
	if r.DryRun {
		verb = "Would move"
	}
	fmt.Fprintf(w, "%s: %s -> %s\n", verb, r.Moved.From, r.Moved.To)

	total := 0
	for _, rw := range r.Rewritten {
		total += rw.Count
	}
	if len(r.Rewritten) > 0 {
		label := "Rewrote"
		if r.DryRun {
			label = "Would rewrite"
		}
		fmt.Fprintf(w, "%s %d link(s) across %d note(s):\n", label, total, len(r.Rewritten))
		for _, rw := range r.Rewritten {
			fmt.Fprintf(w, "  %s (%d)\n", rw.Path, rw.Count)
		}
	} else {
		fmt.Fprintln(w, "No links to rewrite.")
	}

	if len(r.SkippedAmbiguous) > 0 {
		fmt.Fprintf(w, "Skipped %d ambiguous link(s):\n", len(r.SkippedAmbiguous))
		for _, a := range r.SkippedAmbiguous {
			fmt.Fprintf(w, "  %s -> [[%s]]: %s\n", a.Path, a.Target, a.Reason)
		}
	}

	if len(r.Failed) > 0 {
		fmt.Fprintf(w, "Failed on %d file(s):\n", len(r.Failed))
		for _, f := range r.Failed {
			fmt.Fprintf(w, "  %s: %s\n", f.Path, f.Error)
		}
	}
}
