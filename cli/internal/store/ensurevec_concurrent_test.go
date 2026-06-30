package store

import (
	"path/filepath"
	"sync"
	"testing"
)

// TestEnsureVecChunksConcurrent fires N goroutines at EnsureVecChunks on a fresh
// DB, racing the lazy first-create of the vec_chunks vec0 table. Run under -race
// (and without the ensureVecMu guard) this reproduces the "table already exists"
// create race the concurrent embed pool exposed; with the mutex all N succeed.
func TestEnsureVecChunksConcurrent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = db.EnsureVecChunks(8)
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Errorf("goroutine %d: EnsureVecChunks errored: %v", i, e)
		}
	}
	if d, err := db.vecChunksDim(); err != nil || d != 8 {
		t.Errorf("vecChunksDim = %d (err %v), want 8", d, err)
	}
}
