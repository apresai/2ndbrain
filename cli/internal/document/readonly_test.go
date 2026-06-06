package document

import "testing"

func TestIsReadOnlyType(t *testing.T) {
	if !IsReadOnlyType("canvas") || !IsReadOnlyType("base") {
		t.Error("canvas/base should be read-only types")
	}
	if IsReadOnlyType("note") || IsReadOnlyType("") {
		t.Error("note/empty should not be read-only types")
	}
}

// TestSerialize_RefusesReadOnlyType covers the defense-in-depth backstop: even a
// direct Serialize call (bypassing the command-level guards) must refuse to
// write a synthetic .canvas/.base document.
func TestSerialize_RefusesReadOnlyType(t *testing.T) {
	for _, typ := range []string{"canvas", "base"} {
		d := &Document{Type: typ, Body: "# synthesized\n"}
		if _, err := d.Serialize(); err == nil {
			t.Errorf("Serialize on a %q document should error", typ)
		}
	}
}
