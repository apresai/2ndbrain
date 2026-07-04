package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/llama"
)

func TestEngineSpecsFor(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Inject a downloaded fixture model + an undownloaded one.
	const dl = "test-engine-downloaded"
	llama.ModelManifest[dl] = llama.ModelArtifact{ID: dl, Role: llama.RoleGen, File: "g.gguf", SHA256: "x"}
	t.Cleanup(func() { delete(llama.ModelManifest, dl) })
	path, err := llama.ModelPath(dl, "g.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}

	const notDL = "embeddinggemma-300m" // in the manifest, but not on disk
	specs, warnings := engineSpecsFor(dl, notDL, "not-a-real-model")

	if len(specs) != 1 || specs[0].Role != llama.RoleGen || specs[0].ModelPath != path {
		t.Fatalf("expected exactly the downloaded gen spec, got %+v", specs)
	}
	// The not-downloaded embed model and the unknown rerank model each warn.
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings (not-downloaded + unknown), got %d: %v", len(warnings), warnings)
	}
}

func TestEngineSpecsForEmpty(t *testing.T) {
	specs, warnings := engineSpecsFor("", "", "")
	if len(specs) != 0 || len(warnings) != 0 {
		t.Errorf("empty ids should yield no specs and no warnings, got specs=%v warns=%v", specs, warnings)
	}
}
