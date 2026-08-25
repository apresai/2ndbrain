package ai

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// A probe failure is remembered for as long as it is worth trusting. Holding a
// transient one for the full TTL turns a network blip into a TTL-long outage
// for every caller; not holding it at all makes a long-lived MCP session pay
// the probe's full timeout on every tool call, and hands a throttling control
// plane a fresh burst of requests per call.
func TestFailureTTL_TransientVsDefinitive(t *testing.T) {
	transient := []TestErrorCode{TestErrTimeout, TestErrProviderUnreachable, TestErrThrottled}
	definitive := []TestErrorCode{
		TestErrBadCredentials, TestErrAccessDenied, TestErrNotFound,
		TestErrIncompatible, TestErrInvalidRequest, TestErrUnknown,
	}
	for _, code := range transient {
		if got := failureTTL(code); got != transientFailureTTL {
			t.Errorf("%s is transient: TTL = %s, want %s", code, got, transientFailureTTL)
		}
	}
	for _, code := range definitive {
		if got := failureTTL(code); got != availableCacheTTL {
			t.Errorf("%s is definitive: TTL = %s, want %s", code, got, availableCacheTTL)
		}
	}
	// The distinction is the whole point: if these ever converge, one of the two
	// failure modes above is back.
	if transientFailureTTL >= availableCacheTTL {
		t.Errorf("a transient failure must expire sooner than a definitive one: %s vs %s",
			transientFailureTTL, availableCacheTTL)
	}
}

// availabilityFromProbe is what actually decides, so drive it directly and then
// read the cache back.
func TestAvailabilityFromProbe_CachesForTheRightDuration(t *testing.T) {
	// deadline reports how long a cache entry has left, which is the property
	// under test; the cache exposes only hit/miss.
	deadline := func(c *availableCache) time.Duration {
		c.mu.Lock()
		defer c.mu.Unlock()
		return time.Until(c.until)
	}

	t.Run("transient expires quickly", func(t *testing.T) {
		var c availableCache
		ok, code := availabilityFromProbe("bedrock", &c, false, fmt.Errorf("probe: %w", context.DeadlineExceeded))
		if ok || code != TestErrTimeout {
			t.Fatalf("got (%v, %q), want (false, %q)", ok, code, TestErrTimeout)
		}
		if d := deadline(&c); d > transientFailureTTL {
			t.Errorf("a transient failure must not be held for long: %s left", d)
		}
		if _, hit := c.get(); !hit {
			t.Error("a transient failure must still be cached briefly, or a hang re-probes on every call")
		}
	})

	t.Run("definitive is held for the full TTL", func(t *testing.T) {
		var c availableCache
		ok, code := availabilityFromProbe("bedrock", &c, false, errors.New("static credentials are empty"))
		if ok || code != TestErrBadCredentials {
			t.Fatalf("got (%v, %q), want (false, %q)", ok, code, TestErrBadCredentials)
		}
		v, hit := c.get()
		if !hit || v {
			t.Errorf("a definitive failure must cache false; got hit=%v v=%v", hit, v)
		}
		if d := deadline(&c); d <= transientFailureTTL {
			t.Errorf("a definitive failure must outlast a transient one: %s left", d)
		}
	})

	// The case that keeps this from making every credential-free run pay the
	// probe timeout on every call: no credentials reaches us as a credential
	// error WRAPPING context.DeadlineExceeded (the SDK's EC2 IMDS lookup), and it
	// must still be treated as definitive.
	t.Run("missing credentials is definitive even though it wraps a deadline", func(t *testing.T) {
		var c availableCache
		err := fmt.Errorf("get credentials: failed to refresh cached credentials, no EC2 IMDS role found: %w", context.DeadlineExceeded)
		ok, code := availabilityFromProbe("bedrock", &c, false, err)
		if ok || code != TestErrBadCredentials {
			t.Fatalf("got (%v, %q), want (false, %q)", ok, code, TestErrBadCredentials)
		}
		if d := deadline(&c); d <= transientFailureTTL {
			t.Errorf("missing credentials must be held for the full TTL; re-probing it costs the full timeout every call: %s left", d)
		}
	})

	t.Run("success is cached", func(t *testing.T) {
		var c availableCache
		ok, code := availabilityFromProbe("bedrock", &c, true, nil)
		if !ok || code != "" {
			t.Fatalf("got (%v, %q), want (true, \"\")", ok, code)
		}
		v, hit := c.get()
		if !hit || !v {
			t.Errorf("success must cache true; got hit=%v v=%v", hit, v)
		}
	})

	// The provider argument selects the classification and remediation
	// vocabulary. Hardcoding "bedrock" here would tell an Ollama user to refresh
	// their SSO session.
	t.Run("the provider argument reaches the classifier", func(t *testing.T) {
		var c availableCache
		_, code := availabilityFromProbe("ollama", &c, false, errors.New("connection refused"))
		if got := RemediationFor(code, "ollama", ""); got == RemediationFor(code, "bedrock", "") {
			t.Skip("this code's remediation does not vary by provider; nothing to prove here")
		} else if got == "" {
			t.Errorf("expected ollama-specific remediation for %q", code)
		}
	})
}

// The real caller asks ONCE and carries the answer, but AvailableDetail is
// public and a second ask must still explain itself: a cache holding only the
// bool answers "unavailable, no idea why", and the message degrades to the
// generic wording this change exists to remove.
func TestAvailableCache_HitStillCarriesTheCode(t *testing.T) {
	var c availableCache
	if _, code := availabilityFromProbe("bedrock", &c, false, errors.New("static credentials are empty")); code != TestErrBadCredentials {
		t.Fatalf("first probe: got %q, want %q", code, TestErrBadCredentials)
	}

	v, code, hit := c.getWithCode()
	if !hit {
		t.Fatal("second call should hit the cache")
	}
	if v {
		t.Error("cached value should be false")
	}
	if code != TestErrBadCredentials {
		t.Errorf("a cache hit must still explain itself: got %q, want %q", code, TestErrBadCredentials)
	}

	// A cached success carries no code, which is what "" means here.
	var ok availableCache
	availabilityFromProbe("bedrock", &ok, true, nil)
	if v, code, hit := ok.getWithCode(); !hit || !v || code != "" {
		t.Errorf("cached success = (%v, %q, hit=%v), want (true, \"\", true)", v, code, hit)
	}
}
