package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Version is the build version stamped into installed SKILL.md files so an
// installed copy is self-describing. The CLI sets it from its ldflags Version
// (skills can't import the cli package — that would be an import cycle).
var Version = "dev"

const (
	stampVersionKey = "x-2nb-version"
	stampSHAKey     = "x-2nb-content-sha"
)

// canonicalContentSHA is the sha256 of the embedded (unstamped) skill content —
// the binary's source of truth for "what the installed skill should be".
func canonicalContentSHA() string {
	return sha256Hex(coreContent)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// StampedContent returns the embedded skill content with x-2nb-version and
// x-2nb-content-sha injected into the frontmatter, so a later `skills doctor`
// can distinguish a fresh install from a stale or hand-edited one. The stamped
// body, with those two lines removed, is byte-identical to the embedded content.
func StampedContent() string {
	stamp := stampVersionKey + ": " + Version + "\n" + stampSHAKey + ": " + canonicalContentSHA() + "\n"
	const open = "---\n"
	if strings.HasPrefix(coreContent, open) {
		if i := strings.Index(coreContent[len(open):], "\n---"); i >= 0 {
			insertAt := len(open) + i + 1 // start of the closing "---"
			return coreContent[:insertAt] + stamp + coreContent[insertAt:]
		}
	}
	return coreContent
}

// Freshness describes how an installed SKILL.md compares to the binary's content.
type Freshness struct {
	Stamped          bool   `json:"stamped"`           // carries the 2nb version/hash stamp
	InstalledVersion string `json:"installed_version"` // x-2nb-version (empty if unstamped)
	UpToDate         bool   `json:"up_to_date"`        // matches the binary's current content
	Modified         bool   `json:"modified"`          // hand-edited since install
}

// FreshnessOf classifies an installed SKILL.md's bytes against the binary's
// embedded content. An unstamped file (an older install, or a hand-made one)
// reports Stamped=false and is treated as not-up-to-date by callers.
func FreshnessOf(data []byte) Freshness {
	s := string(data)
	ver := frontmatterValue(s, stampVersionKey)
	claimed := frontmatterValue(s, stampSHAKey)
	if ver == "" && claimed == "" {
		return Freshness{}
	}
	return Freshness{
		Stamped:          true,
		InstalledVersion: ver,
		UpToDate:         claimed == canonicalContentSHA(),
		Modified:         claimed != "" && sha256Hex(stripStamp(s)) != claimed,
	}
}

// stripStamp removes the two stamp lines, restoring the canonical body of an
// unmodified install.
func stripStamp(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, stampVersionKey+":") || strings.HasPrefix(t, stampSHAKey+":") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// frontmatterValue reads a single key from the leading YAML frontmatter block.
func frontmatterValue(s, key string) string {
	const open = "---\n"
	if !strings.HasPrefix(s, open) {
		return ""
	}
	rest := s[len(open):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	for _, ln := range strings.Split(rest[:end], "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, key+":") {
			return strings.TrimSpace(t[len(key)+1:])
		}
	}
	return ""
}
