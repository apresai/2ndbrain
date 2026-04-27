package ai

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// dotDirName mirrors vault.DotDirName. Duplicated here to keep the ai package
// free of a dependency on internal/vault (which imports ai → cycle).
const dotDirName = ".2ndbrain"

// UserCatalogScope identifies where a user catalog entry lives.
type UserCatalogScope string

const (
	// ScopeGlobal is the per-user catalog at $XDG_CONFIG_HOME/2nb/models.yaml
	// (falling back to ~/.config/2nb/models.yaml).
	ScopeGlobal UserCatalogScope = "global"
	// ScopeVault is the per-vault catalog at <vault>/.2ndbrain/models.yaml.
	ScopeVault UserCatalogScope = "vault"
)

const userCatalogFileName = "models.yaml"
const userCatalogVersion = 1

var userCatalogMu sync.Mutex

// UserCatalog is the YAML shape for both global and per-vault catalog files.
type UserCatalog struct {
	Version int         `yaml:"version"`
	Models  []ModelInfo `yaml:"models"`
}

// LoadUserCatalog reads both the global and the per-vault user catalogs and
// returns a single merged slice. The vault catalog takes precedence over the
// global catalog when both contain an entry with the same (provider, id).
//
// Missing files are not errors. A corrupt file is renamed to .bak and treated
// as empty so a malformed catalog never blocks the CLI.
func LoadUserCatalog(vaultRoot string) []ModelInfo {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	global := readCatalog(globalCatalogPath(), true).Models
	perVault := readCatalog(vaultCatalogPath(vaultRoot), true).Models

	merged := make([]ModelInfo, 0, len(global)+len(perVault))
	merged = append(merged, global...)
	merged = overlay(merged, perVault)
	for i := range merged {
		tagAsUserCatalog(&merged[i])
	}
	return merged
}

// SaveUserCatalogEntry writes a single entry to the catalog at `scope`. The
// file is created if it doesn't exist; an existing entry with the same
// (provider, id) is replaced in place.
func SaveUserCatalogEntry(scope UserCatalogScope, vaultRoot string, entry ModelInfo) error {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return err
	}
	cat, err := readCatalogForWrite(path)
	if err != nil {
		return err
	}

	replaced := false
	for i := range cat.Models {
		if cat.Models[i].Provider == entry.Provider && cat.Models[i].ID == entry.ID {
			cat.Models[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		cat.Models = append(cat.Models, entry)
	}

	return writeCatalog(path, cat)
}

// RemoveUserCatalogEntry removes the matching (provider, id) from the catalog
// at `scope`. Returns nil if the entry was not present — no empty catalog file
// is written in that case.
func RemoveUserCatalogEntry(scope UserCatalogScope, vaultRoot, provider, id string) error {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return err
	}
	cat, err := readCatalogForWrite(path)
	if err != nil {
		return err
	}

	kept := cat.Models[:0]
	removed := false
	for _, m := range cat.Models {
		if m.Provider == provider && m.ID == id {
			removed = true
			continue
		}
		kept = append(kept, m)
	}
	if !removed {
		return nil
	}
	cat.Models = kept
	return writeCatalog(path, cat)
}

// globalCatalogPath returns the path to the per-user catalog file, respecting
// $XDG_CONFIG_HOME if set.
func globalCatalogPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "2nb", userCatalogFileName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "2nb", userCatalogFileName)
}

// vaultCatalogPath returns the per-vault catalog path, or "" when vaultRoot is empty.
func vaultCatalogPath(vaultRoot string) string {
	if vaultRoot == "" {
		return ""
	}
	return filepath.Join(vaultRoot, dotDirName, userCatalogFileName)
}

// CatalogPathForScope is the exported form of catalogPathForScope, used by
// CLI code that wants to tell the user exactly which file was written.
func CatalogPathForScope(scope UserCatalogScope, vaultRoot string) (string, error) {
	return catalogPathForScope(scope, vaultRoot)
}

func catalogPathForScope(scope UserCatalogScope, vaultRoot string) (string, error) {
	switch scope {
	case ScopeGlobal:
		p := globalCatalogPath()
		if p == "" {
			return "", fmt.Errorf("cannot resolve user home directory")
		}
		return p, nil
	case ScopeVault:
		if vaultRoot == "" {
			return "", fmt.Errorf("vault scope requires an open vault")
		}
		return vaultCatalogPath(vaultRoot), nil
	default:
		return "", fmt.Errorf("unknown scope %q (use global or vault)", scope)
	}
}

// readCatalog reads and parses a user catalog file. Missing files return an
// empty catalog at the current version. Corrupt files are handled based on
// `quarantineCorrupt`: true renames the bad file to .bak (so the next write
// produces a fresh one); false leaves it in place. Either way the caller
// gets an empty catalog — the CLI never bricks on a bad file.
func readCatalog(path string, quarantineCorrupt bool) UserCatalog {
	empty := UserCatalog{Version: userCatalogVersion}
	if path == "" {
		return empty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("read user catalog failed", "path", path, "err", err)
		}
		return empty
	}
	var cat UserCatalog
	if err := yaml.Unmarshal(data, &cat); err != nil {
		slog.Warn("parse user catalog failed", "path", path, "err", err)
		if quarantineCorrupt {
			if renameErr := os.Rename(path, path+".bak"); renameErr != nil {
				slog.Warn("quarantine corrupt user catalog failed", "path", path, "backup", path+".bak", "err", renameErr)
			} else {
				slog.Warn("quarantined corrupt user catalog", "path", path, "backup", path+".bak")
			}
		}
		return empty
	}
	if cat.Version == 0 {
		cat.Version = userCatalogVersion
	}
	return cat
}

func readCatalogForWrite(path string) (UserCatalog, error) {
	empty := UserCatalog{Version: userCatalogVersion}
	if path == "" {
		return empty, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		slog.Warn("read user catalog failed", "path", path, "err", err)
		return empty, fmt.Errorf("read catalog: %w", err)
	}
	var cat UserCatalog
	if err := yaml.Unmarshal(data, &cat); err != nil {
		backup := path + ".bak"
		slog.Warn("parse user catalog failed before write", "path", path, "backup", backup, "err", err)
		if renameErr := os.Rename(path, backup); renameErr != nil {
			slog.Warn("quarantine corrupt user catalog failed before write", "path", path, "backup", backup, "err", renameErr)
			return empty, fmt.Errorf("catalog %s is corrupt and could not be moved to %s: %w", path, backup, renameErr)
		}
		slog.Warn("quarantined corrupt user catalog before write", "path", path, "backup", backup)
		return empty, nil
	}
	if cat.Version == 0 {
		cat.Version = userCatalogVersion
	}
	return cat, nil
}

func writeCatalog(path string, cat UserCatalog) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create catalog dir: %w", err)
	}
	data, err := yaml.Marshal(cat)
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	return nil
}

// overlay replaces entries in base with matching entries from overlay (by
// provider+id) and appends any overlay entries that don't exist in base.
// Returns a new slice; inputs are not mutated.
func overlay(base, top []ModelInfo) []ModelInfo {
	if len(top) == 0 {
		return base
	}
	index := map[string]int{}
	for i, m := range base {
		index[catalogKey(m.Provider, m.ID)] = i
	}
	out := make([]ModelInfo, len(base))
	copy(out, base)
	for _, m := range top {
		key := catalogKey(m.Provider, m.ID)
		if i, ok := index[key]; ok {
			out[i] = mergeFields(out[i], m)
		} else {
			out = append(out, m)
			index[key] = len(out) - 1
		}
	}
	return out
}

func modelHasAnyPrice(m ModelInfo) bool {
	return m.PriceIn != 0 || m.PriceOut != 0 || m.PriceRequest != 0
}

func hasUserPriceOverride(m ModelInfo) bool {
	return m.PriceSource == "user" && (m.PriceOverride || modelHasAnyPrice(m))
}

// mergeFields copies fields from `top` onto `base`, returning the merged
// entry. Price fields are copied as a unit when `top.PriceSource` is set,
// so a user catalog entry with explicit price_in=0 (e.g. a free tier)
// correctly overrides a non-zero builtin price. Tier is monotonically
// elevated (verified beats user_verified beats unverified) so bundled
// prices can apply without demoting a user-verified entry.
func mergeFields(base, top ModelInfo) ModelInfo {
	out := base
	if top.Name != "" {
		out.Name = top.Name
	}
	if top.Dimensions != 0 {
		out.Dimensions = top.Dimensions
	}
	if top.ContextLen != 0 {
		out.ContextLen = top.ContextLen
	}
	// When the overlay declares a price source, treat prices as intentional
	// even if zero. Otherwise only non-zero overrides apply (protects builtin
	// prices from overlays that haven't populated them).
	if top.PriceSource == "user" {
		if hasUserPriceOverride(top) {
			out.PriceIn = top.PriceIn
			out.PriceOut = top.PriceOut
			out.PriceRequest = top.PriceRequest
			out.PriceSource = "user"
			out.PriceOverride = true
		}
	} else if top.PriceSource != "" {
		out.PriceIn = top.PriceIn
		out.PriceOut = top.PriceOut
		out.PriceRequest = top.PriceRequest
		out.PriceSource = top.PriceSource
		out.PriceOverride = top.PriceOverride
	} else {
		if top.PriceIn != 0 {
			out.PriceIn = top.PriceIn
		}
		if top.PriceOut != 0 {
			out.PriceOut = top.PriceOut
		}
		if top.PriceRequest != 0 {
			out.PriceRequest = top.PriceRequest
		}
	}
	if top.ConfigHint != "" {
		out.ConfigHint = top.ConfigHint
	}
	if top.Notes != "" {
		out.Notes = top.Notes
	}
	if top.TestedAt != "" {
		out.TestedAt = top.TestedAt
		// Test result fields move as a unit with TestedAt: they describe the
		// same event. TestLatencyMs and TestError may legitimately be zero /
		// empty on a passing run, so overlay even zero values here.
		out.TestLatencyMs = top.TestLatencyMs
		out.TestError = top.TestError
	}
	// RecommendedSimilarityThreshold: any positive overlay value wins. Zero
	// means "not set in the overlay" — preserve the builtin value. Users who
	// want to reset to the global default can set ai.similarity_threshold on
	// the vault config instead (explicit override beats catalog).
	if top.RecommendedSimilarityThreshold > 0 {
		out.RecommendedSimilarityThreshold = top.RecommendedSimilarityThreshold
	}
	if top.InvokeStrategy != "" {
		out.InvokeStrategy = top.InvokeStrategy
	}
	if top.Benchmark != nil {
		b := *top.Benchmark
		out.Benchmark = &b
	}
	if top.Enabled != nil {
		e := *top.Enabled
		out.Enabled = &e
	}
	out.Tier = elevateTier(out.Tier, top.Tier)
	if top.Local {
		out.Local = true
	}
	return out
}

// elevateTier returns whichever tier represents more trust. Order:
// verified > user_verified > unverified > "".
func elevateTier(a, b ModelTier) ModelTier {
	if tierRank(b) > tierRank(a) {
		return b
	}
	return a
}

func tierRank(t ModelTier) int {
	switch t {
	case TierVerified:
		return 3
	case TierUserVerified:
		return 2
	case TierUnverified:
		return 1
	}
	return 0
}

func tagAsUserCatalog(m *ModelInfo) {
	if m.Tier == "" {
		m.Tier = TierUserVerified
	}
	switch {
	case m.PriceOverride:
		m.PriceSource = "user"
	case m.PriceSource == "user":
		if modelHasAnyPrice(*m) {
			m.PriceOverride = true
		} else {
			// Back-compat for buggy legacy entries written without explicit
			// price flags: zero-valued user prices should not mask vendor data.
			m.PriceSource = ""
		}
	case m.PriceSource == "" && modelHasAnyPrice(*m):
		m.PriceSource = "user"
		m.PriceOverride = true
	}
}
