package ai

import (
	"fmt"
	"strconv"
	"strings"
)

// HasKnownPricing reports whether we have confident price data for a model:
// it is local (free to run), carries a non-zero price, or has an explicit
// PriceSource (which marks known-free entries). This is the single
// definition shared by display labels and cost estimates; entries with all
// zero prices and no source are "unknown" so callers can say so.
func HasKnownPricing(m ModelInfo) bool {
	if m.Local {
		return true
	}
	if m.PriceIn > 0 || m.PriceOut > 0 || m.PriceRequest > 0 {
		return true
	}
	return m.PriceSource != ""
}

// IsExplicitlyFree reports whether the model is explicitly known to be free.
func IsExplicitlyFree(m ModelInfo) bool {
	return HasKnownPricing(m) && m.PriceIn == 0 && m.PriceOut == 0 && m.PriceRequest == 0
}

// CompactPriceLabel returns a short human-readable price label for tables.
func CompactPriceLabel(m ModelInfo) string {
	switch {
	case !HasKnownPricing(m):
		return "—"
	case IsExplicitlyFree(m):
		return "free"
	case m.PriceRequest > 0:
		return fmt.Sprintf("$%s/req", formatCompactUSD(m.PriceRequest))
	default:
		outPart := "--"
		if m.PriceOut > 0 {
			outPart = "$" + formatCompactUSD(m.PriceOut)
		}
		return fmt.Sprintf("$%s/%s", formatCompactUSD(m.PriceIn), outPart)
	}
}

// VerbosePriceLabel returns a descriptive price label for status output.
func VerbosePriceLabel(m ModelInfo) string {
	switch {
	case !HasKnownPricing(m):
		return "unknown"
	case IsExplicitlyFree(m):
		return "free"
	case m.PriceRequest > 0:
		return fmt.Sprintf("$%s per request", formatCompactUSD(m.PriceRequest))
	case m.PriceOut > 0:
		return fmt.Sprintf("$%s in / $%s out per 1M tokens", formatCompactUSD(m.PriceIn), formatCompactUSD(m.PriceOut))
	default:
		return fmt.Sprintf("$%s per 1M input tokens", formatCompactUSD(m.PriceIn))
	}
}

// IsCloudProvider reports whether the given provider/local pair runs on a
// remote cloud API rather than on-device hardware.
func IsCloudProvider(provider string, local bool) bool {
	if local {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "llama-local", "ollama":
		return false
	default:
		return true
	}
}

// EstimateUsageCost projects the USD cost for an operation leg using catalog
// per-million-token or per-request pricing. On-device models always return
// ($0.00, isCloud=false, knownPricing=true). Cloud models with missing pricing
// return (0, isCloud=true, knownPricing=false).
func EstimateUsageCost(m ModelInfo, inputTokens, outputTokens, requests int) (usd float64, isCloud bool, knownPricing bool) {
	isCloud = IsCloudProvider(m.Provider, m.Local)
	if !isCloud {
		return 0, false, true
	}
	if !HasKnownPricing(m) {
		return 0, true, false
	}
	if IsExplicitlyFree(m) {
		return 0, true, true
	}
	usd = float64(inputTokens)*m.PriceIn/1_000_000 +
		float64(outputTokens)*m.PriceOut/1_000_000 +
		float64(requests)*m.PriceRequest
	return usd, true, true
}

// FormatUSD renders a USD cost estimate compactly across sub-cent and dollar
// scales ("$0.00", "<$0.0001", "$0.0012", "$0.042", "$1.25").
func FormatUSD(usd float64) string {
	switch {
	case usd <= 0:
		return "$0.00"
	case usd < 0.0001:
		return "<$0.0001"
	case usd < 0.01:
		return fmt.Sprintf("$%.4f", usd)
	case usd < 0.10:
		return fmt.Sprintf("$%.3f", usd)
	default:
		return fmt.Sprintf("$%.2f", usd)
	}
}

// EstimateInputCost returns the estimated input-side cost for the model.
// The estimate is based on either per-request or per-million input pricing.
func EstimateInputCost(m ModelInfo, inputTokens float64, requests int) (float64, bool) {
	switch {
	case !HasKnownPricing(m):
		return 0, false
	case IsExplicitlyFree(m):
		return 0, true
	case m.PriceRequest > 0:
		return float64(requests) * m.PriceRequest, true
	case m.PriceIn > 0:
		return (inputTokens / 1_000_000) * m.PriceIn, true
	default:
		return 0, false
	}
}

func formatCompactUSD(v float64) string {
	if v == 0 {
		return "0"
	}
	s := strconv.FormatFloat(v, 'f', 5, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}
