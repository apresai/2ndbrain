package ai

import "time"

// ProviderRPSDefault returns a conservative default embedding request rate
// per provider. Used by bulk-embedding paths so a vault with hundreds of
// documents doesn't hammer Bedrock's quota or hit OpenRouter's free-tier
// rpm limit.
//
// Returns 0 (meaning "no throttle") for local providers where the cost of
// a request is CPU-bound and the user would rather finish fast. Callers
// should treat 0 as "skip the sleep" rather than "sleep forever."
func ProviderRPSDefault(provider string) float64 {
	switch provider {
	case "bedrock":
		// AWS Bedrock default quotas vary by model and account. Nova-2
		// embeddings are ~20 rps on first-request quota; keep a margin.
		return 10
	case "openrouter":
		// Free models enforce ~20 requests/minute. Paid can burst much
		// higher but we don't know which tier the user is on at call time.
		return 5
	case "ollama":
		// Local HTTP loopback — no upstream throttling to worry about.
		return 0
	default:
		return 0
	}
}

// ProviderEmbedConcurrencyDefault returns a conservative default number of
// simultaneous in-flight embedding requests for the bulk-embed worker pool.
//
// Bedrock embeddings are RPM-throttled and AWS does not publish per-account
// defaults, so this starts LOW (4) and is self-correcting: the embed path
// retries ThrottlingException with exponential backoff, so an over-set value
// degrades to retries rather than failures. Raise it per-vault with
// `2nb config set ai.embed_concurrency N` once an account's real ceiling is
// known (a `2nb ai embed-probe` tuner is planned to find it). OpenRouter free
// tiers are tight; Ollama is a
// local server that commonly serializes requests, so concurrency there is modest.
func ProviderEmbedConcurrencyDefault(provider string) int {
	switch provider {
	case "bedrock":
		return 4
	case "openrouter":
		return 3
	case "ollama":
		return 2
	default:
		return 4
	}
}

// ThrottleDelay converts an RPS value into a per-request sleep duration.
// RPS <= 0 returns 0 (no throttle).
func ThrottleDelay(rps float64) time.Duration {
	if rps <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / rps)
}
