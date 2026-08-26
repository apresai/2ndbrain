package ai

import (
	"fmt"
	"strings"
)

// SlotRoute is a configured slot resolved to the single endpoint it invokes.
type SlotRoute struct {
	// Route is the endpoint to call. Plane and Region may be empty when the
	// model is not in any catalog, which is legitimate: a model can exist
	// before 2nb learns about it, and the provider's own defaults apply.
	Route RouteKey
	// Strategy is the wire dialect for this route, empty for "provider
	// default".
	Strategy string
	// Endpoint is a full host override, normally empty (derived from region).
	Endpoint string
}

// UnroutedSlotError reports a slot whose model has several possible endpoints
// and whose config names none of them.
//
// This is the refusal that replaces the old silent fallback. Previously a
// model with no recorded routing fell through to classic Converse, which is
// how a mantle-only id was dispatched to a plane that cannot serve it and
// answered with "Invocation of model ID ... with on-demand throughput isn't
// supported". Refusing here turns that into an actionable message before any
// request is sent.
type UnroutedSlotError struct {
	Slot       string
	Model      string
	Candidates []ModelInfo
}

func (e *UnroutedSlotError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s model %q has %d possible routes and the config names none of them.\n",
		e.Slot, e.Model, len(e.Candidates))
	b.WriteString("Pick one:\n")
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "  2nb config set ai.%s_model %s\n", e.Slot, c.Route().Unqualified())
	}
	b.WriteString("(run `2nb models discover` first if a route you expect is missing)")
	return b.String()
}

// ResolveSlotRoute resolves the endpoint a configured slot invokes.
//
// Behavior, in order:
//
//   - Config that pins both plane and region is authoritative and is used as
//     given. This is the "nothing is inferred at invoke time" case.
//   - Otherwise the catalog is consulted for routes of that model, honoring
//     whichever of plane/region the config DID pin. Exactly one match is used.
//   - Several matches with nothing to choose between them is an
//     *UnroutedSlotError. It is not resolved by preference order on purpose:
//     preference is right for deciding what to PROBE, where a wrong guess
//     costs a probe, but wrong for deciding what to INVOKE, where a wrong
//     guess silently sends a user's real query to the wrong endpoint.
//   - No match at all is not an error: the model is simply unknown to the
//     catalog, and the provider defaults apply exactly as before.
func ResolveSlotRoute(slot string, want RouteKey, rows []ModelInfo) (SlotRoute, error) {
	out := SlotRoute{Route: want}
	if want.ID == "" {
		return out, nil
	}

	var matches []ModelInfo
	for _, m := range rows {
		if m.Provider != want.Provider || m.ID != want.ID {
			continue
		}
		if want.Plane != "" && m.Plane != want.Plane {
			continue
		}
		if want.Region != "" && m.Region != want.Region {
			continue
		}
		matches = append(matches, m)
	}

	// A fully-pinned config needs no catalog agreement: the user named the
	// endpoint, and an endpoint 2nb has not catalogued is still callable.
	if want.Plane != "" && want.Region != "" {
		if len(matches) == 1 {
			out.Strategy, out.Endpoint = matches[0].InvokeStrategy, matches[0].Endpoint
		} else if want.Plane == PlaneMantle {
			out.Strategy = StrategyBedrockMantleResponses
		}
		return out, nil
	}

	switch len(matches) {
	case 0:
		return out, nil
	case 1:
		m := matches[0]
		out.Route = m.Route()
		out.Strategy, out.Endpoint = m.InvokeStrategy, m.Endpoint
		return out, nil
	default:
		// Collapse rows that are the same endpoint, then retire any unpinned
		// template that concrete routes already cover, BEFORE calling it
		// ambiguous. Neither is a real alternative endpoint, and counting
		// them would manufacture a refusal out of one route: a legacy catalog
		// row canonicalizes onto its builtin's plane while keeping no region,
		// so almost every upgraded vault would otherwise see its working
		// generation model reported as ambiguous with itself.
		uniq := dedupeDiscoveredRoutes(matches)
		uniq = dropSupersededUnpinned(uniq, pinnedRoutePlanes(uniq))
		if len(uniq) == 1 {
			out.Route = uniq[0].Route()
			out.Strategy, out.Endpoint = uniq[0].InvokeStrategy, uniq[0].Endpoint
			return out, nil
		}
		return out, &UnroutedSlotError{Slot: slot, Model: want.ID, Candidates: uniq}
	}
}
