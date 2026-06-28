package store

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
)

// seedEmbeddings bulk-inserts n documents each carrying a dim-length
// embedding BLOB, in one transaction (fast setup, not timed).
func seedEmbeddings(b *testing.B, db *DB, n, dim int) {
	b.Helper()
	r := rand.New(rand.NewSource(7))
	tx, err := db.conn.Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO documents (id, path, embedding, embedding_model, embedding_hash) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		v := make([]float32, dim)
		for j := range v {
			v[j] = r.Float32()*2 - 1
		}
		if _, err := stmt.Exec(fmt.Sprintf("doc-%d", i), fmt.Sprintf("n/%d.md", i), float32sToBytes(v), "bench", "h"); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkAllEmbeddings measures the per-query load+decode cost every CLI
// vector search pays today with no cache: read every embedding BLOB from
// SQLite and decode it to []float32. The brute-force cosine scan itself is
// cheap (see search.BenchmarkVectorBruteForce); this load is the real
// production latency driver for one-shot CLI processes, and it is exactly
// what sqlite-vec eliminates by running the KNN scan in C against resident
// BLOBs. dim=1024 is the Amazon Nova-2 default.
//
//	go test -tags fts5 -bench=BenchmarkAllEmbeddings -benchmem ./internal/store/
func BenchmarkAllEmbeddings(b *testing.B) {
	const dim = 1024
	for _, n := range []int{1000, 10000, 50000, 100000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			db, err := Open(filepath.Join(b.TempDir(), "index.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			seedEmbeddings(b, db, n, dim)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ids, vecs, err := db.AllEmbeddings()
				if err != nil {
					b.Fatal(err)
				}
				if len(ids) != n || len(vecs) != n {
					b.Fatalf("loaded %d ids / %d vecs, want %d", len(ids), len(vecs), n)
				}
			}
		})
	}
}
