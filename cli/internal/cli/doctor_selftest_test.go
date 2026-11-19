package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestModelCheck_OKDetailCarriesProof(t *testing.T) {
	tests := []struct {
		name string
		res  *ai.TestProbeResult
		want string
	}{
		{"generation with reply", &ai.TestProbeResult{OK: true, Type: "generation", Latency: "812ms", Detail: "4"}, `responded in 812ms (replied "4")`},
		{"embedding with dims", &ai.TestProbeResult{OK: true, Type: "embedding", Latency: "90ms", Detail: "dims=256, L2=1.00"}, "responded in 90ms (dims=256, L2=1.00)"},
		{"generation without detail", &ai.TestProbeResult{OK: true, Type: "generation", Latency: "1s"}, "responded in 1s"},
		{"embedding without detail", &ai.TestProbeResult{OK: true, Type: "embedding", Latency: "1s"}, "responded in 1s"},
		{"whitespace-only detail", &ai.TestProbeResult{OK: true, Type: "generation", Latency: "1s", Detail: "  \n"}, "responded in 1s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := modelCheck("answer model", "m", tt.res)
			if !c.OK || c.Detail != tt.want {
				t.Errorf("modelCheck = ok:%v detail:%q, want ok:true detail:%q", c.OK, c.Detail, tt.want)
			}
			if !strings.HasPrefix(c.Detail, "responded in ") {
				t.Errorf("detail lost its stable prefix: %q", c.Detail)
			}
		})
	}
	// A failure never claims a response.
	fail := modelCheck("answer model", "m", &ai.TestProbeResult{OK: false, Detail: "boom", Code: ai.TestErrUnknown})
	if fail.OK || strings.Contains(fail.Detail, "responded in") {
		t.Errorf("failure rendered as success: %+v", fail)
	}
}

func TestApplyDoctorGeminiProbeMode(t *testing.T) {
	base := func() ai.AIConfig {
		cfg := ai.DefaultAIConfig()
		cfg.Gemini.UseVertexAI = true
		cfg.Gemini.Project = "saved-proj"
		cfg.Gemini.Location = "us-central1"
		return cfg
	}

	t.Run("unset leaves config alone", func(t *testing.T) {
		t.Setenv("2NB_DOCTOR_PROBE_GEMINI_MODE", "")
		cfg := base()
		applyDoctorGeminiProbeMode(&cfg)
		if !cfg.Gemini.UseVertexAI || cfg.Gemini.Project != "saved-proj" {
			t.Errorf("config mutated: %+v", cfg.Gemini)
		}
	})
	t.Run("apikey clears vertex and project", func(t *testing.T) {
		t.Setenv("2NB_DOCTOR_PROBE_GEMINI_MODE", "apikey")
		cfg := base()
		applyDoctorGeminiProbeMode(&cfg)
		if cfg.Gemini.UseVertexAI || cfg.Gemini.Project != "" {
			t.Errorf("apikey mode left Vertex state behind: %+v", cfg.Gemini)
		}
	})
	t.Run("vertex applies project and location", func(t *testing.T) {
		t.Setenv("2NB_DOCTOR_PROBE_GEMINI_MODE", "vertex")
		t.Setenv("2NB_DOCTOR_PROBE_GCP_PROJECT", "typed-proj")
		t.Setenv("2NB_DOCTOR_PROBE_GCP_LOCATION", "europe-west4")
		cfg := ai.DefaultAIConfig()
		applyDoctorGeminiProbeMode(&cfg)
		if !cfg.Gemini.UseVertexAI || cfg.Gemini.Project != "typed-proj" || cfg.Gemini.Location != "europe-west4" {
			t.Errorf("vertex mode: %+v", cfg.Gemini)
		}
	})
	t.Run("vertex without overrides keeps saved project", func(t *testing.T) {
		t.Setenv("2NB_DOCTOR_PROBE_GEMINI_MODE", "vertex")
		t.Setenv("2NB_DOCTOR_PROBE_GCP_PROJECT", "")
		t.Setenv("2NB_DOCTOR_PROBE_GCP_LOCATION", "")
		cfg := base()
		applyDoctorGeminiProbeMode(&cfg)
		if !cfg.Gemini.UseVertexAI || cfg.Gemini.Project != "saved-proj" || cfg.Gemini.Location != "us-central1" {
			t.Errorf("vertex mode clobbered saved values: %+v", cfg.Gemini)
		}
	})
}

// modelsTestProbeOffline runs `models test` for a local model that cannot start
// (no weights, hermetic state) so the probe fails fast and deterministically.
func modelsTestProbeOffline(t *testing.T, root string, extra ...string) (res struct {
	OK   bool   `json:"ok"`
	Code string `json:"code"`
}) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("TWONB_SKIP_ENGINE_AUTOSTART", "1")
	argv := append([]string{"models", "test", "embeddinggemma-2", "--provider", "llama-local", "--json", "--porcelain"}, extra...)
	got, err := runCLIArgs(t, root, argv...)
	if err != nil {
		t.Fatalf("models test: %v (out=%s)", err, truncate(got, 400))
	}
	if err := json.Unmarshal(got, &res); err != nil {
		t.Fatalf("models test JSON: %v (body=%s)", err, truncate(got, 400))
	}
	return res
}

// `models test` is a probe, not a vault operation: on a plain Obsidian folder
// (no .2ndbrain/) it must run and must NOT mint a sidecar.
func TestModelsTest_NoSidecarOnPlainObsidianFolder(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := modelsTestProbeOffline(t, root)
	if res.OK {
		t.Fatal("probe of an unstartable local model unexpectedly passed")
	}
	if res.Code != string(ai.TestErrProviderUnreachable) {
		t.Errorf("code = %q, want provider_unreachable", res.Code)
	}
	if _, err := os.Stat(filepath.Join(root, ".2ndbrain")); !os.IsNotExist(err) {
		t.Errorf("models test created a .2ndbrain sidecar on an uninitialized folder (stat err=%v)", err)
	}
}

// With no vault resolving at all (the Settings window's first-run state) the
// probe still runs.
func TestModelsTest_NoVaultBound(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	res := modelsTestProbeOffline(t, root)
	if res.OK || res.Code != string(ai.TestErrProviderUnreachable) {
		t.Errorf("no-vault probe: %+v", res)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("models test created %s (stat err=%v)", root, err)
	}
}

// A GLOBAL-scoped --save needs no vault either, and still leaves no sidecar.
func TestModelsTest_GlobalSaveNeedsNoSidecar(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := modelsTestProbeOffline(t, root, "--save", "--scope", "global")
	if res.OK {
		t.Fatal("probe unexpectedly passed")
	}
	if _, err := os.Stat(filepath.Join(root, ".2ndbrain")); !os.IsNotExist(err) {
		t.Errorf("global --save created a .2ndbrain sidecar (stat err=%v)", err)
	}
	var saved bool
	for _, m := range ai.LoadUserCatalog("") {
		if m.ID == "embeddinggemma-2" && m.TestErrorCode == string(ai.TestErrProviderUnreachable) {
			saved = true
		}
	}
	if !saved {
		t.Error("global --save did not persist the classified failure")
	}
}

// An existing sidecar is still honored: the vault opens and its config governs.
func TestModelsTest_SidecarVaultStillOpens(t *testing.T) {
	_, root := newContractVault(t)
	res := modelsTestProbeOffline(t, root)
	if res.OK {
		t.Fatal("probe unexpectedly passed")
	}
}
