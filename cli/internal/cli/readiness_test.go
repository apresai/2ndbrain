package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// The point of the change: a readiness failure names its real cause. Blaming
// credentials for a five-second network timeout sends the user to fix the
// wrong thing, which is exactly the misdirection this replaced.
//
// These are pure formatting assertions over a TestErrorCode. No provider, real
// or stubbed, is involved: the caller probes and this only renders the answer.
func TestProviderNotReadyError_NamesTheRealCause(t *testing.T) {
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

	// The premise this replaced was "keep the legacy message". That message said
	// "(check credentials)", and the providers that reach this branch (ollama,
	// openrouter, llama-local) have no credentials to check, so carrying it
	// outward to MCP would have been a NEW false claim on a surface that never
	// made one.
	t.Run("a provider that cannot report never blames credentials", func(t *testing.T) {
		msg := embedderNotReadyError("ollama", "").Error()
		if strings.Contains(msg, "credentials") {
			t.Errorf("must not blame credentials for an unclassified failure: %q", msg)
		}
		if !strings.Contains(msg, "did not report a cause") {
			t.Errorf("expected the honest no-cause wording: %q", msg)
		}
	})

	// The role is named so the message says which thing the user was doing.
	// `ask` failing to generate and `index` failing to embed are different
	// problems and used to produce interchangeable text.
	t.Run("the role is named", func(t *testing.T) {
		emb := embedderNotReadyError("bedrock", ai.TestErrTimeout).Error()
		gen := generatorNotReadyError("bedrock", ai.TestErrTimeout).Error()
		if !strings.HasPrefix(emb, "embedding provider") {
			t.Errorf("expected the embedding role named: %q", emb)
		}
		if !strings.HasPrefix(gen, "generation provider") {
			t.Errorf("expected the generation role named: %q", gen)
		}
	})
}

// providerReadiness renders for two different surfaces: a full sentence with a
// remedy for the portability hint, and a compact clause for the per-provider
// `reason` field, which already sits next to the provider's name.
func TestProviderReadiness_Rendering(t *testing.T) {
	t.Run("a ready provider explains nothing", func(t *testing.T) {
		r := providerReadiness{ready: true}
		if r.hint("bedrock") != "" || r.shortReason() != "" {
			t.Errorf("a ready provider must render empty, got hint=%q reason=%q", r.hint("bedrock"), r.shortReason())
		}
	})

	t.Run("a classified failure names the cause on both surfaces", func(t *testing.T) {
		r := providerReadiness{ready: false, code: ai.TestErrTimeout}
		hint := r.hint("bedrock")
		if !strings.Contains(hint, string(ai.TestErrTimeout)) || strings.Contains(hint, "If using Ollama") {
			t.Errorf("hint should name the cause, not list every provider's likeliest problem: %q", hint)
		}
		reason := r.shortReason()
		if !strings.Contains(reason, string(ai.TestErrTimeout)) {
			t.Errorf("reason should carry the code: %q", reason)
		}
		// The compact form does not repeat the provider name; the UI shows it.
		if strings.Contains(reason, "bedrock") {
			t.Errorf("reason should not repeat the provider name: %q", reason)
		}
	})

	// Both render paths must stay honest on the unclassified branch. The old
	// hint named two providers and let the reader guess which applied; the old
	// reason asserted "credentials missing or region unreachable", a cause
	// nobody observed, on the field the macOS app shows as its headline.
	t.Run("an unclassifiable failure asserts no cause", func(t *testing.T) {
		r := providerReadiness{ready: false}
		hint := r.hint("ollama")
		if strings.Contains(hint, "credentials") || strings.Contains(hint, "If using Ollama") {
			t.Errorf("hint must not guess a cause: %q", hint)
		}
		if !strings.Contains(hint, "did not report a cause") {
			t.Errorf("expected the honest no-cause hint: %q", hint)
		}
		if reason := r.shortReason(); strings.Contains(reason, "credentials") {
			t.Errorf("reason must not guess a cause: %q", reason)
		}
	})

	// The zero value must be distinguishable from a real verdict. A caller that
	// never probed (the active provider's embedder failed to register) would
	// otherwise report a confident cause nobody observed.
	t.Run("the zero value is not a verdict", func(t *testing.T) {
		var never providerReadiness
		if never.resolved {
			t.Error("an unprobed readiness must not claim to be resolved")
		}
		if got := resolveReadiness(context.Background(), nil); !got.resolved {
			t.Error("a probe that ran must be marked resolved, even when it found nothing")
		}
	})
}

// bedrockProviderStatus reuses the caller's verdict only when bedrock is active
// AND that verdict was actually probed. The reuse shipped without this test and
// a reviewer found the hole: when bedrock is active but its embedder failed to
// register (a profile named in config that is absent from ~/.aws), the caller
// holds no verdict, and the zero value would have reported "credentials missing
// or region unreachable" while the same run's portability line correctly said
// the provider was not registered. The macOS app renders that reason as its
// headline readiness text, so the wrong cause is the first thing a user reads.
func TestBedrockProviderStatus_ReusesOnlyAProbedVerdict(t *testing.T) {
	cfg := ai.AIConfig{Provider: "bedrock"}
	cfg.Bedrock.Region = "us-east-1"

	t.Run("an unprobed verdict is not reported as a cause", func(t *testing.T) {
		var never providerReadiness // embedder never registered, so never probed
		s := bedrockProviderStatus(context.Background(), cfg, never)
		// The invariant, and the only one available here: whatever the fallback
		// path produces, it must not be the UNPROBED verdict rendered as if it
		// were an observed cause.
		//
		// The exact fallback text cannot be asserted, because it depends on
		// whether ai.DefaultRegistry has bedrock registered, which varies with
		// what else ran in this binary: "not registered" when it does not, a real
		// probe's classified cause when it does. An earlier version asserted a
		// specific string and passed or failed depending on test order.
		// Deliberately the ONLY assertion. The fallback outcome legitimately
		// varies with the ambient environment: "not registered" when the registry
		// lacks bedrock, a classified cause when it has it and the probe fails,
		// and an EMPTY reason when it has it and the probe succeeds (a machine
		// with working credentials). All three are correct; rendering the
		// unprobed verdict as an observed cause is the bug.
		if s.Reason == never.shortReason() {
			t.Errorf("an unprobed verdict must not be rendered as a cause: %q", s.Reason)
		}
	})

	t.Run("a probed verdict is reused verbatim", func(t *testing.T) {
		probed := providerReadiness{ready: false, code: ai.TestErrTimeout, resolved: true}
		s := bedrockProviderStatus(context.Background(), cfg, probed)
		if s.Reachable {
			t.Error("a failed verdict must not report reachable")
		}
		if s.Reason != probed.shortReason() {
			t.Errorf("reason = %q, want the carried verdict %q", s.Reason, probed.shortReason())
		}
	})
}

// derivePortability consults a verdict in exactly one branch, and several
// earlier returns never reach it. Resolving eagerly made an offline `config
// doctor` wait out a live probe whose answer it then discarded.
func TestDerivePortability_DoesNotProbeWhenItDoesNotNeedTo(t *testing.T) {
	probes := 0
	counting := func() providerReadiness {
		probes++
		return providerReadiness{ready: true, resolved: true}
	}

	// An empty vault is decided before any provider question arises.
	if status, _ := derivePortability(ai.AIConfig{Provider: "bedrock"}, nil, counting,
		0, nil, 0, 0, 0, vault.IndexFreshness{}); status != "empty_vault" {
		t.Fatalf("status = %q, want empty_vault", status)
	}
	if probes != 0 {
		t.Errorf("empty vault must not probe a provider, got %d probe(s)", probes)
	}

	// So is a vault with content but nothing embedded yet.
	if status, _ := derivePortability(ai.AIConfig{Provider: "bedrock"}, nil, counting,
		0, nil, 3, 0, 3, vault.IndexFreshness{}); status != "unindexed" {
		t.Fatalf("status = %q, want unindexed", status)
	}
	if probes != 0 {
		t.Errorf("an unindexed vault must not probe a provider, got %d probe(s)", probes)
	}
}
