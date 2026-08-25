package cli

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// The point of the change: a readiness failure names its real cause. Blaming
// credentials for a five-second network timeout sends the user to fix the
// wrong thing, which is exactly the misdirection this replaced.
//
// These are pure formatting assertions over a TestErrorCode. No provider, real
// or stubbed, is involved: the caller probes and this only renders the answer.
func TestEmbedderNotReadyError_NamesTheRealCause(t *testing.T) {
	t.Run("timeout never says check credentials", func(t *testing.T) {
		msg := embedderNotReadyError("bedrock", ai.TestErrTimeout).Error()
		if strings.Contains(msg, "check credentials") {
			t.Errorf("a timeout must not blame credentials: %q", msg)
		}
		if !strings.Contains(msg, "timed out") || !strings.Contains(msg, string(ai.TestErrTimeout)) {
			t.Errorf("expected the timeout cause and its code: %q", msg)
		}
	})

	t.Run("unreachable never says check credentials", func(t *testing.T) {
		msg := embedderNotReadyError("bedrock", ai.TestErrProviderUnreachable).Error()
		if strings.Contains(msg, "check credentials") {
			t.Errorf("an unreachable provider must not blame credentials: %q", msg)
		}
	})

	t.Run("bad credentials still points at credentials", func(t *testing.T) {
		msg := embedderNotReadyError("bedrock", ai.TestErrBadCredentials).Error()
		if !strings.Contains(msg, "credentials") {
			t.Errorf("a credential rejection must still mention credentials: %q", msg)
		}
		// The remediation is ai.RemediationFor's, so the user reads the same
		// guidance here as in `models test` and the macOS app.
		if !strings.Contains(msg, ai.RemediationFor(ai.TestErrBadCredentials, "bedrock", "")) {
			t.Errorf("expected the shared remediation text: %q", msg)
		}
	})

	// The readiness probe is a control-plane listing, never a model invocation,
	// so an access_denied here is an IAM permission problem. The shared
	// remediation answers for `models test` (which does invoke a model) and would
	// send the user to the Bedrock console's Model access page, which cannot
	// grant an API permission: a dead end.
	t.Run("access denied blames the API permission, not model entitlement", func(t *testing.T) {
		msg := embedderNotReadyError("bedrock", ai.TestErrAccessDenied).Error()
		if !strings.Contains(msg, "bedrock:ListFoundationModels") {
			t.Errorf("expected the control-plane permission named: %q", msg)
		}
		if strings.Contains(msg, ai.RemediationFor(ai.TestErrAccessDenied, "bedrock", "")) {
			t.Errorf("the model-entitlement remediation must not be reused here: %q", msg)
		}
	})

	// ai.RemediationFor has no text for an unclassifiable failure, and an error
	// ending in a bare code helps nobody.
	t.Run("an unknown cause still gives the user a next step", func(t *testing.T) {
		msg := embedderNotReadyError("bedrock", ai.TestErrUnknown).Error()
		if !strings.Contains(msg, "2nb doctor") {
			t.Errorf("expected a fallback next step: %q", msg)
		}
		if strings.HasSuffix(strings.TrimSpace(msg), "(unknown).") {
			t.Errorf("message must not end at the bare code: %q", msg)
		}
	})

	t.Run("a provider that cannot report keeps the legacy message", func(t *testing.T) {
		msg := embedderNotReadyError("ollama", "").Error()
		if !strings.Contains(msg, "(check credentials)") {
			t.Errorf("expected the unchanged fallback wording: %q", msg)
		}
	})
}
