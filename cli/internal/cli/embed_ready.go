package cli

import (
	"fmt"

	"github.com/apresai/2ndbrain/internal/ai"
)

// embedderNotReadyError builds the error shown when an embedding provider is
// registered but not ready. It names the ACTUAL cause, because the alternative
// is what this replaced: a single "(check credentials)" for every failure,
// which tells someone whose network blipped for five seconds to go
// re-authenticate.
//
// It formats, it does not probe. The caller already asked ai.Availability and
// holds the code; asking again here would mean a second live round trip on an
// error path, and a second answer that can disagree with the first. An empty
// code (a provider that cannot explain itself) keeps the original wording.
//
// The prose is ai.RemediationFor, the same text `models test` and the macOS app
// show for that code, so a user meets one explanation of a given failure
// wherever they meet it. The code travels with it so a bug report can name the
// failure exactly.
func embedderNotReadyError(provider string, code ai.TestErrorCode) error {
	if code == "" {
		return fmt.Errorf("embedding provider %q is not ready (check credentials). Run `2nb ai setup`", provider)
	}
	return fmt.Errorf("embedding provider %q is not ready (%s). %s",
		provider, code, readinessRemediation(code, provider))
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
