package cli

import (
	"context"
	"fmt"

	"github.com/apresai/2ndbrain/internal/ai"
)

// embedderNotReadyError builds the error shown when an embedding provider is
// registered but not ready. It names the ACTUAL cause when the provider can
// report one (ai.AvailabilityReporter), because the alternative is what this
// replaced: a single "(check credentials)" for every failure, which tells
// someone whose network blipped for five seconds to go re-authenticate.
//
// The remediation prose is ai.RemediationFor, the same text `models test` and
// the macOS app already show for that code, so a user sees one explanation of
// a given failure wherever they meet it.
//
// Providers that do not implement AvailabilityReporter keep the original
// message verbatim.
func embedderNotReadyError(ctx context.Context, provider string, embedder ai.EmbeddingProvider) error {
	reporter, ok := embedder.(ai.AvailabilityReporter)
	if !ok {
		return fmt.Errorf("embedding provider %q is not ready (check credentials) — run `2nb ai setup`", provider)
	}
	ready, code := reporter.AvailableDetail(ctx)
	if ready {
		// Raced back to healthy between the caller's check and this call.
		// Report it as not-ready anyway (the caller decided), but without
		// inventing a cause we no longer have.
		return fmt.Errorf("embedding provider %q is not ready — run `2nb ai setup`", provider)
	}
	if code == "" {
		return fmt.Errorf("embedding provider %q is not ready (check credentials) — run `2nb ai setup`", provider)
	}
	return fmt.Errorf("embedding provider %q is not ready: %s (%s). %s",
		provider, notReadySummary(code), code, ai.RemediationFor(code, provider, ""))
}

// notReadySummary is the short human clause before the machine-readable code.
func notReadySummary(code ai.TestErrorCode) string {
	switch code {
	case ai.TestErrTimeout:
		return "the readiness probe timed out"
	case ai.TestErrProviderUnreachable:
		return "the provider is unreachable"
	case ai.TestErrThrottled:
		return "the provider is throttling requests"
	case ai.TestErrBadCredentials:
		return "credentials were rejected"
	case ai.TestErrAccessDenied:
		return "this account is not entitled to the model"
	case ai.TestErrNotFound:
		return "the model was not found"
	case ai.TestErrIncompatible:
		return "the model is not usable for embeddings"
	case ai.TestErrInvalidRequest:
		return "the provider rejected the request"
	default:
		return "the readiness probe failed"
	}
}
