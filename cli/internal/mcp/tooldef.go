package mcp

import (
	"encoding/json"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// allMCPToolNames is the catalog TestMCPToolRegistrationsIncludesAllTools
// asserts. The tools/list JSON shape test reuses this list so the two
// cannot drift.
var allMCPToolNames = []string{
	"kb_info", "kb_search", "kb_ask", "kb_read", "kb_list", "kb_create",
	"kb_update_meta", "kb_related", "kb_structure", "kb_delete", "kb_index",
	"kb_suggest_links", "kb_polish", "kb_git_activity", "kb_git_diff", "kb_git_status",
	"kb_backlinks", "kb_links", "kb_tags", "kb_tasks", "kb_append", "kb_replace_section",
}

// ToolInfo is one registered kb_* tool's name and routing description.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolCatalog is name plus description of every registered kb_* tool, in
// registration order. mcp-setup prints this so the human list cannot drift
// from tools/list.
func ToolCatalog() []ToolInfo {
	regs := mcpToolRegistrations(&handlers{})
	out := make([]ToolInfo, len(regs))
	for i, r := range regs {
		out[i] = ToolInfo{Name: r.tool.Name, Description: r.tool.Description}
	}
	return out
}

// kbTool builds a tool whose inputSchema JSON omits an empty required array.
// mcp-go's ToolInputSchema marshaler always emits "required": [] even when
// there are no required fields (and NewTool's comment claiming otherwise is
// stale). RawInputSchema is the constructor that lets us control the bytes.
// Empty annotations are still emitted by mcp-go's Tool.MarshalJSON; the
// tools/list sanitizer in wire.go strips those on the wire.
func kbTool(name, description string, properties map[string]any, required []string) mcplib.Tool {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		// Static maps of strings and nested maps; a failure is a programmer error.
		panic("kbTool schema: " + err.Error())
	}
	return mcplib.NewToolWithRawSchema(name, description, raw)
}
