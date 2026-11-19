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

func TestLookupLocalModelPricingAndEstimateUsageCost(t *testing.T) {
	t.Run("gemini generation pricing", func(t *testing.T) {
		m := LookupLocalModelPricing("", "gemini", "gemini-4-hydrogen")
		if m.PriceIn != 1.50 || m.PriceOut != 7.50 {
			t.Fatalf("gemini-4-hydrogen pricing = (%v, %v), want (1.50, 7.50)", m.PriceIn, m.PriceOut)
		}
		usd, isCloud, known := EstimateUsageCost(m, 1_000_000, 100_000, 1)
		if !isCloud || !known {
			t.Fatalf("got isCloud=%v known=%v, want true/true", isCloud, known)
		}
		want := 1.50 + 0.75
		if usd < want-1e-9 || usd > want+1e-9 {
			t.Fatalf("usd = %v, want %v", usd, want)
		}
	})

	t.Run("gemini embedding pricing", func(t *testing.T) {
		m := LookupLocalModelPricing("", "gemini", "gemini-embedding-2")
		if m.PriceIn != 0.20 {
			t.Fatalf("gemini-embedding-2 price_in = %v, want 0.20", m.PriceIn)
		}
		usd, isCloud, known := EstimateUsageCost(m, 500_000, 0, 1)
		if !isCloud || !known || usd != 0.10 {
			t.Fatalf("got (%v, %v, %v), want (0.10, true, true)", usd, isCloud, known)
		}
	})

	t.Run("on-device local models always $0.00", func(t *testing.T) {
		for _, id := range []string{"embeddinggemma-2", "gemma4-e2b"} {
			m := LookupLocalModelPricing("", "llama-local", id)
			usd, isCloud, known := EstimateUsageCost(m, 250_000, 10_000, 5)
			if usd != 0 || isCloud || !known {
				t.Fatalf("%s: got (%v, isCloud=%v, known=%v), want (0, false, true)", id, usd, isCloud, known)
			}
		}
	})

	t.Run("explicit free cloud openrouter model", func(t *testing.T) {
		m := LookupLocalModelPricing("", "openrouter", "google/gemma-4-31b-it:free")
		usd, isCloud, known := EstimateUsageCost(m, 100_000, 5_000, 1)
		if usd != 0 || !isCloud || !known {
			t.Fatalf("explicit free cloud: got (%v, isCloud=%v, known=%v), want (0, true, true)", usd, isCloud, known)
		}
	})

	t.Run("unpriced custom cloud model reports unknown", func(t *testing.T) {
		m := LookupLocalModelPricing("", "openrouter", "vendor/custom-unpriced")
		usd, isCloud, known := EstimateUsageCost(m, 50_000, 1_000, 1)
		if usd != 0 || !isCloud || known {
			t.Fatalf("unpriced custom cloud: got (%v, isCloud=%v, known=%v), want (0, true, false)", usd, isCloud, known)
		}
	})
}

func TestFormatUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{-0.01, "$0.00"},
		{0.00004, "<$0.0001"},
		{0.00124, "$0.0012"},
		{0.0426, "$0.043"},
		{1.254, "$1.25"},
	}
	for _, tc := range cases {
		if got := FormatUSD(tc.in); got != tc.want {
			t.Errorf("FormatUSD(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
