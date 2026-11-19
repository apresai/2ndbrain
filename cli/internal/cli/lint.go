package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/store"
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

	// The fields below are ADDITIVE classification for broken-wikilink issues
	// (all empty on every other issue kind). Message stays byte-identical to the
	// pre-classification format because the Obsidian plugin and older app builds
	// parse it.

	// Target is the raw authored target of a broken wikilink (the T from
	// "broken wikilink: [[T]]"), so consumers never re-parse Message.
	Target string `json:"target,omitempty"`
	// Fix classifies how a broken wikilink can be fixed, using the same
	// polish.RepairIndex the repair tools resolve against:
	//   "drift"     - the target maps to exactly one existing note (repairable
	//                 one-click via repair-links)
	//   "ambiguous" - the target maps to more than one note (needs a pick)
	//   "missing"   - the target maps to no note (including path-qualified
	//                 targets, which the repair index refuses by design)
	Fix string `json:"fix,omitempty"`
	// DriftTarget is the canonical target the link would be repaired to when
	// Fix == "drift", so a UI can render [[x]] -> [[y]] without a preview
	// round-trip.
	DriftTarget string `json:"drift_target,omitempty"`
	// Candidates carries the repair index's matches when Fix == "ambiguous"
	// (each a canonical target usable as relink --to), so a UI can offer the
	// pick list without a suggest-target round-trip. Empty for other Fix
	// classes: drift's single match is DriftTarget, and missing has none.
	Candidates []string `json:"candidates,omitempty"`

	// Additive metadata fields for structured lint consumers (all omitempty so
	// existing JSON contracts remain unchanged).
	Kind  string `json:"kind,omitempty"`  // parse_error | missing_required | schema_enum | date_format | date_invalid | link_syntax | broken_link
	Field string `json:"field,omitempty"` // frontmatter field name when applicable
}

type LintReport struct {
	Issues []LintIssue `json:"issues"` // always [] in JSON, never null; initialized non-nil in buildLintReport
	Files  int         `json:"files_checked"`
	Errors int         `json:"errors"`
	Warns  int         `json:"warnings"`
}

func runLint(cmd *cobra.Command, args []string) error {
	if err := refuseBodylessFormat(cmd, "lint"); err != nil {
		return err
	}

	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	report, err := buildLintReport(v, args)
	if err != nil {
		return err
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

func buildLintReport(v *vault.Vault, args []string) (*LintReport, error) {
	startTime := time.Now()

	lintPattern := "**/*.md (recursive)"
	if len(args) > 0 {
		lintPattern = args[0]
	}
	slog.Info("lint started", "vault", v.Root, "pattern", lintPattern)

	var matches []string
	var err error
	if len(args) > 0 {
		matches, err = filepath.Glob(filepath.Join(v.Root, args[0]))
		if err != nil {
			return nil, fmt.Errorf("glob: %w", err)
		}
	} else {
		if werr := filepath.Walk(v.Root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				base := filepath.Base(path)
				// Skip dot-directories (.git, .obsidian, .2ndbrain, .trash, ...) and node_modules.
				if path != v.Root && (strings.HasPrefix(base, ".") || base == "node_modules") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(strings.ToLower(path), ".md") {
				matches = append(matches, path)
			}
			return nil
		}); werr != nil {
			return nil, fmt.Errorf("walk: %w", werr)
		}
	}
	sort.Strings(matches)

	// Issues starts non-nil: an issue-free vault must marshal as `"issues": []`,
	// not `null`, or `jq '.issues[]'` fails on the good case.
	report := &LintReport{Issues: []LintIssue{}}

	docs, aliasIndex, werr := vault.CollectLiveDocs(v.Root)
	if werr != nil {
		return nil, fmt.Errorf("walk for resolver index: %w", werr)
	}
	resolver := store.NewResolver(docs, aliasIndex)
	repairIdx := polish.NewRepairIndex(docs, aliasIndex)

	excludedFolders := vault.ObsidianTemplateFolders(v.Root)

	for _, path := range matches {
		relPath := v.RelPath(path)
		if vault.IsIgnored(relPath) {
			continue
		}
		if folder, ok := vault.ExcludedFolderFor(relPath, excludedFolders); ok {
			slog.Debug("lint skipping a note in an Obsidian template folder", "path", relPath, "folder", folder)
			continue
		}

		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			report.Files++
			report.Issues = append(report.Issues, LintIssue{
				Path: relPath, Level: "error", Message: fmt.Sprintf("parse error: %v", rerr), Kind: "parse_error",
			})
			report.Errors++
			continue
		}
		if vault.HasTemplatePlaceholders(raw) {
			slog.Debug("lint skipping template (unresolved {{placeholders}} in frontmatter)", "path", relPath)
			continue
		}

		doc, perr := document.Parse(path, raw)
		if perr != nil {
			report.Files++
			report.Issues = append(report.Issues, LintIssue{
				Path: relPath, Level: "error", Message: fmt.Sprintf("parse error: %v", perr), Kind: "parse_error",
			})
			report.Errors++
			continue
		}
		if doc.Frontmatter == nil && document.HasUnclosedFrontmatter(raw) {
			report.Files++
			report.Issues = append(report.Issues, LintIssue{
				Path: relPath, Level: "error", Message: "parse error: unclosed YAML frontmatter fence", Kind: "parse_error",
			})
			report.Errors++
			continue
		}
		report.Files++

		// Note: no 'id' check. Under the path-based identity model
		// (docs/obsidian/identity-model.md) a document's identity is its path;
		// frontmatter 'id' is read if present but never required.

		// Check: required fields and enum validation across all schema types.
		if v.Schemas != nil {
			if schema, ok := v.Schemas.Types[doc.Type]; ok {
				for _, field := range schema.Required {
					if lintFieldMissing(doc, v.Schemas, schema, field) {
						report.Issues = append(report.Issues, LintIssue{
							Path:    relPath,
							Level:   "error",
							Message: fmt.Sprintf("missing required field '%s' for type '%s'", field, doc.Type),
							Kind:    "missing_required",
							Field:   field,
						})
						report.Errors++
					}
				}

				fieldNames := make([]string, 0, len(schema.Fields))
				for fieldName := range schema.Fields {
					fieldNames = append(fieldNames, fieldName)
				}
				sort.Strings(fieldNames)

				for _, fieldName := range fieldNames {
					fieldDef := schema.Fields[fieldName]
					val, exists := doc.Frontmatter[fieldName]
					if !exists || val == nil || len(fieldDef.Enum) == 0 {
						continue
					}
					if fieldName != "status" && v.Schemas.IsListField(doc.Type, fieldName) {
						elems, ok := lintListElements(doc, fieldName, val)
						if !ok {
							report.Issues = append(report.Issues, LintIssue{
								Path:    relPath,
								Level:   "error",
								Message: fmt.Sprintf("field '%s' expects scalar values in %v", fieldName, fieldDef.Enum),
								Kind:    "schema_enum",
								Field:   fieldName,
							})
							report.Errors++
							continue
						}
						for _, elem := range elems {
							if !stringInSlice(elem, fieldDef.Enum) {
								report.Issues = append(report.Issues, LintIssue{
									Path:    relPath,
									Level:   "error",
									Message: fmt.Sprintf("field '%s' value '%s' not in %v", fieldName, elem, fieldDef.Enum),
									Kind:    "schema_enum",
									Field:   fieldName,
								})
								report.Errors++
							}
						}
						continue
					}

					strVal, ok := lintScalarText(doc, fieldName, val)
					if !ok {
						report.Issues = append(report.Issues, LintIssue{
							Path:    relPath,
							Level:   "error",
							Message: fmt.Sprintf("field '%s' expects a scalar string in %v", fieldName, fieldDef.Enum),
							Kind:    "schema_enum",
							Field:   fieldName,
						})
						report.Errors++
						continue
					}
					if !stringInSlice(strVal, fieldDef.Enum) {
						report.Issues = append(report.Issues, LintIssue{
							Path:    relPath,
							Level:   "error",
							Message: fmt.Sprintf("field '%s' value '%s' not in %v", fieldName, strVal, fieldDef.Enum),
							Kind:    "schema_enum",
							Field:   fieldName,
						})
						report.Errors++
					}
				}
			}

			// Check: date fields (`created`, `modified`, schema `date`/`datetime`).
			fmKeys := make([]string, 0, len(doc.Frontmatter))
			for k := range doc.Frontmatter {
				fmKeys = append(fmKeys, k)
			}
			sort.Strings(fmKeys)
			for _, key := range fmKeys {
				if !v.Schemas.IsDateField(doc.Type, key) {
					continue
				}
				val := doc.Frontmatter[key]
				switch t := val.(type) {
				case nil:
					continue
				case time.Time:
					if t.Nanosecond() != 0 {
						report.Issues = append(report.Issues, LintIssue{
							Path:    relPath,
							Level:   "warning",
							Message: fmt.Sprintf("date field '%s' has sub-second precision (run `2nb fix` or `2nb obsidian migrate-properties`)", key),
							Kind:    "date_format",
							Field:   key,
						})
						report.Warns++
					}
				case string:
					if strings.TrimSpace(t) == "" {
						continue
					}
					if _, ok := document.ParseFrontmatterDateText(t); ok {
						report.Issues = append(report.Issues, LintIssue{
							Path:    relPath,
							Level:   "warning",
							Message: fmt.Sprintf("date field '%s' is quoted or unnormalized %q (run `2nb fix` or `2nb obsidian migrate-properties`)", key, t),
							Kind:    "date_format",
							Field:   key,
						})
						report.Warns++
					} else {
						report.Issues = append(report.Issues, LintIssue{
							Path:    relPath,
							Level:   "error",
							Message: fmt.Sprintf("invalid date value %q for field '%s'", t, key),
							Kind:    "date_invalid",
							Field:   key,
						})
						report.Errors++
					}
				default:
					report.Issues = append(report.Issues, LintIssue{
						Path:    relPath,
						Level:   "error",
						Message: fmt.Sprintf("field '%s' holds %T, expected date", key, val),
						Kind:    "date_invalid",
						Field:   key,
					})
					report.Errors++
				}
			}
		}

		// Check: link syntax formatting (matches polish.FixVault NormalizeLinkFormatting).
		_, fmtChanges := document.NormalizeLinkFormatting(doc.Body, func(target string) bool {
			_, rerr := resolver.Resolve(target)
			return rerr == nil
		})
		for _, fc := range fmtChanges {
			report.Issues = append(report.Issues, LintIssue{
				Path:    relPath,
				Level:   "warning",
				Message: fmt.Sprintf("link formatting: %s -> %s", fc.From, fc.To),
				Kind:    "link_syntax",
			})
			report.Warns++
		}

		// Check: broken wikilinks.
		links := document.ExtractWikiLinks(doc.Body)
		for _, link := range links {
			target := link.Target
			if isAssetOrAnchorTarget(target) {
				continue
			}
			if _, rerr := resolver.Resolve(target); errors.Is(rerr, store.ErrTargetNotFound) {
				issue := LintIssue{
					Path:    relPath,
					Level:   "warning",
					Message: fmt.Sprintf("broken wikilink: [[%s]]", target),
					Target:  target,
					Kind:    "broken_link",
				}
				switch candidates := repairIdx.Lookup(target); len(candidates) {
				case 1:
					issue.Fix = "drift"
					issue.DriftTarget = candidates[0]
				case 0:
					issue.Fix = "missing"
				default:
					issue.Fix = "ambiguous"
					issue.Candidates = candidates
				}
				report.Issues = append(report.Issues, issue)
				report.Warns++
			}
		}
	}

	slog.Info("lint complete",
		"files", report.Files,
		"errors", report.Errors,
		"warnings", report.Warns,
		"resolver_docs", len(docs),
		"resolver_aliases", len(aliasIndex),
		"elapsed", time.Since(startTime),
	)

	return report, nil
}

func lintScalarText(doc *document.Document, field string, val any) (string, bool) {
	if s, ok := doc.MetaText(field); ok {
		return s, true
	}
	return document.ScalarText(val)
}

func lintListElements(doc *document.Document, field string, val any) ([]string, bool) {
	switch lv := val.(type) {
	case []any:
		out := make([]string, 0, len(lv))
		for i, item := range lv {
			if s, ok := doc.MetaTextItem(field, i); ok {
				out = append(out, s)
				continue
			}
			s, ok := document.ScalarText(item)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		return lv, true
	default:
		if s, ok := lintScalarText(doc, field, val); ok {
			return []string{s}, true
		}
		return nil, false
	}
}

func lintFieldMissing(doc *document.Document, schemas *vault.SchemaSet, schema vault.DocTypeSchema, field string) bool {
	val, exists := doc.Frontmatter[field]
	if !exists || val == nil {
		return true
	}
	if field != "status" && schemas != nil && schemas.IsListField(doc.Type, field) {
		switch lv := val.(type) {
		case []any:
			return len(lv) == 0
		case []string:
			return len(lv) == 0
		default:
			s, ok := lintScalarText(doc, field, val)
			return !ok || strings.TrimSpace(s) == ""
		}
	}
	str, ok := lintScalarText(doc, field, val)
	if !ok {
		// Non-scalar on a non-enum required scalar field is treated as missing;
		// enum fields emit a specific non-scalar enum error instead.
		return len(schema.Fields[field].Enum) == 0
	}
	return strings.TrimSpace(str) == ""
}

func stringInSlice(s string, list []string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
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
