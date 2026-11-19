package cli

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/vault"
)

// recordMetric writes one operation row to the vault's metrics sidecar
// (.2ndbrain/metrics.db). It is strictly best-effort: any failure (open or
// write) is logged at debug and swallowed, so a metrics problem can never break
// the operation it measures. Never creates .2ndbrain on an uninitialized dir.
func recordMetric(v *vault.Vault, op metrics.Operation) {
	if v == nil || v.DotDir == "" {
		return
	}
	recordMetricAtDotDir(v.DotDir, op)
}

func recordMetricAtDotDir(dotDir string, op metrics.Operation) {
	if dotDir == "" {
		return
	}
	if _, err := os.Stat(dotDir); err != nil {
		return
	}
	if op.CLIVersion == "" {
		op.CLIVersion = Version
	}
	if op.Source == "" {
		op.Source = "cli"
	}
	mdb, err := metrics.Open(filepath.Join(dotDir, "metrics.db"))
	if err != nil {
		slog.Debug("metrics: open failed", "err", err)
		return
	}
	defer mdb.Close()
	if err := mdb.Record(op); err != nil {
		slog.Debug("metrics: record failed", "err", err)
	}
}

// buildUsageLeg resolves local catalog pricing (zero network) and builds one
// metered slot leg for operations + ai_usage_ledger.
func buildUsageLeg(vaultRoot, provider, model, slot, operation string, calls, inTok, outTok int) metrics.UsageLeg {
	if model == "" && provider == "" {
		return metrics.UsageLeg{}
	}
	if provider == "" && model != "" {
		provider = ai.InferProvider(model)
	}
	m := ai.LookupLocalModelPricing(vaultRoot, provider, model)
	usd, isCloud, knownPricing := ai.EstimateUsageCost(m, inTok, outTok, calls)
	return metrics.UsageLeg{
		Provider:     provider,
		Model:        model,
		Slot:         slot,
		Operation:    operation,
		IsCloud:      isCloud,
		Calls:        calls,
		InputTokens:  inTok,
		OutputTokens: outTok,
		CostUSD:      usd,
		KnownPricing: knownPricing,
	}
}

// attachUsageLegs attaches active slot legs onto op, folding slot breakdowns,
// aggregate InputTokens/OutputTokens, CostUSD, and KnownPricing in one place.
func attachUsageLegs(op *metrics.Operation, legs ...metrics.UsageLeg) {
	var (
		active       []metrics.UsageLeg
		totalCost    float64
		embedIn      int
		genIn        int
		genOut       int
		hasActive    bool
		allCloudKnown = true
	)
	for _, leg := range legs {
		if leg.Calls <= 0 && leg.InputTokens <= 0 && leg.OutputTokens <= 0 {
			continue
		}
		hasActive = true
		active = append(active, leg)
		totalCost += leg.CostUSD
		if leg.IsCloud && !leg.KnownPricing {
			allCloudKnown = false
		}
		switch leg.Slot {
		case "embedding":
			if leg.Provider != "" {
				op.EmbeddingProvider = leg.Provider
			}
			if leg.Model != "" {
				op.EmbeddingModel = leg.Model
			}
			embedIn += leg.InputTokens
		case "generation":
			if leg.Provider != "" {
				op.GenerationProvider = leg.Provider
			}
			if leg.Model != "" {
				op.GenerationModel = leg.Model
			}
			genIn += leg.InputTokens
			genOut += leg.OutputTokens
		}
	}
	if !hasActive {
		return
	}
	op.EmbedInputTokens += embedIn
	op.GenInputTokens += genIn
	op.GenOutputTokens += genOut
	if embedIn > 0 || genIn > 0 {
		op.InputTokens = op.EmbedInputTokens + op.GenInputTokens
	}
	if genOut > 0 {
		op.OutputTokens = op.GenOutputTokens
	}
	op.CostUSD = totalCost
	op.KnownPricing = allCloudKnown
	op.UsageLegs = append(op.UsageLegs, active...)
}

// historicalEmbedThroughput reads the most recent measured throughput from an
// existing metrics.db without creating metrics.db as a side effect.
func historicalEmbedThroughput(v *vault.Vault, provider, model string) (docsPerSec, charsPerSec float64, ok bool) {
	if v == nil || v.DotDir == "" || model == "" {
		return 0, 0, false
	}
	dbPath := filepath.Join(v.DotDir, "metrics.db")
	if _, err := os.Stat(dbPath); err != nil {
		return 0, 0, false
	}
	mdb, err := metrics.Open(dbPath)
	if err != nil {
		return 0, 0, false
	}
	defer mdb.Close()
	return mdb.HistoricalEmbedThroughput(provider, model)
}

// errString is err.Error() or "" for a nil error — for the metrics Error field.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
