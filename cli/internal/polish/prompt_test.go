package polish

import (
	"strings"
	"testing"
)

func TestBuildPolishUserMessage_NoCandidates(t *testing.T) {
	body := "# Title\n\nSome body text.\n"
	got := BuildPolishUserMessage(body, nil)
	if got != body {
		t.Fatalf("empty candidates must return body verbatim:\n got: %q\nwant: %q", got, body)
	}
	if strings.Contains(got, "LINK TARGETS") {
		t.Fatalf("no LINK TARGETS block should appear with zero candidates")
	}
}

func TestBuildPolishUserMessage_WithCandidates(t *testing.T) {
	body := "Body about auth."
	cands := []LinkCandidate{
		{Title: "Auth Flow", Path: "auth-flow.md"},
		{Title: "JWT Tokens", Path: "jwt.md", Aliases: []string{"JWT", "json web token"}},
	}
	got := BuildPolishUserMessage(body, cands)

	if !strings.HasPrefix(got, body) {
		t.Fatalf("body must come first:\n%q", got)
	}
	if !strings.Contains(got, linkTargetsHeader) {
		t.Fatalf("missing delimiter header:\n%q", got)
	}
	if !strings.Contains(got, "- Auth Flow\n") {
		t.Fatalf("missing plain candidate line:\n%q", got)
	}
	if !strings.Contains(got, "- JWT Tokens  (aliases: JWT, json web token)") {
		t.Fatalf("missing aliased candidate line:\n%q", got)
	}
}
