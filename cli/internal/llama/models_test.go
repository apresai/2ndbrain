package llama

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestArtifactsWellFormed(t *testing.T) {
	for id, art := range ModelManifest {
		if art.ID != id {
			t.Errorf("manifest key %q != artifact.ID %q", id, art.ID)
		}
		if art.File == "" {
			t.Errorf("%s: empty File", id)
		}
		switch art.Role {
		case RoleGen, RoleEmbed, RoleRerank:
		default:
			t.Errorf("%s: invalid Role %q", id, art.Role)
		}
		// Every shipped entry must be fully pinned (ungated HF URL + LFS sha256 +
		// size), so a future entry added without a pin fails CI rather than
		// shipping a non-downloadable model.
		if art.URL == "" {
			t.Errorf("%s: empty URL — must be pinned to an ungated GGUF", id)
		}
		if len(art.SHA256) != 64 {
			t.Errorf("%s: SHA256 must be a 64-char hex digest, got %q", id, art.SHA256)
		}
		if art.SizeBytes <= 0 {
			t.Errorf("%s: SizeBytes must be > 0 (drives the disk/readiness report)", id)
		}
	}
	if _, ok := ArtifactFor("does-not-exist"); ok {
		t.Error("ArtifactFor returned ok for an unknown id")
	}
}

func TestEnsureModelFailsClosedWithoutPinnedSHA(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// A manifest entry with no pinned sha256 must be refused rather than fetched
	// as unverified bytes. Shipped entries are now all pinned, so inject an
	// unpinned fixture to exercise the guard (the empty-sha check fires before
	// any network call, so the invalid URL is never reached).
	const id = "unpinned-fixture"
	ModelManifest[id] = ModelArtifact{ID: id, Role: RoleEmbed, File: "x.gguf", URL: "https://example.invalid/x.gguf", SHA256: ""}
	t.Cleanup(func() { delete(ModelManifest, id) })
	if _, err := EnsureModel(context.Background(), id); err == nil {
		t.Fatal("expected EnsureModel to fail closed on an unpinned (empty sha256) model")
	}
}

func TestProgressReader(t *testing.T) {
	var lastDone, lastTotal int64
	var calls int
	pr := &progressReader{
		r:          bytes.NewReader(make([]byte, 1000)),
		total:      1000,
		onProgress: func(done, total int64) { lastDone, lastTotal, calls = done, total, calls+1 },
	}
	if _, err := io.Copy(io.Discard, pr); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("onProgress was never called")
	}
	if lastDone != 1000 || lastTotal != 1000 {
		t.Errorf("final progress = (%d/%d), want (1000/1000)", lastDone, lastTotal)
	}
}

func TestEnsureModelUnknownID(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := EnsureModel(context.Background(), "totally-unknown"); err == nil {
		t.Fatal("expected error for an unknown model id")
	}
}

func TestEnsureModelFastPathVerifiedCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const id = "test-fixture-model"
	body := []byte("pretend gguf bytes")
	sum := sha256.Sum256(body)
	hexSum := hex.EncodeToString(sum[:])

	// Inject a pinned fixture entry and pre-write the matching cached file so
	// EnsureModel's fast path returns it with no network call.
	ModelManifest[id] = ModelArtifact{ID: id, Role: RoleEmbed, File: "fixture.gguf", SHA256: hexSum, SizeBytes: int64(len(body))}
	t.Cleanup(func() { delete(ModelManifest, id) })

	dest, err := ModelPath(id, "fixture.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := EnsureModel(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureModel fast path errored: %v", err)
	}
	if got != dest {
		t.Errorf("EnsureModel = %q, want cached %q", got, dest)
	}

	// VerifyModel agrees; ModelStatus reports it present and size-matched.
	ok, err := VerifyModel(id)
	if err != nil || !ok {
		t.Errorf("VerifyModel = (%v, %v), want (true, nil)", ok, err)
	}
	st := ModelStatus(id)
	if !st.Present || !st.SizeMatch || !st.Pinned {
		t.Errorf("ModelStatus = %+v, want present+sizeMatch+pinned", st)
	}
}

func TestModelStatusAbsent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st := ModelStatus("embeddinggemma-300m")
	if !st.Known {
		t.Error("expected a manifest model to be Known")
	}
	if st.Present {
		t.Error("expected the model to be absent in a fresh cache")
	}
	unknown := ModelStatus("nope")
	if unknown.Known {
		t.Error("unknown id should not be Known")
	}
}

func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	body := []byte("hello sha")
	if err := os.WriteFile(f, body, 0o644); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(body)
	got, err := fileSHA256(f)
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("fileSHA256 = %q, want %q", got, hex.EncodeToString(want[:]))
	}
}
