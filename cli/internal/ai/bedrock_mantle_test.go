package ai

// Offline tests exercise the pure build/parse helpers on fixtures captured
// from the REAL mantle endpoints (live-probed 2026-07-07) — per the no-mocks
// policy the endpoint itself is never faked (no httptest servers). The one
// end-to-end test is cred-gated on AWS_BEARER_TOKEN_BEDROCK.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// mantleFixtureCompleted mirrors the live grok-4.3 probe response: a
// "reasoning" item first, then the "message" item carrying the answer in an
// output_text part, plus the usage block with reasoning-token detail.
const mantleFixtureCompleted = `{
  "id": "resp_fixture_completed",
  "object": "response",
  "status": "completed",
  "incomplete_details": null,
  "model": "xai.grok-4.3",
  "output": [
    {"id": "rs_1", "type": "reasoning", "summary": []},
    {"id": "msg_1", "type": "message", "status": "completed", "role": "assistant",
     "content": [{"type": "output_text", "annotations": [], "text": "4"}]}
  ],
  "usage": {"input_tokens": 21, "output_tokens": 75,
            "output_tokens_details": {"reasoning_tokens": 64}, "total_tokens": 96}
}`

// mantleFixtureReasoningOnly mirrors the live 16-max-token call: reasoning
// consumed the whole budget, status "incomplete", and NO message item.
const mantleFixtureReasoningOnly = `{
  "id": "resp_fixture_incomplete",
  "object": "response",
  "status": "incomplete",
  "incomplete_details": {"reason": "max_output_tokens"},
  "model": "openai.gpt-5.5",
  "output": [
    {"id": "rs_2", "type": "reasoning", "summary": []}
  ],
  "usage": {"input_tokens": 21, "output_tokens": 16,
            "output_tokens_details": {"reasoning_tokens": 16}, "total_tokens": 37}
}`

func TestBuildMantleRequest(t *testing.T) {
	body, err := buildMantleRequest("xai.grok-4.3", "What is 2+2?", GenOpts{
		MaxTokens:    32,
		SystemPrompt: "Be concise.",
		Temperature:  Ptr(0.1),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal built request: %v", err)
	}
	if req["model"] != "xai.grok-4.3" {
		t.Errorf("model = %v", req["model"])
	}
	if req["input"] != "What is 2+2?" {
		t.Errorf("input = %v", req["input"])
	}
	if req["instructions"] != "Be concise." {
		t.Errorf("instructions (system prompt) = %v", req["instructions"])
	}
	// Default-on reasoning bills against max_output_tokens, so small caps are
	// floored: a raw 32 would come back reasoning-only/incomplete.
	if got := req["max_output_tokens"].(float64); got != float64(mantleMinOutputTokens) {
		t.Errorf("max_output_tokens = %v, want floored %d", got, mantleMinOutputTokens)
	}
	// Reasoning stays at the model default; sampler params are never sent.
	if _, ok := req["reasoning"]; ok {
		t.Error("reasoning must not be set (model default effort applies)")
	}
	if _, ok := req["temperature"]; ok {
		t.Error("temperature must not be sent on the mantle plane")
	}
}

func TestBuildMantleRequest_ReasoningEffort(t *testing.T) {
	// A smoke probe sets effort "none" so reasoning does not starve the answer;
	// the field is sent only when non-empty (real generation omits it).
	body, err := buildMantleRequest("xai.grok-4.3", "hi", GenOpts{MaxTokens: 512, ReasoningEffort: "none"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var req struct {
		Reasoning *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "none" {
		t.Errorf("reasoning effort not sent: %+v", req.Reasoning)
	}

	// Empty ReasoningEffort omits the key entirely (model default reasoning).
	body, err = buildMantleRequest("xai.grok-4.3", "hi", GenOpts{MaxTokens: 512})
	if err != nil {
		t.Fatalf("build (empty): %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal (empty): %v", err)
	}
	if _, ok := raw["reasoning"]; ok {
		t.Error("reasoning key must be omitted when ReasoningEffort is empty")
	}
}

func TestBuildMantleRequest_MaxTokens(t *testing.T) {
	for _, tt := range []struct {
		in, want int
	}{
		{0, 512},                     // unset -> generator default
		{16, mantleMinOutputTokens},  // live-probed failure case, floored
		{mantleMinOutputTokens, 256}, // at the floor
		{4096, 4096},                 // large values pass through
	} {
		body, err := buildMantleRequest("openai.gpt-5.5", "hi", GenOpts{MaxTokens: tt.in})
		if err != nil {
			t.Fatalf("build(%d): %v", tt.in, err)
		}
		var req struct {
			MaxOutputTokens int `json:"max_output_tokens"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if req.MaxOutputTokens != tt.want {
			t.Errorf("MaxTokens %d -> max_output_tokens %d, want %d", tt.in, req.MaxOutputTokens, tt.want)
		}
	}
}

func TestBuildMantleRequest_NoInstructionsKeyWhenEmpty(t *testing.T) {
	body, err := buildMantleRequest("xai.grok-4.3", "hi", GenOpts{MaxTokens: 512})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(string(body), `"instructions"`) {
		t.Errorf("empty system prompt must omit instructions, got %s", body)
	}
}

func TestParseMantleResponse_MessageAfterReasoning(t *testing.T) {
	text, usage, err := parseMantleResponse("xai.grok-4.3", []byte(mantleFixtureCompleted))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if text != "4" {
		t.Errorf("text = %q, want %q (only message output_text, never reasoning)", text, "4")
	}
	if usage.InputTokens != 21 || usage.OutputTokens != 75 {
		t.Errorf("usage = %+v, want in=21 out=75", usage)
	}
}

func TestParseMantleResponse_ReasoningOnlyIncompleteIsError(t *testing.T) {
	_, usage, err := parseMantleResponse("openai.gpt-5.5", []byte(mantleFixtureReasoningOnly))
	if err == nil {
		t.Fatal("reasoning-only incomplete response must be an error, never an empty success")
	}
	for _, want := range []string{"no output text", "raise max tokens", "incomplete", "max_output_tokens"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
	// Usage is still extracted so the failed call's cost is observable.
	if usage.InputTokens != 21 || usage.OutputTokens != 16 {
		t.Errorf("usage = %+v, want in=21 out=16", usage)
	}
}

func TestParseMantleResponse_ErrorObject(t *testing.T) {
	body := `{"status": "failed", "output": [], "error": {"message": "boom", "type": "server_error"}}`
	_, _, err := parseMantleResponse("xai.grok-4.3", []byte(body))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error-object response should surface the message, got %v", err)
	}
}

func TestParseMantleResponse_Malformed(t *testing.T) {
	if _, _, err := parseMantleResponse("xai.grok-4.3", []byte("<html>gateway error</html>")); err == nil {
		t.Error("malformed body should error")
	}
}

// TestMantleErrorClassification fabricates the *ProviderHTTPError values
// doMantleRequest produces for each live-probed status and asserts the
// existing classifier routes them with zero mantle-specific rules.
func TestMantleErrorClassification(t *testing.T) {
	url := "https://bedrock-mantle.us-west-2.api.aws/openai/v1/responses"
	for _, tt := range []struct {
		status int
		want   TestErrorCode
	}{
		// A body with no `error.code` falls back to HTTP-status classification.
		{401, TestErrBadCredentials},
		{403, TestErrAccessDenied},
		{404, TestErrNotFound}, // wrong region/plane: "model does not exist"
		{429, TestErrThrottled},
		{500, TestErrProviderUnreachable},
	} {
		err := error(mantleHTTPError("xai.grok-4.3", url, tt.status, []byte(`{"error":{"message":"x"}}`)))
		if got := ClassifyProbeError("bedrock", err); got != tt.want {
			t.Errorf("status %d classified %q, want %q", tt.status, got, tt.want)
		}
	}

	// The 401 ambiguity hint must ride in the error text when the body carries
	// no code to disambiguate with.
	err401 := mantleHTTPError("xai.grok-4.3", url, 401, []byte("unauthorized"))
	if !strings.Contains(err401.Error(), "not entitled on this account") {
		t.Errorf("401 error missing entitlement hint: %v", err401)
	}
	if !strings.Contains(err401.Error(), "xai.grok-4.3") {
		t.Errorf("error should name the model for debuggability: %v", err401)
	}

	// A missing bearer token classifies as bad credentials before any call.
	if got := ClassifyProbeError("bedrock", errors.New(errNoMantleTokenText)); got != TestErrBadCredentials {
		t.Errorf("missing-token error classified %q, want bad_credentials", got)
	}
}

// TestMantleMappedCodeWinsOverNon401Status pins, deliberately, that a mapped
// error.code overrides the HTTP status even on a non-401 response. The mantle
// plane attaches error.code to every non-2xx (live-observed 400
// integer_below_min_value / missing_required_parameter, 404 not_found_error),
// and ClassifyProbeError consults Code before StatusCode unconditionally. Those
// live codes are currently unmapped so they fall back to status; this asserts
// the intended behavior for the day a mapped code (access_denied /
// invalid_api_key) arrives on a non-401 — the definitive cause wins over the
// coarser status.
func TestMantleMappedCodeWinsOverNon401Status(t *testing.T) {
	url := "https://bedrock-mantle.us-west-2.api.aws/openai/v1/responses"
	for _, tt := range []struct {
		name       string
		status     int
		body       string
		want       TestErrorCode
		wantStatus TestErrorCode // what pure status classification would have said
	}{
		{"429 carrying access_denied", 429, `{"error":{"code":"access_denied"}}`, TestErrAccessDenied, TestErrThrottled},
		{"403 carrying invalid_api_key", 403, `{"error":{"code":"invalid_api_key"}}`, TestErrBadCredentials, TestErrAccessDenied},
		{"500 carrying an unmapped code falls back to status", 500, `{"error":{"code":"internal_server_error"}}`, TestErrProviderUnreachable, TestErrProviderUnreachable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := mantleHTTPError("xai.grok-4.3", url, tt.status, []byte(tt.body))
			if got := ClassifyProbeError("bedrock", err); got != tt.want {
				t.Errorf("classified %q, want %q (pure-status would be %q)", got, tt.want, tt.wantStatus)
			}
			if err.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d (must not be rewritten)", err.StatusCode, tt.status)
			}
		})
	}
}

// TestMantle401DisambiguatedByErrorCode pins the behavior that a mantle 401 is
// classified from the body's `error.code`, not the status. Both bodies below
// were captured verbatim from the live plane on 2026-07-08: an invalid bearer
// token against an entitled model (us-west-2 / xai.grok-4.3), and a valid
// bearer token against a model the account is not entitled to (us-east-2 /
// openai.gpt-5.5). Both are HTTP 401 with type "permission_denied_error".
//
// Regression guard: classifying the second as bad_credentials told a user with
// working credentials to re-run `aws configure`.
func TestMantle401DisambiguatedByErrorCode(t *testing.T) {
	const (
		badTokenBody = `{"error":{"code":"invalid_api_key","message":"Invalid bearer token","param":null,"type":"permission_denied_error"}}`
		notEntitled  = `{"error":{"code":"access_denied","message":"openai.gpt-5.5 is not available for this account. You can explore other available models on Amazon Bedrock. For additional access options, contact AWS Sales at https://aws.amazon.com/contact-us/sales-support/","param":null,"type":"permission_denied_error"}}`
	)
	for _, tt := range []struct {
		name  string
		model string
		url   string
		body  string
		want  TestErrorCode
	}{
		{"bad token", "xai.grok-4.3", "https://bedrock-mantle.us-west-2.api.aws/openai/v1/responses", badTokenBody, TestErrBadCredentials},
		{"valid token, model not entitled", "openai.gpt-5.5", "https://bedrock-mantle.us-east-2.api.aws/openai/v1/responses", notEntitled, TestErrAccessDenied},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := mantleHTTPError(tt.model, tt.url, 401, []byte(tt.body))
			if got := ClassifyProbeError("bedrock", err); got != tt.want {
				t.Errorf("classified %q, want %q", got, tt.want)
			}
			// The status is reported honestly; only classification is lifted.
			if err.StatusCode != 401 {
				t.Errorf("StatusCode = %d, want 401 (must not be rewritten)", err.StatusCode)
			}
			// With a code present, the speculative "or bad token" hint is
			// suppressed: we now know which one it is.
			if strings.Contains(err.Error(), "not entitled on this account, or bad token") {
				t.Errorf("ambiguity hint should be dropped once error.code disambiguates: %v", err)
			}
			// The provider's own message must survive for the detail pane.
			if !strings.Contains(err.Error(), tt.model) {
				t.Errorf("error should name the model: %v", err)
			}
		})
	}
}

func TestMantleErrorCodeExtraction(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{"access denied", `{"error":{"code":"access_denied"}}`, "access_denied"},
		{"invalid key", `{"error":{"code":"invalid_api_key"}}`, "invalid_api_key"},
		{"no code field", `{"error":{"message":"x"}}`, ""},
		{"not json", `unauthorized`, ""},
		{"empty", ``, ""},
		{"html error page", `<html><body>502</body></html>`, ""},
		{"error is a string not an object", `{"error":"boom"}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := mantleErrorCode([]byte(tt.body)); got != tt.want {
				t.Errorf("mantleErrorCode(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// An unknown provider code must not swallow the status-based classification.
func TestUnknownProviderErrorCodeFallsBackToStatus(t *testing.T) {
	err := &ProviderHTTPError{Provider: "bedrock", StatusCode: 429, Code: "some_novel_code"}
	if got := ClassifyProbeError("bedrock", err); got != TestErrThrottled {
		t.Errorf("unknown code classified %q, want throttled (status fallback)", got)
	}
}

func TestMantleBaseURL(t *testing.T) {
	setupHome(t)

	mustURL := func(model string, cfg BedrockConfig) string {
		t.Helper()
		got, err := mantleBaseURL(cfg, model, "")
		if err != nil {
			t.Fatalf("mantleBaseURL(%s): %v", model, err)
		}
		return got
	}

	// Builtin region pins.
	if got := mustURL("openai.gpt-5.5", BedrockConfig{Region: "us-east-1"}); got != "https://bedrock-mantle.us-east-2.api.aws" {
		t.Errorf("gpt-5.5 base URL = %q", got)
	}
	if got := mustURL("xai.grok-4.3", BedrockConfig{Region: "us-east-1"}); got != "https://bedrock-mantle.us-west-2.api.aws" {
		t.Errorf("grok-4.3 base URL = %q", got)
	}

	// No catalog pin: configured region, then us-east-1.
	if got := mustURL("acme.unpinned", BedrockConfig{Region: "eu-west-1"}); got != "https://bedrock-mantle.eu-west-1.api.aws" {
		t.Errorf("unpinned base URL = %q", got)
	}
	if got := mustURL("acme.unpinned", BedrockConfig{}); got != "https://bedrock-mantle.us-east-1.api.aws" {
		t.Errorf("default base URL = %q", got)
	}

	// A catalog Endpoint override wins over Region derivation, but must still
	// be an https *.api.aws host.
	entry := ModelInfo{
		ID:             "acme.endpoint-pinned",
		Provider:       "bedrock",
		Type:           "generation",
		InvokeStrategy: StrategyBedrockMantleResponses,
		Region:         "us-east-2",
		Endpoint:       "https://bedrock-mantle-custom.api.aws/",
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := mustURL("acme.endpoint-pinned", BedrockConfig{}); got != "https://bedrock-mantle-custom.api.aws" {
		t.Errorf("endpoint override = %q", got)
	}
}

// TestMantleBaseURL_RejectsHostileEndpoint is the token-exfiltration guard: a
// vault-scoped models.yaml travels inside shared vaults, so a poisoned
// Endpoint or Region must never send the AWS bearer token to a non-AWS host.
func TestMantleBaseURL_RejectsHostileEndpoint(t *testing.T) {
	setupHome(t)

	cases := []struct {
		name     string
		endpoint string
		region   string
	}{
		{"http endpoint", "http://attacker.tld", ""},
		{"https non-aws endpoint", "https://attacker.tld", ""},
		{"aws-lookalike suffix", "https://api.aws.attacker.tld", ""},
		{"region smuggles host", "", "attacker.tld/x"},
		{"region smuggles scheme", "", "https://attacker.tld"},
		{"region with dot", "", "us-east-2.attacker.tld"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "acme.hostile-" + strings.ReplaceAll(tc.name, " ", "-")
			entry := ModelInfo{
				ID:             id,
				Provider:       "bedrock",
				Type:           "generation",
				InvokeStrategy: StrategyBedrockMantleResponses,
				Region:         tc.region,
				Endpoint:       tc.endpoint,
			}
			if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
				t.Fatalf("save: %v", err)
			}
			cfg := BedrockConfig{}
			if tc.region != "" {
				// Ensure the derivation path (not the config fallback) is exercised.
				cfg.Region = ""
			}
			if got, err := mantleBaseURL(cfg, id, ""); err == nil {
				t.Errorf("hostile input (%q/%q) must be rejected, got URL %q", tc.endpoint, tc.region, got)
			}
		})
	}
}

// TestNewBedrockGeneration_DispatchesMantle pins the exhaustive dispatch:
// a mantle-strategy model constructs the Responses client, and the classic
// Converse constructor refuses it outright, so it can never fall through to
// converseWithRetry. No network: both paths return before any call.
func TestNewBedrockGeneration_DispatchesMantle(t *testing.T) {
	setupHome(t)
	t.Setenv(bedrockBearerTokenEnv, "test-token")
	ctx := context.Background()
	cfg := BedrockConfig{Region: "us-east-1"}

	g, err := NewBedrockGeneration(ctx, cfg, "xai.grok-4.3", "")
	if err != nil {
		t.Fatalf("construct mantle generator: %v", err)
	}
	mg, ok := g.(*BedrockMantleGenerator)
	if !ok {
		t.Fatalf("grok-4.3 dispatched to %T, want *BedrockMantleGenerator", g)
	}
	if !mg.Available(ctx) {
		t.Error("mantle generator with a token should report available")
	}
	if _, isUsage := g.(UsageGenerator); !isUsage {
		t.Error("mantle generator must implement UsageGenerator")
	}

	if _, err := NewBedrockGenerator(ctx, cfg, "openai.gpt-5.5"); err == nil {
		t.Error("classic Converse constructor must refuse a mantle-strategy model")
	}

	// A vault-scoped mantle entry dispatches too (the #178 carryover fix).
	vaultRoot := t.TempDir()
	entry := ModelInfo{
		ID:             "acme.vault-mantle",
		Provider:       "bedrock",
		Type:           "generation",
		InvokeStrategy: StrategyBedrockMantleResponses,
		Region:         "us-east-2",
	}
	if err := SaveUserCatalogEntry(ScopeVault, vaultRoot, entry); err != nil {
		t.Fatalf("save vault entry: %v", err)
	}
	vg, err := NewBedrockGeneration(ctx, cfg, "acme.vault-mantle", vaultRoot)
	if err != nil {
		t.Fatalf("construct vault-scoped mantle generator: %v", err)
	}
	if _, ok := vg.(*BedrockMantleGenerator); !ok {
		t.Errorf("vault-scoped mantle entry dispatched to %T, want *BedrockMantleGenerator", vg)
	}
}

// TestLiveMantleGrokProbe is the cred-gated end-to-end test: the same path
// `2nb models test xai.grok-4.3 --provider bedrock` takes, against the real
// us-west-2 mantle endpoint. Requires a Bedrock API key. Non-defect outcomes
// skip rather than fail: 401 (account not entitled to grok), throttling, and
// a persistent hang (the plane demonstrably stalls occasionally — one live
// probe hung >120s — which the 90s client timeout mitigates in production;
// the probe's own 30s deadline can still eat one stall, so timeout retries
// once).
func TestLiveMantleGrokProbe(t *testing.T) {
	if os.Getenv(bedrockBearerTokenEnv) == "" {
		t.Skipf("set %s to run the live mantle probe", bedrockBearerTokenEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var result *TestProbeResult
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		result, err = TestProbeModel(ctx, AIConfig{}, "xai.grok-4.3", "bedrock", "generation", "")
		if err != nil {
			t.Fatalf("TestProbeModel returned hard error: %v", err)
		}
		if result.OK || result.Code != TestErrTimeout {
			break
		}
		t.Logf("attempt %d timed out (known transient mantle stall): %s", attempt, result.Detail)
	}
	if !result.OK {
		switch {
		// An unentitled account now reports access_denied (from the body's
		// error.code); a genuinely bad token still reports bad_credentials.
		// Neither is a client defect, so both skip.
		case result.Code == TestErrAccessDenied:
			t.Skipf("account not entitled to xai.grok-4.3: %s", result.Detail)
		case result.Code == TestErrBadCredentials && strings.Contains(result.Detail, "status 401"):
			t.Skipf("bedrock bearer token rejected by the mantle plane: %s", result.Detail)
		case result.Code == TestErrThrottled:
			t.Skipf("throttled (model very likely works): %s", result.Detail)
		case result.Code == TestErrProviderUnreachable:
			t.Skipf("mantle plane returned a transient 5xx (not a client defect): %s", result.Detail)
		case result.Code == TestErrTimeout:
			t.Skipf("mantle plane stalled twice (transient, not a client defect): %s", result.Detail)
		default:
			t.Fatalf("live grok probe failed: code=%s detail=%s", result.Code, result.Detail)
		}
	}
	if result.Detail == "" {
		t.Error("passing probe should carry the response snippet")
	}
	t.Logf("live grok-4.3 probe: OK in %s (detail %q)", result.Latency, result.Detail)
}
