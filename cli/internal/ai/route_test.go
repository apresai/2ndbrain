package ai

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRouteRef(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  RouteRef
		error string
	}{
		{name: "bare id", in: "xai.grok-4.6", want: RouteRef{ID: "xai.grok-4.6"}},
		{
			name: "plane only",
			in:   "xai.grok-4.6@mantle",
			want: RouteRef{ID: "xai.grok-4.6", Plane: PlaneMantle},
		},
		{
			name: "plane and region",
			in:   "xai.grok-4.6@mantle/us-west-2",
			want: RouteRef{ID: "xai.grok-4.6", Plane: PlaneMantle, Region: "us-west-2"},
		},
		{
			name: "provider qualified",
			in:   "bedrock|xai.grok-4.6@classic/us-east-1",
			want: RouteRef{Provider: "bedrock", ID: "xai.grok-4.6", Plane: PlaneClassic, Region: "us-east-1"},
		},
		// The separator choices exist so real ids survive. A ':' in the id is
		// why ':' could not be the route separator.
		{
			name: "id containing a colon is untouched",
			in:   "cohere.rerank-v3-5:0@classic/us-east-1",
			want: RouteRef{ID: "cohere.rerank-v3-5:0", Plane: PlaneClassic, Region: "us-east-1"},
		},
		// A '/' in the id is why '/' alone could not be the separator: an
		// OpenRouter id is indistinguishable from a qualified route.
		{
			name: "openrouter id containing a slash is untouched",
			in:   "nvidia/llama-nemotron-embed-vl-1b-v2:free",
			want: RouteRef{ID: "nvidia/llama-nemotron-embed-vl-1b-v2:free"},
		},
		{
			name: "ollama tag is untouched",
			in:   "llama3.1:8b",
			want: RouteRef{ID: "llama3.1:8b"},
		},
		{name: "unknown plane", in: "x@warp", error: "unknown plane"},
		{name: "region with a slash", in: "x@mantle/us-west-2/extra", error: "invalid region"},
		{name: "region with illegal chars", in: "x@mantle/us_west_2", error: "invalid region"},
		{name: "empty suffix", in: "x@", error: "empty route suffix"},
		{name: "empty id", in: "@mantle", error: "empty model id"},
		{name: "empty provider", in: "|x", error: "empty provider"},
		{name: "empty string", in: "   ", error: "empty model reference"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRouteRef(tc.in)
			if tc.error != "" {
				if err == nil || !strings.Contains(err.Error(), tc.error) {
					t.Fatalf("ParseRouteRef(%q) error = %v, want containing %q", tc.in, err, tc.error)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRouteRef(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseRouteRef(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestRouteRoundTrip pins that a fully-qualified route string parses back to
// the same key, so the form printed in an ambiguity message is pasteable.
// The four plane/region combinations are the property: a single example
// would miss id@/us-west-2, which String used to drop.
func TestRouteRoundTrip(t *testing.T) {
	id := "xai.grok-4.6"
	for _, plane := range []Plane{"", PlaneClassic, PlaneMantle} {
		for _, region := range []string{"", "us-west-2"} {
			k := RouteKey{Provider: "bedrock", ID: id, Plane: plane, Region: region}
			s := k.String()
			ref, err := ParseRouteRef(s)
			if err != nil {
				t.Fatalf("ParseRouteRef(%q): %v", s, err)
			}
			got := RouteKey{Provider: ref.Provider, ID: ref.ID, Plane: ref.Plane, Region: ref.Region}
			if got != k {
				t.Errorf("round trip of %q = %+v (plane=%q region=%q), want plane=%q region=%q",
					s, got, got.Plane, got.Region, k.Plane, k.Region)
			}
		}
	}
	// Non-Bedrock ids (no plane, no region) still round-trip as a bare id.
	k := RouteKey{Provider: "openrouter", ID: "nvidia/llama-nemotron-embed-vl-1b-v2:free"}
	s := k.String()
	ref, err := ParseRouteRef(s)
	if err != nil {
		t.Fatalf("ParseRouteRef(%q): %v", s, err)
	}
	got := RouteKey{Provider: ref.Provider, ID: ref.ID, Plane: ref.Plane, Region: ref.Region}
	if got != k {
		t.Errorf("round trip of %q = plane=%q region=%q, want plane=%q region=%q",
			s, got.Plane, got.Region, k.Plane, k.Region)
	}
}

// TestRouteKeyNonBedrockUnchanged pins that adding plane and region to the key
// does not change identity for providers that have neither. Their key stays
// the old provider+NUL+id form plus two empty components, so an ollama or
// openrouter row keeps its pre-route identity exactly.
func TestRouteKeyNonBedrockUnchanged(t *testing.T) {
	// Assert the PROPERTY, not the construction. Comparing routeKey against
	// `catalogKey(...) + "\x00\x00"` just transcribed routeKey's body and
	// would pass however the key were built.
	//
	// What must hold: for providers with no plane or region, route identity
	// draws exactly the same distinctions the old flat key did. Same model,
	// same key; different model or provider, different key.
	rows := []ModelInfo{
		{Provider: "ollama", ID: "nomic-embed-text"},
		{Provider: "ollama", ID: "llama3.1:8b"},
		{Provider: "openrouter", ID: "nomic-embed-text"},
		{Provider: "llama-local", ID: "gemma4-e2b"},
	}
	for i, a := range rows {
		for j, b := range rows {
			sameOld := catalogKey(a.Provider, a.ID) == catalogKey(b.Provider, b.ID)
			sameNew := routeKey(a.Route()) == routeKey(b.Route())
			if sameOld != sameNew {
				t.Errorf("rows %d/%d: old key says same=%v, route key says same=%v; non-Bedrock identity must be unchanged",
					i, j, sameOld, sameNew)
			}
		}
	}
	// And a duplicate of the same row is still the same route.
	if routeKey(rows[0].Route()) != routeKey(rows[0].Route()) {
		t.Error("route key must be stable for an identical row")
	}
}

// TestRouteKeySeparatesPlanes is the whole point of the change: the two
// spellings of Grok 4.6 that fought over one slot are now distinct rows, and
// the same id on two planes no longer collides.
func TestRouteKeySeparatesPlanes(t *testing.T) {
	mantle := ModelInfo{Provider: "bedrock", ID: "xai.grok-4.6", Plane: PlaneMantle, Region: "us-west-2"}
	classic := ModelInfo{Provider: "bedrock", ID: "xai.grok-4.6", Plane: PlaneClassic, Region: "us-east-1"}
	if routeKey(mantle.Route()) == routeKey(classic.Route()) {
		t.Fatal("mantle and classic routes for the same id must not share a key")
	}
	// Same plane, different region, is also distinct: Bedrock entitlement is
	// per-region, so these can independently succeed and fail.
	other := ModelInfo{Provider: "bedrock", ID: "xai.grok-4.6", Plane: PlaneMantle, Region: "us-east-1"}
	if routeKey(mantle.Route()) == routeKey(other.Route()) {
		t.Fatal("routes differing only by region must not share a key")
	}
}

func TestResolveRouteRefAmbiguityIsRefused(t *testing.T) {
	rows := []ModelInfo{
		{Provider: "bedrock", ID: "xai.grok-4.6", Plane: PlaneMantle, Region: "us-west-2"},
		{Provider: "bedrock", ID: "xai.grok-4.6", Plane: PlaneClassic, Region: "us-east-1"},
	}
	ref, err := ParseRouteRef("xai.grok-4.6")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveRouteRef(rows, ref, AIConfig{})
	var amb *AmbiguousRouteError
	if !errors.As(err, &amb) {
		t.Fatalf("want *AmbiguousRouteError, got %v", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(amb.Candidates))
	}
	// The message must name every candidate in pasteable form, since it is
	// the user's only way to learn which routes exist.
	msg := amb.Error()
	for _, want := range []string{"xai.grok-4.6@mantle/us-west-2", "xai.grok-4.6@classic/us-east-1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("ambiguity message missing %q:\n%s", want, msg)
		}
	}

	// Qualifying it resolves to exactly one row.
	ref, err = ParseRouteRef("xai.grok-4.6@mantle/us-west-2")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRouteRef(rows, ref, AIConfig{})
	if err != nil {
		t.Fatalf("qualified ref should resolve: %v", err)
	}
	if got.Plane != PlaneMantle || got.Region != "us-west-2" {
		t.Errorf("resolved wrong route: %+v", got.Route())
	}
}

func TestResolveRouteRefNoMatch(t *testing.T) {
	ref, _ := ParseRouteRef("nope@mantle/us-west-2")
	_, err := ResolveRouteRef(nil, ref, AIConfig{})
	var missing *NoSuchRouteError
	if !errors.As(err, &missing) {
		t.Fatalf("want *NoSuchRouteError, got %v", err)
	}
}

// TestPreferRoutesOrder pins the tiebreak chain, which several callers share
// (verify's probe pick, the collapsed list view, the ambiguity message).
func TestPreferRoutesOrder(t *testing.T) {
	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Region: "us-east-1"}}
	disabled := false

	configured := ModelInfo{Provider: "bedrock", ID: "m", Plane: PlaneMantle, Region: "us-west-2"}
	cfg.GenerationModel, cfg.GenerationPlane, cfg.GenerationRegion = "m", PlaneMantle, "us-west-2"

	knownGood := ModelInfo{Provider: "bedrock", ID: "m", Plane: PlaneClassic, Region: "us-east-2", TestedAt: "2026-08-25T00:00:00Z"}
	primary := ModelInfo{Provider: "bedrock", ID: "m", Plane: PlaneClassic, Region: "us-east-1"}
	mantleElsewhere := ModelInfo{Provider: "bedrock", ID: "m", Plane: PlaneMantle, Region: "us-east-1"}
	denied := ModelInfo{Provider: "bedrock", ID: "m", Plane: PlaneClassic, Region: "us-west-2", TestErrorCode: string(TestErrAccessDenied)}
	off := ModelInfo{Provider: "bedrock", ID: "m", Plane: PlaneClassic, Region: "eu-west-1", Enabled: &disabled}

	got := PreferRoutes([]ModelInfo{off, denied, mantleElsewhere, primary, knownGood, configured}, cfg)

	// 1: the configured route wins outright.
	if got[0].Route() != configured.Route() {
		t.Errorf("position 0 = %v, want the configured route %v", got[0].Route(), configured.Route())
	}
	// 2: last known good beats an untested primary-region row.
	if got[1].Route() != knownGood.Route() {
		t.Errorf("position 1 = %v, want the known-good route %v", got[1].Route(), knownGood.Route())
	}
	// 3 and 4: primary region, then classic before mantle.
	if got[2].Route() != primary.Route() {
		t.Errorf("position 2 = %v, want the primary-region route %v", got[2].Route(), primary.Route())
	}
	if got[3].Route() != mantleElsewhere.Route() {
		t.Errorf("position 3 = %v, want the mantle route %v", got[3].Route(), mantleElsewhere.Route())
	}
	// 6: demoted rows sort last but are never dropped, so --all-routes can
	// still reach them and a later probe can self-heal an access_denied.
	if len(got) != 6 {
		t.Fatalf("PreferRoutes dropped rows: got %d, want 6", len(got))
	}
	last := []RouteKey{got[4].Route(), got[5].Route()}
	for _, want := range []RouteKey{denied.Route(), off.Route()} {
		if want != last[0] && want != last[1] {
			t.Errorf("demoted route %v did not sort last (tail = %v)", want, last)
		}
	}
}

// TestInheritModelFacts covers the failure this helper exists to prevent: a
// per-region discovered row rendering nameless and unpriced because the
// builtin catalog declares those facts exactly once.
func TestInheritModelFacts(t *testing.T) {
	builtin := []ModelInfo{{
		Provider: "bedrock", ID: "us.anthropic.claude-sonnet-5", Plane: PlaneClassic, Region: "us-east-1",
		Name: "Claude Sonnet 5", ContextLen: 1000000, PriceIn: 3, PriceOut: 15, PriceSource: "builtin",
	}}
	rows := []ModelInfo{{
		Provider: "bedrock", ID: "us.anthropic.claude-sonnet-5", Plane: PlaneClassic, Region: "us-west-2",
	}}
	inheritModelFacts(rows, builtin)

	if rows[0].Name != "Claude Sonnet 5" || rows[0].ContextLen != 1000000 || rows[0].PriceIn != 3 {
		t.Errorf("model facts not inherited: %+v", rows[0])
	}
	// Route facts must NOT be inherited: the sibling's region would silently
	// repoint this row at the wrong endpoint.
	if rows[0].Region != "us-west-2" {
		t.Errorf("region was overwritten by inheritance: %q", rows[0].Region)
	}
}

// TestInheritModelFactsNeverOverwrites pins the fill-only-empty discipline: an
// authored value always wins, matching AdoptRoutingHints.
func TestInheritModelFactsNeverOverwrites(t *testing.T) {
	rows := []ModelInfo{{Provider: "bedrock", ID: "m", Name: "Authored", PriceIn: 9}}
	inheritModelFacts(rows, []ModelInfo{{Provider: "bedrock", ID: "m", Name: "Discovered", PriceIn: 1}})
	if rows[0].Name != "Authored" || rows[0].PriceIn != 9 {
		t.Errorf("inheritance overwrote authored facts: %+v", rows[0])
	}
}
