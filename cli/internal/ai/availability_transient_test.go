package ai

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// A probe failure is cached only when it is DEFINITIVE. Caching a transient
// one turns a single network blip into a TTL-long outage for every caller;
// refusing to cache a definitive one (notably "no credentials", which every
// credential-free CI run and every unconfigured user hits) would re-probe at
// the probe's full timeout on every single call.
func TestCacheProbeFailure_TransientVsDefinitive(t *testing.T) {
	transient := []TestErrorCode{TestErrTimeout, TestErrProviderUnreachable, TestErrThrottled}
	definitive := []TestErrorCode{
		TestErrBadCredentials, TestErrAccessDenied, TestErrNotFound,
		TestErrIncompatible, TestErrInvalidRequest, TestErrUnknown,
	}
	for _, code := range transient {
		if cacheProbeFailure(code) {
			t.Errorf("%s is transient and must NOT be cached", code)
		}
	}
	for _, code := range definitive {
		if !cacheProbeFailure(code) {
			t.Errorf("%s is definitive and must be cached", code)
		}
	}
}

// availabilityFromProbe is what actually decides, so drive it directly and
// then read the cache back: a transient failure must leave the cache cold so
// the next call re-probes, a definitive one must leave it warm.
func TestAvailabilityFromProbe_CachesOnlyDefinitive(t *testing.T) {
	t.Run("transient leaves the cache cold", func(t *testing.T) {
		var c availableCache
		ok, code := availabilityFromProbe(&c, false, fmt.Errorf("probe: %w", context.DeadlineExceeded))
		if ok || code != TestErrTimeout {
			t.Fatalf("got (%v, %q), want (false, %q)", ok, code, TestErrTimeout)
		}
		if _, hit := c.get(); hit {
			t.Error("a transient failure was cached; the next call must re-probe")
		}
	})

	t.Run("definitive is cached", func(t *testing.T) {
		var c availableCache
		ok, code := availabilityFromProbe(&c, false, errors.New("static credentials are empty"))
		if ok || code != TestErrBadCredentials {
			t.Fatalf("got (%v, %q), want (false, %q)", ok, code, TestErrBadCredentials)
		}
		v, hit := c.get()
		if !hit || v {
			t.Errorf("a definitive failure must cache false; got hit=%v v=%v", hit, v)
		}
	})

	// The case that keeps this change from making every credential-free run
	// pay the probe timeout on every call: no credentials reaches us as a
	// credential error WRAPPING context.DeadlineExceeded (the SDK's EC2 IMDS
	// lookup), and it must still be treated as definitive.
	t.Run("missing credentials is definitive even though it wraps a deadline", func(t *testing.T) {
		var c availableCache
		err := fmt.Errorf("get credentials: failed to refresh cached credentials, no EC2 IMDS role found: %w", context.DeadlineExceeded)
		ok, code := availabilityFromProbe(&c, false, err)
		if ok || code != TestErrBadCredentials {
			t.Fatalf("got (%v, %q), want (false, %q)", ok, code, TestErrBadCredentials)
		}
		if _, hit := c.get(); !hit {
			t.Error("missing credentials must be cached; re-probing it costs the full timeout every call")
		}
	})

	t.Run("success is cached", func(t *testing.T) {
		var c availableCache
		ok, code := availabilityFromProbe(&c, true, nil)
		if !ok || code != "" {
			t.Fatalf("got (%v, %q), want (true, \"\")", ok, code)
		}
		v, hit := c.get()
		if !hit || !v {
			t.Errorf("success must cache true; got hit=%v v=%v", hit, v)
		}
	})
}
