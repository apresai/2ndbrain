package metrics

import (
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRecordAndRecent(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 3; i++ {
		if err := db.Record(Operation{Operation: OpSearch, DurationMs: int64(10 + i), OK: true, ResultCount: i}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	ops, err := db.Recent(0)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("recent = %d, want 3", len(ops))
	}
	// Newest first (highest rowid), and defaults applied.
	if ops[0].DurationMs != 12 {
		t.Errorf("newest duration = %d, want 12 (newest-first order)", ops[0].DurationMs)
	}
	if ops[0].Source != "cli" {
		t.Errorf("default source = %q, want cli", ops[0].Source)
	}
	if ops[0].Timestamp == "" {
		t.Errorf("timestamp should default to now, got empty")
	}
}

func TestLastByOp_PicksMostRecentBuildAndComputesRates(t *testing.T) {
	db := openTestDB(t)
	// Interleave a query op between two builds; LastByOp(build types) must skip it.
	mustRecord(t, db, Operation{Operation: OpIndex, DurationMs: 1000, DocsIndexed: 5, OK: true})
	mustRecord(t, db, Operation{Operation: OpSearch, DurationMs: 40, OK: true})
	mustRecord(t, db, Operation{Operation: OpReembed, DurationMs: 2000, DocsIndexed: 10, Embedded: 10, EmbedMs: 2000, OK: true})

	last, err := db.LastByOp(OpIndex, OpReembed)
	if err != nil {
		t.Fatalf("lastByOp: %v", err)
	}
	if last == nil {
		t.Fatal("lastByOp = nil, want the reembed row")
	}
	if last.Operation != OpReembed {
		t.Errorf("last build = %q, want reembed (most recent build)", last.Operation)
	}
	// 10 docs / 2s = 5 docs/sec; 10 embedded / 2s = 5 emb/sec.
	if last.DocsPerSec != 5 {
		t.Errorf("docs_per_sec = %v, want 5", last.DocsPerSec)
	}
	if last.EmbeddingsPerSec != 5 {
		t.Errorf("embeddings_per_sec = %v, want 5", last.EmbeddingsPerSec)
	}
}

func TestLastByOp_NoneReturnsNil(t *testing.T) {
	db := openTestDB(t)
	mustRecord(t, db, Operation{Operation: OpSearch, DurationMs: 10, OK: true})
	last, err := db.LastByOp(OpIndex, OpReembed)
	if err != nil {
		t.Fatalf("lastByOp: %v", err)
	}
	if last != nil {
		t.Errorf("lastByOp with no builds = %+v, want nil", last)
	}
}

// TestPrunePartitionedByType locks the key retention invariant: pruning one
// operation type to a cap never touches another type's rows.
func TestPrunePartitionedByType(t *testing.T) {
	db := openTestDB(t)
	mustRecord(t, db, Operation{Operation: OpIndex, DurationMs: 1000, DocsIndexed: 5, OK: true})
	mustRecord(t, db, Operation{Operation: OpIndex, DurationMs: 1100, DocsIndexed: 6, OK: true})
	for i := 0; i < 5; i++ {
		mustRecord(t, db, Operation{Operation: OpSearch, DurationMs: int64(i), OK: true})
	}
	if err := db.prune(OpSearch, 2); err != nil {
		t.Fatalf("prune: %v", err)
	}
	ops, _ := db.Recent(0)
	var nIndex, nSearch int
	for _, o := range ops {
		switch o.Operation {
		case OpIndex:
			nIndex++
		case OpSearch:
			nSearch++
		}
	}
	if nIndex != 2 {
		t.Errorf("index rows = %d, want 2 (untouched by a search prune)", nIndex)
	}
	if nSearch != 2 {
		t.Errorf("search rows = %d, want 2 (capped)", nSearch)
	}
	// The 2 surviving search rows must be the newest (durations 3 and 4).
	var maxSearch int64
	for _, o := range ops {
		if o.Operation == OpSearch && o.DurationMs > maxSearch {
			maxSearch = o.DurationMs
		}
	}
	if maxSearch != 4 {
		t.Errorf("newest surviving search duration = %d, want 4", maxSearch)
	}
}

func TestAggregates(t *testing.T) {
	db := openTestDB(t)
	// Two index builds: 5 docs/1s = 5/s, 20 docs/2s = 10/s → avg docs/sec 7.5.
	mustRecord(t, db, Operation{Operation: OpIndex, DurationMs: 1000, DocsIndexed: 5, OK: true})
	mustRecord(t, db, Operation{Operation: OpIndex, DurationMs: 2000, DocsIndexed: 20, OK: true})
	// Three searches: durations 30, 50, 70 → avg 50, p50 50.
	for _, d := range []int64{30, 50, 70} {
		mustRecord(t, db, Operation{Operation: OpSearch, DurationMs: d, OK: true})
	}
	agg, err := db.Aggregates()
	if err != nil {
		t.Fatalf("aggregates: %v", err)
	}
	idx, ok := agg[OpIndex]
	if !ok || idx.Count != 2 {
		t.Fatalf("index aggregate = %+v, want count 2", idx)
	}
	if idx.AvgDocsPerSec != 7.5 {
		t.Errorf("index avg_docs_per_sec = %v, want 7.5", idx.AvgDocsPerSec)
	}
	s := agg[OpSearch]
	if s.Count != 3 || s.AvgMs != 50 || s.P50Ms != 50 {
		t.Errorf("search aggregate = %+v, want count 3 / avg 50 / p50 50", s)
	}
}

func TestWithRates_ZeroDurationGuard(t *testing.T) {
	o := Operation{Operation: OpIndex, DurationMs: 0, DocsIndexed: 10}.WithRates()
	if o.DocsPerSec != 0 {
		t.Errorf("docs_per_sec with zero duration = %v, want 0 (no divide-by-zero)", o.DocsPerSec)
	}
}

// TestSchemaVersionIdempotentAcrossOpens is a regression test for the
// schema_version row growing one-per-open: Open runs on every recorded op, so
// the INSERT OR IGNORE must actually be ignored on subsequent opens (it is only
// once `version` is a PRIMARY KEY).
func TestSchemaVersionIdempotentAcrossOpens(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	for i := 0; i < 4; i++ {
		db, err := Open(p)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		db.Close()
	}
	db, err := Open(p)
	if err != nil {
		t.Fatalf("final open: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatalf("count schema_version: %v", err)
	}
	if n != 1 {
		t.Errorf("schema_version rows = %d after 5 opens, want 1 (INSERT OR IGNORE must be idempotent)", n)
	}
}

func TestClear(t *testing.T) {
	db := openTestDB(t)
	mustRecord(t, db, Operation{Operation: OpIndex, DurationMs: 100, DocsIndexed: 1, OK: true})
	mustRecord(t, db, Operation{Operation: OpSearch, DurationMs: 10, OK: true})
	n, err := db.Clear()
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n != 2 {
		t.Errorf("cleared = %d, want 2", n)
	}
	if ops, _ := db.Recent(0); len(ops) != 0 {
		t.Errorf("after clear: %d ops, want 0", len(ops))
	}
}

func TestAggregatesTokens(t *testing.T) {
	db := openTestDB(t)
	mustRecord(t, db, Operation{Operation: OpAsk, DurationMs: 100, OK: true, InputTokens: 100, OutputTokens: 50})
	mustRecord(t, db, Operation{Operation: OpAsk, DurationMs: 120, OK: true, InputTokens: 200, OutputTokens: 80})
	mustRecord(t, db, Operation{Operation: OpIndex, DurationMs: 1000, DocsIndexed: 5, OK: true, InputTokens: 25})

	agg, err := db.Aggregates()
	if err != nil {
		t.Fatalf("aggregates: %v", err)
	}
	if agg[OpAsk].TokensIn != 300 || agg[OpAsk].TokensOut != 130 {
		t.Errorf("ask tokens = %d/%d, want 300/130", agg[OpAsk].TokensIn, agg[OpAsk].TokensOut)
	}
	if agg[OpIndex].TokensIn != 25 || agg[OpIndex].TokensOut != 0 {
		t.Errorf("index tokens = %d/%d, want 25/0", agg[OpIndex].TokensIn, agg[OpIndex].TokensOut)
	}
}

func mustRecord(t *testing.T, db *DB, op Operation) {
	t.Helper()
	if err := db.Record(op); err != nil {
		t.Fatalf("record: %v", err)
	}
}

func TestUsageLedgerSurvivesOperationPrune(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 205; i++ {
		mustRecord(t, db, Operation{
			Timestamp:         "2026-11-17T10:00:00Z",
			Operation:         OpSearch,
			DurationMs:        15,
			OK:                true,
			EmbeddingProvider: "gemini",
			EmbeddingModel:    "gemini-embedding-2",
			EmbedInputTokens:  100,
			CostUSD:           0.00002,
			KnownPricing:      true,
			InputTokens:       100,
			UsageLegs: []UsageLeg{{
				Provider:     "gemini",
				Model:        "gemini-embedding-2",
				Slot:         "embedding",
				Operation:    OpSearch,
				IsCloud:      true,
				Calls:        1,
				InputTokens:  100,
				CostUSD:      0.00002,
				KnownPricing: true,
			}},
		})
	}
	ops, err := db.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != DefaultPerTypeCap {
		t.Fatalf("pruned operations = %d, want %d", len(ops), DefaultPerTypeCap)
	}
	rows, err := db.UsageEntries("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Calls != 205 || rows[0].InputTokens != 20500 {
		t.Fatalf("ledger after prune = %+v, want 1 row with 205 calls and 20500 input tokens", rows)
	}
	if wantUSD := 205 * 0.00002; rows[0].CostUSD < wantUSD-1e-9 || rows[0].CostUSD > wantUSD+1e-9 {
		t.Fatalf("ledger cost_usd = %v, want %v", rows[0].CostUSD, wantUSD)
	}
}

func TestUsageLedgerHybridMultiLegAndClear(t *testing.T) {
	db := openTestDB(t)
	mustRecord(t, db, Operation{
		Timestamp:          "2026-11-17T11:00:00Z",
		Operation:          OpAsk,
		DurationMs:         800,
		OK:                 true,
		EmbeddingProvider:  "llama-local",
		EmbeddingModel:     "embeddinggemma-2",
		GenerationProvider: "gemini",
		GenerationModel:    "gemini-4-hydrogen",
		EmbedInputTokens:   50,
		GenInputTokens:     4000,
		GenOutputTokens:    400,
		InputTokens:        4050,
		OutputTokens:       400,
		CostUSD:            0.009,
		KnownPricing:       true,
		UsageLegs: []UsageLeg{
			{
				Provider: "llama-local", Model: "embeddinggemma-2", Slot: "embedding", Operation: OpAsk,
				IsCloud: false, Calls: 1, InputTokens: 50, CostUSD: 0, KnownPricing: true,
			},
			{
				Provider: "gemini", Model: "gemini-4-hydrogen", Slot: "generation", Operation: OpAsk,
				IsCloud: true, Calls: 1, InputTokens: 4000, OutputTokens: 400, CostUSD: 0.009, KnownPricing: true,
			},
		},
	})

	rows, err := db.UsageEntries("2026-11-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 ledger rows (embed + gen), got %+v", rows)
	}

	cleared, err := db.Clear()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 {
		t.Fatalf("cleared = %d, want 1", cleared)
	}
	if left, _ := db.UsageEntries(""); len(left) != 0 {
		t.Fatalf("ledger after Clear() = %+v, want empty", left)
	}
}

func TestHistoricalEmbedThroughput(t *testing.T) {
	db := openTestDB(t)
	// Below threshold (<3 embedded) -> ignored
	mustRecord(t, db, Operation{
		Operation: OpReembed, OK: true, Embedded: 2, TotalChars: 1000, EmbedMs: 500,
		EmbeddingProvider: "llama-local", EmbeddingModel: "embeddinggemma-2",
	})
	if _, _, ok := db.HistoricalEmbedThroughput("llama-local", "embeddinggemma-2"); ok {
		t.Fatal("expected <3 embedded build to be ignored")
	}

	// Older matching build (with legacy empty provider)
	mustRecord(t, db, Operation{
		Operation: OpReembed, OK: true, Embedded: 10, TotalChars: 20000, EmbedMs: 2000,
		EmbeddingProvider: "", EmbeddingModel: "embeddinggemma-2",
	})
	// Failed newer build -> ignored
	mustRecord(t, db, Operation{
		Operation: OpReembed, OK: false, Embedded: 50, TotalChars: 50000, EmbedMs: 1000,
		EmbeddingProvider: "llama-local", EmbeddingModel: "embeddinggemma-2",
	})

	dps, cps, ok := db.HistoricalEmbedThroughput("llama-local", "embeddinggemma-2")
	if !ok || dps != 5.0 || cps != 10000.0 {
		t.Fatalf("got (%v, %v, %v), want (5.0, 10000.0, true)", dps, cps, ok)
	}
}
