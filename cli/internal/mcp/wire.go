package mcp

import (
	"bytes"
	"encoding/json"
	"io"
)

// sanitizeToolsListJSON rewrites a JSON-RPC tools/list response so empty
// annotations objects and empty required arrays are omitted. mcp-go v0.58.0
// always emits both (Tool.MarshalJSON assigns annotations unconditionally;
// ToolInputSchema marshal writes required: [] when the slice is empty).
//
// Other JSON-RPC messages pass through unchanged. IDs are preserved as raw
// JSON so an integer id does not become 2.0.
func sanitizeToolsListJSON(raw []byte) []byte {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return raw
	}
	resultRaw, ok := top["result"]
	if !ok {
		return raw
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return raw
	}
	toolsRaw, ok := result["tools"]
	if !ok {
		return raw
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(toolsRaw, &tools); err != nil || len(tools) == 0 {
		return raw
	}
	cleaned := make([]json.RawMessage, len(tools))
	for i, t := range tools {
		cleaned[i] = sanitizeOneToolJSON(t)
	}
	newTools, err := json.Marshal(cleaned)
	if err != nil {
		return raw
	}
	result["tools"] = newTools
	newResult, err := json.Marshal(result)
	if err != nil {
		return raw
	}
	top["result"] = newResult
	out, err := json.Marshal(top)
	if err != nil {
		return raw
	}
	return out
}

func sanitizeOneToolJSON(raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	if ann, ok := m["annotations"]; ok {
		var obj map[string]any
		if json.Unmarshal(ann, &obj) == nil && len(obj) == 0 {
			delete(m, "annotations")
		}
	}
	if schemaRaw, ok := m["inputSchema"]; ok {
		var schema map[string]json.RawMessage
		if json.Unmarshal(schemaRaw, &schema) == nil {
			if req, ok := schema["required"]; ok {
				var arr []json.RawMessage
				if json.Unmarshal(req, &arr) == nil && len(arr) == 0 {
					delete(schema, "required")
					if b, err := json.Marshal(schema); err == nil {
						m["inputSchema"] = b
					}
				}
			}
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// toolsListSanitizer is a line-buffered stdout wrapper. Each complete
// JSON-RPC line is passed through sanitizeToolsListJSON so a live stdio
// client sees the compact tools/list shape.
type toolsListSanitizer struct {
	w   io.Writer
	buf []byte
}

func newToolsListSanitizer(w io.Writer) *toolsListSanitizer {
	return &toolsListSanitizer{w: w}
}

func (s *toolsListSanitizer) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := append([]byte(nil), s.buf[:i]...)
		s.buf = append([]byte(nil), s.buf[i+1:]...)
		out := sanitizeToolsListJSON(line)
		if _, err := s.w.Write(out); err != nil {
			return len(p), err
		}
		if _, err := s.w.Write([]byte{'\n'}); err != nil {
			return len(p), err
		}
	}
}
