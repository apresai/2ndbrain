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
// that configures neither, and follows no convention, is unaffected and indexes
// exactly what it did before.
//
// Reading the config alone was not enough, and that is why the convention
// fallback below exists. Obsidian writes templates.json only once you SAVE the
// Templates folder setting, so a vault with the core plugin enabled and the
// default folder has no templates.json at all: the exclusion was inert on
// exactly the vault it was written for, which kept logging a parse failure per
// template on every index.

// conventionalTemplateFolder is the folder name Obsidian's own Templates
// plugin, the community's convention, and this repo's own import walk all
// treat as the template location.
const conventionalTemplateFolder = "templates"

// ObsidianTemplateFolders returns the vault-relative folders that hold template
// files, sorted and deduped. A missing, unreadable or malformed config is not
// an error: it means "no template folder configured", the same answer as a
// vault that never set one.
//
// Obsidian's own configuration stays authoritative. Only when it declares none
// does the conventional top-level "templates/" folder apply, and only under two
// gates that must BOTH hold:
//
//   - .obsidian/core-plugins.json enables "templates", so the user actually
//     uses the feature this convention belongs to, and
//   - <root>/templates exists and is a directory.
//
// The gates are not decoration. purgeStale DELETES the rows, chunks and vectors
// of every note under a newly-excluded folder, so an ungated convention would
// silently strip the index of a user who keeps real notes in a folder they
// happened to name "templates". Both gates fail CLOSED: an unreadable or
// unrecognized config means "not enabled", the fallback stays off, and that
// vault indexes exactly what it did before.
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

	if len(seen) == 0 && conventionalTemplatesApply(root) {
		seen[conventionalTemplateFolder] = true
	}

	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// conventionalTemplatesApply reports whether the unconfigured top-level
// "templates/" convention should be honored for this vault.
func conventionalTemplatesApply(root string) bool {
	if !readJSONBoolField(filepath.Join(root, ".obsidian", "core-plugins.json"), "templates") {
		return false
	}
	info, err := os.Stat(filepath.Join(root, conventionalTemplateFolder))
	return err == nil && info.IsDir()
}

// readJSONBoolField reports whether a JSON file marks field as enabled, under
// the total-tolerance contract readJSONStringField follows: every failure mode
// (absent, unreadable, not JSON, field missing, field of another type) returns
// false, because a template folder is a convenience and never a reason to fail
// an index.
//
// Obsidian has shipped core-plugins.json in two shapes. The current one, and
// the one verified live against a real vault on 2026-09-04, is an OBJECT of
// booleans ({"templates": true, "slides": false}). Older releases wrote an
// ARRAY of the enabled plugin ids (["file-explorer", "templates"]). Both are
// read here: the array form is handled defensively rather than from a live
// observation, and reading it wrong would only ever leave the fallback off.
func readJSONBoolField(path, field string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("read obsidian config failed", "path", path, "err", err)
		}
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		b, _ := obj[field].(bool)
		return b
	}
	var list []any
	if err := json.Unmarshal(data, &list); err == nil {
		for _, item := range list {
			if s, ok := item.(string); ok && s == field {
				return true
			}
		}
		return false
	}
	slog.Debug("obsidian config is neither a JSON object nor an array", "path", path)
	return false
}

// HasTemplatePlaceholders reports whether a file's YAML frontmatter carries
// unresolved {{...}} template tokens (Obsidian core Templates / Templater).
//
// It is the FILE-level half of the template definition, and the backstop for a
// template kept outside any template folder. Only the frontmatter block is
// inspected, deliberately, so a note ABOUT templating whose body mentions {{ }}
// is never mistaken for one.
//
// It moved here from package cli so lint, import and the indexer share one
// definition. The comment it carried there claimed "the indexer skips them
// too", which was false: the indexer excluded template FOLDERS and knew nothing
// about placeholders.
func HasTemplatePlaceholders(raw []byte) bool {
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

// ExcludedFolderFor returns the excluded folder a vault-relative path sits in.
// Callers that report the exclusion to a user need the folder's name: "not
// indexed" is not actionable without knowing which setting caused it.
func ExcludedFolderFor(relPath string, folders []string) (string, bool) {
	for _, f := range folders {
		if isUnderFolder(relPath, f) {
			return f, true
		}
	}
	return "", false
}

// IsExcludedFolderPath reports whether a vault-relative path sits in any of the
// given excluded folders.
func IsExcludedFolderPath(relPath string, folders []string) bool {
	_, ok := ExcludedFolderFor(relPath, folders)
	return ok
}
