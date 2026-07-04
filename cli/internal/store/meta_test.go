package store

import (
	"path/filepath"
	"testing"
)

// TestMetaKV exercises the v4 meta table end to end: a fresh Open runs the v4
// migration (so SetMeta would fail if the table weren't created), absent keys
// read as not-present / default, and SetMeta upserts.
func TestMetaKV(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, ok, err := db.GetMeta("missing"); err != nil || ok {
		t.Fatalf("absent key: got ok=%v err=%v, want ok=false", ok, err)
	}
	if got := db.GetMetaInt("missing", 7); got != 7 {
		t.Fatalf("GetMetaInt default = %d, want 7", got)
	}

	if err := db.SetMetaInt("gen", 3); err != nil {
		t.Fatalf("SetMetaInt: %v", err)
	}
	if v, ok, err := db.GetMeta("gen"); err != nil || !ok || v != "3" {
		t.Fatalf("GetMeta = (%q, %v, %v), want (\"3\", true, nil)", v, ok, err)
	}
	if got := db.GetMetaInt("gen", 0); got != 3 {
		t.Fatalf("GetMetaInt = %d, want 3", got)
	}

	if err := db.SetMetaInt("gen", 5); err != nil { // upsert existing key
		t.Fatalf("SetMetaInt upsert: %v", err)
	}
	if got := db.GetMetaInt("gen", 0); got != 5 {
		t.Fatalf("after upsert GetMetaInt = %d, want 5", got)
	}
}
