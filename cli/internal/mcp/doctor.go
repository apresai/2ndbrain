package mcp

import (
	"context"
	"fmt"

	"github.com/apresai/2ndbrain/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// Engine runs MCP tools in-process for the self-test (`2nb mcp doctor`), reusing
// the EXACT mcpToolRegistrations list the stdio server serves so the tool set
// and per-tool timeout wrappers can never drift from what a real client gets. It
// spins no stdio transport and writes no status sidecar.
type Engine struct {
	regs []toolRegistration
}

// NewEngine builds an in-process engine over the vault's handlers.
func NewEngine(v *vault.Vault) *Engine {
	h := &handlers{vault: v}
	return &Engine{regs: mcpToolRegistrations(h)}
}

// ToolCount is the number of registered tools (the same set Start serves).
func (e *Engine) ToolCount() int { return len(e.regs) }

// ToolNames lists the registered tool names in registration order.
func (e *Engine) ToolNames() []string {
	names := make([]string, len(e.regs))
	for i, r := range e.regs {
		names[i] = r.tool.Name
	}
	return names
}

// Call invokes a registered tool by name with the given arguments, through the
// same per-tool timeout wrapper the live server uses. It returns the result text
// and whether the tool reported an error (isErr); err is non-nil only for an
// unknown tool or a handler that returned a Go error.
func (e *Engine) Call(ctx context.Context, name string, args map[string]any) (text string, isErr bool, err error) {
	for _, r := range e.regs {
		if r.tool.Name != name {
			continue
		}
		handler := withTimeout(r.timeout, r.handler)
		res, herr := handler(ctx, mcplib.CallToolRequest{Params: mcplib.CallToolParams{Arguments: args}})
		if herr != nil {
			return "", false, herr
		}
		return resultToText(res), res.IsError, nil
	}
	return "", false, fmt.Errorf("unknown tool %q", name)
}

func resultToText(res *mcplib.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcplib.TextContent); ok {
		return tc.Text
	}
	return ""
}
