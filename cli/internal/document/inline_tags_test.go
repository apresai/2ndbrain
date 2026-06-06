package document

import (
	"reflect"
	"testing"
)

func TestExtractInlineTags_Basic(t *testing.T) {
	got := ExtractInlineTags("Working on #projects and #area-51 today.")
	want := []string{"projects", "area-51"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractInlineTags_SkipsHeadingsAndCode(t *testing.T) {
	body := "# Heading is not a tag\n" +
		"body with #realtag here\n" +
		"```\n#codetag should be ignored\n```\n" +
		"## Another heading\n"
	got := ExtractInlineTags(body)
	want := []string{"realtag"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractInlineTags_DedupesPreservingOrder(t *testing.T) {
	got := ExtractInlineTags("#b then #a then #b again")
	want := []string{"b", "a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractInlineTags_NestedCapturesFirstSegment(t *testing.T) {
	// Matches the legacy importer: #area/work captures "area".
	got := ExtractInlineTags("see #area/work")
	want := []string{"area"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractInlineTags_None(t *testing.T) {
	if got := ExtractInlineTags("no tags here, just a # symbol and text"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
