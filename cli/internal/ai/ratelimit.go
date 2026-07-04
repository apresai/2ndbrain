package ai

// ProviderEmbedConcurrencyDefault returns a conservative default number of
// simultaneous in-flight embedding requests for the bulk-embed worker pool.
//
// Bedrock embeddings are RPM-throttled and AWS does not publish a per-account
// sync quota for Nova-2, so this starts LOW (4) and is self-correcting: the
// embed path retries ThrottlingException with exponential backoff + jitter, so
// an over-set value degrades to retries rather than failures. Raise it per-vault
// with `2nb config set ai.embed_concurrency N`, or find an account's real
// ceiling empirically with `2nb ai embed-probe` (it ramps 4,8,16,32). OpenRouter
// free tiers are tight; Ollama is a local server that commonly serializes
// requests, so concurrency there is modest.
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
