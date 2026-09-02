package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

// Every argument a tool declares REQUIRED in its inputSchema must actually be
// enforced by its handler.
//
// Declaring `required` in the schema does not enforce anything at runtime: the
// MCP transport passes whatever the client sent, so a handler that reads an
// absent argument just gets the zero value and carries on. Two tools were doing
// exactly that. `kb_search` fell through to the query-less listing path and
// returned EVERY document with score 0 dressed as search results, where the
// CLI refuses the same input. `kb_update_meta` rewrote the file with the
// frontmatter it already had and reported success, so an agent believed it had
// written metadata that never changed.
//
// This is table-free on purpose: it reads the required list off each registered
// tool's own schema, so a tool added later is covered without touching this
// test, and a NEW required argument cannot ship unenforced.
func TestMCPTools_DeclaredRequiredArgumentsAreEnforced(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Alpha", "note", "alpha body with words")
	eng := NewEngine(v)

	// A plausible value for every argument any tool declares required, so the
	// ONLY thing wrong with each call is the one argument being omitted.
	sample := map[string]any{
		"query":    "alpha",
		"question": "what is alpha?",
		"path":     "alpha.md",
		"title":    "Some Title",
		"type":     "note",
		"text":     "some text",
		"section":  "Alpha",
		"fields":   map[string]any{"status": "complete"},
		"target":   "alpha",
	}

	for _, reg := range eng.regs {
		name := reg.tool.Name
		raw, err := reg.tool.MarshalJSON()
		if err != nil {
			t.Fatalf("%s: marshal tool: %v", name, err)
		}
		var def struct {
			InputSchema struct {
				Required []string `json:"required"`
			} `json:"inputSchema"`
		}
		if err := json.Unmarshal(raw, &def); err != nil {
			t.Fatalf("%s: parse inputSchema: %v", name, err)
		}
		if len(def.InputSchema.Required) == 0 {
			continue
		}

		for _, omitted := range def.InputSchema.Required {
			t.Run(name+"/omits_"+omitted, func(t *testing.T) {
				args := map[string]any{}
				for _, req := range def.InputSchema.Required {
					if req == omitted {
						continue
					}
					val, ok := sample[req]
					if !ok {
						t.Fatalf("no sample value for required argument %q of %s; add one to the sample map", req, name)
					}
					args[req] = val
				}
				_, isErr, err := eng.Call(context.Background(), name, args)
				if err != nil {
					t.Fatalf("%s: Call returned a Go error: %v", name, err)
				}
				if !isErr {
					t.Errorf("%s reported SUCCESS with required argument %q omitted; the schema says it is required, so the handler must refuse rather than act on a zero value", name, omitted)
				}
			})
		}
	}
}
