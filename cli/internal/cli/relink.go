package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var relinkCmd = &cobra.Command{
	Use:   "relink <path>",
	Short: "Repoint a broken [[wikilink]] to a chosen existing note (no AI)",
	Long: `Rewrites every [[wikilink]] in a note whose authored target equals --from so it
points at --to instead, preserving any #heading / #^block / |alias suffix and the
author's bare-vs-path form. This is the "apply a Did-you-mean suggestion" action:
the GUI offers ranked existing-note candidates (see "2nb suggest-target") and
relink commits the one the user picks.

Matching is EXACT (case- and separator-sensitive), so relink only touches the
specific broken link you named, never a near-miss. By default it PREVIEWS; with
--write it applies the change in place and snapshots the original, reversible
with "2nb polish <path> --undo".`,
	Args: cobra.ExactArgs(1),
	RunE: runRelink,
}

var (
	relinkFrom  string
	relinkTo    string
	relinkWrite bool
)

func init() {
	relinkCmd.GroupID = "quality"
	relinkCmd.Flags().StringVar(&relinkFrom, "from", "", "The broken target to repoint (the TARGET from `broken wikilink: [[TARGET]]`), taken verbatim")
	relinkCmd.Flags().StringVar(&relinkTo, "to", "", "The existing note to point at (a path or bare name, taken verbatim)")
	relinkCmd.Flags().BoolVar(&relinkWrite, "write", false, "Apply the change in place (opt-in; default previews only) and snapshot the original for `polish --undo`")
	rootCmd.AddCommand(relinkCmd)
}

func runRelink(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(relinkFrom) == "" || strings.TrimSpace(relinkTo) == "" {
		return exitWithError(ExitValidation, "error: relink requires both --from and --to")
	}

	parsed, absPath, rel, v, err := openNoteForLinkEdit(args[0], relinkWrite)
	if err != nil {
		return err
	}
	defer v.Close()

	start := time.Now()
	newBody, n := document.RewriteWikiLinks(parsed.Body, relinkFrom, relinkTo)

	result := PolishResult{
		Path:       rel,
		Original:   parsed.Body,
		Polished:   newBody,
		Provider:   "relink",
		DurationMs: time.Since(start).Milliseconds(),
	}
	var warnings []string
	if n > 0 {
		result.LinksRepaired = []polish.LinkRepair{{Raw: relinkFrom, NewTarget: document.Basename(relinkTo)}}
		// relink is meant to point at an EXISTING note (the "apply a Did-you-mean
		// suggestion" action). Warn, but don't block, when --to resolves to no
		// note on the LIVE FILESYSTEM (vault.CollectLiveDocs, the same walk lint
		// reports from), so a typo isn't silently left as a still-broken link.
		// Resolving live rather than via the index DB means a note created
		// moments ago that isn't indexed yet resolves cleanly (no false warning),
		// and a note deleted on disk but still in the DB does warn. The check is
		// advisory, so a failed walk skips it rather than failing the relink.
		if docs, aliases, lerr := vault.CollectLiveDocs(v.Root); lerr == nil {
			if _, rerr := store.NewResolver(docs, aliases).Resolve(relinkTo); rerr != nil {
				warnings = append(warnings, fmt.Sprintf("--to %q does not resolve to an existing note; the link may remain broken", relinkTo))
			}
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("no [[%s]] link found to repoint", relinkFrom))
	}

	if relinkWrite && n > 0 {
		w, werr := writeBodyWithSnapshot(v, parsed, absPath, rel, newBody, "relink")
		if werr != nil {
			return werr
		}
		warnings = append(warnings, w...)
		fmt.Fprintf(os.Stderr, "Repointed %d link(s) in %s\n", n, rel)
	}

	return emitLinkEditResult(cmd, result, warnings, relinkWrite, n)
}

// openNoteForLinkEdit resolves a note path for a link-mutating command. When
// write is true it pins the active vault (matching repair-links/polish); the
// preview path is read-only. It parses the file and rejects read-only
// .canvas/.base views up front so the message is clear even in preview.
func openNoteForLinkEdit(arg string, write bool) (*document.Document, string, string, *vault.Vault, error) {
	var v *vault.Vault
	var err error
	if write {
		v, err = openVaultAndSetActive()
	} else {
		v, err = openVault()
	}
	if err != nil {
		return nil, "", "", nil, err
	}
	setupFileLogging(v)

	absPath, rel, err := resolveTargetArg(v, arg)
	if err != nil {
		v.Close()
		return nil, "", "", nil, err
	}
	if _, err := os.Stat(absPath); err != nil {
		v.Close()
		return nil, "", "", nil, fmt.Errorf("resolve doc path: %w", err)
	}
	parsed, err := document.ParseFile(absPath)
	if err != nil {
		v.Close()
		return nil, "", "", nil, fmt.Errorf("parse source: %w", err)
	}
	if document.IsReadOnlyType(parsed.Type) {
		v.Close()
		return nil, "", "", nil, exitWithError(ExitValidation, fmt.Sprintf("error: cannot edit links in a read-only %s file (%s); .canvas/.base files are indexed read-only", parsed.Type, rel))
	}
	return parsed, absPath, rel, v, nil
}

// writeBodyWithSnapshot writes newBody to the note and snapshots the original so
// the change is reversible with `polish --undo`, returning any warnings. It
// mirrors the write+snapshot path repair-links uses, so every link-mutating
// command shares one undo mechanism. The caller guarantees newBody differs from
// the original (the rewrite count was > 0).
func writeBodyWithSnapshot(v *vault.Vault, parsed *document.Document, absPath, rel, newBody, provider string) ([]string, error) {
	var warnings []string
	originalFull, readErr := os.ReadFile(absPath)
	canSnapshot := readErr == nil && len(originalFull) > 0
	if !canSnapshot {
		warnings = append(warnings, "could not snapshot the original; undo will be unavailable for this change")
	}
	parsed.Body = newBody
	parsed.Path = rel
	if err := writeBody(v, parsed, absPath); err != nil {
		return warnings, err
	}
	if canSnapshot {
		written, _ := os.ReadFile(absPath)
		snap := polish.PolishSnapshot{
			Path:          rel,
			OriginalFull:  string(originalFull),
			PolishedBody:  newBody,
			Provider:      provider,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			PostWriteHash: polish.HashContent(written),
		}
		if err := polish.WriteSnapshot(v, snap); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to write undo snapshot: %v", err))
		}
	}
	return warnings, nil
}

// emitLinkEditResult renders a relink/unlink PolishResult as JSON or a
// human-readable preview/summary, mirroring repair-links' output shape.
func emitLinkEditResult(cmd *cobra.Command, result PolishResult, warnings []string, wrote bool, n int) error {
	result.Warning = strings.Join(warnings, "; ")
	if result.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", result.Warning)
	}

	if getFormat(cmd) == output.FormatJSON {
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if n == 0 {
		fmt.Printf("No matching link in %s.\n", result.Path)
		return nil
	}
	if !wrote {
		fmt.Fprintln(os.Stderr, "Preview only; re-run with --write to apply.")
	}
	return nil
}
