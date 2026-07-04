package store

import (
	"database/sql"
	"strconv"
)

// Meta keys stamped into the index-db `meta` table. They record the LOGIC
// generation an index/embedding was produced with, so vault.CheckIndexFreshness
// can detect when a newer 2nb changed chunking/indexing/embedding behavior and
// prompt a reindex/re-embed. Absent keys read as 0 (an index built before the
// key existed), which correctly flags a pre-existing vault as stale.
const (
	MetaIndexGeneration  = "index_generation"
	MetaEmbedGeneration  = "embed_generation"
	MetaIndexedByVersion = "indexed_by_version" // diagnostic: the 2nb version that last stamped
)

// GetMeta reads a value from the meta KV table. ok=false when the key is absent.
func (db *DB) GetMeta(key string) (value string, ok bool, err error) {
	err = db.conn.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// GetMetaInt reads a meta value as an int, returning def when the key is absent
// or unparseable. An un-stamped older index therefore reads as def (typically 0).
func (db *DB) GetMetaInt(key string, def int) int {
	v, ok, err := db.GetMeta(key)
	if err != nil || !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// SetMeta upserts a KV value into the meta table.
func (db *DB) SetMeta(key, value string) error {
	_, err := db.execRetry(
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// SetMetaInt upserts an integer meta value.
func (db *DB) SetMetaInt(key string, value int) error {
	return db.SetMeta(key, strconv.Itoa(value))
}
