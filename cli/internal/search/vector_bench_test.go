package search

import (
	"fmt"
	"math/rand"
	"testing"
)

// makeCorpus builds n deterministic pseudo-random vectors of the given
// dimension. The fixed seed keeps results comparable across runs/builds so
// a regression shows up as a latency delta, not seed noise.
func makeCorpus(n, dim int) ([]string, [][]float32) {
	r := rand.New(rand.NewSource(42))
	ids := make([]string, n)
	embs := make([][]float32, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("doc-%d", i)
		v := make([]float32, dim)
		for j := range v {
			v[j] = r.Float32()*2 - 1
		}
		embs[i] = v
	}
	return ids, embs
}

// BenchmarkVectorBruteForce measures the brute-force cosine scan
// (VectorSearchThreshold) across corpus sizes at the default embedding
// dimension (1024 = Amazon Nova-2). This is the baseline the sqlite-vec
// path is compared against, and the proof of where the per-query budget
// (reqs.md PERF-EV-002: <300ms for vaults up to 10k docs) is met or missed.
//
// minScore=0 forces scoring + sorting of every vector (the worst case), so
// the numbers are an upper bound on the scan cost, independent of any
// threshold pruning. Embeddings are stored per-document, so N here is the
// document count.
//
//	go test -tags fts5 -bench=BenchmarkVectorBruteForce -benchmem ./internal/search/
func BenchmarkVectorBruteForce(b *testing.B) {
	const dim = 1024
	_, qv := makeCorpus(1, dim)
	query := qv[0]

	for _, n := range []int{1000, 5000, 10000, 50000, 100000} {
		ids, embs := makeCorpus(n, dim)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = VectorSearchThreshold(query, ids, embs, 10, 0)
			}
		})
	}
}
