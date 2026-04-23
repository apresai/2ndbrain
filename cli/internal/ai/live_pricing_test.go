package ai

import (
	"context"
	"encoding/json"
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
		Provider:   "openrouter",
		ID:         "some/model",
		PriceIn:    9.99,
		PriceOut:   9.99,
		PriceSource: "user",
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

func TestEnrichModels_BuiltinFreePreservedWhenLiveAbsent(t *testing.T) {
	// A builtin :free model that is NOT in the live API response must keep
	// its builtin PriceSource. If clearModelPrice is called, PriceSource would
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

func TestEnrichModels_NonBuiltinClearedWhenLiveAbsent(t *testing.T) {
	// A non-builtin model that is absent from the ready live API gets cleared.
	model := ModelInfo{
		Provider:    "openrouter",
		ID:          "some/model",
		PriceSource: "vendor",
		PriceIn:     2.0,
	}
	orPricing := makeOrPricing(true, map[string]modelPrice{}) // ready but absent
	out := enrichModels([]ModelInfo{model}, orPricing, emptyBrPricing())
	if out[0].PriceSource != "" {
		t.Errorf("PriceSource = %q, want empty (stale vendor info cleared)", out[0].PriceSource)
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
