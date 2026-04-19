package cli

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/spf13/cobra"
)

var (
	calibrateSamples int
	calibrateSave    bool
	calibrateScope   string
	calibrateSeed    int64
)

var modelsCalibrateCmd = &cobra.Command{
	Use:   "calibrate",
	Short: "Measure your vault's embedding noise floor and recommend a similarity threshold",
	Long: `Samples random chunk pairs from different documents, computes their
cosine similarity, and reports the distribution. The p95 of unrelated-pair
cosines approximates the noise floor — a threshold at or just above p95 is
where real semantic matches typically separate from random neighbors.

Use --save to persist the recommendation into the user catalog, which
ResolveSimilarityThreshold consults before falling back to the builtin
catalog. Per-vault scope is the default since different vaults (different
document sets, different topic mixes) will get different baselines.`,
	RunE: runModelsCalibrate,
}

func init() {
	modelsCalibrateCmd.Flags().IntVar(&calibrateSamples, "samples", 500, "Number of random doc pairs to sample")
	modelsCalibrateCmd.Flags().BoolVar(&calibrateSave, "save", false, "Save recommended threshold to the user catalog")
	modelsCalibrateCmd.Flags().StringVar(&calibrateScope, "scope", "vault", "When --save: scope to write to (vault | global)")
	modelsCalibrateCmd.Flags().Int64Var(&calibrateSeed, "seed", 0, "Seed for random pair sampling (0 = non-deterministic)")
	_ = modelsCalibrateCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)
	modelsCmd.AddCommand(modelsCalibrateCmd)
}

type calibrationResult struct {
	Provider        string                     `json:"provider"`
	Model           string                     `json:"model"`
	Dimensions      int                        `json:"dimensions"`
	DocCount        int                        `json:"doc_count"`
	SampleCount     int                        `json:"sample_count"`
	Min             float64                    `json:"min"`
	P50             float64                    `json:"p50"`
	P90             float64                    `json:"p90"`
	P95             float64                    `json:"p95"`
	P99             float64                    `json:"p99"`
	Max             float64                    `json:"max"`
	Recommended     float64                    `json:"recommended_threshold"`
	ActiveThreshold float64                    `json:"active_threshold"`
	ActiveSource    ai.ResolvedThresholdSource `json:"active_source"`
	Saved           string                     `json:"saved_to,omitempty"`
}

func runModelsCalibrate(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	cfg := v.Config.AI

	if cfg.EmbeddingModel == "" {
		return fmt.Errorf("no embedding model configured — run `2nb ai setup`")
	}

	docIDs, vecs, err := v.DB.AllEmbeddings()
	if err != nil {
		return fmt.Errorf("load embeddings: %w", err)
	}
	if len(docIDs) < 5 {
		return fmt.Errorf("need at least 5 embedded documents to calibrate (have %d) — run `2nb index`", len(docIDs))
	}

	if calibrateSamples < 1 {
		return fmt.Errorf("--samples must be >= 1")
	}

	// Generate random pair indices. Cap samples at the number of distinct
	// unordered pairs; small vaults can't supply 500 unique pairs.
	maxPairs := len(docIDs) * (len(docIDs) - 1) / 2
	targetSamples := calibrateSamples
	if targetSamples > maxPairs {
		targetSamples = maxPairs
	}

	var rng *rand.Rand
	if calibrateSeed != 0 {
		rng = rand.New(rand.NewSource(calibrateSeed))
	} else {
		rng = rand.New(rand.NewSource(rand.Int63()))
	}

	cosines := sampleUnrelatedCosines(rng, vecs, targetSamples)
	if len(cosines) == 0 {
		return fmt.Errorf("no valid pairs produced — embeddings may be zero-length or mismatched dimensions")
	}
	sort.Float64s(cosines)

	threshold, source := cfg.ResolveSimilarityThresholdFull(v.Root)
	p50 := percentile(cosines, 0.50)
	p90 := percentile(cosines, 0.90)
	p95 := percentile(cosines, 0.95)
	p99 := percentile(cosines, 0.99)

	// Recommendation: p95 plus a small margin so the threshold is *above*
	// the noise floor, not at it. Round up to the nearest 0.01 for human
	// readability. Clamp at 1.0.
	recommended := math.Ceil((p95+0.01)*100) / 100
	if recommended > 1 {
		recommended = 1
	}

	result := calibrationResult{
		Provider:        cfg.Provider,
		Model:           cfg.EmbeddingModel,
		Dimensions:      len(vecs[0]),
		DocCount:        len(docIDs),
		SampleCount:     len(cosines),
		Min:             cosines[0],
		P50:             p50,
		P90:             p90,
		P95:             p95,
		P99:             p99,
		Max:             cosines[len(cosines)-1],
		Recommended:     recommended,
		ActiveThreshold: threshold,
		ActiveSource:    source,
	}

	if calibrateSave {
		scope, vaultRoot, err := resolveCatalogScope(calibrateScope)
		if err != nil {
			return err
		}
		if err := saveCalibration(scope, vaultRoot, cfg.Provider, cfg.EmbeddingModel, recommended); err != nil {
			return fmt.Errorf("save calibration: %w", err)
		}
		path, _ := ai.CatalogPathForScope(scope, vaultRoot)
		result.Saved = path
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, result)
	}

	printCalibration(cmd, result, calibrateSave)
	return nil
}

// sampleUnrelatedCosines picks `samples` random unordered pairs of distinct
// indices and returns their cosine similarities. Dimension-mismatched pairs
// are skipped. Returns fewer than `samples` entries if the vault produces
// too many skipped pairs (can happen with mixed-model vaults).
func sampleUnrelatedCosines(rng *rand.Rand, vecs [][]float32, samples int) []float64 {
	n := len(vecs)
	if n < 2 {
		return nil
	}
	seen := make(map[uint64]bool, samples)
	out := make([]float64, 0, samples)
	// Cap attempts so tiny vaults don't spin forever looking for unique pairs.
	maxAttempts := samples * 4
	for attempt := 0; attempt < maxAttempts && len(out) < samples; attempt++ {
		i := rng.Intn(n)
		j := rng.Intn(n)
		if i == j {
			continue
		}
		if i > j {
			i, j = j, i
		}
		key := uint64(i)<<32 | uint64(j)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(vecs[i]) == 0 || len(vecs[j]) == 0 || len(vecs[i]) != len(vecs[j]) {
			continue
		}
		out = append(out, search.CosineSimilarity(vecs[i], vecs[j]))
	}
	return out
}

// percentile returns the value at the given percentile (0..1) assuming the
// slice is already sorted ascending. Uses nearest-rank interpolation.
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Round(q*float64(len(sorted)-1) + 0.0001))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// saveCalibration upserts a user-catalog entry for the calibrated model that
// carries only the recommended threshold. Existing fields on a matching entry
// are preserved via overlay semantics in LoadUserCatalog.
func saveCalibration(scope ai.UserCatalogScope, vaultRoot, provider, modelID string, threshold float64) error {
	entry := ai.ModelInfo{
		ID:                             modelID,
		Provider:                       provider,
		Type:                           "embedding",
		Tier:                           ai.TierUserVerified,
		RecommendedSimilarityThreshold: threshold,
	}
	return ai.SaveUserCatalogEntry(scope, vaultRoot, entry)
}

func printCalibration(cmd *cobra.Command, r calibrationResult, saved bool) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Calibrating %s/%s on %d documents (%d random pairs)\n\n",
		r.Provider, r.Model, r.DocCount, r.SampleCount)
	fmt.Fprintln(w, "Unrelated-pair cosine distribution:")
	fmt.Fprintf(w, "  min   %.3f\n", r.Min)
	fmt.Fprintf(w, "  p50   %.3f\n", r.P50)
	fmt.Fprintf(w, "  p90   %.3f\n", r.P90)
	fmt.Fprintf(w, "  p95   %.3f\n", r.P95)
	fmt.Fprintf(w, "  p99   %.3f\n", r.P99)
	fmt.Fprintf(w, "  max   %.3f\n\n", r.Max)
	fmt.Fprintf(w, "Recommended threshold: %.2f  (p95 + 0.01 margin, rounded up)\n", r.Recommended)
	fmt.Fprintf(w, "Currently active:      %.2f  (%s)\n", r.ActiveThreshold, r.ActiveSource)
	if saved {
		fmt.Fprintf(w, "\nSaved to %s (source will now show as \"user calibration\")\n", r.Saved)
	} else {
		fmt.Fprintln(w, "\nTo apply this for your vault: 2nb models calibrate --save")
		fmt.Fprintln(w, "To apply globally:             2nb models calibrate --save --scope global")
	}
}

