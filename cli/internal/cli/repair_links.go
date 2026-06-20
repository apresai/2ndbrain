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
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var repairLinksCmd = &cobra.Command{
	Use:   "repair-links <path>",
	Short: "Deterministically repair broken [[wikilinks]] to existing notes (no AI)",
	Long: `Repairs broken [[wikilinks]] in a note by canonicalizing each broken target
to the existing note it unambiguously names (the common case being case or
whitespace drift, since 2nb's resolver is case-sensitive but Obsidian's is not).

This is the deterministic, AI-free sibling of "polish --repair-links": it runs
NO generation provider, so it works offline and never touches your prose — only
the broken links. A broken target is rewritten only when its normalized name
maps to exactly one note; ambiguous or unmatched targets are reported and left
untouched (never guessed). Asset embeds and code are never touched.

By default it PREVIEWS (prints the proposed result without writing). With
--write it applies the repairs in place and snapshots the original, so the
change is reversible with "2nb polish <path> --undo".`,
	Args: cobra.ExactArgs(1),
	RunE: runRepairLinks,
}

var repairLinksWrite bool
var repairLinksTargets []string

func init() {
	repairLinksCmd.GroupID = "quality"
	repairLinksCmd.Flags().BoolVar(&repairLinksWrite, "write", false, "Apply the repairs in place (opt-in; default previews only) and snapshot the original for `polish --undo`")
	// StringArray (not StringSlice) so a target value is taken VERBATIM — a
	// StringSlice would comma-split, mangling a legitimate target like
	// [[Smith, John]] into two unrelated names and repairing the wrong links.
	repairLinksCmd.Flags().StringArrayVar(&repairLinksTargets, "target", nil, "Repair only links whose authored target equals this (repeatable). The value is the TARGET from `broken wikilink: [[TARGET]]`, taken verbatim (not comma-split). Omitted = repair every confident broken link")
	rootCmd.AddCommand(repairLinksCmd)
}

func runRepairLinks(cmd *cobra.Command, args []string) error {
	// --write mutates the document on disk, so it pins the active vault (matching
	// polish/meta/append). The default preview path is read-only.
	var v *vault.Vault
	var err error
	if repairLinksWrite {
		v, err = openVaultAndSetActive()
	} else {
		v, err = openVault()
	}
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	absPath, rel, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("resolve doc path: %w", err)
	}

	parsed, err := document.ParseFile(absPath)
	if err != nil {
		return fmt.Errorf("parse source: %w", err)
	}

	// Synthetic .canvas/.base bodies have no editable wikilinks; reject up front
	// (writeBody would refuse anyway) so the message is clear even in preview.
	if document.IsReadOnlyType(parsed.Type) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: cannot repair links in a read-only %s file (%s); .canvas/.base files are indexed read-only", parsed.Type, rel))
	}

	originalBody := parsed.Body
	start := time.Now()
	rr, err := polish.RepairBrokenLinksFiltered(v, originalBody, repairLinksTargets)
	if err != nil {
		return fmt.Errorf("repair links: %w", err)
	}

	var warnings []string
	if len(rr.Skipped) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d broken link(s) left unrepaired (no confident target)", len(rr.Skipped)))
	}

	result := PolishResult{
		Path:          rel,
		Original:      originalBody,
		Polished:      rr.Body,
		Provider:      "repair-links",
		Model:         "",
		DurationMs:    time.Since(start).Milliseconds(),
		LinksRepaired: rr.Repaired,
		LinksSkipped:  rr.Skipped,
	}

	// Only write when something actually changed: a no-op write would churn the
	// file and overwrite any prior polish snapshot (whose undo would then be
	// lost) for nothing.
	if repairLinksWrite && len(rr.Repaired) > 0 {
		originalFull, readErr := os.ReadFile(absPath)
		canSnapshot := readErr == nil && len(originalFull) > 0
		if !canSnapshot {
			warnings = append(warnings, "could not snapshot the original; undo will be unavailable for this repair")
		}
		parsed.Body = rr.Body
		parsed.Path = rel
		if err := writeBody(v, parsed, absPath); err != nil {
			return err
		}
		if canSnapshot {
			written, _ := os.ReadFile(absPath)
			snap := polish.PolishSnapshot{
				Path:          rel,
				OriginalFull:  string(originalFull),
				PolishedBody:  rr.Body,
				Provider:      "repair-links",
				Model:         "",
				Links:         false,
				Timestamp:     time.Now().UTC().Format(time.RFC3339),
				PostWriteHash: polish.HashContent(written),
			}
			if err := polish.WriteSnapshot(v, snap); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to write undo snapshot: %v", err))
			}
		}
		fmt.Fprintf(os.Stderr, "Repaired %d link(s) in %s\n", len(rr.Repaired), rel)
	}

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

	// Human-readable preview/summary.
	if len(rr.Repaired) == 0 && len(rr.Skipped) == 0 {
		fmt.Printf("No broken links to repair in %s.\n", rel)
		return nil
	}
	for _, r := range rr.Repaired {
		fmt.Printf("Repaired link: [[%s]] -> [[%s]]\n", r.Raw, r.NewTarget)
	}
	for _, s := range rr.Skipped {
		fmt.Printf("Skipped [[%s]] (%s)\n", s.Raw, s.Reason)
	}
	if !repairLinksWrite && len(rr.Repaired) > 0 {
		fmt.Fprintln(os.Stderr, "Preview only; re-run with --write to apply.")
	}
	return nil
}
