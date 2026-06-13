package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/vault"
)

// readWriteContent resolves the content for a body-write command from, in
// order: the --text flag, a --file path, or stdin. Exactly one source is used.
// An empty --text is still treated as an explicit (empty) source; --file and
// stdin are read verbatim. Trailing newlines on a file/stdin read are left as
// the user supplied them — body-write commands normalize spacing themselves.
func readWriteContent(text, file string, textSet bool) (string, error) {
	switch {
	case textSet:
		return text, nil
	case file != "":
		b, err := os.ReadFile(expandPath(file))
		if err != nil {
			return "", fmt.Errorf("read --file %s: %w", file, err)
		}
		return string(b), nil
	default:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	}
}

// writeBody persists an edited document body to disk and refreshes the index.
//
// This is the single write path shared by `append`, `prepend`, and `replace`.
// It mirrors the meta.go write dance but for body edits:
//
//  1. Refuse read-only synthetic types (.canvas/.base) before touching disk —
//     Serialize would emit the synthesized markdown over the original JSON/YAML
//     and corrupt the file.
//  2. Serialize the in-memory doc (Serialize persists doc.Body) with doc.Path
//     pointed at the absolute path so it works from any cwd, then restore the
//     vault-relative path for indexing.
//  3. Atomic temp+rename write.
//  4. Reindex the single file (chunks/tags/links + ResolveLinks) so new body
//     text that adds #tags or [[links]] is picked up.
//  5. Recompute the content hash and re-embed inline if a provider is
//     available (silently skipped otherwise), matching create.go.
//
// absPath must be the absolute on-disk path; doc.Path is left vault-relative on
// return so callers can format/output it.
func writeBody(v *vault.Vault, doc *document.Document, absPath string) error {
	if document.IsReadOnlyType(doc.Type) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: cannot edit the body of a read-only %s file (%s); .canvas/.base files are indexed read-only", doc.Type, doc.Path))
	}

	// Serialize reads the on-disk file (by doc.Path) to preserve YAML comments
	// and key order in the frontmatter, then writes d.Body for the content.
	// Point it at the absolute path, then restore the vault-relative path.
	rel := doc.Path
	doc.Path = absPath
	content, err := doc.Serialize()
	doc.Path = rel
	if err != nil {
		return fmt.Errorf("serialize document: %w", err)
	}

	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, absPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}

	// Reindex this file: chunks/tags/links plus global link resolution, so a
	// newly appended [[link]] or #tag is reflected in the index immediately.
	if err := vault.IndexSingleFile(v, absPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index document: %v\n", err)
		slog.Warn("failed to index document after body write", "err", err)
	}

	// The body changed, so the content hash must be recomputed before
	// re-embedding (SetEmbedding stores the hash alongside the vector).
	doc.ComputeContentHash()
	embedNewDocument(v, doc)

	return nil
}
