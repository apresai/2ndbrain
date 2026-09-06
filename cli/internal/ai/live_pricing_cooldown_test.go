package ai

import (
	"context"
	"os"
	"strings"
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
	setupHome(t) // keep the stamp out of the developer's real pricing cache
	const url = "https://example.invalid/offers/current/index.json"

	if _, cooling := pricingFetchCooling(url); cooling {
		t.Fatal("a fetch that has never failed must not be in cooldown")
	}

	notePricingFetchFailure(url)
	t.Cleanup(func() { clearPricingFetchFailure(url) })

	until, cooling := pricingFetchCooling(url)
	if !cooling {
		t.Fatal("a fetch that just failed must be in cooldown")
	}
	if time.Until(until) > pricingFetchCooldown+time.Minute {
		t.Errorf("cooldown until %s is further out than the %s window", until, pricingFetchCooldown)
	}

	// The stamp is written into the pricing cache directory, creating it if the
	// failure beat the first successful write.
	if _, err := os.Stat(pricingFailurePath(url)); err != nil {
		t.Errorf("failure stamp not written: %v", err)
	}

	clearPricingFetchFailure(url)
	if _, cooling := pricingFetchCooling(url); cooling {
		t.Error("a success must clear the cooldown, or pricing never recovers")
	}
}

// The stamp is named from the URL, not from the cache entry. The two Bedrock
// offer URLs carry no region while their cache filenames do, so a path-keyed
// stamp would be keyed on a dimension the URL does not have and a second
// configured region would re-pay a stall the first had already proved.
func TestPricingFetchCooldown_IsKeyedByURLNotRegion(t *testing.T) {
	setupHome(t)
	// Deliberately NOT the real offer URLs. Other tests in this binary reach the
	// real ones for real, and on a machine where those fetches fail they stamp
	// the shared in-memory map, so a test naming them would assert on whatever
	// the rest of the package happened to do first.
	const url = "https://example.invalid/offers/v1.0/aws/AmazonBedrock/current/index.json"
	const other = "https://example.invalid/offers/v1.0/aws/AmazonBedrockFoundationModels/current/index.json"

	notePricingFetchFailure(url)
	t.Cleanup(func() {
		clearPricingFetchFailure(url)
		clearPricingFetchFailure(other)
	})

	// The same URL is still cooling however the caller names its cache entry,
	// which is what a second configured region amounts to.
	if _, cooling := pricingFetchCooling(url); !cooling {
		t.Error("the failed offer URL must stay in cooldown")
	}
	// A DIFFERENT offer must not be suppressed by its sibling's failure.
	if _, cooling := pricingFetchCooling(other); cooling {
		t.Error("a different offer URL must not inherit the cooldown")
	}
}

// The two layers exist because neither covers the other, and the LATER deadline
// has to win. Asserted with genuinely divergent timestamps: with both stamped at
// one instant, a regression that always preferred one source would still pass.
func TestPricingFetchCooldown_LaterDeadlineWins(t *testing.T) {
	setupHome(t)
	const url = "https://example.invalid/offers/divergent/index.json"

	notePricingFetchFailure(url)
	t.Cleanup(func() { clearPricingFetchFailure(url) })

	stamp := pricingFailurePath(url)
	if stamp == "" {
		t.Fatal("no pricing cache directory resolved")
	}

	// Disk is made OLD, so it is already outside the window. Memory is fresh, so
	// the answer must still be "cooling": an old stamp may not cut the in-process
	// cooldown short.
	old := time.Now().Add(-2 * pricingFetchCooldown)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
	until, cooling := pricingFetchCooling(url)
	if !cooling {
		t.Fatal("a stale disk stamp must not shorten the in-memory cooldown")
	}
	if !until.After(time.Now().Add(pricingFetchCooldown - time.Minute)) {
		t.Errorf("deadline %s came from the OLD disk stamp, not the fresh memory entry", until)
	}

	// Now the mirror image: memory cleared, disk fresh. A process that never
	// failed itself must still honor the stamp an earlier one left.
	pricingFetchFailures.mu.Lock()
	delete(pricingFetchFailures.at, url)
	pricingFetchFailures.mu.Unlock()

	now := time.Now()
	if err := os.Chtimes(stamp, now, now); err != nil {
		t.Fatal(err)
	}
	if _, cooling := pricingFetchCooling(url); !cooling {
		t.Error("a fresh disk stamp must cool a process with nothing in memory")
	}

	// And the mtime must actually be READ. With memory still empty, age the stamp
	// past the window: the answer has to flip to not-cooling. Without this, an
	// implementation that returned "cooling" on the mere PRESENCE of the file
	// would pass every other assertion here and then suppress pricing forever.
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
	if _, cooling := pricingFetchCooling(url); cooling {
		t.Error("an EXPIRED disk stamp must not cool; the stamp's mtime is the deadline, not its existence")
	}
}

// The cooldown only matters if loadCachedHTTPBody actually SKIPS the fetch, and
// nothing above proves that: the helpers could be perfect while the caller
// ignored them. This drives the real function.
//
// No mock server: the URL is unroutable, so the first call fails on its own and
// the second must come back from the cooldown instead of trying again. The
// second call being much faster than the first is the observable.
func TestPricingFetchCooldown_LoadCachedHTTPBodySkipsTheFetch(t *testing.T) {
	setupHome(t)
	const url = "https://not-a-real-host.invalid/offers/v1.0/aws/Skip/current/index.json"

	if _, err := loadCachedHTTPBody(context.Background(), url, "skip-test.json"); err == nil {
		t.Fatal("an unroutable host must fail rather than return a body")
	}

	start := time.Now()
	_, err := loadCachedHTTPBody(context.Background(), url, "skip-test.json")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("the second call must still report failure, not a body")
	}
	if !strings.Contains(err.Error(), "not retrying") {
		t.Errorf("second call error = %q; want the cooldown refusal, which means the fetch was skipped", err)
	}
	// A real DNS attempt cannot complete in this budget; a map and a stat can.
	if elapsed > 2*time.Second {
		t.Errorf("second call took %s; the cooldown did not short-circuit the fetch", elapsed)
	}
}
