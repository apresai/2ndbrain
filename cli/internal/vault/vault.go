package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/store"
)

const DotDirName = ".2ndbrain"

var (
	ErrNotAVault   = errors.New("not a 2ndbrain vault (missing .2ndbrain directory)")
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

	root := findVaultRoot(absDir)
	if root == "" {
		return nil, ErrNotAVault
	}

	dotDir := filepath.Join(root, DotDirName)

	cfg, err := LoadConfig(dotDir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	// Surface config self-healing so the user sees it happened without
	// having to dig through logs. stderr only — this is operational
	// information, not an error, and the command the user ran still
	// proceeds normally.
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

	// Detect a missing index.db specifically — the user likely deleted
	// it thinking it was a cache. Recreate an empty DB and tell them
	// to rebuild. The vault still opens; markdown files are intact.
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

	dotDir := filepath.Join(absDir, DotDirName)
	if _, err := os.Stat(dotDir); err == nil {
		return nil, ErrAlreadyInit
	}

	// Create directory structure
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

	return &Vault{
		Root:    absDir,
		DotDir:  dotDir,
		Config:  cfg,
		Schemas: schemas,
		DB:      db,
	}, nil
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

func findVaultRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, DotDirName)); err == nil {
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
