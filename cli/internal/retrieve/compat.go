package retrieve

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// VectorCompat reports whether the vault's stored embeddings are usable by the
// given embedder right now. When not usable, it returns a single human-readable
// message suitable for stderr. A zero-embedding vault returns (false, "")
// because silent BM25 fallback is the correct UX there, not an error state.
//
// It is the single decision point for "can we run hybrid?", shared by every
// retrieval surface via Retriever.ensureReady (and called directly by the
// polish link-suggestion path). Check order matters: fail fast on the
// highest-signal problem first so the user sees one actionable line, not a
// chain of warnings.
func VectorCompat(ctx context.Context, v *vault.Vault, embedder ai.EmbeddingProvider) (ready bool, message string) {
	dim, err := v.DB.SampleEmbeddingDim()
	if err != nil {
		slog.Debug("SampleEmbeddingDim failed", "err", err)
		return false, ""
	}
	if dim == 0 {
		// No embeddings at all: hybrid search has nothing to work with.
		// Silent fallback; `ai status` is where the user learns about it.
		return false, ""
	}

	providerName := ""
	if v.Config != nil {
		providerName = v.Config.AI.Provider
	}

	if embedder == nil {
		if providerName == "" {
			return false, "semantic search disabled: no AI provider configured — run '2nb ai setup' to enable"
		}
		return false, fmt.Sprintf("semantic search disabled: embedder %q not registered", providerName)
	}

	if !embedder.Available(ctx) {
		return false, fmt.Sprintf("semantic search disabled: provider %q unavailable — falling back to keyword search", providerName)
	}

	// Mixed-DIMENSION vaults (e.g. a partial re-embed after a Matryoshka
	// dimension change: DocumentsNeedingEmbedding gates on content, not
	// dimension, so a bare reindex won't normalize widths) can't be cosine-
	// compared reliably. Checked BEFORE the single-sample provider comparison
	// so the accurate mixed-dim diagnosis wins: SampleEmbeddingDim returns one
	// arbitrary row, so the provider check below could otherwise mis-report a
	// mixed vault as a uniform provider mismatch depending on which row it hit.
	if dims, err := v.DB.DistinctEmbeddingDims(); err == nil && len(dims) > 1 {
		return false, fmt.Sprintf("semantic search disabled: vault mixes embedding dimensions %v (a partial re-embed) — run '2nb index --force-reembed' to normalize", dims)
	}

	if providerDim := embedder.Dimensions(); providerDim != dim {
		return false, fmt.Sprintf("semantic search disabled: vault was embedded with %dd vectors but current provider %q produces %dd — run '2nb index --force-reembed' or switch provider back to the one that built this vault", dim, providerName, providerDim)
	}

	// Mixed-model vaults still work as long as the dimensions match.
	// Existing DocumentsNeedingEmbedding auto-heals on the next index,
	// so we just log this at debug and let search proceed.
	if models, err := v.DB.DistinctEmbeddingModels(); err == nil && len(models) > 1 {
		slog.Debug("vault has mixed embedding models", "models", models)
	}

	return true, ""
}
