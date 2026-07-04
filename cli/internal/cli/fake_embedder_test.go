package cli

import (
	"context"

	"github.com/apresai/2ndbrain/internal/ai"
)

// fakeEmbedder is a local interface fake shared by the cli package's tests
// (config doctor, portability). It is NOT a provider mock — the CLAUDE.md
// "no mocks" policy forbids stubbing real provider integrations
// (Bedrock/Ollama/OpenRouter), but does not forbid a tiny test-local fake that
// implements an interface purely for testing logic independent of the actual
// provider.
type fakeEmbedder struct {
	name      string
	dims      int
	available bool
}

func (f *fakeEmbedder) Name() string                       { return f.name }
func (f *fakeEmbedder) Dimensions() int                    { return f.dims }
func (f *fakeEmbedder) Available(ctx context.Context) bool { return f.available }
func (f *fakeEmbedder) Embed(ctx context.Context, texts []string, _ ...ai.EmbedOption) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = make([]float32, f.dims)
	}
	return vecs, nil
}
func (f *fakeEmbedder) ListModels(ctx context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}

var _ ai.EmbeddingProvider = (*fakeEmbedder)(nil)
