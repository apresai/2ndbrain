package bench

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "bench.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open bench DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestAddAndListFavorites(t *testing.T) {
	db := openTestDB(t)

	if err := db.AddFavorite("bedrock", "claude-haiku", "generation"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if err := db.AddFavorite("ollama", "nomic-embed-text", "embedding"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	favs, err := db.ListFavorites()
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if len(favs) != 2 {
		t.Fatalf("expected 2 favorites, got %d", len(favs))
	}
	if favs[0].Provider != "bedrock" || favs[0].ModelID != "claude-haiku" {
		t.Errorf("first fav: got %s/%s, want bedrock/claude-haiku", favs[0].Provider, favs[0].ModelID)
	}
	if favs[1].Provider != "ollama" || favs[1].ModelType != "embedding" {
		t.Errorf("second fav: got %s/%s, want ollama/embedding", favs[1].Provider, favs[1].ModelType)
	}
}

func TestAddFavoriteIdempotent(t *testing.T) {
	db := openTestDB(t)

	db.AddFavorite("bedrock", "claude-haiku", "generation")
	db.AddFavorite("bedrock", "claude-haiku", "generation") // duplicate

	favs, _ := db.ListFavorites()
	if len(favs) != 1 {
		t.Fatalf("expected 1 favorite after duplicate add, got %d", len(favs))
	}
}

func TestRemoveFavorite(t *testing.T) {
	db := openTestDB(t)

	db.AddFavorite("bedrock", "claude-haiku", "generation")
	db.AddFavorite("ollama", "nomic-embed-text", "embedding")

	if err := db.RemoveFavorite("bedrock", "claude-haiku"); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}

	favs, _ := db.ListFavorites()
	if len(favs) != 1 {
		t.Fatalf("expected 1 favorite after removal, got %d", len(favs))
	}
	if favs[0].ModelID != "nomic-embed-text" {
		t.Errorf("remaining fav: got %s, want nomic-embed-text", favs[0].ModelID)
	}
}

func TestRemoveFavoriteNonexistent(t *testing.T) {
	db := openTestDB(t)

	// Should not error when removing something that doesn't exist.
	if err := db.RemoveFavorite("bedrock", "nonexistent"); err != nil {
		t.Fatalf("RemoveFavorite nonexistent: %v", err)
	}
}

func TestInsertAndListRuns(t *testing.T) {
	db := openTestDB(t)

	runs := []Run{
		{Timestamp: "2026-04-09T10:00:00Z", Provider: "bedrock", ModelID: "haiku", Probe: "generate", LatencyMs: 500, OK: true, Detail: "4", VaultDocCount: 20},
		{Timestamp: "2026-04-09T10:00:01Z", Provider: "bedrock", ModelID: "haiku", Probe: "search", LatencyMs: 10, OK: true, Detail: "results=5", VaultDocCount: 20},
		{Timestamp: "2026-04-09T10:00:02Z", Provider: "ollama", ModelID: "gemma", Probe: "generate", LatencyMs: 2000, OK: false, Detail: "timeout", VaultDocCount: 20},
	}
	for _, r := range runs {
		if err := db.InsertRun(&r); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
	}

	got, err := db.ListRuns(10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(got))
	}
	// ListRuns returns newest first.
	if got[0].ModelID != "gemma" {
		t.Errorf("first run should be gemma (newest), got %s", got[0].ModelID)
	}
	if got[2].OK != true {
		t.Errorf("third run should be OK=true")
	}
}

func TestListRunsLimit(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 10; i++ {
		db.InsertRun(&Run{
			Timestamp: fmt.Sprintf("2026-04-09T10:00:%02dZ", i),
			Provider:  "bedrock", ModelID: "haiku", Probe: "generate",
			LatencyMs: int64(100 + i), OK: true, VaultDocCount: 20,
		})
	}

	got, _ := db.ListRuns(3)
	if len(got) != 3 {
		t.Fatalf("expected 3 runs with limit, got %d", len(got))
	}
}

func TestLatestRunsPerModel(t *testing.T) {
	db := openTestDB(t)

	// Two runs for haiku/generate — only latest should appear.
	db.InsertRun(&Run{Timestamp: "2026-04-09T10:00:00Z", Provider: "bedrock", ModelID: "haiku", Probe: "generate", LatencyMs: 500, OK: true, VaultDocCount: 20})
	db.InsertRun(&Run{Timestamp: "2026-04-09T11:00:00Z", Provider: "bedrock", ModelID: "haiku", Probe: "generate", LatencyMs: 450, OK: true, VaultDocCount: 20})

	// One run for gemma/generate.
	db.InsertRun(&Run{Timestamp: "2026-04-09T10:30:00Z", Provider: "openrouter", ModelID: "gemma", Probe: "generate", LatencyMs: 800, OK: true, VaultDocCount: 20})

	// One run for haiku/search.
	db.InsertRun(&Run{Timestamp: "2026-04-09T10:00:00Z", Provider: "bedrock", ModelID: "haiku", Probe: "search", LatencyMs: 10, OK: true, VaultDocCount: 20})

	got, err := db.LatestRunsPerModel()
	if err != nil {
		t.Fatalf("LatestRunsPerModel: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 latest runs (haiku/generate, gemma/generate, haiku/search), got %d", len(got))
	}

	// Should be sorted by probe then latency. "generate" before "search" alphabetically.
	// Within generate: haiku 450ms < gemma 800ms.
	if got[0].ModelID != "haiku" || got[0].Probe != "generate" || got[0].LatencyMs != 450 {
		t.Errorf("first: expected haiku/generate/450ms, got %s/%s/%dms", got[0].ModelID, got[0].Probe, got[0].LatencyMs)
	}
	if got[1].ModelID != "gemma" || got[1].LatencyMs != 800 {
		t.Errorf("second: expected gemma/800ms, got %s/%dms", got[1].ModelID, got[1].LatencyMs)
	}
	if got[2].Probe != "search" {
		t.Errorf("third: expected search probe, got %s", got[2].Probe)
	}
}

func TestLatestRunsPerModelSeparatesRoutes(t *testing.T) {
	db := openTestDB(t)
	ts := "2026-04-09T10:00:00Z"
	if err := db.InsertRun(&Run{
		Timestamp: ts, Provider: "bedrock", ModelID: "grok", Probe: "generate",
		LatencyMs: 100, OK: true, Plane: "classic", Region: "us-east-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertRun(&Run{
		Timestamp: ts, Provider: "bedrock", ModelID: "grok", Probe: "generate",
		LatencyMs: 200, OK: true, Plane: "mantle", Region: "us-west-2",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.LatestRunsPerModel()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows (one per route), got %d", len(got))
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.Plane+"/"+r.Region] = true
	}
	if !seen["classic/us-east-1"] || !seen["mantle/us-west-2"] {
		t.Errorf("routes = %v, want both classic/us-east-1 and mantle/us-west-2", seen)
	}
}

func TestMigrateCollapsesSchemaVersionStamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bench.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(`
CREATE TABLE runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL,
    provider TEXT NOT NULL,
    model_id TEXT NOT NULL,
    probe TEXT NOT NULL,
    latency_ms INTEGER NOT NULL,
    ok INTEGER NOT NULL DEFAULT 1,
    detail TEXT NOT NULL DEFAULT '',
    vault_doc_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE schema_version (version INTEGER NOT NULL);
`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err := conn.Exec(`INSERT INTO schema_version (version) VALUES (1)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(
		`INSERT INTO runs (timestamp, provider, model_id, probe, latency_ms, ok, detail, vault_doc_count)
		 VALUES ('2026-04-09T10:00:00Z','bedrock','haiku','generate',500,1,'',10)`,
	); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open migrated db: %v", err)
	}
	defer db.Close()
	var n, ver int
	if err := db.conn.QueryRow(`SELECT COUNT(*), MAX(version) FROM schema_version`).Scan(&n, &ver); err != nil {
		t.Fatal(err)
	}
	if n != 1 || ver != benchSchemaVersion {
		t.Fatalf("schema_version: count=%d max=%d, want 1 row at version %d", n, ver, benchSchemaVersion)
	}
	has, err := db.hasColumn("runs", "plane")
	if err != nil || !has {
		t.Fatalf("plane column missing after migrate: has=%v err=%v", has, err)
	}
}

func TestBackfillMissingRoutesInfersOnce(t *testing.T) {
	db := openTestDB(t)
	if err := db.InsertRun(&Run{
		Timestamp: "2026-04-09T10:00:00Z", Provider: "bedrock", ModelID: "haiku",
		Probe: "generate", LatencyMs: 500, OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.BackfillMissingRoutes(func(provider, modelID string) (string, string) {
		return "classic", "us-east-1"
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListRuns(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d runs", len(got))
	}
	if got[0].Plane != "classic" || got[0].Region != "us-east-1" {
		t.Errorf("backfill = %s/%s, want classic/us-east-1", got[0].Plane, got[0].Region)
	}
	// A second pass with a different lookup must not rewrite a filled row.
	if err := db.BackfillMissingRoutes(func(provider, modelID string) (string, string) {
		return "mantle", "us-west-2"
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.ListRuns(1)
	if got[0].Plane != "classic" || got[0].Region != "us-east-1" {
		t.Errorf("second backfill rewrote a filled row: %s/%s", got[0].Plane, got[0].Region)
	}
}
