package vault

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Obsidian's template files are not notes. A template is authored with
// placeholder syntax the core Templates plugin and Templater expand at insert
// time ("{{date}}", "<% tp.file.title %>"), which is not YAML and not markdown
// anybody searches for. Indexing them produced a parse failure on every single
// run: one real vault logged "index file failed" three times per index, forever,
// for templates/note.md and templates/daily.md, and their bodies polluted search
// results with text that only ever existed to be replaced.
//
// Obsidian records where they live, so 2nb reads it rather than guessing: the
// core plugin's .obsidian/templates.json ("folder") and Templater's
// .obsidian/plugins/templater-obsidian/data.json ("templates_folder"). A vault
// that configures neither is unaffected and indexes exactly what it did before.

// ObsidianTemplateFolders returns the vault-relative folders Obsidian has
// configured as template locations, sorted and deduped. A missing, unreadable or
// malformed config is not an error: it means "no template folder configured",
// which is the same answer as a vault that never set one.
func ObsidianTemplateFolders(root string) []string {
	seen := map[string]bool{}
	add := func(raw string) {
		rel := normalizeTemplateFolder(raw)
		if rel == "" {
			return
		}
		seen[rel] = true
	}

	add(readJSONStringField(filepath.Join(root, ".obsidian", "templates.json"), "folder"))
	add(readJSONStringField(
		filepath.Join(root, ".obsidian", "plugins", "templater-obsidian", "data.json"),
		"templates_folder",
	))

	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// readJSONStringField reads one top-level string field out of a JSON file.
// Every failure mode (absent, unreadable, not JSON, field missing, field not a
// string) returns "": a template folder is a convenience, never a reason to fail
// an index.
func readJSONStringField(path, field string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("read obsidian config failed", "path", path, "err", err)
		}
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		slog.Debug("obsidian config is not valid JSON", "path", path, "err", err)
		return ""
	}
	s, _ := obj[field].(string)
	return s
}

// normalizeTemplateFolder canonicalizes a configured folder to a clean,
// vault-relative, slash-separated path. An absolute path, an empty value, or
// anything that escapes the vault is dropped: the setting names a folder INSIDE
// the vault, and honoring an escape would exclude an unrelated part of the tree.
func normalizeTemplateFolder(raw string) string {
	s := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	s = strings.Trim(s, "/")
	if s == "" || s == "." {
		return ""
	}
	if filepath.IsAbs(raw) {
		return ""
	}
	s = filepath.ToSlash(filepath.Clean(s))
	if s == "." || s == ".." || strings.HasPrefix(s, "../") {
		return ""
	}
	return s
}

// isUnderFolder reports whether the vault-relative relPath sits inside folder.
// Segment-aware, so a "templates" exclusion never swallows "templates-archive".
func isUnderFolder(relPath, folder string) bool {
	rel := filepath.ToSlash(relPath)
	return rel == folder || strings.HasPrefix(rel, folder+"/")
}

// IsExcludedFolderPath reports whether a vault-relative path sits in any of the
// given excluded folders.
func IsExcludedFolderPath(relPath string, folders []string) bool {
	for _, f := range folders {
		if isUnderFolder(relPath, f) {
			return true
		}
	}
	return false
}
