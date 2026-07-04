// Package eval holds credential-gated retrieval-quality measurements used to
// justify embedding changes (e.g. Nova's asymmetric GENERIC_INDEX vs
// GENERIC_RETRIEVAL purpose) with real before/after numbers rather than a
// hunch. It reads a real vault's stored embeddings (no API) and issues real
// provider embedding calls for the query side, so callers gate it on
// credentials per the no-mock test policy.
package eval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/store"
)

// PurposeReport holds title-as-query retrieval metrics for one embedding purpose.
type PurposeReport struct {
	Purpose   string
	N         int
	K         int
	MRRAtK    float64
	RecallAt1 float64
	RecallAtK float64
	// TrueP* / TrueMean: the cosine of each title-query to its OWN document
	// embedding — where real matches land. ai.similarity_threshold must sit
	// below this, so a downward shift here forces the threshold down too.
	TrueP50  float64
	TrueP90  float64
	TrueP95  float64
	TrueMean float64
	// NegP*: the cosine of each title-query to OTHER documents — the negatives.
	// The similarity threshold should sit at roughly the high percentile of
	// these (what `models calibrate` approximates), so it admits real matches
	// while rejecting noise.
	NegP90 float64
	NegP95 float64
	NegP99 float64
	// SuggestedThreshold is a data-driven cut: max(negP95 + margin) clamped just
	// under the true-match p50, rounded to 2dp. Advisory — a starting point for
	// `config set ai.similarity_threshold`, not an automatic write.
	SuggestedThreshold float64
}

// AsymmetricPurpose measures title-as-query retrieval over a vault's stored
// document embeddings (produced with GENERIC_INDEX). Each titled document's
// title is used as a query, embedded once per purpose (PurposeIndex = the old
// symmetric behavior, PurposeQuery = Nova's GENERIC_RETRIEVAL, PurposeQueryText
// = Nova's TEXT_RETRIEVAL for a text-only store), and the corpus is ranked by
// cosine. It returns one report per purpose so the effect on MRR/Recall and on
// the cosine distributions can be compared head to head.
//
// It issues 3*N real embedding calls (N titles x 3 purposes); callers gate it
// on credentials. The document corpus is read from the index (no API calls).
func AsymmetricPurpose(ctx context.Context, db *store.DB, emb ai.EmbeddingProvider, k int) (map[string]PurposeReport, error) {
	if k <= 0 {
		k = 10
	}
	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("load embeddings: %w", err)
	}
	titleByID, err := loadTitles(db)
	if err != nil {
		return nil, err
	}

	// Keep only documents with a non-empty title (the title IS the query, and
	// Nova rejects empty input) and a usable vector. Corpus and queries stay
	// aligned by index.
	var titles []string
	var corpus [][]float32
	for i, id := range ids {
		t := strings.TrimSpace(titleByID[id])
		if t == "" || len(vecs[i]) == 0 {
			continue
		}
		titles = append(titles, t)
		corpus = append(corpus, vecs[i])
	}
	if len(titles) < 2 {
		return nil, fmt.Errorf("need >= 2 titled, embedded documents, have %d", len(titles))
	}

	norms := make([]float64, len(corpus))
	for i, v := range corpus {
		norms[i] = l2(v)
	}

	out := make(map[string]PurposeReport, 3)
	for _, purpose := range []string{ai.PurposeIndex, ai.PurposeQuery, ai.PurposeQueryText} {
		qv, err := emb.Embed(ctx, titles, ai.WithPurpose(purpose))
		if err != nil {
			return nil, fmt.Errorf("embed query side (%s): %w", purpose, err)
		}
		if len(qv) != len(titles) {
			return nil, fmt.Errorf("provider returned %d vectors for %d titles (%s)", len(qv), len(titles), purpose)
		}
		out[purpose] = scorePurpose(purpose, corpus, norms, qv, k)
	}
	return out, nil
}

func scorePurpose(purpose string, docs [][]float32, docNorms []float64, queries [][]float32, k int) PurposeReport {
	n := len(queries)
	var sumRR, hit1, hitK float64
	trueCos := make([]float64, 0, n)
	neg := make([]float64, 0, n*(n-1))
	for i := range queries {
		qn := l2(queries[i])
		self := cosine(queries[i], docs[i], qn, docNorms[i])
		trueCos = append(trueCos, self)
		// rank of doc i = 1 + #docs scoring strictly higher than its own match.
		rank := 1
		for j := range docs {
			if j == i {
				continue
			}
			c := cosine(queries[i], docs[j], qn, docNorms[j])
			neg = append(neg, c)
			if c > self {
				rank++
			}
		}
		if rank == 1 {
			hit1++
		}
		if rank <= k {
			hitK++
			sumRR += 1.0 / float64(rank)
		}
	}
	sort.Float64s(trueCos)
	sort.Float64s(neg)
	r := PurposeReport{
		Purpose:   purpose,
		N:         n,
		K:         k,
		MRRAtK:    sumRR / float64(n),
		RecallAt1: hit1 / float64(n),
		RecallAtK: hitK / float64(n),
		TrueP50:   pct(trueCos, 0.50),
		TrueP90:   pct(trueCos, 0.90),
		TrueP95:   pct(trueCos, 0.95),
		TrueMean:  mean(trueCos),
		NegP90:    pct(neg, 0.90),
		NegP95:    pct(neg, 0.95),
		NegP99:    pct(neg, 0.99),
	}
	r.SuggestedThreshold = suggestThreshold(r.NegP95, r.TrueP50)
	return r
}

// suggestThreshold picks a similarity cut just above the negative p95 (admit
// most true matches, reject noise) but never above true-match p50 - 0.01 (which
// would drop half the real hits), rounded to 2dp for a clean config value. With
// the measured Nova asymmetric numbers (negP95≈0.229, trueP50≈0.344) this yields
// the shipped 0.25 builtin.
func suggestThreshold(negP95, trueP50 float64) float64 {
	thr := negP95 + 0.02
	if capThr := trueP50 - 0.01; thr > capThr {
		thr = capThr
	}
	return math.Round(thr*100) / 100
}

func loadTitles(db *store.DB) (map[string]string, error) {
	rows, err := db.Conn().Query(`SELECT id, title FROM documents WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("load titles: %w", err)
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		m[id] = title
	}
	return m, rows.Err()
}

func l2(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func cosine(a, b []float32, na, nb float64) float64 {
	if na == 0 || nb == 0 {
		return 0
	}
	m := len(a)
	if len(b) < m {
		m = len(b)
	}
	var dot float64
	for i := 0; i < m; i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot / (na * nb)
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// pct returns the nearest-rank percentile p (0..1) of an already-sorted slice.
func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
