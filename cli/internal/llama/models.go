package llama

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ModelArtifact is one downloadable GGUF weight file, pinned by URL + sha256.
// Weights are always fetched at runtime and cached under ModelsCacheDir — they
// are never shipped in the app bundle or the release archive (100s of MB to GB).
type ModelArtifact struct {
	ID        string // catalog model id, e.g. "embeddinggemma-300m"
	Role      Role   // which server role loads it
	File      string // filename within <models>/<id>/
	URL       string // https download URL (GitHub release mirror or Hugging Face)
	SHA256    string // lower-case hex; EnsureModel refuses to accept a download that doesn't match
	SizeBytes int64  // expected size, for the readiness/RAM report and a cheap presence check
	Dims      int    // embedding width (embedding models only)
	License   string // SPDX-ish tag, for the readiness report ("apache-2.0", "mit", ...)
}

// ModelManifest pins the local Gemma-family models. GGUF weights are
// arch-independent, so there is no GOARCH key here (only the engine binary is
// arch-specific — see the engine manifest, added with the download flow in the
// packaging phase).
//
// SHA256 values are intentionally empty until the hosting decision is made
// (mirror on the 2ndbrain GitHub releases vs. Hugging Face direct) and the real
// hashes are pinned. EnsureModel FAILS CLOSED on an empty SHA256 rather than
// accepting an unverified multi-GB download, so `2nb ai engine pull` will report
// "not yet pinned" for these until the hashes land.
var ModelManifest = map[string]ModelArtifact{
	"embeddinggemma-300m": {
		ID:      "embeddinggemma-300m",
		Role:    RoleEmbed,
		File:    "embeddinggemma-300m-Q8_0.gguf",
		URL:     "", // TODO(pin): host + sha256 (UNKNOWN 3)
		SHA256:  "",
		Dims:    768,
		License: "apache-2.0",
	},
	"gemma4-e4b": {
		ID:      "gemma4-e4b",
		Role:    RoleGen,
		File:    "gemma-4-e4b-it-Q4_K_M.gguf",
		URL:     "", // TODO(pin)
		SHA256:  "",
		License: "apache-2.0",
	},
	"gemma4-e2b": {
		ID:      "gemma4-e2b",
		Role:    RoleGen,
		File:    "gemma-4-e2b-it-Q4_K_M.gguf",
		URL:     "", // TODO(pin)
		SHA256:  "",
		License: "apache-2.0",
	},
	"bge-reranker-v2-m3": {
		ID:      "bge-reranker-v2-m3",
		Role:    RoleRerank,
		File:    "bge-reranker-v2-m3-Q8_0.gguf",
		URL:     "", // TODO(pin): confirm reranker license permits redistribution (UNKNOWN 3)
		SHA256:  "",
		License: "", // TODO(pin): verify
	},
}

// ArtifactFor looks up a model artifact by id.
func ArtifactFor(id string) (ModelArtifact, bool) {
	a, ok := ModelManifest[id]
	return a, ok
}

// ModelStatusInfo is a cheap (no full-hash) status of a cached model.
type ModelStatusInfo struct {
	ID        string `json:"id"`
	Known     bool   `json:"known"`   // present in the manifest
	Present   bool   `json:"present"` // file exists on disk
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	SizeMatch bool   `json:"size_match"` // on-disk size == manifest SizeBytes (when known)
	Pinned    bool   `json:"pinned"`     // manifest has a sha256 to verify against
}

// ModelStatus reports whether a model is cached, using a cheap size check (it
// does NOT re-hash a multi-GB file on every call — use VerifyModel for that).
func ModelStatus(id string) ModelStatusInfo {
	st := ModelStatusInfo{ID: id}
	art, ok := ArtifactFor(id)
	if !ok {
		return st
	}
	st.Known = true
	st.Pinned = art.SHA256 != ""
	path, err := ModelPath(id, art.File)
	if err != nil {
		return st
	}
	st.Path = path
	info, err := os.Stat(path)
	if err != nil {
		return st
	}
	st.Present = true
	st.SizeBytes = info.Size()
	st.SizeMatch = art.SizeBytes == 0 || info.Size() == art.SizeBytes
	return st
}

// VerifyModel re-hashes the cached file and reports whether it matches the
// pinned sha256. Returns (false, nil) when the model is absent; an error only
// for I/O problems.
func VerifyModel(id string) (bool, error) {
	art, ok := ArtifactFor(id)
	if !ok {
		return false, fmt.Errorf("unknown model %q", id)
	}
	if art.SHA256 == "" {
		return false, nil
	}
	path, err := ModelPath(id, art.File)
	if err != nil {
		return false, err
	}
	sum, err := fileSHA256(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return sum == art.SHA256, nil
}

// EnsureModel returns the local path to a verified copy of the model, fetching
// it if necessary. It is idempotent: a cached file whose sha256 already matches
// is returned without a network call. A download is streamed to a .part file,
// hashed while writing, verified against the pinned sha256, and atomically
// renamed into place — a mismatch deletes the partial and errors, so a
// truncated or wrong download is never accepted.
//
// It FAILS CLOSED when the manifest has no pinned sha256 (returns an error
// instead of accepting an unverified download) and when the manifest has no
// URL.
func EnsureModel(ctx context.Context, id string) (string, error) {
	art, ok := ArtifactFor(id)
	if !ok {
		return "", fmt.Errorf("unknown model %q (not in the manifest)", id)
	}
	dest, err := ModelPath(id, art.File)
	if err != nil {
		return "", err
	}

	// Fast path: already cached and verified.
	if art.SHA256 != "" {
		if sum, err := fileSHA256(dest); err == nil && sum == art.SHA256 {
			return dest, nil
		}
	}

	if art.SHA256 == "" {
		return "", fmt.Errorf("model %q has no pinned sha256 in the manifest; refusing an unverified download", id)
	}
	if art.URL == "" {
		return "", fmt.Errorf("model %q has no download URL pinned in the manifest", id)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	tmp := dest + ".part"
	sum, err := downloadTo(ctx, art.URL, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if sum != art.SHA256 {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("model %q sha256 mismatch: got %s, want %s", id, sum, art.SHA256)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("install %q: %w", id, err)
	}
	return dest, nil
}

// downloadHTTPClient has no overall timeout (model files are large); callers
// bound the fetch with the context.
var downloadHTTPClient = &http.Client{Timeout: 0}

// downloadTo streams url into path and returns the lower-case hex sha256 of the
// bytes written, computed on the fly (no second read of the file).
func downloadTo(ctx context.Context, url, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := downloadHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileSHA256 streams a file and returns its lower-case hex sha256.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
