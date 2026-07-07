package ai

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// --- enrichModels pure-logic tests (no HTTP, no disk) ---

func makeOrPricing(ready bool, prices map[string]modelPrice) *providerPricing {
	if prices == nil {
		prices = map[string]modelPrice{}
	}
	return &providerPricing{ready: ready, exact: prices}
}

func emptyBrPricing() *providerPricing {
	return &providerPricing{alias: map[string]modelPrice{}}
}

func TestEnrichModels_UserSourcePreserved(t *testing.T) {
	model := ModelInfo{
		Provider:      "openrouter",
		ID:            "some/model",
		PriceIn:       9.99,
		PriceOut:      9.99,
		PriceSource:   "user",
		PriceOverride: true,
	}
	orPricing := makeOrPricing(true, map[string]modelPrice{
		"some/model": {PriceIn: 0.5, PriceOut: 1.0, HasIn: true, HasOut: true, Source: "vendor"},
	})
	out := enrichModels([]ModelInfo{model}, orPricing, emptyBrPricing())
	if out[0].PriceSource != "user" {
		t.Errorf("PriceSource = %q, want user", out[0].PriceSource)
	}
	if out[0].PriceIn != 9.99 {
		t.Errorf("PriceIn = %g, want 9.99 (user price preserved)", out[0].PriceIn)
	}
}

func TestEnrichModels_LegacyZeroUserSourceDoesNotBlockVendorPricing(t *testing.T) {
	model := ModelInfo{
		Provider:    "openrouter",
		ID:          "some/model",
		PriceSource: "user",
	}
	orPricing := makeOrPricing(true, map[string]modelPrice{
		"some/model": {PriceIn: 0.5, PriceOut: 1.0, HasIn: true, HasOut: true, Source: "vendor"},
	})
	out := enrichModels([]ModelInfo{model}, orPricing, emptyBrPricing())
	if out[0].PriceSource != "vendor" {
		t.Errorf("PriceSource = %q, want vendor", out[0].PriceSource)
	}
	if out[0].PriceIn != 0.5 || out[0].PriceOut != 1.0 {
		t.Fatalf("legacy zero-price user entry should accept vendor pricing, got %+v", out[0])
	}
}

func TestEnrichModels_BuiltinFreePreservedWhenLiveAbsent(t *testing.T) {
	// A builtin :free model that is NOT in the live API response must keep
	// its builtin PriceSource. Under the old no-match clearing behavior (removed), PriceSource would
	// become "" and IsExplicitlyFree would return false.
	model := ModelInfo{
		Provider:    "openrouter",
		ID:          "some/model:free",
		PriceSource: "builtin",
	}
	orPricing := makeOrPricing(true, map[string]modelPrice{}) // live API ready but model absent
	out := enrichModels([]ModelInfo{model}, orPricing, emptyBrPricing())
	if out[0].PriceSource != "builtin" {
		t.Errorf("PriceSource = %q, want builtin (must not be cleared)", out[0].PriceSource)
	}
}

func TestEnrichModels_LivePricingApplied(t *testing.T) {
	model := ModelInfo{
		Provider:    "openrouter",
		ID:          "some/model",
		PriceSource: "builtin",
		PriceIn:     0,
	}
	orPricing := makeOrPricing(true, map[string]modelPrice{
		"some/model": {PriceIn: 1.5, PriceOut: 3.0, HasIn: true, HasOut: true, Source: "vendor"},
	})
	out := enrichModels([]ModelInfo{model}, orPricing, emptyBrPricing())
	if out[0].PriceIn != 1.5 {
		t.Errorf("PriceIn = %g, want 1.5", out[0].PriceIn)
	}
	if out[0].PriceOut != 3.0 {
		t.Errorf("PriceOut = %g, want 3.0", out[0].PriceOut)
	}
	if out[0].PriceSource != "vendor" {
		t.Errorf("PriceSource = %q, want vendor", out[0].PriceSource)
	}
}

func TestEnrichModels_NotReadyClearsNothing(t *testing.T) {
	model := ModelInfo{
		Provider:    "openrouter",
		ID:          "some/model",
		PriceSource: "builtin",
		PriceIn:     0.5,
	}
	orPricing := makeOrPricing(false, nil) // not ready — fetch failed
	out := enrichModels([]ModelInfo{model}, orPricing, emptyBrPricing())
	if out[0].PriceSource != "builtin" {
		t.Errorf("PriceSource = %q, want builtin (not-ready must not clear)", out[0].PriceSource)
	}
	if out[0].PriceIn != 0.5 {
		t.Errorf("PriceIn = %g, want 0.5 (not-ready must not clear)", out[0].PriceIn)
	}
}

func TestEnrichModels_NonBuiltinRetainedWhenLiveAbsent(t *testing.T) {
	// A vendor-priced entry absent from the ready live API keeps its price:
	// a stale-but-labeled price (price_source records provenance) beats
	// "unknown" for cost previews, and bedrock's heuristic alias matching
	// used to silently blank real prices on a no-match (e.g. a promoted
	// user entry with PriceSource=vendor flipping to unknown).
	model := ModelInfo{
		Provider:    "openrouter",
		ID:          "some/model",
		PriceSource: "vendor",
		PriceIn:     2.0,
	}
	orPricing := makeOrPricing(true, map[string]modelPrice{}) // ready but absent
	out := enrichModels([]ModelInfo{model}, orPricing, emptyBrPricing())
	if out[0].PriceSource != "vendor" || out[0].PriceIn != 2.0 {
		t.Errorf("no-match must retain the labeled price, got source=%q in=%g", out[0].PriceSource, out[0].PriceIn)
	}
}

func TestOpenRouterPricingFeed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	models, err := ListOpenRouterModels(ctx, "", "")
	if err != nil {
		t.Skipf("OpenRouter pricing feed unavailable: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("ListOpenRouterModels returned no models")
	}

	foundPriced := false
	for _, m := range models {
		if m.PriceSource != "" {
			foundPriced = true
			break
		}
	}
	if !foundPriced {
		t.Fatal("OpenRouter pricing feed returned no explicit pricing metadata")
	}
}

func addTestOfferPrice(offer *awsOfferFile, sku string, attrs map[string]string, unit, desc, usd string) {
	if offer.Products == nil {
		offer.Products = map[string]awsOfferProduct{}
	}
	if offer.Terms.OnDemand == nil {
		offer.Terms.OnDemand = map[string]map[string]awsOfferTerm{}
	}
	offer.Products[sku] = awsOfferProduct{Attributes: attrs}
	offer.Terms.OnDemand[sku] = map[string]awsOfferTerm{
		"ondemand": {
			PriceDimensions: map[string]awsOfferDimension{
				"dim": {
					Unit:         unit,
					Description:  desc,
					PricePerUnit: map[string]string{"USD": usd},
				},
			},
		},
	}
}

func assertPriceClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("price = %v, want %v", got, want)
	}
}

func TestBedrockPricingPrefersGlobalStandardLegacyMarketplace(t *testing.T) {
	offer := awsOfferFile{}
	baseAttrs := map[string]string{
		"regionCode":   "us-east-1",
		"servicename":  "Claude Sonnet 4.6 (Amazon Bedrock Edition)",
		"locationType": "AWS Region",
	}

	addTestOfferPrice(&offer, "in-regional", map[string]string{
		"regionCode":  baseAttrs["regionCode"],
		"servicename": baseAttrs["servicename"],
		"usagetype":   "USE1-MP:USE1_InputTokenCount-Units",
	}, "Units", "AWS Marketplace software usage|us-east-1|Million Input Tokens Regional CRIS", "3.3000000000")
	addTestOfferPrice(&offer, "in-global", map[string]string{
		"regionCode":  baseAttrs["regionCode"],
		"servicename": baseAttrs["servicename"],
		"usagetype":   "USE1-MP:USE1_InputTokenCount_Global-Units",
	}, "Units", "AWS Marketplace software usage|us-east-1|Million Input Tokens Global", "3.0000000000")
	addTestOfferPrice(&offer, "out-regional", map[string]string{
		"regionCode":  baseAttrs["regionCode"],
		"servicename": baseAttrs["servicename"],
		"usagetype":   "USE1-MP:USE1_OutputTokenCount-Units",
	}, "Units", "AWS Marketplace software usage|us-east-1|Million Response Tokens Regional CRIS", "16.5000000000")
	addTestOfferPrice(&offer, "out-global", map[string]string{
		"regionCode":  baseAttrs["regionCode"],
		"servicename": baseAttrs["servicename"],
		"usagetype":   "USE1-MP:USE1_OutputTokenCount_Global-Units",
	}, "Units", "AWS Marketplace software usage|us-east-1|Million Response Tokens Global", "15.0000000000")

	dst := map[string]modelPrice{}
	addBedrockOfferPricing(dst, offer, "us-east-1")
	price, ok := bedrockPriceForModel(&providerPricing{alias: dst}, "us.anthropic.claude-sonnet-4-6")
	if !ok {
		t.Fatal("expected Claude Sonnet 4.6 price to resolve")
	}
	if price.PriceIn != 3.0 || price.PriceOut != 15.0 {
		t.Fatalf("expected global standard pricing, got in=%v out=%v", price.PriceIn, price.PriceOut)
	}
}

func TestBedrockPricingPrefersGlobalStandardSnakeCaseMarketplace(t *testing.T) {
	offer := awsOfferFile{}
	addTestOfferPrice(&offer, "in-standard", map[string]string{
		"regionCode":  "us-east-1",
		"servicename": "Claude Opus 4.7 (Amazon Bedrock Edition)",
		"usagetype":   "USE1-MP:USE1_input_tokens_standard-Units",
	}, "Units", "AWS Marketplace software usage|us-east-1|Million Input Tokens Standard", "5.5000000000")
	addTestOfferPrice(&offer, "in-global", map[string]string{
		"regionCode":  "us-east-1",
		"servicename": "Claude Opus 4.7 (Amazon Bedrock Edition)",
		"usagetype":   "USE1-MP:USE1_input_tokens_global_standard-Units",
	}, "Units", "AWS Marketplace software usage|us-east-1|Input Tokens - Standard, Global", "5.0000000000")
	addTestOfferPrice(&offer, "out-standard", map[string]string{
		"regionCode":  "us-east-1",
		"servicename": "Claude Opus 4.7 (Amazon Bedrock Edition)",
		"usagetype":   "USE1-MP:USE1_output_tokens_standard-Units",
	}, "Units", "AWS Marketplace software usage|us-east-1|Million Response Tokens Standard", "27.5000000000")
	addTestOfferPrice(&offer, "out-global", map[string]string{
		"regionCode":  "us-east-1",
		"servicename": "Claude Opus 4.7 (Amazon Bedrock Edition)",
		"usagetype":   "USE1-MP:USE1_output_tokens_global_standard-Units",
	}, "Units", "AWS Marketplace software usage|us-east-1|Output Tokens - Standard, Global", "25.0000000000")

	dst := map[string]modelPrice{}
	addBedrockOfferPricing(dst, offer, "us-east-1")
	price, ok := bedrockPriceForModel(&providerPricing{alias: dst}, "us.anthropic.claude-opus-4-7")
	if !ok {
		t.Fatal("expected Claude Opus 4.7 price to resolve")
	}
	if price.PriceIn != 5.0 || price.PriceOut != 25.0 {
		t.Fatalf("expected global standard snake_case pricing, got in=%v out=%v", price.PriceIn, price.PriceOut)
	}
}

func TestBedrockPricingIgnoresNonStandardTiersForCanonicalDisplayPrice(t *testing.T) {
	offer := awsOfferFile{}
	base := map[string]string{
		"regionCode": "us-east-1",
		"model":      "qwen.qwen3-coder-next",
		"provider":   "Qwen",
	}

	addTestOfferPrice(&offer, "input-standard", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Input tokens",
		"feature":       "On-demand Inference",
		"usagetype":     "USE1-qwen.qwen3-coder-next-input-tokens",
		"service_tier":  "",
	}, "1K tokens", "$0.0005 per 1K input tokens for Qwen3 Coder Next in US East (N. Virginia)", "0.0005000000")
	addTestOfferPrice(&offer, "output-standard", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Output tokens",
		"feature":       "On-demand Inference",
		"usagetype":     "USE1-qwen.qwen3-coder-next-output-tokens",
	}, "1K tokens", "$0.0012 per 1K output tokens for Qwen3 Coder Next in US East (N. Virginia)", "0.0012000000")
	addTestOfferPrice(&offer, "input-flex", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Input tokens flex",
		"usagetype":     "USE1-qwen.qwen3-coder-next-input-tokens-flex",
		"service_tier":  "flex",
	}, "1K tokens", "$0.00025 per 1K token for qwen.qwen3-coder-next-input-tokens-flex in US East (N. Virginia)", "0.0002500000")
	addTestOfferPrice(&offer, "output-priority", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Output tokens priority",
		"usagetype":     "USE1-qwen.qwen3-coder-next-output-tokens-priority",
		"service_tier":  "priority",
	}, "1K tokens", "$0.0021 per 1K token for qwen.qwen3-coder-next-output-tokens-priority in US East (N. Virginia)", "0.0021000000")
	addTestOfferPrice(&offer, "input-batch", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "input tokens batch",
		"feature":       "On-demand Inference",
		"usagetype":     "USE1-qwen.qwen3-coder-next-input-tokens-batch",
	}, "1K tokens", "$0.00025 per 1K input tokens batch for Qwen3 Coder Next in US East (N. Virginia)", "0.0002500000")
	addTestOfferPrice(&offer, "cache-write", map[string]string{
		"regionCode":   base["regionCode"],
		"model":        base["model"],
		"provider":     base["provider"],
		"usagetype":    "USE1-qwen.qwen3-coder-next-cache-write-tokens",
		"service_tier": "standard",
	}, "1K tokens", "$0.0001 per 1K cache write tokens for Qwen3 Coder Next in US East (N. Virginia)", "0.0001000000")
	addTestOfferPrice(&offer, "reserved", map[string]string{
		"regionCode": base["regionCode"],
		"model":      base["model"],
		"provider":   base["provider"],
		"usagetype":  "USE1-qwen.qwen3-coder-next-reserved-input-tpm",
	}, "Units", "Per Hour per 1K Input TPM Reserved 1 Month Global", "0.1800000000")

	dst := map[string]modelPrice{}
	addBedrockOfferPricing(dst, offer, "us-east-1")
	price, ok := bedrockPriceForModel(&providerPricing{alias: dst}, "qwen.qwen3-coder-next")
	if !ok {
		t.Fatal("expected Qwen3 Coder Next price to resolve")
	}
	if price.PriceIn != 0.5 || price.PriceOut != 1.2 {
		t.Fatalf("expected canonical standard pricing, got in=%v out=%v", price.PriceIn, price.PriceOut)
	}
}

func TestBedrockPricingDoesNotStoreNonStandardOnlyAliases(t *testing.T) {
	offer := awsOfferFile{}
	addTestOfferPrice(&offer, "flex-only", map[string]string{
		"regionCode":    "us-east-1",
		"model":         "Flex Only",
		"provider":      "Example",
		"inferenceType": "Input tokens flex",
		"usagetype":     "USE1-example.flex-only-mantle-input-tokens-flex",
		"service_tier":  "flex",
	}, "1K tokens", "$0.0001 per 1K token for example.flex-only-mantle-input-tokens-flex in US East (N. Virginia)", "0.0001000000")

	dst := map[string]modelPrice{}
	addBedrockOfferPricing(dst, offer, "us-east-1")
	if _, ok := bedrockPriceForModel(&providerPricing{alias: dst}, "example.flex-only"); ok {
		t.Fatal("non-standard-only Bedrock rows should not create empty pricing aliases")
	}
}

func TestBedrockPricingMatchesGemmaModelIDToDisplayName(t *testing.T) {
	offer := awsOfferFile{}
	base := map[string]string{
		"regionCode":   "us-east-1",
		"model":        "Gemma 3 4B",
		"provider":     "Google",
		"service_tier": "standard",
	}

	addTestOfferPrice(&offer, "gemma-input", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Input tokens",
		"usagetype":     "USE1-google.gemma-3-4b-it-mantle-input-tokens-standard",
		"service_tier":  base["service_tier"],
	}, "1K tokens", "$0.00004 per 1K token for google.gemma-3-4b-it-mantle-input-tokens-standard in US East (N. Virginia)", "0.0000400000")
	addTestOfferPrice(&offer, "gemma-output", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Output tokens",
		"usagetype":     "USE1-google.gemma-3-4b-it-mantle-output-tokens-standard",
		"service_tier":  base["service_tier"],
	}, "1K tokens", "$0.00008 per 1K token for google.gemma-3-4b-it-mantle-output-tokens-standard in US East (N. Virginia)", "0.0000800000")

	dst := map[string]modelPrice{}
	addBedrockOfferPricing(dst, offer, "us-east-1")
	price, ok := bedrockPriceForModel(&providerPricing{alias: dst}, "google.gemma-3-4b-it")
	if !ok {
		t.Fatal("expected Gemma 3 4B price to resolve")
	}
	if price.PriceIn != 0.04 || price.PriceOut != 0.08 {
		t.Fatalf("expected Gemma standard pricing, got in=%v out=%v", price.PriceIn, price.PriceOut)
	}
}

func TestBedrockPricingMatchesNova2ModelIDToDisplayName(t *testing.T) {
	offer := awsOfferFile{}
	base := map[string]string{
		"regionCode": "us-east-1",
		"model":      "Nova 2.0 Lite",
		"provider":   "Amazon",
	}

	addTestOfferPrice(&offer, "nova-regional-input", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Input tokens",
		"usagetype":     "USE1-Nova2.0Lite-input-tokens",
	}, "1K tokens", "$0.00033 per 1K tokens for USE1-Nova2.0Lite-input-tokens in US East (N. Virginia)", "0.0003300000")
	addTestOfferPrice(&offer, "nova-regional-output", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Output tokens",
		"usagetype":     "USE1-Nova2.0Lite-output-tokens",
	}, "1K tokens", "$0.00275 per 1K tokens for USE1-Nova2.0Lite-output-tokens in US East (N. Virginia)", "0.0027500000")
	addTestOfferPrice(&offer, "nova-global-input", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Input tokens",
		"usagetype":     "USE1-Nova2.0Lite-input-tokens-cross-region-global",
	}, "1K tokens", "$0.0003 per 1K token for Nova2.0Lite-input-tokens-cross-region-global in US East (N. Virginia)", "0.0003000000")
	addTestOfferPrice(&offer, "nova-global-output", map[string]string{
		"regionCode":    base["regionCode"],
		"model":         base["model"],
		"provider":      base["provider"],
		"inferenceType": "Output tokens",
		"usagetype":     "USE1-Nova2.0Lite-output-tokens-cross-region-global",
	}, "1K tokens", "$0.0025 per 1K token for Nova2.0Lite-output-tokens-cross-region-global in US East (N. Virginia)", "0.0025000000")

	dst := map[string]modelPrice{}
	addBedrockOfferPricing(dst, offer, "us-east-1")
	price, ok := bedrockPriceForModel(&providerPricing{alias: dst}, "global.amazon.nova-2-lite-v1:0")
	if !ok {
		t.Fatal("expected Nova 2 Lite price to resolve")
	}
	if price.PriceIn != 0.3 || price.PriceOut != 2.5 {
		t.Fatalf("expected Nova 2 Lite global standard pricing, got in=%v out=%v", price.PriceIn, price.PriceOut)
	}
}

func TestBedrockPricingMatchesUsagetypeModelIDAliases(t *testing.T) {
	offer := awsOfferFile{}

	addTestOfferPrice(&offer, "mistral-input", map[string]string{
		"regionCode":    "us-east-1",
		"model":         "Ministral 8B 3.0",
		"provider":      "Mistral",
		"inferenceType": "Input tokens",
		"usagetype":     "USE1-mistral.ministral-3-8b-instruct-mantle-input-tokens-standard",
		"service_tier":  "standard",
	}, "1K tokens", "$0.00004 per 1K token for mistral.ministral-3-8b-instruct-mantle-input-tokens-standard in US East (N. Virginia)", "0.0000400000")
	addTestOfferPrice(&offer, "mistral-output", map[string]string{
		"regionCode":    "us-east-1",
		"model":         "Ministral 8B 3.0",
		"provider":      "Mistral",
		"inferenceType": "Output tokens",
		"usagetype":     "USE1-mistral.ministral-3-8b-instruct-mantle-output-tokens-standard",
		"service_tier":  "standard",
	}, "1K tokens", "$0.00012 per 1K token for mistral.ministral-3-8b-instruct-mantle-output-tokens-standard in US East (N. Virginia)", "0.0001200000")
	addTestOfferPrice(&offer, "nvidia-input", map[string]string{
		"regionCode":    "us-east-1",
		"model":         "NVIDIA Nemotron 3 Super 120B A12B",
		"provider":      "Nvidia",
		"inferenceType": "Input tokens",
		"usagetype":     "USE1-nvidia.nemotron-super-3-120b-mantle-input-tokens-standard",
		"service_tier":  "standard",
	}, "1K tokens", "$0.00045 per 1K token for nvidia.nemotron-super-3-120b-mantle-input-tokens-standard in US East (N. Virginia)", "0.0004500000")
	addTestOfferPrice(&offer, "nvidia-output", map[string]string{
		"regionCode":    "us-east-1",
		"model":         "NVIDIA Nemotron 3 Super 120B A12B",
		"provider":      "Nvidia",
		"inferenceType": "Output tokens",
		"usagetype":     "USE1-nvidia.nemotron-super-3-120b-mantle-output-tokens-standard",
		"service_tier":  "standard",
	}, "1K tokens", "$0.0018 per 1K token for nvidia.nemotron-super-3-120b-mantle-output-tokens-standard in US East (N. Virginia)", "0.0018000000")

	dst := map[string]modelPrice{}
	addBedrockOfferPricing(dst, offer, "us-east-1")
	pricing := &providerPricing{alias: dst}

	mistralPrice, ok := bedrockPriceForModel(pricing, "mistral.ministral-3-8b-instruct")
	if !ok {
		t.Fatal("expected Mistral usagetype model ID price to resolve")
	}
	assertPriceClose(t, mistralPrice.PriceIn, 0.04)
	assertPriceClose(t, mistralPrice.PriceOut, 0.12)

	nvidiaPrice, ok := bedrockPriceForModel(pricing, "nvidia.nemotron-super-3-120b")
	if !ok {
		t.Fatal("expected NVIDIA usagetype model ID price to resolve")
	}
	assertPriceClose(t, nvidiaPrice.PriceIn, 0.45)
	assertPriceClose(t, nvidiaPrice.PriceOut, 1.8)
}

func TestBedrockPricingMatchesDatedMistralDisplayAlias(t *testing.T) {
	offer := awsOfferFile{}
	addTestOfferPrice(&offer, "mistral-small-input", map[string]string{
		"regionCode":    "us-east-1",
		"model":         "Mistral Small",
		"provider":      "Mistral",
		"inferenceType": "Input tokens",
		"usagetype":     "USE1-MistralSmall-input-tokens",
	}, "1K tokens", "$0.0001 per 1K tokens for USE1-MistralSmall-input-tokens in US East (N. Virginia)", "0.0001000000")
	addTestOfferPrice(&offer, "mistral-small-output", map[string]string{
		"regionCode":    "us-east-1",
		"model":         "Mistral Small",
		"provider":      "Mistral",
		"inferenceType": "Output tokens",
		"usagetype":     "USE1-MistralSmall-output-tokens",
	}, "1K tokens", "$0.0003 per 1K tokens for USE1-MistralSmall-output-tokens in US East (N. Virginia)", "0.0003000000")

	dst := map[string]modelPrice{}
	addBedrockOfferPricing(dst, offer, "us-east-1")
	price, ok := bedrockPriceForModel(&providerPricing{alias: dst}, "mistral.mistral-small-2402-v1:0")
	if !ok {
		t.Fatal("expected dated Mistral display alias price to resolve")
	}
	if price.PriceIn != 0.1 || price.PriceOut != 0.3 {
		t.Fatalf("expected Mistral Small pricing, got in=%v out=%v", price.PriceIn, price.PriceOut)
	}
}

func TestBedrockPublicPricingOfferFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	urls := []string{
		awsOfferBaseURL + bedrockOfferCode + "/current/index.json",
		awsOfferBaseURL + bedrockFMOfferCode + "/current/index.json",
	}

	for _, url := range urls {
		body, err := fetchHTTPBody(ctx, url)
		if err != nil {
			t.Skipf("Bedrock pricing feed unavailable: %v", err)
		}
		var offer awsOfferFile
		if err := json.Unmarshal(body, &offer); err != nil {
			t.Fatalf("unmarshal %s: %v", url, err)
		}
		if len(offer.Products) == 0 {
			t.Fatalf("%s returned zero products", url)
		}
	}
}

func TestBedrockCanonicalPricingSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pricing := loadBedrockPricing(ctx, "us-east-1")
	if pricing == nil || !pricing.ready {
		t.Skip("Bedrock pricing feed unavailable")
	}

	for _, modelID := range []string{
		"deepseek.v3.2",
		"qwen.qwen3-coder-next",
		"google.gemma-3-4b-it",
		"google.gemma-3-12b-it",
		"google.gemma-3-27b-it",
		"global.amazon.nova-2-lite-v1:0",
		"mistral.ministral-3-8b-instruct",
		"mistral.mistral-small-2402-v1:0",
		"nvidia.nemotron-nano-9b-v2",
		"nvidia.nemotron-super-3-120b",
		"us.anthropic.claude-sonnet-4-6",
		"us.anthropic.claude-opus-4-7",
	} {
		price, ok := bedrockPriceForModel(pricing, modelID)
		if !ok {
			t.Fatalf("expected canonical Bedrock price for %s", modelID)
		}
		if !price.HasIn || !price.HasOut || price.PriceIn <= 0 || price.PriceOut <= 0 {
			t.Fatalf("expected non-zero canonical Bedrock input/output prices for %s, got %+v", modelID, price)
		}
	}
}

// TestLivePricing_ResolvesUnpinnedBuiltinAnthropic guards the decision to ship
// newer builtin Anthropic entries with zero prices: the AWS offer-file alias
// machinery must resolve input+output prices for every unpinned builtin
// Bedrock Anthropic model, or users would see "unknown" pricing forever.
// Network test (public offer files, no AWS creds needed); skips offline.
func TestLivePricing_ResolvesUnpinnedBuiltinAnthropic(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pricing := loadBedrockPricing(ctx, "us-east-1")
	if pricing == nil || !pricing.ready {
		t.Skip("bedrock offer files unavailable (offline?)")
	}
	for _, m := range BuiltinCatalog() {
		if m.Provider != "bedrock" || m.Type != "generation" || !strings.Contains(m.ID, "anthropic") {
			continue
		}
		if m.PriceIn > 0 || m.PriceOut > 0 {
			continue // pinned; enrichment optional
		}
		price, ok := bedrockPriceForModel(pricing, m.ID)
		if !ok {
			t.Errorf("unpinned builtin %s did not match the AWS offer file — pin a price or fix the alias machinery", m.ID)
			continue
		}
		if !price.HasIn || !price.HasOut || price.PriceIn <= 0 || price.PriceOut <= 0 {
			t.Errorf("unpinned builtin %s resolved incomplete pricing: in=%v(%v) out=%v(%v)", m.ID, price.PriceIn, price.HasIn, price.PriceOut, price.HasOut)
		}
	}
}
