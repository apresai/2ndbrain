package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

func TestRunRAGFailsOnUnreadableContextSource(t *testing.T) {
	v := testutil.NewTestVault(t)
	doc := testutil.CreateAndIndex(t, v, "Knowledge Base Topics", "note", "The main topics covered in this knowledge base are architecture decisions, runbooks, and operational notes.")

	if err := os.Remove(filepath.Join(v.Root, doc.Path)); err != nil {
		t.Fatalf("remove context source: %v", err)
	}

	result := RunRAG(ProbeOpts{
		Ctx:       context.Background(),
		Provider:  "unknown",
		ModelID:   "unknown",
		ModelType: "generation",
		SearchDB:  v.DB.Conn(),
		VaultRoot: v.Root,
	})
	if result.OK {
		t.Fatalf("RunRAG should fail when the indexed context file cannot be read: %+v", result)
	}
	if !strings.Contains(result.Detail, "read context") {
		t.Fatalf("RunRAG detail = %q, want read context error", result.Detail)
	}
}

func TestRunEmbedUnknownProvider(t *testing.T) {
	result := RunEmbed(ProbeOpts{Ctx: context.Background(), Provider: "unknown"})
	if result.Probe != "embed" || result.OK {
		t.Fatalf("RunEmbed unknown provider = %+v, want failed embed probe", result)
	}
	if !strings.Contains(result.Detail, "unknown provider") {
		t.Fatalf("detail = %q, want unknown provider", result.Detail)
	}
}

func TestRunGenerateUnknownProvider(t *testing.T) {
	result := RunGenerate(ProbeOpts{Ctx: context.Background(), Provider: "unknown"})
	if result.Probe != "generate" || result.OK {
		t.Fatalf("RunGenerate unknown provider = %+v, want failed generate probe", result)
	}
	if !strings.Contains(result.Detail, "unknown provider") {
		t.Fatalf("detail = %q, want unknown provider", result.Detail)
	}
}

func TestRunSearchWithoutDatabase(t *testing.T) {
	result := RunSearch(ProbeOpts{})
	if result.Probe != "search" || result.OK || result.Detail != "no search database" {
		t.Fatalf("RunSearch without DB = %+v", result)
	}
}

func TestRunRetrievalQualityWithoutDatabase(t *testing.T) {
	result := RunRetrievalQuality(ProbeOpts{})
	if result.Probe != "retrieval" || result.OK || result.Detail != "no search database" {
		t.Fatalf("RunRetrievalQuality without DB = %+v", result)
	}
}

func TestRunAllSelectsProbesByModelType(t *testing.T) {
	embedResults := RunAll(ProbeOpts{Ctx: context.Background(), Provider: "unknown", ModelType: "embedding"})
	if len(embedResults) != 2 || embedResults[0].Probe != "embed" || embedResults[1].Probe != "retrieval" {
		t.Fatalf("embedding RunAll probes = %+v", embedResults)
	}

	genResults := RunAll(ProbeOpts{Ctx: context.Background(), Provider: "unknown", ModelType: "generation"})
	if len(genResults) != 3 || genResults[0].Probe != "generate" || genResults[1].Probe != "search" || genResults[2].Probe != "rag" {
		t.Fatalf("generation RunAll probes = %+v", genResults)
	}
}
