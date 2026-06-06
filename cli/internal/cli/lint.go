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

var lintCmd = &cobra.Command{
	Use:   "lint [glob]",
	Short: "Validate frontmatter schemas and check for broken wikilinks",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runLint,
}

func init() {
	lintCmd.GroupID = "quality"
	rootCmd.AddCommand(lintCmd)
}

type LintIssue struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Level   string `json:"level"` // error, warning
	Message string `json:"message"`
}

type LintReport struct {
	Issues []LintIssue `json:"issues"`
	Files  int         `json:"files_checked"`
	Errors int         `json:"errors"`
	Warns  int         `json:"warnings"`
}

func runLint(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	startTime := time.Now()
	pattern := "*.md"
	if len(args) > 0 {
		pattern = args[0]
	}
	slog.Info("lint started", "vault", v.Root, "pattern", pattern)

	matches, err := filepath.Glob(filepath.Join(v.Root, pattern))
	if err != nil {
		return fmt.Errorf("glob: %w", err)
	}

	// Also try recursive if no matches
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(v.Root, "**", pattern))
	}

	report := &LintReport{}
	allDocs := make(map[string]bool)

	// First pass: collect all known doc filenames for link resolution. Markdown
	// plus the Obsidian file types 2nb now indexes (.canvas/.base) so links to
	// them aren't reported as broken.
	filepath.Walk(v.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		lower := strings.ToLower(path)
		for _, ext := range []string{".md", ".canvas", ".base"} {
			if strings.HasSuffix(lower, ext) {
				rel := v.RelPath(path)
				allDocs[strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))] = true // basename, no ext
				allDocs[filepath.Base(rel)] = true                                        // basename, with ext
				allDocs[rel] = true                                                       // rel path, with ext
				allDocs[strings.TrimSuffix(rel, ext)] = true                              // rel path, no ext
				break
			}
		}
		return nil
	})

	for _, path := range matches {
		relPath := v.RelPath(path)
		if vault.IsIgnored(relPath) {
			continue
		}
		report.Files++

		doc, err := document.ParseFile(path)
		if err != nil {
			report.Issues = append(report.Issues, LintIssue{
				Path: relPath, Level: "error", Message: fmt.Sprintf("parse error: %v", err),
			})
			report.Errors++
			continue
		}

		// Check: document has an ID
		if doc.ID == "" {
			report.Issues = append(report.Issues, LintIssue{
				Path: relPath, Level: "error", Message: "missing 'id' in frontmatter",
			})
			report.Errors++
		}

		// Check: required fields
		if schema, ok := v.Schemas.Types[doc.Type]; ok {
			for _, field := range schema.Required {
				if _, exists := doc.Frontmatter[field]; !exists {
					report.Issues = append(report.Issues, LintIssue{
						Path: relPath, Level: "error",
						Message: fmt.Sprintf("missing required field '%s' for type '%s'", field, doc.Type),
					})
					report.Errors++
				}
			}

			// Check: enum validation
			for fieldName, fieldDef := range schema.Fields {
				val, exists := doc.Frontmatter[fieldName]
				if !exists {
					continue
				}
				if len(fieldDef.Enum) > 0 {
					strVal, ok := val.(string)
					if !ok {
						continue
					}
					found := false
					for _, e := range fieldDef.Enum {
						if e == strVal {
							found = true
							break
						}
					}
					if !found {
						report.Issues = append(report.Issues, LintIssue{
							Path: relPath, Level: "error",
							Message: fmt.Sprintf("field '%s' value '%s' not in %v", fieldName, strVal, fieldDef.Enum),
						})
						report.Errors++
					}
				}
			}
		}

		// Check: broken wikilinks. Skip anchor-only links ([x](#section), empty
		// target) and embedded assets ([alt](img.png), ![[img.png]]) — Obsidian
		// vaults are image- and anchor-heavy, and treating those as broken notes
		// makes lint noisy to useless.
		links := document.ExtractWikiLinks(doc.Body)
		for _, link := range links {
			target := link.Target
			if isAssetOrAnchorTarget(target) {
				continue
			}
			if !allDocs[target] {
				report.Issues = append(report.Issues, LintIssue{
					Path: relPath, Level: "warning",
					Message: fmt.Sprintf("broken wikilink: [[%s]]", target),
				})
				report.Warns++
			}
		}
	}

	slog.Info("lint complete",
		"files", report.Files,
		"errors", report.Errors,
		"warnings", report.Warns,
		"elapsed", time.Since(startTime),
	)

	if !flagPorcelain && report.Errors+report.Warns > 0 {
		fmt.Fprintf(os.Stderr, "%d files checked, %d errors, %d warnings\n",
			report.Files, report.Errors, report.Warns)
	}

	format := getFormat(cmd)
	if err := output.Write(os.Stdout, format, report); err != nil {
		return err
	}

	if report.Errors > 0 {
		os.Exit(ExitValidation)
	}
	return nil
}

// isAssetOrAnchorTarget reports whether a link target should be excluded from
// the broken-wikilink check. An empty target is an anchor-only / same-document
// link ([x](#section)). A target with a non-note extension is an embedded asset
// (image, pdf, audio, ...) — only .md/.canvas/.base are resolvable notes that
// warrant a broken-link warning.
func isAssetOrAnchorTarget(target string) bool {
	if target == "" {
		return true
	}
	switch strings.ToLower(filepath.Ext(target)) {
	case "":
		return false // bare note reference like [[note]]
	case ".md", ".canvas", ".base":
		return false // resolvable note types — do check these
	default:
		return true // asset (png/jpg/pdf/...) — skip
	}
}
