package ai

import (
	"sync"
	"time"
)

// availableCache throttles lightweight probes used by Provider.Available().
// A successful probe is cached briefly so long-lived MCP sessions don't hit
// the network on every kb_* call. Caching failure for the same TTL lets a
// provider recover after a transient blip without us pinning it at "down"
// for the rest of the session.
//
// The previous implementation cached availability forever on first call.
// That was cheap-ish when the probe was a real embed/generate call (so you
// really wanted to pay the cost only once), but it meant failures were
// permanent and successes hid pricing from the user. With lightweight
// probes there's no reason to cache forever.
type availableCache struct {
	mu    sync.Mutex
	value bool
	until time.Time
}

// availableCacheTTL is short enough to recover from transient provider
// outages within a single MCP session, long enough to amortize the probe
// across a burst of tool calls. 30s is a compromise informed by Bedrock's
// typical transient-failure durations.
const availableCacheTTL = 30 * time.Second

// get returns (cachedValue, hit) — hit==false when the caller needs to run
// a fresh probe.
func (c *availableCache) get() (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.until) {
		return c.value, true
	}
	return false, false
}

func (c *availableCache) set(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = v
	c.until = time.Now().Add(availableCacheTTL)
}
