package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsAssetOrAnchorTarget(t *testing.T) {
	cases := []struct {
		target string
		want   bool
	}{
		{"", true},              // anchor-only / same-doc link
		{"note", false},         // bare note reference
		{"note.md", false},      // markdown note
		{"board.canvas", false}, // indexed canvas
		{"cfg.base", false},     // indexed base
		{"img.png", true},       // image asset
		{"doc.pdf", true},       // pdf asset
		{"clip.mp4", true},      // video asset
		{"sub/dir/note.md", false},
		{"sub/dir/pic.jpg", true},
	}
	for _, c := range cases {
		if got := isAssetOrAnchorTarget(c.target); got != c.want {
			t.Errorf("isAssetOrAnchorTarget(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}

// TestLint_SkipsAssetsAndAnchors runs `2nb lint --json` over a vault whose only
// genuinely broken link is [[missing]] — images, anchor-only links, and links
// to indexed .canvas files must NOT be reported (the noise the fix removed).
func TestLint_SkipsAssetsAndAnchors(t *testing.T) {
	_, root := newContractVault(t)

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("overview.md", "---\nid: o1\ntitle: Overview\ntype: note\nstatus: draft\n---\nNo links here.\n")
	write("board.canvas", `{"nodes":[{"id":"n1","type":"text","text":"card"}],"edges":[]}`)
	write("note.md", "---\nid: n1\ntitle: Note\ntype: note\nstatus: draft\n---\n"+
		"See [[overview]] and [[missing]]. "+
		"Image ![[pic.png]] and ![alt](photo.jpg). "+
		"Anchor [top](#section). Canvas [[board.canvas]].\n")

	out, err := runCLIArgs(t, root, "lint", "--json")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	var report struct {
		Issues []struct {
			Path    string `json:"path"`
			Level   string `json:"level"`
			Message string `json:"message"`
		} `json:"issues"`
		Errors int `json:"errors"`
		Warns  int `json:"warnings"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode lint report: %v\n%s", err, out)
	}

	if report.Errors != 0 {
		t.Errorf("expected 0 errors, got %d: %+v", report.Errors, report.Issues)
	}
	if report.Warns != 1 {
		t.Fatalf("expected exactly 1 broken-link warning ([[missing]]), got %d: %+v", report.Warns, report.Issues)
	}
	if got := report.Issues[len(report.Issues)-1].Message; got == "" ||
		!containsSubstr(got, "missing") {
		t.Errorf("expected the warning to be about 'missing', got %q", got)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
