package ai

import "testing"

func TestPricingHelpers(t *testing.T) {
	t.Run("unknown pricing", func(t *testing.T) {
		model := ModelInfo{}
		if HasKnownPricing(model) {
			t.Fatal("unknown model should not have known pricing")
		}
		if IsExplicitlyFree(model) {
			t.Fatal("unknown model should not be explicitly free")
		}
		if got := CompactPriceLabel(model); got != "—" {
			t.Fatalf("CompactPriceLabel = %q, want em dash", got)
		}
		if got := VerbosePriceLabel(model); got != "unknown" {
			t.Fatalf("VerbosePriceLabel = %q, want unknown", got)
		}
		if cost, ok := EstimateInputCost(model, 1000, 1); ok || cost != 0 {
			t.Fatalf("EstimateInputCost = (%v, %v), want (0, false)", cost, ok)
		}
	})

	t.Run("explicitly free pricing", func(t *testing.T) {
		model := ModelInfo{PriceSource: "vendor"}
		if !HasKnownPricing(model) {
			t.Fatal("known free model should have known pricing")
		}
		if !IsExplicitlyFree(model) {
			t.Fatal("known free model should be explicitly free")
		}
		if got := CompactPriceLabel(model); got != "free" {
			t.Fatalf("CompactPriceLabel = %q, want free", got)
		}
		if got := VerbosePriceLabel(model); got != "free" {
			t.Fatalf("VerbosePriceLabel = %q, want free", got)
		}
		if cost, ok := EstimateInputCost(model, 500000, 3); !ok || cost != 0 {
			t.Fatalf("EstimateInputCost = (%v, %v), want (0, true)", cost, ok)
		}
	})

	t.Run("token pricing", func(t *testing.T) {
		model := ModelInfo{PriceSource: "vendor", PriceIn: 0.6, PriceOut: 1.2}
		if got := CompactPriceLabel(model); got != "$0.6/$1.2" {
			t.Fatalf("CompactPriceLabel = %q, want $0.6/$1.2", got)
		}
		if got := VerbosePriceLabel(model); got != "$0.6 in / $1.2 out per 1M tokens" {
			t.Fatalf("VerbosePriceLabel = %q", got)
		}
		if cost, ok := EstimateInputCost(model, 500000, 1); !ok || cost != 0.3 {
			t.Fatalf("EstimateInputCost = (%v, %v), want (0.3, true)", cost, ok)
		}
	})

	t.Run("request pricing", func(t *testing.T) {
		model := ModelInfo{PriceSource: "vendor", PriceRequest: 0.02}
		if got := CompactPriceLabel(model); got != "$0.02/req" {
			t.Fatalf("CompactPriceLabel = %q, want $0.02/req", got)
		}
		if got := VerbosePriceLabel(model); got != "$0.02 per request" {
			t.Fatalf("VerbosePriceLabel = %q", got)
		}
		if cost, ok := EstimateInputCost(model, 100000, 3); !ok || cost != 0.06 {
			t.Fatalf("EstimateInputCost = (%v, %v), want (0.06, true)", cost, ok)
		}
	})
}
