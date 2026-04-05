package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var importObsidianTarget string

var importObsidianCmd = &cobra.Command{
	Use:   "import-obsidian <obsidian-vault-path>",
	Short: "Import an Obsidian vault into 2ndbrain",
	Args:  cobra.ExactArgs(1),
	RunE:  runImportObsidian,
}

func init() {
	importObsidianCmd.Flags().StringVar(&importObsidianTarget, "target", "", "Target directory for the 2ndbrain vault (default: import in-place)")
	rootCmd.AddCommand(importObsidianCmd)
}

// isObsidianSkipDir returns true for directories that should not be imported.
func isObsidianSkipDir(base string) bool {
	return base == ".obsidian" || base == "templates" ||
		strings.HasPrefix(base, ".") || base == "node_modules"
}

// inlineTagRe matches #tag patterns that are NOT headings.
// We apply this per-line, skipping headings and code blocks.
var inlineTagRe = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][a-zA-Z0-9_-]*)`)

type importObsidianStats struct {
	FilesProcessed int `json:"files_processed"`
	UUIDsGenerated int `json:"uuids_generated"`
	TagsNormalized int `json:"tags_normalized"`
}

func runImportObsidian(cmd *cobra.Command, args []string) error {
	srcPath, err := filepath.Abs(expandPath(args[0]))
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}

	// Validate source is an Obsidian vault or contains markdown files.
	if err := validateObsidianSource(srcPath); err != nil {
		return err
	}

	// Determine the target root where .2ndbrain/ will live.
	// Priority: --target flag > --vault flag > error (never modify source)
	var targetRoot string
	if importObsidianTarget != "" {
		targetRoot, err = filepath.Abs(importObsidianTarget)
		if err != nil {
			return fmt.Errorf("resolve target path: %w", err)
		}
	} else if flagVault != "" {
		targetRoot, err = filepath.Abs(expandPath(flagVault))
		if err != nil {
			return fmt.Errorf("resolve vault path: %w", err)
		}
	} else {
		return fmt.Errorf("target vault required: use --vault <path> or --target <path>")
	}

	if targetRoot == srcPath {
		return fmt.Errorf("target vault cannot be the same as source Obsidian vault — the source is never modified")
	}

	// Copy markdown files from source to target (source is never modified).
	if err := copyMarkdownFiles(srcPath, targetRoot); err != nil {
		return fmt.Errorf("copy files: %w", err)
	}

	// Walk and rewrite the COPIES in the target directory so
	// every file has a UUID when IndexVault runs.
	stats := &importObsidianStats{}
	if err := filepath.Walk(targetRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // skip inaccessible paths
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if isObsidianSkipDir(base) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}

		uuidsAdded, tagsAdded, processErr := processObsidianFile(path)
		if processErr != nil {
			fmt.Fprintf(os.Stderr, "warning: skip %s: %v\n", path, processErr)
			return nil
		}
		stats.FilesProcessed++
		stats.UUIDsGenerated += uuidsAdded
		stats.TagsNormalized += tagsAdded
		return nil
	}); err != nil {
		return fmt.Errorf("walk source: %w", err)
	}

	// Initialize vault at targetRoot (no-op if already initialized).
	v, err := vault.Init(targetRoot)
	if err != nil {
		if errors.Is(err, vault.ErrAlreadyInit) {
			// Open existing vault instead.
			v, err = vault.Open(targetRoot)
			if err != nil {
				return fmt.Errorf("open existing vault: %w", err)
			}
		} else {
			return fmt.Errorf("init vault: %w", err)
		}
	}
	defer v.Close()

	if !flagPorcelain {
		fmt.Fprintln(os.Stderr, "Indexing vault...")
	}

	_, indexErr := vault.IndexVault(v, func(path string) {
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "  %s\n", path)
		}
	})
	if indexErr != nil {
		return fmt.Errorf("index vault: %w", indexErr)
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Import complete: %d files processed, %d UUIDs generated, %d tags normalized\n",
			stats.FilesProcessed, stats.UUIDsGenerated, stats.TagsNormalized)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, stats)
}

// copyMarkdownFiles copies .md files from src to dst, preserving directory structure.
// Skips .obsidian/, hidden directories, and non-markdown files.
func copyMarkdownFiles(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if isObsidianSkipDir(base) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return nil
		}
		dstPath := filepath.Join(dst, rel)

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(dstPath), err)
		}

		// Copy file
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dstPath, err)
		}
		return nil
	})
}

// validateObsidianSource checks that srcPath has .obsidian/ or at least one .md file.
func validateObsidianSource(srcPath string) error {
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("source path does not exist: %s", srcPath)
	}

	// Presence of .obsidian/ is the canonical Obsidian marker.
	if _, err := os.Stat(filepath.Join(srcPath, ".obsidian")); err == nil {
		return nil
	}

	// Fall back: accept any directory that contains at least one .md file.
	found := false
	_ = filepath.Walk(srcPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if !found {
		return fmt.Errorf("source does not appear to be an Obsidian vault (no .obsidian/ directory and no .md files): %s", srcPath)
	}
	return nil
}

// processObsidianFile reads a markdown file, enriches its frontmatter, and
// writes it back. Returns (uuidsAdded, tagsNormalized, error).
func processObsidianFile(path string) (int, int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read: %w", err)
	}

	meta, body, err := document.ParseFrontmatter(content)
	if err != nil {
		return 0, 0, fmt.Errorf("parse frontmatter: %w", err)
	}

	// Ensure meta map exists even for files with no frontmatter.
	if meta == nil {
		meta = make(map[string]any)
	}

	uuidsAdded := 0
	tagsNormalized := 0

	// Generate UUID if missing.
	if _, ok := meta["id"]; !ok {
		meta["id"] = uuid.New().String()
		uuidsAdded = 1
	}

	// Set default type.
	if _, ok := meta["type"]; !ok {
		meta["type"] = "note"
	}

	// Set default status.
	if _, ok := meta["status"]; !ok {
		meta["status"] = "draft"
	}

	// Ensure created / modified timestamps.
	now := time.Now().UTC().Format(time.RFC3339)
	if _, ok := meta["created"]; !ok {
		meta["created"] = now
	}
	if _, ok := meta["modified"]; !ok {
		meta["modified"] = now
	}

	// Extract title from filename if missing.
	if _, ok := meta["title"]; !ok {
		base := filepath.Base(path)
		meta["title"] = strings.TrimSuffix(base, filepath.Ext(base))
	}

	// Normalize inline #tags from body into frontmatter tags.
	inlineTags, cleanedBody := extractInlineTags(body)
	if len(inlineTags) > 0 {
		merged := mergeTagsIntoMeta(meta, inlineTags)
		tagsNormalized = merged
		body = cleanedBody
	}

	// Write back to disk.
	out, err := document.SerializeDocument(meta, body)
	if err != nil {
		return 0, 0, fmt.Errorf("serialize: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return 0, 0, fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return 0, 0, fmt.Errorf("rename: %w", err)
	}

	return uuidsAdded, tagsNormalized, nil
}

// extractInlineTags scans body lines for #tag patterns (skipping headings and
// fenced code blocks) and returns the discovered tags plus a cleaned body
// where inline tags have been removed from non-heading content.
func extractInlineTags(body string) (tags []string, cleanedBody string) {
	lines := strings.Split(body, "\n")
	inCode := false
	seen := make(map[string]bool)
	cleaned := make([]string, 0, len(lines))

	for _, line := range lines {
		// Toggle fenced code block state.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCode = !inCode
			cleaned = append(cleaned, line)
			continue
		}
		if inCode {
			cleaned = append(cleaned, line)
			continue
		}

		// Skip heading lines — a leading # is markdown heading syntax, not a tag.
		if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#!") {
			// Determine if this is a heading: one or more # followed by a space.
			isHeading := false
			for i, c := range trimmed {
				if c == '#' {
					continue
				}
				if c == ' ' && i > 0 {
					isHeading = true
				}
				break
			}
			if isHeading {
				cleaned = append(cleaned, line)
				continue
			}
		}

		// Find all #tags in this line.
		matches := inlineTagRe.FindAllStringSubmatch(line, -1)
		if len(matches) == 0 {
			cleaned = append(cleaned, line)
			continue
		}

		newLine := line
		for _, m := range matches {
			tag := m[1]
			if !seen[tag] {
				seen[tag] = true
				tags = append(tags, tag)
			}
			// Remove the tag from the line (preserve surrounding whitespace minimally).
			newLine = strings.Replace(newLine, "#"+tag, "", 1)
		}
		// Collapse multiple spaces left behind.
		newLine = strings.TrimRight(newLine, " ")
		cleaned = append(cleaned, newLine)
	}

	return tags, strings.Join(cleaned, "\n")
}

// mergeTagsIntoMeta adds newTags into the "tags" field of meta, deduplicating.
// Returns the count of net-new tags added.
func mergeTagsIntoMeta(meta map[string]any, newTags []string) int {
	existing := make(map[string]bool)
	var current []string

	switch v := meta["tags"].(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				existing[s] = true
				current = append(current, s)
			}
		}
	case []string:
		for _, s := range v {
			existing[s] = true
			current = append(current, s)
		}
	}

	added := 0
	for _, t := range newTags {
		if !existing[t] {
			existing[t] = true
			current = append(current, t)
			added++
		}
	}

	// Store as []any so yaml.v3 marshals it correctly.
	asAny := make([]any, len(current))
	for i, s := range current {
		asAny[i] = s
	}
	meta["tags"] = asAny
	return added
}
