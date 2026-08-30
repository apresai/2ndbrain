package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

var toolNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`)

const maxToolDescriptionBytes = 400
const maxToolsListPayloadBytes = 16000

// TestToolsListJSONShape pins the on-wire tools/list bytes Grok session
// cataloging reads. Assertions are on the sanitized JSON (the same rewrite
// stdout applies), not on the mcp-go Tool struct, because mcp-go always
// emits empty annotations and empty required arrays.
func TestToolsListJSONShape(t *testing.T) {
	raw := toolsListWireJSON(t)
	if len(raw) >= maxToolsListPayloadBytes {
		t.Fatalf("tools/list payload is %d bytes, want < %d", len(raw), maxToolsListPayloadBytes)
	}

	var envelope struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("tools/list is not JSON: %v\n%s", err, raw)
	}
	tools := envelope.Result.Tools
	if len(tools) != len(allMCPToolNames) {
		t.Fatalf("tools/list count = %d, want %d", len(tools), len(allMCPToolNames))
	}

	got := make(map[string]map[string]any, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatal("tools/list entry missing name")
		}
		got[name] = tool
	}
	for _, name := range allMCPToolNames {
		tool, ok := got[name]
		if !ok {
			t.Fatalf("tools/list missing %q", name)
		}
		assertWireTool(t, name, tool)
	}
	if len(got) != len(allMCPToolNames) {
		t.Fatalf("tools/list has extra names: got %d want %d", len(got), len(allMCPToolNames))
	}
}

func TestGrokPrefixedServerNameMustNotStartWithDigit(t *testing.T) {
	// Grok session cataloging prefixes tools as <server>__<name> then applies
	// toolNameRe. The Claude-imported key "2ndbrain" makes every prefixed
	// name illegal; "twonb" (mcp-setup snippet) does not.
	if toolNameRe.MatchString("2ndbrain__kb_info") {
		t.Fatal("Grok would accept 2ndbrain__kb_info; the twonb snippet would be stale")
	}
	if !toolNameRe.MatchString("twonb__kb_info") {
		t.Fatal("twonb__kb_info should pass Grok's tool-name pattern")
	}
}

func TestMCPGoEmitsEmptyAnnotations(t *testing.T) {
	raw, err := json.Marshal(kbInfoTool())
	if err != nil {
		t.Fatalf("marshal kb_info: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"annotations"`)) {
		t.Fatal("mcp-go stopped emitting empty annotations; drop the tools/list sanitizer")
	}
	cleaned := sanitizeOneToolJSON(raw)
	if bytes.Contains(cleaned, []byte(`"annotations"`)) {
		t.Fatalf("sanitizer left annotations: %s", cleaned)
	}
	if bytes.Contains(cleaned, []byte(`"required"`)) {
		t.Fatalf("sanitizer left empty required: %s", cleaned)
	}
}

func TestToolsListSanitizerRewritesStdioLine(t *testing.T) {
	var buf bytes.Buffer
	w := newToolsListSanitizer(&buf)
	line := `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"kb_info","description":"overview","inputSchema":{"type":"object","properties":{},"required":[]},"annotations":{}}]}}` + "\n"
	if _, err := w.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	if bytes.Contains(out, []byte(`"annotations"`)) {
		t.Fatalf("stdout still has annotations: %s", out)
	}
	if bytes.Contains(out, []byte(`"required"`)) {
		t.Fatalf("stdout still has empty required: %s", out)
	}
	if !bytes.Contains(out, []byte(`"kb_info"`)) {
		t.Fatalf("stdout lost the tool: %s", out)
	}

	// Non-tools/list JSON-RPC is unchanged, including integer ids.
	buf.Reset()
	ping := `{"jsonrpc":"2.0","id":7,"result":{}}` + "\n"
	if _, err := w.Write([]byte(ping)); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != ping {
		t.Fatalf("passthrough: got %q want %q", got, ping)
	}
}

func toolsListWireJSON(t *testing.T) []byte {
	t.Helper()
	_, v := makeHandlers(t)
	s, sw, mdb := newMCPServer(v, "test")
	if sw != nil {
		defer sw.Remove()
	}
	if mdb != nil {
		defer mdb.Close()
	}
	ctx := context.Background()

	initReq, err := json.Marshal(mcplib.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      mcplib.NewRequestId(int64(1)),
		Request: mcplib.Request{Method: "initialize"},
	})
	if err != nil {
		t.Fatalf("marshal initialize: %v", err)
	}
	_ = s.HandleMessage(ctx, initReq)

	listReq, err := json.Marshal(mcplib.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      mcplib.NewRequestId(int64(2)),
		Request: mcplib.Request{Method: "tools/list"},
	})
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	resp := s.HandleMessage(ctx, listReq)
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal tools/list response: %v", err)
	}
	return sanitizeToolsListJSON(raw)
}

func assertWireTool(t *testing.T, name string, tool map[string]any) {
	t.Helper()
	desc, _ := tool["description"].(string)
	if desc == "" {
		t.Errorf("%s: empty description", name)
	}
	if n := len([]byte(desc)); n > maxToolDescriptionBytes {
		t.Errorf("%s: description is %d bytes, want <= %d", name, n, maxToolDescriptionBytes)
	}
	if _, ok := tool["annotations"]; ok {
		t.Errorf("%s: annotations key should be omitted when empty", name)
	}
	if strings.Contains(desc, "Example prompts") {
		t.Errorf("%s: description still has example-prompt essay", name)
	}
	if strings.ContainsAny(desc, "\u2013\u2014") {
		t.Errorf("%s: description contains em/en dash", name)
	}
	// Grok prefixes as <server>__<name> then checks this pattern. The kb_*
	// names themselves must match; a digit-leading *server* key (2ndbrain)
	// is a client-config problem, not a tool-name problem.
	if !toolNameRe.MatchString(name) {
		t.Errorf("%s: tool name fails Grok's ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$", name)
	}
	schema, _ := tool["inputSchema"].(map[string]any)
	if schema == nil {
		t.Errorf("%s: missing inputSchema", name)
		return
	}
	if req, ok := schema["required"]; ok {
		arr, ok := req.([]any)
		if !ok {
			t.Errorf("%s: required is %T, want array", name, req)
		} else if len(arr) == 0 {
			t.Errorf("%s: empty required array should be omitted", name)
		}
	}
}
