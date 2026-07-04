// Package llama manages a bundled or downloaded llama.cpp `llama-server`
// process as a CGO-free local inference engine behind the "llama-local"
// provider. The Go binary never links llama.cpp: it starts llama-server as a
// separate OS process and talks to it over localhost HTTP, exactly the way the
// Ollama provider talks to the Ollama daemon. This keeps the shipped `2nb`
// binary CGO_ENABLED=0 and cross-compilable from a single host.
//
// Three concerns live here:
//   - locate.go  — resolve the engine binary + weights + cache/state dirs
//   - models.go  — the pinned model manifest, download+verify, status
//   - manager.go — the always-on supervisor + launchd integration
package llama

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// engineBinaryName is the llama.cpp server executable 2nb drives.
const engineBinaryName = "llama-server"

// lookPath is exec.LookPath, indirected so tests can control PATH resolution.
var lookPath = exec.LookPath

// Role names a llama-server process. llama-server's --embeddings and
// --reranking flags are mutually exclusive, so embeddings, reranking, and
// generation each require their own process.
type Role string

const (
	RoleGen    Role = "gen"
	RoleEmbed  Role = "embed"
	RoleRerank Role = "rerank"
)

// AllRoles is the canonical ordering used by status/serve loops.
var AllRoles = []Role{RoleGen, RoleEmbed, RoleRerank}

// CacheRoot returns the machine-local store for downloaded engine binaries and
// model weights: $XDG_CACHE_HOME/2nb, else os.UserCacheDir()/2nb (macOS:
// ~/Library/Caches/2nb), else ~/.cache/2nb. Mirrors pricingCacheDir in the ai
// package so the two caches sit under one root.
func CacheRoot() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "2nb"), nil
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "2nb"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "2nb"), nil
}

// EngineCacheDir is where a downloaded llama-server binary is stored.
func EngineCacheDir() (string, error) {
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "engine"), nil
}

// ModelsCacheDir is where downloaded GGUF weights are stored.
func ModelsCacheDir() (string, error) {
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "models"), nil
}

// StateDir is the machine-scoped runtime directory for the engine's sidecar
// registry and launchd artifacts: os.UserConfigDir()/2nb/engine (macOS:
// ~/Library/Application Support/2nb/engine). It is machine-scoped, not
// per-vault, because one always-on engine serves every vault.
func StateDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "2nb", "engine"), nil
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "2nb", "engine"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "2nb", "engine"), nil
}

// ModelPath returns the on-disk path a model artifact is cached at:
// <models>/<id>/<file>.
func ModelPath(id, file string) (string, error) {
	dir, err := ModelsCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id, file), nil
}

// LocateEngine resolves the llama-server binary, checking, in order:
//  1. override (config ai.llama.engine_path), when set;
//  2. a bundled sibling of the running executable — in the macOS app this is
//     SecondBrain.app/Contents/Resources/llama-server (next to the bundled
//     2nb), in the Obsidian plugin <plugin>/bin/llama-server;
//  3. the downloaded copy in EngineCacheDir();
//  4. anything named llama-server on PATH.
//
// Returns "" when none is found; callers decide whether to error, download, or
// mark the provider unavailable.
func LocateEngine(override string) string {
	if override != "" && isExecutableFile(override) {
		return override
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		sibling := filepath.Join(filepath.Dir(exe), engineBinaryName)
		if isExecutableFile(sibling) {
			return sibling
		}
	}
	if dir, err := EngineCacheDir(); err == nil {
		cached := filepath.Join(dir, engineBinaryName)
		if isExecutableFile(cached) {
			return cached
		}
	}
	if p, err := lookPath(engineBinaryName); err == nil {
		return p
	}
	return ""
}

// EngineAvailable reports whether an engine binary can be found for the given
// override. It does not check that it can actually run (that is a /health probe
// against a running server).
func EngineAvailable(override string) bool { return LocateEngine(override) != "" }

// HostSupportsMetal reports whether the current host is Apple Silicon macOS,
// the primary (Metal) target for the bundled engine. Intel Macs and other
// platforms need a CPU-only engine build or should fall back to Ollama/Bedrock.
func HostSupportsMetal() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	// On Unix, require an executable bit; on other OSes os.Stat can't tell, so
	// presence is enough (LocateEngine's PATH branch handles the rest).
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0111 != 0
}
