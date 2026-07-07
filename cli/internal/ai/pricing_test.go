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

// TestHasKnownPricing_UnifiedSemantics pins the single shared definition:
// local, or non-zero price, or explicit source. The pre-unification exported
// helper required a PriceSource, so a priced entry without one rendered "—".
func TestHasKnownPricing_UnifiedSemantics(t *testing.T) {
	pricedNoSource := ModelInfo{PriceIn: 0.6, PriceOut: 1.2}
	if !HasKnownPricing(pricedNoSource) {
		t.Error("priced entry without a source must count as known pricing")
	}
	if IsExplicitlyFree(pricedNoSource) {
		t.Error("priced entry is not free")
	}
	if got := CompactPriceLabel(pricedNoSource); got != "$0.6/$1.2" {
		t.Errorf("CompactPriceLabel = %q, want $0.6/$1.2", got)
	}

	localNoSource := ModelInfo{Local: true}
	if !HasKnownPricing(localNoSource) || !IsExplicitlyFree(localNoSource) {
		t.Error("a local model is known-free even without a price source")
	}
	if got := CompactPriceLabel(localNoSource); got != "free" {
		t.Errorf("CompactPriceLabel(local) = %q, want free", got)
	}

	unknown := ModelInfo{}
	if HasKnownPricing(unknown) {
		t.Error("all-zero cloud entry with no source must stay unknown")
	}
	if got := CompactPriceLabel(unknown); got != "—" {
		t.Errorf("CompactPriceLabel(unknown) = %q, want —", got)
	}
}
