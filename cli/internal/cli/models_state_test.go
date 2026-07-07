package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
)

func TestStateLabel(t *testing.T) {
	recent := time.Now().UTC().Add(-3 * 24 * time.Hour).Format(time.RFC3339)
	tests := []struct {
		name string
		m    ai.ModelInfo
		want string
	}{
		{"untested", ai.ModelInfo{}, "-"},
		{"recommended untested", ai.ModelInfo{Recommended: true}, "★ -"},
		{"passed", ai.ModelInfo{TestedAt: recent}, "ok 3d"},
		{"recommended passed", ai.ModelInfo{Recommended: true, TestedAt: recent}, "★ ok 3d"},
		{"classified failure", ai.ModelInfo{TestedAt: recent, TestError: "403", TestErrorCode: "access_denied"}, "access_denied"},
		{"unclassified failure", ai.ModelInfo{TestedAt: recent, TestError: "boom"}, "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stateLabel(tt.m); got != tt.want {
				t.Errorf("stateLabel(%+v) = %q, want %q", tt.m, got, tt.want)
			}
		})
	}
}

func TestTestAge(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		testedAt string
		want     string
	}{
		{"minutes ago", now.Add(-10 * time.Minute).Format(time.RFC3339), "now"},
		{"hours ago", now.Add(-5 * time.Hour).Format(time.RFC3339), "5h"},
		{"days ago", now.Add(-72 * time.Hour).Format(time.RFC3339), "3d"},
		{"months ago", now.Add(-65 * 24 * time.Hour).Format(time.RFC3339), "2mo"},
		{"garbage", "not-a-timestamp", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := testAge(tt.testedAt); got != tt.want {
				t.Errorf("testAge(%q) = %q, want %q", tt.testedAt, got, tt.want)
			}
		})
	}
	// Guard the corrupt-timestamp rendering path end to end: a garbage
	// TestedAt must not leave a trailing-space "ok " label.
	label := stateLabel(ai.ModelInfo{TestedAt: "garbage"})
	if strings.HasSuffix(label, " ") {
		t.Errorf("stateLabel with corrupt TestedAt has trailing space: %q", label)
	}
}

// TestContract_ModelsListBenchColumnAndSort seeds a user-catalog entry with a
// benchmark block and asserts (a) the benchmark round-trips through
// models list --json, (b) the BENCH table cell renders, and (c) --sort best
// puts the quality-benched model ahead of its unbenched peers.
func TestContract_ModelsListBenchColumnAndSort(t *testing.T) {
	_, root := newContractVault(t)

	entry := ai.ModelInfo{
		ID: "amazon.titan-embed-text-v2:0", Provider: "bedrock", Type: "embedding",
		Tier:     ai.TierUserVerified,
		TestedAt: "2026-07-01T00:00:00Z",
		Benchmark: &ai.BenchmarkSummary{
			RanAt:         "2026-07-01T00:00:00Z",
			AvgLatencyMs:  412,
			QualityScore:  0.87,
			VaultDocCount: 150,
		},
	}
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, entry); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	// (a) JSON round-trip.
	got, err := runCLIArgs(t, root, "models", "list", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models list --json: %v", err)
	}
	var models []struct {
		ID        string `json:"id"`
		Benchmark *struct {
			QualityScore float64 `json:"quality_score"`
			AvgLatencyMs int64   `json:"avg_latency_ms"`
		} `json:"benchmark"`
	}
	if err := json.Unmarshal(got, &models); err != nil {
		t.Fatalf("parse: %v", err)
	}
	found := false
	for _, m := range models {
		if m.ID == entry.ID && m.Benchmark != nil && m.Benchmark.QualityScore == 0.87 {
			found = true
		}
	}
	if !found {
		t.Fatal("benchmark summary did not round-trip through models list --json")
	}

	// (b) Table cell renders q= and ms.
	table, err := runCLIArgs(t, root, "models", "list")
	if err != nil {
		t.Fatalf("models list table: %v", err)
	}
	if !strings.Contains(string(table), "q=0.87 412ms") {
		t.Fatalf("BENCH cell missing from table:\n%s", truncate(table, 1200))
	}

	// (c) --sort best puts the quality-benched embedding first among
	// embeddings in JSON order.
	got, err = runCLIArgs(t, root, "models", "list", "--sort", "best", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models list --sort best: %v", err)
	}
	var sorted []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(got, &sorted); err != nil {
		t.Fatalf("parse sorted: %v", err)
	}
	for _, m := range sorted {
		if m.Type != "embedding" {
			continue
		}
		if m.ID != entry.ID {
			t.Fatalf("--sort best should rank the quality-benched embedding first, got %s", m.ID)
		}
		break
	}

	// (d) Unknown sort value errors.
	if _, err := runCLIArgs(t, root, "models", "list", "--sort", "bogus"); err == nil {
		t.Fatal("--sort bogus should error")
	}
}
