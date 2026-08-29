package bench

import (
	"context"
	"database/sql"
	"errors"
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
	Probe         string  `json:"probe"`
	LatencyMs     int64   `json:"latency_ms"`
	OK            bool    `json:"ok"`
	Skipped       bool    `json:"skipped,omitempty"`
	Detail        string  `json:"detail,omitempty"`
	QualityScore  float64 `json:"quality_score,omitempty"`
	VaultDocCount int     `json:"vault_doc_count,omitempty"`
}

const (
	embedText   = "The quick brown fox jumps over the lazy dog. This is a benchmark embedding probe for 2ndbrain knowledge base."
	genPrompt   = "Summarize the purpose of a personal knowledge base in exactly two sentences."
	searchQuery = "knowledge management best practices"
	ragQuestion = "What are the main topics covered in this knowledge base?"
)

// RunAll runs the appropriate probes based on model type.
func RunAll(opts ProbeOpts) []ProbeResult {
	if opts.ModelType == "embedding" {
		return []ProbeResult{RunEmbed(opts), RunRetrievalQuality(opts)}
	}
	return []ProbeResult{
		RunGenerate(opts),
		RunSearch(opts),
		RunRAG(opts),
	}
}

// RunRetrievalQuality benchmarks the current vault's semantic retrieval
// quality against resolved wikilinks. It does not call an AI provider; it
// scores the embeddings already stored in the index.
func RunRetrievalQuality(opts ProbeOpts) ProbeResult {
	start := time.Now()
	if opts.SearchDB == nil {
		return ProbeResult{Probe: "retrieval", OK: false, Detail: "no search database"}
	}
	result, err := RetrievalQualityProbe(opts.SearchDB)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		if errors.Is(err, ErrTooFewLinks) {
			return ProbeResult{
				Probe:         "retrieval",
				LatencyMs:     ms,
				OK:            false,
				Skipped:       true,
				Detail:        fmt.Sprintf("not enough linked docs: need at least %d resolved wikilinks, found %d usable pairs", MinLinksForRetrievalProbe, result.PairsUsed),
				VaultDocCount: result.Documents,
			}
		}
		return ProbeResult{Probe: "retrieval", LatencyMs: ms, OK: false, Detail: err.Error()}
	}
	return ProbeResult{
		Probe:         "retrieval",
		LatencyMs:     ms,
		OK:            true,
		Detail:        fmt.Sprintf("mrr@%d=%.3f recall@%d=%.3f pairs=%d", result.K, result.ScoreMRR, result.K, result.ScoreRecallAtK, result.PairsUsed),
		QualityScore:  result.ScoreMRR,
		VaultDocCount: result.Documents,
	}
}

// RunEmbed benchmarks embedding latency.
func RunEmbed(opts ProbeOpts) ProbeResult {
	start := time.Now()
	var dims int

	err := func() error {
		switch opts.Provider {
		case "bedrock":
			// Measure the endpoint the model is actually served from.
			// Constructing from opts.AICfg.Bedrock alone let the catalog's
			// row order decide the region, so a benchmark could describe a
			// different endpoint than the one serving queries.
			route := ai.ResolveMeasurementRoute(opts.AICfg, opts.ModelID, opts.VaultRoot)
			e, err := ai.NewBedrockEmbedder(opts.Ctx, ai.ApplyRouteRegion(opts.AICfg.Bedrock, route), opts.ModelID, opts.AICfg.Dimensions)
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
	// ReasoningEffort "none" for the same reason the models-test smoke probe
	// sets it: this probe measures generation LATENCY, and an always-reasoning
	// model (mantle grok, gpt-5.5) left at its model default bills reasoning
	// against the output budget, returning a reasoning-only "incomplete"
	// response or, observed live 2026-08-21 on xai.grok-4.6, running past the
	// mantle client's 90s HTTP timeout and failing the bench outright at
	// latency_ms=90008. Non-reasoning providers ignore the field — and so
	// does the CLASSIC Converse plane, which has no reasoning knob at all,
	// which is why the budget is ai.BenchGenMaxTokens rather than the old
	// 128: classic grok-4.6 bills ~180 reasoning tokens before any answer
	// text, so 128 failed a working model. Budgets bound runaway cost; they
	// must never fail a working model.
	genOpts := ai.GenOpts{MaxTokens: ai.BenchGenMaxTokens, SystemPrompt: "You are a helpful assistant. Be concise.", ReasoningEffort: "none"}

	resp, err := func() (string, error) {
		switch opts.Provider {
		case "bedrock":
			route := ai.ResolveMeasurementRoute(opts.AICfg, opts.ModelID, opts.VaultRoot)
			g, err := ai.NewBedrockGenerationForRoute(opts.Ctx, ai.ApplyRouteRegion(opts.AICfg.Bedrock, route), route, opts.VaultRoot)
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
			return ProbeResult{Probe: "rag", LatencyMs: time.Since(start).Milliseconds(), OK: false, Detail: fmt.Sprintf("read context %s: %v", r.Path, err)}
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
	if len(chunks) == 0 {
		return ProbeResult{Probe: "rag", LatencyMs: time.Since(start).Milliseconds(), OK: false, Detail: "no readable context sources"}
	}

	// Generate
	var generator ai.GenerationProvider
	switch opts.Provider {
	case "bedrock":
		route := ai.ResolveMeasurementRoute(opts.AICfg, opts.ModelID, opts.VaultRoot)
		g, err := ai.NewBedrockGenerationForRoute(opts.Ctx, ai.ApplyRouteRegion(opts.AICfg.Bedrock, route), route, opts.VaultRoot)
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

	// Effort "none" for the same latency-measurement rationale as
	// RunGenerate: the bench must not inherit an always-reasoning mantle
	// model's default and time out. Production ask keeps the user's
	// configured reasoning depth; only the bench pins it off.
	result, err := ai.RAG(opts.Ctx, generator, ragQuestion, chunks, ai.WithReasoningEffort("none"))
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
