package vault

import (
	"fmt"
	"math"
	"testing"

	"github.com/apresai/2ndbrain/internal/store"
)

func TestEstimateInitialEmbedSeconds_HistoryAndBaseline(t *testing.T) {
	if est, src := EstimateInitialEmbedSeconds(0, 0, "llama-local", 4, 0, 0); est != 0 || src != "none" {
		t.Fatalf("empty corpus: got (%v, %q), want (0, none)", est, src)
	}

	// History takes precedence over baseline when both positive.
	estHist, srcHist := EstimateInitialEmbedSeconds(100, 100_000, "llama-local", 4, 10.0, 20_000.0)
	if srcHist != "history" || math.Abs(estHist-10.0) > 1e-9 {
		t.Fatalf("history: got (%v, %q), want (10.0, history)", estHist, srcHist)
	}

	// Baseline includes warmup and scales cleanly on llama-local vs gemini.
	estLocal, srcLocal := EstimateInitialEmbedSeconds(120, 180_000, "llama-local", 4, 0, 0)
	if srcLocal != "baseline" || math.Abs(estLocal-10.6) > 1e-9 {
		t.Fatalf("llama-local baseline: got (%v, %q), want (10.6, baseline)", estLocal, srcLocal)
	}

	estGemini, srcGemini := EstimateInitialEmbedSeconds(60, 160_000, "gemini", 4, 0, 0)
	if srcGemini != "baseline" || math.Abs(estGemini-10.3) > 1e-9 {
		t.Fatalf("gemini baseline: got (%v, %q), want (10.3, baseline)", estGemini, srcGemini)
	}
}

func TestEmbedETATracker_UniformAndSkewedVaults(t *testing.T) {
	t.Run("uniform docs converge smoothly and finish at zero", func(t *testing.T) {
		sizing := map[string]store.DocEmbedSizing{
			"d1": {Chunks: 2, Chars: 1000},
			"d2": {Chunks: 2, Chars: 1000},
			"d3": {Chunks: 2, Chars: 1000},
			"d4": {Chunks: 2, Chars: 1000},
			"d5": {Chunks: 2, Chars: 1000},
		}
		tr := NewEmbedETATracker(sizing, 5, 10, 5000, 10.0)

		s1 := tr.Observe("d1", 2000)
		if s1.DoneEmbeddable != 1 || s1.DoneChunks != 2 || s1.ETASeconds <= 0 {
			t.Fatalf("after 1/5: got %+v", s1)
		}
		// Weight after 4 docs is 0.8 in 8s -> liveWorkPerSec = 0.1/s -> ETA = 0.2/0.1 = 2.0s
		tr.Observe("d2", 4000)
		tr.Observe("d3", 6000)
		s4 := tr.Observe("d4", 8000)
		if math.Abs(s4.ETASeconds-2.0) > 1e-6 {
			t.Fatalf("after 4/5 uniform docs: eta = %v, want 2.0", s4.ETASeconds)
		}
		if math.Abs(s4.DocsPerSec-0.5) > 1e-6 || math.Abs(s4.ChunksPerSec-1.0) > 1e-6 {
			t.Fatalf("rates mismatch: %+v", s4)
		}
		s5 := tr.Observe("d5", 10000)
		if s5.ETASeconds != 0 {
			t.Fatalf("after 5/5: eta = %v, want 0", s5.ETASeconds)
		}
	})

	t.Run("skewed vault weights huge notes heavily instead of naive doc-count ETA", func(t *testing.T) {
		// 10 tiny notes (100 chars each = 1000 chars total) + 2 huge notes (50,000 chars each = 100,000 chars)
		sizing := make(map[string]store.DocEmbedSizing, 12)
		for i := 1; i <= 10; i++ {
			sizing[fmt.Sprintf("tiny-%d", i)] = store.DocEmbedSizing{Chunks: 1, Chars: 100}
		}
		sizing["huge-1"] = store.DocEmbedSizing{Chunks: 25, Chars: 50_000}
		sizing["huge-2"] = store.DocEmbedSizing{Chunks: 25, Chars: 50_000}
		sizing["empty"] = store.DocEmbedSizing{Chunks: 0, Chars: 0}

		tr := NewEmbedETATracker(sizing, 12, 60, 101_000, 100.0)
		// Empty note carries zero weight and does not increment DoneEmbeddable.
		sEmpty := tr.Observe("empty", 500)
		if sEmpty.Processed != 1 || sEmpty.DoneEmbeddable != 0 {
			t.Fatalf("empty observe mismatch: %+v", sEmpty)
		}

		// Complete all 10 tiny notes in 5s total.
		var sLast EmbedETASnapshot
		for i := 1; i <= 10; i++ {
			sLast = tr.Observe(fmt.Sprintf("tiny-%d", i), int64(500*i))
		}
		// Weight completed = 0.5*(10/12) + 0.5*(1000/101000) ≈ 0.4216 (NOT 10/12 = 0.833!).
		// Wait: naive doc-count ETA at 2 docs/s for 2 remaining docs would falsely predict 1.0s!
		// Work-weighted ETA sees >57% work (the two 50k notes) remains -> predicts ~6.86s.
		if sLast.ETASeconds <= 5.0 {
			t.Fatalf("skewed work-weighted ETA = %v, expected > 5.0s because two huge notes remain", sLast.ETASeconds)
		}
	})
}
