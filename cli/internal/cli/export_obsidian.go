package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var exportObsidianStripIDs bool

var exportObsidianCmd = &cobra.Command{
	Use:   "export-obsidian <target-path>",
	Short: "Export the vault as an Obsidian-compatible vault",
	Args:  cobra.ExactArgs(1),
	RunE:  runExportObsidian,
}

func init() {
	exportObsidianCmd.Flags().BoolVar(&exportObsidianStripIDs, "strip-ids", false, "Remove id and type fields from frontmatter")
	rootCmd.AddCommand(exportObsidianCmd)
}

type exportObsidianStats struct {
	FilesExported int `json:"files_exported"`
}

// obsidianAppJSON is the minimal .obsidian/app.json content.
const obsidianAppJSON = `{}`

// obsidianCorePluginsJSON lists the standard Obsidian core plugins to enable.
const obsidianCorePluginsJSON = `["file-explorer","global-search","tag-pane","backlink","page-preview","templates","daily-notes","outline"]`

func runExportObsidian(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	targetPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("resolve target path: %w", err)
	}

	// Create target directory.
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}

	// Write minimal .obsidian/ config files.
	if err := writeObsidianConfig(targetPath); err != nil {
		return fmt.Errorf("write obsidian config: %w", err)
	}

	stats := &exportObsidianStats{}

	// Walk vault root and copy files.
	if err := filepath.Walk(v.Root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // skip inaccessible paths
		}
		if info.IsDir() {
			base := filepath.Base(path)
			// Skip .2ndbrain and other hidden directories.
			if base == vault.DotDirName || strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		lowerPath := strings.ToLower(path)

		// Handle .md files: copy with optional frontmatter stripping.
		if strings.HasSuffix(lowerPath, ".md") {
			if err := exportMarkdownFile(v, path, targetPath); err != nil {
				fmt.Fprintf(os.Stderr, "warning: skip %s: %v\n", path, err)
				return nil
			}
			stats.FilesExported++
			return nil
		}

		// Copy .canvas files verbatim.
		if strings.HasSuffix(lowerPath, ".canvas") {
			if err := copyFileToTarget(v, path, targetPath); err != nil {
				fmt.Fprintf(os.Stderr, "warning: skip canvas %s: %v\n", path, err)
				return nil
			}
			stats.FilesExported++
			return nil
		}

		return nil
	}); err != nil {
		return fmt.Errorf("walk vault: %w", err)
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Export complete: %d files exported to %s\n", stats.FilesExported, targetPath)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, stats)
}

// writeObsidianConfig creates .obsidian/app.json and .obsidian/core-plugins.json.
func writeObsidianConfig(targetPath string) error {
	obsidianDir := filepath.Join(targetPath, ".obsidian")
	if err := os.MkdirAll(obsidianDir, 0o755); err != nil {
		return fmt.Errorf("create .obsidian: %w", err)
	}

	files := map[string]string{
		"app.json":           obsidianAppJSON,
		"core-plugins.json":  obsidianCorePluginsJSON,
	}

	for name, content := range files {
		p := filepath.Join(obsidianDir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	return nil
}

// exportMarkdownFile copies a markdown file to the target, optionally stripping
// id and type fields from the frontmatter.
func exportMarkdownFile(v *vault.Vault, srcPath, targetRoot string) error {
	if !exportObsidianStripIDs {
		// Fast path: no rewriting needed.
		return copyFileToTarget(v, srcPath, targetRoot)
	}

	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	meta, body, err := document.ParseFrontmatter(content)
	if err != nil {
		return fmt.Errorf("parse frontmatter: %w", err)
	}

	if meta != nil {
		delete(meta, "id")
		delete(meta, "type")
	}

	out, err := document.SerializeDocument(meta, body)
	if err != nil {
		return fmt.Errorf("serialize: %w", err)
	}

	destPath := destPathFor(v, srcPath, targetRoot)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	tmp := destPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// copyFileToTarget copies srcPath to targetRoot, preserving vault-relative directory structure.
func copyFileToTarget(v *vault.Vault, srcPath, targetRoot string) error {
	destPath := destPathFor(v, srcPath, targetRoot)

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	tmp := destPath + ".tmp"
	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmp)
		return fmt.Errorf("copy: %w", err)
	}
	dst.Close()

	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// destPathFor computes the destination path by replacing the vault root prefix
// with targetRoot while preserving the rest of the relative path.
func destPathFor(v *vault.Vault, srcPath, targetRoot string) string {
	rel := v.RelPath(srcPath)
	return filepath.Join(targetRoot, rel)
}
