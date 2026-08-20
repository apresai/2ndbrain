package cli

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// deriveCredentialVerdict is pure logic over probe results (no provider call),
// so it is unit-tested directly. The cases below encode live-measured provider
// behavior: a bogus bearer token 403s on the classic Bedrock runtime plane
// (access_denied) while the mantle plane 401s with invalid_api_key
// (bad_credentials). A verdict that trusted either signal alone would be wrong.
func TestDeriveCredentialVerdict(t *testing.T) {
	ok := func(id string) *ai.TestProbeResult {
		return &ai.TestProbeResult{ModelID: id, OK: true}
	}
	failed := func(id string, code ai.TestErrorCode) *ai.TestProbeResult {
		return &ai.TestProbeResult{ModelID: id, Code: code}
	}

	tests := []struct {
		name    string
		results []*ai.TestProbeResult
		want    string
		wantWhy bool
	}{
		{
			name:    "any success proves the key authenticated",
			results: []*ai.TestProbeResult{ok("embed"), failed("gen", ai.TestErrAccessDenied)},
			want:    credAccepted,
		},
		{
			name:    "bad_credentials is definitive even when another probe merely 403s",
			results: []*ai.TestProbeResult{failed("embed", ai.TestErrAccessDenied), failed("gen", ai.TestErrBadCredentials)},
			want:    credRejected,
		},
		{
			name:    "bad_credentials wins regardless of probe order",
			results: []*ai.TestProbeResult{failed("gen", ai.TestErrBadCredentials), failed("embed", ai.TestErrAccessDenied)},
			want:    credRejected,
		},
		{
			name:    "access_denied alone is ambiguous, never 'accepted'",
			results: []*ai.TestProbeResult{failed("embed", ai.TestErrAccessDenied), failed("gen", ai.TestErrAccessDenied)},
			want:    credUnknown,
			wantWhy: true,
		},
		{
			name:    "network failure reports unreachable, not rejected",
			results: []*ai.TestProbeResult{failed("embed", ai.TestErrProviderUnreachable), failed("gen", ai.TestErrTimeout)},
			want:    credUnreachable,
		},
		{
			name:    "success outranks a network failure on the other slot",
			results: []*ai.TestProbeResult{failed("embed", ai.TestErrProviderUnreachable), ok("gen")},
			want:    credAccepted,
		},
		{
			name:    "no probes ran at all",
			results: []*ai.TestProbeResult{nil, nil},
			want:    credUnknown,
			wantWhy: true,
		},
		{
			name:    "an unclassified failure stays unknown",
			results: []*ai.TestProbeResult{failed("embed", ai.TestErrUnknown)},
			want:    credUnknown,
			wantWhy: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := deriveCredentialVerdict(tt.results...)
			if got != tt.want {
				t.Errorf("verdict = %q, want %q", got, tt.want)
			}
			if tt.wantWhy && why == "" {
				t.Error("expected an explanation for the inconclusive verdict, got none")
			}
			if !tt.wantWhy && why != "" {
				t.Errorf("expected no explanation for a definitive verdict, got %q", why)
			}
		})
	}
}

// A rejected key must never render as accepted, and an inconclusive verdict
// must say why rather than leaving the user to guess.
func TestCredentialDetail(t *testing.T) {
	if got := credentialDetail("bedrock", credRejected, ""); got != "rejected by bedrock" {
		t.Errorf("rejected detail = %q", got)
	}
	if got := credentialDetail("bedrock", credAccepted, ""); got != "accepted by bedrock" {
		t.Errorf("accepted detail = %q", got)
	}
	got := credentialDetail("bedrock", credUnknown, "every model refused with access denied")
	if got == "" || got == "accepted by bedrock" {
		t.Errorf("unknown detail must explain itself, got %q", got)
	}
}

// A rejected key gets a key-fixing command; an ambiguous one must not send the
// user to replace a key that may well be fine.
func TestCredentialFix(t *testing.T) {
	if fix := credentialFix("bedrock", credRejected); fix == "" {
		t.Error("a rejected key must carry a fix command")
	}
	if fix := credentialFix("bedrock", credAccepted); fix != "" {
		t.Errorf("an accepted key needs no fix, got %q", fix)
	}
}

// tokenSuffix backs the masked-key display. It must reveal enough to identify a
// key and refuse to reveal a meaningful fraction of a short one.
func TestTokenSuffix(t *testing.T) {
	tests := []struct {
		token string
		want  string
	}{
		{"", ""},
		{"short", ""},
		{"abcdefgh", ""},         // 8 chars: too short, 4 would be half
		{"abcdefghijkl", "ijkl"}, // 12 chars: at the threshold
		// A realistic length. Deliberately not shaped like any real vendor's key
		// prefix: git-secrets scans committed test fixtures too, and a plausible
		// looking fake is indistinguishable from a leak at commit time.
		{"bearer-token-placeholder-0123456789", "6789"},
	}
	for _, tt := range tests {
		if got := tokenSuffix(tt.token); got != tt.want {
			t.Errorf("tokenSuffix(%q) = %q, want %q", tt.token, got, tt.want)
		}
	}
}

// firstSearchMode is what turned mcp doctor's kb_search check from a rubber
// stamp into a real assertion, so its parsing is pinned.
func TestFirstSearchMode(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
		wantOK  bool
	}{
		{"hybrid result", `[{"search_mode":"hybrid"}]`, "hybrid", true},
		{"keyword fallback", `[{"search_mode":"keyword"}]`, "keyword", true},
		{"empty result set is not judgable", `[]`, "", false},
		{"malformed payload", `not json`, "", false},
		{"missing field", `[{"title":"x"}]`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := firstSearchMode(tt.payload)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("firstSearchMode(%q) = (%q, %v), want (%q, %v)", tt.payload, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// classifySearchMode is the rule that turns a BM25 fallback into a failure.
// It is the whole point of the kb_search check: retrieval degrading to keyword
// search is invisible in the error channel, so a vault with embeddings that
// answers in keyword mode must be loud, while a vault with no embeddings at all
// must not be — BM25 is simply correct there.
func TestClassifySearchMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		observable bool
		embedded   int
		wantOK     bool
		wantWarn   bool
	}{
		{
			name: "hybrid with embeddings is a clean pass",
			mode: "hybrid", observable: true, embedded: 277,
			wantOK: true, wantWarn: false,
		},
		{
			name: "keyword fallback WITH embeddings is a hard failure",
			mode: "keyword", observable: true, embedded: 277,
			wantOK: false, wantWarn: false,
		},
		{
			name: "keyword with no embeddings warns, never fails",
			mode: "keyword", observable: true, embedded: 0,
			wantOK: true, wantWarn: true,
		},
		{
			name: "an empty result set is not judged either way",
			mode: "", observable: false, embedded: 277,
			wantOK: true, wantWarn: false,
		},
		{
			name: "hybrid on an unembedded vault still passes",
			mode: "hybrid", observable: true, embedded: 0,
			wantOK: true, wantWarn: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySearchMode("kb_search round-trip", tt.mode, tt.observable, tt.embedded)
			if got.OK != tt.wantOK {
				t.Errorf("OK = %v, want %v (detail: %s)", got.OK, tt.wantOK, got.Detail)
			}
			if got.Warn != tt.wantWarn {
				t.Errorf("Warn = %v, want %v (detail: %s)", got.Warn, tt.wantWarn, got.Detail)
			}
			if !got.OK && got.Fix == "" {
				t.Error("a failing check must carry a fix hint")
			}
		})
	}
}

// trimDetail truncates provider error strings for one-line display; truncating
// by bytes would split a multi-byte rune and emit invalid UTF-8 into --json.
func TestTrimDetailKeepsValidUTF8(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "é"
	}
	got := trimDetail(long)
	for i, r := range got {
		if r == '�' {
			t.Fatalf("trimDetail produced an invalid rune at byte %d", i)
		}
	}
	if trimDetail("") != "(no detail)" {
		t.Error("empty detail should render as (no detail)")
	}
	if got := trimDetail("done\n"); got != "done" {
		t.Errorf("trailing newline not stripped: %q", got)
	}
}

func TestBedrockKeySourceCheck(t *testing.T) {
	// No divergence: no check at all.
	if c := bedrockKeySourceCheck(ai.TokenDivergence{Diverges: false}); c != nil {
		t.Fatalf("no divergence should render no check: %+v", c)
	}
	// Divergence without prefer: the split-brain WARNING with both suffixes
	// and the prefer-stored fix hint.
	warn := bedrockKeySourceCheck(ai.TokenDivergence{
		Diverges: true, EnvSuffix: "dkE9", StoredSuffix: "RT0=",
	})
	if warn == nil || !warn.Warn || !warn.OK {
		t.Fatalf("divergence should warn: %+v", warn)
	}
	for _, want := range []string{"dkE9", "RT0=", "overrides"} {
		if !strings.Contains(warn.Detail, want) {
			t.Errorf("warn detail missing %q: %s", want, warn.Detail)
		}
	}
	if !strings.Contains(warn.Fix, "prefer-stored-token") {
		t.Errorf("warn fix should name the prefer flag: %s", warn.Fix)
	}
	// Divergence WITH prefer: informational (no Warn), stored key leads.
	info := bedrockKeySourceCheck(ai.TokenDivergence{
		Diverges: true, PreferStored: true, EnvSuffix: "dkE9", StoredSuffix: "RT0=",
	})
	if info == nil || info.Warn || !info.OK {
		t.Fatalf("prefer divergence should be informational: %+v", info)
	}
	if !strings.Contains(info.Detail, "prefer_stored_token is on") || !strings.Contains(info.Detail, "RT0=") {
		t.Errorf("info detail wrong: %s", info.Detail)
	}
}
