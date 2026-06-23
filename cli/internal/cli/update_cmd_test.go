package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeReleaseVersion(t *testing.T) {
	cases := map[string]string{
		"v0.10.1":  "0.10.1",
		"0.10.1":   "0.10.1",
		" v1.2.3 ": "1.2.3",
		"v":        "",
	}
	for in, want := range cases {
		if got := normalizeReleaseVersion(in); got != want {
			t.Errorf("normalizeReleaseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadUpdateCache_FreshVsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, []byte(`{"tag_name":"v1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fresh: just written, within TTL.
	if _, ok := readUpdateCache(path, true); !ok {
		t.Error("fresh cache should be readable with freshOnly=true")
	}

	// Age it past the TTL: freshOnly rejects it, but a non-fresh read still gets it.
	old := time.Now().Add(-updateCacheTTL - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := readUpdateCache(path, true); ok {
		t.Error("stale cache should be rejected with freshOnly=true")
	}
	if _, ok := readUpdateCache(path, false); !ok {
		t.Error("stale cache should still be readable with freshOnly=false")
	}
}

func TestFetchLatestReleaseVersion_FreshCacheShortCircuitsNetwork(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := writeUpdateCache(updateCachePath(), []byte(`{"tag_name":"v9.9.9"}`)); err != nil {
		t.Fatal(err)
	}
	// A fresh cache returns without any network call (background ctx, but the
	// cache is consulted first).
	got, err := fetchLatestReleaseVersion(context.Background(), false)
	if err != nil || got != "v9.9.9" {
		t.Errorf("got (%q, %v), want (v9.9.9, nil) from fresh cache", got, err)
	}
}

func TestFetchLatestReleaseVersion_ForceBypassesFreshCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := updateCachePath()
	if err := writeUpdateCache(path, []byte(`{"tag_name":"v9.9.9"}`)); err != nil {
		t.Fatal(err)
	}
	// force=true skips the fresh-cache read and attempts the network; a cancelled
	// context fails it immediately, so it must fall back to the (fresh) stale
	// cache rather than returning before trying — proving force bypassed the
	// short-circuit but stayed offline-safe.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := fetchLatestReleaseVersion(ctx, true)
	if err != nil || got != "v9.9.9" {
		t.Errorf("got (%q, %v), want (v9.9.9, nil) from cache fallback after forced fetch", got, err)
	}
}

func TestFetchLatestReleaseVersion_StaleCacheFallbackOnFetchFailure(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := updateCachePath()
	if err := writeUpdateCache(path, []byte(`{"tag_name":"v8.8.8"}`)); err != nil {
		t.Fatal(err)
	}
	// Age the cache past the TTL so the fresh read is skipped and a fetch is
	// attempted; a cancelled context makes the fetch fail immediately (no real
	// network), exercising the stale-cache fallback.
	old := time.Now().Add(-updateCacheTTL - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := fetchLatestReleaseVersion(ctx, false)
	if err != nil || got != "v8.8.8" {
		t.Errorf("got (%q, %v), want (v8.8.8, nil) from stale fallback", got, err)
	}
}

func TestUpdateCacheOlderThan(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := updateCachePath()

	// No cache file -> false (caller treats it as just-written / fresh, so the
	// install-ahead refetch path stays gated rather than firing every run).
	if updateCacheOlderThan(time.Hour) {
		t.Error("no cache should report not-older-than (false)")
	}

	if err := writeUpdateCache(path, []byte(`{"tag_name":"v1.0.0"}`)); err != nil {
		t.Fatal(err)
	}
	if updateCacheOlderThan(time.Hour) {
		t.Error("a just-written cache must be within the cooldown (false)")
	}

	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if !updateCacheOlderThan(time.Hour) {
		t.Error("a 2h-old cache must be older than a 1h cooldown (true)")
	}
}
