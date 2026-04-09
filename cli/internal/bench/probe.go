package bench

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/search"
)

// ProbeOpts carries the dependencies each probe needs.
type ProbeOpts struct {
	Ctx       context.Context
	AICfg     ai.AIConfig
	Provider  string
	ModelID   string
	ModelType string // "embedding" or "generation"
	SearchDB  *sql.DB
	VaultRoot string
}

// ProbeResult is the outcome of a single probe execution.
type ProbeResult struct {
	Probe     string `json:"probe"`
	LatencyMs int64  `json:"latency_ms"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
}

const (
	embedText    = "The quick brown fox jumps over the lazy dog. This is a benchmark embedding probe for 2ndbrain knowledge base."
	genPrompt    = "Summarize the purpose of a personal knowledge base in exactly two sentences."
	searchQuery  = "knowledge management best practices"
	ragQuestion  = "What are the main topics covered in this knowledge base?"
)

// RunAll runs the appropriate probes based on model type.
func RunAll(opts ProbeOpts) []ProbeResult {
	if opts.ModelType == "embedding" {
		return []ProbeResult{RunEmbed(opts)}
	}
	return []ProbeResult{
		RunGenerate(opts),
		RunSearch(opts),
		RunRAG(opts),
	}
}

// RunEmbed benchmarks embedding latency.
func RunEmbed(opts ProbeOpts) ProbeResult {
	start := time.Now()
	var dims int

	err := func() error {
		switch opts.Provider {
		case "bedrock":
			e, err := ai.NewBedrockEmbedder(opts.Ctx, opts.AICfg.Bedrock, opts.ModelID, opts.AICfg.Dimensions)
			if err != nil {
				return err
			}
			vecs, err := e.Embed(opts.Ctx, []string{embedText})
			if err != nil {
				return err
			}
			if len(vecs) == 0 || len(vecs[0]) == 0 {
				return fmt.Errorf("empty embedding")
			}
			dims = len(vecs[0])
			return nil

		case "openrouter":
			key, err := ai.GetAPIKey("openrouter")
			if err != nil {
				return err
			}
			e := ai.NewOpenRouterEmbedder(key, opts.ModelID, opts.AICfg.Dimensions)
			vecs, err := e.Embed(opts.Ctx, []string{embedText})
			if err != nil {
				return err
			}
			if len(vecs) == 0 || len(vecs[0]) == 0 {
				return fmt.Errorf("empty embedding")
			}
			dims = len(vecs[0])
			return nil

		case "ollama":
			endpoint := opts.AICfg.Ollama.Endpoint
			if endpoint == "" {
				endpoint = "http://localhost:11434"
			}
			e := ai.NewOllamaEmbedder(endpoint, opts.ModelID, opts.AICfg.Dimensions)
			vecs, err := e.Embed(opts.Ctx, []string{embedText})
			if err != nil {
				return err
			}
			if len(vecs) == 0 || len(vecs[0]) == 0 {
				return fmt.Errorf("empty embedding")
			}
			dims = len(vecs[0])
			return nil

		default:
			return fmt.Errorf("unknown provider %q", opts.Provider)
		}
	}()

	ms := time.Since(start).Milliseconds()
	if err != nil {
		return ProbeResult{Probe: "embed", LatencyMs: ms, OK: false, Detail: err.Error()}
	}
	return ProbeResult{Probe: "embed", LatencyMs: ms, OK: true, Detail: fmt.Sprintf("dims=%d", dims)}
}

// RunGenerate benchmarks generation latency.
func RunGenerate(opts ProbeOpts) ProbeResult {
	start := time.Now()
	genOpts := ai.GenOpts{MaxTokens: 128, Temperature: 0}

	resp, err := func() (string, error) {
		switch opts.Provider {
		case "bedrock":
			g, err := ai.NewBedrockGenerator(opts.Ctx, opts.AICfg.Bedrock, opts.ModelID)
			if err != nil {
				return "", err
			}
			return g.Generate(opts.Ctx, genPrompt, genOpts)

		case "openrouter":
			key, err := ai.GetAPIKey("openrouter")
			if err != nil {
				return "", err
			}
			g := ai.NewOpenRouterGenerator(key, opts.ModelID)
			return g.Generate(opts.Ctx, genPrompt, genOpts)

		case "ollama":
			endpoint := opts.AICfg.Ollama.Endpoint
			if endpoint == "" {
				endpoint = "http://localhost:11434"
			}
			g := ai.NewOllamaGenerator(endpoint, opts.ModelID)
			return g.Generate(opts.Ctx, genPrompt, genOpts)

		default:
			return "", fmt.Errorf("unknown provider %q", opts.Provider)
		}
	}()

	ms := time.Since(start).Milliseconds()
	if err != nil {
		return ProbeResult{Probe: "generate", LatencyMs: ms, OK: false, Detail: err.Error()}
	}
	detail := resp
	if len(detail) > 120 {
		detail = detail[:120] + "..."
	}
	return ProbeResult{Probe: "generate", LatencyMs: ms, OK: true, Detail: detail}
}

// RunSearch benchmarks BM25 search latency on the vault.
func RunSearch(opts ProbeOpts) ProbeResult {
	if opts.SearchDB == nil {
		return ProbeResult{Probe: "search", OK: false, Detail: "no search database"}
	}
	engine := search.NewEngine(opts.SearchDB)
	start := time.Now()
	results, err := engine.Search(search.Options{Query: searchQuery, Limit: 10})
	ms := time.Since(start).Milliseconds()

	if err != nil {
		return ProbeResult{Probe: "search", LatencyMs: ms, OK: false, Detail: err.Error()}
	}
	return ProbeResult{Probe: "search", LatencyMs: ms, OK: true, Detail: fmt.Sprintf("results=%d", len(results))}
}

// RunRAG benchmarks the full RAG pipeline: search → read files → generate answer.
func RunRAG(opts ProbeOpts) ProbeResult {
	if opts.SearchDB == nil {
		return ProbeResult{Probe: "rag", OK: false, Detail: "no search database"}
	}

	start := time.Now()

	// Search
	engine := search.NewEngine(opts.SearchDB)
	results, err := engine.Search(search.Options{Query: ragQuestion, Limit: 5})
	if err != nil {
		return ProbeResult{Probe: "rag", LatencyMs: time.Since(start).Milliseconds(), OK: false, Detail: err.Error()}
	}
	if len(results) == 0 {
		return ProbeResult{Probe: "rag", LatencyMs: time.Since(start).Milliseconds(), OK: false, Detail: "vault empty, cannot run RAG probe"}
	}

	// Read files and build chunks (mirrors ask.go)
	var chunks []ai.RAGChunk
	seen := make(map[string]bool)
	for _, r := range results {
		if r.Path == "" || seen[r.Path] {
			continue
		}
		seen[r.Path] = true
		content, err := os.ReadFile(filepath.Join(opts.VaultRoot, r.Path))
		if err != nil {
			continue
		}
		runes := []rune(string(content))
		if len(runes) > 2000 {
			runes = runes[:2000]
		}
		text := string(runes)
		if len(runes) == 2000 {
			text += "..."
		}
		chunks = append(chunks, ai.RAGChunk{Title: r.Title, Path: r.Path, Content: text})
	}

	// Generate
	var generator ai.GenerationProvider
	switch opts.Provider {
	case "bedrock":
		g, err := ai.NewBedrockGenerator(opts.Ctx, opts.AICfg.Bedrock, opts.ModelID)
		if err != nil {
			return ProbeResult{Probe: "rag", LatencyMs: time.Since(start).Milliseconds(), OK: false, Detail: err.Error()}
		}
		generator = g
	case "openrouter":
		key, err := ai.GetAPIKey("openrouter")
		if err != nil {
			return ProbeResult{Probe: "rag", LatencyMs: time.Since(start).Milliseconds(), OK: false, Detail: err.Error()}
		}
		generator = ai.NewOpenRouterGenerator(key, opts.ModelID)
	case "ollama":
		endpoint := opts.AICfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		generator = ai.NewOllamaGenerator(endpoint, opts.ModelID)
	default:
		return ProbeResult{Probe: "rag", LatencyMs: time.Since(start).Milliseconds(), OK: false, Detail: fmt.Sprintf("unknown provider %q", opts.Provider)}
	}

	result, err := ai.RAG(opts.Ctx, generator, ragQuestion, chunks)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return ProbeResult{Probe: "rag", LatencyMs: ms, OK: false, Detail: err.Error()}
	}

	detail := result.Answer
	if len(detail) > 120 {
		detail = detail[:120] + "..."
	}
	detail += fmt.Sprintf(" [%d sources]", len(result.Sources))
	return ProbeResult{Probe: "rag", LatencyMs: ms, OK: true, Detail: detail}
}
