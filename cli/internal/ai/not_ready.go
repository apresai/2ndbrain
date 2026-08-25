package ai

import "fmt"

// This file renders a readiness verdict into words. It is the counterpart to
// probe_error.go, which classifies an error INTO a TestErrorCode; here a code
// becomes prose. The two live apart on purpose: classification is about what
// the provider did, rendering is about what to tell the user.
//
// It lives in this package rather than in internal/cli because internal/mcp and
// internal/retrieve report readiness failures too, and a user should meet one
// explanation of a given failure wherever they meet it. The division of labour:
// internal/cli owns the CARRIER (providerReadiness, a resolved verdict passed
// between surfaces), this package owns the WORDS.

// NotReadyMessage renders the one-line "why not ready, and what to do" shown
// when a registered provider fails its readiness probe. kind is the role it was
// being used for, "embedding" or "generation", so the message names the thing
// the user was trying to do.
//
// It formats, it does not probe: the caller already asked Availability and holds
// the code. It returns a string rather than an error because MCP tool handlers
// need the text, and the CLI wraps it in an error at the call site.
//
// The prose is ReadinessRemediation, which is RemediationFor with one deliberate
// override (see below). The code travels with it so a bug report can name the
// failure exactly.
func NotReadyMessage(kind, provider string, code TestErrorCode) string {
	if code == "" {
		return fmt.Sprintf("%s provider %q is not ready and did not report a cause. %s",
			kind, provider, UnclassifiedNextStep(provider))
	}
	return fmt.Sprintf("%s provider %q is not ready (%s). %s",
		kind, provider, code, ReadinessRemediation(code, provider))
}

// UnclassifiedNextStep is where to look when a provider failed its probe but
// cannot say why (ollama, openrouter, and llama-local answer a bare bool).
//
// It is phrased as a place to CHECK, never as a diagnosis, because no cause was
// observed. That distinction is the whole point, and it is also why this is not
// simply RemediationFor's text: that prose asserts a specific failure.
//
// The wording it replaced said "check credentials" for every provider. That was
// wrong for ollama and llama-local, which have none. Note it was NOT wrong for
// openrouter, whose probe sends a bearer token and can genuinely fail on a
// rejected key, so dropping the credential hint entirely would have lost real
// guidance for the one local-ish provider that has a credential.
func UnclassifiedNextStep(provider string) string {
	switch provider {
	case "ollama":
		return "Check that Ollama is running (`ollama serve`) and that ai.ollama.endpoint is correct."
	case "openrouter":
		return "Check OPENROUTER_API_KEY, then `2nb ai status`. Run `2nb --verbose` to see the underlying error."
	case "llama-local":
		return "Check the bundled engine with `2nb ai engine status`."
	}
	return "Run `2nb doctor` for a full check."
}

// ReadinessRemediation is RemediationFor with two adjustments for this path.
//
// access_denied means something different here. The readiness probe is a
// CONTROL-PLANE listing (ListFoundationModels), not a model invocation, so a
// denial is the IAM principal lacking that API permission. RemediationFor
// answers for `models test`, which does invoke a model, and so reads
// access_denied as model entitlement and sends the user to the Bedrock console's
// "Model access" page. That page cannot grant an API permission: a dead end.
//
// An unclassifiable failure (TestErrUnknown) has no remediation text at all, and
// an error ending in a bare code helps nobody.
//
// The divergence is deliberate and is guarded by a test in this package. Do not
// "dedupe" this into RemediationFor now that they are neighbours.
func ReadinessRemediation(code TestErrorCode, provider string) string {
	if code == TestErrAccessDenied && provider == "bedrock" {
		return "These credentials aren't allowed to query Bedrock in this region. Grant the `bedrock:ListFoundationModels` permission to the IAM principal (or use a Bedrock API key that has it), and check ai.bedrock.region. This is an API permission, not model access, so the console's Model access page won't change it."
	}
	if r := RemediationFor(code, provider, ""); r != "" {
		return r
	}
	return "The readiness probe failed without a recognizable cause. Run `2nb doctor` for a full check, and `2nb --verbose` to see the underlying error."
}

// NotReadySummary is the short human clause for surfaces where the
// machine-readable code alone would be terse, notably `ai status`'s
// per-provider `reason` field and `mcp doctor`'s detail line.
//
// It has no answer for an empty code on purpose: a caller that might hold one
// must choose its own wording rather than inherit a cause nobody observed.
func NotReadySummary(code TestErrorCode) string {
	switch code {
	case TestErrTimeout:
		return "the readiness probe timed out"
	case TestErrProviderUnreachable:
		return "the provider is unreachable"
	case TestErrThrottled:
		return "the provider is throttling requests"
	case TestErrBadCredentials:
		return "credentials were rejected"
	case TestErrAccessDenied:
		// Not "the model is gated": the probe never touched a model. See
		// ReadinessRemediation.
		return "these credentials may not query the provider"
	case TestErrNotFound:
		return "the model was not found"
	case TestErrIncompatible:
		// Role-agnostic on purpose: this renders for generators too.
		return "2nb cannot call this model"
	case TestErrInvalidRequest:
		return "the provider rejected the request"
	default:
		return "the readiness probe failed"
	}
}
