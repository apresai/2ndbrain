package ai

import (
	"sync"
	"time"
)

// availableCache throttles lightweight probes used by Provider.Available().
// A successful probe is cached briefly so long-lived MCP sessions don't hit
// the network on every kb_* call. Caching failure lets a provider recover
// after a transient blip without us pinning it at "down" for the rest of the
// session.
//
// The previous implementation cached availability forever on first call.
// That was cheap-ish when the probe was a real embed/generate call (so you
// really wanted to pay the cost only once), but it meant failures were
// permanent and successes hid pricing from the user. With lightweight
// probes there's no reason to cache forever.
//
// The entry carries the failure's classification alongside the bool. Without
// it, a caller that asks "is it available?" and then "why not?" gets a cache
// hit with no reason, and the second question falls back to a generic answer,
// which is the vague message this mechanism exists to replace.
type availableCache struct {
	mu    sync.Mutex
	value bool
	code  TestErrorCode
	until time.Time
}

// availableCacheTTL is short enough to recover from transient provider
// outages within a single MCP session, long enough to amortize the probe
// across a burst of tool calls. 30s is a compromise informed by Bedrock's
// typical transient-failure durations.
const availableCacheTTL = 30 * time.Second

// transientFailureTTL is how long a TRANSIENT failure is remembered.
//
// It is not zero. Not caching a transient failure at all sounds like the
// honest choice, but the cache's other job is bounding a probe storm, and a
// long-lived MCP server holds one provider instance for its whole life: every
// kb_search and kb_ask consults Available(). With no entry at all, a
// black-holed network adds the probe's full 5s timeout to EVERY tool call, and
// a throttled control plane gets a fresh burst of requests per call, which is
// the cache being disabled in the exact regime it exists to protect.
//
// Two seconds bounds the storm to roughly one probe per couple of seconds
// while still letting a provider that recovers be seen as recovered almost
// immediately, which is the property that matters.
const transientFailureTTL = 2 * time.Second

// failureTTL returns how long a probe failure is worth remembering.
//
// A DEFINITIVE failure (bad credentials, access denied, a model that does not
// exist) will still be false a second from now, so it holds for the full TTL.
// A TRANSIENT one (timeout, unreachable, throttled) says nothing about the next
// call, so it holds only long enough to bound a burst.
//
// TestErrUnknown is treated as definitive on purpose: it preserves the
// amortization the cache exists for, and a genuine transient landing there is a
// missing rule in ClassifyProbeError, which is where it should be fixed (and
// where probe_error_test.go already guards the surface).
func failureTTL(code TestErrorCode) time.Duration {
	switch code {
	case TestErrTimeout, TestErrProviderUnreachable, TestErrThrottled:
		return transientFailureTTL
	default:
		return availableCacheTTL
	}
}

// availabilityFromProbe folds a probe result into the (ok, code) pair the
// AvailabilityReporter contract returns, caching each outcome for as long as it
// is worth trusting. provider selects the classification and remediation
// vocabulary (ClassifyProbeError and RemediationFor both tailor per provider),
// so it must name the provider that ran the probe.
func availabilityFromProbe(provider string, avail *availableCache, ok bool, err error) (bool, TestErrorCode) {
	if ok {
		avail.setWithCode(true, "")
		return true, ""
	}
	code := ClassifyProbeError(provider, err)
	avail.setWithCodeFor(false, code, failureTTL(code))
	return false, code
}

// get returns (cachedValue, hit) — hit==false when the caller needs to run
// a fresh probe.
func (c *availableCache) get() (bool, bool) {
	v, _, hit := c.getWithCode()
	return v, hit
}

// getWithCode is get plus the classification of a cached failure ("" for a
// cached success). Providers that can explain themselves use this so a cache
// hit still answers "why not?".
func (c *availableCache) getWithCode() (bool, TestErrorCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.until) {
		return c.value, c.code, true
	}
	return false, "", false
}

func (c *availableCache) set(v bool) { c.setWithCode(v, "") }

// setWithCode caches an outcome and the code that explains it, for the
// standard TTL.
func (c *availableCache) setWithCode(v bool, code TestErrorCode) {
	c.setWithCodeFor(v, code, availableCacheTTL)
}

// setWithCodeFor is setWithCode with an explicit lifetime, so a transient
// failure can be remembered for less time than a definitive one.
func (c *availableCache) setWithCodeFor(v bool, code TestErrorCode, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = v
	c.code = code
	c.until = time.Now().Add(ttl)
}
