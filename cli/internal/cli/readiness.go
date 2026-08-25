package cli

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/apresai/2ndbrain/internal/ai"
)

// providerReadiness is a RESOLVED availability verdict: whether a provider is
// ready and, when it is not and the provider can say, the classified reason.
//
// It exists to be carried. A readiness answer used to be re-derived at each
// place that reports it: `2nb ai status` asked once for the JSON field, again
// inside derivePortability, and again inside bedrockProviderStatus. Those were
// repeated CALLS, not repeated network round trips. A success or a definitive
// failure holds in the cache for the full TTL, so the repeats cost nothing in
// the common case, and the live-probe count is one per registered role either
// way. Only a TRANSIENT verdict, which is deliberately short-lived, could lapse
// between calls and make a repeat pay a fresh probe, on exactly the degraded
// network where a status command most needs to answer quickly.
type providerReadiness struct {
	ready bool
	code  ai.TestErrorCode
	// resolved distinguishes "probed, and it is not ready" from "never probed".
	// Without it the zero value is indistinguishable from a genuine failure, so
	// a caller that never ran a probe would report a confident-sounding cause it
	// never observed.
	resolved bool
}

// resolveReadiness probes once. A nil provider is not ready and has nothing to
// say about why, which callers should handle before asking (there is a better
// message for "not registered" than anything a probe can produce).
func resolveReadiness(ctx context.Context, p any) providerReadiness {
	ready, code := ai.Availability(ctx, p)
	return providerReadiness{ready: ready, code: code, resolved: true}
}

// readinessResolver defers a probe to the point of use. derivePortability needs
// a verdict only in one branch, and several of its early returns (empty vault,
// nothing indexed, no provider configured) are reached without ever consulting
// it. Passing the value eagerly made an offline `config doctor` pay a live
// probe whose answer was then discarded.
type readinessResolver func() providerReadiness

// knownReadiness wraps a verdict the caller already resolved.
func knownReadiness(r providerReadiness) readinessResolver {
	return func() providerReadiness { return r }
}

// readinessProbe pairs a provider with where its verdict should land.
type readinessProbe struct {
	provider any
	out      *providerReadiness
}

// resolveReadinessAll probes every non-nil target concurrently, because each
// probe can block for seconds on a degraded network and a status command has no
// reason to pay their sum. Nil targets are skipped, leaving the zero value,
// which reports itself as unresolved rather than as a failure nobody observed.
func resolveReadinessAll(ctx context.Context, probes ...readinessProbe) {
	var wg sync.WaitGroup
	for _, p := range probes {
		if p.provider == nil {
			continue
		}
		wg.Add(1)
		go func(p readinessProbe) {
			defer wg.Done()
			*p.out = resolveReadiness(ctx, p.provider)
		}(p)
	}
	wg.Wait()
}

// hint renders the one-line "why not ready, and what to do" clause for a
// status surface. Empty when the provider is ready.
func (r providerReadiness) hint(provider string) string {
	if r.ready {
		return ""
	}
	if r.code == "" {
		// A provider that cannot classify itself (ollama, openrouter,
		// llama-local today). Say only what is known. The previous wording named
		// two providers and let the reader pick, which is right by luck and
		// blames credentials for a provider that has none.
		return fmt.Sprintf("Provider %q is not ready, and it did not report a cause. Run `2nb doctor` for a full check.", provider)
	}
	return fmt.Sprintf("Provider %q is not ready (%s). %s", provider, r.code, ai.ReadinessRemediation(r.code, provider))
}

// shortReason is hint's compact form for the per-provider `reason` field, which
// sits next to the provider's own name in the UI and so does not repeat it.
func (r providerReadiness) shortReason() string {
	if r.ready {
		return ""
	}
	if r.code == "" {
		// Not "credentials missing or region unreachable": that asserted a cause
		// nobody observed, on the field the macOS app renders as its headline
		// readiness line.
		return "not ready, cause not reported"
	}
	return fmt.Sprintf("%s (%s)", ai.NotReadySummary(r.code), r.code)
}

// embedderNotReadyError and generatorNotReadyError name the role so a message
// says whether embedding or generation failed; the two were interchangeable
// before. The words come from ai.NotReadyMessage, which internal/mcp and
// internal/retrieve also use, so a user meets one explanation of a given
// failure wherever they meet it.
//
// errors.New, not fmt.Errorf: the message is already formatted, and a provider
// name containing a % would otherwise corrupt it.
func embedderNotReadyError(provider string, code ai.TestErrorCode) error {
	return errors.New(ai.NotReadyMessage("embedding", provider, code))
}

func generatorNotReadyError(provider string, code ai.TestErrorCode) error {
	return errors.New(ai.NotReadyMessage("generation", provider, code))
}
