package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
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
	if err := ai.InitBedrock(ctx, ai.DefaultRegistry, v.Config.AI.Bedrock, v.Config.AI); err != nil {
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
