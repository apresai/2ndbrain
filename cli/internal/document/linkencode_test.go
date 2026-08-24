package document

import "testing"

func TestDecodeLinkTarget(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"identity without percent", "notes/My Note.md", "notes/My Note.md"},
		{"space", "My%20Note.md", "My Note.md"},
		{"paren", "Note%28v2%29.md", "Note(v2).md"},
		{"hash decodes after split", "My%23Tag.md", "My#Tag.md"},
		{"malformed escape falls through", "My%ZGNote.md", "My%ZGNote.md"},
		{"trailing percent falls through", "100%.md", "100%.md"},
		{"lowercase hex", "My%2fNote.md", "My/Note.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DecodeLinkTarget(c.in); got != c.want {
				t.Fatalf("DecodeLinkTarget(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestEncodeLinkTarget(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"clean path untouched", "notes/new.md", "notes/new.md"},
		{"space", "My New Note.md", "My%20New%20Note.md"},
		{"slash never escaped", "a b/c d.md", "a%20b/c%20d.md"},
		{"percent escaped", "200%.md", "200%25.md"},
		{"parens escaped", "Note (v2).md", "Note%20%28v2%29.md"},
		{"hash and query escaped", "a#b?c.md", "a%23b%3Fc.md"},
		{"utf8 verbatim", "café notes.md", "café%20notes.md"},
		{"probe sentinel untouched", "\x00probe-dst.md", "\x00probe-dst.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EncodeLinkTarget(c.in); got != c.want {
				t.Fatalf("EncodeLinkTarget(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, s := range []string{
		"My Note.md",
		"200%.md",
		"Note (v2).md",
		"a#b?c.md",
		"nested/dir with space/50% off.md",
	} {
		if got := DecodeLinkTarget(EncodeLinkTarget(s)); got != s {
			t.Fatalf("round trip %q -> %q", s, got)
		}
	}
}

func TestMdLinkNeedsEncoding(t *testing.T) {
	if mdLinkNeedsEncoding("notes/clean.md") {
		t.Fatal("clean path should not need encoding")
	}
	for _, s := range []string{"a b.md", "50%.md", "a(b).md", "a#b.md", "a?b.md"} {
		if !mdLinkNeedsEncoding(s) {
			t.Fatalf("%q should need encoding", s)
		}
	}
	if mdLinkNeedsEncoding("\x00probe-dst.md") {
		t.Fatal("probe sentinel must not need encoding")
	}
}
