package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	probeLevelsFlag string
	probeSampleFlag int
	probeYesFlag    bool
)

var aiEmbedProbeCmd = &cobra.Command{
	Use:   "embed-probe",
	Short: "Find a safe embedding concurrency for your account by ramping it",
	Long: `Embeds a sample of the vault's chunks (discarded — never stored) at escalating
concurrency levels and measures throughput and errors at each, then recommends
the lowest level that reaches near-peak throughput before throttling appears.

AWS does not publish per-account Bedrock RPM quotas, so this discovers your real
ceiling empirically. Apply the result with:
  2nb config set ai.embed_concurrency <N>

It makes real embedding calls on the sample (a small cost); pass --yes to skip
the confirmation.`,
	RunE: runAIEmbedProbe,
}

func init() {
	aiEmbedProbeCmd.Flags().StringVar(&probeLevelsFlag, "levels", "4,8,16,32", "Comma-separated concurrency levels to test")
	aiEmbedProbeCmd.Flags().IntVar(&probeSampleFlag, "sample", 64, "Number of chunks to embed at each level")
	aiEmbedProbeCmd.Flags().BoolVar(&probeYesFlag, "yes", false, "Skip the cost confirmation prompt")
	aiCmd.AddCommand(aiEmbedProbeCmd)
}

// ProbeLevel is one concurrency level's measurement.
type ProbeLevel struct {
	Concurrency int     `json:"concurrency"`
	DurationMs  int64   `json:"duration_ms"`
	TextsPerSec float64 `json:"texts_per_sec"`
	Errors      int     `json:"errors"`
}

// ProbeResult is the `ai embed-probe --json` payload.
type ProbeResult struct {
	Provider    string       `json:"provider"`
	Model       string       `json:"model"`
	SampleSize  int          `json:"sample_size"`
	Levels      []ProbeLevel `json:"levels"`
	Recommended int          `json:"recommended"`
}

func runAIEmbedProbe(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil {
		return fmt.Errorf("no embedding provider: %w", err)
	}
	if !embedder.Available(ctx) {
		return fmt.Errorf("embedding provider %q is not ready (check credentials) — run `2nb ai setup`", cfg.Provider)
	}

	levels, err := parseProbeLevels(probeLevelsFlag)
	if err != nil {
		return err
	}

	sample, err := sampleChunkTexts(v, probeSampleFlag)
	if err != nil {
		return err
	}
	if len(sample) == 0 {
		return fmt.Errorf("no chunks to sample — run `2nb index` first")
	}

	format := getFormat(cmd)
	if !probeYesFlag && format == "" {
		fmt.Fprintf(os.Stderr, "Probe will make ~%d embedding calls (%d chunks × %d levels) via %s. Continue? [y/N] ",
			len(sample)*len(levels), len(sample), len(levels), cfg.Provider)
		var resp string
		fmt.Scanln(&resp)
		if !strings.EqualFold(strings.TrimSpace(resp), "y") {
			fmt.Fprintln(os.Stderr, "aborted")
			return nil
		}
	}

	result := ProbeResult{Provider: cfg.Provider, Model: cfg.EmbeddingModel, SampleSize: len(sample)}
	for _, level := range levels {
		pl := probeOneLevel(ctx, embedder, sample, level)
		result.Levels = append(result.Levels, pl)
		if format == "" {
			fmt.Fprintf(os.Stderr, "  concurrency %2d: %6.1f texts/sec, %d error(s)  (%s)\n",
				pl.Concurrency, pl.TextsPerSec, pl.Errors,
				(time.Duration(pl.DurationMs) * time.Millisecond).Round(time.Millisecond))
		}
	}
	result.Recommended = recommendConcurrency(result.Levels)

	if format != "" {
		return output.Write(os.Stdout, format, result)
	}
	fmt.Printf("\nRecommended concurrency: %d\n", result.Recommended)
	fmt.Printf("  apply with: 2nb config set ai.embed_concurrency %d\n", result.Recommended)
	return nil
}

// parseProbeLevels parses "4,8,16,32" into a sorted, deduped []int in [1,64].
func parseProbeLevels(s string) ([]int, error) {
	seen := map[int]bool{}
	var levels []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 64 {
			return nil, fmt.Errorf("invalid level %q: levels must be integers in [1,64]", part)
		}
		if !seen[n] {
			seen[n] = true
			levels = append(levels, n)
		}
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("no valid levels in %q", s)
	}
	sort.Ints(levels)
	return levels, nil
}

// sampleChunkTexts pulls up to n non-empty chunk bodies straight from the index
// — the real texts the embedder would see, so the probe measures realistic load.
func sampleChunkTexts(v *vault.Vault, n int) ([]string, error) {
	if n <= 0 {
		n = 64
	}
	rows, err := v.DB.Conn().Query(`SELECT content FROM chunks WHERE content != '' LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("sample chunks: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// probeOneLevel embeds every sample text with a bounded worker pool of size
// `level` (each text discarded), timing the batch and counting hard failures
// (errors that survived the provider's retry/backoff). Mirrors the embed pool.
func probeOneLevel(ctx context.Context, embedder ai.EmbeddingProvider, sample []string, level int) ProbeLevel {
	var errs atomic.Int64
	sem := make(chan struct{}, level)
	var wg sync.WaitGroup

	start := time.Now()
	for i := range sample {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if _, err := embedder.Embed(ctx, []string{text}, ai.WithPurpose(ai.PurposeIndex)); err != nil {
				errs.Add(1)
			}
		}(sample[i])
	}
	wg.Wait()
	elapsed := time.Since(start)

	tput := 0.0
	if elapsed > 0 {
		tput = float64(len(sample)) / elapsed.Seconds()
	}
	return ProbeLevel{
		Concurrency: level,
		DurationMs:  elapsed.Milliseconds(),
		TextsPerSec: round2(tput),
		Errors:      int(errs.Load()),
	}
}

// recommendConcurrency picks the LOWEST level that reaches ≥90% of peak
// throughput among levels that ran error-free — past that, more concurrency just
// adds throttle risk for little gain. The first level that errors caps the scan
// (treat it as the throttling ceiling).
func recommendConcurrency(levels []ProbeLevel) int {
	var clean []ProbeLevel
	for _, l := range levels {
		if l.Errors > 0 {
			break
		}
		clean = append(clean, l)
	}
	if len(clean) == 0 {
		// Even the lowest level threw errors — recommend something conservative.
		if len(levels) > 0 {
			return max(1, levels[0].Concurrency/2)
		}
		return 4
	}
	var peak float64
	for _, l := range clean {
		if l.TextsPerSec > peak {
			peak = l.TextsPerSec
		}
	}
	for _, l := range clean {
		if l.TextsPerSec >= 0.9*peak {
			return l.Concurrency
		}
	}
	return clean[len(clean)-1].Concurrency
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
