package ai

// The AWS Bedrock "mantle" invocation plane serves partner-hosted frontier
// models (openai.gpt-5.5, xai.grok-4.3) over the OpenAI Responses REST
// dialect at https://bedrock-mantle.<region>.api.aws/openai/v1/responses.
// It is a separate plane from classic Bedrock: auth is a Bedrock API key
// (bearer token) only — SigV4 does not work — each model is pinned to its
// own region (ModelInfo.Region), and the classic control plane
// (ListFoundationModels / GetFoundationModel) cannot see the models.
// Request/response shapes below were live-probed 2026-07-07 against the real
// endpoints; the parse/build helpers are pure functions over []byte so tests
// exercise them on captured fixtures without faking the endpoint.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

// mantleMaxResponseBytes bounds the response body read so a buggy or hostile
// endpoint cannot exhaust memory (the 90s timeout only weakly bounds a slow
// unbounded stream). A Responses payload is a few KB; 8 MB is generous.
const mantleMaxResponseBytes = 8 << 20

// bedrockMantleTimeout hard-bounds one mantle HTTP call. A live probe hung
// for over 120 seconds once, so the client carries its own timeout instead of
// trusting the caller's context alone; the resulting net timeout classifies
// as TestErrTimeout in ClassifyProbeError.
const bedrockMantleTimeout = 90 * time.Second

// mantleMinOutputTokens floors max_output_tokens. Mantle models reason by
// default (grok effort "low", gpt-5.5 "medium") and the reasoning tokens bill
// against max_output_tokens: a live 16-token call returned ONLY a reasoning
// item with status "incomplete" and no message text. 256 leaves room for the
// default reasoning plus a short answer, so even the 32-token models-test
// probe gets text back.
const mantleMinOutputTokens = 256

// errNoMantleTokenText is matched by ClassifyProbeError (bad_credentials), so
// keep the "need a Bedrock API key" phrase stable.
const errNoMantleTokenText = "mantle models need a Bedrock API key (bearer token): set " +
	bedrockBearerTokenEnv + " or 2nb config set-key bedrock"

// BedrockMantleGenerator implements GenerationProvider (and UsageGenerator)
// for models on the Bedrock mantle plane. It is stdlib net/http only — no AWS
// SDK client — because the plane speaks plain REST with bearer auth.
type BedrockMantleGenerator struct {
	client  *http.Client
	model   string
	baseURL string
	token   string
}

var (
	_ GenerationProvider = (*BedrockMantleGenerator)(nil)
	_ UsageGenerator     = (*BedrockMantleGenerator)(nil)
)

// NewBedrockMantleGenerator creates a mantle generation client. vaultRoot
// lets vault-scoped user-catalog entries pin Region/Endpoint; pass "" when no
// vault is open (builtin entries still resolve). It errors when no bearer
// token resolves, since the plane has no SigV4 fallback.
func NewBedrockMantleGenerator(cfg BedrockConfig, model, vaultRoot string) (*BedrockMantleGenerator, error) {
	token := resolveMantleBearerToken()
	if token == "" {
		return nil, errors.New(errNoMantleTokenText)
	}
	baseURL, err := mantleBaseURL(cfg, model, vaultRoot)
	if err != nil {
		return nil, err
	}
	return &BedrockMantleGenerator{
		client:  &http.Client{Timeout: bedrockMantleTimeout},
		model:   model,
		baseURL: baseURL,
		token:   token,
	}, nil
}

// resolveMantleBearerToken returns the Bedrock API key the mantle plane
// authenticates with: AWS_BEARER_TOKEN_BEDROCK, hydrated from the macOS
// Keychain first via the same ensureBedrockBearerToken plumbing the SDK path
// (loadBedrockAWSConfig) uses, so a `2nb config set-key bedrock` key works
// for mantle models too.
func resolveMantleBearerToken() string {
	if runtime.GOOS == "darwin" {
		ensureBedrockBearerToken(os.Getenv, os.Setenv, keychainGet)
	}
	return os.Getenv(bedrockBearerTokenEnv)
}

// mantleBaseURL resolves the plane's base URL for a model: a catalog Endpoint
// override wins, else the URL is derived from the model's pinned Region
// (falling back to the configured ai.bedrock.region, then us-east-1). A wrong
// region surfaces from the live endpoint as 404 "model does not exist".
//
// The resolved URL is validated before it can carry the bearer token: the
// Endpoint and Region fields come from the user catalog, and a vault-scoped
// .2ndbrain/models.yaml travels inside shared or downloaded vaults, so an
// unvalidated host would let a hostile entry exfiltrate the AWS Bedrock bearer
// token to an attacker. The host must be https and end in ".api.aws"; a
// region that is not a bare label (contains a slash, dot, or scheme) is
// likewise rejected rather than interpolated into the host.
func mantleBaseURL(cfg BedrockConfig, model, vaultRoot string) (string, error) {
	if ep := ResolveModelEndpoint("bedrock", model, vaultRoot); ep != "" {
		u := strings.TrimRight(ep, "/")
		if err := validateMantleHost(u); err != nil {
			return "", fmt.Errorf("model %s has an invalid mantle endpoint %q: %w", model, ep, err)
		}
		return u, nil
	}
	region := ResolveModelRegion("bedrock", model, vaultRoot)
	if region == "" {
		region = cfg.Region
	}
	if region == "" {
		region = "us-east-1"
	}
	if !isBareRegionLabel(region) {
		return "", fmt.Errorf("model %s has an invalid mantle region %q: expected a bare region label like us-east-2", model, region)
	}
	return fmt.Sprintf("https://bedrock-mantle.%s.api.aws", region), nil
}

// validateMantleHost rejects any URL that is not an https URL whose host ends
// in ".api.aws", so the bearer token is never sent to an arbitrary host.
func validateMantleHost(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("scheme must be https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host != "api.aws" && !strings.HasSuffix(host, ".api.aws") {
		return fmt.Errorf("host %q is not an *.api.aws AWS endpoint", host)
	}
	return nil
}

// isBareRegionLabel accepts only a plain AWS region token (letters, digits,
// hyphens) so it cannot smuggle a host, path, or scheme into the derived URL.
func isBareRegionLabel(region string) bool {
	if region == "" {
		return false
	}
	for _, r := range region {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}

func (g *BedrockMantleGenerator) Name() string { return "bedrock" }

// Available reports whether a bearer token resolves. The mantle plane has no
// free control-plane probe (classic ListFoundationModels cannot see it, and a
// real /responses call costs tokens), so this checks the credential
// precondition only; per-model entitlement still needs `2nb models test`.
func (g *BedrockMantleGenerator) Available(_ context.Context) bool { return g.token != "" }

// Generate satisfies GenerationProvider; it delegates to GenerateWithUsage
// and drops the token usage.
func (g *BedrockMantleGenerator) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
	text, _, err := g.GenerateWithUsage(ctx, prompt, opts)
	return text, err
}

// GenerateWithUsage runs one Responses call and returns the answer plus the
// provider-reported input/output token usage. Implements ai.UsageGenerator so
// `ask` records real usage, like the Converse generator.
func (g *BedrockMantleGenerator) GenerateWithUsage(ctx context.Context, prompt string, opts GenOpts) (string, GenUsage, error) {
	reqBody, err := buildMantleRequest(g.model, prompt, opts)
	if err != nil {
		return "", GenUsage{}, err
	}
	respBody, err := g.doMantleRequest(ctx, reqBody)
	if err != nil {
		return "", GenUsage{}, err
	}
	return parseMantleResponse(g.model, respBody)
}

func (g *BedrockMantleGenerator) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:       g.model,
		Name:     g.model,
		Provider: "bedrock",
		Type:     "generation",
		Local:    false,
	}}, nil
}

// ── Request/response shapes (live-probed 2026-07-07) ──────────────────────

// mantleResponsesRequest is the POST /openai/v1/responses body.
// "instructions" is the system prompt (verified honored by the live probe).
type mantleResponsesRequest struct {
	Model           string           `json:"model"`
	Input           string           `json:"input"`
	Instructions    string           `json:"instructions,omitempty"`
	MaxOutputTokens int              `json:"max_output_tokens"`
	Reasoning       *mantleReasoning `json:"reasoning,omitempty"`
}

// mantleReasoning tunes the reasoning stage ({"effort": "low"|"medium"|...}).
// 2nb never sets it — the per-model defaults (grok "low", gpt-5.5 "medium")
// are what mantleMinOutputTokens budgets for — but the field is modeled so a
// future GenOpts knob needs no request-shape change.
type mantleReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// mantleResponsesResponse is the Responses envelope. "output" is an ARRAY of
// typed items: a "reasoning" item may come first; the answer lives only in
// "message" items whose content parts are {"type":"output_text","text":...}.
type mantleResponsesResponse struct {
	Status            string                  `json:"status"` // "completed" | "incomplete" | ...
	IncompleteDetails *mantleIncompleteDetail `json:"incomplete_details"`
	Output            []mantleOutputItem      `json:"output"`
	Usage             *mantleUsage            `json:"usage"`
	Error             *mantleError            `json:"error"`
}

type mantleIncompleteDetail struct {
	Reason string `json:"reason"`
}

type mantleOutputItem struct {
	Type    string              `json:"type"` // "reasoning" | "message"
	Content []mantleContentPart `json:"content"`
}

type mantleContentPart struct {
	Type string `json:"type"` // "output_text" | ...
	Text string `json:"text"`
}

type mantleUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	TotalTokens int `json:"total_tokens"`
}

type mantleError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// buildMantleRequest marshals one Responses request. opts.Temperature is
// deliberately not sent: the live-probed request shape carries no sampler
// params, and reasoning models on this plane commonly reject them, so the
// model default applies. MaxTokens is floored at mantleMinOutputTokens
// because default-on reasoning bills against it.
func buildMantleRequest(model, prompt string, opts GenOpts) ([]byte, error) {
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 512
	}
	if maxTokens < mantleMinOutputTokens {
		maxTokens = mantleMinOutputTokens
	}
	req := mantleResponsesRequest{
		Model:           model,
		Input:           prompt,
		Instructions:    opts.SystemPrompt,
		MaxOutputTokens: maxTokens,
	}
	// Default-on reasoning bills against MaxOutputTokens and, at a low budget,
	// can consume it entirely and return a reasoning-only "incomplete" response
	// with no answer text. A smoke probe passes effort "none" so the answer is
	// never starved; real generation leaves this empty for the model default.
	if opts.ReasoningEffort != "" {
		req.Reasoning = &mantleReasoning{Effort: opts.ReasoningEffort}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal mantle request: %w", err)
	}
	return body, nil
}

// parseMantleResponse extracts the answer text and token usage from one
// Responses body. Text comes ONLY from "message" items' "output_text" parts;
// a response with no such text (reasoning-only, status "incomplete") is an
// error, never an empty success — reasoning is on by default and a too-small
// max_output_tokens yields exactly that shape.
func parseMantleResponse(model string, body []byte) (string, GenUsage, error) {
	var resp mantleResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", GenUsage{}, fmt.Errorf("unmarshal mantle response from %s: %w", model, err)
	}
	usage := GenUsage{}
	if resp.Usage != nil {
		usage.InputTokens = resp.Usage.InputTokens
		usage.OutputTokens = resp.Usage.OutputTokens
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", usage, fmt.Errorf("mantle %s: %s", model, resp.Error.Message)
	}
	var b strings.Builder
	for _, item := range resp.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" {
				b.WriteString(part.Text)
			}
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		detail := resp.Status
		if resp.IncompleteDetails != nil && resp.IncompleteDetails.Reason != "" {
			detail += " (" + resp.IncompleteDetails.Reason + ")"
		}
		if detail == "" {
			detail = "no message item"
		}
		return "", usage, fmt.Errorf("no output text from %s: %s; reasoning likely consumed the budget — raise max tokens", model, detail)
	}
	return text, usage, nil
}

// doMantleRequest POSTs to the responses endpoint, retrying HTTP 429 with
// exponential backoff (mirroring doOpenRouterRequest). Any other non-2xx
// becomes a *ProviderHTTPError so ClassifyProbeError routes 401/403/404
// without mantle-specific rules.
func (g *BedrockMantleGenerator) doMantleRequest(ctx context.Context, body []byte) ([]byte, error) {
	url := g.baseURL + "/openai/v1/responses"
	const maxRetries = 3
	for attempt := range maxRetries {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+g.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := g.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("bedrock mantle POST %s: %w", url, err)
		}
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, mantleMaxResponseBytes))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries-1 {
			delay := time.Duration(1<<attempt) * time.Second // 1s, 2s, 4s
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, mantleHTTPError(g.model, url, resp.StatusCode, respBody)
		}
		return respBody, nil
	}
	// Unreachable: every iteration either returns or continues, and the final
	// attempt never takes the retry branch. Kept to satisfy the compiler.
	return nil, fmt.Errorf("bedrock mantle %s: retry loop exited unexpectedly", url)
}

// mantleErrorCode extracts the mantle plane's machine-readable error code from
// a non-2xx body shaped `{"error":{"code":"...","message":"...","type":"..."}}`.
// Returns "" when the body is empty, not JSON, or carries no code — callers
// then fall back to HTTP-status classification.
func mantleErrorCode(body []byte) string {
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Error.Code
}

// mantleHTTPError wraps a non-2xx mantle response as a *ProviderHTTPError.
//
// A 401 on this plane means one of two very different things, and the HTTP
// status alone cannot tell them apart: a bad bearer token, or a valid token
// whose AWS account is not entitled to the model (the staged per-account
// rollout gate). The response body DOES disambiguate them via `error.code`
// (`invalid_api_key` vs `access_denied`), so we lift that code onto the error
// and let it drive classification. Without it, a gated-but-working account saw
// "bad credentials" and a remediation telling it to re-run `aws configure`.
//
// A 404 usually means the wrong region/plane for the model ("model does not
// exist").
func mantleHTTPError(model, url string, status int, body []byte) *ProviderHTTPError {
	text := strings.TrimSpace(string(body))
	code := mantleErrorCode(body)
	if status == http.StatusUnauthorized && classifyProviderErrorCode(code) == "" {
		// The code (if any) doesn't map to a definitive cause, so the 401 is
		// still ambiguous: keep the human-readable hint. Gating on the mapped
		// classification (not merely code != "") means an UNMAPPED code — e.g.
		// a future mantle 401 variant — keeps the hint too, instead of silently
		// dropping it and falling back to bad_credentials with no explanation.
		if text != "" {
			text += " "
		}
		text += "(mantle 401: valid token but model not entitled on this account, or bad token)"
	}
	if model != "" {
		if text != "" {
			text += " "
		}
		text += "[model " + model + "]"
	}
	return &ProviderHTTPError{Provider: "bedrock", URL: url, StatusCode: status, Body: text, Code: code}
}
