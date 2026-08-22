package ai

import (
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/llama"
)

// TestTimeoutBudgetsNested machine-checks the nesting invariant of
// timeouts.go: every outer budget contains its inner layer's worst case
// (attempts x per-attempt timeout + backoff) plus slack, so the innermost
// bound always fires first and a timeout error names the real subsystem. A
// change that reintroduces an inversion — an outer deadline that can expire
// while an inner client would still have legitimately succeeded — fails here
// instead of failing a working model in the field.
func TestTimeoutBudgetsNested(t *testing.T) {
	// Floor: the mantle attempt bound must stay generous enough for a cold
	// start. Evidence: a live grok-4.6 `models test` on 2026-08-21 failed with
	// a classified [timeout] because the old 90s per-attempt bound (under a
	// flat 30s probe deadline) fired while a cold-starting always-reasoning
	// model was still working; the model answered fine on the next call. A
	// per-attempt bound below 2 minutes is known-unsafe for this plane.
	if MantleAttemptTimeout < 2*time.Minute {
		t.Errorf("MantleAttemptTimeout = %v, below the 2min floor; 90s demonstrably killed a working cold-start grok-4.6 reasoning run — a timeout bounds hangs, it must never fail a working model", MantleAttemptTimeout)
	}

	// The declared backoff worst totals must contain what the retry loops
	// actually sleep (retryBackoff over the non-final attempts).
	var mantleBackoff time.Duration
	for a := 0; a < mantleMaxAttempts-1; a++ {
		mantleBackoff += retryBackoff(a)
	}
	if mantleRetryBackoffWorstTotal < mantleBackoff {
		t.Errorf("mantleRetryBackoffWorstTotal = %v < the loop's real worst backoff %v", mantleRetryBackoffWorstTotal, mantleBackoff)
	}
	if MantleWorstCase < mantleMaxAttempts*MantleAttemptTimeout+mantleBackoff {
		t.Errorf("MantleWorstCase = %v does not contain %d attempts x %v + %v backoff", MantleWorstCase, mantleMaxAttempts, MantleAttemptTimeout, mantleBackoff)
	}

	var orBackoff time.Duration
	for a := 0; a < openRouterMaxAttempts-1; a++ {
		orBackoff += retryBackoff(a)
	}
	if OpenRouterWorstCase < openRouterMaxAttempts*OpenRouterAttemptTimeout+orBackoff {
		t.Errorf("OpenRouterWorstCase = %v does not contain %d attempts x %v + %v backoff", OpenRouterWorstCase, openRouterMaxAttempts, OpenRouterAttemptTimeout, orBackoff)
	}

	// Classic Bedrock: the declared worst-case backoff must contain the real
	// maximum sleep of bedrockRetryDelay (jitter tops out at the full
	// exponential base, per-round cap 10s) across 2nb's retry rounds.
	var brBackoff time.Duration
	for a := 1; a < maxBedrockAttempts; a++ {
		base := 200 * time.Millisecond * time.Duration(1<<(a-1))
		if base > 10*time.Second {
			base = 10 * time.Second
		}
		brBackoff += base
	}
	if bedrockRetryBackoffWorstTotal < brBackoff {
		t.Errorf("bedrockRetryBackoffWorstTotal = %v < the retry loop's real worst backoff %v", bedrockRetryBackoffWorstTotal, brBackoff)
	}
	if BedrockClassicWorstCase < bedrockSDKMaxAttempts*BedrockClassicAttemptTimeout+brBackoff {
		t.Errorf("BedrockClassicWorstCase = %v does not contain %d SDK attempts x %v + %v backoff", BedrockClassicWorstCase, bedrockSDKMaxAttempts, BedrockClassicAttemptTimeout, brBackoff)
	}

	// Local: the worst case must contain the llama manager's real cold-start
	// health wait plus one client attempt.
	if LocalWorstCase < llama.HealthTimeout+localAttemptTimeout {
		t.Errorf("LocalWorstCase = %v does not contain the engine health wait %v + one attempt %v", LocalWorstCase, llama.HealthTimeout, localAttemptTimeout)
	}

	// Every strategy-specific probe deadline contains its route's worst case
	// plus slack, and the strategy-blind ceiling contains every deadline.
	cases := []struct {
		name, provider, strategy string
		inner                    time.Duration
	}{
		{"mantle", "bedrock", StrategyBedrockMantleResponses, MantleWorstCase},
		{"bedrock classic", "bedrock", "", BedrockClassicWorstCase},
		{"bedrock classic converse", "bedrock", StrategyBedrockConverse, BedrockClassicWorstCase},
		{"openrouter", "openrouter", "", OpenRouterWorstCase},
		{"ollama", "ollama", "", LocalWorstCase},
		{"llama-local", llamaProviderName, "", LocalWorstCase},
		{"unknown provider", "someday-provider", "", 0},
	}
	for _, c := range cases {
		for _, modelType := range []string{"generation", "embedding"} {
			got := ProbeDeadline(c.provider, c.strategy, modelType)
			if got < c.inner+ProbeSlack {
				t.Errorf("ProbeDeadline(%s, %s) = %v, below its inner worst case %v + ProbeSlack %v: the outer deadline could fire while the inner client was still legitimately working", c.name, modelType, got, c.inner, ProbeSlack)
			}
			if MaxProbeDeadline() < got {
				t.Errorf("MaxProbeDeadline() = %v < ProbeDeadline(%s, %s) = %v; strategy-blind callers (doctor, bench) would starve this route", MaxProbeDeadline(), c.name, modelType, got)
			}
		}
	}
}
