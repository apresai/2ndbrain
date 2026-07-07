package ai

// Invoke strategies name the API dialect used to call a model. Catalog
// entries declare their strategy so the dispatcher can route requests
// without a per-model-ID switch inside each provider client. Phase 1
// only defines the vocabulary; dispatching by strategy lands in phase 2.
const (
	// StrategyBedrockConverse — AWS Bedrock Runtime Converse / ConverseStream.
	// The unified chat API that works across Claude, Nova, Llama, Mistral, etc.
	StrategyBedrockConverse = "bedrock_converse"

	// StrategyBedrockInvokeAnthropic — Bedrock InvokeModel with an Anthropic
	// messages envelope. Used for Claude models when Converse isn't desired
	// (streaming quirks, legacy behavior).
	StrategyBedrockInvokeAnthropic = "bedrock_invoke_anthropic"

	// StrategyBedrockInvokeNova — Bedrock InvokeModel with the Nova generation
	// envelope (rarely needed — Nova gen models route through Converse today).
	StrategyBedrockInvokeNova = "bedrock_invoke_nova"

	// StrategyBedrockInvokeNovaEmbed — Bedrock InvokeModel Nova embeddings v2
	// envelope ({"taskType":...,"singleEmbeddingParams":{...}}).
	StrategyBedrockInvokeNovaEmbed = "bedrock_invoke_nova_embed"

	// StrategyBedrockInvokeTitanEmbed — Bedrock InvokeModel Titan embedding
	// envelope (amazon.titan-embed-*).
	StrategyBedrockInvokeTitanEmbed = "bedrock_invoke_titan_embed"

	// StrategyBedrockInvokeCohereEmbed — Bedrock InvokeModel Cohere embedding
	// envelope (cohere.embed-*).
	StrategyBedrockInvokeCohereEmbed = "bedrock_invoke_cohere_embed"

	// StrategyBedrockInvokeMarengo27 — TwelveLabs Marengo 2.7 embed envelope
	// ({"inputType":"text","inputText":"..."}).
	StrategyBedrockInvokeMarengo27 = "bedrock_invoke_marengo_2_7"

	// StrategyBedrockInvokeMarengo30 — TwelveLabs Marengo 3.0 embed envelope
	// ({"inputType":"text","text":{"inputText":"..."}}).
	StrategyBedrockInvokeMarengo30 = "bedrock_invoke_marengo_3_0"

	// StrategyBedrockMantleResponses names the AWS Bedrock "mantle" invocation
	// plane: the OpenAI Responses dialect over REST at
	// https://bedrock-mantle.<region>.api.aws with bearer-token auth. Models
	// on this plane (e.g. openai.gpt-5.5, xai.grok-4.3) are region-pinned per
	// model (ModelInfo.Region) and invisible to the classic control plane
	// (list-foundation-models / GetFoundationModel).
	StrategyBedrockMantleResponses = "bedrock_mantle_responses"

	// StrategyAnthropicMessages — api.anthropic.com/v1/messages (direct,
	// not via Bedrock). Auth: x-api-key header.
	StrategyAnthropicMessages = "anthropic_messages"

	// StrategyOpenAIChat — api.openai.com/v1/chat/completions (direct).
	StrategyOpenAIChat = "openai_chat"

	// StrategyOpenAIEmbeddings — api.openai.com/v1/embeddings (direct).
	StrategyOpenAIEmbeddings = "openai_embeddings"

	// StrategyOpenRouterChat — openrouter.ai/api/v1/chat/completions.
	StrategyOpenRouterChat = "openrouter_chat"

	// StrategyOpenRouterEmbeddings — openrouter.ai/api/v1/embeddings.
	StrategyOpenRouterEmbeddings = "openrouter_embeddings"

	// StrategyOllamaGenerate — localhost Ollama /api/generate.
	StrategyOllamaGenerate = "ollama_generate"

	// StrategyOllamaEmbeddings — localhost Ollama /api/embeddings.
	StrategyOllamaEmbeddings = "ollama_embeddings"

	// StrategyLlamaChat — localhost llama.cpp llama-server /v1/chat/completions.
	StrategyLlamaChat = "llama_chat"

	// StrategyLlamaEmbeddings — localhost llama-server /v1/embeddings.
	StrategyLlamaEmbeddings = "llama_embeddings"

	// StrategyLlamaRerank — localhost llama-server /v1/rerank (cross-encoder,
	// --reranking --pooling rank).
	StrategyLlamaRerank = "llama_rerank"
)

// KnownInvokeStrategies returns the full list of recognized strategies.
// Used by validation in catalog tooling and the wizard.
func KnownInvokeStrategies() []string {
	return []string{
		StrategyBedrockConverse,
		StrategyBedrockInvokeAnthropic,
		StrategyBedrockInvokeNova,
		StrategyBedrockInvokeNovaEmbed,
		StrategyBedrockInvokeTitanEmbed,
		StrategyBedrockInvokeCohereEmbed,
		StrategyBedrockInvokeMarengo27,
		StrategyBedrockInvokeMarengo30,
		StrategyBedrockMantleResponses,
		StrategyAnthropicMessages,
		StrategyOpenAIChat,
		StrategyOpenAIEmbeddings,
		StrategyOpenRouterChat,
		StrategyOpenRouterEmbeddings,
		StrategyOllamaGenerate,
		StrategyOllamaEmbeddings,
		StrategyLlamaChat,
		StrategyLlamaEmbeddings,
		StrategyLlamaRerank,
	}
}

// IsKnownInvokeStrategy reports whether s is one of the strategies this
// binary recognizes. Unknown strategies are preserved on catalog load
// (for forward-compat with newer yaml files) but the dispatcher will
// refuse to route them in phase 2.
func IsKnownInvokeStrategy(s string) bool {
	if s == "" {
		return false
	}
	for _, k := range KnownInvokeStrategies() {
		if k == s {
			return true
		}
	}
	return false
}
