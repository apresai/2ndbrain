package mcp

import (
	"context"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

func TestEngine_ToolCountMatchesServer(t *testing.T) {
	v := testutil.NewTestVault(t)
	eng := NewEngine(v)
	if got := eng.ToolCount(); got != 22 {
		t.Errorf("ToolCount() = %d, want 22 (drift from mcpToolRegistrations)", got)
	}
	names := eng.ToolNames()
	want := map[string]bool{"kb_info": false, "kb_search": false, "kb_list": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("ToolNames() missing %q", n)
		}
	}
}

func TestEngine_CallKBInfoRoundTrip(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Alpha", "note", "alpha body")
	text, isErr, err := NewEngine(v).Call(context.Background(), "kb_info", nil)
	if err != nil || isErr {
		t.Fatalf("kb_info should answer: err=%v isErr=%v", err, isErr)
	}
	if text == "" {
		t.Error("kb_info returned empty text")
	}
}

func TestEngine_CallKBSearchOfflineBM25(t *testing.T) {
	// The default test vault has no AI provider; kb_search must still answer via
	// the BM25 fallback (so mcp doctor passes offline).
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Flow", "note", "alpha content about tokens")
	_, isErr, err := NewEngine(v).Call(context.Background(), "kb_search", map[string]any{"query": "alpha", "limit": 1})
	if err != nil || isErr {
		t.Fatalf("kb_search should answer offline via BM25: err=%v isErr=%v", err, isErr)
	}
}

func TestEngine_CallUnknownTool(t *testing.T) {
	v := testutil.NewTestVault(t)
	if _, _, err := NewEngine(v).Call(context.Background(), "kb_nope", nil); err == nil {
		t.Error("Call on an unknown tool should return an error")
	}
}
