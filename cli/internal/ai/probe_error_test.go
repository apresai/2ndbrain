package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go"
)

func TestClassifyProbeError(t *testing.T) {
	smithyErr := func(code string) error {
		return &smithy.GenericAPIError{Code: code, Message: "boom"}
	}
	tests := []struct {
		name     string
		provider string
		err      error
		want     TestErrorCode
	}{
		{"nil", "bedrock", nil, ""},
		{"incompatible", "bedrock", &IncompatibleModelError{Reason: "image model"}, TestErrIncompatible},
		{"incompatible wrapped", "bedrock", fmt.Errorf("preflight: %w", &IncompatibleModelError{Reason: "x"}), TestErrIncompatible},
		{"deadline", "bedrock", context.DeadlineExceeded, TestErrTimeout},
		{"deadline wrapped", "openrouter", fmt.Errorf("probe: %w", context.DeadlineExceeded), TestErrTimeout},

		// Bedrock smithy codes. AccessDeniedException is the staged frontier
		// rollout gate: the listing shows the model, the runtime 403s.
		{"access denied", "bedrock", smithyErr("AccessDeniedException"), TestErrAccessDenied},
		{"access denied wrapped", "bedrock", fmt.Errorf("invoke m: %w", smithyErr("AccessDeniedException")), TestErrAccessDenied},
		{"access denied bare", "bedrock", smithyErr("AccessDenied"), TestErrAccessDenied},
		{"not found", "bedrock", smithyErr("ResourceNotFoundException"), TestErrNotFound},
		{"throttled", "bedrock", smithyErr("ThrottlingException"), TestErrThrottled},
		{"too many requests", "bedrock", smithyErr("TooManyRequestsException"), TestErrThrottled},
		{"quota exceeded", "bedrock", smithyErr("ServiceQuotaExceededException"), TestErrThrottled},
		{"expired token", "bedrock", smithyErr("ExpiredTokenException"), TestErrBadCredentials},
		{"expired token bare", "bedrock", smithyErr("ExpiredToken"), TestErrBadCredentials},
		{"unrecognized client", "bedrock", smithyErr("UnrecognizedClientException"), TestErrBadCredentials},
		{"invalid signature", "bedrock", smithyErr("InvalidSignatureException"), TestErrBadCredentials},
		{"invalid client token", "bedrock", smithyErr("InvalidClientTokenId"), TestErrBadCredentials},
		{"incomplete signature", "bedrock", smithyErr("IncompleteSignature"), TestErrBadCredentials},
		{"unauthorized", "bedrock", smithyErr("UnauthorizedException"), TestErrBadCredentials},
		{"validation", "bedrock", smithyErr("ValidationException"), TestErrInvalidRequest},
		{"model timeout", "bedrock", smithyErr("ModelTimeoutException"), TestErrTimeout},
		{"service unavailable", "bedrock", smithyErr("ServiceUnavailableException"), TestErrProviderUnreachable},
		{"internal server", "bedrock", smithyErr("InternalServerException"), TestErrProviderUnreachable},
		{"model not ready", "bedrock", smithyErr("ModelNotReadyException"), TestErrProviderUnreachable},
		{"server fault unknown code", "bedrock", &smithy.GenericAPIError{Code: "SomethingNovelException", Message: "boom", Fault: smithy.FaultServer}, TestErrProviderUnreachable},
		{"unknown smithy code", "bedrock", smithyErr("SomethingNovelException"), TestErrUnknown},

		// HTTP providers.
		{"http 401", "openrouter", &ProviderHTTPError{Provider: "openrouter", StatusCode: 401}, TestErrBadCredentials},
		{"http 402", "openrouter", &ProviderHTTPError{Provider: "openrouter", StatusCode: 402}, TestErrAccessDenied},
		{"http 403", "openrouter", &ProviderHTTPError{Provider: "openrouter", StatusCode: 403}, TestErrAccessDenied},
		{"http 404", "ollama", &ProviderHTTPError{Provider: "ollama", StatusCode: 404}, TestErrNotFound},
		{"http 408", "openrouter", &ProviderHTTPError{Provider: "openrouter", StatusCode: 408}, TestErrTimeout},
		{"http 429", "openrouter", &ProviderHTTPError{Provider: "openrouter", StatusCode: 429}, TestErrThrottled},
		{"http 500", "ollama", &ProviderHTTPError{Provider: "ollama", StatusCode: 500}, TestErrProviderUnreachable},
		{"http 400", "openrouter", &ProviderHTTPError{Provider: "openrouter", StatusCode: 400}, TestErrInvalidRequest},
		{"http wrapped", "ollama", fmt.Errorf("embed: %w", &ProviderHTTPError{Provider: "ollama", StatusCode: 404}), TestErrNotFound},

		// Credential resolution before any API call.
		{"missing api key", "openrouter", errors.New("no API key for openrouter: set OPENROUTER_API_KEY or run `2nb config set-key openrouter`"), TestErrBadCredentials},
		{"api key not found", "openrouter", errors.New("api key not found in keychain"), TestErrBadCredentials},
		{"sso expired", "bedrock", errors.New("failed to refresh cached credentials, the SSO session has expired or is invalid"), TestErrBadCredentials},
		{"imds no role", "bedrock", errors.New("no EC2 IMDS role found, operation error ec2imds"), TestErrBadCredentials},
		{"retrieve credentials", "bedrock", errors.New("failed to retrieve credentials from provider chain"), TestErrBadCredentials},
		{"empty static creds", "bedrock", errors.New("static credentials are empty"), TestErrBadCredentials},

		// Network failures.
		{"net op error", "ollama", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, TestErrProviderUnreachable},
		{"net timeout", "ollama", &timeoutNetError{}, TestErrTimeout},
		{"stringly connection refused", "ollama", errors.New("ollama POST http://x/api/embed: dial tcp 127.0.0.1:9: connect: connection refused"), TestErrProviderUnreachable},
		{"stringly no such host", "ollama", errors.New("Get http://nope.invalid/api/tags: no such host"), TestErrProviderUnreachable},
		{"stringly connection reset", "openrouter", errors.New("read tcp: connection reset by peer"), TestErrProviderUnreachable},
		{"stringly deadline", "bedrock", errors.New("operation error: context deadline exceeded"), TestErrTimeout},

		{"opaque", "bedrock", errors.New("something inexplicable"), TestErrUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyProbeError(tt.provider, tt.err)
			if got != tt.want {
				t.Errorf("ClassifyProbeError(%q, %v) = %q, want %q", tt.provider, tt.err, got, tt.want)
			}
		})
	}
}

// timeoutNetError implements net.Error with Timeout()=true.
type timeoutNetError struct{}

func (*timeoutNetError) Error() string   { return "i/o timeout" }
func (*timeoutNetError) Timeout() bool   { return true }
func (*timeoutNetError) Temporary() bool { return false }

func TestProviderHTTPErrorPreservesMessageShape(t *testing.T) {
	err := &ProviderHTTPError{Provider: "ollama", URL: "http://localhost:11434/api/embed", StatusCode: 404, Body: `{"error":"model not found"}`}
	want := `ollama http://localhost:11434/api/embed: status 404: {"error":"model not found"}`
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestRemediationFor(t *testing.T) {
	// Every non-empty code except unknown should have provider-appropriate advice.
	if got := RemediationFor(TestErrAccessDenied, "bedrock"); !strings.Contains(got, "Model access") || !strings.Contains(got, "AWS Support") {
		t.Errorf("bedrock access_denied remediation missing console/support guidance: %q", got)
	}
	if got := RemediationFor(TestErrBadCredentials, "openrouter"); !strings.Contains(got, "OPENROUTER_API_KEY") {
		t.Errorf("openrouter bad_credentials remediation missing key guidance: %q", got)
	}
	if got := RemediationFor(TestErrProviderUnreachable, "ollama"); !strings.Contains(got, "ollama serve") {
		t.Errorf("ollama unreachable remediation missing serve hint: %q", got)
	}
	if got := RemediationFor(TestErrUnknown, "bedrock"); got != "" {
		t.Errorf("unknown code should have no remediation, got %q", got)
	}
}

// TestProbeClassification_OfflineOllama drives the real probe path against a
// dead endpoint: no mock, deterministic offline failure.
func TestProbeClassification_OfflineOllama(t *testing.T) {
	cfg := AIConfig{}
	cfg.Ollama.Endpoint = "http://127.0.0.1:9" // discard port, nothing listens
	result, err := TestProbeModel(context.Background(), cfg, "all-minilm", "ollama", "embedding")
	if err != nil {
		t.Fatalf("TestProbeModel returned hard error: %v", err)
	}
	if result.OK {
		t.Fatal("probe against dead endpoint unexpectedly passed")
	}
	if result.Code != TestErrProviderUnreachable && result.Code != TestErrTimeout {
		t.Errorf("code = %q (detail %q), want provider_unreachable or timeout", result.Code, result.Detail)
	}
	if result.Remediation == "" {
		t.Error("expected a remediation hint for an unreachable provider")
	}
}

// TestProbeClassification_StaticIncompatible exercises the bedrock static
// preflight, which fails before any AWS call: works with no credentials.
func TestProbeClassification_StaticIncompatible(t *testing.T) {
	result, err := TestProbeModel(context.Background(), AIConfig{}, "amazon.nova-canvas-v1:0", "bedrock", "generation")
	if err != nil {
		t.Fatalf("TestProbeModel returned hard error: %v", err)
	}
	if result.OK {
		t.Fatal("nova-canvas probe unexpectedly passed")
	}
	if result.Code != TestErrIncompatible {
		t.Errorf("code = %q (detail %q), want incompatible", result.Code, result.Detail)
	}
}

// TestProbeClassification_BedrockRealError is cred-gated: it probes a
// nonexistent-but-allowlisted model ID against the real Bedrock runtime and
// asserts the failure classifies to something more specific than unknown.
// This is the class of test that catches the staged-rollout 403 gate.
func TestProbeClassification_BedrockRealError(t *testing.T) {
	requireBedrock(t)

	cfg := AIConfig{}
	cfg.Bedrock = BedrockConfig{Profile: "default", Region: "us-east-1"}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := TestProbeModel(ctx, cfg, "anthropic.claude-nonexistent-v1:0", "bedrock", "generation")
	if err != nil {
		t.Fatalf("TestProbeModel returned hard error: %v", err)
	}
	if result.OK {
		t.Fatal("nonexistent model probe unexpectedly passed")
	}
	if result.Code == "" || result.Code == TestErrUnknown {
		t.Errorf("real bedrock failure was not classified: code=%q detail=%q", result.Code, result.Detail)
	}
}
