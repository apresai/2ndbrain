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
