package document

import "testing"

func TestRewriteWikiLinks_Matrix(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		oldTarget string
		newTarget string
		want      string
		wantCount int
	}{
		{
			name:      "bare basename",
			body:      "See [[old]] for context.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "See [[new]] for context.",
			wantCount: 1,
		},
		{
			name:      "with heading suffix preserved",
			body:      "See [[old#Decision]] here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "See [[new#Decision]] here.",
			wantCount: 1,
		},
		{
			name:      "with alias preserved",
			body:      "See [[old|the old note]] here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "See [[new|the old note]] here.",
			wantCount: 1,
		},
		{
			name:      "with block ref preserved",
			body:      "See [[old#^abc123]] here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "See [[new#^abc123]] here.",
			wantCount: 1,
		},
		{
			name:      "with heading and alias preserved",
			body:      "See [[old#Decision|jump]] here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "See [[new#Decision|jump]] here.",
			wantCount: 1,
		},
		{
			name:      "embed form preserved",
			body:      "Inline: ![[old]] embedded.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Inline: ![[new]] embedded.",
			wantCount: 1,
		},
		{
			name:      "path form rewritten to new path",
			body:      "See [[dir/old]] in folder.",
			oldTarget: "dir/old.md",
			newTarget: "newdir/new.md",
			want:      "See [[newdir/new]] in folder.",
			wantCount: 1,
		},
		{
			name:      "basename link to a path-located doc keeps basename form",
			body:      "Short [[old]] ref.",
			oldTarget: "dir/old.md",
			newTarget: "newdir/new.md",
			want:      "Short [[new]] ref.",
			wantCount: 1,
		},
		{
			name:      "multiple occurrences all rewritten",
			body:      "[[old]] then [[old#h]] then ![[old|a]].",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "[[new]] then [[new#h]] then ![[new|a]].",
			wantCount: 3,
		},
		{
			name:      "no false positive on name prefix",
			body:      "Keep [[oldish]] and [[older]] but move [[old]].",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Keep [[oldish]] and [[older]] but move [[new]].",
			wantCount: 1,
		},
		{
			name:      "inline code span not rewritten",
			body:      "Write `[[old]]` literally, but [[old]] is a real link.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Write `[[old]]` literally, but [[new]] is a real link.",
			wantCount: 1,
		},
		{
			name:      "double-backtick code span not rewritten",
			body:      "Write ``[[old]]`` literally, but [[old]] is a real link.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Write ``[[old]]`` literally, but [[new]] is a real link.",
			wantCount: 1,
		},
		{
			name:      "fenced code block not rewritten",
			body:      "```\n[[old]]\n```\nReal: [[old]].",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "```\n[[old]]\n```\nReal: [[new]].",
			wantCount: 1,
		},
		{
			name:      "no match leaves body unchanged",
			body:      "Nothing about [[other]] here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Nothing about [[other]] here.",
			wantCount: 0,
		},
		{
			name:      "target with explicit .md in link",
			body:      "Ref [[old.md]] explicit.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Ref [[new]] explicit.",
			wantCount: 1,
		},
		{
			name:      "rename in same dir keeps no folder prefix when authored bare",
			body:      "A [[old]] note.",
			oldTarget: "notes/old.md",
			newTarget: "notes/renamed.md",
			want:      "A [[renamed]] note.",
			wantCount: 1,
		},
		{
			name:      "rename in same dir rewrites path form",
			body:      "A [[notes/old]] note.",
			oldTarget: "notes/old.md",
			newTarget: "notes/renamed.md",
			want:      "A [[notes/renamed]] note.",
			wantCount: 1,
		},
		{
			// Folder-only move with an unchanged basename: a bare [[old]] still
			// resolves by basename afterward, so it is left untouched (no-op).
			name:      "folder-only move keeps bare basename link unchanged",
			body:      "A [[old]] note.",
			oldTarget: "old.md",
			newTarget: "archive/old.md",
			want:      "A [[old]] note.",
			wantCount: 0,
		},
		{
			// Moving src/old.md -> archive/old.md (basename unchanged): a
			// path-qualified [[src/old]] would break, so it is rewritten to the
			// new path, while a bare [[old]] still resolves and is left alone.
			name:      "folder change rewrites path link but leaves bare name",
			body:      "Path [[src/old]] and bare [[old]].",
			oldTarget: "src/old.md",
			newTarget: "archive/old.md",
			want:      "Path [[archive/old]] and bare [[old]].",
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := RewriteWikiLinks(tt.body, tt.oldTarget, tt.newTarget)
			if got != tt.want {
				t.Errorf("body mismatch:\n got: %q\nwant: %q", got, tt.want)
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
		})
	}
}

// TestRewriteWikiLinks_MarkdownLinks covers the markdown-style [label](target)
// link form, which move/rename now rewrites alongside [[wikilinks]] so a rename
// no longer silently breaks them.
func TestRewriteWikiLinks_MarkdownLinks(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		oldTarget string
		newTarget string
		want      string
		wantCount int
	}{
		{
			name:      "bare md link rewritten with .md preserved",
			body:      "See [see](old.md) here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "See [see](new.md) here.",
			wantCount: 1,
		},
		{
			name:      "md link with heading anchor preserved",
			body:      "See [see](old.md#heading) here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "See [see](new.md#heading) here.",
			wantCount: 1,
		},
		{
			name:      "label text preserved exactly",
			body:      "Read [the **important** doc](old.md) now.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Read [the **important** doc](new.md) now.",
			wantCount: 1,
		},
		{
			name:      "external https link untouched",
			body:      "Visit [x](https://example.com) please.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Visit [x](https://example.com) please.",
			wantCount: 0,
		},
		{
			name:      "mailto link untouched",
			body:      "Mail [me](mailto:a@b.com) now.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Mail [me](mailto:a@b.com) now.",
			wantCount: 0,
		},
		{
			name:      "anchor-only md link untouched",
			body:      "Jump [up](#top) here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Jump [up](#top) here.",
			wantCount: 0,
		},
		{
			name:      "md link inside inline code span not rewritten",
			body:      "Write `[see](old.md)` literally, but [see](old.md) is real.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Write `[see](old.md)` literally, but [see](new.md) is real.",
			wantCount: 1,
		},
		{
			name:      "md link inside fenced code block not rewritten",
			body:      "```\n[see](old.md)\n```\nReal: [see](old.md).",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "```\n[see](old.md)\n```\nReal: [see](new.md).",
			wantCount: 1,
		},
		{
			name:      "image embed md link rewritten",
			body:      "Embed ![alt](old.md) here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Embed ![alt](new.md) here.",
			wantCount: 1,
		},
		{
			name:      "path-form md link rewritten to new path",
			body:      "See [doc](dir/old.md) here.",
			oldTarget: "dir/old.md",
			newTarget: "newdir/new.md",
			want:      "See [doc](newdir/new.md) here.",
			wantCount: 1,
		},
		{
			name:      "wikilink and md link both rewritten in one pass",
			body:      "Both [[old]] and [see](old.md) point at it.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "Both [[new]] and [see](new.md) point at it.",
			wantCount: 2,
		},
		{
			name:      "md link to other note untouched",
			body:      "See [other](other.md) here.",
			oldTarget: "old.md",
			newTarget: "new.md",
			want:      "See [other](other.md) here.",
			wantCount: 0,
		},
		{
			name:      "percent-encoded md link decode-matched, encoding preserved on emit",
			body:      "See [x](My%20Note.md) here.",
			oldTarget: "My Note.md",
			newTarget: "Renamed Note.md",
			want:      "See [x](Renamed%20Note.md) here.",
			wantCount: 1,
		},
		{
			name:      "percent-encoded md link to clean new path emits raw",
			body:      "See [x](My%20Note.md) here.",
			oldTarget: "My Note.md",
			newTarget: "new.md",
			want:      "See [x](new.md) here.",
			wantCount: 1,
		},
		{
			name:      "raw-space authored md link comes out encoded",
			body:      "See [x](My Note.md) here.",
			oldTarget: "My Note.md",
			newTarget: "Other Note.md",
			want:      "See [x](Other%20Note.md) here.",
			wantCount: 1,
		},
		{
			name:      "clean-authored md link to space-bearing destination gets encoded",
			body:      "See [x](old.md) here.",
			oldTarget: "old.md",
			newTarget: "New Name.md",
			want:      "See [x](New%20Name.md) here.",
			wantCount: 1,
		},
		{
			name:      "encoded md link with encoded anchor: suffix verbatim, decode after split",
			body:      "See [x](My%20Note.md#Sec%20One) here.",
			oldTarget: "My Note.md",
			newTarget: "Renamed Note.md",
			want:      "See [x](Renamed%20Note.md#Sec%20One) here.",
			wantCount: 1,
		},
		{
			name:      "encoded md link with query suffix preserved",
			body:      "See [x](My%20Note.md?q=1) here.",
			oldTarget: "My Note.md",
			newTarget: "Renamed Note.md",
			want:      "See [x](Renamed%20Note.md?q=1) here.",
			wantCount: 1,
		},
		{
			name:      "malformed percent escape falls back to raw, no match, no panic",
			body:      "See [x](My%ZGNote.md) here.",
			oldTarget: "My Note.md",
			newTarget: "Renamed Note.md",
			want:      "See [x](My%ZGNote.md) here.",
			wantCount: 0,
		},
		{
			name:      "literal-percent filename raw-matches and emit escapes the percent",
			body:      "See [x](100%.md) here.",
			oldTarget: "100%.md",
			newTarget: "200%.md",
			want:      "See [x](200%25.md) here.",
			wantCount: 1,
		},
		{
			name:      "parens in destination escaped on emit",
			body:      "See [x](doc.md) here.",
			oldTarget: "doc.md",
			newTarget: "Note (v2).md",
			want:      "See [x](Note%20%28v2%29.md) here.",
			wantCount: 1,
		},
		{
			name:      "folder-only move under encoded bare link is a no-op",
			body:      "See [x](My%20Note.md) here.",
			oldTarget: "a/My Note.md",
			newTarget: "b/My Note.md",
			want:      "See [x](My%20Note.md) here.",
			wantCount: 0,
		},
		{
			name:      "folder-only move under raw-space bare link is a no-op, never a respell",
			body:      "See [x](My Note.md) here.",
			oldTarget: "a/My Note.md",
			newTarget: "b/My Note.md",
			want:      "See [x](My Note.md) here.",
			wantCount: 0,
		},
		{
			name:      "wikilink pass never decodes percent-encoding",
			body:      "See [[My%20Note]] here.",
			oldTarget: "My Note.md",
			newTarget: "Renamed Note.md",
			want:      "See [[My%20Note]] here.",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := RewriteWikiLinks(tt.body, tt.oldTarget, tt.newTarget)
			if got != tt.want {
				t.Errorf("body mismatch:\n got: %q\nwant: %q", got, tt.want)
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
		})
	}
}

// TestRewriteWikiLinks_PathSuffixMatch verifies a multi-segment path suffix
// (not just the basename) resolves and rewrites to the new path, mirroring the
// shortest-unique-path tier of store.ResolveLinks.
func TestRewriteWikiLinks_PathSuffixMatch(t *testing.T) {
	body := "Link via suffix [[b/c]] here."
	got, count := RewriteWikiLinks(body, "a/b/c.md", "x/y/z.md")
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	want := "Link via suffix [[x/y/z]] here."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRewriteWikiLinks_PreservesSurroundingText is a guard that splicing leaves
// non-link bytes (including multi-byte UTF-8) byte-identical.
func TestRewriteWikiLinks_PreservesSurroundingText(t *testing.T) {
	body := "café — résumé [[old]] naïve\n\nsecond [[old]] paragraph"
	got, count := RewriteWikiLinks(body, "old.md", "new.md")
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	want := "café — résumé [[new]] naïve\n\nsecond [[new]] paragraph"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRewriteLinksSyntaxAware pins the per-syntax destination contract used by
// repair and relink: wikilink occurrences get wikiTarget (the pretty form),
// while every matched markdown occurrence gets mdDest substituted verbatim
// (encoded as needed), ignoring the authored bare-vs-path shape. A title-based
// markdown destination resolves through no resolver tier, which is why the
// markdown side takes a caller-verified path form.
func TestRewriteLinksSyntaxAware(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		oldTarget  string
		wikiTarget string
		mdDest     string
		want       string
		wantCount  int
	}{
		{
			name:       "md occurrence gets mdDest, wiki occurrence gets wikiTarget, one pass",
			body:       "See [[guide to models]] and [x](guide to models.md).",
			oldTarget:  "guide to models",
			wikiTarget: "Guide To Models",
			mdDest:     "guide-to-models.md",
			want:       "See [[Guide To Models]] and [x](guide-to-models.md).",
			wantCount:  2,
		},
		{
			name:       "anchor suffix and label preserved under verbatim mdDest",
			body:       "See [setup](auth%20flow.md#Setup) here.",
			oldTarget:  "auth flow.md",
			wikiTarget: "Auth Flow",
			mdDest:     "auth-flow.md",
			want:       "See [setup](auth-flow.md#Setup) here.",
			wantCount:  1,
		},
		{
			name:       "mdDest containing spaces is percent-encoded",
			body:       "See [x](auth%20flow.md) here.",
			oldTarget:  "auth flow.md",
			wikiTarget: "Auth Flow",
			mdDest:     "guides/Auth Flow.md",
			want:       "See [x](guides/Auth%20Flow.md) here.",
			wantCount:  1,
		},
		{
			name:       "mdDest equal to the authored target is a no-op",
			body:       "See [x](auth-flow.md) here.",
			oldTarget:  "auth-flow.md",
			wikiTarget: "Auth Flow",
			mdDest:     "auth-flow.md",
			want:       "See [x](auth-flow.md) here.",
			wantCount:  0,
		},
		{
			name:       "path-authored md occurrence also gets mdDest verbatim",
			body:       "See [x](notes/auth%20flow.md) here.",
			oldTarget:  "notes/auth flow.md",
			wikiTarget: "Auth Flow",
			mdDest:     "auth-flow.md",
			want:       "See [x](auth-flow.md) here.",
			wantCount:  1,
		},
		{
			name:       "wikilink-only body: mdDest unused, pretty form emitted",
			body:       "See [[auth flow]] here.",
			oldTarget:  "auth flow",
			wikiTarget: "Auth Flow",
			mdDest:     "auth-flow.md",
			want:       "See [[Auth Flow]] here.",
			wantCount:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := RewriteLinksSyntaxAware(tt.body, tt.oldTarget, tt.wikiTarget, tt.mdDest)
			if got != tt.want {
				t.Errorf("body mismatch:\n got: %q\nwant: %q", got, tt.want)
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
		})
	}
}

// TestUnlinkWikiLink covers the "remove the link, keep the words" resolution:
// brackets are stripped, the visible text (alias or target) is preserved, and
// code/embeds are never touched. Matching is exact (case/separator-sensitive).
func TestUnlinkWikiLink(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		target    string
		wantBody  string
		wantCount int
	}{
		{
			name:      "bare junk id -> plain text",
			body:      "See [[083477d]] for the run.",
			target:    "083477d",
			wantBody:  "See 083477d for the run.",
			wantCount: 1,
		},
		{
			name:      "alias kept",
			body:      "Read [[aagent|the GTM kit]] now.",
			target:    "aagent",
			wantBody:  "Read the GTM kit now.",
			wantCount: 1,
		},
		{
			name:      "heading suffix dropped (no alias)",
			body:      "Jump to [[ccbr#Setup]] here.",
			target:    "ccbr",
			wantBody:  "Jump to ccbr here.",
			wantCount: 1,
		},
		{
			name:      "block ref dropped",
			body:      "Quote [[warp#^abc123]].",
			target:    "warp",
			wantBody:  "Quote warp.",
			wantCount: 1,
		},
		{
			name:      "multiple occurrences both unlinked",
			body:      "[[warp]] and again [[warp]].",
			target:    "warp",
			wantBody:  "warp and again warp.",
			wantCount: 2,
		},
		{
			name:      "non-matching target untouched",
			body:      "Keep [[other-note]] linked.",
			target:    "warp",
			wantBody:  "Keep [[other-note]] linked.",
			wantCount: 0,
		},
		{
			name:      "embed/transclusion never unlinked",
			body:      "Image ![[warp]] stays.",
			target:    "warp",
			wantBody:  "Image ![[warp]] stays.",
			wantCount: 0,
		},
		{
			name:      "link inside inline code untouched",
			body:      "Use `[[warp]]` syntax but [[warp]] here.",
			target:    "warp",
			wantBody:  "Use `[[warp]]` syntax but warp here.",
			wantCount: 1,
		},
		{
			name:      "case/separator mismatch does NOT unlink (exact match only)",
			body:      "Keep [[Warp]] and [[war-p]] linked.",
			target:    "warp",
			wantBody:  "Keep [[Warp]] and [[war-p]] linked.",
			wantCount: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, n := UnlinkWikiLink(tc.body, tc.target)
			if n != tc.wantCount {
				t.Errorf("count = %d, want %d", n, tc.wantCount)
			}
			if got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
			}
		})
	}
}
