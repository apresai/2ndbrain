package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/metrics"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestMCPMetricOp(t *testing.T) {
	cases := map[string]string{
		"kb_search":          metrics.OpSearch,
		"kb_ask":             metrics.OpAsk,
		"kb_index":           metrics.OpIndex,
		"kb_append":          metrics.OpIndexDoc,
		"kb_create":          metrics.OpIndexDoc,
		"kb_update_meta":     metrics.OpIndexDoc,
		"kb_replace_section": metrics.OpIndexDoc,
		// read-only / metadata / git tools are not performance-recorded
		"kb_info":       "",
		"kb_list":       "",
		"kb_read":       "",
		"kb_git_status": "",
		"kb_delete":     "",
	}
	for tool, want := range cases {
		if got := mcpMetricOp(tool); got != want {
			t.Errorf("mcpMetricOp(%q) = %q, want %q", tool, got, want)
		}
	}
}

// TestWrapMCPMetric_RecordsWithMCPSource locks the Phase-2 wiring: the wrap
// records each call as source=mcp with the right op/version, captures the inner
// error, and still propagates the handler's result+error unchanged.
func TestWrapMCPMetric_RecordsWithMCPSource(t *testing.T) {
	mdb, err := metrics.Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()

	called := false
	okHandler := wrapMCPMetric(mdb, metrics.OpSearch, "v9", func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		called = true
		return nil, nil
	})
	if _, err := okHandler(context.Background(), mcplib.CallToolRequest{}); err != nil {
		t.Fatalf("wrapped ok handler returned err: %v", err)
	}
	if !called {
		t.Error("inner handler was not called")
	}

	failHandler := wrapMCPMetric(mdb, metrics.OpAsk, "v9", func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return nil, errors.New("boom")
	})
	if _, err := failHandler(context.Background(), mcplib.CallToolRequest{}); err == nil {
		t.Error("wrapped fail handler should propagate the inner error")
	}

	ops, err := mdb.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("recorded %d ops, want 2", len(ops))
	}
	var search, ask *metrics.Operation
	for i := range ops {
		switch ops[i].Operation {
		case metrics.OpSearch:
			search = &ops[i]
		case metrics.OpAsk:
			ask = &ops[i]
		}
	}
	if search == nil || search.Source != "mcp" || !search.OK || search.CLIVersion != "v9" {
		t.Errorf("search row wrong: %+v", search)
	}
	if ask == nil || ask.OK || ask.Error != "boom" {
		t.Errorf("ask failure row not captured: %+v", ask)
	}
}

// TestWrapMCPMetric_CapturesHandlerDetail locks the token/count capture: a
// handler attaches usage + result count via recordMCPDetail, and the wrapper
// folds it into the recorded row (so agent-driven ask/search/index rows are no
// longer all-zero on tokens and counts).
func TestWrapMCPMetric_CapturesHandlerDetail(t *testing.T) {
	mdb, err := metrics.Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()

	h := wrapMCPMetric(mdb, metrics.OpAsk, "v1", func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		recordMCPDetail(ctx, func(d *mcpMetricDetail) {
			d.InputTokens = 120
			d.OutputTokens = 45
			d.ResultCount = 3
			d.Mode = "hybrid"
			d.TotalChars = 800
		})
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
	if o := ops[0]; o.InputTokens != 120 || o.OutputTokens != 45 || o.ResultCount != 3 ||
		o.Mode != "hybrid" || o.TotalChars != 800 || o.Source != "mcp" {
		t.Errorf("handler detail not captured: in=%d out=%d rc=%d mode=%q chars=%d src=%q (want 120/45/3/hybrid/800/mcp)",
			o.InputTokens, o.OutputTokens, o.ResultCount, o.Mode, o.TotalChars, o.Source)
	}
}

// TestRecordMCPDetail_NoSinkIsNoOp guards that a handler calling recordMCPDetail
// outside the metric wrapper (no sink in context) is a safe no-op, not a panic —
// so handlers can call it unconditionally.
func TestRecordMCPDetail_NoSinkIsNoOp(t *testing.T) {
	called := false
	recordMCPDetail(context.Background(), func(d *mcpMetricDetail) { called = true; d.InputTokens = 1 })
	if called {
		t.Error("recordMCPDetail ran the callback with no sink in context; want no-op")
	}
}
