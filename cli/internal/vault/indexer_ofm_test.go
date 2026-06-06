package vault

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestIndexFile_InlineTagsBlockIDsAndComments proves the OFM wiring end-to-end:
// inline #tags are indexed and merged with frontmatter tags, block-reference ids
// land in chunks.block_id / links.block_id, and content inside %% comments %% is
// excluded from tags and links.
func TestIndexFile_InlineTagsBlockIDsAndComments(t *testing.T) {
	v := initTestVault(t)

	content := "---\nid: d1\ntitle: Main\ntype: note\nstatus: draft\ntags:\n  - fromfm\n---\n" +
		"Body mentions #inline and a block link [[other#^blk]].\n\n" +
		"A referenced block. ^def-456\n\n" +
		"%% hidden #commenttag and [[secretlink]] %%\n"
	abs := filepath.Join(v.Root, "d1.md")
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := indexFile(v.DB, abs, "d1.md"); err != nil {
		t.Fatalf("indexFile: %v", err)
	}

	// Tags: frontmatter + inline, but NOT the tag inside the comment.
	tags := map[string]bool{}
	rows, err := v.DB.Conn().Query("SELECT tag FROM tags WHERE doc_id = 'd1'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var tg string
		if err := rows.Scan(&tg); err != nil {
			t.Fatal(err)
		}
		tags[tg] = true
	}
	rows.Close()
	if !tags["fromfm"] || !tags["inline"] {
		t.Errorf("expected fromfm + inline tags, got %v", tags)
	}
	if tags["commenttag"] {
		t.Errorf("tag inside %% comment %% should not be indexed, got %v", tags)
	}

	// chunks.block_id populated from the trailing ^def-456 marker.
	var chunkBlock sql.NullString
	if err := v.DB.Conn().QueryRow(
		"SELECT block_id FROM chunks WHERE doc_id='d1' AND block_id IS NOT NULL LIMIT 1",
	).Scan(&chunkBlock); err != nil {
		t.Fatalf("query chunk block_id: %v", err)
	}
	if chunkBlock.String != "def-456" {
		t.Errorf("chunk block_id = %q, want def-456", chunkBlock.String)
	}

	// links.block_id populated from [[other#^blk]].
	var linkBlock sql.NullString
	if err := v.DB.Conn().QueryRow(
		"SELECT block_id FROM links WHERE source_id='d1' AND block_id IS NOT NULL LIMIT 1",
	).Scan(&linkBlock); err != nil {
		t.Fatalf("query link block_id: %v", err)
	}
	if linkBlock.String != "blk" {
		t.Errorf("link block_id = %q, want blk", linkBlock.String)
	}

	// The [[secretlink]] inside the comment must not be indexed.
	var secret int
	if err := v.DB.Conn().QueryRow(
		"SELECT COUNT(*) FROM links WHERE source_id='d1' AND target_raw='secretlink'",
	).Scan(&secret); err != nil {
		t.Fatal(err)
	}
	if secret != 0 {
		t.Errorf("link inside comment should not be indexed, found %d", secret)
	}
}
