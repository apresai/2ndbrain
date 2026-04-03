package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	v, err := vault.Open(".")
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	pattern := "*.md"
	if len(args) > 0 {
		pattern = args[0]
	}

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

	// First pass: collect all known doc titles/filenames for link resolution
	filepath.Walk(v.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".md") {
			rel := v.RelPath(path)
			base := strings.TrimSuffix(filepath.Base(rel), ".md")
			allDocs[base] = true
			allDocs[rel] = true
			allDocs[strings.TrimSuffix(rel, ".md")] = true
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

		// Check: broken wikilinks
		links := document.ExtractWikiLinks(doc.Body)
		for _, link := range links {
			target := link.Target
			if !allDocs[target] {
				report.Issues = append(report.Issues, LintIssue{
					Path: relPath, Level: "warning",
					Message: fmt.Sprintf("broken wikilink: [[%s]]", target),
				})
				report.Warns++
			}
		}
	}

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
