package document

import (
	"strings"
	"testing"
)

func TestStripComments_Inline(t *testing.T) {
	got := StripComments("before %%hidden%% after")
	if strings.Contains(got, "hidden") {
		t.Errorf("comment text leaked: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("non-comment text removed: %q", got)
	}
	// Length is preserved (comment blanked, not deleted).
	if len(got) != len("before %%hidden%% after") {
		t.Errorf("length changed: got %d want %d", len(got), len("before %%hidden%% after"))
	}
}

func TestStripComments_PreservesNewlineCount(t *testing.T) {
	in := "line1\n%% a multi\nline comment %%\nline4\n"
	got := StripComments(in)
	if strings.Count(got, "\n") != strings.Count(in, "\n") {
		t.Errorf("newline count changed: got %d want %d (%q)", strings.Count(got, "\n"), strings.Count(in, "\n"), got)
	}
	if strings.Contains(got, "comment") {
		t.Errorf("comment leaked across lines: %q", got)
	}
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line4") {
		t.Errorf("surrounding lines lost: %q", got)
	}
}

func TestStripComments_Unterminated(t *testing.T) {
	got := StripComments("keep %% dangling to end")
	if strings.Contains(got, "dangling") {
		t.Errorf("unterminated comment not blanked: %q", got)
	}
	if !strings.Contains(got, "keep") {
		t.Errorf("text before comment lost: %q", got)
	}
}

func TestStripComments_NoComment(t *testing.T) {
	in := "plain text with a lone % and no comment"
	if got := StripComments(in); got != in {
		t.Errorf("non-comment text altered: got %q want %q", got, in)
	}
}

func TestIndexableBody_StripsComments(t *testing.T) {
	d := &Document{Body: "real %%secret%% content"}
	if strings.Contains(d.IndexableBody(), "secret") {
		t.Errorf("IndexableBody leaked comment: %q", d.IndexableBody())
	}
}
