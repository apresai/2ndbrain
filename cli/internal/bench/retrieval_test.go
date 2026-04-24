package bench

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newRetrievalTestDB spins up an in-memory SQLite with just the tables
// the retrieval probe reads. We skip the real migrations to keep the
// test package free of a cycle with internal/store.
func newRetrievalTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := `
		CREATE TABLE documents (
			id TEXT PRIMARY KEY,
			embedding BLOB
		);
		CREATE TABLE links (
			source_id TEXT,
			target_id TEXT,
			resolved  INTEGER
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// encodeVec mirrors the on-disk format (little-endian float32).
func encodeVec(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// seedDocVec inserts a document with its embedding.
func seedDocVec(t *testing.T, db *sql.DB, id string, v []float32) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO documents (id, embedding) VALUES (?, ?)`, id, encodeVec(v)); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func seedResolvedLink(t *testing.T, db *sql.DB, src, tgt string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO links (source_id, target_id, resolved) VALUES (?, ?, 1)`, src, tgt); err != nil {
		t.Fatalf("link %s→%s: %v", src, tgt, err)
	}
}

// TestRetrievalQualityProbe_TooFewLinks is the silent-skip case from the
// phase-4 design: fewer than MinLinksForRetrievalProbe links → ErrTooFewLinks.
func TestRetrievalQualityProbe_TooFewLinks(t *testing.T) {
	db := newRetrievalTestDB(t)
	seedDocVec(t, db, "a", []float32{1, 0})
	seedDocVec(t, db, "b", []float32{0, 1})
	seedResolvedLink(t, db, "a", "b")

	_, err := RetrievalQualityProbe(db)
	if !errors.Is(err, ErrTooFewLinks) {
		t.Fatalf("expected ErrTooFewLinks, got %v", err)
	}
}

// TestRetrievalQualityProbe_PerfectScore places 20 docs on the unit
// circle and links each to its angular neighbor (the nearest-cosine
// pair). The probe should score very high, since for every linked
// pair the target truly is the closest semantic neighbor.
func TestRetrievalQualityProbe_PerfectScore(t *testing.T) {
	db := newRetrievalTestDB(t)

	const n = 20
	for i := 0; i < n; i++ {
		theta := 2.0 * math.Pi * float64(i) / float64(n)
		v := []float32{float32(math.Cos(theta)), float32(math.Sin(theta))}
		seedDocVec(t, db, docID(i), v)
	}
	// Link each doc to its neighbor on the circle — guaranteed to be the
	// maximum-cosine match (ignoring self).
	for i := 0; i < n-1; i++ {
		seedResolvedLink(t, db, docID(i), docID(i+1))
	}

	got, err := RetrievalQualityProbe(db)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	// On the circle each doc has two equidistant neighbors; the linked
	// one is ranked 1st or 2nd depending on tie-breaking. MRR ≥ 0.7
	// is the honest "linked neighbor is almost always in the top-2" bar.
	if got.ScoreMRR < 0.7 {
		t.Errorf("expected high MRR, got %v", got.ScoreMRR)
	}
	// Recall@K=10 should be 1.0 — there are only 20 docs total, so the
	// target is always within the top-10 semantic neighbors.
	if got.ScoreRecallAtK < 0.99 {
		t.Errorf("expected recall@K=1.0, got %v", got.ScoreRecallAtK)
	}
	if got.PairsUsed != n-1 {
		t.Errorf("pairs_used = %d, want %d", got.PairsUsed, n-1)
	}
}

// TestRetrievalQualityProbe_SkipsMissingEmbeddings covers the case
// where a link endpoint has no embedding — it should not crash and
// should not count that pair.
func TestRetrievalQualityProbe_SkipsMissingEmbeddings(t *testing.T) {
	db := newRetrievalTestDB(t)
	const n = 15
	for i := 0; i < n; i++ {
		theta := 2.0 * math.Pi * float64(i) / float64(n)
		v := []float32{float32(math.Cos(theta)), float32(math.Sin(theta))}
		seedDocVec(t, db, docID(i), v)
	}
	for i := 0; i < n-1; i++ {
		seedResolvedLink(t, db, docID(i), docID(i+1))
	}
	// A link pointing at a doc with no embedding: should be skipped.
	if _, err := db.Exec(`INSERT INTO documents (id, embedding) VALUES ('ghost', NULL)`); err != nil {
		t.Fatal(err)
	}
	seedResolvedLink(t, db, docID(0), "ghost")

	got, err := RetrievalQualityProbe(db)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	// Should use 14 pairs (15 links, minus the ghost). Don't depend on
	// exact MRR value — just that the ghost didn't break the probe.
	if got.PairsUsed != 14 {
		t.Errorf("pairs_used = %d, want 14", got.PairsUsed)
	}
}

func docID(i int) string {
	return "doc-" + string(rune('a'+i))
}
