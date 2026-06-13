package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

// daily resolves "today's daily note" from Obsidian's own core daily-notes
// plugin config (.obsidian/daily-notes.json) and lets the user resolve+create,
// read, or append to it headlessly. The vault's Obsidian config is the source
// of truth for folder + filename format, so the path 2nb resolves matches the
// note Obsidian itself would open.

var (
	dailyAppendText string
	dailyAppendFile string
)

var dailyCmd = &cobra.Command{
	Use:   "daily",
	Short: "Resolve, create, read, or append to today's daily note",
	Long: `Resolve today's daily note from Obsidian's core daily-notes plugin config
(.obsidian/daily-notes.json: folder, format, optional template). Invoked bare,
2nb daily resolves today's path, creates the note if it does not exist, and
prints the path. Subcommands read or append to it.

When the daily-notes plugin is disabled or never configured, Obsidian's
defaults apply (folder = vault root, filename format = YYYY-MM-DD), so daily
never hard-errors on a missing config.`,
	Example: `  2nb daily                          # resolve + create today's note, print path
  2nb daily read                     # print today's note body
  2nb daily append --text "- did X"  # append a bullet to today's note
  echo "- via pipe" | 2nb daily append`,
	Args: cobra.NoArgs,
	// Default action when invoked without a subcommand: resolve + create + print.
	RunE: runDailyResolve,
}

var dailyReadCmd = &cobra.Command{
	Use:   "read",
	Short: "Print today's daily note body",
	Args:  cobra.NoArgs,
	RunE:  runDailyRead,
}

var dailyAppendCmd = &cobra.Command{
	Use:   "append",
	Short: "Append content to today's daily note body",
	Long: `Append content to the end of today's daily note body, creating the note
first if it does not exist. The frontmatter is left untouched.

Content comes from --text, --file, or stdin (in that order of precedence).
This is an explicit, opt-in body write: 2nb otherwise never rewrites note bodies.`,
	Example: `  2nb daily append --text "- 14:00 standup notes"
  2nb daily append --file snippet.md
  echo "- via pipe" | 2nb daily append`,
	Args: cobra.NoArgs,
	RunE: runDailyAppend,
}

func init() {
	dailyCmd.GroupID = "docs"
	dailyAppendCmd.Flags().StringVar(&dailyAppendText, "text", "", "Content to append (inline string)")
	dailyAppendCmd.Flags().StringVar(&dailyAppendFile, "file", "", "Read content to append from this file")
	dailyCmd.AddCommand(dailyReadCmd)
	dailyCmd.AddCommand(dailyAppendCmd)
	rootCmd.AddCommand(dailyCmd)
}

// runDailyResolve resolves today's daily note path, creates the note if it is
// missing, and prints the path. This is the bare `2nb daily` action.
func runDailyResolve(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	relPath, absPath, created, err := ensureDailyNote(v, time.Now())
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, map[string]any{
			"path":    relPath,
			"created": created,
		})
	}

	fmt.Println(relPath)
	if created && !flagPorcelain {
		fmt.Fprintf(os.Stderr, "  Created today's daily note.\n")
		fmt.Fprintf(os.Stderr, "  Edit: open %s\n", absPath)
	}
	return nil
}

// runDailyRead resolves today's daily note and prints its body. If the note
// does not exist it is created first (so `daily read` always prints something
// for today), matching the bare resolve action's create-on-demand behavior.
func runDailyRead(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	relPath, absPath, _, err := ensureDailyNote(v, time.Now())
	if err != nil {
		return err
	}

	doc, err := document.ParseFile(absPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}
	doc.Path = relPath

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, doc)
}

// runDailyAppend resolves today's daily note (creating it if missing) and
// appends content to its body via the shared writeBody helper.
func runDailyAppend(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	_, absPath, _, err := ensureDailyNote(v, time.Now())
	if err != nil {
		return err
	}

	doc, err := document.ParseFile(absPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}
	doc.Path = v.RelPath(absPath)

	content, err := readWriteContent(dailyAppendText, dailyAppendFile, cmd.Flags().Changed("text"))
	if err != nil {
		return err
	}

	doc.Body = doc.Body + "\n" + content

	if err := writeBody(v, doc, absPath); err != nil {
		return err
	}

	return reportBodyWrite(cmd, doc, "daily-append")
}

// ensureDailyNote resolves today's daily note from the vault's Obsidian
// daily-notes config and creates it if it does not already exist. It returns
// the vault-relative path, the absolute path, and whether a new note was
// created. The note is created via the same create path the `create` command
// uses (NewDocument with type note + the configured template body), so it is
// indexed and embedded like any other note.
func ensureDailyNote(v *vault.Vault, t time.Time) (relPath, absPath string, created bool, err error) {
	relPath, err = vault.DailyNotePath(v, t)
	if err != nil {
		return "", "", false, err
	}
	absPath = v.AbsPath(relPath)

	// Guard against a config (folder/format) that escapes the vault.
	if !v.ContainsPath(absPath) {
		return "", "", false, fmt.Errorf("daily note path escapes the vault: %q", relPath)
	}

	if _, statErr := os.Stat(absPath); statErr == nil {
		// Already exists: nothing to create.
		return relPath, absPath, false, nil
	} else if !os.IsNotExist(statErr) {
		return "", "", false, fmt.Errorf("stat daily note: %w", statErr)
	}

	// Create it. The title is the date-stem of the resolved filename so the
	// note reads naturally (e.g. "2026-06-13").
	title := strings.TrimSuffix(filepath.Base(relPath), ".md")

	// Body: an optional configured template, otherwise the built-in note
	// template with placeholders filled. The note type is always "note".
	body := dailyTemplateBody(v, title)

	doc := document.NewDocument(title, "note", body)
	doc.SetMeta("status", "draft")
	doc.Path = absPath
	doc.ComputeContentHash()

	// Ensure the parent directory exists (the configured folder may be nested).
	if mkErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkErr != nil {
		return "", "", false, fmt.Errorf("create daily note directory: %w", mkErr)
	}

	written, werr := doc.WriteFile(filepath.Dir(absPath))
	if werr != nil {
		return "", "", false, fmt.Errorf("write daily note: %w", werr)
	}
	absPath = written
	doc.Path = v.RelPath(absPath)
	relPath = doc.Path

	if idxErr := vault.IndexSingleFile(v, absPath); idxErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index daily note: %v\n", idxErr)
		slog.Warn("failed to index daily note", "err", idxErr)
	}
	embedNewDocument(v, doc)

	slog.Info("daily note created", "path", relPath)
	return relPath, absPath, true, nil
}

// dailyTemplateBody returns the body for a freshly created daily note. When the
// daily-notes config names a template path that exists in the vault, its body
// is used verbatim (Obsidian-style placeholders are left as-is, since 2nb does
// not interpret Templater/core-template syntax). Otherwise the built-in note
// template is used with {{.Title}}/{{.Status}} placeholders filled.
func dailyTemplateBody(v *vault.Vault, title string) string {
	cfg, err := vault.LoadDailyNotesConfig(v)
	if err == nil && strings.TrimSpace(cfg.Template) != "" {
		tmplRel := strings.TrimSuffix(cfg.Template, ".md") + ".md"
		tmplAbs := v.AbsPath(filepath.FromSlash(tmplRel))
		if v.ContainsPath(tmplAbs) {
			if tdoc, perr := document.ParseFile(tmplAbs); perr == nil {
				return tdoc.Body
			}
		}
	}

	body := vault.GetTemplate("note")
	body = strings.ReplaceAll(body, "{{.Title}}", title)
	body = strings.ReplaceAll(body, "{{.Status}}", "draft")
	return body
}
