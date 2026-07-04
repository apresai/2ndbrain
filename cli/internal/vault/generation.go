package vault

import "github.com/apresai/2ndbrain/internal/store"

// Index/embedding LOGIC generation counters — the release-pipeline "flag". Bump
// one in a release commit when that release changes HOW the vault is indexed or
// embedded, so the shipped CLI can detect a user's stale index and prompt a
// reindex/re-embed. They are stamped into the index DB (store.MetaIndexGeneration
// / MetaEmbedGeneration) on a full index / --force-reembed and compared by
// CheckIndexFreshness. Distinct from schema_version (DB shape) and the per-row
// content/model hashes (content/model drift), which all miss a same-model,
// same-dimension, same-schema LOGIC change.
//
// Bump history:
//
//	EmbedGeneration
//	  1  chunk-size cap (#134) + asymmetric GENERIC_RETRIEVAL query purpose:
//	     chunk boundaries / vec_chunks changed, so a full re-embed is required.
//	IndexGeneration
//	  1  baseline.
//
// If you change the watched files (see `make check-index-generation`) but a
// reindex is genuinely NOT needed, add a `Reindex-Not-Needed: <reason>` trailer
// to the commit instead of bumping.
const (
	// IndexGeneration bumps for index-only logic changes (FTS content, link/tag
	// extraction) that do NOT alter chunk boundaries or embeddings. Fix: 2nb index.
	IndexGeneration = 1

	// EmbedGeneration bumps for chunking OR embedding-production logic changes
	// (chunk boundaries, purpose, pooling, normalization) at the SAME model and
	// dimension. A full re-embed is required because vec_chunks ids/vectors must be
	// regenerated to match the rebuilt chunks table. Fix: 2nb index --force-reembed.
	EmbedGeneration = 1
)

// IndexFreshness reports whether a vault's index was built by an older 2nb whose
// indexing/embedding logic this binary has since changed. It is surfaced by
// vault status / ai status / doctor and the app/plugin.
type IndexFreshness struct {
	ReindexRecommended bool   `json:"reindex_recommended"`
	ReembedRecommended bool   `json:"reembed_recommended"`
	StoredIndexGen     int    `json:"stored_index_generation"`
	StoredEmbedGen     int    `json:"stored_embed_generation"`
	CurrentIndexGen    int    `json:"current_index_generation"`
	CurrentEmbedGen    int    `json:"current_embed_generation"`
	Reason             string `json:"reason,omitempty"`
	Fix                string `json:"fix,omitempty"`
}

// Stale reports whether either a reindex or re-embed is recommended.
func (f IndexFreshness) Stale() bool { return f.ReindexRecommended || f.ReembedRecommended }

// CheckIndexFreshness compares the generation stamps in the index DB against this
// binary's constants. Re-embed takes precedence (it also re-chunks/reindexes),
// and is only recommended once the vault actually has embeddings — an unindexed
// or unembedded vault is handled by the normal "unindexed" status, not an upgrade
// prompt. A missing stamp reads as generation 0, so an index built before this
// mechanism (or before the current logic) is correctly flagged as stale.
func CheckIndexFreshness(db *store.DB) IndexFreshness {
	f := IndexFreshness{
		StoredIndexGen:  db.GetMetaInt(store.MetaIndexGeneration, 0),
		StoredEmbedGen:  db.GetMetaInt(store.MetaEmbedGeneration, 0),
		CurrentIndexGen: IndexGeneration,
		CurrentEmbedGen: EmbedGeneration,
	}
	hasEmbeddings := false
	if n, err := db.EmbeddingCount(); err == nil && n > 0 {
		hasEmbeddings = true
	}
	if hasEmbeddings && f.StoredEmbedGen < EmbedGeneration {
		f.ReembedRecommended = true
		f.Reason = "this vault's embeddings were produced by an older 2nb whose chunking/embedding logic has since improved"
		f.Fix = "2nb index --force-reembed"
		return f
	}
	if f.StoredIndexGen < IndexGeneration {
		f.ReindexRecommended = true
		f.Reason = "this vault was indexed by an older 2nb whose indexing logic has since improved"
		f.Fix = "2nb index"
	}
	return f
}

// StampIndexGeneration records that the index was (re)built at the current
// IndexGeneration. Call after a successful FULL index (not --doc/incremental).
func StampIndexGeneration(db *store.DB, cliVersion string) error {
	if err := db.SetMetaInt(store.MetaIndexGeneration, IndexGeneration); err != nil {
		return err
	}
	_ = db.SetMeta(store.MetaIndexedByVersion, cliVersion)
	return nil
}

// StampEmbedGeneration records that all embeddings were (re)produced at the
// current EmbedGeneration. Call after a successful FULL re-embed (--force-reembed
// with no failures). A force-reembed also re-chunks + reindexes, so it advances
// the index generation too.
func StampEmbedGeneration(db *store.DB, cliVersion string) error {
	if err := db.SetMetaInt(store.MetaEmbedGeneration, EmbedGeneration); err != nil {
		return err
	}
	return StampIndexGeneration(db, cliVersion)
}

// PriorEmbedGeneration reads the embed-generation stamp from before a run (0 when
// absent). Capture it BEFORE an embed pass to feed StampAfterIndex.
func PriorEmbedGeneration(db *store.DB) int {
	return db.GetMetaInt(store.MetaEmbedGeneration, 0)
}

// StampAfterIndex records the logic generation a full index run achieved. It
// advances the EMBED generation only when ALL stored embeddings are current-gen:
// a full re-embed just ran, OR the vault held no older-generation vectors before
// this run (freshly embedded, or already current) — and every embeddable doc is
// now embedded with no failures. A plain reindex re-chunks all files but leaves
// UNCHANGED docs' embeddings untouched, so their generation is unknown and only
// the INDEX generation advances (which keeps prompting a --force-reembed until
// the embeddings are actually regenerated). Best-effort; the caller logs errors.
//
// "all embedded" uses EmbeddingCounts().embeddableUnembedded (chunk-aware) NOT
// DocumentsNeedingEmbedding: empty/whitespace notes carry a NULL embedding
// forever (the embed pass skips them), so counting them would leave the stamp
// unwritten and nag a re-embed on any vault holding a blank note.
//
// embeddingCountBefore and priorEmbedGen must be captured BEFORE the embed pass.
func StampAfterIndex(db *store.DB, cliVersion string, forceReembed bool, embedFailures, embeddingCountBefore, priorEmbedGen int) error {
	_, _, embeddableUnembedded, err := db.EmbeddingCounts()
	allEmbedded := err == nil && embeddableUnembedded == 0
	embedCurrent := forceReembed || priorEmbedGen >= EmbedGeneration || embeddingCountBefore == 0
	if embedFailures == 0 && allEmbedded && embedCurrent {
		return StampEmbedGeneration(db, cliVersion)
	}
	return StampIndexGeneration(db, cliVersion)
}
