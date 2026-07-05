package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPullEventJSON locks the JSONL contract the macOS app's download button
// decodes: stable keys, and omitempty so a "done"/"error" event carries no
// zero-valued done/total.
func TestPullEventJSON(t *testing.T) {
	prog, _ := json.Marshal(pullEvent{Model: "gemma4-e2b", Status: "progress", Done: 5, Total: 10})
	for _, want := range []string{`"model":"gemma4-e2b"`, `"status":"progress"`, `"done":5`, `"total":10`} {
		if !strings.Contains(string(prog), want) {
			t.Errorf("progress event %s missing %s", prog, want)
		}
	}

	done, _ := json.Marshal(pullEvent{Model: "m", Status: "done", Path: "/p"})
	// Check for the KEYs (with colon) — the status VALUE is "done", so a bare
	// `"done"` substring would false-match.
	if strings.Contains(string(done), `"done":`) || strings.Contains(string(done), `"total":`) {
		t.Errorf("done event should omit zero done/total via omitempty: %s", done)
	}
	if !strings.Contains(string(done), `"path":"/p"`) {
		t.Errorf("done event should carry the path: %s", done)
	}

	errEv, _ := json.Marshal(pullEvent{Model: "m", Status: "error", Error: "sha256 mismatch"})
	if !strings.Contains(string(errEv), `"error":"sha256 mismatch"`) {
		t.Errorf("error event should carry the error: %s", errEv)
	}
}
