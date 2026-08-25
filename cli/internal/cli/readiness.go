package cli

import (
	"context"
	"fmt"

	"github.com/apresai/2ndbrain/internal/ai"
)

// providerReadiness is a RESOLVED availability verdict: whether a provider is
// ready and, when it is not and the provider can say, the classified reason.
//
// It exists to be carried. A readiness answer used to be re-derived at each
// place that reports it, so a single `2nb ai status` probed the same embedder
// three times: once for the JSON field, once inside derivePortability, once
// inside bedrockProviderStatus. Definitive failures are cached so the repeats
// were free, but a TRANSIENT failure is deliberately short-lived in the cache,
// which made the repeats real 5-second probes on exactly the degraded network
// where a status command most needs to answer quickly.
type providerReadiness struct {
	ready bool
	code  ai.TestErrorCode
}

// resolveReadiness probes once. A nil provider is not ready and has nothing to
// say about why, which callers should handle before asking (there is a better
// message for "not registered" than anything a probe can produce).
func resolveReadiness(ctx context.Context, p any) providerReadiness {
	ready, code := ai.Availability(ctx, p)
	return providerReadiness{ready: ready, code: code}
}

// hint renders the one-line "why not ready, and what to do" clause for a
// status surface. Empty when the provider is ready.
func (r providerReadiness) hint(provider string) string {
	if r.ready {
		return ""
	}
	if r.code == "" {
		// A provider that cannot classify itself. Keep the old wording rather
		// than inventing a cause we do not have.
		return fmt.Sprintf("Provider %q is unreachable. If using Ollama, start the daemon; if using Bedrock, check AWS credentials.", provider)
	}
	return fmt.Sprintf("Provider %q is not ready (%s). %s", provider, r.code, readinessRemediation(r.code, provider))
}

// shortReason is hint's compact form for the per-provider `reason` field, which
// sits next to the provider's own name in the UI and so does not repeat it.
func (r providerReadiness) shortReason() string {
	if r.ready {
		return ""
	}
	if r.code == "" {
		return "credentials missing or region unreachable"
	}
	return fmt.Sprintf("%s (%s)", notReadySummary(r.code), r.code)
}

// providerNotReadyError builds the error shown when a provider is registered
// but not ready. It names the ACTUAL cause, because the alternative is what
// this replaced: one "(check credentials)" for every failure, which tells
// someone whose network blipped for five seconds to go re-authenticate.
//
// It formats, it does not probe. The caller already asked ai.Availability and
// holds the code; asking again here would mean a second live round trip on an
// error path, and a second answer that can disagree with the first. An empty
// code (a provider that cannot explain itself) keeps the original wording.
//
// kind is the role the provider was being used for, "embedding" or
// "generation", so the message names the thing the user was trying to do.
//
// The prose is ai.RemediationFor, the same text `models test` and the macOS app
// show for that code, so a user meets one explanation of a given failure
// wherever they meet it. The code travels with it so a bug report can name the
// failure exactly.
func providerNotReadyError(kind, provider string, code ai.TestErrorCode) error {
	if code == "" {
		return fmt.Errorf("%s provider %q is not ready (check credentials). Run `2nb ai setup`", kind, provider)
	}
	return fmt.Errorf("%s provider %q is not ready (%s). %s",
		kind, provider, code, readinessRemediation(code, provider))
}

// embedderNotReadyError is providerNotReadyError for the embedding role.
func embedderNotReadyError(provider string, code ai.TestErrorCode) error {
	return providerNotReadyError("embedding", provider, code)
}

// generatorNotReadyError is providerNotReadyError for the generation role.
func generatorNotReadyError(provider string, code ai.TestErrorCode) error {
	return providerNotReadyError("generation", provider, code)
}

// readinessRemediation is ai.RemediationFor with two adjustments for this path.
//
// access_denied means something different here. The readiness probe is a
// CONTROL-PLANE listing (ListFoundationModels), not a model invocation, so a
// denial is the IAM principal lacking that API permission. ai.RemediationFor
// answers for `models test`, which does invoke a model, and so reads
// access_denied as model entitlement and sends the user to the Bedrock console's
// "Model access" page. That page cannot grant an API permission: a dead end.
//
// An unclassifiable failure (TestErrUnknown) has no remediation text at all, and
// an error ending in a bare code helps nobody.
func readinessRemediation(code ai.TestErrorCode, provider string) string {
	if code == ai.TestErrAccessDenied && provider == "bedrock" {
		return "These credentials aren't allowed to query Bedrock in this region. Grant the `bedrock:ListFoundationModels` permission to the IAM principal (or use a Bedrock API key that has it), and check ai.bedrock.region. This is an API permission, not model access, so the console's Model access page won't change it."
	}
	if r := ai.RemediationFor(code, provider, ""); r != "" {
		return r
	}
	return "The readiness probe failed without a recognizable cause. Run `2nb doctor` for a full check, and `2nb --verbose` to see the underlying error."
}

// notReadySummary is the short human clause used where the machine-readable
// code alone would be terse, notably the per-provider `reason` field.
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
		// Not "the model is gated": the probe never touched a model. See
		// readinessRemediation.
		return "these credentials may not query the provider"
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
