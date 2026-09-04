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
//	  2  fenced-code heading exemption: `#` inside ``` / ~~~ is no longer an
//	     ATX heading, so heading_path / chunk ids change for notes with
//	     in-fence `#` comments (typical of runbooks).
//	  3  repeated heading paths get distinct chunk ids. A document with two
//	     sections sharing a heading path (a second `## Standup` under `# Log`,
//	     which every daily-note template produces) hashed both to the same
//	     chunk id, and chunks.id is a PRIMARY KEY, so the later section
//	     overwrote the earlier and that text disappeared from the index. The
//	     first occurrence keeps its old id, so only documents that actually
//	     repeat a heading re-chunk, but their vectors must be regenerated.
//	IndexGeneration
//	  1  baseline.
//	  2  percent-encoding-aware link resolution: an Obsidian-encoded markdown
//	     link ([x](My%20Note.md)) to a real note now resolves (target_id set),
//	     so backlinks/lint/graph outcomes change for vaults holding encoded
//	     links. Fix: 2nb index.
//	  3  the frontmatter/body boundary moved for a note that opens with an
//	     empty properties block. Such a note either failed to parse or had the
//	     block's delimiters read as body text, so what got indexed differs from
//	     what 0.22.2 indexed. The content hash is computed from the PARSED body
//	     and every file is re-parsed on every run, so a plain reindex notices
//	     the change and re-chunks and re-embeds exactly those notes.
//	     Fix: 2nb index. Deliberately NOT an EmbedGeneration bump: charging
//	     every user for a whole-vault re-embed to repair a handful of notes is
//	     the wrong trade when content drift already repairs them.
//	  4  an UNQUOTED frontmatter date is read as a date. yaml.v3 resolves
//	     `modified: 2020-01-01T00:00:00Z` (the shape Obsidian's own Date
//	     property writes) to time.Time, and the old string assertion failed
//	     silently, so documents.created_at/modified_at stayed EMPTY and `stale`
//	     omitted the note. The same coercion fills title/type/status/id from a
//	     scalar YAML read as a number, a boolean or a date, and keeps a
//	     list entry of those types as a tag or alias. Only INDEXED COLUMNS
//	     change: the content hash is computed from the parsed BODY, which
//	     frontmatter cannot move, so no chunk and no vector is affected.
//	     Fix: 2nb index.
//
// If you change the watched files (see `make check-index-generation`) but a
// reindex is genuinely NOT needed, add a `Reindex-Not-Needed: <reason>` trailer
// to the commit instead of bumping.
const (
	// IndexGeneration bumps for index-only logic changes (FTS content, link/tag
	// extraction) that do NOT alter chunk boundaries or embeddings. Fix: 2nb index.
	IndexGeneration = 4

	// EmbedGeneration bumps for chunking OR embedding-production logic changes
	// (chunk boundaries, purpose, pooling, normalization) at the SAME model and
	// dimension. A full re-embed is required because vec_chunks ids/vectors must be
	// regenerated to match the rebuilt chunks table. Fix: 2nb index --force-reembed.
	EmbedGeneration = 3
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

// StampFreshGenerations records the current generations on a vault that was
// just created. It holds no chunks and no vectors yet, so it is trivially at
// the current logic generation for both, and every embedding it acquires from
// here on is produced by this binary.
//
// Without this stamp the quick start that `2nb --help` prints (vault create,
// then create, then index) reported "UPGRADE REEMBED RECOMMENDED" on a vault
// seconds old: `create` embeds inline without stamping, so by the time `index`
// ran there were already embeddings and no stamp, which is exactly what an old
// pre-stamp vault looks like, and StampAfterIndex correctly refused to guess.
// The nag then told a brand-new user to pay for a full re-embed to fix nothing.
// indexed_by_version is deliberately NOT written here: nothing has been indexed.
func StampFreshGenerations(db *store.DB) error {
	if err := db.SetMetaInt(store.MetaEmbedGeneration, EmbedGeneration); err != nil {
		return err
	}
	return db.SetMetaInt(store.MetaIndexGeneration, IndexGeneration)
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
// embedShortfall is every document that needed embedding this run and did not
// get it: failed calls plus notes the pass could not open. A note that could not
// be opened keeps its previous vector, which a force-reembed's invalidation does
// not clear, so it would otherwise be invisible to the "all embedded" check and
// let a partial run claim the whole vault is at the current embed generation.
//
// embeddingCountBefore and priorEmbedGen must be captured BEFORE the embed pass.
func StampAfterIndex(db *store.DB, cliVersion string, forceReembed bool, embedShortfall, embeddingCountBefore, priorEmbedGen int) error {
	_, _, embeddableUnembedded, err := db.EmbeddingCounts()
	allEmbedded := err == nil && embeddableUnembedded == 0
	embedCurrent := forceReembed || priorEmbedGen >= EmbedGeneration || embeddingCountBefore == 0
	if embedShortfall == 0 && allEmbedded && embedCurrent {
		return StampEmbedGeneration(db, cliVersion)
	}
	return StampIndexGeneration(db, cliVersion)
}
