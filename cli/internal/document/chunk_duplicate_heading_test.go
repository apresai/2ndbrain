package document

import (
	"strings"
	"testing"
)

// A heading path is not unique within a document. Daily-note and meeting-note
// templates repeat "## Standup", "## Notes" and "## Decision" under the same
// parent all the time. The chunk id hashes (doc id, heading path) and
// chunks.id is a PRIMARY KEY, so the later section used to overwrite the
// earlier one and its text disappeared from the index: content silently
// unsearchable, with no warning and a zero exit.
func TestChunkDocument_RepeatedHeadingPathsGetDistinctIDs(t *testing.T) {
	doc := &Document{ID: "d1", Body: `# Log

## Standup

ZEBRAWORD only in the first standup.

## Standup

QUOKKAWORD only in the second standup.
`}
	chunks := ChunkDocument(doc)

	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3 (the Log preamble and BOTH Standup sections)", len(chunks))
	}

	seen := map[string]string{}
	for _, c := range chunks {
		if prev, dup := seen[c.ID]; dup {
			t.Errorf("chunk id %s reused by %q and %q; chunks.id is a PRIMARY KEY so one of these would be lost on insert",
				c.ID, prev, c.Content)
		}
		seen[c.ID] = c.Content
	}

	// Both bodies must survive. This is the user-visible half: whichever
	// section lost the id race became unsearchable.
	var haveZebra, haveQuokka bool
	for _, c := range chunks {
		if strings.Contains(c.Content, "ZEBRAWORD") {
			haveZebra = true
		}
		if strings.Contains(c.Content, "QUOKKAWORD") {
			haveQuokka = true
		}
	}
	if !haveZebra || !haveQuokka {
		t.Errorf("lost a section: zebra=%v quokka=%v", haveZebra, haveQuokka)
	}

	// The displayed heading path is NOT disambiguated: only the id is. RAG
	// windowing and the outline both key off the real heading.
	for _, c := range chunks {
		if c.HeadingPath != "# Log" && c.HeadingPath != "# Log > ## Standup" {
			t.Errorf("heading path was mangled by id disambiguation: %q", c.HeadingPath)
		}
	}
}

// The first occurrence must keep the id it had before this fix, so upgrading
// does not re-chunk (and force a re-embed of) every document in every vault.
func TestChunkDocument_FirstOccurrenceKeepsItsOriginalID(t *testing.T) {
	single := ChunkDocument(&Document{ID: "d1", Body: "# Log\n\n## Standup\n\nonly one.\n"})
	repeated := ChunkDocument(&Document{ID: "d1", Body: "# Log\n\n## Standup\n\nonly one.\n\n## Standup\n\nsecond.\n"})
	if len(single) < 2 || len(repeated) < 3 {
		t.Fatalf("unexpected chunk counts: single=%d repeated=%d", len(single), len(repeated))
	}
	if single[1].ID != repeated[1].ID {
		t.Errorf("first Standup chunk id changed from %s to %s; every vault would re-chunk needlessly",
			single[1].ID, repeated[1].ID)
	}
}
