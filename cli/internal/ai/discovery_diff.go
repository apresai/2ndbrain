package ai

// Discovery freshness, invalidation, and the NEW/GONE diff behind
// `2nb models discover`.
//
// The 24h discovery cache (discovery_cache.go) answers "what did the vendor
// planes list last time we looked", but until this file nothing reported HOW
// OLD that answer was, nothing could drop it on demand, and nothing compared
// one listing against the previous one. Three pieces close that:
//
//   - DiscoveryCacheAges stats the per-region cache files (classic
//     bedrock-<region>-<profile>.json and mantle
//     bedrock-mantle-<region>-<profile>.json) and reports each source's
//     fetched-at (the file mtime; writeDiscoveryCache writes at fetch time,
//     so mtime IS the fetch time) and staleness against the 24h TTL.
//   - InvalidateDiscoveryCache deletes those files, so the next CACHED
//     read-through performs a live walk and re-warms the cache.
//   - The seen baseline (Load/SaveDiscoverySeen + DiffAgainstSeen) is a
//     dedicated discovery-seen-bedrock-<profile>.json in the same cache dir,
//     holding the "provider|id" key set of the last reported pool. It is
//     deliberately a separate file from the caches: --refresh deletes the
//     caches, so they cannot double as the diff baseline. It is machine-local
//     (never the vault) for the same reason the GUI's UserDefaults snapshot
//     is: a synced vault sidecar would mis-badge models on a second machine
//     that has genuinely never seen them.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DiscoverySourceAge reports the freshness of one discovery cache file.
// Source is "classic" (the Bedrock control-plane listing) or "mantle" (the
// plane's /v1/models listing); each is cached per region + profile.
type DiscoverySourceAge struct {
	Source string `json:"source"`
	Region string `json:"region"`
	Path   string `json:"path,omitempty"`
	Exists bool   `json:"exists"`
	// FetchedAt is the cache file's mtime (RFC3339, UTC): the moment the
	// listing was actually fetched. Empty when no cache file exists.
	FetchedAt  string `json:"fetched_at,omitempty"`
	AgeSeconds int64  `json:"age_seconds,omitempty"`
	// Stale means the entry is older than the 24h TTL: the next cached
	// discovery read-through will attempt a live walk (keeping this entry
	// only as the failure fallback).
	Stale bool `json:"stale"`
}

// Age returns the source's age as a duration (0 when no cache file exists).
func (a DiscoverySourceAge) Age() time.Duration {
	return time.Duration(a.AgeSeconds) * time.Second
}

// DiscoveryCacheAges reports the freshness of every discovery cache file the
// current config would read: one classic row per included Bedrock region
// (primary first), then one mantle row per documented mantle region. Mantle
// rows on a SigV4-only setup (no bearer token, so mantle discovery is skipped
// entirely) are included only when a cache file exists from an earlier
// tokened run: reporting "missing" for a plane the setup cannot reach would
// read as a defect.
func DiscoveryCacheAges(cfg BedrockConfig) []DiscoverySourceAge {
	var out []DiscoverySourceAge
	for _, region := range ResolveBedrockRegions(cfg) {
		path, err := classicDiscoveryCachePathForRegion(cfg, region)
		out = append(out, discoverySourceAge("classic", region, path, err))
	}
	mantleReachable := resolveMantleBearerToken() != ""
	for _, region := range mantleDiscoveryRegions(cfg) {
		path, err := mantleDiscoveryCachePath(cfg, region)
		age := discoverySourceAge("mantle", region, path, err)
		if !mantleReachable && !age.Exists {
			continue
		}
		out = append(out, age)
	}
	return out
}

// classicDiscoveryCachePathForRegion resolves the classic cache file for one
// region exactly the way the cached listing does (RegionOverride through
// ResolveBedrockConfig), so the freshness report and the invalidation can
// never point at a different file than the read-through uses.
func classicDiscoveryCachePathForRegion(cfg BedrockConfig, region string) (string, error) {
	rcfg := cfg
	rcfg.RegionOverride = region
	return discoveryCachePath(ResolveBedrockConfig(rcfg))
}

func discoverySourceAge(source, region, path string, pathErr error) DiscoverySourceAge {
	a := DiscoverySourceAge{Source: source, Region: region, Path: path}
	if pathErr != nil || path == "" {
		return a
	}
	info, err := os.Stat(path)
	if err != nil {
		return a
	}
	a.Exists = true
	fetched := info.ModTime()
	a.FetchedAt = fetched.UTC().Format(time.RFC3339)
	age := time.Since(fetched)
	if age < 0 {
		age = 0
	}
	a.AgeSeconds = int64(age / time.Second)
	a.Stale = age > discoveryCacheTTL
	return a
}

// InvalidateDiscoveryCache deletes this profile's discovery cache files
// (classic per included region, mantle per documented mantle region) and
// returns the paths actually removed. Missing files are not errors. After
// this, a CACHED discovery read-through finds nothing fresh, performs the
// live walk, and re-warms the cache (which a DiscoverCached:false walk would
// leave cold). The discovery-seen baseline is deliberately NOT touched: it is
// the NEW/GONE diff baseline, not a cache of vendor state.
func InvalidateDiscoveryCache(cfg BedrockConfig) ([]string, error) {
	var paths []string
	for _, region := range ResolveBedrockRegions(cfg) {
		if p, err := classicDiscoveryCachePathForRegion(cfg, region); err == nil {
			paths = append(paths, p)
		}
	}
	for _, region := range mantleDiscoveryRegions(cfg) {
		if p, err := mantleDiscoveryCachePath(cfg, region); err == nil {
			paths = append(paths, p)
		}
	}

	discoveryCacheMu.Lock()
	defer discoveryCacheMu.Unlock()
	var removed []string
	var firstErr error
	for _, p := range paths {
		err := os.Remove(p)
		switch {
		case err == nil:
			removed = append(removed, p)
		case os.IsNotExist(err):
			// nothing cached for this source; fine
		case firstErr == nil:
			firstErr = err
		}
	}
	return removed, firstErr
}

// discoverySeenVersion guards the seen-file shape. A mismatch is treated as
// "no baseline" (silent re-seed), never an error: the baseline is a
// convenience for the NEW badge, not state worth failing over.
const discoverySeenVersion = 1

// DiscoverySeen is the on-disk baseline for the NEW/GONE diff: the
// "provider|id" keys of every model the last successful discovery reported.
type DiscoverySeen struct {
	Version int      `json:"version"`
	SavedAt string   `json:"saved_at"`
	Keys    []string `json:"keys"`
}

// DiscoverySeenKey is the baseline key for one model: "provider|id", the
// same shape as the GUI's UserDefaults discovery snapshot keys.
func DiscoverySeenKey(m ModelInfo) string {
	return m.Provider + "|" + m.ID
}

func seenKeyProvider(key string) string {
	provider, _, ok := strings.Cut(key, "|")
	if !ok {
		return ""
	}
	return provider
}

// discoverySeenPath keys the baseline per profile only (not per region): the
// seen set spans every region and both Bedrock planes, plus whatever the
// non-Bedrock providers discover.
func discoverySeenPath(cfg BedrockConfig) (string, error) {
	dir, err := discoveryCacheDir()
	if err != nil {
		return "", err
	}
	profile := cfg.Profile
	if profile == "" {
		profile = "default"
	}
	return filepath.Join(dir, fmt.Sprintf("discovery-seen-bedrock-%s.json", sanitizeCacheKey(profile))), nil
}

// LoadDiscoverySeen reads the baseline. A missing file returns (nil, nil),
// the first-run signal DiffAgainstSeen turns into a silent seed. A corrupt
// or version-mismatched file is logged and also returns (nil, nil): the
// next save rewrites it.
func LoadDiscoverySeen(cfg BedrockConfig) (*DiscoverySeen, error) {
	path, err := discoverySeenPath(cfg)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var seen DiscoverySeen
	if err := json.Unmarshal(data, &seen); err != nil {
		slog.Warn("parse discovery seen baseline failed; re-seeding", "path", path, "err", err)
		return nil, nil
	}
	if seen.Version != discoverySeenVersion {
		slog.Warn("discovery seen baseline version mismatch; re-seeding", "path", path, "version", seen.Version)
		return nil, nil
	}
	return &seen, nil
}

// SaveDiscoverySeen writes the baseline key set (sorted, deduped). Callers
// save only after a successful listing, so a failed run can never shrink the
// baseline and re-announce everything as NEW afterwards.
func SaveDiscoverySeen(cfg BedrockConfig, keys []string) error {
	path, err := discoverySeenPath(cfg)
	if err != nil {
		return err
	}
	sorted := dedupeSorted(keys)
	data, err := json.Marshal(DiscoverySeen{
		Version: discoverySeenVersion,
		SavedAt: time.Now().UTC().Format(time.RFC3339),
		Keys:    sorted,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func dedupeSorted(keys []string) []string {
	set := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" || set[k] {
			continue
		}
		set[k] = true
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DiscoveryDiff is the NEW/GONE comparison of the current discovered pool
// against the seen baseline.
type DiscoveryDiff struct {
	// New holds current models whose key is absent from the baseline.
	New []ModelInfo `json:"new"`
	// Gone holds baseline keys absent from the current pool, excluding keys
	// from providers whose discovery failed this run (unknown, not gone) and
	// keys now present in the merged catalog (adopted, not delisted).
	Gone []string `json:"gone"`
	// FirstRun is true when no baseline existed: the caller seeds silently
	// (reports the pool, no NEW badge) and saves.
	FirstRun bool `json:"first_run,omitempty"`
}

// DiffAgainstSeen compares the current discovered pool against the seen
// baseline. A nil seen (no baseline file) is the first run: New and Gone stay
// empty so a fresh machine's entire catalog is never badged as news; the
// caller saves the baseline and future runs diff against it.
//
// Two kinds of absence are deliberately NOT gone: keys from providers whose
// discovery failed this run (failed, from FailedDiscoveryProviders; a source
// that did not answer proves nothing about what it no longer lists), and keys
// now present in the merged catalog (catalog: a discovered model that was
// adopted via --add or a verify save leaves the pool by graduating, not by
// being delisted; without this exclusion every --add would badge its own
// model GONE on the next run). Catalog keys never join New: they are not
// discoveries, and badging the whole builtin catalog as news would drown the
// signal. New and Gone are always non-nil so JSON emits [] rather than null.
func DiffAgainstSeen(current []ModelInfo, catalog []ModelInfo, seen *DiscoverySeen, failed map[string]bool) DiscoveryDiff {
	d := DiscoveryDiff{New: []ModelInfo{}, Gone: []string{}}
	if seen == nil {
		d.FirstRun = true
		return d
	}
	seenSet := make(map[string]bool, len(seen.Keys))
	for _, k := range seen.Keys {
		seenSet[k] = true
	}
	currentSet := make(map[string]bool, len(current))
	for _, m := range current {
		key := DiscoverySeenKey(m)
		// Report each newly-seen MODEL once, not once per route. Since
		// discovery began emitting one row per (plane, region), a single new
		// model can arrive as several rows — grok-4.6 alone lists on two
		// planes across three regions — and appending per row would announce
		// "6 new models" for one. The seen baseline is deliberately keyed by
		// model, not route (re-keying it would re-badge every user's whole
		// catalog as NEW), so deduping here keeps the diff aligned with it.
		if currentSet[key] {
			continue
		}
		currentSet[key] = true
		if !seenSet[key] {
			d.New = append(d.New, m)
		}
	}
	catalogSet := make(map[string]bool, len(catalog))
	for _, m := range catalog {
		catalogSet[DiscoverySeenKey(m)] = true
	}
	for _, k := range dedupeSorted(seen.Keys) {
		if currentSet[k] || catalogSet[k] || failed[seenKeyProvider(k)] {
			continue
		}
		d.Gone = append(d.Gone, k)
	}
	return d
}

// NextSeenKeys builds the baseline to persist after this listing: every
// current key, plus previously-seen keys from providers whose discovery
// failed this run. Carrying the failed providers' keys forward means a
// transient source outage (Ollama stopped, an expired key) neither floods
// GONE now nor re-announces that provider's whole catalog as NEW when it
// recovers.
func NextSeenKeys(current []ModelInfo, seen *DiscoverySeen, failed map[string]bool) []string {
	keys := make([]string, 0, len(current))
	for _, m := range current {
		keys = append(keys, DiscoverySeenKey(m))
	}
	if seen != nil {
		for _, k := range seen.Keys {
			if failed[seenKeyProvider(k)] {
				keys = append(keys, k)
			}
		}
	}
	return dedupeSorted(keys)
}

// FailedDiscoveryProviders parses BuildModelList's discovery warnings
// ("<source> discovery failed: ...", minted in discoverVendorModels) into
// the provider slugs the seen keys use. bedrock-mantle maps to bedrock:
// mantle rows carry provider "bedrock", so their baseline keys are
// indistinguishable from classic ones, so a mantle outage shields
// ALL bedrock keys from GONE that run, the conservative direction.
func FailedDiscoveryProviders(warnings []string) map[string]bool {
	out := map[string]bool{}
	for _, w := range warnings {
		source, _, ok := strings.Cut(w, " discovery failed")
		if !ok || source == "" || strings.Contains(source, " ") {
			continue
		}
		if source == "bedrock-mantle" {
			source = "bedrock"
		}
		out[source] = true
	}
	return out
}
