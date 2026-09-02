package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// End-to-end proof, through the real binary, that a document repeating a
// heading keeps BOTH sections searchable.
//
// The chunk id was sha256(doc id + heading path) and chunks.id is a PRIMARY
// KEY, so a second "## Standup" under "# Log" (what every daily-note and
// meeting-note template emits) overwrote the first and that text vanished from
// the index. It was silent: exit 0, no warning, and the only symptom was a
// search that found nothing.
//
// Credential-free: BM25 needs no provider, and BM25 is where the loss showed.
// This is the "index-consistency regression" shape the usage battery exists to
// catch, so it belongs at the real-binary tier rather than only in the chunker
// unit tests.
func TestBattery_RepeatedHeadingKeepsBothSectionsSearchable(t *testing.T) {
	home := isolatedHome(t)
	vault := filepath.Join(home, "vault")
	if out, code := runWithHome(t, home, "vault", "create", vault); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}
	setObsidianOpenVault(t, home, vault)

	body := "---\ntitle: Standup Log\ntype: note\nstatus: draft\n---\n" +
		"# Log\n\n## Standup\n\nZEBRAWORD appears only in the first standup.\n\n" +
		"## Standup\n\nQUOKKAWORD appears only in the second standup.\n"
	if err := os.WriteFile(filepath.Join(vault, "standup-log.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if out, code := runWithHome(t, home, "index", "--vault", vault); code != 0 {
		t.Fatalf("index: exit %d: %s", code, out)
	}

	// Both unique words must be reachable. Before the fix ZEBRAWORD, in the
	// section that lost the id race, returned zero hits while QUOKKAWORD
	// returned one, which is what made the loss so easy to miss.
	for _, word := range []string{"ZEBRAWORD", "QUOKKAWORD"} {
		out, code := runWithHome(t, home, "search", word, "--vault", vault, "--bm25-only", "--json")
		if code != 0 {
			t.Fatalf("search %s: exit %d: %s", word, code, out)
		}
		var env struct {
			Results []struct {
				Path string `json:"path"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("parse search envelope for %s: %v\n%s", word, err, out)
		}
		if len(env.Results) == 0 {
			t.Errorf("%s is in the file but unreachable by search: its section was dropped from the index", word)
		}
	}
}
