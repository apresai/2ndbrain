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
