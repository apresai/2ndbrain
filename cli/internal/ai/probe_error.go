package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/aws/smithy-go"
)

// TestErrorCode classifies a failed model test probe into a small, stable
// vocabulary that CLIs and GUIs can act on. Empty means "no classification":
// the probe passed, or the model was never tested.
//
// Why this exists: provider errors arrive as free-text strings, and the most
// actionable failure (an AWS account that cannot invoke a Bedrock model) was
// indistinguishable from bad credentials or throttling. The hard-won rule this
// package encodes: Bedrock's control-plane APIs (ListFoundationModels,
// GetFoundationModelAvailability) can report a model as AUTHORIZED/AVAILABLE
// while bedrock-runtime still returns HTTP 403 "not available for this
// account". That is AWS's staged frontier-model rollout gate, and it means the
// ONLY trustworthy invokability check is a real invoke probe, which is exactly
// what produces the errors classified here. Do not "optimize" the probe away
// in favor of an availability API call.
type TestErrorCode string

const (
	// TestErrAccessDenied: the provider authenticated us but refused this
	// model (Bedrock AccessDeniedException, OpenRouter 402/403).
	TestErrAccessDenied TestErrorCode = "access_denied"
	// TestErrNotFound: the model ID does not exist for this account/region.
	TestErrNotFound TestErrorCode = "not_found"
	// TestErrThrottled: rate-limited; the model very likely works.
	TestErrThrottled TestErrorCode = "throttled"
	// TestErrBadCredentials: credentials missing, expired, or invalid.
	TestErrBadCredentials TestErrorCode = "bad_credentials"
	// TestErrProviderUnreachable: network/endpoint failure or provider outage.
	TestErrProviderUnreachable TestErrorCode = "provider_unreachable"
	// TestErrInvalidRequest: the provider rejected the request shape.
	TestErrInvalidRequest TestErrorCode = "invalid_request"
	// TestErrIncompatible: 2nb's own static preflight refused the model; it
	// was never invoked.
	TestErrIncompatible TestErrorCode = "incompatible"
	// TestErrTimeout: the probe deadline elapsed.
	TestErrTimeout TestErrorCode = "timeout"
	// TestErrUnknown: a failure the classifier has no rule for. The raw
	// detail string is the only guidance.
	TestErrUnknown TestErrorCode = "unknown"
)

// IncompatibleModelError marks a deterministic, local incompatibility (static
// allowlist or lifecycle block). The model was never invoked, so this must not
// be presented as an account or credential problem.
type IncompatibleModelError struct{ Reason string }

func (e *IncompatibleModelError) Error() string { return e.Reason }

// ProviderHTTPError is a typed non-2xx HTTP response from an HTTP-based AI
// provider (OpenRouter, Ollama). Error() preserves the exact message text the
// stringly-typed fmt.Errorf paths produced before this type existed, so
// callers that match on message content keep working.
type ProviderHTTPError struct {
	Provider   string
	URL        string
	StatusCode int
	Body       string
}

func (e *ProviderHTTPError) Error() string {
	return fmt.Sprintf("%s %s: status %d: %s", e.Provider, e.URL, e.StatusCode, e.Body)
}

// ClassifyProbeError folds a probe failure from any provider into a
// TestErrorCode. Returns "" for nil errors. The raw error text should be kept
// alongside the code (TestProbeResult.Detail / ModelInfo.TestError); the code
// is for routing UI guidance, not for replacing the detail.
func ClassifyProbeError(provider string, err error) TestErrorCode {
	if err == nil {
		return ""
	}

	var incompat *IncompatibleModelError
	if errors.As(err, &incompat) {
		return TestErrIncompatible
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return TestErrTimeout
	}

	var httpErr *ProviderHTTPError
	if errors.As(err, &httpErr) {
		return classifyHTTPStatus(httpErr.StatusCode)
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return classifySmithyError(apiErr)
	}

	// Credential-resolution failures happen before any API call and carry no
	// smithy code: a missing OpenRouter key (GetAPIKey), an unresolvable AWS
	// profile, an expired SSO session surfaced by the SDK's config loader, or
	// a mantle model with no Bedrock API key (errNoMantleTokenText).
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no api key"),
		strings.Contains(msg, "api key not found"),
		strings.Contains(msg, "need a bedrock api key"),
		strings.Contains(msg, "failed to refresh cached credentials"),
		strings.Contains(msg, "no ec2 imds role found"),
		strings.Contains(msg, "failed to retrieve credentials"),
		strings.Contains(msg, "sso session"),
		strings.Contains(msg, "static credentials are empty"):
		return TestErrBadCredentials
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return TestErrTimeout
		}
		return TestErrProviderUnreachable
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return TestErrProviderUnreachable
	}
	// url.Error and "connection refused" style failures that lost their typed
	// chain through fmt.Errorf("%s", ...) wrapping.
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "dial tcp"),
		strings.Contains(msg, "connection reset"):
		return TestErrProviderUnreachable
	case strings.Contains(msg, "context deadline exceeded"):
		return TestErrTimeout
	}

	return TestErrUnknown
}

func classifySmithyError(apiErr smithy.APIError) TestErrorCode {
	code := apiErr.ErrorCode()
	switch {
	case strings.EqualFold(code, "AccessDeniedException"),
		strings.EqualFold(code, "AccessDenied"):
		return TestErrAccessDenied
	case strings.EqualFold(code, "ResourceNotFoundException"):
		return TestErrNotFound
	case strings.EqualFold(code, "ThrottlingException"),
		strings.EqualFold(code, "TooManyRequestsException"),
		strings.EqualFold(code, "ServiceQuotaExceededException"):
		return TestErrThrottled
	case strings.EqualFold(code, "ExpiredTokenException"),
		strings.EqualFold(code, "ExpiredToken"),
		strings.EqualFold(code, "UnrecognizedClientException"),
		strings.EqualFold(code, "InvalidSignatureException"),
		strings.EqualFold(code, "InvalidClientTokenId"),
		strings.EqualFold(code, "IncompleteSignature"),
		strings.EqualFold(code, "UnauthorizedException"):
		return TestErrBadCredentials
	case strings.EqualFold(code, "ValidationException"):
		return TestErrInvalidRequest
	case strings.EqualFold(code, "ModelTimeoutException"):
		return TestErrTimeout
	case strings.EqualFold(code, "ServiceUnavailableException"),
		strings.EqualFold(code, "InternalServerException"),
		strings.EqualFold(code, "ModelNotReadyException"):
		return TestErrProviderUnreachable
	}
	if apiErr.ErrorFault() == smithy.FaultServer {
		return TestErrProviderUnreachable
	}
	return TestErrUnknown
}

func classifyHTTPStatus(status int) TestErrorCode {
	switch {
	case status == 401:
		return TestErrBadCredentials
	case status == 402, status == 403:
		return TestErrAccessDenied
	case status == 404:
		return TestErrNotFound
	case status == 408:
		return TestErrTimeout
	case status == 429:
		return TestErrThrottled
	case status >= 500:
		return TestErrProviderUnreachable
	case status >= 400:
		return TestErrInvalidRequest
	}
	return TestErrUnknown
}

// RemediationFor returns a one-paragraph, user-actionable fix hint for a
// classified probe failure. Empty for codes with no better advice than the
// raw error text.
func RemediationFor(code TestErrorCode, provider string) string {
	switch code {
	case TestErrAccessDenied:
		if provider == "bedrock" {
			return "Your AWS account can't invoke this model yet. AWS's staged rollout can gate newer frontier models even when the console shows access as granted. Request access under Bedrock > Model access in the AWS console; if it already shows granted and invocations still 403, only an AWS Support case unblocks it."
		}
		return "Your account doesn't have access to this model. Check your plan or entitlements with the provider."
	case TestErrBadCredentials:
		switch provider {
		case "bedrock":
			return "AWS credentials are missing, expired, or invalid. Refresh your SSO session or run `aws configure`, or store a Bedrock API key with `2nb config set-key bedrock`."
		case "openrouter":
			return "OpenRouter API key is missing or invalid. Set OPENROUTER_API_KEY or run `2nb config set-key openrouter`."
		}
		return "Credentials for this provider are missing or invalid."
	case TestErrThrottled:
		return "The request was rate-limited, so the model very likely works. Retry in a minute, or lower ai.embed_concurrency if this happened during indexing."
	case TestErrNotFound:
		if provider == "bedrock" {
			return "The model ID wasn't found in this region. Check ai.bedrock.region, or use the region-prefixed inference-profile ID (us., eu., global.)."
		}
		return "The model ID wasn't found. Check the spelling, or whether the provider still offers it."
	case TestErrProviderUnreachable:
		if provider == "ollama" {
			return "Ollama isn't reachable. Start it with `ollama serve`, or fix ai.ollama.endpoint."
		}
		return "The provider endpoint is unreachable. Check your network, or the provider's status page."
	case TestErrTimeout:
		return "The probe timed out. Retry; if it persists the model may be cold-starting or the provider degraded."
	case TestErrIncompatible:
		return "2nb doesn't support this model's invoke path, so it was not called. Pick a compatible model from `2nb models list`."
	case TestErrInvalidRequest:
		return "The provider rejected the request as invalid. The model may need a different invoke strategy; see the notes in `2nb models list`."
	}
	return ""
}
