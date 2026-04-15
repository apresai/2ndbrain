// Package git wraps git CLI operations for vaults that happen to be git
// repositories. Everything is read-only — no commits, no push/pull. If a
// caller needs mutation it should shell out to git itself.
//
// All functions are no-ops (returning zero values and nil errors) when the
// vault is not a git repo, so callers don't need to special-case. Use
// IsRepo() up front if you want to branch the UI.
package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// IsRepo returns true when the given directory (or any ancestor) is a git
// repository that git can talk to.
func IsRepo(root string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--git-dir")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// RepoRoot returns the top-level of the git repository containing root.
func RepoRoot(root string) (string, error) {
	out, err := runGit(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// FileStatus describes a file's state in a single-letter porcelain code.
type FileStatus string

const (
	StatusModified  FileStatus = "M"
	StatusAdded     FileStatus = "A"
	StatusDeleted   FileStatus = "D"
	StatusRenamed   FileStatus = "R"
	StatusUntracked FileStatus = "??"
)

// Change is one commit from `git log`.
type Change struct {
	Hash    string    `json:"hash"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Subject string    `json:"subject"`
	Files   []string  `json:"files"`
}

// StatusFiles returns a map of relpath → status code for the working tree.
// Untracked files use "??"; everything else uses the single-letter XY code
// from `git status --porcelain`.
func StatusFiles(root string) (map[string]string, error) {
	if !IsRepo(root) {
		return map[string]string{}, nil
	}
	out, err := runGit(root, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		code := strings.TrimSpace(line[:2])
		if code == "" {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// Handle renames: `R  old -> new`
		if strings.Contains(path, " -> ") {
			parts := strings.SplitN(path, " -> ", 2)
			if len(parts) == 2 {
				path = parts[1]
			}
		}
		result[path] = code
	}
	return result, nil
}

// DiffFile returns the unified diff of a file versus HEAD. If the file is
// untracked, returns an empty string with no error (untracked files have no
// "previous version" to diff against).
func DiffFile(root, relPath string) (string, error) {
	if !IsRepo(root) {
		return "", nil
	}
	out, err := runGit(root, "diff", "HEAD", "--", relPath)
	if err != nil {
		return "", err
	}
	return out, nil
}

// CommitDetail is the full data for one commit returned by `git show`,
// including per-file diffs. Used by the 2nb app's commit-details modal
// which renders a file list + selectable diff pane.
type CommitDetail struct {
	Hash    string       `json:"hash"`
	Author  string       `json:"author"`
	Date    time.Time    `json:"date"`
	Subject string       `json:"subject"`
	Body    string       `json:"body"`
	Stats   CommitStats  `json:"stats"`
	Files   []CommitFile `json:"files"`
}

// CommitStats summarizes file/line counts for a commit.
type CommitStats struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

// CommitFile is one file touched by a commit, with its diff and counts.
// Merge commits are diffed against the first parent. Binary files have
// Diff == "" and Binary == true so the UI can render a placeholder
// instead of attempting syntax highlighting.
type CommitFile struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	Binary     bool   `json:"binary"`
	Diff       string `json:"diff"`
}

// Show returns the full detail for a commit identified by hash. Hash may
// be any git revision spec (full SHA, short SHA, ref name); git resolves
// it. Returns an error if the repo is not initialized or the hash is
// unknown. Merge commits get diffed against the first parent.
//
// Uses a single `git show` invocation with --numstat --patch, then parses
// the combined output. Previous implementation ran git N+1 times (once
// for numstat + once per file for the patch); for commits touching many
// files this was substantially slower. Output ordering is deterministic:
// header, blank line, numstat block, blank line, per-file patches each
// starting with "diff --git a/...".
func Show(root, hash string) (*CommitDetail, error) {
	if !IsRepo(root) {
		return nil, fmt.Errorf("not a git repository: %s", root)
	}

	// Delimit the header with ASCII field/record separators so commit
	// messages containing newlines or tabs can't break parsing. We mark
	// the end of the header with a sentinel line that's impossible to
	// collide with real git output.
	const fieldSep = "\x1f"
	const headerEnd = "\x1e__2NB_HEADER_END__\x1e"
	format := "--format=" + "%H" + fieldSep + "%an" + fieldSep +
		"%aI" + fieldSep + "%s" + fieldSep + "%b" + headerEnd

	combined, err := runGit(root, "show", "-m", "--first-parent",
		format, "--numstat", "--patch", hash)
	if err != nil {
		return nil, err
	}

	// Split header from numstat+patch body using the sentinel.
	headerIdx := strings.Index(combined, headerEnd)
	if headerIdx == -1 {
		return nil, fmt.Errorf("unexpected git show output for %s: missing header sentinel", hash)
	}
	headerRec := strings.TrimSpace(combined[:headerIdx])
	body := combined[headerIdx+len(headerEnd):]

	parts := strings.SplitN(headerRec, fieldSep, 5)
	if len(parts) < 4 {
		return nil, fmt.Errorf("unexpected git show header format for %s", hash)
	}
	date, _ := time.Parse(time.RFC3339, parts[2])
	detail := &CommitDetail{
		Hash:    parts[0],
		Author:  parts[1],
		Date:    date,
		Subject: parts[3],
	}
	if len(parts) >= 5 {
		detail.Body = strings.TrimRight(parts[4], "\n")
	}

	// The body contains numstat lines (tab-separated add/del/path) then
	// the unified patches. Numstat lines appear first, one per file,
	// before any "diff --git a/" marker. Split on the first "diff --git"
	// occurrence to isolate the numstat section.
	numstatSection := body
	patchSection := ""
	if idx := strings.Index(body, "diff --git "); idx != -1 {
		numstatSection = body[:idx]
		patchSection = body[idx:]
	}

	var files []CommitFile
	var totalIns, totalDel int
	pathIndex := map[string]int{}
	for _, line := range strings.Split(numstatSection, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cols := strings.SplitN(line, "\t", 3)
		if len(cols) != 3 {
			continue
		}
		binary := cols[0] == "-" && cols[1] == "-"
		var add, del int
		if !binary {
			add, _ = strconv.Atoi(cols[0])
			del, _ = strconv.Atoi(cols[1])
		}
		totalIns += add
		totalDel += del
		pathIndex[cols[2]] = len(files)
		files = append(files, CommitFile{
			Path:      cols[2],
			Additions: add,
			Deletions: del,
			Binary:    binary,
		})
	}

	// Parse the patch section: each per-file patch starts with a line
	// like "diff --git a/<path> b/<path>". Splitting on "\ndiff --git "
	// gives us individual patch chunks; the first split produces the
	// first patch (minus the leading "diff --git " prefix).
	if patchSection != "" {
		// Ensure the first chunk also starts with "diff --git " for
		// uniform handling below.
		chunks := strings.Split("\n"+patchSection, "\ndiff --git ")
		for _, chunk := range chunks {
			chunk = strings.TrimSpace(chunk)
			if chunk == "" {
				continue
			}
			// Extract the "a/<path>" portion from the first line.
			// Form: "a/<path> b/<path>\n<rest>"
			firstLineEnd := strings.IndexByte(chunk, '\n')
			if firstLineEnd == -1 {
				continue
			}
			firstLine := chunk[:firstLineEnd]
			// The path spec is "a/<path> b/<path>" — take everything
			// between "a/" and " b/".
			aIdx := strings.Index(firstLine, "a/")
			bIdx := strings.Index(firstLine, " b/")
			if aIdx == -1 || bIdx == -1 || bIdx <= aIdx {
				continue
			}
			path := firstLine[aIdx+2 : bIdx]
			idx, ok := pathIndex[path]
			if !ok {
				// Numstat didn't report this path (unusual). Skip.
				continue
			}
			if files[idx].Binary {
				// Binary files have no textual diff; leave Diff empty.
				continue
			}
			// Reconstruct the full patch including the "diff --git "
			// prefix that was consumed by the split.
			files[idx].Diff = "diff --git " + chunk + "\n"
		}
	}

	detail.Stats = CommitStats{
		FilesChanged: len(files),
		Insertions:   totalIns,
		Deletions:    totalDel,
	}
	detail.Files = files
	return detail, nil
}

// Activity returns commits within the last `since` duration that touched
// files inside the repo.
func Activity(root string, since time.Duration) ([]Change, error) {
	if !IsRepo(root) {
		return []Change{}, nil
	}
	sinceArg := fmt.Sprintf("--since=%d.seconds", int(since.Seconds()))
	// %x1e is a record separator, %x1f a field separator. Using control
	// characters keeps the parser unambiguous even when commit messages
	// contain tabs or newlines.
	const recordSep = "\x1e"
	const fieldSep = "\x1f"
	format := "--pretty=format:" + recordSep + "%H" + fieldSep + "%an" + fieldSep + "%aI" + fieldSep + "%s"
	out, err := runGit(root, "log", sinceArg, "--name-only", format)
	if err != nil {
		return nil, err
	}
	var changes []Change
	records := strings.Split(out, recordSep)
	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		// A record is: hash\x1fauthor\x1fiso8601\x1fsubject\nfile1\nfile2\n...
		headAndFiles := strings.SplitN(rec, "\n", 2)
		head := headAndFiles[0]
		parts := strings.SplitN(head, fieldSep, 4)
		if len(parts) < 4 {
			continue
		}
		date, _ := time.Parse(time.RFC3339, parts[2])
		change := Change{
			Hash:    parts[0],
			Author:  parts[1],
			Date:    date,
			Subject: parts[3],
		}
		if len(headAndFiles) == 2 {
			for _, f := range strings.Split(strings.TrimSpace(headAndFiles[1]), "\n") {
				if f != "" {
					change.Files = append(change.Files, f)
				}
			}
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// runGit runs `git <args...>` inside root and returns stdout.
func runGit(root string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// RelPath returns a normalized relative path for display/lookups.
func RelPath(base, abs string) string {
	if rel, err := filepath.Rel(base, abs); err == nil {
		return rel
	}
	return abs
}
