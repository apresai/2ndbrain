package store

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func refCosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	d := math.Sqrt(na) * math.Sqrt(nb)
	if d == 0 {
		return 0
	}
	return dot / d
}

func TestVecChunks_KNNMatchesBruteForce(t *testing.T) {
	const dim = 16
	db := openTestDB(t)
	if err := db.EnsureVecChunks(dim); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	r := rand.New(rand.NewSource(1))
	const n = 200
	ids := make([]string, n)
	vecs := make([][]float32, n)
	batch := make([]ChunkVector, n)
	for i := 0; i < n; i++ {
		v := make([]float32, dim)
		for j := range v {
			v[j] = r.Float32()*2 - 1
		}
		ids[i] = "chunk-" + string(rune('a'+i%26)) + "-" + itoa(i)
		vecs[i] = v
		batch[i] = ChunkVector{ChunkID: ids[i], DocID: "doc-" + itoa(i/5), ContentHash: "h" + itoa(i), Vector: v}
	}
	if err := db.SetDocChunkVectors("doc-bulk", batch, "test-model"); err != nil {
		t.Fatalf("set vectors: %v", err)
	}

	// Query vector
	q := make([]float32, dim)
	for j := range q {
		q[j] = r.Float32()*2 - 1
	}

	// vec0 KNN top-10
	got, err := db.VecSearchChunks(q, 10, 0)
	if err != nil {
		t.Fatalf("vec search: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("got %d hits, want 10", len(got))
	}

	// Brute-force reference top-10 by cosine.
	type sc struct {
		id  string
		cos float64
	}
	ref := make([]sc, n)
	for i := range ids {
		ref[i] = sc{ids[i], refCosine(q, vecs[i])}
	}
	sort.Slice(ref, func(i, j int) bool { return ref[i].cos > ref[j].cos })

	for i := 0; i < 10; i++ {
		if got[i].ChunkID != ref[i].id {
			t.Errorf("rank %d: vec0=%s (cos %.4f) != bruteforce=%s (cos %.4f)",
				i, got[i].ChunkID, got[i].Score, ref[i].id, ref[i].cos)
		}
		if math.Abs(got[i].Score-ref[i].cos) > 1e-3 {
			t.Errorf("rank %d cosine: vec0=%.5f bruteforce=%.5f", i, got[i].Score, ref[i].cos)
		}
	}
}

func TestVecChunks_DimRebuildAndDelete(t *testing.T) {
	db := openTestDB(t)

	// Absent table -> ErrVecUnavailable on search.
	if _, err := db.VecSearchChunks([]float32{1, 0}, 5, 0); err != ErrVecUnavailable {
		t.Fatalf("expected ErrVecUnavailable, got %v", err)
	}

	if err := db.EnsureVecChunks(4); err != nil {
		t.Fatal(err)
	}
	if d, _ := db.vecChunksDim(); d != 4 {
		t.Fatalf("dim = %d, want 4", d)
	}
	v := []ChunkVector{{ChunkID: "c1", DocID: "d1", ContentHash: "h1", Vector: []float32{1, 0, 0, 0}}}
	if err := db.SetDocChunkVectors("d1", v, "m"); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.ChunkVectorCount(); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}

	// Dimension change drops + recreates (clearing the old vectors).
	if err := db.EnsureVecChunks(8); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if d, _ := db.vecChunksDim(); d != 8 {
		t.Fatalf("after rebuild dim = %d, want 8", d)
	}
	if n, _ := db.ChunkVectorCount(); n != 0 {
		t.Fatalf("after rebuild count = %d, want 0 (table recreated)", n)
	}

	// Delete by doc.
	if err := db.EnsureVecChunks(4); err != nil {
		t.Fatal(err)
	}
	_ = db.SetDocChunkVectors("d1", v, "m")
	_ = db.SetDocChunkVectors("d2", []ChunkVector{{ChunkID: "c2", DocID: "d2", ContentHash: "h2", Vector: []float32{0, 1, 0, 0}}}, "m")
	if err := db.DeleteDocChunkVectors("d1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.ChunkVectorCount(); n != 1 {
		t.Fatalf("after delete d1, count = %d, want 1", n)
	}
}

func TestSetDocChunkVectors_DedupLastWins(t *testing.T) {
	const dim = 4
	db := openTestDB(t)
	if err := db.EnsureVecChunks(dim); err != nil {
		t.Fatal(err)
	}
	// Two chunk vectors collide on chunk_id — the duplicate-heading case, since
	// makeChunkID hashes only docID+headingPath. vec0's chunk_id PRIMARY KEY has
	// no UPSERT, so inserting both naively would UNIQUE-fail and wedge the doc's
	// embedding; SetDocChunkVectors must collapse them last-wins (mirroring the
	// chunks table's ON CONFLICT(id) DO UPDATE) instead.
	batch := []ChunkVector{
		{ChunkID: "dup", DocID: "d1", ContentHash: "h-a", Vector: []float32{1, 0, 0, 0}},
		{ChunkID: "dup", DocID: "d1", ContentHash: "h-b", Vector: []float32{0, 1, 0, 0}},
	}
	if err := db.SetDocChunkVectors("d1", batch, "m"); err != nil {
		t.Fatalf("duplicate chunk_id must not error: %v", err)
	}
	if n, _ := db.ChunkVectorCount(); n != 1 {
		t.Fatalf("count = %d, want 1 (collapsed)", n)
	}
	// Last occurrence wins: a query aligned with the SECOND vector should hit.
	got, err := db.VecSearchChunks([]float32{0, 1, 0, 0}, 5, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ChunkID != "dup" {
		t.Fatalf("got %+v, want one hit for chunk dup", got)
	}
	if got[0].Score < 0.99 {
		t.Fatalf("score = %.4f, want ~1.0 (last-wins vector stored)", got[0].Score)
	}
}

func TestSetDocChunkVectors_SkipsDegenerateVectors(t *testing.T) {
	const dim = 4
	db := openTestDB(t)
	if err := db.EnsureVecChunks(dim); err != nil {
		t.Fatal(err)
	}
	nan := float32(math.NaN())
	batch := []ChunkVector{
		{ChunkID: "ok", DocID: "d1", ContentHash: "h1", Vector: []float32{1, 0, 0, 0}},
		{ChunkID: "zero", DocID: "d1", ContentHash: "h2", Vector: []float32{0, 0, 0, 0}},
		{ChunkID: "nan", DocID: "d1", ContentHash: "h3", Vector: []float32{nan, 0, 0, 0}},
	}
	if err := db.SetDocChunkVectors("d1", batch, "m"); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Only the finite, non-zero vector is stored — a zero-norm/NaN vector makes
	// vec0 return a NULL cosine distance that aborts the whole KNN scan.
	if n, _ := db.ChunkVectorCount(); n != 1 {
		t.Fatalf("count = %d, want 1 (degenerate vectors skipped)", n)
	}
	got, err := db.VecSearchChunks([]float32{1, 0, 0, 0}, 5, 0)
	if err != nil {
		t.Fatalf("search must not error with a degenerate vector skipped: %v", err)
	}
	if len(got) != 1 || got[0].ChunkID != "ok" {
		t.Fatalf("got %+v, want one hit for chunk ok", got)
	}
}

func TestFiniteNonZero(t *testing.T) {
	cases := []struct {
		name string
		v    []float32
		want bool
	}{
		{"normal", []float32{0.1, -0.2, 0.3}, true},
		{"empty", nil, false},
		{"all zero", []float32{0, 0, 0}, false},
		{"has nan", []float32{1, float32(math.NaN()), 0}, false},
		{"has +inf", []float32{1, float32(math.Inf(1)), 0}, false},
		{"has -inf", []float32{float32(math.Inf(-1)), 1}, false},
		{"single non-zero", []float32{0, 0, 0.0001}, true},
	}
	for _, tc := range cases {
		if got := FiniteNonZero(tc.v); got != tc.want {
			t.Errorf("%s: FiniteNonZero(%v) = %v, want %v", tc.name, tc.v, got, tc.want)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
