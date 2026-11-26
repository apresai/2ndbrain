package ai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestGemini_NormalizeL2 validates vector normalization and zero-norm / epsilon protection.
func TestGemini_NormalizeL2(t *testing.T) {
	// 3-4-5 triangle: vector [3, 4] -> norm 5 -> [0.6, 0.8]
	v := []float32{3, 4}
	normed := normalizeL2(v)
	if len(normed) != 2 {
		t.Fatalf("len = %d, want 2", len(normed))
	}
	if math.Abs(float64(normed[0])-0.6) > 1e-6 || math.Abs(float64(normed[1])-0.8) > 1e-6 {
		t.Errorf("normalized [3, 4] = %v, want [0.6, 0.8]", normed)
	}

	// Unit vector length should be 1.0
	var sum float64
	for _, x := range normed {
		sum += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(sum)-1.0) > 1e-6 {
		t.Errorf("L2 norm of normalized vector = %f, want 1.0", math.Sqrt(sum))
	}

	// Zero vector protection: norm < 1e-12 should not divide by zero or produce NaN
	zeroVec := []float32{0, 0, 0}
	normedZero := normalizeL2(zeroVec)
	for i, x := range normedZero {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			t.Errorf("zero vector normalization produced NaN/Inf at index %d: %v", i, normedZero)
		}
		if x != 0 {
			t.Errorf("zero vector component %d = %f, want 0", i, x)
		}
	}

	// Tiny vector below epsilon protection (1e-13)
	tinyVec := []float32{1e-13, 0}
	normedTiny := normalizeL2(tinyVec)
	if normedTiny[0] != tinyVec[0] {
		t.Errorf("sub-epsilon vector should be returned unchanged, got %v", normedTiny)
	}
}

// TestGemini_ResolveGCPProject tests project resolution precedence for BYOK GCP environments.
func TestGemini_ResolveGCPProject(t *testing.T) {
	// Clean env
	os.Unsetenv("GOOGLE_CLOUD_PROJECT")
	os.Unsetenv("GCLOUD_PROJECT")
	os.Unsetenv("CLOUDSDK_CORE_PROJECT")

	// Config wins above all env vars
	os.Setenv("GOOGLE_CLOUD_PROJECT", "env-project")
	if got := ResolveGCPProject("cfg-project"); got != "cfg-project" {
		t.Errorf("ResolveGCPProject with cfg = %q, want cfg-project", got)
	}
	os.Unsetenv("GOOGLE_CLOUD_PROJECT")

	// GOOGLE_CLOUD_PROJECT
	os.Setenv("GOOGLE_CLOUD_PROJECT", "gcp-proj-1")
	if got := ResolveGCPProject(""); got != "gcp-proj-1" {
		t.Errorf("ResolveGCPProject with GOOGLE_CLOUD_PROJECT = %q, want gcp-proj-1", got)
	}
	os.Unsetenv("GOOGLE_CLOUD_PROJECT")

	// GCLOUD_PROJECT fallback
	os.Setenv("GCLOUD_PROJECT", "gcloud-proj-2")
	if got := ResolveGCPProject(""); got != "gcloud-proj-2" {
		t.Errorf("ResolveGCPProject with GCLOUD_PROJECT = %q, want gcloud-proj-2", got)
	}
	os.Unsetenv("GCLOUD_PROJECT")

	// CLOUDSDK_CORE_PROJECT fallback
	os.Setenv("CLOUDSDK_CORE_PROJECT", "cloudsdk-proj-3")
	if got := ResolveGCPProject(""); got != "cloudsdk-proj-3" {
		t.Errorf("ResolveGCPProject with CLOUDSDK_CORE_PROJECT = %q, want cloudsdk-proj-3", got)
	}
	os.Unsetenv("CLOUDSDK_CORE_PROJECT")

	// Empty when nothing is set
	if got := ResolveGCPProject(""); got != "" {
		t.Errorf("ResolveGCPProject with empty env = %q, want empty", got)
	}
}

// TestGemini_NewGeminiClient_ProhibitsGoogleAPIKey enforces project safety rule:
// GOOGLE_API_KEY must never be read or accepted.
func TestGemini_NewGeminiClient_ProhibitsGoogleAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "prohibited-secret")
	ctx := context.Background()

	_, err := NewGeminiClient(ctx, GeminiConfig{})
	if err == nil {
		t.Fatal("NewGeminiClient must fail when GOOGLE_API_KEY is present in environment")
	}
	if !strings.Contains(err.Error(), "GOOGLE_API_KEY is prohibited") {
		t.Errorf("expected prohibition error message, got %v", err)
	}
}

// TestGemini_NewGeminiClient_VertexWithoutProject ensures actionable BYOK error
// when Vertex AI is requested but no GCP project is provided.
func TestGemini_NewGeminiClient_VertexWithoutProject(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "")

	ctx := context.Background()
	_, err := NewGeminiClient(ctx, GeminiConfig{UseVertexAI: true, Project: ""})
	if err == nil {
		t.Fatal("NewGeminiClient with UseVertexAI and no project must fail")
	}
	if !strings.Contains(err.Error(), "no GCP project configured") {
		t.Errorf("expected 'no GCP project configured' error, got %v", err)
	}
	if !strings.Contains(err.Error(), "ai.gemini.project") {
		t.Errorf("expected remediation hint naming 'ai.gemini.project', got %v", err)
	}
}

// TestGemini_NewGeminiClient_BYOKMissingKey ensures actionable guidance when
// no project is configured and no GEMINI_API_KEY exists.
func TestGemini_NewGeminiClient_BYOKMissingKey(t *testing.T) {
	t.Setenv("2NB_BEDROCK_SKIP_KEYCHAIN", "1")
	t.Setenv("2NB_SKIP_KEYCHAIN", "1")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "")

	ctx := context.Background()
	_, err := NewGeminiClient(ctx, GeminiConfig{UseVertexAI: false, Project: ""})
	if err == nil {
		t.Fatal("NewGeminiClient with no credentials must fail")
	}
	if !strings.Contains(err.Error(), "GEMINI_API_KEY is not set") {
		t.Errorf("expected missing key message, got %v", err)
	}
}

// TestGemini_IsRateLimited checks 429 and RESOURCE_EXHAUSTED detection.
func TestGemini_IsRateLimited(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"api error 429", &genai.APIError{Code: 429, Message: "Too Many Requests"}, true},
		{"api error resource exhausted", &genai.APIError{Code: 403, Status: "RESOURCE_EXHAUSTED"}, true},
		{"grpc resource exhausted", status.Error(codes.ResourceExhausted, "quota exceeded"), true},
		{"text 429", errors.New("HTTP 429 returned from endpoint"), true},
		{"text quota exceeded", errors.New("user quota exceeded for project"), true},
		{"regular error", errors.New("something went wrong"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGeminiRateLimited(tt.err)
			if got != tt.want {
				t.Errorf("isGeminiRateLimited(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestGemini_ProviderBasics checks provider identification, dimensions, and model listing.
func TestGemini_ProviderBasics(t *testing.T) {
	cfg := GeminiConfig{
		Project:  "my-proj",
		Location: "us-central1",
	}
	p := NewGeminiProviderWithClient(nil, cfg, "gemini-embedding-2", "gemini-4-hydrogen", 768)

	if p.Name() != "gemini" {
		t.Errorf("p.Name() = %q, want gemini", p.Name())
	}
	if p.Dimensions() != 768 {
		t.Errorf("p.Dimensions() = %d, want 768", p.Dimensions())
	}
	if !p.isVertexAI() {
		t.Error("provider with Project set should report isVertexAI() = true")
	}

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 5 {
		t.Errorf("expected 5 Gemini models (2 embedding + 3 Gemini 4 generation), got %d", len(models))
	}

	// Verify current embedding and Gemini 4 generation models are present,
	// and legacy 2.5 / 3.x models are absent.
	var foundEmbed2, foundEmbed001, foundHydrogen, foundArgon, foundCarbon bool
	for _, m := range models {
		switch {
		case m.ID == "gemini-embedding-2" && m.Type == "embedding":
			foundEmbed2 = true
		case m.ID == "gemini-embedding-001" && m.Type == "embedding":
			foundEmbed001 = true
		case m.ID == "gemini-4-hydrogen" && m.Type == "generation":
			foundHydrogen = true
		case m.ID == "gemini-4-argon" && m.Type == "generation":
			foundArgon = true
		case m.ID == "gemini-4-carbon" && m.Type == "generation":
			foundCarbon = true
		}
		if strings.HasPrefix(m.ID, "gemini-2.") || strings.HasPrefix(m.ID, "gemini-3.") {
			t.Errorf("legacy non-Gemini-4 generation model %q should not appear in Gemini catalog", m.ID)
		}
	}
	if !foundEmbed2 {
		t.Error("gemini-embedding-2 not found in Gemini models list")
	}
	if !foundEmbed001 {
		t.Error("gemini-embedding-001 not found in Gemini models list")
	}
	if !foundHydrogen {
		t.Error("gemini-4-hydrogen not found in Gemini models list")
	}
	if !foundArgon {
		t.Error("gemini-4-argon not found in Gemini models list")
	}
	if !foundCarbon {
		t.Error("gemini-4-carbon not found in Gemini models list")
	}
}

// TestGemini_ThinkingConfigMapping verifies ThinkingLevel/ThinkingBudget mapping
// across Gemini 4 models and legacy custom model IDs.
func TestGemini_ThinkingConfigMapping(t *testing.T) {
	if got := thinkingConfigForModel("gemini-4-hydrogen", ""); got != nil {
		t.Errorf("empty effort should yield nil ThinkingConfig, got %+v", got)
	}

	tests := []struct {
		modelID    string
		effort     string
		wantLevel  genai.ThinkingLevel
		wantBudget *int32
	}{
		{"gemini-4-hydrogen", "none", genai.ThinkingLevelLow, nil},
		{"gemini-4-hydrogen", "low", genai.ThinkingLevelLow, nil},
		{"gemini-4-hydrogen", "medium", genai.ThinkingLevelMedium, nil},
		{"gemini-4-hydrogen", "high", genai.ThinkingLevelHigh, nil},
		{"gemini-4-hydrogen", "xhigh", geminiThinkingLevelXHigh, nil},
		{"gemini-4-argon", "medium", genai.ThinkingLevelMedium, nil},
		{"gemini-4-carbon", "high", genai.ThinkingLevelHigh, nil},
		{"gemini-4-carbon", "xhigh", genai.ThinkingLevelHigh, nil},
		{"gemini-3.5-flash-lite", "none", genai.ThinkingLevelMinimal, nil},
		{"gemini-2.5-flash", "none", "", genai.Ptr[int32](0)},
	}

	for _, tt := range tests {
		t.Run(tt.modelID+"/"+tt.effort, func(t *testing.T) {
			tc := thinkingConfigForModel(tt.modelID, tt.effort)
			if tc == nil {
				t.Fatalf("thinkingConfigForModel(%q, %q) = nil, want non-nil", tt.modelID, tt.effort)
			}
			if tt.wantLevel != "" && tc.ThinkingLevel != tt.wantLevel {
				t.Errorf("ThinkingLevel = %q, want %q", tc.ThinkingLevel, tt.wantLevel)
			}
			if tt.wantBudget != nil {
				if tc.ThinkingBudget == nil || *tc.ThinkingBudget != *tt.wantBudget {
					t.Errorf("ThinkingBudget = %v, want %d", tc.ThinkingBudget, *tt.wantBudget)
				}
			}
		})
	}
}

// TestGemini_BuildGenerateConfig enforces Gemini 4 parameter rules:
// omit Temperature on Gemini 4 models, floor MaxOutputTokens at 32768 while
// preserving larger caller budgets, and attach Vertex AI attribution labels.
func TestGemini_BuildGenerateConfig(t *testing.T) {
	for _, modelID := range []string{"gemini-4-hydrogen", "gemini-4-argon", "gemini-4-carbon"} {
		t.Run(modelID+"/floor_and_omit_temp", func(t *testing.T) {
			cfg := buildGeminiGenerateConfig(modelID, false, "", GenOpts{
				Temperature: Ptr(0.1),
				MaxTokens:   1024,
			})
			if cfg.Temperature != nil {
				t.Errorf("%s: expected nil Temperature on Gemini 4, got %v", modelID, *cfg.Temperature)
			}
			if cfg.MaxOutputTokens != gemini4MinOutputTokens {
				t.Errorf("%s: MaxOutputTokens = %d, want floor %d", modelID, cfg.MaxOutputTokens, gemini4MinOutputTokens)
			}
			if cfg.Labels != nil {
				t.Errorf("%s: expected nil Labels when not Vertex AI, got %v", modelID, cfg.Labels)
			}
		})
	}

	cfgLarge := buildGeminiGenerateConfig("gemini-4-hydrogen", true, "", GenOpts{
		MaxTokens:       65536,
		ReasoningEffort: "xhigh",
		SystemPrompt:    "Be concise.",
	})
	if cfgLarge.MaxOutputTokens != 65536 {
		t.Errorf("MaxOutputTokens = %d, want 65536 preserved", cfgLarge.MaxOutputTokens)
	}
	if cfgLarge.Labels["app"] != "2nb" {
		t.Errorf("Labels[app] = %q, want 2nb", cfgLarge.Labels["app"])
	}
	if cfgLarge.ThinkingConfig == nil || cfgLarge.ThinkingConfig.ThinkingLevel != geminiThinkingLevelXHigh {
		t.Errorf("ThinkingConfig = %+v, want XHIGH", cfgLarge.ThinkingConfig)
	}
	if cfgLarge.SystemInstruction == nil || len(cfgLarge.SystemInstruction.Parts) != 1 {
		t.Errorf("SystemInstruction not populated: %+v", cfgLarge.SystemInstruction)
	}
}

// TestGemini_RemediationUsesGenericProjectGuidance verifies that Gemini probe
// remediation strings stay vendor-generic (BYOK / BYOP) rather than naming any
// hardcoded GCP project.
func TestGemini_RemediationUsesGenericProjectGuidance(t *testing.T) {
	accessMsg := RemediationFor(TestErrAccessDenied, "gemini", "")
	if !strings.Contains(accessMsg, "your configured GCP project") || !strings.Contains(accessMsg, "roles/aiplatform.user") {
		t.Errorf("unexpected access_denied remediation: %q", accessMsg)
	}
	credMsg := RemediationFor(TestErrBadCredentials, "gemini", "")
	if !strings.Contains(credMsg, "GEMINI_API_KEY") || !strings.Contains(credMsg, "gcloud auth application-default login") {
		t.Errorf("unexpected bad_credentials remediation: %q", credMsg)
	}
	notFoundMsg := RemediationFor(TestErrNotFound, "gemini", "")
	if !strings.Contains(notFoundMsg, "gemini-embedding-2") || !strings.Contains(notFoundMsg, "gemini-4-hydrogen") {
		t.Errorf("unexpected not_found remediation: %q", notFoundMsg)
	}
}

// TestGemini_SplitEmbedBatches verifies sub-batching against both Gemini
// Developer API's 100-item ceiling and Vertex AI's per-call byte/token budget.
func TestGemini_SplitEmbedBatches(t *testing.T) {
	if got := splitGeminiEmbedBatches(nil, geminiEmbedMaxBatchItems, geminiEmbedMaxBatchChars); got != nil {
		t.Fatalf("empty input = %v, want nil", got)
	}

	// Case 1: 202 moderate chunks (mirrors >100-chunk multi-section markdown notes).
	chunks202 := make([]string, 202)
	for i := range chunks202 {
		chunks202[i] = fmt.Sprintf("## Section %d\ncontent for chunk %d", i+1, i+1)
	}
	batches := splitGeminiEmbedBatches(chunks202, geminiEmbedMaxBatchItems, geminiEmbedMaxBatchChars)
	wantLens := []int{100, 100, 2}
	if len(batches) != len(wantLens) {
		t.Fatalf("202 chunks split into %d batches, want %d", len(batches), len(wantLens))
	}
	var flattened []string
	for i, b := range batches {
		if len(b) != wantLens[i] {
			t.Errorf("batch[%d] len = %d, want %d", i, len(b), wantLens[i])
		}
		flattened = append(flattened, b...)
	}
	if !slices.Equal(flattened, chunks202) {
		t.Error("sub-batching altered order or contents of 202-chunk slice")
	}

	// Case 2: 93 chunks x 6,000 bytes (~558KB total, under item cap but over byte/token ceiling).
	chunks93 := make([]string, 93)
	for i := range chunks93 {
		chunks93[i] = strings.Repeat(fmt.Sprintf("%02d", i%100), 3000)
	}
	byteBatches := splitGeminiEmbedBatches(chunks93, geminiEmbedMaxBatchItems, geminiEmbedMaxBatchChars)
	if len(byteBatches) != 12 {
		t.Fatalf("93x6KB chunks split into %d batches, want 12 (8 per batch + remainder 5)", len(byteBatches))
	}
	var byteFlattened []string
	for i, b := range byteBatches {
		totalBytes := 0
		for _, s := range b {
			totalBytes += len(s)
		}
		if totalBytes > geminiEmbedMaxBatchChars {
			t.Errorf("batch[%d] has %d bytes > ceiling %d", i, totalBytes, geminiEmbedMaxBatchChars)
		}
		byteFlattened = append(byteFlattened, b...)
	}
	if !slices.Equal(byteFlattened, chunks93) {
		t.Error("byte-bounded sub-batching altered order or contents")
	}

	// Case 3: Single chunk larger than maxChars must form a 1-item batch (forward progress).
	oversized := []string{strings.Repeat("x", 60000), "small trailing chunk"}
	oversizedBatches := splitGeminiEmbedBatches(oversized, geminiEmbedMaxBatchItems, geminiEmbedMaxBatchChars)
	if len(oversizedBatches) != 2 || len(oversizedBatches[0]) != 1 || len(oversizedBatches[1]) != 1 {
		t.Fatalf("oversized single item batching = %v, want [[oversized], [trailing]]", []int{len(oversizedBatches[0]), len(oversizedBatches[1])})
	}
}

// TestGemini_RetryDelayAndObservability checks transient classification,
// RetryInfo / "Please retry in" extraction, max delay clamp, and RetryCounter recording.
func TestGemini_RetryDelayAndObservability(t *testing.T) {
	retryCases := []struct {
		name string
		err  error
		want bool
	}{
		{"429", &genai.APIError{Code: 429, Status: "RESOURCE_EXHAUSTED"}, true},
		{"503", &genai.APIError{Code: 503, Status: "UNAVAILABLE"}, true},
		{"500", &genai.APIError{Code: 500, Status: "INTERNAL"}, true},
		{"grpc unavailable", status.Error(codes.Unavailable, "upstream reset"), true},
		{"400 invalid argument", &genai.APIError{Code: 400, Status: "INVALID_ARGUMENT", Message: "at most 100 requests"}, false},
		{"401 bad key", &genai.APIError{Code: 401, Status: "UNAUTHENTICATED"}, false},
	}
	for _, tc := range retryCases {
		if got := isGeminiRetryable(tc.err); got != tc.want {
			t.Errorf("isGeminiRetryable(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	errWithDetail := &genai.APIError{
		Code:   429,
		Status: "RESOURCE_EXHAUSTED",
		Message: "Quota exceeded. Please retry in 12.405079454s.",
		Details: []map[string]any{
			{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "12s"},
		},
	}
	if d := parseGeminiRetryAfter(errWithDetail); d < 12*time.Second || d > 13*time.Second {
		t.Errorf("parseGeminiRetryAfter = %v, want ~12.405s", d)
	}
	wait := geminiRetryDelay(0, errWithDetail)
	if wait < 12*time.Second || wait > geminiMaxRetryDelay {
		t.Errorf("geminiRetryDelay(0, 12.4s) = %v, want in [12s, %v]", wait, geminiMaxRetryDelay)
	}

	errHugeDelay := errors.New("Error 429, Message: Please retry in 95s.")
	if capped := geminiRetryDelay(0, errHugeDelay); capped > geminiMaxRetryDelay {
		t.Errorf("geminiRetryDelay(95s) = %v > cap %v", capped, geminiMaxRetryDelay)
	}

	counter := &RetryCounter{}
	ctx := WithRetryCounter(context.Background(), counter)
	noteGeminiRetry(ctx, "embed", 1, geminiMaxAttempts, wait, errWithDetail)
	snap := counter.Snapshot()
	if snap.Count != 1 || snap.Cause != "Gemini throttled" || snap.MaxAttempts != geminiMaxAttempts {
		t.Errorf("RetryCounter snapshot = %+v, want Count=1 Cause='Gemini throttled' MaxAttempts=%d", snap, geminiMaxAttempts)
	}
}

func TestGemini_InitGemini_LazyWithoutCredentials(t *testing.T) {
	t.Setenv("2NB_BEDROCK_SKIP_KEYCHAIN", "1")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "")

	reg := NewRegistry()
	ctx := context.Background()
	cfg := DefaultAIConfig()
	cfg.Provider = "gemini"

	if err := InitGemini(ctx, reg, cfg.Gemini, cfg); err != nil {
		t.Fatalf("InitGemini should register lazily without error when credentials are absent, got: %v", err)
	}

	emb, err := reg.Embedder("gemini")
	if err != nil {
		t.Fatalf("Embedder(gemini) not registered: %v", err)
	}
	if _, err := reg.Generator("gemini"); err != nil {
		t.Fatalf("Generator(gemini) not registered: %v", err)
	}

	ready, code := Availability(ctx, emb)
	if ready {
		t.Fatal("Availability should report false without credentials")
	}
	if code != TestErrBadCredentials {
		t.Errorf("Availability code = %q, want %q", code, TestErrBadCredentials)
	}

	if _, err := emb.Embed(ctx, []string{"hello"}); err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("Embed without credentials error = %v, want GEMINI_API_KEY guidance", err)
	}
}

func TestGemini_ResolveGCPLocation(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	t.Setenv("CLOUD_ML_REGION", "")
	if got := ResolveGCPLocation(""); got != DefaultGeminiLocation {
		t.Errorf("default = %q, want %q", got, DefaultGeminiLocation)
	}
	t.Setenv("CLOUD_ML_REGION", "europe-west4")
	if got := ResolveGCPLocation(""); got != "europe-west4" {
		t.Errorf("CLOUD_ML_REGION = %q", got)
	}
	t.Setenv("GOOGLE_CLOUD_LOCATION", "asia-northeast1")
	if got := ResolveGCPLocation(""); got != "asia-northeast1" {
		t.Errorf("GOOGLE_CLOUD_LOCATION must outrank CLOUD_ML_REGION, got %q", got)
	}
	if got := ResolveGCPLocation("us-east5"); got != "us-east5" {
		t.Errorf("config must outrank the environment, got %q", got)
	}
	if got := ResolveGCPLocation("  "); got != "asia-northeast1" {
		t.Errorf("blank config should fall through, got %q", got)
	}
}

func clearGeminiEnv(t *testing.T) {
	t.Helper()
	t.Setenv("2NB_BEDROCK_SKIP_KEYCHAIN", "1")
	t.Setenv("2NB_SKIP_KEYCHAIN", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, k := range []string{"GOOGLE_API_KEY", "GEMINI_API_KEY", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "2NB_DOCTOR_PROBE_GEMINI_MODE"} {
		t.Setenv(k, "")
	}
}

// apikey mode judges GEMINI_API_KEY alone: a configured Vertex project and an
// ambient GOOGLE_CLOUD_PROJECT must not redirect it to Vertex AI.
func TestGemini_ProbeMode_APIKeyIgnoresVertex(t *testing.T) {
	clearGeminiEnv(t)
	t.Setenv("2NB_DOCTOR_PROBE_GEMINI_MODE", "apikey")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "ambient-proj")
	cfg := GeminiConfig{UseVertexAI: true, Project: "cfg-proj"}
	ctx := context.Background()

	if CheckGeminiCredentials(ctx, cfg) {
		t.Error("apikey mode with no GEMINI_API_KEY must not report credentials just because a Vertex project exists")
	}
	_, err := NewGeminiClient(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY is not set") {
		t.Errorf("apikey mode must demand the key, got %v", err)
	}

	t.Setenv("GEMINI_API_KEY", "typed-test-key")
	if !CheckGeminiCredentials(ctx, cfg) {
		t.Error("apikey mode with GEMINI_API_KEY set should report credentials")
	}
	client, err := NewGeminiClient(ctx, cfg)
	if err != nil {
		t.Fatalf("NewGeminiClient apikey: %v", err)
	}
	if got := client.ClientConfig().Backend; got != genai.BackendGeminiAPI {
		t.Errorf("backend = %v, want BackendGeminiAPI", got)
	}
	if got := client.ClientConfig().HTTPOptions.APIVersion; got != "v1beta" {
		t.Errorf("APIVersion = %q, want v1beta", got)
	}
	p := NewGeminiProviderWithClient(client, cfg, "", "", 0)
	if p.isVertexAI() {
		t.Error("apikey mode must not attribute requests to Vertex AI")
	}
}

func TestGemini_ClientConfig_APIVersionPinned(t *testing.T) {
	vcfg := geminiVertexClientConfig("proj-1", "us-central1")
	if vcfg.Backend != genai.BackendVertexAI {
		t.Errorf("Vertex backend = %v, want BackendVertexAI", vcfg.Backend)
	}
	if vcfg.HTTPOptions.APIVersion != "v1" {
		t.Errorf("Vertex APIVersion = %q, want v1", vcfg.HTTPOptions.APIVersion)
	}

	acfg := geminiAPIClientConfig("test-key")
	if acfg.Backend != genai.BackendGeminiAPI {
		t.Errorf("Gemini API backend = %v, want BackendGeminiAPI", acfg.Backend)
	}
	if acfg.HTTPOptions.APIVersion != "v1beta" {
		t.Errorf("Gemini API APIVersion = %q, want v1beta", acfg.HTTPOptions.APIVersion)
	}
}

// vertex mode forces Vertex AI even when no project is configured in the usual
// places, and reports the missing project instead of falling back to a key.
func TestGemini_ProbeMode_VertexForcedWithoutProject(t *testing.T) {
	clearGeminiEnv(t)
	t.Setenv("2NB_DOCTOR_PROBE_GEMINI_MODE", "vertex")
	t.Setenv("GEMINI_API_KEY", "typed-test-key")
	ctx := context.Background()

	if CheckGeminiCredentials(ctx, GeminiConfig{}) {
		t.Error("vertex mode without a project must not report credentials via GEMINI_API_KEY")
	}
	_, err := NewGeminiClient(ctx, GeminiConfig{})
	if err == nil || !strings.Contains(err.Error(), "no GCP project configured") {
		t.Errorf("vertex mode without a project: %v", err)
	}
	p := NewGeminiProviderWithClient(nil, GeminiConfig{}, "", "", 0)
	if !p.isVertexAI() {
		t.Error("vertex mode must attribute requests to Vertex AI")
	}
}

func TestGemini_ProbeMode_UnsetOrUnknownChangesNothing(t *testing.T) {
	clearGeminiEnv(t)
	t.Setenv("2NB_DOCTOR_PROBE_GEMINI_MODE", "bogus")
	if geminiProbeMode() != "" {
		t.Errorf("unknown mode should be ignored, got %q", geminiProbeMode())
	}
	if geminiUsesVertex(GeminiConfig{}) {
		t.Error("default config must not select Vertex")
	}
	if !geminiUsesVertex(GeminiConfig{Project: "p"}) {
		t.Error("a configured project still selects Vertex when no mode is pinned")
	}
}
