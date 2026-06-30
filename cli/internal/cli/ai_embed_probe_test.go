package cli

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

func TestParseProbeLevels(t *testing.T) {
	got, err := parseProbeLevels("8,4,4,16")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := []int{4, 8, 16}; !reflect.DeepEqual(got, want) {
		t.Errorf("levels = %v, want %v (sorted, deduped)", got, want)
	}
	// blank parts are skipped, not errors.
	if got, err := parseProbeLevels("4, ,8"); err != nil || !reflect.DeepEqual(got, []int{4, 8}) {
		t.Errorf("parse with blank = %v (err %v), want [4 8]", got, err)
	}
	for _, bad := range []string{"0,4", "4,65", "-1", "abc", "", " , "} {
		if _, err := parseProbeLevels(bad); err == nil {
			t.Errorf("parseProbeLevels(%q) = nil err, want error", bad)
		}
	}
}

func TestRecommendConcurrency(t *testing.T) {
	lvl := func(c int, tput float64, errs int) ProbeLevel {
		return ProbeLevel{Concurrency: c, TextsPerSec: tput, Errors: errs}
	}

	// Diminishing returns: 8 reaches ≥90% of the peak (17) → recommend 8, not 16.
	if got := recommendConcurrency([]ProbeLevel{lvl(4, 11, 0), lvl(8, 16, 0), lvl(16, 17, 0)}); got != 8 {
		t.Errorf("diminishing: got %d, want 8", got)
	}
	// Throttling at 16 (errors) caps the scan → recommend the last clean level 8.
	if got := recommendConcurrency([]ProbeLevel{lvl(4, 11, 0), lvl(8, 16, 0), lvl(16, 5, 3)}); got != 8 {
		t.Errorf("throttled-16: got %d, want 8", got)
	}
	// Strong scaling all the way → recommend the highest (16 reaches 90% of peak 20).
	if got := recommendConcurrency([]ProbeLevel{lvl(4, 5, 0), lvl(8, 11, 0), lvl(16, 20, 0)}); got != 16 {
		t.Errorf("strong-scaling: got %d, want 16", got)
	}
	// Even the lowest level errors → conservative half.
	if got := recommendConcurrency([]ProbeLevel{lvl(8, 3, 5)}); got != 4 {
		t.Errorf("all-error: got %d, want 4 (8/2)", got)
	}
	if got := recommendConcurrency(nil); got != 4 {
		t.Errorf("empty: got %d, want 4", got)
	}
}

func TestSampleChunkTexts(t *testing.T) {
	v := testutil.NewTestVault(t)

	// Zero chunks → empty (the caller turns this into "run 2nb index first").
	if got, err := sampleChunkTexts(v, 64); err != nil || len(got) != 0 {
		t.Fatalf("empty vault sample = %d (err %v), want 0", len(got), err)
	}

	for i := 0; i < 5; i++ {
		testutil.CreateAndIndex(t, v, fmt.Sprintf("Doc %d", i), "note",
			fmt.Sprintf("body content number %d with several words", i))
	}

	// Fewer-than-n → returns what's available (>= one chunk per doc).
	got, err := sampleChunkTexts(v, 100)
	if err != nil {
		t.Fatalf("seeded sample: %v", err)
	}
	if len(got) < 5 {
		t.Errorf("sample = %d, want >= 5 (one+ chunk per doc)", len(got))
	}
	for _, c := range got {
		if c == "" {
			t.Error("sampled an empty chunk (WHERE content != '' should exclude these)")
		}
	}

	// LIMIT respected.
	if got, err := sampleChunkTexts(v, 2); err != nil || len(got) > 2 {
		t.Errorf("limit-2 sample = %d (err %v), want <= 2", len(got), err)
	}
}
