package mcp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// TestWrapMCPMetricCountsProviderRetries is challenger M1. Every MCP tool call
// recorded embed_retries 0 while the CLI rows recorded the real number, so the
// observatory's retry history had a hole exactly the shape of agent-driven work.
// The handler drives the REAL retry recorder through the context, the way a
// provider call does; no HTTP is involved.
func TestWrapMCPMetricCountsProviderRetries(t *testing.T) {
	mdb, err := metrics.Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()

	h := wrapMCPMetric(mdb, metrics.OpIndex, "v1", func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if ai.RetryCounterFrom(ctx) == nil {
			t.Error("no retry counter on the tool call's context")
		}
		// Two retries, each reporting the classic loop's five-attempt budget.
		ai.RecordRetryForTest(ctx, 5)
		ai.RecordRetryForTest(ctx, 5)
		return nil, nil
	})
	if _, err := h(context.Background(), mcplib.CallToolRequest{}); err != nil {
		t.Fatal(err)
	}

	ops, err := mdb.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("recorded %d ops, want 1", len(ops))
	}
	if ops[0].EmbedRetries != 2 {
		t.Errorf("embed_retries = %d, want 2: an MCP row must report the retries its call rode out", ops[0].EmbedRetries)
	}
}

// TestWrapMCPMetricGivesEachCallItsOwnCounter: two concurrent tool calls must
// not report each other's retries, so the counter is per call, never shared.
func TestWrapMCPMetricGivesEachCallItsOwnCounter(t *testing.T) {
	mdb, err := metrics.Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()

	retryOnce := wrapMCPMetric(mdb, metrics.OpSearch, "v1", func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		ai.RecordRetryForTest(ctx, 5)
		return nil, nil
	})
	retryNone := wrapMCPMetric(mdb, metrics.OpAsk, "v1", func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return nil, nil
	})
	if _, err := retryOnce(context.Background(), mcplib.CallToolRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := retryNone(context.Background(), mcplib.CallToolRequest{}); err != nil {
		t.Fatal(err)
	}

	ops, err := mdb.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	byOp := map[string]int{}
	for _, o := range ops {
		byOp[o.Operation] = o.EmbedRetries
	}
	if byOp[metrics.OpSearch] != 1 {
		t.Errorf("search row embed_retries = %d, want 1", byOp[metrics.OpSearch])
	}
	if byOp[metrics.OpAsk] != 0 {
		t.Errorf("ask row embed_retries = %d, want 0: a call that never retried must not inherit another call's count", byOp[metrics.OpAsk])
	}
}
