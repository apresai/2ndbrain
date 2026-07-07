package ai

import "testing"

func TestBedrockContextLenHint(t *testing.T) {
	tests := []struct {
		id   string
		want int
	}{
		// Anthropic current line (inference-profile IDs strip the geo prefix).
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", 200_000},
		{"anthropic.claude-3-haiku-20240307-v1:0", 200_000},
		{"us.anthropic.claude-sonnet-4-6", 1_000_000},
		{"global.anthropic.claude-sonnet-5", 1_000_000},
		{"us.anthropic.claude-opus-4-8", 1_000_000},
		{"us.anthropic.claude-fable-5", 1_000_000},
		// Pre-4.6 versions stay 200K, including date-suffixed IDs.
		{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", 200_000},
		{"us.anthropic.claude-sonnet-4-20250514-v1:0", 200_000},
		{"us.anthropic.claude-opus-4-1-20250805-v1:0", 200_000},
		{"us.anthropic.claude-opus-4-5-20251101-v1:0", 200_000},
		// Amazon + others.
		{"amazon.nova-micro-v1:0", 128_000},
		{"amazon.nova-lite-v1:0", 300_000},
		{"amazon.nova-pro-v1:0", 300_000},
		{"amazon.nova-premier-v1:0", 1_000_000},
		{"amazon.titan-embed-text-v2:0", 8192},
		{"amazon.nova-2-multimodal-embeddings-v1:0", 8192},
		{"cohere.embed-english-v3", 512},
		{"meta.llama4-scout-17b-instruct-v1:0", 128_000},
		{"us.meta.llama3-3-70b-instruct-v1:0", 128_000},
		{"mistral.pixtral-large-2502-v1:0", 128_000},
		{"mistral.mistral-large-2407-v1:0", 128_000},
		// Unknown families stay honest.
		{"ai21.jamba-1-5-large-v1:0", 0},
		{"deepseek.v3.2", 0},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := bedrockContextLenHint(tt.id); got != tt.want {
				t.Errorf("bedrockContextLenHint(%q) = %d, want %d", tt.id, got, tt.want)
			}
		})
	}
}

// TestBedrockContextLenHint_AgreesWithBuiltinCatalog pins the hint map to the
// curated catalog: every builtin bedrock entry with a declared context length
// must get the same value from the discovery hint, so a user sees consistent
// numbers whether a model arrived via the catalog or via discovery.
func TestBedrockContextLenHint_AgreesWithBuiltinCatalog(t *testing.T) {
	for _, m := range BuiltinCatalog() {
		if m.Provider != "bedrock" || m.ContextLen == 0 || m.Type == "rerank" {
			continue
		}
		if hint := bedrockContextLenHint(m.ID); hint != 0 && hint != m.ContextLen {
			t.Errorf("hint for %s = %d disagrees with builtin catalog %d", m.ID, hint, m.ContextLen)
		}
	}
}
