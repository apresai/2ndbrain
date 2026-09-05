package ai

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A FAILED pricing fetch has to be remembered, and that is the entire point of
// the cooldown. Caching only successes meant every caller re-paid the whole
// network stall: the AmazonBedrock offer file is 16MB against a 15s client
// deadline, so on a slow link the fetch never finishes, and `models list`
// blocked about 30s on EVERY invocation rather than once. In one test binary
// that turned a single stall into twelve and pushed the package past Go's 10m
// timeout.
//
// No server is stood up here: the cooldown is bookkeeping over a stamp file and
// a map, so it is testable as the pure logic it is.
func TestPricingFetchCooldown(t *testing.T) {
	const url = "https://example.invalid/offers/current/index.json"
	cachePath := filepath.Join(t.TempDir(), "nested", "offer.json")

	if _, cooling := pricingFetchCooling(url, cachePath); cooling {
		t.Fatal("a fetch that has never failed must not be in cooldown")
	}

	notePricingFetchFailure(url, cachePath)
	t.Cleanup(func() { clearPricingFetchFailure(url, cachePath) })

	until, cooling := pricingFetchCooling(url, cachePath)
	if !cooling {
		t.Fatal("a fetch that just failed must be in cooldown")
	}
	if time.Until(until) > pricingFetchCooldown+time.Minute {
		t.Errorf("cooldown until %s is further out than the %s window", until, pricingFetchCooldown)
	}

	// The stamp is written beside the entry it refers to, creating the directory
	// if the failure beat the first successful write to it.
	if _, err := os.Stat(pricingFailurePath(cachePath)); err != nil {
		t.Errorf("failure stamp not written: %v", err)
	}

	clearPricingFetchFailure(url, cachePath)
	if _, cooling := pricingFetchCooling(url, cachePath); cooling {
		t.Error("a success must clear the cooldown, or pricing never recovers")
	}
}

// The in-memory half is keyed by URL rather than by cache path precisely so it
// survives a caller that redirects HOME. Every test in this package does exactly
// that through setupHome, giving each its own cache directory, which is why the
// on-disk stamp alone did not stop the repeated stalls.
func TestPricingFetchCooldown_SurvivesADifferentCacheDirectory(t *testing.T) {
	const url = "https://example.invalid/offers/current/index.json"
	first := filepath.Join(t.TempDir(), "offer.json")
	second := filepath.Join(t.TempDir(), "offer.json") // a different HOME entirely

	notePricingFetchFailure(url, first)
	t.Cleanup(func() {
		clearPricingFetchFailure(url, first)
		clearPricingFetchFailure(url, second)
	})

	if _, cooling := pricingFetchCooling(url, second); !cooling {
		t.Error("the same offer URL must stay in cooldown under a fresh cache directory")
	}
}
