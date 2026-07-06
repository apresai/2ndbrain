// Package instructions writes a small, managed "2ndbrain" reference block into a
// user's global agent memory file (e.g. ~/.claude/CLAUDE.md), so an AI agent
// loads a lightweight always-on pointer to 2nb without a per-project skill
// install. It is the always-loaded complement to the installable skill
// (internal/skills): the skill carries the full guidance, this block just tells
// an agent 2nb exists and when to reach for it.
//
// The block is delimited by HTML-comment sentinels and version/content-sha
// stamped, so it can be updated in place, detected, and removed without touching
// the surrounding user content. Writes are backup-first (<file>.bak) and atomic
// (temp + rename), mirroring the skills and mcp-install writers.
package instructions

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed content/instructions.md
var blockBody string

// Version is stamped into the managed block's BEGIN marker so an installed block
// is self-describing. The CLI sets it from its ldflags Version (this package
// can't import cli — that would be an import cycle), mirroring skills.Version.
var Version = "dev"

const (
	// beginPrefix is the stable prefix of the BEGIN marker; the full line carries
	// a trailing "| version: X | sha: Y -->" stamp, so detection matches by prefix.
	beginPrefix = "<!-- BEGIN 2nb managed instructions"
	endMarker   = "<!-- END 2nb managed instructions -->"
)

// Result reports what Install/Uninstall did.
type Result struct {
	Path      string `json:"path"`
	Backup    string `json:"backup,omitempty"`
	Changed   bool   `json:"changed"`
	Installed bool   `json:"installed"`
	DryRun    bool   `json:"dry_run,omitempty"`
}

// Status reports whether the managed block is present in a memory file and how
// it compares to the binary's current content. Client is set by the caller (the
// CLI), since this package is client-agnostic.
type Status struct {
	Client           string `json:"client,omitempty"`
	Path             string `json:"file_path"`
	Installed        bool   `json:"installed"`
	UpToDate         bool   `json:"up_to_date"`
	Modified         bool   `json:"modified"`
	InstalledVersion string `json:"installed_version,omitempty"`
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// bodySHA is the sha256 of the embedded block body (trailing newline trimmed) —
// the binary's source of truth for "what the installed block should contain".
func bodySHA() string { return sha256Hex(strings.TrimRight(blockBody, "\n")) }

// managedBlock returns the full BEGIN...END block for this binary, with the
// version and content sha stamped into the BEGIN marker.
func managedBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s | version: %s | sha: %s -->\n", beginPrefix, Version, bodySHA())
	b.WriteString(strings.TrimRight(blockBody, "\n"))
	b.WriteString("\n")
	b.WriteString(endMarker)
	return b.String()
}

// parseMarker extracts version and sha from a BEGIN marker line like
// "<!-- BEGIN 2nb managed instructions | version: 0.13.2 | sha: abc -->".
func parseMarker(line string) (ver, sha string) {
	inner := strings.TrimSpace(line)
	inner = strings.TrimPrefix(inner, "<!--")
	inner = strings.TrimSuffix(strings.TrimSpace(inner), "-->")
	for _, part := range strings.Split(inner, "|") {
		p := strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(p, "version:"); ok {
			ver = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(p, "sha:"); ok {
			sha = strings.TrimSpace(v)
		}
	}
	return ver, sha
}

// statusFromContent classifies a memory file's content against the binary's
// embedded block.
func statusFromContent(content, path string) Status {
	st := Status{Path: path}
	lines := strings.Split(content, "\n")
	begin, end := -1, -1
	var ver, claimed string
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if begin == -1 && strings.HasPrefix(t, beginPrefix) {
			begin = i
			ver, claimed = parseMarker(t)
			continue
		}
		if begin != -1 && t == endMarker {
			end = i
			break
		}
	}
	if begin == -1 || end == -1 || end <= begin {
		return st // no complete block
	}
	st.Installed = true
	st.InstalledVersion = ver
	body := strings.Join(lines[begin+1:end], "\n")
	st.UpToDate = claimed == bodySHA()
	st.Modified = claimed != "" && sha256Hex(strings.TrimRight(body, "\n")) != claimed
	return st
}

// stripBlock removes COMPLETE managed blocks (a BEGIN with a matching END)
// from content, leaving all surrounding user content intact. A dangling BEGIN
// with no following END (a corrupted/hand-truncated block) is NOT stripped: its
// lines are flushed back verbatim, so this never drops content to EOF. Install
// refuses such a file without --force (see hasDanglingBegin).
func stripBlock(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	var pending []string // buffered lines since an unmatched BEGIN
	inBlock := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !inBlock && strings.HasPrefix(t, beginPrefix) {
			inBlock = true
			pending = []string{ln}
			continue
		}
		if inBlock {
			pending = append(pending, ln)
			if t == endMarker {
				inBlock = false // complete block: drop the whole buffer
				pending = nil
			}
			continue
		}
		out = append(out, ln)
	}
	// A BEGIN with no matching END is not a complete block: keep its lines.
	out = append(out, pending...)
	return strings.Join(out, "\n")
}

// hasDanglingBegin reports whether content has a BEGIN marker with no matching
// END after it — a corrupted block that stripBlock deliberately leaves intact.
func hasDanglingBegin(content string) bool {
	begin := false
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		if !begin && strings.HasPrefix(t, beginPrefix) {
			begin = true
			continue
		}
		if begin && t == endMarker {
			return false
		}
	}
	return begin
}

// injectBlock appends block after the (block-stripped) content, preserving user
// text and separating with a blank line. An empty file becomes just the block.
func injectBlock(stripped, block string) string {
	s := strings.TrimRight(stripped, "\n")
	if s == "" {
		return block + "\n"
	}
	return s + "\n\n" + block + "\n"
}

// Configured reports the managed-block status of a memory file. A missing file
// is reported as not-installed (no error).
func Configured(memPath string) (Status, error) {
	data, err := os.ReadFile(memPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Status{Path: memPath}, nil
		}
		return Status{Path: memPath}, fmt.Errorf("read %s: %w", memPath, err)
	}
	return statusFromContent(string(data), memPath), nil
}

// Install writes (or refreshes) the managed block in memPath, creating the file
// (and parent dir) if needed. It is idempotent: an already-current, unmodified
// block is left untouched (no rewrite, no .bak churn on version-only bumps). A
// hand-edited block is refused unless force is set. On any real write the prior
// file is backed up to <file>.bak and replaced atomically.
func Install(memPath string, force, dryRun bool) (Result, error) {
	res := Result{Path: memPath, DryRun: dryRun}
	data, err := os.ReadFile(memPath)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return res, fmt.Errorf("read %s: %w", memPath, err)
	}
	existing := string(data)
	st := statusFromContent(existing, memPath)

	if st.Installed && st.Modified && !force {
		return res, fmt.Errorf("the 2nb block in %s was hand-edited since install; re-run with --force to overwrite", memPath)
	}
	// A dangling BEGIN with no END is a corrupted block statusFromContent can't
	// classify as installed. Refuse rather than append a second block next to the
	// orphan; --force proceeds (stripBlock preserves the orphan's content).
	if !st.Installed && hasDanglingBegin(existing) && !force {
		return res, fmt.Errorf("the 2nb block markers in %s are malformed (BEGIN with no END); fix by hand or re-run with --force", memPath)
	}
	res.Installed = true
	// Already current and untouched: nothing to do.
	if st.Installed && st.UpToDate && !st.Modified && !force {
		return res, nil
	}

	updated := injectBlock(stripBlock(existing), managedBlock())
	if strings.TrimRight(existing, "\n") == strings.TrimRight(updated, "\n") {
		return res, nil // defensive: byte-identical, no change
	}
	res.Changed = true
	if dryRun {
		return res, nil
	}

	if existed {
		res.Backup = memPath + ".bak"
		if werr := os.WriteFile(res.Backup, data, fileMode(memPath)); werr != nil {
			return res, fmt.Errorf("write backup %s: %w", res.Backup, werr)
		}
	} else if merr := os.MkdirAll(filepath.Dir(memPath), 0o755); merr != nil {
		return res, fmt.Errorf("create dir: %w", merr)
	}
	if werr := atomicWrite(memPath, []byte(updated)); werr != nil {
		return res, werr
	}
	return res, nil
}

// Uninstall removes the managed block from memPath (backing up first), leaving
// the surrounding user content. A missing file or absent block is a no-op.
func Uninstall(memPath string) (Result, error) {
	res := Result{Path: memPath}
	data, err := os.ReadFile(memPath)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, fmt.Errorf("read %s: %w", memPath, err)
	}
	existing := string(data)
	stripped := strings.TrimRight(stripBlock(existing), "\n")
	if strings.TrimRight(existing, "\n") == stripped {
		return res, nil // no block present
	}
	res.Changed = true
	res.Backup = memPath + ".bak"
	if werr := os.WriteFile(res.Backup, data, fileMode(memPath)); werr != nil {
		return res, fmt.Errorf("write backup %s: %w", res.Backup, werr)
	}
	out := stripped
	if out != "" {
		out += "\n"
	}
	if werr := atomicWrite(memPath, []byte(out)); werr != nil {
		return res, werr
	}
	return res, nil
}

func fileMode(path string) os.FileMode {
	if fi, err := os.Stat(path); err == nil {
		return fi.Mode().Perm()
	}
	return 0o644
}

// atomicWrite replaces path via a temp file + rename, resolving symlinks so an
// editor-managed symlinked CLAUDE.md isn't clobbered.
func atomicWrite(path string, content []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	tmp := fmt.Sprintf("%s.tmp.%d", target, os.Getpid())
	if err := os.WriteFile(tmp, content, fileMode(target)); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}
