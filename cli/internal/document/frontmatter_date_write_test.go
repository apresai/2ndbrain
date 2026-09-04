package document

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// plainDateLine matches a `key: <value>` frontmatter line whose value opens
// with neither quote character. Obsidian reads a QUOTED ISO value as Text, so
// the quoting IS the property type; asserting on the emitted bytes is the only
// assertion that sees it.
func plainDateLine(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + key + `: [^"'\n]+$`)
}

// UNKNOWN 1 from the plan: does a time.Time in the frontmatter map survive BOTH
// write paths unquoted?
//
// Path B is SerializeDocument, the whole-map re-marshal a fresh `2nb create`
// takes. Path A is UpdateDocumentFrontmatterAST, the surgical writer every
// later edit takes, which builds a new value node through valueNode (marshal,
// then unmarshal into a Node). They are different encoders and either one
// quoting the date would leave Obsidian showing Text.
//
// The assertion is on the WRITTEN BYTES and then on a REPARSE of them, because
// a write that produces mis-typed YAML is invisible until the next read.
func TestNewDocument_BothWritePathsEmitAnUnquotedDate(t *testing.T) {
	doc := NewDocument("Write Path Note", "note", "body\n")

	pathB, err := SerializeDocument(doc.Frontmatter, doc.Body)
	if err != nil {
		t.Fatalf("SerializeDocument: %v", err)
	}
	pathA, err := UpdateDocumentFrontmatterAST(pathB, doc.Frontmatter, doc.Body)
	if err != nil {
		t.Fatalf("UpdateDocumentFrontmatterAST: %v", err)
	}

	for name, out := range map[string][]byte{"SerializeDocument": pathB, "UpdateDocumentFrontmatterAST": pathA} {
		for _, key := range []string{"created", "modified"} {
			if !plainDateLine(key).Match(out) {
				t.Errorf("%s wrote a quoted or missing %s, which Obsidian types as Text:\n%s", name, key, out)
			}
		}
		// A fraction on disk that the reader's time.RFC3339 format drops is
		// how the file and the index column disagree from birth.
		if strings.Contains(string(out), ".") && plainDateLine(`created`).Find(out) != nil {
			if line := string(plainDateLine("created").Find(out)); strings.Contains(line, ".") {
				t.Errorf("%s wrote sub-second precision the reader will drop: %s", name, line)
			}
		}
		// Reparse: the bytes must read back as the same instant the in-memory
		// document holds.
		back, err := Parse("n.md", out)
		if err != nil {
			t.Fatalf("%s: reparse failed: %v\n%s", name, err, out)
		}
		if back.CreatedAt != doc.CreatedAt {
			t.Errorf("%s: reparsed CreatedAt = %q, want %q", name, back.CreatedAt, doc.CreatedAt)
		}
		if back.ModifiedAt != doc.ModifiedAt {
			t.Errorf("%s: reparsed ModifiedAt = %q, want %q", name, back.ModifiedAt, doc.ModifiedAt)
		}
		if _, perr := time.Parse(time.RFC3339, back.CreatedAt); perr != nil {
			t.Errorf("%s: reparsed CreatedAt %q is not RFC3339: %v", name, back.CreatedAt, perr)
		}
	}
}

// UNKNOWN 2 from the plan: store.UpsertDocumentTx does json.Marshal on the
// whole Frontmatter map, so a time.Time there must produce the same JSON the
// string produced or the documents.frontmatter column, and every `search
// --json` row that carries it, changes shape.
//
// time.Time marshals through its own MarshalJSON as a quoted RFC3339Nano
// string, and at second precision RFC3339Nano emits no fraction, so the two are
// byte-identical. This asserts that rather than assuming it.
func TestNewDocument_FrontmatterJSONIsUnchangedByTheTimeType(t *testing.T) {
	doc := NewDocument("JSON Column Note", "note", "body\n")

	asTime, err := json.Marshal(doc.Frontmatter)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	asString := make(map[string]any, len(doc.Frontmatter))
	for k, v := range doc.Frontmatter {
		if tv, ok := v.(time.Time); ok {
			asString[k] = tv.Format(time.RFC3339)
			continue
		}
		asString[k] = v
	}
	want, err := json.Marshal(asString)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if string(asTime) != string(want) {
		t.Errorf("the documents.frontmatter JSON column changed shape:\n got %s\nwant %s", asTime, want)
	}
}

// Truncate(time.Second) is load-bearing and easy to drop, so it gets its own
// assertion: the in-memory struct field and the map value must name the same
// instant to the second, in both directions.
func TestNewDocument_DatesAreSecondPrecision(t *testing.T) {
	doc := NewDocument("Precision Note", "note", "")
	for key, field := range map[string]string{"created": doc.CreatedAt, "modified": doc.ModifiedAt} {
		tv, ok := doc.Frontmatter[key].(time.Time)
		if !ok {
			t.Fatalf("Frontmatter[%q] is %T, want time.Time", key, doc.Frontmatter[key])
		}
		if tv.Nanosecond() != 0 {
			t.Errorf("Frontmatter[%q] carries %d ns; the encoder writes RFC3339Nano and the reader drops it", key, tv.Nanosecond())
		}
		if got := tv.Format(time.RFC3339); got != field {
			t.Errorf("Frontmatter[%q] formats as %q but the struct field holds %q", key, got, field)
		}
	}
}

// The mechanism the write-side coercion exists to close: once a note's date
// node is PLAIN, writing a plain STRING back over it requotes the line, because
// nodeHoldsValue compares the decoded time.Time against the supplied string and
// they are not equal. That is one note per `meta --set`, silently reverting the
// property to Obsidian's Text type.
//
// Supplying a time.Time instead leaves the node exactly as the file wrote it,
// which is also what makes the migration in a later commit idempotent.
func TestSetMeta_AStringOverAPlainDateRequotesItAndATimeDoesNot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "n.md")
	original := "---\ntitle: N\ncreated: 2026-04-05T07:27:34Z\nmodified: 2026-04-05T07:27:34Z\n---\nbody\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	instant := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name       string
		value      any
		wantQuoted bool
	}{
		{"a raw string requotes", instant.Format(time.RFC3339), true},
		{"a time.Time stays plain", instant, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseFile(path)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			doc.SetMeta("modified", tc.value)
			out, err := doc.Serialize()
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			quoted := strings.Contains(string(out), `modified: "`)
			if quoted != tc.wantQuoted {
				t.Errorf("modified quoted = %v, want %v:\n%s", quoted, tc.wantQuoted, out)
			}
			// Either way the untouched `created` line must survive byte for
			// byte: only the key the caller named may move.
			if !strings.Contains(string(out), "created: 2026-04-05T07:27:34Z\n") {
				t.Errorf("the untouched created line was rewritten:\n%s", out)
			}
			// And either way the bytes must reparse to the instant set.
			back, err := Parse("n.md", out)
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if back.ModifiedAt != instant.Format(time.RFC3339) {
				t.Errorf("reparsed ModifiedAt = %q, want %q", back.ModifiedAt, instant.Format(time.RFC3339))
			}
		})
	}
}
