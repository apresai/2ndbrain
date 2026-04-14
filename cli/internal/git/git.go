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
