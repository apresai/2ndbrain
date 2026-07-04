package llama

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCacheAndStateDirsHonorXDG(t *testing.T) {
	cache := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", state)

	root, err := CacheRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cache, "2nb"); root != want {
		t.Errorf("CacheRoot = %q, want %q", root, want)
	}
	if dir, _ := EngineCacheDir(); dir != filepath.Join(cache, "2nb", "engine") {
		t.Errorf("EngineCacheDir = %q", dir)
	}
	if dir, _ := ModelsCacheDir(); dir != filepath.Join(cache, "2nb", "models") {
		t.Errorf("ModelsCacheDir = %q", dir)
	}
	if dir, _ := StateDir(); dir != filepath.Join(state, "2nb", "engine") {
		t.Errorf("StateDir = %q", dir)
	}
}

func TestModelPath(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p, err := ModelPath("embeddinggemma-300m", "weights.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "weights.gguf" || filepath.Base(filepath.Dir(p)) != "embeddinggemma-300m" {
		t.Errorf("ModelPath = %q, want <cache>/2nb/models/embeddinggemma-300m/weights.gguf", p)
	}
}

func TestLocateEngineOverride(t *testing.T) {
	dir := t.TempDir()
	// An override path must exist and be executable to win.
	missing := filepath.Join(dir, "nope")
	if got := LocateEngine(missing); got == missing {
		t.Errorf("LocateEngine accepted a non-existent override %q", missing)
	}

	bin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := LocateEngine(bin); got != bin {
		t.Errorf("LocateEngine(override) = %q, want %q", got, bin)
	}
}

func TestLocateEngineFromPATH(t *testing.T) {
	// Force the cache/bundle branches to miss by pointing them at empty dirs.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	want := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := lookPath
	lookPath = func(name string) (string, error) {
		if name == engineBinaryName {
			return want, nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { lookPath = orig })

	if got := LocateEngine(""); got != want {
		t.Errorf("LocateEngine() = %q, want PATH result %q", got, want)
	}
}

func TestIsExecutableFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "exe")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isExecutableFile(exe) {
		t.Error("expected exe to be executable")
	}
	if runtime.GOOS != "windows" && isExecutableFile(plain) {
		t.Error("expected non-0111 file to be non-executable on unix")
	}
	if isExecutableFile(dir) {
		t.Error("a directory is not an executable file")
	}
}
