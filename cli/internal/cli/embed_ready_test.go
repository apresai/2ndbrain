package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// stubEmbedder reports a fixed availability verdict. It implements
// ai.AvailabilityReporter only when reports is true, so the fallback path can
// be exercised too.
type stubEmbedder struct {
	code    ai.TestErrorCode
	reports bool
}

func (s stubEmbedder) Name() string                   { return "stub" }
func (s stubEmbedder) Dimensions() int                { return 8 }
func (s stubEmbedder) Available(context.Context) bool { return false }
func (s stubEmbedder) Embed(context.Context, []string, ...ai.EmbedOption) ([][]float32, error) {
	return nil, nil
}
func (s stubEmbedder) ListModels(context.Context) ([]ai.ModelInfo, error) { return nil, nil }

type stubReportingEmbedder struct{ stubEmbedder }

func (s stubReportingEmbedder) AvailableDetail(context.Context) (bool, ai.TestErrorCode) {
	return false, s.code
}

// The point of the change: a readiness failure names its real cause. Blaming
// credentials for a five-second network timeout sends the user to fix the
// wrong thing, which is exactly the misdirection this replaced.
func TestEmbedderNotReadyError_NamesTheRealCause(t *testing.T) {
	ctx := context.Background()

	t.Run("timeout never says check credentials", func(t *testing.T) {
		err := embedderNotReadyError(ctx, "bedrock", stubReportingEmbedder{stubEmbedder{code: ai.TestErrTimeout}})
		msg := err.Error()
		if strings.Contains(msg, "check credentials") {
			t.Errorf("a timeout must not blame credentials: %q", msg)
		}
		if !strings.Contains(msg, "timed out") || !strings.Contains(msg, string(ai.TestErrTimeout)) {
			t.Errorf("expected the timeout cause and its code: %q", msg)
		}
	})

	t.Run("unreachable never says check credentials", func(t *testing.T) {
		err := embedderNotReadyError(ctx, "bedrock", stubReportingEmbedder{stubEmbedder{code: ai.TestErrProviderUnreachable}})
		if strings.Contains(err.Error(), "check credentials") {
			t.Errorf("an unreachable provider must not blame credentials: %q", err.Error())
		}
	})

	t.Run("bad credentials still points at credentials", func(t *testing.T) {
		err := embedderNotReadyError(ctx, "bedrock", stubReportingEmbedder{stubEmbedder{code: ai.TestErrBadCredentials}})
		msg := err.Error()
		if !strings.Contains(msg, "credentials") {
			t.Errorf("a credential rejection must still mention credentials: %q", msg)
		}
		// The remediation is ai.RemediationFor's, so the user reads the same
		// guidance here as in `models test` and the macOS app.
		if !strings.Contains(msg, ai.RemediationFor(ai.TestErrBadCredentials, "bedrock", "")) {
			t.Errorf("expected the shared remediation text: %q", msg)
		}
	})

	t.Run("a provider that cannot report keeps the legacy message", func(t *testing.T) {
		err := embedderNotReadyError(ctx, "ollama", stubEmbedder{})
		if !strings.Contains(err.Error(), "(check credentials)") {
			t.Errorf("expected the unchanged fallback wording: %q", err.Error())
		}
	})
}
