package ai

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const bedrockFileName = "bedrock.json"

// BedrockFile is the machine-local Bedrock credentials document at
// ~/.config/2nb/bedrock.json (XDG-aware). It is never written into a vault.
type BedrockFile struct {
	Region string `json:"region,omitempty"`
	Token  string `json:"token,omitempty"`
	// Regions are additional AWS regions to include beyond the primary when
	// verifying model access and discovering vendor listings (Bedrock model
	// entitlement and ListFoundationModels are both per-region). The primary
	// region is always included and probed first. Empty means single-region.
	Regions []string `json:"regions,omitempty"`
	// TokenUpdatedAt records (RFC3339 UTC) when the stored token last
	// changed, so a UI can flag model-access verdicts that predate the
	// current key. Stamped only on token writes, never on region edits.
	TokenUpdatedAt string `json:"token_updated_at,omitempty"`
}

// BedrockTokenSource names where ResolveBedrockToken found a bearer token.
// Values are stable for `2nb config bedrock --json`.
type BedrockTokenSource string

const (
	BedrockTokenEnv      BedrockTokenSource = "env"
	BedrockTokenFile     BedrockTokenSource = "file"
	BedrockTokenKeychain BedrockTokenSource = "keychain"
	BedrockTokenNone     BedrockTokenSource = "none"
)

// BedrockFilePath returns the resolved machine-local credentials path, or ""
// when the user config directory cannot be determined.
func BedrockFilePath() string {
	dir := globalConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, bedrockFileName)
}

// ReadBedrockFile loads the machine-local credentials file. A missing file is
// not an error (empty document). A file that is group- or world-readable is
// refused so a leaked token cannot be used silently.
func ReadBedrockFile() (BedrockFile, error) {
	path := BedrockFilePath()
	if path == "" {
		return BedrockFile{}, fmt.Errorf("cannot resolve user config directory")
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return BedrockFile{}, nil
		}
		return BedrockFile{}, err
	}
	if !fileIsPrivate(fi.Mode()) {
		return BedrockFile{}, fmt.Errorf("refusing to read %s: mode %04o is not private (want 0600)", path, fi.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return BedrockFile{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return BedrockFile{}, nil
	}
	var out BedrockFile
	if err := json.Unmarshal(data, &out); err != nil {
		return BedrockFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	out.Region = strings.TrimSpace(out.Region)
	out.Token = strings.TrimSpace(out.Token)
	out.Regions = normalizeRegionList(out.Regions)
	return out, nil
}

// normalizeRegionList trims entries and drops empties, preserving order.
func normalizeRegionList(regions []string) []string {
	var out []string
	for _, r := range regions {
		if t := strings.TrimSpace(r); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// WriteBedrockFile replaces the machine-local credentials file. The parent
// directory is created at 0700 and the file is written 0600 via a temp+rename.
func WriteBedrockFile(doc BedrockFile) error {
	path := BedrockFilePath()
	if path == "" {
		return fmt.Errorf("cannot resolve user config directory")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	doc.Region = strings.TrimSpace(doc.Region)
	doc.Token = strings.TrimSpace(doc.Token)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "bedrock-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	// Rename can preserve a previously-world-readable dest on some systems;
	// force the published mode after the swap.
	return os.Chmod(path, 0o600)
}

// UpdateBedrockFile merges fields into the existing document. A nil token
// pointer leaves the stored token unchanged; a non-nil pointer (including
// empty string) replaces it. A non-empty region replaces the stored region;
// pass clearRegion to drop it.
func isNotPrivateErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not private")
}

func UpdateBedrockFile(region string, token *string, clearRegion bool) error {
	cur, err := ReadBedrockFile()
	if err != nil {
		if BedrockFilePath() == "" {
			return err
		}
		// A leaked (non-private) or corrupt file must still be replaceable:
		// WriteBedrockFile writes a new 0600 document over it.
		if !isNotPrivateErr(err) && !strings.Contains(err.Error(), "parse ") {
			return err
		}
		cur = BedrockFile{}
	}
	if clearRegion {
		cur.Region = ""
	} else if strings.TrimSpace(region) != "" {
		cur.Region = strings.TrimSpace(region)
	}
	if token != nil {
		cur.Token = strings.TrimSpace(*token)
		// Any token change (set or clear) is a key change: stamp it so
		// model-access verdicts recorded before this moment can be flagged
		// stale. Region-only edits deliberately leave the stamp alone.
		cur.TokenUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return WriteBedrockFile(cur)
}

// UpdateBedrockRegions replaces the stored additional-regions list (or clears
// it). Each entry must be a bare AWS region label; a hostile or mistyped
// value is refused before anything is written.
func UpdateBedrockRegions(regions []string, clear bool) error {
	cur, err := ReadBedrockFile()
	if err != nil {
		if BedrockFilePath() == "" {
			return err
		}
		if !isNotPrivateErr(err) && !strings.Contains(err.Error(), "parse ") {
			return err
		}
		cur = BedrockFile{}
	}
	if clear {
		cur.Regions = nil
		return WriteBedrockFile(cur)
	}
	normalized := normalizeRegionList(regions)
	for _, r := range normalized {
		if !isBareRegionLabel(r) {
			return fmt.Errorf("invalid region %q: expected a bare AWS region label like us-west-2", r)
		}
	}
	cur.Regions = normalized
	return WriteBedrockFile(cur)
}

// ResolveBedrockRegions returns the full included region set for multi-region
// verification and discovery: the primary region first (today's single-region
// resolution, unchanged), then the machine file's additional regions, deduped
// in order. With no additional regions configured this is exactly [primary].
func ResolveBedrockRegions(vault BedrockConfig) []string {
	primary := ResolveBedrockConfig(vault).Region
	out := []string{primary}
	seen := map[string]bool{primary: true}
	if f, err := ReadBedrockFile(); err == nil {
		for _, r := range f.Regions {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// ClearBedrockStoredToken removes the file token and, on macOS, the Keychain
// item so a Settings / `config bedrock --clear-token` action cannot leave a
// fallback key in place.
func ClearBedrockStoredToken() error {
	empty := ""
	if err := UpdateBedrockFile("", &empty, false); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		_ = DeleteAPIKey("bedrock")
	}
	return nil
}

// ResolveBedrockConfig overlays the machine-file region onto vault config
// when the file names one. An empty result falls back to us-east-1. An
// in-memory RegionOverride outranks the file: it is how per-call region
// variation (multi-region verify, catalog pins) survives this overlay, which
// would otherwise clobber whatever region the caller set.
func ResolveBedrockConfig(vault BedrockConfig) BedrockConfig {
	out := vault
	if out.RegionOverride != "" {
		out.Region = out.RegionOverride
		return out
	}
	if f, err := ReadBedrockFile(); err == nil && f.Region != "" {
		out.Region = f.Region
	}
	if out.Region == "" {
		out.Region = "us-east-1"
	}
	return out
}

// ResolveBedrockToken returns the bearer token and where it came from.
// Precedence: AWS_BEARER_TOKEN_BEDROCK, then the machine file, then the
// macOS Keychain. It does not consult SigV4.
func ResolveBedrockToken() (string, BedrockTokenSource) {
	if t := strings.TrimSpace(os.Getenv(bedrockBearerTokenEnv)); t != "" {
		return t, BedrockTokenEnv
	}
	if t := readBedrockFileToken(); t != "" {
		return t, BedrockTokenFile
	}
	if keychainLookupEnabled() {
		if t, err := keychainGet("bedrock"); err == nil && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t), BedrockTokenKeychain
		}
	}
	return "", BedrockTokenNone
}

// TokenSuffix returns the last 4 characters of a token, or "" when the token
// is too short to reveal a suffix without leaking a meaningful fraction of
// it. Standard BYOK practice: enough to identify a key, useless to an
// attacker. Single source of the rule for every redacted-token surface.
func TokenSuffix(token string) string {
	const revealed = 4
	r := []rune(token)
	if len(r) < revealed*3 {
		return ""
	}
	return string(r[len(r)-revealed:])
}

// TokenDivergence reports the relationship between the environment bearer
// token and the stored key (file, then Keychain). Suffixes only — never the
// tokens themselves.
type TokenDivergence struct {
	EnvSet       bool
	StoredSet    bool
	Diverges     bool // env token set AND stored token set AND they differ
	EnvSuffix    string
	StoredSuffix string
}

// BedrockTokenDivergence detects the split-brain state where
// AWS_BEARER_TOKEN_BEDROCK in this process's environment overrides a
// DIFFERENT saved key: the app (no shell env) uses the new stored key while
// every terminal/MCP invocation silently keeps using the old env token.
func BedrockTokenDivergence() TokenDivergence {
	var kc func(string) (string, error)
	if keychainLookupEnabled() {
		kc = keychainGet
	}
	return bedrockTokenDivergence(os.Getenv, readBedrockFileToken, kc)
}

func bedrockTokenDivergence(getenv func(string) string, fileToken func() string, keychain func(string) (string, error)) TokenDivergence {
	env := strings.TrimSpace(getenv(bedrockBearerTokenEnv))
	stored := ""
	if fileToken != nil {
		stored = strings.TrimSpace(fileToken())
	}
	if stored == "" && keychain != nil {
		if t, err := keychain("bedrock"); err == nil {
			stored = strings.TrimSpace(t)
		}
	}
	return TokenDivergence{
		EnvSet:       env != "",
		StoredSet:    stored != "",
		Diverges:     env != "" && stored != "" && env != stored,
		EnvSuffix:    TokenSuffix(env),
		StoredSuffix: TokenSuffix(stored),
	}
}

// bedrockSkipKeychainEnv lets tests neutralize the login Keychain without
// deleting the developer's real item. Production never sets this.
const bedrockSkipKeychainEnv = "2NB_BEDROCK_SKIP_KEYCHAIN"

func keychainLookupEnabled() bool {
	return runtime.GOOS == "darwin" && os.Getenv(bedrockSkipKeychainEnv) == ""
}

func readBedrockFileToken() string {
	f, err := ReadBedrockFile()
	if err != nil {
		return ""
	}
	return f.Token
}

func fileIsPrivate(mode fs.FileMode) bool {
	return mode.Perm()&0o077 == 0
}

func hydrateBedrockBearerToken() {
	var kc func(string) (string, error)
	if keychainLookupEnabled() {
		kc = keychainGet
	}
	ensureBedrockBearerToken(os.Getenv, os.Setenv, readBedrockFileToken, kc)
}
