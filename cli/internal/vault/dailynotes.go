package vault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DailyNotesConfig mirrors the subset of Obsidian's core "daily notes" plugin
// configuration that DailyNotePath needs. Obsidian stores it at
// <vault>/.obsidian/daily-notes.json. The plugin may be disabled or never
// configured, in which case the file is absent and Obsidian's own defaults
// apply (folder = vault root, format = "YYYY-MM-DD").
type DailyNotesConfig struct {
	// Folder is the vault-relative directory new daily notes are placed in.
	// "" (the Obsidian default) means the vault root.
	Folder string `json:"folder"`
	// Format is a Moment.js date format string. "" defaults to "YYYY-MM-DD".
	Format string `json:"format"`
	// Template is an optional vault-relative path to a template note. We read
	// it so callers can honor it; an absent value means no template.
	Template string `json:"template"`
}

const defaultDailyNoteFormat = "YYYY-MM-DD"

// LoadDailyNotesConfig reads <vault>/.obsidian/daily-notes.json and returns the
// effective daily-notes configuration. A missing file, an absent field, or a
// disabled plugin all fall back to Obsidian's defaults (folder = "" = vault
// root, format = "YYYY-MM-DD"); it never hard-errors on those cases. A genuinely
// malformed (non-JSON) config is returned as an error so the caller can decide,
// but a missing file is not an error.
func LoadDailyNotesConfig(v *Vault) (DailyNotesConfig, error) {
	cfg := DailyNotesConfig{Format: defaultDailyNoteFormat}

	path := filepath.Join(v.Root, ".obsidian", "daily-notes.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Plugin never configured / disabled: Obsidian defaults apply.
			return cfg, nil
		}
		return cfg, fmt.Errorf("read daily-notes.json: %w", err)
	}

	// An empty or whitespace-only file is a plausible sync / partial-write
	// artifact; treat it as "no config" and fall back to defaults rather than
	// hard-erroring on json.Unmarshal ("unexpected end of JSON input").
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}

	var parsed DailyNotesConfig
	if err := json.Unmarshal(data, &parsed); err != nil {
		return cfg, fmt.Errorf("parse daily-notes.json: %w", err)
	}

	// Apply only the fields Obsidian actually wrote; leave the default format
	// in place when the field is absent or empty.
	cfg.Folder = parsed.Folder
	if strings.TrimSpace(parsed.Format) != "" {
		cfg.Format = parsed.Format
	}
	cfg.Template = parsed.Template
	return cfg, nil
}

// DailyNotePath returns the vault-relative path (with a .md extension) of the
// daily note for the given day, resolved from the vault's Obsidian daily-notes
// configuration. The time t is supplied by the caller (the command passes
// time.Now()) so the result is deterministic and testable.
//
// The path is folder + "/" + formatted-date + ".md", where the date is rendered
// by translating the configured Moment.js format to a Go time layout (see
// momentToGoLayout for the supported token subset). Slashes inside the format
// produce nested subdirectories, matching Obsidian's behavior with formats like
// "YYYY/MM/DD".
func DailyNotePath(v *Vault, t time.Time) (string, error) {
	cfg, err := LoadDailyNotesConfig(v)
	if err != nil {
		return "", err
	}
	return dailyNotePathFromConfig(cfg, t)
}

// dailyNotePathFromConfig is the pure resolver: given a config and a time it
// returns the vault-relative path. Split out so tests can exercise the
// folder/format math without touching disk.
func dailyNotePathFromConfig(cfg DailyNotesConfig, t time.Time) (string, error) {
	layout, err := momentToGoLayout(cfg.Format)
	if err != nil {
		return "", err
	}
	name := t.Format(layout) + ".md"

	// filepath.Join cleans the result and normalizes separators, so a format
	// like "YYYY/MM/DD" yields nested directories and a folder of "" (the
	// default) yields the bare filename at the vault root.
	rel := filepath.Join(filepath.FromSlash(cfg.Folder), filepath.FromSlash(name))
	return rel, nil
}

// momentToGoLayout translates the common Moment.js date tokens Obsidian uses in
// daily-note formats into a Go reference-time layout. Supported tokens:
//
//	YYYY -> 2006  (4-digit year)
//	YY   -> 06    (2-digit year)
//	MM   -> 01    (zero-padded month)
//	M    -> 1     (month, no padding)
//	DD   -> 02    (zero-padded day of month)
//	D    -> 2     (day of month, no padding)
//
// Everything else (separators like "-", "/", "_", "." and literal text) passes
// through verbatim. This is deliberately a SMALL subset: exotic Moment tokens
// (day names, month names, hours/minutes, locale-aware ordinals, escaped
// literals in [brackets], etc.) are NOT supported and would pass through as
// literal characters, which is wrong for those tokens. Daily notes in practice
// use a date-only format built from the tokens above, so the subset covers the
// real-world cases; callers wanting an exotic format should configure a plainer
// one in Obsidian.
//
// Token matching is longest-first (YYYY before YY, MM before M, DD before D) so
// a longer token is never partially consumed by a shorter one.
func momentToGoLayout(format string) (string, error) {
	if strings.TrimSpace(format) == "" {
		format = defaultDailyNoteFormat
	}

	// Ordered longest-first within each field so "YYYY" is matched before "YY"
	// and "MM"/"DD" before "M"/"D".
	type tok struct {
		moment string
		golang string
	}
	tokens := []tok{
		{"YYYY", "2006"},
		{"YY", "06"},
		{"MM", "01"},
		{"M", "1"},
		{"DD", "02"},
		{"D", "2"},
	}

	var b strings.Builder
	i := 0
	for i < len(format) {
		matched := false
		for _, tk := range tokens {
			if strings.HasPrefix(format[i:], tk.moment) {
				b.WriteString(tk.golang)
				i += len(tk.moment)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// Literal passthrough for separators and any unsupported character.
		b.WriteByte(format[i])
		i++
	}

	return b.String(), nil
}
