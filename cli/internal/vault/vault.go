package vault

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/store"
)

const DotDirName = ".2ndbrain"

var (
	ErrNotAVault   = errors.New("not an Obsidian vault (missing .obsidian directory)")
	ErrAlreadyInit = errors.New("vault already initialized")
)

type Vault struct {
	Root    string
	DotDir  string
	Config  *VaultConfig
	Schemas *SchemaSet
	DB      *store.DB
}

// Open finds and opens an existing vault from the given directory or any parent.
func Open(dir string) (*Vault, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	root := FindVaultRoot(absDir)
	if root == "" {
		return nil, ErrNotAVault
	}

	dotDir := filepath.Join(root, DotDirName)

	// Automatically initialize .2ndbrain/ sidecar if missing in a native Obsidian vault
	if _, err := os.Stat(dotDir); os.IsNotExist(err) {
		for _, sub := range []string{"", "models", "recovery", "logs"} {
			if err := os.MkdirAll(filepath.Join(dotDir, sub), 0o755); err != nil {
				return nil, fmt.Errorf("create sidecar %s: %w", sub, err)
			}
		}

		name := filepath.Base(root)
		cfg := DefaultConfig(name)
		if err := cfg.Save(dotDir); err != nil {
			return nil, fmt.Errorf("save config: %w", err)
		}

		schemas := DefaultSchemas()
		if err := schemas.Save(dotDir); err != nil {
			return nil, fmt.Errorf("save schemas: %w", err)
		}

		ensureGitignore(root)
	}

	cfg, err := LoadConfig(dotDir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	switch cfg.Recovered {
	case "config_missing":
		fmt.Fprintln(os.Stderr, "  .2ndbrain/config.yaml was missing — regenerated with defaults")
	case "config_corrupt_backup":
		fmt.Fprintln(os.Stderr, "  .2ndbrain/config.yaml was corrupt — backed up to config.yaml.bak and regenerated with defaults")
	}

	schemas, err := LoadSchemas(dotDir)
	if err != nil {
		return nil, fmt.Errorf("load schemas: %w", err)
	}

	indexPath := filepath.Join(dotDir, "index.db")
	indexWasMissing := false
	if _, statErr := os.Stat(indexPath); os.IsNotExist(statErr) {
		indexWasMissing = true
	}

	db, err := store.Open(indexPath)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	if indexWasMissing {
		fmt.Fprintln(os.Stderr, "  .2ndbrain/index.db was missing — created empty index (run '2nb index' to rebuild)")
	}

	return &Vault{
		Root:    root,
		DotDir:  dotDir,
		Config:  cfg,
		Schemas: schemas,
		DB:      db,
	}, nil
}

// Init creates a new vault at the given directory.
func Init(dir string) (*Vault, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	obsidianDir := filepath.Join(absDir, ".obsidian")
	if err := os.MkdirAll(obsidianDir, 0o755); err != nil {
		return nil, fmt.Errorf("create .obsidian: %w", err)
	}

	dotDir := filepath.Join(absDir, DotDirName)
	if _, err := os.Stat(dotDir); err == nil {
		return nil, ErrAlreadyInit
	}

	for _, sub := range []string{"", "models", "recovery", "logs"} {
		if err := os.MkdirAll(filepath.Join(dotDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", sub, err)
		}
	}

	name := filepath.Base(absDir)
	cfg := DefaultConfig(name)
	if err := cfg.Save(dotDir); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	schemas := DefaultSchemas()
	if err := schemas.Save(dotDir); err != nil {
		return nil, fmt.Errorf("save schemas: %w", err)
	}

	db, err := store.Open(filepath.Join(dotDir, "index.db"))
	if err != nil {
		return nil, fmt.Errorf("create index: %w", err)
	}

	ensureGitignore(absDir)

	return &Vault{
		Root:    absDir,
		DotDir:  dotDir,
		Config:  cfg,
		Schemas: schemas,
		DB:      db,
	}, nil
}

func ensureGitignore(root string) {
	gitignorePath := filepath.Join(root, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	switch {
	case err == nil:
		for _, line := range strings.Split(string(content), "\n") {
			if t := strings.TrimSpace(line); t == ".2ndbrain/" || t == ".2ndbrain" {
				return // already ignored
			}
		}
		f, oerr := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0o644)
		if oerr != nil {
			// Loud, not silent: this guard is what keeps index.db out of git.
			slog.Warn("could not open .gitignore to ignore .2ndbrain/; the sidecar may be committed", "path", gitignorePath, "err", oerr)
			return
		}
		defer f.Close()
		if _, werr := f.WriteString("\n# 2ndbrain sidecar directory\n.2ndbrain/\n"); werr != nil {
			slog.Warn("could not append .2ndbrain/ to .gitignore; the sidecar may be committed", "path", gitignorePath, "err", werr)
		}
	case os.IsNotExist(err):
		if werr := os.WriteFile(gitignorePath, []byte("# 2ndbrain sidecar directory\n.2ndbrain/\n"), 0o644); werr != nil {
			slog.Warn("could not create .gitignore for .2ndbrain/; the sidecar may be committed", "path", gitignorePath, "err", werr)
		}
	default:
		slog.Warn("could not read .gitignore to ensure .2ndbrain/ is ignored", "path", gitignorePath, "err", err)
	}
}

func (v *Vault) Close() error {
	if v.DB != nil {
		return v.DB.Close()
	}
	return nil
}

// RelPath returns the vault-relative path for an absolute path.
func (v *Vault) RelPath(absPath string) string {
	rel, err := filepath.Rel(v.Root, absPath)
	if err != nil {
		return absPath
	}
	return rel
}

// AbsPath returns the absolute path for a vault-relative path.
func (v *Vault) AbsPath(relPath string) string {
	if filepath.IsAbs(relPath) {
		return relPath
	}
	return filepath.Join(v.Root, relPath)
}

// ContainsPath reports whether absPath resolves to a location strictly
// inside the vault root (or the root itself). Uses filepath.Rel so a
// sibling "<root>2" directory or a ".." climb can't pass a prefix check.
// Trusted CLI callers don't need this; MCP handlers must.
//
// Both sides are symlink-resolved before the check: a lexical-only guard would
// let an in-vault symlink (e.g. "<root>/escape" -> "/etc") redirect a write
// outside the vault, which the untrusted MCP write handlers must not allow.
// Canonicalizing both sides is required, not just the target: on macOS the
// vault root often lives under "/var" -> "/private/var", so resolving only the
// target would falsely reject every legitimate in-vault path.
func (v *Vault) ContainsPath(absPath string) bool {
	root := resolveSymlinksLenient(v.Root)
	p := resolveSymlinksLenient(absPath)
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	// !IsAbs is a Windows safety net: filepath.Rel returns an absolute
	// path when source and dest sit on different drives.
	return !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// resolveSymlinksLenient returns path with symlinks resolved. If path itself
// doesn't exist yet (the common case for a not-yet-created target file), it
// resolves the deepest existing ancestor and re-joins the non-existent
// remainder. That still catches a symlinked intermediate directory (the escape
// vector) while letting a brand-new file path resolve. Falls back to a cleaned
// lexical path if nothing along the chain resolves.
//
// Limitation: a *dangling* symlink (an in-vault symlink whose target does not
// exist yet) is treated as an ordinary non-existent component, so it isn't
// caught here; it would only escape if the external target later materializes
// and a write then follows the link. Not a regression (the prior lexical guard
// allowed it too); a per-component Lstat walk to catch it is a tracked follow-up.
func resolveSymlinksLenient(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	dir := path
	var tail []string
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return path // reached the root with nothing resolvable; lexical fallback
		}
		tail = append([]string{filepath.Base(dir)}, tail...)
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...)
		}
	}
}

// IsVaultRoot reports whether dir itself is a vault root: a directory
// containing either an .obsidian/ child (an Obsidian vault — Open will
// recreate the .2ndbrain sidecar) or a .2ndbrain/ child. Unlike
// FindVaultRoot it never walks up, so it's the right check for validating
// a stored vault path (e.g. ~/.2ndbrain-active-vault), where resolving a
// dead path to a parent vault would silently change which vault is used.
func IsVaultRoot(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".obsidian")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, DotDirName)); err == nil {
		return true
	}
	return false
}

// FindVaultRoot walks up from dir until it finds a vault root (a directory
// with an .obsidian/ or .2ndbrain/ child, per IsVaultRoot) and returns it.
// Returns "" if no vault is found before reaching the filesystem root.
// Intended for read-only callers (e.g. shell completion) that need the
// vault root without paying for a full Open.
func FindVaultRoot(dir string) string {
	for {
		if IsVaultRoot(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// IsIgnored returns true if the path should be excluded from indexing.
func IsIgnored(path string) bool {
	base := filepath.Base(path)
	lower := strings.ToLower(base)

	// Dot directories
	if strings.HasPrefix(base, ".") {
		return true
	}

	// Security: exclude sensitive files
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return true
	}
	if strings.HasPrefix(lower, "credentials") {
		return true
	}
	if strings.Contains(lower, "secret") {
		return true
	}

	return false
}
