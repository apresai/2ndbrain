package ai

// Every timeout, retry count, and derived worst-case budget for the provider
// transports lives here, so the nesting invariant is auditable in one screen
// and machine-checked by TestTimeoutBudgetsNested:
//
//	every OUTER budget derives from its inner layer's worst case
//	(attempts x per-attempt timeout + backoff) plus slack,
//
// so the innermost bound always fires first and a timeout error names the
// real subsystem. The project principle extends from token budgets to time:
// a timeout bounds hangs and runaway cost, it must NEVER fail a working but
// slow model. The motivating incident: a live grok-4.6 `models test` failed
// with a classified [timeout] because the flat 30s probe deadline (and the
// 90s mantle per-attempt bound under it) fired while a cold-starting
// reasoning model was still legitimately working.
//
// Connect-phase bounds stay TIGHT (netstall.DialTimeout /
// netstall.TLSHandshakeTimeout, 10s each): a dead network must fail in
// seconds. Only the think-time bounds are generous.

import (
	"net/http"
	"time"

	"github.com/apresai/2ndbrain/internal/llama"
	"github.com/apresai/2ndbrain/internal/netstall"
)

// ── Mantle plane (OpenAI-Responses dialect) ────────────────────────────────

// MantleAttemptTimeout hard-bounds ONE mantle HTTP call, wall clock. The
// Responses call is NON-STREAMING: no byte reaches the client until the model
// has finished reasoning and answering, so byte progress carries no signal
// and a stall watchdog (netstall.Guard) is the wrong shape here — the honest
// split is tight connect bounds (dial + TLS, 10s each, via
// netstall.Transport) plus this generous think-time bound. 240s, raised from
// 90s: a cold-starting reasoning model (grok-4.6, first call of the day) was
// demonstrably still working when the 90s bound killed it, and a per-attempt
// bound that can fail a working model violates the project rule above. The
// resulting net timeout classifies as TestErrTimeout in ClassifyProbeError.
const MantleAttemptTimeout = 240 * time.Second

// mantleMaxAttempts bounds the 429 retry loop in doMantleRequest (moved here
// so MantleWorstCase derives from the count the loop actually runs).
const mantleMaxAttempts = 3

// mantleRetryBackoffWorstTotal is the total sleep the mantle 429 loop can
// insert: retryBackoff(a) for the non-final attempts a = 0..mantleMaxAttempts-2,
// a geometric sum of (2^(mantleMaxAttempts-1) - 1) seconds. 1s + 2s = 3s today.
const mantleRetryBackoffWorstTotal = (1<<(mantleMaxAttempts-1) - 1) * time.Second

// MantleWorstCase is the longest one mantle generation call can take before
// its own bounds resolve it: every attempt at the full per-attempt timeout
// plus the loop's backoff. Outer budgets (MCP tGenerate, probe deadlines)
// derive from this so they can never fire before the client's own bound.
const MantleWorstCase = mantleMaxAttempts*MantleAttemptTimeout + mantleRetryBackoffWorstTotal

// ── OpenRouter ─────────────────────────────────────────────────────────────

// OpenRouterAttemptTimeout bounds one OpenRouter HTTP call. It replaces the
// UNBOUNDED http.DefaultClient the embedder and generator previously shared,
// which had no timeout at all: a hung upstream parked the caller forever.
// Chat completions are non-streaming here, so the same wall-clock-attempt
// shape as mantle applies, sized for a slow hosted model.
const OpenRouterAttemptTimeout = 120 * time.Second

// openRouterMaxAttempts bounds the 429 retry loop in doOpenRouterRequest.
const openRouterMaxAttempts = 3

// openRouterRetryBackoffWorstTotal mirrors the mantle derivation for the
// identical backoff shape (1s + 2s = 3s today).
const openRouterRetryBackoffWorstTotal = (1<<(openRouterMaxAttempts-1) - 1) * time.Second

// OpenRouterWorstCase is the longest one OpenRouter call can take before its
// own bounds resolve it.
const OpenRouterWorstCase = openRouterMaxAttempts*OpenRouterAttemptTimeout + openRouterRetryBackoffWorstTotal

// retryBackoff returns the exponential delay before re-issuing 0-based
// attempt+1: 1s, 2s, 4s, ... Shared by the mantle and OpenRouter 429 retry
// loops so the worst-case totals above derive from the code that actually
// sleeps (TestTimeoutBudgetsNested sums this function and compares).
func retryBackoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}

// ── Classic Bedrock (AWS SDK: Converse / InvokeModel) ──────────────────────

// BedrockClassicAttemptTimeout bounds one HTTP attempt on the classic Bedrock
// planes, applied via the SDK HTTP client in loadBedrockAWSConfig. The classic
// SDK path previously had NO client timeout at all — the flat 30s probe
// context was its only bound, so `ask` and the embed pass could hang forever
// on a half-open connection. Classic Converse is non-streaming in 2nb and
// always-reasoning classic models (grok-4.6) burn real server-side think time
// before the response starts, so this matches the mantle attempt bound.
const BedrockClassicAttemptTimeout = 240 * time.Second

// bedrockSDKMaxAttempts pins the AWS SDK standard retryer's attempt count
// (loadBedrockAWSConfig passes it via WithRetryMaxAttempts). Pinning matters
// because the SDK retries transport timeouts internally: one Converse call
// can burn up to this many HTTP attempts at BedrockClassicAttemptTimeout
// each, and the worst case below must count them. 3 is the SDK default.
const bedrockSDKMaxAttempts = 3

// bedrockRetryBackoffWorstTotal is the maximum sleep 2nb's own retry loop
// (converseWithRetry / invokeModel, maxBedrockAttempts rounds) can insert:
// bedrockRetryDelay's jitter tops out at the full exponential base, so the
// worst total is 200ms * (2^(maxBedrockAttempts-1) - 1) = 3s today. (The 10s
// per-round cap in bedrockRetryDelay is unreached at 5 attempts; if
// maxBedrockAttempts grows past the cap this constant overstates, which only
// makes outer budgets more patient, never inverted.)
const bedrockRetryBackoffWorstTotal = 200 * time.Millisecond * ((1 << (maxBedrockAttempts - 1)) - 1)

// BedrockClassicWorstCase is the longest one classic Bedrock call can HANG
// before its own bounds resolve it: the SDK's internal attempts at the full
// per-attempt timeout, plus 2nb's own retry backoff. 2nb's retry rounds
// (maxBedrockAttempts) deliberately do not multiply the attempt timeout:
// they re-run only on throttle/server-fault RESPONSES, which return fast —
// a genuine hang burns SDK attempts inside one round and then exits the loop
// as non-retryable, so counting both multipliers would model an impossible
// path and inflate every outer budget above it.
const BedrockClassicWorstCase = bedrockSDKMaxAttempts*BedrockClassicAttemptTimeout + bedrockRetryBackoffWorstTotal

// ── Local engines (Ollama, llama-local) ────────────────────────────────────

// localAttemptTimeout bounds one HTTP call to a local inference server
// (Ollama, llama-server). Local generation on a big model is slow but the
// server is on-box: two minutes of silence means wedged, not thinking.
// Shared by every local client constructor so LocalWorstCase cannot drift
// from what the clients enforce.
const localAttemptTimeout = 120 * time.Second

// LocalWorstCase is the ceiling for one local-provider call. The request
// path itself performs only a short (~2s) health probe before the attempt,
// not the llama manager's HealthTimeout-bounded readiness loop (that loop
// runs in the launchd manager's background goroutine); the derivation
// deliberately budgets the full cold-start wait anyway so a probe issued
// the moment the engine boots is never failed while the engine is still
// legitimately loading weights. Over-generosity here is a hang ceiling,
// never a sleep.
const LocalWorstCase = llama.HealthTimeout + localAttemptTimeout

// ── Probe deadlines (models test / verify / doctor) ────────────────────────

// ProbeSlack is the headroom an outer probe deadline adds above the transport
// worst case it contains: classification, catalog persistence, and the fast
// non-final rounds of a throttle retry.
const ProbeSlack = 30 * time.Second

// ProbeDeadline returns the outer context deadline for one model probe,
// derived from the resolved route's transport worst case plus ProbeSlack so
// the innermost transport bound always fires first and a probe timeout names
// the transport, not the probe. This replaced a flat 30s that sat INSIDE the
// mantle client's own budget — the inversion that failed a working model.
// modelType is accepted for future refinement (an embed-only route could
// carry a tighter bound once its transport does); today provider + strategy
// decide, because embeddings ride the same per-attempt client bounds.
func ProbeDeadline(provider, strategy, modelType string) time.Duration {
	_ = modelType
	switch provider {
	case "bedrock":
		if strategy == StrategyBedrockMantleResponses {
			return MantleWorstCase + ProbeSlack
		}
		return BedrockClassicWorstCase + ProbeSlack
	case "openrouter":
		return OpenRouterWorstCase + ProbeSlack
	case "ollama", llamaProviderName:
		return LocalWorstCase + ProbeSlack
	default:
		return MaxProbeDeadline()
	}
}

// MaxProbeDeadline is the strategy-blind ceiling: the largest ProbeDeadline
// any route can return. Callers that cannot know the route in advance
// (doctor's tier budgets, bench) derive from this.
func MaxProbeDeadline() time.Duration {
	max := MantleWorstCase
	for _, d := range []time.Duration{BedrockClassicWorstCase, OpenRouterWorstCase, LocalWorstCase} {
		if d > max {
			max = d
		}
	}
	return max + ProbeSlack
}

// newProviderHTTPClient builds an HTTP client for the plain net/http provider
// paths (mantle, OpenRouter): connect phase tight (10s dial / 10s TLS via
// netstall.Transport, so a dead network fails in seconds), think time bounded
// by the given per-attempt wall-clock timeout.
func newProviderHTTPClient(attemptTimeout time.Duration) *http.Client {
	return &http.Client{Timeout: attemptTimeout, Transport: netstall.Transport()}
}
