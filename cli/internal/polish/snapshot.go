package polish

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/vault"
)

// SnapshotVersion is the on-disk schema version for a PolishSnapshot.
const SnapshotVersion = 1

// PolishSnapshot captures the state needed to undo one applied polish. It stores
// the FULL pre-write file content (frontmatter + body) so undo restores the
// original byte-for-byte, and the hash of the file AS WRITTEN so undo can detect
// edits made after the polish. Snapshots are latest-only per note (a new polish
// overwrites the prior snapshot) and live under the gitignored
// .2ndbrain/recovery/polish/ directory.
type PolishSnapshot struct {
	Version       int    `json:"version"`
	Path          string `json:"path"` // vault-relative
	OriginalFull  string `json:"original_full_content"`
	PolishedBody  string `json:"polished_body"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Links         bool   `json:"links"`
	Timestamp     string `json:"timestamp"` // RFC3339
	PostWriteHash string `json:"post_write_hash"`
}

// UndoDecision classifies the on-disk file state against a snapshot.
type UndoDecision int

const (
	// UndoProceed: the file is exactly what polish wrote, safe to restore.
	UndoProceed UndoDecision = iota
	// UndoNoop: the file already equals the original, nothing to undo.
	UndoNoop
	// UndoConflict: the file was edited since polish, restoring would discard
	// those edits, so it requires --force.
	UndoConflict
)

// HashContent returns the hex SHA-256 of b, used for snapshot integrity checks.
func HashContent(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func snapshotDir(v *vault.Vault) string {
	return filepath.Join(v.Root, ".2ndbrain", "recovery", "polish")
}

// SnapshotPath is the latest-only, per-note snapshot file for relPath. The path
// is hashed (not embedded) so it is filesystem-safe and collision-free across
// same-basename notes in different folders.
func SnapshotPath(v *vault.Vault, relPath string) string {
	sum := sha256.Sum256([]byte(relPath))
	return filepath.Join(snapshotDir(v), hex.EncodeToString(sum[:8])+".json")
}

// WriteSnapshot persists s atomically (temp + rename), creating the polish
// recovery directory on first use.
func WriteSnapshot(v *vault.Vault, s PolishSnapshot) error {
	if s.Version == 0 {
		s.Version = SnapshotVersion
	}
	dir := snapshotDir(v)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create polish recovery dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	path := SnapshotPath(v, s.Path)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename snapshot: %w", err)
	}
	return nil
}

// LoadSnapshot reads the snapshot for relPath. A missing snapshot returns
// (nil, nil) so callers can distinguish "nothing to undo" from a read error.
func LoadSnapshot(v *vault.Vault, relPath string) (*PolishSnapshot, error) {
	data, err := os.ReadFile(SnapshotPath(v, relPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var s PolishSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse snapshot: %w", err)
	}
	return &s, nil
}

// DeleteSnapshot removes the snapshot for relPath. A missing snapshot is not an
// error (undo is one-shot; double-undo is a no-op).
func DeleteSnapshot(v *vault.Vault, relPath string) error {
	err := os.Remove(SnapshotPath(v, relPath))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}

// ClassifyUndo decides whether undoing is safe given the current on-disk file
// content and the snapshot taken at polish time.
func ClassifyUndo(currentFull []byte, s *PolishSnapshot) UndoDecision {
	cur := HashContent(currentFull)
	if cur == s.PostWriteHash {
		return UndoProceed
	}
	if cur == HashContent([]byte(s.OriginalFull)) {
		return UndoNoop
	}
	return UndoConflict
}
