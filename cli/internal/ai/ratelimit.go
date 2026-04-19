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

// ThrottleDelay converts an RPS value into a per-request sleep duration.
// RPS <= 0 returns 0 (no throttle).
func ThrottleDelay(rps float64) time.Duration {
	if rps <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / rps)
}
