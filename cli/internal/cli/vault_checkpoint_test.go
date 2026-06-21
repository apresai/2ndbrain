package cli

import (
	"encoding/json"
	"testing"

	"github.com/apresai/2ndbrain/internal/store"
)

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:               "0B",
		512:             "512B",
		1024:            "1.0KB",
		1536:            "1.5KB",
		1024 * 1024:     "1.0MB",
		3 * 1024 * 1024: "3.0MB",
	}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

// Command-level test of `vault checkpoint --json`: it runs the checkpoint and
// emits the CheckpointResult contract. No concurrent reader, so it must not be
// busy and must not grow the WAL.
func TestVaultCheckpoint_JSON(t *testing.T) {
	_, root := newContractVault(t)
	// Seed + index a few notes so there's WAL traffic to checkpoint.
	for _, name := range []string{"a", "b", "c"} {
		writeNote(t, root, name+".md", name, "# "+name+"\n\nsome body text")
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "vault", "checkpoint", "--json")
	if err != nil {
		t.Fatalf("vault checkpoint: %v\n%s", err, out)
	}
	var res store.CheckpointResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal CheckpointResult: %v\n%s", err, out)
	}
	if res.Busy {
		t.Errorf("checkpoint should not be busy with no concurrent reader")
	}
	if res.WALBytesAfter > res.WALBytesBefore {
		t.Errorf("WAL grew across checkpoint: before=%d after=%d", res.WALBytesBefore, res.WALBytesAfter)
	}
	if res.DBBytes <= 0 {
		t.Errorf("DBBytes should be positive, got %d", res.DBBytes)
	}
}
