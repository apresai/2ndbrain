package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/spf13/cobra"
)

var unlinkCmd = &cobra.Command{
	Use:   "unlink <path>",
	Short: "Remove a broken [[wikilink]], keeping its visible text (no AI)",
	Long: `Strips the [[ ]] brackets around every link whose authored target equals
--target, keeping the visible text: [[083477d]] becomes 083477d, [[page|the
page]] becomes "the page", and [[note#Setup]] becomes note. This is the
"remove the link, keep the words" resolution for a broken wikilink that names no
real note (a stray id, an abbreviation, an external reference).

Matching is EXACT (case- and separator-sensitive), so unlink only touches the
specific broken link you named. Embeds (![[...]]) and links inside code are
never touched. By default it PREVIEWS; with --write it applies the change in
place and snapshots the original, reversible with "2nb polish <path> --undo".`,
	Args: cobra.ExactArgs(1),
	RunE: runUnlink,
}

var (
	unlinkTarget string
	unlinkWrite  bool
)

func init() {
	unlinkCmd.GroupID = "quality"
	unlinkCmd.Flags().StringVar(&unlinkTarget, "target", "", "The broken target to unlink (the TARGET from `broken wikilink: [[TARGET]]`), taken verbatim")
	unlinkCmd.Flags().BoolVar(&unlinkWrite, "write", false, "Apply the change in place (opt-in; default previews only) and snapshot the original for `polish --undo`")
	rootCmd.AddCommand(unlinkCmd)
}

func runUnlink(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(unlinkTarget) == "" {
		return exitWithError(ExitValidation, "error: unlink requires --target")
	}

	parsed, absPath, rel, v, err := openNoteForLinkEdit(args[0], unlinkWrite)
	if err != nil {
		return err
	}
	defer v.Close()

	start := time.Now()
	newBody, n := document.UnlinkWikiLink(parsed.Body, unlinkTarget)

	result := PolishResult{
		Path:       rel,
		Original:   parsed.Body,
		Polished:   newBody,
		Provider:   "unlink",
		DurationMs: time.Since(start).Milliseconds(),
	}
	var warnings []string
	if n > 0 {
		// new_target empty: the bracket-strip leaves plain text, no retarget.
		result.LinksRepaired = []polish.LinkRepair{{Raw: unlinkTarget}}
	} else {
		warnings = append(warnings, fmt.Sprintf("no [[%s]] link found to unlink", unlinkTarget))
	}

	if unlinkWrite && n > 0 {
		w, werr := writeBodyWithSnapshot(v, parsed, absPath, rel, newBody, "unlink")
		if werr != nil {
			return werr
		}
		warnings = append(warnings, w...)
		fmt.Fprintf(os.Stderr, "Unlinked %d link(s) in %s\n", n, rel)
	}

	return emitLinkEditResult(cmd, result, warnings, unlinkWrite, n)
}
