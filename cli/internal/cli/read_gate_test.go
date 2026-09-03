package cli

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

// The unconfigured-write refusal exists to stop a mis-directed WRITE from
// splitting a vault. It was firing on pure reads: `meta <p> --get title`, bare
// `meta <p>`, and `daily read` on a note that already exists all opened through
// openVaultAndSetActive. The app, the plugin, and the MCP server pin --vault on
// every call, so users hit this constantly.
//
// 2NB_TEST must be EMPTY in these tests, or openWriteTarget's honor branch
// short-circuits and the refusal can never fire.

// newStrayVaultWithNote builds a vault Obsidian does not know (no .obsidian),
// registers a DIFFERENT vault as the configured one, and writes one note.
func newStrayVaultWithNote(t *testing.T, relPath, body string) string {
	t.Helper()
	clearWriteEnv(t, "")
	t.Chdir(t.TempDir()) // the cwd is never the target

	writeObsidianRegistryForTest(t, newResolveTestVault(t)) // a configured vault exists elsewhere
	stray := newStrayVault(t)

	abs := filepath.Join(stray, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	return stray
}

func mtimeOf(t *testing.T, path string) time.Time {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.ModTime()
}

const readGateNote = "---\ntitle: Stray Note\ntype: note\nstatus: draft\n---\n\nBody text.\n"

func TestReadGate_MetaViewsNeedNoUnconfigured(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote)
	notePath := filepath.Join(stray, "n.md")
	before := mtimeOf(t, notePath)

	for _, argv := range [][]string{
		{"meta", "n.md", "--get", "title"},
		{"meta", "n.md"},
	} {
		out, err := runCLIArgs(t, stray, argv...)
		if err != nil {
			t.Fatalf("%v was refused on a vault Obsidian does not know: %v\n%s", argv, err, out)
		}
		if !strings.Contains(string(out), "Stray Note") {
			t.Errorf("%v printed no frontmatter:\n%s", argv, out)
		}
	}

	if after := mtimeOf(t, notePath); !after.Equal(before) {
		t.Errorf("a meta read changed the note's mtime: %v -> %v", before, after)
	}
}

func TestWriteGate_MetaSetAndRemoveStillRefused(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote)

	for _, argv := range [][]string{
		{"meta", "n.md", "--set", "status=active"},
		{"meta", "n.md", "--remove", "status"},
	} {
		out, err := runCLIArgs(t, stray, argv...)
		if err == nil {
			t.Fatalf("%v was allowed on a vault Obsidian does not know:\n%s", argv, out)
		}
		if !strings.Contains(err.Error(), "unconfigured") {
			t.Errorf("%v refusal should name the unconfigured vault, got: %v", argv, err)
		}
	}
}

// A flag combination that cannot run must be refused before the vault is opened,
// so the error a user sees names the flags, not the vault.
func TestReadGate_MetaFlagConflictIsRefusedBeforeTheOpen(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote)

	_, err := runCLIArgs(t, stray, "meta", "n.md", "--get", "title", "--set", "status=active")
	if err == nil {
		t.Fatal("--get with --set was accepted")
	}
	if !strings.Contains(err.Error(), "--get cannot be combined") {
		t.Errorf("refusal should name the flag conflict, got: %v", err)
	}
	if strings.Contains(err.Error(), "unconfigured") {
		t.Errorf("the flag conflict must be reported before the vault open, got: %v", err)
	}
}

func TestReadGate_DailyOnAnExistingNoteNeedsNoUnconfigured(t *testing.T) {
	stem := time.Now().Format("2006-01-02")
	stray := newStrayVaultWithNote(t, stem+".md",
		"---\ntitle: "+stem+"\ntype: note\nstatus: draft\n---\n\nToday's entry.\n")
	notePath := filepath.Join(stray, stem+".md")
	before := mtimeOf(t, notePath)

	for _, argv := range [][]string{
		{"daily"},
		{"daily", "path"},
		{"daily", "read"},
	} {
		out, err := runCLIArgs(t, stray, argv...)
		if err != nil {
			t.Fatalf("%v was refused on an existing daily note: %v\n%s", argv, err, out)
		}
	}

	if after := mtimeOf(t, notePath); !after.Equal(before) {
		t.Errorf("a daily read changed the note's mtime: %v -> %v", before, after)
	}
}

// Creating today's note IS a write, so the guard still applies to it, and to
// every daily body write.
func TestWriteGate_DailyCreateAndAppendStillRefused(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote) // no daily note today
	stem := time.Now().Format("2006-01-02")

	for _, argv := range [][]string{
		{"daily"},
		{"daily", "read"},
		{"daily", "append", "--text", "hello"},
		{"daily", "prepend", "--text", "hello"},
	} {
		out, err := runCLIArgs(t, stray, argv...)
		if err == nil {
			t.Fatalf("%v created or wrote a daily note in a vault Obsidian does not know:\n%s", argv, out)
		}
		if !strings.Contains(err.Error(), "unconfigured") {
			t.Errorf("%v refusal should name the unconfigured vault, got: %v", argv, err)
		}
	}

	if _, err := os.Stat(filepath.Join(stray, stem+".md")); !os.IsNotExist(err) {
		t.Errorf("a refused daily invocation still created %s.md", stem)
	}
}

// resolveDailyNote is the read half of the daily opener: it must resolve and
// report existence without creating anything.
func TestResolveDailyNoteCreatesNothing(t *testing.T) {
	root := newResolveTestVault(t)
	v, err := vault.Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer v.Close()

	now := time.Now()
	relPath, absPath, exists, err := resolveDailyNote(v, now)
	if err != nil {
		t.Fatalf("resolveDailyNote: %v", err)
	}
	if exists {
		t.Fatalf("a fresh vault reported today's note as existing at %q", relPath)
	}
	if _, statErr := os.Stat(absPath); !os.IsNotExist(statErr) {
		t.Errorf("resolveDailyNote created %s", absPath)
	}

	if err := os.WriteFile(absPath, []byte("# today\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, exists, err = resolveDailyNote(v, now); err != nil || !exists {
		t.Errorf("resolveDailyNote missed an existing note: exists=%v err=%v", exists, err)
	}
}

// `2nb migrate` is two halves with two openers on purpose: the schema check and
// the whole --dry-run preview are READS, and the real run is a write (vault.Open
// applies the migrations and writes .gitignore). Only the second half is gated.

// writeLegacyV2Index replaces a vault's index with the smallest schema-v2
// database the migration path accepts. The battery's writeV2Index lives in the
// e2e_test package and cannot be imported here, so the statements are mirrored;
// they only need the two tables migrate reads.
func writeLegacyV2Index(t *testing.T, vaultRoot string) string {
	t.Helper()
	idx := filepath.Join(vaultRoot, ".2ndbrain", "index.db")
	for _, p := range []string{idx, idx + "-wal", idx + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", p, err)
		}
	}
	db, err := sql.Open("sqlite", idx)
	if err != nil {
		t.Fatalf("open v2 db: %v", err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE documents (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL DEFAULT '',
			doc_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			modified_at TEXT NOT NULL DEFAULT '',
			indexed_at TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			frontmatter TEXT NOT NULL DEFAULT '{}',
			embedding BLOB,
			embedding_model TEXT NOT NULL DEFAULT '',
			embedding_hash TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version (version) VALUES (2)`,
		`INSERT INTO documents (id, path, title, doc_type) VALUES ('m1', 'n.md', 'Stray Note', 'note')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("build v2 db (%q): %v", stmt, err)
		}
	}
	return idx
}

func indexSchemaVersion(t *testing.T, idx string) int {
	t.Helper()
	db, err := sql.Open("sqlite", idx)
	if err != nil {
		t.Fatalf("open %s: %v", idx, err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&v); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	return v
}

func TestReadGate_MigrateDryRunNeedsNoUnconfigured(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote)
	idx := writeLegacyV2Index(t, stray)
	before := mtimeOf(t, idx)

	out, err := runCLIArgs(t, stray, "migrate", "--dry-run")
	if err != nil {
		t.Fatalf("migrate --dry-run was refused on a vault Obsidian does not know: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "[dry-run]") {
		t.Errorf("dry-run printed no preview:\n%s", out)
	}
	if got := indexSchemaVersion(t, idx); got != 2 {
		t.Errorf("dry-run migrated the index: schema = v%d, want v2", got)
	}
	if after := mtimeOf(t, idx); !after.Equal(before) {
		t.Errorf("dry-run changed the index mtime: %v -> %v", before, after)
	}
}

func TestWriteGate_MigrateRealRunIsRefusedWithoutUnconfigured(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote)
	idx := writeLegacyV2Index(t, stray)
	before := mtimeOf(t, idx)

	out, err := runCLIArgs(t, stray, "migrate")
	if err == nil {
		t.Fatalf("migrate wrote to a vault Obsidian does not know:\n%s", out)
	}
	if !strings.Contains(err.Error(), "unconfigured") {
		t.Errorf("refusal should name the unconfigured vault, got: %v", err)
	}
	if got := indexSchemaVersion(t, idx); got != 2 {
		t.Errorf("a refused migrate still upgraded the schema to v%d", got)
	}
	if after := mtimeOf(t, idx); !after.Equal(before) {
		t.Errorf("a refused migrate touched the index: mtime %v -> %v", before, after)
	}
}

func TestWriteGate_MigrateRealRunProceedsWithUnconfigured(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote)
	idx := writeLegacyV2Index(t, stray)

	out, err := runCLIArgs(t, stray, "migrate", "--unconfigured")
	if err != nil {
		t.Fatalf("migrate --unconfigured: %v\n%s", err, out)
	}
	if got := indexSchemaVersion(t, idx); got != store.MaxSchemaVersion {
		t.Errorf("after an acknowledged migrate, schema = v%d, want v%d", got, store.MaxSchemaVersion)
	}
}

// Ordering is deliberate: the pre-check runs BEFORE the write opener, so a
// vault already at the current schema is answered without opening a write path
// at all. That is correct precisely because nothing is written on that branch,
// and it means a native stray vault reports "nothing to migrate" rather than a
// refusal for a write that was never going to happen.
func TestReadGate_MigrateOnANativeVaultAnswersBeforeTheWriteGate(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote) // vault.Init left it at the current schema
	idx := filepath.Join(stray, ".2ndbrain", "index.db")
	before := indexSchemaVersion(t, idx)

	out, err := runCLIArgs(t, stray, "migrate")
	if err != nil {
		t.Fatalf("migrate on a native vault: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "nothing to migrate") {
		t.Errorf("want the nothing-to-migrate answer, got:\n%s", out)
	}
	if strings.Contains(string(out), "refusing to write") {
		t.Errorf("a branch that writes nothing must not hit the write gate:\n%s", out)
	}
	if got := indexSchemaVersion(t, idx); got != before {
		t.Errorf("the no-op branch changed the schema: v%d -> v%d", before, got)
	}
}

// MAX(version) over an EMPTY schema_version table is SQL NULL, and scanning
// that into an int surfaced modernc's "converting NULL to int is unsupported",
// which tells a user nothing about their vault or what to do next.
func TestMigrate_EmptySchemaVersionTableIsExplained(t *testing.T) {
	stray := newStrayVaultWithNote(t, "n.md", readGateNote)
	idx := writeLegacyV2Index(t, stray)
	db, err := sql.Open("sqlite", idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM schema_version"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = runCLIArgs(t, stray, "migrate", "--dry-run")
	if err == nil {
		t.Fatal("migrate accepted an index with no schema level")
	}
	msg := err.Error()
	if strings.Contains(msg, "converting NULL") {
		t.Errorf("the driver error still reaches the user: %v", err)
	}
	if !strings.Contains(msg, "empty schema_version") || !strings.Contains(msg, idx) {
		t.Errorf("message should name the empty table and the index file, got: %v", err)
	}
	if !strings.Contains(msg, "2nb index") {
		t.Errorf("message should name the remedy, got: %v", err)
	}
}

// migrate's two ladders can in principle resolve different vaults: the read
// ladder walks up from the cwd for the pre-check, while the write ladder never
// accepts a walked-up cwd and prefers the vault Obsidian points at. The mismatch
// branch exists so a migration is never REPORTED with another vault's schema
// numbers.
//
// It cannot be reached from the CLI as the two ladders are written today: an
// explicit --vault and 2NB_VAULT feed both, the Obsidian rung hands the same
// path to each (checked: with a registry marked open:false, ObsidianOpenVault
// and ObsidianActiveVault both return the same root), and a walked-up cwd is refused
// by the write opener before it can diverge. So the branch is tested where it
// lives rather than through an invocation that cannot produce it.
func TestMigrateTargetMismatch(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()

	if err := migrateTargetMismatch(a, a); err != nil {
		t.Errorf("same vault should be accepted, got: %v", err)
	}
	// Canonicalized, so a symlinked or non-cleaned spelling of one root is the
	// same vault, not a mismatch.
	if err := migrateTargetMismatch(a, filepath.Join(a, ".", "")); err != nil {
		t.Errorf("a different spelling of one root should be accepted, got: %v", err)
	}

	err := migrateTargetMismatch(a, b)
	if err == nil {
		t.Fatal("two different vaults were accepted")
	}
	if !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), b) {
		t.Errorf("the refusal should name both roots, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--vault "+a) {
		t.Errorf("the refusal should point at the vault that was checked, got: %v", err)
	}
}
