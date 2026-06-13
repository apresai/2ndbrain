package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixedDay is a deterministic reference day used across the daily-notes tests
// so the formatted output is predictable: 2026-06-13 (a Saturday).
var fixedDay = time.Date(2026, 6, 13, 9, 30, 0, 0, time.UTC)

func TestMomentToGoLayout(t *testing.T) {
	cases := []struct {
		name     string
		moment   string
		wantDate string // fixedDay rendered through the resulting layout
	}{
		{"default dash", "YYYY-MM-DD", "2026-06-13"},
		{"slash nested", "YYYY/MM/DD", "2026/06/13"},
		{"two digit year", "YY-MM-DD", "26-06-13"},
		{"unpadded month day", "YYYY-M-D", "2026-6-13"},
		{"underscore", "YYYY_MM_DD", "2026_06_13"},
		{"dot separated", "YYYY.MM.DD", "2026.06.13"},
		{"month first", "MM-DD-YYYY", "06-13-2026"},
		{"empty falls back to default", "", "2026-06-13"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layout, err := momentToGoLayout(tc.moment)
			if err != nil {
				t.Fatalf("momentToGoLayout(%q): %v", tc.moment, err)
			}
			got := fixedDay.Format(layout)
			if got != tc.wantDate {
				t.Errorf("format %q -> layout %q -> %q, want %q", tc.moment, layout, got, tc.wantDate)
			}
		})
	}
}

// TestFormatMoment covers the renderer that honors Moment's [...] bracket
// escaping. A Go time layout has no escape mechanism, so a literal that spells a
// Go token (e.g. "Mon", "Jan") would be misinterpreted if fed straight to
// t.Format; formatMoment emits bracket literals verbatim and renders only the
// token runs around them. fixedDay is 2026-06-13, a Saturday.
func TestFormatMoment(t *testing.T) {
	cases := []struct {
		name   string
		moment string
		want   string
	}{
		// Bracket-escaped literals: the brackets are dropped and the inner text
		// renders verbatim, while surrounding tokens still translate.
		{"leading literal then token", "[Week] YYYY", "Week 2026"},
		{"token then trailing literal", "YYYY-[backup]", "2026-backup"},
		// A literal whose text spells a Go layout token ("Mon" = weekday) must
		// stay literal, NOT render as the day name (Sat).
		{"literal that collides with go token", "[Mon]", "Mon"},
		{"literal Jan stays literal", "[Jan]-YYYY", "Jan-2026"},
		// Literal embedded between two token runs.
		{"literal between tokens", "YYYY[at]MM", "2026at06"},
		// Unclosed bracket: treat the remainder as a literal (sane fallback).
		{"unclosed bracket is literal", "YYYY-[oops", "2026-oops"},
		// Empty brackets emit nothing.
		{"empty brackets", "YYYY[]MM", "202606"},
		// No brackets: identical to the plain token translation.
		{"plain format unchanged", "YYYY-MM-DD", "2026-06-13"},
		{"empty falls back to default", "", "2026-06-13"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatMoment(tc.moment, fixedDay)
			if got != tc.want {
				t.Errorf("formatMoment(%q) = %q, want %q", tc.moment, got, tc.want)
			}
		})
	}
}

func TestLoadDailyNotesConfig_Absent(t *testing.T) {
	v := newDailyTestVault(t)

	// No .obsidian/daily-notes.json written: defaults apply, no error.
	cfg, err := LoadDailyNotesConfig(v)
	if err != nil {
		t.Fatalf("LoadDailyNotesConfig (absent): %v", err)
	}
	if cfg.Folder != "" {
		t.Errorf("absent config folder = %q, want empty (vault root)", cfg.Folder)
	}
	if cfg.Format != defaultDailyNoteFormat {
		t.Errorf("absent config format = %q, want %q", cfg.Format, defaultDailyNoteFormat)
	}
}

// TestLoadDailyNotesConfig_EmptyFile guards the fix for an empty or
// whitespace-only daily-notes.json (a plausible sync / partial-write artifact):
// it must fall back to Obsidian defaults rather than hard-error on json
// "unexpected end of JSON input".
func TestLoadDailyNotesConfig_EmptyFile(t *testing.T) {
	for _, body := range []string{"", "   ", "\n\t\n"} {
		v := newDailyTestVault(t)
		dir := filepath.Join(v.Root, ".obsidian")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "daily-notes.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDailyNotesConfig(v)
		if err != nil {
			t.Fatalf("empty config (%q) should not error: %v", body, err)
		}
		if cfg.Folder != "" || cfg.Format != defaultDailyNoteFormat {
			t.Errorf("empty config (%q) -> %+v, want defaults (root, %q)", body, cfg, defaultDailyNoteFormat)
		}
	}
}

func TestLoadDailyNotesConfig_Custom(t *testing.T) {
	v := newDailyTestVault(t)
	writeDailyNotesJSON(t, v, `{"folder":"journal/daily","format":"YYYY/MM/DD","template":"templates/daily.md"}`)

	cfg, err := LoadDailyNotesConfig(v)
	if err != nil {
		t.Fatalf("LoadDailyNotesConfig (custom): %v", err)
	}
	if cfg.Folder != "journal/daily" {
		t.Errorf("folder = %q, want journal/daily", cfg.Folder)
	}
	if cfg.Format != "YYYY/MM/DD" {
		t.Errorf("format = %q, want YYYY/MM/DD", cfg.Format)
	}
	if cfg.Template != "templates/daily.md" {
		t.Errorf("template = %q, want templates/daily.md", cfg.Template)
	}
}

func TestLoadDailyNotesConfig_Partial(t *testing.T) {
	v := newDailyTestVault(t)
	// Only a folder is set: the format must fall back to the default.
	writeDailyNotesJSON(t, v, `{"folder":"daily"}`)

	cfg, err := LoadDailyNotesConfig(v)
	if err != nil {
		t.Fatalf("LoadDailyNotesConfig (partial): %v", err)
	}
	if cfg.Folder != "daily" {
		t.Errorf("folder = %q, want daily", cfg.Folder)
	}
	if cfg.Format != defaultDailyNoteFormat {
		t.Errorf("partial config format = %q, want default %q", cfg.Format, defaultDailyNoteFormat)
	}
}

func TestDailyNotePath(t *testing.T) {
	cases := []struct {
		name     string
		json     string // "" means no config file (defaults)
		wantPath string
	}{
		{
			name:     "defaults: root + YYYY-MM-DD",
			json:     "",
			wantPath: "2026-06-13.md",
		},
		{
			name:     "custom folder, default format",
			json:     `{"folder":"daily"}`,
			wantPath: filepath.Join("daily", "2026-06-13.md"),
		},
		{
			name:     "nested folder + slash format",
			json:     `{"folder":"journal","format":"YYYY/MM/DD"}`,
			wantPath: filepath.Join("journal", "2026", "06", "13.md"),
		},
		{
			name:     "custom dotted format at root",
			json:     `{"format":"YYYY.MM.DD"}`,
			wantPath: "2026.06.13.md",
		},
		{
			// Bracket-escaped literal in the filename, verified end-to-end
			// through the resolver: "[Week-]GGGG" is not a real Obsidian format,
			// but "[Daily] YYYY-MM-DD" is a common pattern. The literal "Daily "
			// must survive verbatim.
			name:     "bracket literal in filename",
			json:     `{"folder":"journal","format":"[Daily] YYYY-MM-DD"}`,
			wantPath: filepath.Join("journal", "Daily 2026-06-13.md"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newDailyTestVault(t)
			if tc.json != "" {
				writeDailyNotesJSON(t, v, tc.json)
			}
			got, err := DailyNotePath(v, fixedDay)
			if err != nil {
				t.Fatalf("DailyNotePath: %v", err)
			}
			if got != tc.wantPath {
				t.Errorf("DailyNotePath = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

// newDailyTestVault initializes a fresh vault in a temp dir and returns it.
func newDailyTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := Init(t.TempDir())
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

// writeDailyNotesJSON writes the given JSON into <vault>/.obsidian/daily-notes.json.
func writeDailyNotesJSON(t *testing.T, v *Vault, body string) {
	t.Helper()
	dir := filepath.Join(v.Root, ".obsidian")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .obsidian: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daily-notes.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write daily-notes.json: %v", err)
	}
}
