package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// TestServerInstructions_Constant guards the instructions copy: it must be
// non-empty (an empty string means the server announces nothing) and accurate —
// it names tools that actually exist. A renamed/removed tool that leaves a
// dangling mention here should fail this test.
func TestServerInstructions_Constant(t *testing.T) {
	if strings.TrimSpace(ServerInstructions) == "" {
		t.Fatal("ServerInstructions must be non-empty")
	}

	registered := map[string]bool{}
	for _, reg := range mcpToolRegistrations(&handlers{}) {
		registered[reg.tool.Name] = true
	}
	// Every "kb_xxx" token mentioned in the instructions must be a real tool
	// (kb_git_* is a glob shorthand, not a literal tool name — allow it).
	for _, field := range strings.FieldsFunc(ServerInstructions, func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z')
	}) {
		if !strings.HasPrefix(field, "kb_") {
			continue
		}
		if field == "kb_git_" { // from "kb_git_*"
			continue
		}
		if !registered[field] {
			t.Errorf("instructions mention %q which is not a registered tool", field)
		}
	}
}

// TestServerInstructions_ReachesWire proves the constant is actually wired into
// the initialize response the client receives, by driving an initialize
// handshake through the real server built by newMCPServer.
func TestServerInstructions_ReachesWire(t *testing.T) {
	h, v := makeHandlers(t)
	_ = h
	s, sw := newMCPServer(v, "test")
	if sw != nil {
		defer sw.Remove()
	}

	req := mcplib.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      mcplib.NewRequestId(int64(1)),
		Request: mcplib.Request{Method: "initialize"},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal initialize: %v", err)
	}

	resp, ok := s.HandleMessage(context.Background(), raw).(mcplib.JSONRPCResponse)
	if !ok {
		t.Fatalf("initialize did not return a JSONRPCResponse")
	}
	result, ok := resp.Result.(mcplib.InitializeResult)
	if !ok {
		t.Fatalf("initialize result is not an InitializeResult, got %T", resp.Result)
	}
	if result.Instructions != ServerInstructions {
		t.Errorf("initialize Instructions = %q, want the ServerInstructions constant", result.Instructions)
	}
}
