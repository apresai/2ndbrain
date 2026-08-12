package ai

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const bedrockFileName = "bedrock.json"

// BedrockFile is the machine-local Bedrock credentials document at
// ~/.config/2nb/bedrock.json (XDG-aware). It is never written into a vault.
type BedrockFile struct {
	Region string `json:"region,omitempty"`
	Token  string `json:"token,omitempty"`
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
	return out, nil
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
func UpdateBedrockFile(region string, token *string, clearRegion bool) error {
	cur, err := ReadBedrockFile()
	if err != nil {
		return err
	}
	if clearRegion {
		cur.Region = ""
	} else if strings.TrimSpace(region) != "" {
		cur.Region = strings.TrimSpace(region)
	}
	if token != nil {
		cur.Token = strings.TrimSpace(*token)
	}
	return WriteBedrockFile(cur)
}

// ResolveBedrockConfig overlays the machine-file region onto vault config
// when the file names one. An empty result falls back to us-east-1.
func ResolveBedrockConfig(vault BedrockConfig) BedrockConfig {
	out := vault
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
