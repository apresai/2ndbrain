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
	"strings"
	"time"
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

// ModelManifest pins the local Gemma-family models to UNGATED Hugging Face GGUF
// repos — the same source re:Gist's iOS app uses (Unsloth's Gemma GGUF
// conversions are ungated even though the base Gemma weights are license-gated
// on HF). GGUF weights are arch-independent, so there is no GOARCH key here (only
// the engine binary is arch-specific). Each SHA256 is the file's Hugging Face LFS
// oid; EnsureModel streams + hashes the download and FAILS CLOSED on a mismatch.
// The Gemma models are governed by the Gemma Terms of Use
// (https://ai.google.dev/gemma/terms) — see THIRD_PARTY_NOTICES.
var ModelManifest = map[string]ModelArtifact{
	"embeddinggemma-300m": {
		ID:        "embeddinggemma-300m",
		Role:      RoleEmbed,
		File:      "embeddinggemma-300m-Q8_0.gguf",
		URL:       "https://huggingface.co/ggml-org/embeddinggemma-300M-GGUF/resolve/main/embeddinggemma-300M-Q8_0.gguf",
		SHA256:    "b5ce9d77a3fc4b3b39ccb5643c36777911cc4eb46a66962eadfa3f5f60490d63",
		SizeBytes: 333590944,
		Dims:      768,
		License:   "gemma", // Gemma Terms of Use
	},
	"gemma4-e4b": {
		ID:        "gemma4-e4b",
		Role:      RoleGen,
		File:      "gemma-4-e4b-it-Q4_K_M.gguf",
		URL:       "https://huggingface.co/unsloth/gemma-4-E4B-it-GGUF/resolve/main/gemma-4-E4B-it-Q4_K_M.gguf",
		SHA256:    "519b9793ed6ce0ff530f1b7c96e848e08e49e7af4d57bb97f76215963a54146d",
		SizeBytes: 4977169568,
		License:   "gemma",
	},
	"gemma4-e2b": {
		ID:        "gemma4-e2b",
		Role:      RoleGen,
		File:      "gemma-4-e2b-it-Q4_K_M.gguf",
		URL:       "https://huggingface.co/unsloth/gemma-4-E2B-it-GGUF/resolve/main/gemma-4-E2B-it-Q4_K_M.gguf",
		SHA256:    "9378bc471710229ef165709b62e34bfb62231420ddaf6d729e727305b5b8672d",
		SizeBytes: 3106736256,
		License:   "gemma",
	},
	"bge-reranker-v2-m3": {
		ID:        "bge-reranker-v2-m3",
		Role:      RoleRerank,
		File:      "bge-reranker-v2-m3-Q8_0.gguf",
		URL:       "https://huggingface.co/gpustack/bge-reranker-v2-m3-GGUF/resolve/main/bge-reranker-v2-m3-Q8_0.gguf",
		SHA256:    "a43c7c9b11a4c1517e5bf95151960e1621d1b72f7a493364b01e386cf1aaa1d3",
		SizeBytes: 635676416,
		License:   "apache-2.0",
	},
}

// ArtifactFor looks up a model artifact by id.
func ArtifactFor(id string) (ModelArtifact, bool) {
	a, ok := ModelManifest[id]
	return a, ok
}

// RemoveModel deletes a cached model's directory (<models>/<id>) and returns the
// bytes freed. It refuses an id containing a path separator (or `.`/`..`) so it
// can never escape the models cache. A not-present model is a no-op (freed 0).
func RemoveModel(id string) (int64, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return 0, fmt.Errorf("invalid model id %q", id)
	}
	dir, err := ModelsCacheDir()
	if err != nil {
		return 0, err
	}
	target := filepath.Join(dir, id)
	freed := modelDirSize(target)
	if err := os.RemoveAll(target); err != nil {
		return 0, err
	}
	return freed, nil
}

// modelDirSize sums the sizes of the files directly in a model dir (which is
// flat: <id>/<file.gguf>). Returns 0 if the dir is absent or unreadable.
func modelDirSize(path string) int64 {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
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
	return EnsureModelProgress(ctx, id, nil)
}

// ProgressFunc reports download progress: bytes done and the total (or -1 when
// the server doesn't report a Content-Length).
type ProgressFunc func(done, total int64)

// EnsureModelProgress is EnsureModel with a progress callback, invoked a few
// times per second during the (multi-GB) download so the CLI/GUI can render a bar.
func EnsureModelProgress(ctx context.Context, id string, onProgress ProgressFunc) (string, error) {
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
	sum, err := downloadTo(ctx, art.URL, tmp, art.SizeBytes, onProgress)
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

// downloadStallTimeout bounds a STALLED transfer (a half-open socket delivering
// no bytes) without capping the total time — a legitimately slow multi-GB pull
// that keeps making progress is never aborted, but a dead connection can't hang
// forever. A blanket http.Client.Timeout would wrongly kill slow downloads.
const downloadStallTimeout = 60 * time.Second

// downloadTo streams url into path and returns the lower-case hex sha256 of the
// bytes written, computed on the fly (no second read of the file). When
// onProgress is non-nil it is invoked ~5x/sec with (bytesDone, total); total
// prefers the response Content-Length, falling back to the manifest's expected
// size, or -1 when neither is known.
func downloadTo(ctx context.Context, url, path string, total int64, onProgress ProgressFunc) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	} else if total <= 0 {
		total = -1
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &progressReader{r: resp.Body, total: total, onProgress: onProgress}
	}
	// Idle watchdog: cancel the request if no bytes arrive for the stall timeout.
	watchdog := time.AfterFunc(downloadStallTimeout, cancel)
	defer watchdog.Stop()
	src = &stallReader{r: src, reset: func() { watchdog.Reset(downloadStallTimeout) }}
	if _, err := io.Copy(io.MultiWriter(f, h), src); err != nil {
		f.Close()
		if ctx.Err() != nil {
			return "", fmt.Errorf("download %s stalled: no data for %s", url, downloadStallTimeout)
		}
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// progressReader wraps the download body, reporting cumulative bytes (throttled
// to ~5x/sec, plus a final report on EOF/error) to onProgress as it is read.
type progressReader struct {
	r          io.Reader
	done, total int64
	onProgress ProgressFunc
	last       time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if now := time.Now(); err != nil || now.Sub(p.last) >= 200*time.Millisecond {
		p.onProgress(p.done, p.total)
		p.last = now
	}
	return n, err
}

// stallReader resets an idle watchdog on each non-empty read, so a transfer that
// stops delivering bytes is cancelled by the watchdog rather than blocking the
// io.Copy forever.
type stallReader struct {
	r     io.Reader
	reset func()
}

func (s *stallReader) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	if n > 0 {
		s.reset()
	}
	return n, err
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
