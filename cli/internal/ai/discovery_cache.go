package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Bedrock vendor discovery is expensive in wall time, not money:
// ListFoundationModels plus a paginated ListInferenceProfiles plus a
// GetFoundationModel per profile the first call didn't already describe. That
// is fine once for `models list --discover`, but `models verify --discover`
// runs it on every validation pass, where the catalog is effectively static
// between AWS releases. So discovery gets the same treatment as live pricing:
// a 24h disk cache under the XDG cache dir, with a stale entry used as the
// fallback when the live call fails (air-gapped, expired credentials).
//
// The cache key is region + profile, the only account identity available
// without a network call. Two AWS accounts reached through the SAME profile
// name and region would therefore share an entry — acceptable because
// discovery only decides which models are OFFERED; whether this account can
// invoke one is answered by a real probe (`models verify`), never by this
// listing.

const discoveryCacheTTL = 24 * time.Hour

var discoveryCacheMu sync.Mutex

// cachedDiscovery is the on-disk envelope. Version guards against a future
// ModelInfo change silently deserializing into nonsense.
type cachedDiscovery struct {
	Version int         `json:"version"`
	Region  string      `json:"region"`
	Models  []ModelInfo `json:"models"`
}

const discoveryCacheVersion = 1

// ListBedrockVendorModelsCached is ListBedrockVendorModels with a 24h disk
// cache. A fresh entry short-circuits the AWS calls entirely; on a live
// failure a stale entry is returned instead of the error, so a discovery-
// backed flow degrades to yesterday's catalog rather than to nothing.
func ListBedrockVendorModelsCached(ctx context.Context, cfg BedrockConfig) ([]ModelInfo, error) {
	cfg = ResolveBedrockConfig(cfg)
	path, pathErr := discoveryCachePath(cfg)

	discoveryCacheMu.Lock()
	defer discoveryCacheMu.Unlock()

	if pathErr == nil {
		if models, ok := readDiscoveryCache(path, cfg.Region, true); ok {
			return models, nil
		}
	}

	models, err := ListBedrockVendorModels(ctx, cfg)
	if err != nil {
		if pathErr == nil {
			if stale, ok := readDiscoveryCache(path, cfg.Region, false); ok {
				slog.Warn("bedrock discovery failed, using stale cache", "path", path, "err", err)
				return stale, nil
			}
		}
		return nil, err
	}
	if pathErr == nil {
		if writeErr := writeDiscoveryCache(path, cfg.Region, models); writeErr != nil {
			slog.Debug("write discovery cache failed", "path", path, "err", writeErr)
		}
	}
	return models, nil
}

func discoveryCachePath(cfg BedrockConfig) (string, error) {
	dir, err := discoveryCacheDir()
	if err != nil {
		return "", err
	}
	profile := cfg.Profile
	if profile == "" {
		profile = "default"
	}
	name := fmt.Sprintf("bedrock-%s-%s.json", sanitizeCacheKey(cfg.Region), sanitizeCacheKey(profile))
	return filepath.Join(dir, name), nil
}

func discoveryCacheDir() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "2nb", "discovery"), nil
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "2nb", "discovery"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "2nb", "discovery"), nil
}

// sanitizeCacheKey keeps a filename component to a safe alphabet so a
// hand-edited region/profile can never escape the cache directory.
func sanitizeCacheKey(s string) string {
	if s == "" {
		return "none"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func readDiscoveryCache(path, region string, freshOnly bool) ([]ModelInfo, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if freshOnly && time.Since(info.ModTime()) > discoveryCacheTTL {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var doc cachedDiscovery
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	if doc.Version != discoveryCacheVersion || doc.Region != region || len(doc.Models) == 0 {
		return nil, false
	}
	return doc.Models, true
}

func writeDiscoveryCache(path, region string, models []ModelInfo) error {
	if len(models) == 0 {
		// Never cache an empty listing: it is indistinguishable from a
		// permissions problem and would suppress discovery for 24h.
		return nil
	}
	data, err := json.Marshal(cachedDiscovery{
		Version: discoveryCacheVersion,
		Region:  region,
		Models:  models,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
