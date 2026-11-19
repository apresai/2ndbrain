package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestHandleKBPolishLinks_E2E_Bedrock exercises the MCP polish path with link
// grounding. CreateAndIndex records titles without embeddings, so the substring
// fallback supplies "Auth Flow" as the only candidate; every link the model adds
// must resolve to it (StripInventedLinks guarantees no invented targets).
func TestHandleKBPolishLinks_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	h, v := makeHandlers(t)
	if err := ai.InitBedrock(ctx, ai.DefaultRegistry, v.Config.AI.Bedrock, v.Config.AI, v.Root); err != nil {
		t.Skipf("init bedrock: %v", err)
	}

	testutil.CreateAndIndex(t, v, "Auth Flow", "note",
		"---\ntitle: Auth Flow\ntype: note\nstatus: draft\n---\n\n# Auth Flow\n\nHow auth works.\n")
	src := testutil.CreateAndIndex(t, v, "Source", "note",
		"---\ntitle: Source\ntype: note\nstatus: draft\n---\n\n# Source\n\nThis design builds on the Auth Flow for login.\n")

	res, err := h.handleKBPolish(ctx, makeRequest(map[string]any{"path": src.Path, "links": true}))
	if err != nil {
		t.Fatalf("handleKBPolish: %v", err)
	}
	if res.IsError {
		t.Fatalf("kb_polish returned error: %s", resultText(t, res))
	}

	var out struct {
		Polished   string   `json:"polished"`
		LinksAdded []string `json:"links_added"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("unmarshal kb_polish result: %v", err)
	}
	for _, target := range out.LinksAdded {
		if polish.NormalizeLinkKey(target) != "auth flow" {
			t.Errorf("ungrounded/invented link target %q in %v", target, out.LinksAdded)
		}
	}
}

func TestHandleKBPolish_EmptyBacklinksAndZeroEmbeddingMetering_LiveGemini(t *testing.T) {
	ctx := context.Background()
	h, v := makeHandlers(t)
	if err := ai.InitGemini(ctx, ai.DefaultRegistry, v.Config.AI.Gemini, v.Config.AI); err != nil {
		t.Skipf("init gemini: %v", err)
	}
	gen, err := ai.DefaultRegistry.Generator(v.Config.AI.EffectiveGenerationProvider())
	if err != nil {
		t.Skipf("gemini generator unavailable: %v", err)
	}
	if ready, code := ai.Availability(ctx, gen); !ready {
		t.Skipf("gemini generation provider unavailable: %s", code)
	}

	note := testutil.CreateAndIndex(t, v, "Solo Note", "note",
		"---\ntitle: Solo Note\ntype: note\nstatus: complete\n---\n\n# Solo Note\n\nThis note have minor grammer issue and no backlinks.\n")

	mdb, err := metrics.Open(filepath.Join(v.Root, ".2ndbrain", "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()

	wrapped := wrapMCPMetric(mdb, metrics.OpPolish, "test", h.handleKBPolish)
	res, err := wrapped(ctx, makeRequest(map[string]any{
		"path":      note.Path,
		"links":     true,
		"backlinks": true,
	}))
	if err != nil {
		t.Fatalf("handleKBPolish: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("kb_polish returned error: %s", text)
	}

	// Verify backlink_previews marshals as [] rather than null when 0 previews exist.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("unmarshal raw JSON: %v\n%s", err, text)
	}
	bpRaw, ok := raw["backlink_previews"]
	if !ok {
		t.Fatalf("missing backlink_previews key in JSON:\n%s", text)
	}
	if strings.TrimSpace(string(bpRaw)) != "[]" {
		t.Fatalf("expected backlink_previews to be [], got %s", string(bpRaw))
	}

	// Verify no spurious embedding leg was recorded on a zero-embedding vault.
	entries, err := mdb.UsageEntries("")
	if err != nil {
		t.Fatalf("UsageEntries: %v", err)
	}
	slots := make(map[string]int)
	for _, e := range entries {
		if e.Operation == metrics.OpPolish {
			slots[e.Slot] += e.Calls
		}
	}
	if slots["generation"] == 0 {
		t.Fatalf("expected generation leg in ai_usage_ledger, got %+v", slots)
	}
	if slots["embedding"] != 0 {
		t.Fatalf("expected 0 embedding legs on zero-embedding vault, got %+v", slots)
	}
}
