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

func TestRemoveModel(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir, err := ModelsCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	mdir := filepath.Join(dir, "fixture-rm")
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 4096)
	if err := os.WriteFile(filepath.Join(mdir, "w.gguf"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	freed, err := RemoveModel("fixture-rm")
	if err != nil {
		t.Fatalf("RemoveModel: %v", err)
	}
	if freed != int64(len(body)) {
		t.Errorf("freed = %d, want %d", freed, len(body))
	}
	if _, err := os.Stat(mdir); !os.IsNotExist(err) {
		t.Error("model dir should be gone after RemoveModel")
	}

	// Absent model is a no-op (freed 0, no error).
	if f, err := RemoveModel("does-not-exist"); err != nil || f != 0 {
		t.Errorf("absent RemoveModel = (%d, %v), want (0, nil)", f, err)
	}

	// Path-traversal / separator ids are refused (can't escape the models cache).
	for _, bad := range []string{"../etc", "a/b", `a\b`, "..", "."} {
		if _, err := RemoveModel(bad); err == nil {
			t.Errorf("RemoveModel(%q) should be refused", bad)
		}
	}
}

func TestEmbeddingGemma2Manifest(t *testing.T) {
	if DefaultEmbedModelID != "embeddinggemma-2" {
		t.Errorf("DefaultEmbedModelID = %q, want embeddinggemma-2", DefaultEmbedModelID)
	}
	art, ok := ModelManifest["embeddinggemma-2"]
	if !ok {
		t.Fatal("embeddinggemma-2 missing from ModelManifest")
	}
	if art.SizeBytes != 309855456 {
		t.Errorf("SizeBytes = %d, want 309855456", art.SizeBytes)
	}
	if art.SHA256 == "" || art.URL == "" {
		t.Errorf("embeddinggemma-2 must pin sha256 and url: %+v", art)
	}
	if art.License != "apache-2.0" {
		t.Errorf("License = %q, want apache-2.0", art.License)
	}
	if art.File != "embeddinggemma-2-Q8_0.gguf" || art.Role != RoleEmbed || art.Dims != 768 {
		t.Errorf("unexpected embeddinggemma-2 artifact: %+v", art)
	}
	// Legacy weights stay resolvable for vaults already indexed with them.
	legacy, ok := ModelManifest["embeddinggemma-300m"]
	if !ok || legacy.File != "embeddinggemma-300m-Q8_0.gguf" {
		t.Errorf("legacy embeddinggemma-300m must remain in the manifest: %+v", legacy)
	}
}

// A verified copy in the Hugging Face hub cache seeds the model cache without a
// download; a corrupt copy is ignored.
func TestTrySeedFromLocalVerifiedCache(t *testing.T) {
	hub := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HF_HUB_CACHE", hub)

	payload := []byte("tiny stand-in for gguf weights")
	sum := sha256.Sum256(payload)
	art := ModelArtifact{
		ID:        "embeddinggemma-2",
		File:      "embeddinggemma-2-Q8_0.gguf",
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(payload)),
	}
	snap := filepath.Join(hub, "models--ggml-org--embeddinggemma-2-GGUF", "snapshots",
		"bfcd298762cc34d0357ece5ebdd31791a3a374d8")
	if err := os.MkdirAll(snap, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(snap, art.File)
	dest := filepath.Join(t.TempDir(), art.File)

	// Corrupt candidate (right size, wrong bytes) must not be accepted.
	if err := os.WriteFile(src, []byte("XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"), 0o644); err != nil {
		t.Fatal(err)
	}
	if trySeedFromLocalVerifiedCache(art, dest, nil) {
		t.Fatal("seeded from a candidate whose sha256 does not match")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("dest must not exist after a rejected candidate")
	}

	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	var gotDone, gotTotal int64
	if !trySeedFromLocalVerifiedCache(art, dest, func(d, tot int64) { gotDone, gotTotal = d, tot }) {
		t.Fatal("expected a verified candidate to seed the cache")
	}
	if gotDone != art.SizeBytes || gotTotal != art.SizeBytes {
		t.Errorf("progress = (%d,%d), want (%d,%d)", gotDone, gotTotal, art.SizeBytes, art.SizeBytes)
	}
	if got, err := fileSHA256(dest); err != nil || got != art.SHA256 {
		t.Errorf("dest sha = %q, %v; want %s", got, err, art.SHA256)
	}
}
