package ai

import (
	"math"
	"strings"
	"testing"
)

func TestInferProvider(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "bedrock"},
		{"amazon.nova-2-multimodal-embeddings-v1:0", "bedrock"},
		{"eu.anthropic.claude-sonnet-4-20250514-v1:0", "bedrock"},
		{"openai.gpt-5.5", "bedrock"},
		{"xai.grok-4.3", "bedrock"},
		{"anthropic/claude-haiku-4-5", "openrouter"},
		{"google/gemma-4-31b-it:free", "openrouter"},
		{"openai/gpt-4o", "openrouter"},
		{"embeddinggemma-2", "llama-local"},
		{"gemma4-e2b", "llama-local"},
		{"gemma4-e4b", "llama-local"},
		{"bge-reranker-v2-m3", "llama-local"},
		{"gemma3:4b", "ollama"},
		{"nomic-embed-text", "ollama"},
		{"qwen2.5:0.5b", "ollama"},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := InferProvider(tt.modelID)
			if got != tt.expected {
				t.Errorf("InferProvider(%q) = %q, want %q", tt.modelID, got, tt.expected)
			}
		})
	}
}

func TestInferModelType(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"amazon.nova-2-multimodal-embeddings-v1:0", "embedding"},
		{"nomic-embed-text", "embedding"},
		{"nvidia/llama-nemotron-embed-vl-1b-v2:free", "embedding"},
		{"embeddinggemma-2", "embedding"},
		{"bge-reranker-v2-m3", "rerank"},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "generation"},
		{"google/gemma-4-31b-it:free", "generation"},
		{"gemma3:4b", "generation"},
		{"gemma4-e2b", "generation"},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := InferModelType(tt.modelID)
			if got != tt.expected {
				t.Errorf("InferModelType(%q) = %q, want %q", tt.modelID, got, tt.expected)
			}
		})
	}
}

// TestProbeBudgetConstantsPinned holds the probe output budget offline where
// plain CI always runs (the live guard, TestLiveGrok46ClassicConverse_CredGated,
// is credential-gated and so invisible to CI). The budget is load-bearing:
// always-reasoning models bill reasoning against the output budget (a live
// "what is 2+2" on grok-4.6 cost 180 output tokens), so a revert toward the
// old 32-token cap truncates mid-reasoning and fails a working model. The
// floor is 1024, not the earlier 256: the probe measures entitlement, not
// budget, so it needs headroom for a deeper reasoning-by-default model to
// still emit answer text after its reasoning overhead, not just enough to
// match one measured 180-token sample. The three values are pinned together
// so the cost estimate can never under-report what a probe may bill and the
// classic-plane budget can never drop below the mantle client's floor, which
// exists for the same cause.
func TestProbeBudgetConstantsPinned(t *testing.T) {
	if probeGenMaxTokens < 1024 {
		t.Errorf("probeGenMaxTokens = %d; below 1024 leaves too little headroom for a deep reasoning-by-default model to emit answer text after its reasoning overhead (measured 180 tokens for grok-4.6 on a trivial prompt, but the probe must cover models with much larger reasoning overhead too)", probeGenMaxTokens)
	}
	if got := DefaultProbeSpec(ProbeTest).OutputTokens; got != probeGenMaxTokens {
		t.Errorf("ProbeTest cost spec OutputTokens = %d, want probeGenMaxTokens (%d); the estimate must not under-report what a probe may bill", got, probeGenMaxTokens)
	}
	if probeGenMaxTokens < mantleMinOutputTokens {
		t.Errorf("probeGenMaxTokens = %d below mantleMinOutputTokens = %d; the classic probe budget must cover the same reasoning overhead the mantle floor exists for", probeGenMaxTokens, mantleMinOutputTokens)
	}
}

func TestAssignResolvedEmbedStrategyOverridesCatalogLookup(t *testing.T) {
	// A discovered-only id has no catalog row, so the constructor would
	// store an empty strategy and fall through to detectEmbedFormat. The
	// probe already resolved the candidate's InvokeStrategy; that value
	// must win.
	e := &BedrockEmbedder{model: "vendor.custom-embed-v9"}
	assignResolvedEmbedStrategy(e, StrategyBedrockInvokeCohereEmbed)
	if e.strategy != StrategyBedrockInvokeCohereEmbed {
		t.Fatalf("strategy = %q, want the resolved Cohere embed strategy", e.strategy)
	}
	got := e.resolvedEmbedFormat()
	want, ok := bedrockEmbedFormatFromStrategy(StrategyBedrockInvokeCohereEmbed)
	if !ok {
		t.Fatal("Cohere embed strategy should map to a format")
	}
	if got != want {
		t.Errorf("resolvedEmbedFormat = %v, want %v (the resolved strategy's format, not id detection)", got, want)
	}
}

func TestClassicRegionFanOutIgnoresSiblingMantleRow(t *testing.T) {
	setupHome(t)
	id := "xai.grok-4.6"
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: id, Provider: "bedrock", Type: "generation",
		Plane: PlaneMantle, InvokeStrategy: StrategyBedrockMantleResponses,
		Region: "us-west-2",
	}); err != nil {
		t.Fatal(err)
	}
	classic := ModelInfo{ID: id, Provider: "bedrock", Type: "generation", Plane: PlaneClassic}
	if !usesClassicRegionFanOut(classic, "") {
		t.Fatal("@classic candidate skipped classic region fan-out because a sibling mantle row won the plane-blind lookup")
	}
	mantle := ModelInfo{ID: id, Provider: "bedrock", Type: "generation", Plane: PlaneMantle}
	if usesClassicRegionFanOut(mantle, "") {
		t.Fatal("named mantle plane must not fan out across classic regions")
	}
}

func TestResolveProbeDimensions(t *testing.T) {
	tests := []struct {
		name       string
		cfg        AIConfig
		provider   string
		modelID    string
		wantProbe  int
		wantStrict int
	}{
		{
			name:     "configured slot honors a supported override and enforces it",
			cfg:      AIConfig{Provider: "gemini", EmbeddingModel: "gemini-embedding-2", Dimensions: 1024},
			provider: "gemini", modelID: "gemini-embedding-2",
			wantProbe: 1024, wantStrict: 1024,
		},
		{
			name:     "configured slot default width is enforced",
			cfg:      AIConfig{Provider: "gemini", EmbeddingModel: "gemini-embedding-2", Dimensions: 768},
			provider: "gemini", modelID: "gemini-embedding-2",
			wantProbe: 768, wantStrict: 768,
		},
		{
			name:     "configured slot out-of-catalog override is requested but not enforced",
			cfg:      AIConfig{EmbeddingProvider: "llama-local", EmbeddingModel: "embeddinggemma-2", Dimensions: 1024},
			provider: "llama-local", modelID: "embeddinggemma-2",
			wantProbe: 1024, wantStrict: 0,
		},
		{
			name:     "configured slot matryoshka width is enforced",
			cfg:      AIConfig{EmbeddingProvider: "llama-local", EmbeddingModel: "embeddinggemma-2", Dimensions: 256},
			provider: "llama-local", modelID: "embeddinggemma-2",
			wantProbe: 256, wantStrict: 256,
		},
		{
			name:     "other model takes a width it supports",
			cfg:      AIConfig{Provider: "gemini", EmbeddingModel: "gemini-embedding-2", Dimensions: 1024},
			provider: "gemini", modelID: "gemini-embedding-001",
			wantProbe: 1024, wantStrict: 1024,
		},
		{
			name:     "other model ignores a width it cannot emit and uses its catalog default",
			cfg:      AIConfig{Provider: "gemini", EmbeddingModel: "gemini-embedding-2", Dimensions: 999},
			provider: "gemini", modelID: "gemini-embedding-001",
			wantProbe: 768, wantStrict: 768,
		},
		{
			name:     "other provider with no dimensions uses the catalog default",
			cfg:      AIConfig{Provider: "gemini", EmbeddingModel: "gemini-embedding-2"},
			provider: "llama-local", modelID: "embeddinggemma-2",
			wantProbe: 768, wantStrict: 768,
		},
		{
			name:     "unknown model enforces nothing",
			cfg:      AIConfig{Provider: "ollama"},
			provider: "ollama", modelID: "not-in-any-catalog",
			wantProbe: 0, wantStrict: 0,
		},
		{
			name:     "unknown model in the configured slot requests the override, enforces nothing",
			cfg:      AIConfig{Provider: "ollama", EmbeddingModel: "not-in-any-catalog", Dimensions: 512},
			provider: "ollama", modelID: "not-in-any-catalog",
			wantProbe: 512, wantStrict: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe, strict := resolveProbeDimensions(tt.cfg, tt.provider, tt.modelID, "")
			if probe != tt.wantProbe || strict != tt.wantStrict {
				t.Errorf("resolveProbeDimensions = (%d, %d), want (%d, %d)", probe, strict, tt.wantProbe, tt.wantStrict)
			}
		})
	}
}

func TestValidateEmbeddingProbeVector(t *testing.T) {
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	tests := []struct {
		name       string
		vecs       [][]float32
		strict     int
		wantErr    string
		wantDetail string
	}{
		{"nil result", nil, 0, "got empty embedding vector", ""},
		{"empty first vector", [][]float32{{}}, 0, "got empty embedding vector", ""},
		{"wrong width", [][]float32{{1, 0, 0}}, 4, "embedding dimension mismatch from m: got 3d, expected 4d", ""},
		{"NaN component", [][]float32{{1, nan}}, 0, "non-finite", ""},
		{"Inf component", [][]float32{{inf, 1}}, 0, "non-finite", ""},
		{"all zeros", [][]float32{{0, 0, 0}}, 3, "zero/degenerate L2 norm", ""},
		{"vanishing norm", [][]float32{{1e-9, 1e-9}}, 0, "zero/degenerate L2 norm", ""},
		{"3-4-5 vector", [][]float32{{3, 4}}, 2, "", "dims=2, L2=5.00"},
		{"unit vector, width not enforced", [][]float32{{1, 0, 0, 0}}, 0, "", "dims=4, L2=1.00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateEmbeddingProbeVector("m", tt.vecs, tt.strict)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantDetail {
				t.Errorf("detail = %q, want %q", got, tt.wantDetail)
			}
		})
	}
}

func TestValidateGenerationProbeResponse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{"empty", "", "", "empty response from m"},
		{"whitespace only", " \n\t ", "", "empty response from m"},
		{"digit", "4", "4", ""},
		{"digit with newline", "\n4\n", "4", ""},
		{"sentence", "2+2 equals 4.", "2+2 equals 4.", ""},
		{"word, mixed case", "Four", "Four", ""},
		{"internal whitespace collapsed", "The answer\n\n  is   4", "The answer is 4", ""},
		{"unrelated", "I cannot help with that", "", `unexpected response from m (expected 4, got "I cannot help with that")`},
		{"wrong number", "5", "", "unexpected response from m (expected 4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateGenerationProbeResponse("m", tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateGenerationProbeResponse_TruncatesByRune(t *testing.T) {
	long := "4 " + strings.Repeat("é", 200)
	got, err := validateGenerationProbeResponse("m", long)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(strings.TrimSuffix(got, "…"))); n != 80 {
		t.Errorf("truncated to %d runes, want 80", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated response should end with an ellipsis: %q", got)
	}
	for _, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("truncation split a multi-byte rune: %q", got)
		}
	}
	if got := truncateProbeRunes("short", 80); got != "short" {
		t.Errorf("short input changed: %q", got)
	}
}

// A dead explicit endpoint must be dialed directly even with no cached weights,
// and an unconfigured default must fail fast on the missing weights; both are
// provider_unreachable, and neither may report success.
func TestProbeLlamaLocalGuard_ExplicitEndpointDialed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("TWONB_SKIP_ENGINE_AUTOSTART", "1")

	cfg := AIConfig{}
	cfg.Llama.GenEndpoint = "http://127.0.0.1:9"
	res, err := TestProbeModel(t.Context(), cfg, "gemma4-e2b", "llama-local", "generation", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("generation probe against a dead endpoint passed")
	}
	if strings.Contains(res.Detail, "not available for generation") {
		t.Errorf("explicit endpoint should be dialed, got the weights guard: %q", res.Detail)
	}
	if res.Code != TestErrProviderUnreachable && res.Code != TestErrTimeout {
		t.Errorf("code = %q (detail %q)", res.Code, res.Detail)
	}

	res, err = TestProbeModel(t.Context(), AIConfig{}, "gemma4-e2b", "llama-local", "generation", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Code != TestErrProviderUnreachable {
		t.Errorf("default probe without weights: ok=%v code=%q detail=%q", res.OK, res.Code, res.Detail)
	}
}
