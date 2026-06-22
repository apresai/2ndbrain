package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
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

	lintPattern := "**/*.md (recursive)"
	if len(args) > 0 {
		lintPattern = args[0]
	}
	slog.Info("lint started", "vault", v.Root, "pattern", lintPattern)

	// Collect the markdown files to lint. An explicit glob argument is honoured
	// verbatim (relative to the vault root). With no argument we walk the whole
	// vault recursively: filepath.Glob does not support "**", so the old "*.md"
	// pattern silently linted only top-level files and skipped every note in a
	// subdirectory.
	var matches []string
	if len(args) > 0 {
		matches, err = filepath.Glob(filepath.Join(v.Root, args[0]))
		if err != nil {
			return fmt.Errorf("glob: %w", err)
		}
	} else {
		if werr := filepath.Walk(v.Root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				// Skip dot-directories (.git, .obsidian, .2ndbrain, .trash, ...).
				// IsIgnored only inspects basenames, so it can't prune subtrees.
				if path != v.Root && strings.HasPrefix(filepath.Base(path), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(strings.ToLower(path), ".md") {
				matches = append(matches, path)
			}
			return nil
		}); werr != nil {
			return fmt.Errorf("walk: %w", werr)
		}
	}

	report := &LintReport{}

	// First pass: build the SAME tiered lookup the rest of 2nb uses
	// (store.NewResolver -> the canonical buildLookupIndex), sourced from the live
	// filesystem so lint never depends on a fresh index.db. For each note we
	// register its vault-relative path and, by parsing it, its frontmatter title
	// and aliases — exactly the inputs the indexer feeds the DB — so a link that
	// resolves only by title or alias resolves here just as `2nb links` resolves
	// it via the DB. Resolving by filename alone (the old behavior) falsely
	// flagged title/alias-only links as broken. The walk mirrors the indexer's
	// prune rules (dot-directories, node_modules, IsIgnored files; see
	// internal/vault/indexer.go) so the resolver's document set EQUALS what the
	// indexer puts in the DB — otherwise lint could resolve a link to a
	// trashed/.obsidian note that `2nb links` reports broken, and it would
	// needlessly ParseFile thousands of plugin node_modules files. Parsing is
	// best-effort: a file that fails to parse (e.g. an Obsidian {{placeholder}}
	// template) is still registered by path, so links to it resolve; only its
	// title/aliases are skipped.
	var docs []store.DocInfo
	aliasIndex := make(map[string][]string)
	if werr := filepath.Walk(v.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != v.Root {
				base := filepath.Base(path)
				if strings.HasPrefix(base, ".") || base == "node_modules" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		lower := strings.ToLower(path)
		isMD := strings.HasSuffix(lower, ".md")
		isCanvasOrBase := strings.HasSuffix(lower, ".canvas") || strings.HasSuffix(lower, ".base")
		if !isMD && !isCanvasOrBase {
			return nil
		}
		rel := v.RelPath(path)
		if vault.IsIgnored(rel) {
			return nil
		}
		// The canonical index strips only the .md extension. Register the
		// extension-stripped basename AND rel-path of .canvas/.base files as
		// aliases so both a bare [[board]] and a path-qualified [[sub/board]]
		// still resolve to board.canvas (as the old lint and Obsidian both do);
		// [[board.canvas]] with the extension resolves via the path tier.
		if isCanvasOrBase {
			base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
			aliasIndex[base] = append(aliasIndex[base], rel)
			if relNoExt := strings.TrimSuffix(rel, filepath.Ext(rel)); relNoExt != base {
				aliasIndex[relNoExt] = append(aliasIndex[relNoExt], rel)
			}
		}
		title := ""
		if isMD {
			if d, perr := document.ParseFile(path); perr == nil {
				title = d.Title
				for _, a := range document.ExtractAliases(d.Frontmatter) {
					aliasIndex[a] = append(aliasIndex[a], rel)
				}
			} else {
				// The note still contributes its path, but its title/aliases are
				// unavailable, so a link resolvable only by them may be flagged.
				// Leave a breadcrumb (mirrors the template-skip debug below).
				slog.Debug("lint resolver: skipping unparseable note (title/aliases unavailable)", "path", rel, "err", perr)
			}
		}
		docs = append(docs, store.DocInfo{ID: rel, Path: rel, Title: title}) // path is the unique id
		return nil
	}); werr != nil {
		return fmt.Errorf("walk for resolver index: %w", werr)
	}
	resolver := store.NewResolver(docs, aliasIndex)

	for _, path := range matches {
		relPath := v.RelPath(path)
		if vault.IsIgnored(relPath) {
			continue
		}

		doc, err := document.ParseFile(path)
		if err != nil {
			// Obsidian template files carry unresolved {{placeholder}} tokens in
			// their frontmatter (e.g. `date: {{date}}`) that are deliberately
			// invalid YAML. They are scaffolding, not notes — the indexer skips
			// them too — so a parse failure there is not a real lint error.
			if raw, rerr := os.ReadFile(path); rerr == nil && hasTemplatePlaceholders(raw) {
				slog.Debug("lint skipping template (unresolved {{placeholders}} in frontmatter)", "path", relPath)
				continue
			}
			report.Files++
			report.Issues = append(report.Issues, LintIssue{
				Path: relPath, Level: "error", Message: fmt.Sprintf("parse error: %v", err),
			})
			report.Errors++
			continue
		}
		report.Files++

		// Note: no 'id' check. Under the path-based identity model
		// (docs/obsidian/identity-model.md) a document's identity is its path;
		// frontmatter 'id' is read if present but never required, so a missing
		// id is not a lint error. Vanilla Obsidian notes carry no id at all.

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
			// Canonical tiered resolution (path -> shortest-unique suffix ->
			// title -> alias). ONLY ErrTargetNotFound is broken: a clean resolve
			// is fine, and an *AmbiguousTargetError means the target DOES match
			// (>1 doc) — not broken. This is what makes lint agree with
			// `2nb links` and the per-finding fix tools.
			if _, rerr := resolver.Resolve(target); errors.Is(rerr, store.ErrTargetNotFound) {
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
		"resolver_docs", len(docs),
		"resolver_aliases", len(aliasIndex),
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

// hasTemplatePlaceholders reports whether a file's YAML frontmatter contains
// unresolved {{...}} template tokens (Obsidian core Templates / Templater).
// Such files are scaffolding, not notes; their frontmatter is deliberately not
// valid YAML, so lint skips them rather than reporting a false-positive parse
// error. Only the frontmatter block is inspected so a body that merely mentions
// {{ }} (e.g. a note about templating) is never mistaken for a template.
func hasTemplatePlaceholders(raw []byte) bool {
	s := string(raw)
	if !strings.HasPrefix(s, "---") {
		return false
	}
	rest := s[3:]
	if end := strings.Index(rest, "\n---"); end >= 0 {
		rest = rest[:end]
	}
	return strings.Contains(rest, "{{")
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
