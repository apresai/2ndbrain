package ai

import (
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateNormalize(t *testing.T) {
	// dim < len: slice + L2-normalize. [3,4,x,x] -> [0.6,0.8].
	got := truncateNormalize([]float32{3, 4, 9, 9}, 2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if math.Abs(float64(got[0])-0.6) > 1e-6 || math.Abs(float64(got[1])-0.8) > 1e-6 {
		t.Errorf("normalized = %v, want [0.6 0.8]", got)
	}
	var norm float64
	for _, x := range got {
		norm += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-6 {
		t.Errorf("result not unit-length: norm=%v", math.Sqrt(norm))
	}

	// dim >= len: unchanged.
	full := []float32{1, 2, 3}
	if out := truncateNormalize(full, 3); len(out) != 3 || out[0] != 1 {
		t.Errorf("dim>=len should return input unchanged, got %v", out)
	}
	if out := truncateNormalize(full, 0); len(out) != 3 {
		t.Errorf("dim<=0 should return input unchanged, got %v", out)
	}

	// all-zero prefix: no NaN, returns zeros.
	z := truncateNormalize([]float32{0, 0, 5}, 2)
	for _, x := range z {
		if math.IsNaN(float64(x)) || x != 0 {
			t.Errorf("zero prefix should stay zero (no NaN), got %v", z)
		}
	}
}

func TestInitLlamaRegisters(t *testing.T) {
	reg := NewRegistry()
	err := InitLlama(context.Background(), reg, LlamaConfig{}, AIConfig{
		EmbeddingModel:  "embeddinggemma-2",
		GenerationModel: "gemma4-e4b",
		Dimensions:      768,
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := reg.Embedder(llamaProviderName)
	if err != nil {
		t.Fatalf("embedder not registered: %v", err)
	}
	if e.Name() != llamaProviderName || e.Dimensions() != 768 {
		t.Errorf("embedder = %q/%d, want %q/768", e.Name(), e.Dimensions(), llamaProviderName)
	}
	g, err := reg.Generator(llamaProviderName)
	if err != nil {
		t.Fatalf("generator not registered: %v", err)
	}
	if g.Name() != llamaProviderName {
		t.Errorf("generator name = %q", g.Name())
	}
	// UsageGenerator is implemented so RAG records real token counts.
	if _, ok := g.(UsageGenerator); !ok {
		t.Error("LlamaGenerator should implement UsageGenerator")
	}
}

func TestLlamaRerankModelDefault(t *testing.T) {
	// Unset → the LOCAL default, not the Bedrock DefaultRerankModel.
	if got := llamaRerankModel(AIConfig{}); got != DefaultLlamaRerankModel {
		t.Errorf("unset rerank model = %q, want %q", got, DefaultLlamaRerankModel)
	}
	if DefaultLlamaRerankModel == DefaultRerankModel {
		t.Error("local rerank default must differ from the Bedrock DefaultRerankModel")
	}
	// Explicit config wins.
	if got := llamaRerankModel(AIConfig{Rerank: RerankConfig{Model: "custom-reranker"}}); got != "custom-reranker" {
		t.Errorf("configured rerank model = %q, want custom-reranker", got)
	}
}

func TestInitLlamaRerankerGating(t *testing.T) {
	// Rerank disabled: no reranker registered.
	reg := NewRegistry()
	if err := InitLlama(context.Background(), reg, LlamaConfig{}, AIConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Reranker(llamaProviderName); err == nil {
		t.Error("reranker should NOT be registered when ai.rerank.enabled is false")
	}

	// Rerank enabled: a RerankProvider is registered.
	reg2 := NewRegistry()
	if err := InitLlama(context.Background(), reg2, LlamaConfig{}, AIConfig{Rerank: RerankConfig{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	rr, err := reg2.Reranker(llamaProviderName)
	if err != nil {
		t.Fatalf("reranker not registered when enabled: %v", err)
	}
	if rr.Name() != llamaProviderName {
		t.Errorf("reranker name = %q, want %q", rr.Name(), llamaProviderName)
	}
}

// requireLlamaEndpoint skips unless a real llama-server is reachable. Point the
// env vars at a manually-started server:
//
//	llama-server -m embeddinggemma.gguf --embeddings --pooling mean --port 8081
//	llama-server -m gemma4-e4b.gguf --port 8080
//	2NB_TEST_LLAMA_EMBED_ENDPOINT=http://127.0.0.1:8081 \
//	2NB_TEST_LLAMA_GEN_ENDPOINT=http://127.0.0.1:8080 go test ./internal/ai -run Llama
func requireLlamaEndpoints(t *testing.T) (embed, gen string) {
	t.Helper()
	embed = os.Getenv("2NB_TEST_LLAMA_EMBED_ENDPOINT")
	gen = os.Getenv("2NB_TEST_LLAMA_GEN_ENDPOINT")
	if embed == "" || gen == "" {
		t.Skip("set 2NB_TEST_LLAMA_EMBED_ENDPOINT and 2NB_TEST_LLAMA_GEN_ENDPOINT to a running llama-server")
	}
	return embed, gen
}

func TestLlamaEmbedAndGenerateLive(t *testing.T) {
	embedEP, genEP := requireLlamaEndpoints(t)
	ctx := context.Background()

	e := NewLlamaEmbedder("embeddinggemma-2", 768, "", embedEP)
	if !e.Available(ctx) {
		t.Skip("embed endpoint not answering /health resolution")
	}
	vecs, err := e.Embed(ctx, []string{"a local knowledge base", "unrelated text"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 768 {
		t.Fatalf("got %d vecs of dim %d, want 2x768", len(vecs), len(vecs[0]))
	}

	// Matryoshka truncation via WithDimension.
	small, err := e.Embed(ctx, []string{"x"}, WithDimension(256))
	if err != nil {
		t.Fatalf("Embed(256): %v", err)
	}
	if len(small[0]) != 256 {
		t.Errorf("WithDimension(256) len = %d, want 256", len(small[0]))
	}

	// Multi-chunk + 5800-rune long-text batch must succeed without HTTP 500 and
	// return valid unit-norm vectors for every chunk.
	longText := strings.Repeat("knowledge graph indexing ", 232) // 5800 runes
	if rc := utf8.RuneCountInString(longText); rc != 5800 {
		t.Fatalf("longText rune count = %d, want 5800", rc)
	}
	multi := make([]string, 10)
	for i := 0; i < 9; i++ {
		multi[i] = fmt.Sprintf("## Section %d\nLocal obsidian vault chunk #%d covering semantic retrieval.", i+1, i+1)
	}
	multi[9] = longText
	multiVecs, err := e.Embed(ctx, multi, WithPurpose(PurposeIndex), WithTitle("Long Vault Note"))
	if err != nil {
		t.Fatalf("Embed(multi + 5800-rune): %v", err)
	}
	if len(multiVecs) != len(multi) {
		t.Fatalf("got %d vecs for %d chunks", len(multiVecs), len(multi))
	}
	for i, v := range multiVecs {
		if len(v) != 768 {
			t.Fatalf("multiVecs[%d] len = %d, want 768", i, len(v))
		}
		var sqSum float64
		for _, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				t.Fatalf("multiVecs[%d] contains non-finite value %v", i, x)
			}
			sqSum += float64(x) * float64(x)
		}
		norm := math.Sqrt(sqSum)
		if math.Abs(norm-1.0) > 1e-4 {
			t.Fatalf("multiVecs[%d] L2 norm = %f, want ~1.0", i, norm)
		}
	}

	g := NewLlamaGenerator("gemma4-e4b", genEP)
	text, usage, err := g.GenerateWithUsage(ctx, "Reply with the single word: ok", GenOpts{MaxTokens: 16})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if text == "" {
		t.Error("empty generation")
	}
	if usage.OutputTokens == 0 {
		t.Error("expected real token usage from llama-server")
	}
}

func TestLlamaRerankLive(t *testing.T) {
	ep := os.Getenv("2NB_TEST_LLAMA_RERANK_ENDPOINT")
	if ep == "" {
		t.Skip("set 2NB_TEST_LLAMA_RERANK_ENDPOINT to a llama-server started with --reranking --pooling rank")
	}
	ctx := context.Background()
	r := NewLlamaReranker("bge-reranker-v2-m3", ep)
	docs := []string{
		"The mitochondrion is the powerhouse of the cell.",
		"To reset your password, open Settings and click Security.",
	}
	hits, err := r.Rerank(ctx, "how do I change my account password?", docs, 0)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(hits) != len(docs) {
		t.Fatalf("got %d hits for %d docs", len(hits), len(docs))
	}
	var s0, s1 float64
	for _, h := range hits {
		switch h.Index {
		case 0:
			s0 = h.Score
		case 1:
			s1 = h.Score
		}
	}
	if s1 <= s0 {
		t.Errorf("expected the relevant doc to score higher: password=%v biology=%v", s1, s0)
	}
}

func TestFormatLlamaEmbedText(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		cfg    EmbedConfig
		preset string
		want   string
	}{
		// Default and search preset
		{
			name:   "query purpose default search preset",
			text:   "how to calibrate models",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "",
			want:   "task: search result | query: how to calibrate models",
		},
		{
			name:   "query purpose explicit search preset",
			text:   "how to calibrate models",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "search",
			want:   "task: search result | query: how to calibrate models",
		},
		// Code preset
		{
			name:   "query purpose code preset",
			text:   "binary search tree implementation",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "code",
			want:   "task: code retrieval | query: binary search tree implementation",
		},
		{
			name:   "query_text purpose code preset with mixed case",
			text:   "func main()",
			cfg:    EmbedConfig{Purpose: PurposeQueryText},
			preset: "  Code ",
			want:   "task: code retrieval | query: func main()",
		},
		// QA preset
		{
			name:   "query purpose qa preset",
			text:   "what is the half-life of carbon-14?",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "qa",
			want:   "task: question answering | query: what is the half-life of carbon-14?",
		},
		// None preset (symmetric retrieval)
		{
			name:   "query purpose none preset",
			text:   "raw search text",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "none",
			want:   "raw search text",
		},
		// Symmetric preset (alias for none)
		{
			name:   "query purpose symmetric preset returns raw text",
			text:   "raw search text",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "symmetric",
			want:   "raw search text",
		},
		{
			name:   "query purpose symmetric preset with mixed case returns raw text",
			text:   "task: fix memory leak",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "Symmetric",
			want:   "task: fix memory leak",
		},
		// Unknown preset fallback
		{
			name:   "query purpose unknown preset fallback to search",
			text:   "general query",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "unrecognized_custom_preset",
			want:   "task: search result | query: general query",
		},
		// Query false-positive collision check (starting with "task:" without delimiter must be conditioned)
		{
			name:   "query starting with task: without delimiter formatted with default search preset",
			text:   "task: fix memory leak",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "",
			want:   "task: search result | query: task: fix memory leak",
		},
		{
			name:   "query starting with task: without delimiter formatted with explicit search preset",
			text:   "task: fix memory leak",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "search",
			want:   "task: search result | query: task: fix memory leak",
		},
		{
			name:   "query starting with task: without delimiter formatted with code preset",
			text:   "task: fix memory leak",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "code",
			want:   "task: code retrieval | query: task: fix memory leak",
		},
		{
			name:   "query starting with task: without delimiter formatted with qa preset",
			text:   "task: fix memory leak",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "qa",
			want:   "task: question answering | query: task: fix memory leak",
		},
		// Double prefixing avoidance for queries
		{
			name:   "query already prefixed with task code retrieval",
			text:   "task: code retrieval | query: existing query",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "code",
			want:   "task: code retrieval | query: existing query",
		},
		{
			name:   "query already prefixed with task search result",
			text:   "task: search result | query: existing query",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "search",
			want:   "task: search result | query: existing query",
		},
		{
			name:   "query already prefixed with task search result mixed case",
			text:   "Task: search result | query: foo",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "search",
			want:   "Task: search result | query: foo",
		},
		{
			name:   "query already prefixed with task code retrieval uppercase",
			text:   "TASK: code retrieval | query: foo",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "code",
			want:   "TASK: code retrieval | query: foo",
		},
		// Query with text starting with 'title:' should NOT bypass query prefixing
		{
			name:   "query text starting with title: must apply query prefix",
			text:   "title: War and Peace",
			cfg:    EmbedConfig{Purpose: PurposeQuery},
			preset: "search",
			want:   "task: search result | query: title: War and Peace",
		},
		// Document formatting
		{
			name:   "index with title",
			text:   "note body paragraph",
			cfg:    EmbedConfig{Purpose: PurposeIndex, Title: "Meeting Notes"},
			preset: "code", // preset has no effect on documents
			want:   "title: Meeting Notes | text: note body paragraph",
		},
		{
			name:   "index without title",
			text:   "note body paragraph",
			cfg:    EmbedConfig{Purpose: PurposeIndex},
			preset: "search",
			want:   "title: none | text: note body paragraph",
		},
		{
			name:   "default purpose without title",
			text:   "default text",
			cfg:    EmbedConfig{},
			preset: "",
			want:   "title: none | text: default text",
		},
		{
			name:   "default purpose with title",
			text:   "default text",
			cfg:    EmbedConfig{Title: "Design Doc"},
			preset: "",
			want:   "title: Design Doc | text: default text",
		},
		// Adversarial cases: document text starting with 'task:' or 'title:' must NOT bypass formatting
		{
			name:   "document starting with task: must be formatted into title and text envelope",
			text:   "task: deploy v2 to production checklist",
			cfg:    EmbedConfig{Purpose: PurposeIndex, Title: "Deployment"},
			preset: "search",
			want:   "title: Deployment | text: task: deploy v2 to production checklist",
		},
		{
			name:   "document starting with task: without title",
			text:   "task: buy groceries",
			cfg:    EmbedConfig{Purpose: PurposeIndex},
			preset: "search",
			want:   "title: none | text: task: buy groceries",
		},
		{
			name:   "document starting with title: without delimiter must NOT bypass",
			text:   "title: Standup Notes from Monday",
			cfg:    EmbedConfig{Purpose: PurposeIndex, Title: "Standup"},
			preset: "search",
			want:   "title: Standup | text: title: Standup Notes from Monday",
		},
		{
			name:   "document starting with title: note without delimiter formatted with title none",
			text:   "title: note",
			cfg:    EmbedConfig{Purpose: PurposeIndex},
			preset: "search",
			want:   "title: none | text: title: note",
		},
		{
			name:   "document starting with title: note without delimiter formatted with configured title",
			text:   "title: note",
			cfg:    EmbedConfig{Purpose: PurposeIndex, Title: "My Note"},
			preset: "search",
			want:   "title: My Note | text: title: note",
		},
		{
			name:   "document starting with Title: note without delimiter formatted with title none",
			text:   "Title: note",
			cfg:    EmbedConfig{Purpose: PurposeIndex},
			preset: "search",
			want:   "title: none | text: Title: note",
		},
		// Legitimate pre-formatted document chunks MUST bypass
		{
			name:   "already formatted document chunk bypasses",
			text:   "title: Standup | text: actual content",
			cfg:    EmbedConfig{Purpose: PurposeIndex, Title: "Ignored"},
			preset: "search",
			want:   "title: Standup | text: actual content",
		},
		{
			name:   "already formatted document chunk with capital Title bypasses",
			text:   "Title: Standup | text: actual content",
			cfg:    EmbedConfig{Purpose: PurposeIndex, Title: "Ignored"},
			preset: "search",
			want:   "Title: Standup | text: actual content",
		},
		{
			name:   "already formatted document chunk with leading whitespace bypasses",
			text:   "  title: Standup | text: actual content",
			cfg:    EmbedConfig{Purpose: PurposeIndex},
			preset: "search",
			want:   "  title: Standup | text: actual content",
		},
		{
			name:   "already formatted document chunk with leading whitespace and capital Title bypasses",
			text:   "  Title: Meeting Notes | text: discussion points",
			cfg:    EmbedConfig{Purpose: PurposeIndex},
			preset: "code",
			want:   "  Title: Meeting Notes | text: discussion points",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatLlamaEmbedText(tt.text, tt.cfg, tt.preset)
			if got != tt.want {
				t.Errorf("formatLlamaEmbedText(%q, %v, %q) = %q, want %q", tt.text, tt.cfg, tt.preset, got, tt.want)
			}
		})
	}
}

func TestInitLlamaRegisters_TaskPreset(t *testing.T) {
	reg := NewRegistry()
	err := InitLlama(context.Background(), reg, LlamaConfig{}, AIConfig{
		EmbeddingModel:  "embeddinggemma-2",
		GenerationModel: "gemma4-e4b",
		Dimensions:      768,
		TaskPreset:      "code",
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := reg.Embedder(llamaProviderName)
	if err != nil {
		t.Fatalf("embedder not registered: %v", err)
	}
	le, ok := e.(*LlamaEmbedder)
	if !ok {
		t.Fatalf("expected *LlamaEmbedder, got %T", e)
	}
	if le.TaskPreset() != "code" {
		t.Errorf("taskPreset = %q, want 'code'", le.TaskPreset())
	}
}

func TestCapLlamaEmbedRunes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{
			name:     "empty string",
			input:    "",
			maxRunes: 10,
			want:     "",
		},
		{
			name:     "ascii under limit is no-op",
			input:    "hello world",
			maxRunes: 20,
			want:     "hello world",
		},
		{
			name:     "ascii at exact limit is no-op",
			input:    "hello",
			maxRunes: 5,
			want:     "hello",
		},
		{
			name:     "ascii over limit truncates to exact rune count",
			input:    "hello world",
			maxRunes: 5,
			want:     "hello",
		},
		{
			name:     "utf8 multibyte under limit is no-op",
			input:    "héllo 🌍 日本語",
			maxRunes: 11,
			want:     "héllo 🌍 日本語",
		},
		{
			name:     "utf8 multibyte splits cleanly on emoji boundary",
			input:    "héllo 🌍 日本語",
			maxRunes: 7,
			want:     "héllo 🌍",
		},
		{
			name:     "utf8 multibyte splits cleanly on CJK boundary",
			input:    "héllo 🌍 日本語",
			maxRunes: 9,
			want:     "héllo 🌍 日",
		},
		{
			name:     "long multibyte input capped at maxLlamaEmbedInputRunes",
			input:    strings.Repeat("é🌍日", 2000), // 6000 runes
			maxRunes: maxLlamaEmbedInputRunes,
			want:     strings.Repeat("é🌍日", 1500), // 4500 runes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := capLlamaEmbedRunes(tt.input, tt.maxRunes)
			if got != tt.want {
				t.Errorf("capLlamaEmbedRunes(%q, %d) = %q, want %q", tt.input, tt.maxRunes, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8: %q", got)
			}
			if tt.maxRunes > 0 && utf8.RuneCountInString(got) > tt.maxRunes {
				t.Errorf("result rune count = %d > max %d", utf8.RuneCountInString(got), tt.maxRunes)
			}
		})
	}
}

func TestPartitionLlamaEmbedBatches(t *testing.T) {
	assertContiguousAndBounded := func(t *testing.T, items []string, batches [][2]int, maxItems, maxRunes int) {
		t.Helper()
		if len(items) == 0 {
			if len(batches) != 0 {
				t.Fatalf("empty input produced %v, want nil/empty", batches)
			}
			return
		}
		if len(batches) == 0 {
			t.Fatalf("non-empty input produced no batches")
		}
		prevEnd := 0
		for bi, b := range batches {
			start, end := b[0], b[1]
			if start != prevEnd {
				t.Fatalf("batch[%d] start = %d, want %d (contiguous)", bi, start, prevEnd)
			}
			if end <= start || end > len(items) {
				t.Fatalf("batch[%d] invalid range [%d,%d) for len %d", bi, start, end, len(items))
			}
			count := end - start
			if count > maxItems {
				t.Errorf("batch[%d] count = %d > maxItems %d", bi, count, maxItems)
			}
			totalRunes := 0
			for i := start; i < end; i++ {
				totalRunes += utf8.RuneCountInString(items[i])
			}
			if count > 1 && totalRunes > maxRunes {
				t.Errorf("batch[%d] total runes = %d > maxRunes %d", bi, totalRunes, maxRunes)
			}
			prevEnd = end
		}
		if prevEnd != len(items) {
			t.Fatalf("batches ended at %d, want %d", prevEnd, len(items))
		}
	}

	if got := partitionLlamaEmbedBatches(nil, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes); got != nil {
		t.Fatalf("nil input = %v, want nil", got)
	}
	if got := partitionLlamaEmbedBatches([]string{}, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes); got != nil {
		t.Fatalf("empty slice = %v, want nil", got)
	}

	// Item count cap: 20 short items split into <= 8 per batch: [0,8), [8,16), [16,20).
	short20 := make([]string, 20)
	for i := range short20 {
		short20[i] = fmt.Sprintf("short chunk %d", i)
	}
	shortBatches := partitionLlamaEmbedBatches(short20, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes)
	assertContiguousAndBounded(t, short20, shortBatches, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes)
	wantShort := [][2]int{{0, 8}, {8, 16}, {16, 20}}
	if !slices.Equal(shortBatches, wantShort) {
		t.Errorf("short20 batches = %v, want %v", shortBatches, wantShort)
	}

	// 20 x 1000-rune items partition into 5 batches of 4 (4000 <= 4500, 5000 > 4500).
	// Use 3-byte UTF-8 runes to confirm rune counting rather than byte counting.
	items20x1000 := make([]string, 20)
	for i := range items20x1000 {
		items20x1000[i] = strings.Repeat("日", 1000)
	}
	batches20 := partitionLlamaEmbedBatches(items20x1000, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes)
	assertContiguousAndBounded(t, items20x1000, batches20, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes)
	want20 := [][2]int{{0, 4}, {4, 8}, {8, 12}, {12, 16}, {16, 20}}
	if !slices.Equal(batches20, want20) {
		t.Errorf("20x1000 batches = %v, want %v", batches20, want20)
	}

	// Single >= 4500-rune item always gets its own batch of 1 and makes forward progress.
	longAndShort := []string{
		"small leading",
		strings.Repeat("a", 4500),
		"small middle",
		strings.Repeat("b", 5000),
		"small trailing",
	}
	longBatches := partitionLlamaEmbedBatches(longAndShort, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes)
	assertContiguousAndBounded(t, longAndShort, longBatches, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes)
	wantLong := [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}}
	if !slices.Equal(longBatches, wantLong) {
		t.Errorf("longAndShort batches = %v, want %v", longBatches, wantLong)
	}
}
