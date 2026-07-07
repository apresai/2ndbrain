package vault

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
)

// CollectLiveDocs walks the vault root on the LIVE FILESYSTEM and returns the
// document set plus alias index that feed store.NewResolver, the same tiered
// lookup the indexer feeds the DB. It is the single source for every caller
// that must resolve wikilinks without depending on a fresh index.db: lint, and
// the link-repair tools (repair-links, suggest-target, relink) that must agree
// with lint about which links are broken. A note created in Obsidian but not
// yet indexed resolves here; a note deleted on disk but still in the DB does
// not.
//
// For each note it registers the vault-relative path and, by parsing it, the
// frontmatter title and aliases, exactly the inputs the indexer feeds the DB,
// so a link that resolves only by title or alias resolves here just as
// `2nb links` resolves it via a fresh DB. The walk mirrors the indexer's prune
// rules (dot-directories, node_modules, IsIgnored files; see indexer.go) so
// the resolver's document set EQUALS what the indexer puts in the DB.
// Otherwise a caller could resolve a link to a trashed/.obsidian note that
// `2nb links` reports broken, and it would needlessly ParseFile thousands of
// plugin node_modules files.
//
// The canonical index strips only the .md extension. The extension-stripped
// basename AND rel-path of .canvas/.base files are registered as aliases so
// both a bare [[board]] and a path-qualified [[sub/board]] still resolve to
// board.canvas (as Obsidian does); [[board.canvas]] with the extension
// resolves via the path tier.
//
// Parsing is best-effort: a file that fails to parse (e.g. an Obsidian
// {{placeholder}} template) is still registered by path, so links to it
// resolve; only its title/aliases are skipped.
func CollectLiveDocs(root string) ([]store.DocInfo, map[string][]string, error) {
	var docs []store.DocInfo
	aliasIndex := make(map[string][]string)
	if werr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != root {
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
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		if IsIgnored(rel) {
			return nil
		}
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
				// unavailable, so a link resolvable only by them may be missed.
				// Leave a breadcrumb (mirrors lint's template-skip debug).
				slog.Debug("live-docs walk: skipping unparseable note (title/aliases unavailable)", "path", rel, "err", perr)
			}
		}
		docs = append(docs, store.DocInfo{ID: rel, Path: rel, Title: title}) // path is the unique id
		return nil
	}); werr != nil {
		return nil, nil, werr
	}
	return docs, aliasIndex, nil
}
