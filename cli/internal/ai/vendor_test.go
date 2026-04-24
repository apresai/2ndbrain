package ai

import "testing"

func TestVendorOf_Bedrock(t *testing.T) {
	cases := []struct {
		id            string
		wantVendor    string
		wantDisplay   string
		wantFamily    string
	}{
		// anthropic + geo prefix
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "anthropic", "Anthropic", "Claude"},
		{"eu.anthropic.claude-opus-4-7", "anthropic", "Anthropic", "Claude"},
		{"anthropic.claude-3-5-haiku-20241022-v1:0", "anthropic", "Anthropic", "Claude"},
		// amazon
		{"amazon.nova-pro-v1:0", "amazon", "Amazon", "Nova"},
		{"amazon.nova-2-multimodal-embeddings-v1:0", "amazon", "Amazon", "Nova Embed"},
		{"amazon.titan-embed-text-v2:0", "amazon", "Amazon", "Titan Embed"},
		// meta via llama
		{"us.meta.llama3-1-70b-instruct-v1:0", "meta", "Meta", "Llama"},
		// mistral families
		{"mistral.mixtral-8x7b-instruct-v0:1", "mistral", "Mistral", "Mixtral"},
		{"us.mistral.pixtral-large-2502-v1:0", "mistral", "Mistral", "Pixtral"},
		// cohere
		{"cohere.embed-english-v3", "cohere", "Cohere", "Embed"},
		{"cohere.command-r-plus-v1:0", "cohere", "Cohere", "Command"},
		// google gemma
		{"google.gemma-3-27b-it", "google", "Google", "Gemma"},
		// qwen / zai / moonshot
		{"qwen.qwen3-32b-v1:0", "qwen", "Qwen", "Qwen"},
		{"zai.glm-4.7", "zai", "Z.ai", "GLM"},
		{"moonshot.kimi-k2-thinking", "moonshot", "Moonshot", "Kimi"},
		// twelvelabs
		{"us.twelvelabs.marengo-embed-3-0-v1:0", "twelvelabs", "TwelveLabs", "Marengo"},
		// unknown
		{"mystery.v1", "mystery", "Mystery", ""},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := VendorOf(tc.id, "bedrock")
			if got.Vendor != tc.wantVendor {
				t.Errorf("vendor = %q, want %q", got.Vendor, tc.wantVendor)
			}
			if got.Display != tc.wantDisplay {
				t.Errorf("display = %q, want %q", got.Display, tc.wantDisplay)
			}
			if got.Family != tc.wantFamily {
				t.Errorf("family = %q, want %q", got.Family, tc.wantFamily)
			}
		})
	}
}

func TestVendorOf_OpenRouter(t *testing.T) {
	cases := []struct {
		id, wantVendor, wantDisplay string
	}{
		{"anthropic/claude-haiku-4-5", "anthropic", "Anthropic"},
		{"google/gemma-4-31b-it:free", "google", "Google"},
		{"openai/gpt-4o-mini", "openai", "OpenAI"},
		{"meta-llama/llama-3.3-70b-instruct:free", "meta-llama", "Meta-Llama"},
		{"nvidia/llama-nemotron-embed-vl-1b-v2:free", "nvidia", "NVIDIA"},
		// No slash → not a well-formed OpenRouter ID.
		{"just-a-name", "other", "Other"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := VendorOf(tc.id, "openrouter")
			if got.Vendor != tc.wantVendor {
				t.Errorf("vendor = %q, want %q", got.Vendor, tc.wantVendor)
			}
			if got.Display != tc.wantDisplay {
				t.Errorf("display = %q, want %q", got.Display, tc.wantDisplay)
			}
		})
	}
}

func TestVendorOf_Ollama(t *testing.T) {
	cases := []struct {
		id, wantVendor, wantDisplay string
	}{
		{"llama3.2:3b", "meta", "Meta"},
		{"gemma3:4b", "google", "Google"},
		{"qwen3:30b-a3b", "qwen", "Qwen"},
		{"mistral:7b-instruct", "mistral", "Mistral"},
		{"nomic-embed-text", "nomic", "Nomic"},
		{"mxbai-embed-large", "mixedbread", "Mixedbread"},
		{"bge-m3", "baai", "BAAI"},
		{"all-minilm", "sentence-transformers", "Sentence Transformers"},
		{"some-random-local-model", "community", "Community"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := VendorOf(tc.id, "ollama")
			if got.Vendor != tc.wantVendor {
				t.Errorf("vendor = %q, want %q", got.Vendor, tc.wantVendor)
			}
			if got.Display != tc.wantDisplay {
				t.Errorf("display = %q, want %q", got.Display, tc.wantDisplay)
			}
		})
	}
}

func TestVendorOf_UnknownProvider(t *testing.T) {
	got := VendorOf("foo", "mystery-provider")
	if got.Vendor != "other" {
		t.Errorf("unknown provider should return vendor=other, got %q", got.Vendor)
	}
}

func TestVersionSortKey_NewestSortsFirst(t *testing.T) {
	// Descending sort by VersionSortKey should put newer versions first.
	ids := []string{
		"claude-3-5-haiku-20241022",
		"claude-haiku-4-5-20251001",
		"claude-opus-4-7",
		"claude-opus-4-6",
	}
	// Expect newest (latest 2025-10-01 / version 4-7) first.
	// The key-based sort isn't perfect — 4-7 > 4-6 > 4-5 > 3-5 in the
	// numeric sense of VersionSortKey. Verify the highest-versioned
	// entries end up at the front.
	models := make([]ModelInfo, len(ids))
	for i, id := range ids {
		models[i] = ModelInfo{ID: id}
	}
	SortByVersionDesc(models)
	// The top entry should be one of the 4.x opus/haiku rather than 3.5.
	if models[len(models)-1].ID != "claude-3-5-haiku-20241022" {
		t.Errorf("expected 3.5 model last, got last=%q (order=%v)", models[len(models)-1].ID, idsOf(models))
	}
	// Claude 4-7 should beat Claude 4-6 / 4-5.
	if models[0].ID != "claude-opus-4-7" {
		t.Errorf("expected claude-opus-4-7 at top, got %q (order=%v)", models[0].ID, idsOf(models))
	}
}

func TestVersionSortKey_PadsSingleDigits(t *testing.T) {
	// "v10" must beat "v9" when sorted lexicographically, which only
	// works if we zero-pad the number runs.
	models := []ModelInfo{{ID: "foo-v9"}, {ID: "foo-v10"}}
	SortByVersionDesc(models)
	if models[0].ID != "foo-v10" {
		t.Errorf("expected v10 first, got %v", idsOf(models))
	}
}

func idsOf(m []ModelInfo) []string {
	out := make([]string, len(m))
	for i, mm := range m {
		out[i] = mm.ID
	}
	return out
}
