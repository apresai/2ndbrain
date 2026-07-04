package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/ragctx"
	"github.com/apresai/2ndbrain/internal/retrieve"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

// AskResponse is the JSON envelope returned by `2nb ask --json`. Same
// rationale as SearchResponse: Swift and automation consumers need to
// see retrieval warnings alongside the answer. Previous consumers that
// decoded *ai.RAGResult directly must now read `.answer` and `.sources`
// out of the envelope.
type AskResponse struct {
	Mode     string   `json:"mode"` // "hybrid" or "keyword"
	Warnings []string `json:"warnings,omitempty"`
	Answer   string   `json:"answer"`
	Sources  []string `json:"sources"`
	// RewrittenQuery is the standalone retrieval query the conversation
	// history condensed the question into. Present only on multi-turn asks
	// where the rewrite differs from the question; additive, so existing
	// envelope consumers are unaffected.
	RewrittenQuery string `json:"rewritten_query,omitempty"`
	// Token usage for the observatory: generation tokens (provider-actual when
	// reported, else a chars/4 estimate) plus the query-embedding tokens.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

var askHistory string

var askCmd = &cobra.Command{
	Use:   "ask <question>",
	Short: "Ask a question about your knowledge base (RAG)",
	Long: `Uses hybrid search to find relevant documents, then generates an answer using the configured AI provider. Requires ` + "`2nb ai setup`" + ` first.

For multi-turn conversations, pass the prior turns with --history: a JSON
array of {"role": "user"|"assistant", "content": "..."} objects, read from a
file or from stdin with '-'. Follow-up questions are rewritten into
standalone retrieval queries using the history (the rewrite is reported as
rewritten_query in --json output).`,
	Example: `  2nb ask "how does auth work?"
  2nb ask "what did we decide about the database?"
  2nb ask "summarize runbook:deploy-rotation"
  printf '[{"role":"user","content":"tell me about auth"},{"role":"assistant","content":"..."}]' | 2nb ask --history - "who owns it?"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAsk,
}

func init() {
	askCmd.Flags().StringVar(&askHistory, "history", "", "Conversation history: JSON array of {role, content}; '-' reads stdin, otherwise a file path")
	askCmd.GroupID = "ai"
	rootCmd.AddCommand(askCmd)
}

// maxHistoryInputBytes bounds the --history payload; a capped conversation
// serializes to a few KB, so anything near a megabyte is a caller bug.
const maxHistoryInputBytes = 1 << 20

// parseHistoryJSON decodes and validates a --history payload. Pure function
// over bytes so tests don't need stdin plumbing.
func parseHistoryJSON(data []byte) ([]ai.ChatTurn, error) {
	if len(data) >= maxHistoryInputBytes {
		return nil, fmt.Errorf("--history payload exceeds %d bytes; trim the conversation before passing it", maxHistoryInputBytes)
	}
	var turns []ai.ChatTurn
	if err := json.Unmarshal(data, &turns); err != nil {
		return nil, fmt.Errorf("--history must be a JSON array of {role, content} objects: %w", err)
	}
	for i, t := range turns {
		if t.Role != "user" && t.Role != "assistant" {
			return nil, fmt.Errorf("--history turn %d has role %q; valid roles: user, assistant", i, t.Role)
		}
	}
	return turns, nil
}

// loadHistoryArg resolves the --history flag value: "-" reads stdin (refusing
// a terminal so the command can never sit blocked waiting for an EOF that
// will not come), anything else is a file path.
func loadHistoryArg(arg string) ([]ai.ChatTurn, error) {
	if arg == "" {
		return nil, nil
	}
	var data []byte
	var err error
	if arg == "-" {
		if fi, statErr := os.Stdin.Stat(); statErr == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return nil, fmt.Errorf("--history - expects JSON on stdin, but stdin is a terminal\n\nPipe the history in:  printf '[...]' | 2nb ask --history - \"question\"")
		}
		data, err = io.ReadAll(io.LimitReader(os.Stdin, maxHistoryInputBytes))
	} else {
		data, err = os.ReadFile(arg)
	}
	if err != nil {
		return nil, fmt.Errorf("read --history: %w", err)
	}
	return parseHistoryJSON(data)
}

func runAsk(cmd *cobra.Command, args []string) (err error) {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	// Record the ask (best-effort) on every return path. result_count is the
	// number of cited source notes; mode is "hybrid" or "keyword".
	startTime := time.Now()
	var resp AskResponse
	defer func() {
		recordMetric(v, metrics.Operation{
			Operation:    metrics.OpAsk,
			DurationMs:   time.Since(startTime).Milliseconds(),
			OK:           err == nil,
			Error:        errString(err),
			ResultCount:  len(resp.Sources),
			Mode:         resp.Mode,
			InputTokens:  resp.InputTokens,
			OutputTokens: resp.OutputTokens,
		})
	}()

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI
	question := strings.Join(args, " ")

	history, err := loadHistoryArg(askHistory)
	if err != nil {
		return err
	}
	// Log the question but only the history size: vault sidecar logs should
	// not accumulate full conversation transcripts.
	slog.Info("ask", "question", question, "history_turns", len(history))
	slog.Debug("ask history", "history", history)

	// Check generator availability
	generator, err := ai.DefaultRegistry.Generator(cfg.Provider)
	if err != nil {
		return fmt.Errorf("no generation provider: %w\nRun `2nb ai status` to check provider configuration", err)
	}
	if !generator.Available(ctx) {
		return fmt.Errorf("generation provider %q not available", cfg.Provider)
	}

	resp, err = askOnce(ctx, v, generator, question, history)
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	if format == output.FormatJSON {
		return output.Write(os.Stdout, format, resp)
	}
	if format != "" {
		return output.Write(os.Stdout, format, &ai.RAGResult{Answer: resp.Answer, Sources: resp.Sources})
	}

	fmt.Println(resp.Answer)
	if len(resp.Sources) > 0 && !flagPorcelain {
		fmt.Fprintf(os.Stderr, "\nSources: %s\n", strings.Join(resp.Sources, ", "))
	}
	return nil
}

// askOnce runs one full ask turn: condense the question against the history
// (when present), retrieve context with the standalone query, fall back to
// the raw question if the rewrite retrieves nothing, then generate with the
// original question plus history. Shared by `2nb ask` and the `2nb chat`
// REPL so the two surfaces cannot drift.
func askOnce(ctx context.Context, v *vault.Vault, generator ai.GenerationProvider, question string, history []ai.ChatTurn) (AskResponse, error) {
	cfg := v.Config.AI
	var warnings []string
	addWarning := func(msg string) {
		fmt.Fprintln(os.Stderr, "  "+msg)
		warnings = append(warnings, msg)
	}

	// History-aware retrieval: rewrite a follow-up ("who owns it?") into a
	// standalone query before searching. Condense failure is never fatal;
	// the raw question still retrieves, just without coreference resolution.
	retrievalQuery := question
	if len(history) > 0 {
		rewritten, err := ai.CondenseQuestion(ctx, generator, history, question)
		if err != nil {
			slog.Warn("ask condense failed, using raw question", "err", err)
			addWarning(fmt.Sprintf("question rewrite failed, searching with the question as asked: %v", err))
		} else {
			retrievalQuery = rewritten
		}
	}
	if retrievalQuery != question && !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Interpreting as: %s\n", retrievalQuery)
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Searching for relevant context...\n")
	}

	// One shared retriever for the turn: the vector-readiness gate and the
	// embedding-corpus load run once and are reused across the (up to two)
	// retrieval passes below (rewrite, then raw-question fallback). Each pass
	// degrades to BM25 with a warning on any compat/embed/hybrid failure.
	r := retrieve.New(v)
	runRetrieve := func(query string) ([]search.Result, bool, error) {
		res, err := r.Retrieve(ctx, retrieve.Options{
			Query: query,
			Limit: ai.DefaultRAGCandidateDocs,
		})
		if err != nil {
			return nil, false, err
		}
		for _, w := range res.Warnings {
			addWarning(w)
		}
		return res.Results, res.Mode == search.ModeHybrid, nil
	}

	results, usedHybrid, err := runRetrieve(retrievalQuery)
	if err != nil {
		return AskResponse{}, fmt.Errorf("search: %w", err)
	}
	// A condensed rewrite can occasionally miss (wrong expansion of a vague
	// follow-up). Before failing the turn, retry with the question as asked.
	if len(results) == 0 && retrievalQuery != question {
		slog.Warn("ask rewritten query matched nothing, retrying with raw question", "rewritten", retrievalQuery)
		addWarning("rewritten query matched nothing; retrying with the question as asked")
		results, usedHybrid, err = runRetrieve(question)
		if err != nil {
			return AskResponse{}, fmt.Errorf("search: %w", err)
		}
	}

	if len(results) == 0 {
		return AskResponse{}, fmt.Errorf("no relevant documents found for %q\n\nTo fix:\n  • Add documents to your vault: 2nb create \"My Note\"\n  • Rebuild the search index: 2nb index\n  • Check what's indexed: 2nb list", question)
	}

	// Build parent-document RAG context: feed each unique source note's FULL
	// body — windowed around the matched section only when it exceeds the budget
	// — so an answer deep in a long note isn't head-truncated away.
	chunks, ctxWarnings := ragctx.Build(results, v.Root, ragctx.Budget{
		TotalRunes: cfg.ResolveRAGContextBudget(),
		NoteRunes:  cfg.ResolveRAGNoteBudget(),
	})
	for _, w := range ctxWarnings {
		slog.Warn("ask context", "warn", w)
		addWarning(w)
	}
	if len(chunks) == 0 {
		return AskResponse{}, fmt.Errorf("failed to build RAG context from %d search result(s); see warnings for unreadable sources", len(results))
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Found %d relevant chunks. Generating answer...\n", len(chunks))
	}

	result, err := ai.RAGWithHistory(ctx, generator, question, history, chunks)
	if err != nil {
		slog.Error("RAG failed", "question", question, "err", err)
		return AskResponse{}, fmt.Errorf("RAG failed: %w", err)
	}

	slog.Info("ask complete", "question", question, "sources", len(result.Sources))

	mode := "hybrid"
	if !usedHybrid {
		mode = "keyword"
	}

	// Token usage: prefer the provider's actual generation usage (Bedrock reports
	// it); estimate at chars/4 when it isn't reported. Add the query-embedding
	// tokens (the retrieval embed call) to the input side.
	inTokens, outTokens := result.InputTokens, result.OutputTokens
	if inTokens == 0 && outTokens == 0 {
		var ctxChars int
		for _, c := range chunks {
			ctxChars += len(c.Content)
		}
		inTokens = (ctxChars + len(question)) / 4
		outTokens = len(result.Answer) / 4
	}
	inTokens += len(retrievalQuery) / 4

	resp := AskResponse{
		Mode:         mode,
		Warnings:     warnings,
		Answer:       result.Answer,
		Sources:      result.Sources,
		InputTokens:  inTokens,
		OutputTokens: outTokens,
	}
	if retrievalQuery != question {
		resp.RewrittenQuery = retrievalQuery
	}
	return resp, nil
}
