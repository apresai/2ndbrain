package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestKBIndexNamesTheNotesItCouldNotRead: an agent indexing over MCP has to be
// able to see a note that failed to OPEN. Its row is deliberately kept, so it
// goes on answering searches from what was indexed before, which is exactly the
// state an agent must be told about. The result carried only `unparseable`,
// which is a different category with a different remedy.
func TestKBIndexNamesTheNotesItCouldNotRead(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	testutil.CreateAndIndex(t, v, "Readable", "note", "a note that opens fine")
	locked := testutil.CreateAndIndex(t, v, "Locked", "note", "a note that stops opening")

	lockedAbs := filepath.Join(v.Root, locked.Path)
	if err := os.Chmod(lockedAbs, 0o000); err != nil {
		t.Fatalf("lock note: %v", err)
	}
	// Restore immediately: t.TempDir() cannot remove a directory holding a
	// 0o000 file, and that failure surfaces as the whole package failing.
	t.Cleanup(func() { _ = os.Chmod(lockedAbs, 0o644) })

	res, err := h.handleKBIndex(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBIndex: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var result struct {
		Unparseable []struct {
			Path string `json:"path"`
			Err  string `json:"error"`
		} `json:"unparseable"`
		Unreadable []struct {
			Path string `json:"path"`
			Err  string `json:"error"`
		} `json:"unreadable"`
	}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}
	if len(result.Unreadable) != 1 || result.Unreadable[0].Path != locked.Path {
		t.Fatalf("unreadable = %+v, want exactly %s once (the walk and the embed pass both meet it)", result.Unreadable, locked.Path)
	}
	if result.Unreadable[0].Err == "" {
		t.Error("the unreadable entry carries no reason")
	}
	if len(result.Unparseable) != 0 {
		t.Errorf("unparseable = %+v, want empty: a note that would not OPEN is a different category", result.Unparseable)
	}
}

// TestKBIndexAlwaysCarriesBothLists: the keys are present and empty on a clean
// vault, matching the index --json contract, so an agent never has to tell
// "absent" apart from "none".
func TestKBIndexAlwaysCarriesBothLists(t *testing.T) {
	h, v := makeHandlers(t)
	testutil.CreateAndIndex(t, v, "Fine", "note", "nothing wrong here")

	res, err := h.handleKBIndex(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBIndex: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(t, res)), &raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	for _, key := range []string{"unparseable", "unreadable"} {
		val, present := raw[key]
		if !present {
			t.Errorf("%q is absent from the kb_index result", key)
			continue
		}
		if string(val) != "[]" {
			t.Errorf("%q = %s, want [] on a clean vault (never null, never absent)", key, val)
		}
	}
}
