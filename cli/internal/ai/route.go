package ai

import (
	"fmt"
	"sort"
	"strings"
)

// Plane names the Bedrock invocation plane a catalog row rides.
//
// Plane is part of a row's IDENTITY, not a preference: the same model id can
// be reachable on both planes with different entitlement, different pricing,
// and a different wire dialect. Plane selects the CLIENT (bedrock-runtime vs
// the bedrock-mantle REST host); InvokeStrategy selects the ENVELOPE within
// that client (the mantle plane has exactly one, the classic plane has many).
//
// Empty means "not a Bedrock row". Only provider "bedrock" carries a plane;
// ollama, openrouter, and llama-local rows leave it unset so their route key
// is byte-identical to the pre-route provider+id form.
type Plane string

const (
	// PlaneClassic is the original Bedrock control/runtime plane: the
	// Converse and InvokeModel APIs over bedrock-runtime.<region>.amazonaws.com,
	// reachable with SigV4 or a bearer token, and enumerable via
	// ListFoundationModels / ListInferenceProfiles.
	PlaneClassic Plane = "classic"

	// PlaneMantle is the partner-hosted frontier plane: the OpenAI-Responses
	// dialect over https://bedrock-mantle.<region>.api.aws, bearer-token only,
	// invisible to the classic control plane, and enumerable only via that
	// host's own /v1/models listing.
	PlaneMantle Plane = "mantle"
)

// KnownPlanes returns the closed set of valid planes, for validation and for
// naming the alternatives in an error message.
func KnownPlanes() []Plane { return []Plane{PlaneClassic, PlaneMantle} }

// IsKnownPlane reports whether p is a plane 2nb can dispatch. The empty plane
// is NOT known: it means "no plane", which is valid on a non-Bedrock row but
// is never a routable Bedrock value.
func IsKnownPlane(p Plane) bool { return p == PlaneClassic || p == PlaneMantle }

// RouteKey is the composite identity of one catalog row: the model, the plane
// it rides, and the region whose endpoint it calls.
//
// This replaces the flat (Provider, ID) identity. That flat key could not
// represent a model served on both planes or in several regions, so discovery
// collapsed the matrix and the invoke path guessed the route back from id
// spelling. The guess was wrong in production three times (2026-08-20 twice,
// 2026-08-21), each time routing a mantle-only id onto classic Converse.
type RouteKey struct {
	Provider string
	ID       string
	Plane    Plane
	Region   string
}

// Route returns the row's composite identity.
func (m ModelInfo) Route() RouteKey {
	return RouteKey{Provider: m.Provider, ID: m.ID, Plane: m.Plane, Region: m.Region}
}

// routeKey is the composite identity key for a catalog ROW: provider, id,
// plane, and region, NUL-separated so no field can spoof another. It replaces
// catalogKey and is shared by the discovery merge, the user-catalog overlay,
// and every save path.
//
// A non-Bedrock row has an empty plane and region, so its key is the old
// provider+NUL+id form plus two empty components: those rows keep their
// pre-route identity exactly.
func routeKey(k RouteKey) string {
	return k.Provider + "\x00" + k.ID + "\x00" + string(k.Plane) + "\x00" + k.Region
}

// String renders a fully-qualified route: provider|id@plane/region. Always
// unambiguous, so it is what error messages and --json emit.
func (k RouteKey) String() string {
	var b strings.Builder
	if k.Provider != "" {
		b.WriteString(k.Provider)
		b.WriteByte('|')
	}
	b.WriteString(k.ID)
	if k.Plane != "" {
		b.WriteByte('@')
		b.WriteString(string(k.Plane))
		if k.Region != "" {
			b.WriteByte('/')
			b.WriteString(k.Region)
		}
	}
	return b.String()
}

// Unqualified renders the route WITHOUT the provider prefix: id@plane/region.
//
// This is the form to print in a suggested shell command. The provider prefix
// uses '|', which a shell reads as a pipe, so a suggestion carrying it would
// not survive being pasted. Anywhere the provider is already implied — a
// config slot, a --provider flag — this is the right rendering; String() stays
// for keys and logs where full qualification matters more than pasteability.
func (k RouteKey) Unqualified() string {
	bare := k
	bare.Provider = ""
	return bare.String()
}

// RouteRef is a possibly-partial, user-supplied reference to a route. An empty
// field means "unconstrained" and is filled by matching against the catalog; a
// match count other than exactly one is an error, never a first-match-wins pick.
type RouteRef struct {
	Provider string
	ID       string
	Plane    Plane
	Region   string
}

// String renders the reference, omitting the parts the user did not supply.
func (r RouteRef) String() string {
	return RouteKey{Provider: r.Provider, ID: r.ID, Plane: r.Plane, Region: r.Region}.String()
}

// ParseRouteRef parses the canonical route form:
//
//	[<provider>|]<id>[@<plane>[/<region>]]
//
// Examples: "xai.grok-4.6", "xai.grok-4.6@mantle",
// "xai.grok-4.6@mantle/us-west-2", "bedrock|xai.grok-4.6@mantle/us-west-2".
//
// Separator choice is load-bearing, not taste. ':' is unusable because model
// ids contain it (cohere.rerank-v3-5:0, anthropic...-v1:0, ollama llama3.1:8b).
// '/' alone is unusable because OpenRouter ids contain it
// (nvidia/llama-nemotron-embed-vl-1b-v2:free), so "a/b" would be ambiguous
// between a qualified route and a bare id. '@' appears in no provider name, no
// Bedrock id, no OpenRouter id, and no Ollama tag, and needs no shell quoting,
// so the common route-qualified form is paste-safe. '|' is kept for provider
// qualification because it is already the established key form.
func ParseRouteRef(s string) (RouteRef, error) {
	var ref RouteRef
	rest := strings.TrimSpace(s)
	if rest == "" {
		return ref, fmt.Errorf("empty model reference")
	}

	// Provider qualification comes first: everything before the first '|'.
	if i := strings.Index(rest, "|"); i >= 0 {
		ref.Provider = rest[:i]
		rest = rest[i+1:]
		if ref.Provider == "" {
			return ref, fmt.Errorf("empty provider in %q", s)
		}
	}

	// The route suffix is everything after the LAST '@'. Last, not first, so
	// an id that ever contains '@' keeps its literal text.
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		suffix := rest[i+1:]
		rest = rest[:i]
		if suffix == "" {
			return ref, fmt.Errorf("empty route suffix in %q (want @plane or @plane/region)", s)
		}
		plane, region, hasRegion := strings.Cut(suffix, "/")
		ref.Plane = Plane(plane)
		if !IsKnownPlane(ref.Plane) {
			return ref, fmt.Errorf("unknown plane %q in %q (want one of: %s)", plane, s, planeList())
		}
		if hasRegion {
			if !isBareRegionLabel(region) {
				return ref, fmt.Errorf("invalid region %q in %q: expected a bare region label like us-west-2", region, s)
			}
			ref.Region = region
		}
	}

	if rest == "" {
		return ref, fmt.Errorf("empty model id in %q", s)
	}
	ref.ID = rest
	return ref, nil
}

func planeList() string {
	names := make([]string, 0, len(KnownPlanes()))
	for _, p := range KnownPlanes() {
		names = append(names, string(p))
	}
	return strings.Join(names, ", ")
}

// Matches reports whether row satisfies every constrained field of the ref.
func (r RouteRef) Matches(row ModelInfo) bool {
	if r.Provider != "" && row.Provider != r.Provider {
		return false
	}
	if r.ID != "" && row.ID != r.ID {
		return false
	}
	if r.Plane != "" && row.Plane != r.Plane {
		return false
	}
	if r.Region != "" && row.Region != r.Region {
		return false
	}
	return true
}

// AmbiguousRouteError reports a reference that matched more than one route.
// It carries every candidate so the caller can print pasteable qualified forms
// rather than making the user guess which routes exist.
type AmbiguousRouteError struct {
	Ref        RouteRef
	Candidates []ModelInfo
}

func (e *AmbiguousRouteError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s matches %d routes; qualify it as one of:", e.Ref.String(), len(e.Candidates))
	for _, c := range e.Candidates {
		// Unqualified, because these lines are meant to be pasted back as an
		// argument and the provider prefix uses '|', which the shell would
		// read as a pipe.
		fmt.Fprintf(&b, "\n  %-42s %s", c.Route().Unqualified(), routeVerdict(c))
	}
	return b.String()
}

// NoSuchRouteError reports a reference that matched nothing.
type NoSuchRouteError struct{ Ref RouteRef }

func (e *NoSuchRouteError) Error() string {
	return fmt.Sprintf("no route matches %s", e.Ref.String())
}

// routeVerdict renders a row's last-probe outcome for the ambiguity message,
// so the error that refuses to guess is also the answer to "which one?".
func routeVerdict(m ModelInfo) string {
	switch {
	case m.TestedAt == "":
		return "untested"
	case m.TestErrorCode != "":
		return fmt.Sprintf("%s %s", m.TestErrorCode, shortDate(m.TestedAt))
	case m.TestLatencyMs > 0:
		return fmt.Sprintf("passed %s (%dms)", shortDate(m.TestedAt), m.TestLatencyMs)
	default:
		return fmt.Sprintf("passed %s", shortDate(m.TestedAt))
	}
}

func shortDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// ResolveRouteRef returns the single row matching ref.
//
// It never picks a first match: an ambiguous reference is an
// *AmbiguousRouteError carrying every candidate in PreferRoutes order, so the
// caller can refuse and print the qualified forms. This generalizes the
// existing discover --add convention (a bare id matching two providers is
// refused, not guessed) to every axis of the route.
func ResolveRouteRef(rows []ModelInfo, ref RouteRef, cfg AIConfig) (ModelInfo, error) {
	var matches []ModelInfo
	for _, m := range rows {
		if ref.Matches(m) {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 0:
		return ModelInfo{}, &NoSuchRouteError{Ref: ref}
	case 1:
		return matches[0], nil
	default:
		return ModelInfo{}, &AmbiguousRouteError{Ref: ref, Candidates: PreferRoutes(matches, cfg)}
	}
}

// PreferRoutes orders routes best-first. It is the single definition of "best
// route", shared by verify's probe choice, the collapsed models-list view, the
// ambiguity message, and ai setup, so the notion cannot drift between them.
//
// Order:
//  1. the route the config already names for a slot — verify must check the
//     route the user actually runs before any other
//  2. last known good, most recent first — entitlement is the scarce fact, and
//     a route that worked yesterday is the cheapest true answer
//  3. region == the primary region — lowest latency, and it preserves the
//     self-heal property that primary is always re-checked
//  4. classic before mantle — classic needs no bearer token and carries richer
//     metadata. This is all that survives of dedupeDiscoveredBedrock's
//     "classic wins", demoted from a data-destroying collapse to a tiebreak
//  5. configured region order, then lexical, so runs are reproducible
//
// Rows with a non-retryable last error or an explicit Enabled:false sort LAST
// but are never dropped: they stay reachable under --all-routes and get
// re-probed once better-ranked siblings also fail, which is the access-denied
// self-heal.
func PreferRoutes(rows []ModelInfo, cfg AIConfig) []ModelInfo {
	primary := ResolveBedrockConfig(cfg.Bedrock).Region
	configured := map[string]bool{}
	for _, k := range []RouteKey{cfg.GenerationRoute(), cfg.EmbeddingRoute(), cfg.RerankRoute()} {
		if k.ID != "" {
			configured[routeKey(k)] = true
		}
	}
	regionRank := map[string]int{}
	for i, r := range ResolveBedrockRegions(cfg.Bedrock) {
		regionRank[r] = i
	}
	rank := func(m ModelInfo) int {
		if i, ok := regionRank[m.Region]; ok {
			return i
		}
		return len(regionRank) + 1
	}

	out := append([]ModelInfo(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		// 6. demoted rows sort last, but are never dropped.
		if da, db := routeDemoted(a), routeDemoted(b); da != db {
			return !da
		}
		// 1. the configured route.
		if ca, cb := configured[routeKey(a.Route())], configured[routeKey(b.Route())]; ca != cb {
			return ca
		}
		// 2. last known good, most recent first.
		ga, gb := routeKnownGood(a), routeKnownGood(b)
		if ga != gb {
			return ga
		}
		if ga && gb && a.TestedAt != b.TestedAt {
			return a.TestedAt > b.TestedAt
		}
		// 3. primary region.
		if pa, pb := a.Region == primary, b.Region == primary; pa != pb {
			return pa
		}
		// 4. classic before mantle.
		if a.Plane != b.Plane {
			return a.Plane == PlaneClassic
		}
		// 5. configured region order, then lexical.
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		return a.ID < b.ID
	})
	return out
}

// routeKnownGood reports a route with a recorded passing probe.
func routeKnownGood(m ModelInfo) bool {
	return m.TestedAt != "" && m.TestError == "" && m.TestErrorCode == ""
}

// routeDemoted reports a route that should sort last: explicitly disabled, or
// whose last probe failed for a reason INTRINSIC to the route rather than to
// the environment.
//
// The distinction matters and is easy to get backwards. regionRetryable's set
// (not_found, invalid_request, access_denied) means "this plane/region does
// not serve this model to this account, try another" — that is precisely a
// route-intrinsic verdict, so those demote. incompatible joins them: the
// model rejected the plane's dialect. Everything else (timeout, throttled,
// provider_unreachable, bad_credentials) is environmental and says nothing
// about the route, so a single network blip must not push an otherwise good
// route below its siblings.
//
// Demoted rows are still never dropped: --all-routes reaches them, and they
// get re-probed once better-ranked siblings also fail, which is how a
// resolved access_denied self-heals.
func routeDemoted(m ModelInfo) bool {
	if m.Enabled != nil && !*m.Enabled {
		return true
	}
	if m.TestErrorCode == "" {
		return false
	}
	code := TestErrorCode(m.TestErrorCode)
	return regionRetryable(code) || code == TestErrIncompatible
}

// planeForStrategy derives a Bedrock row's plane from the invoke strategy it
// already declares. This is derivation from recorded fact, not a guess: the
// mantle strategy names the mantle plane by definition, and every other
// Bedrock dialect is a classic-plane one.
func planeForStrategy(strategy string) Plane {
	if strategy == StrategyBedrockMantleResponses {
		return PlaneMantle
	}
	return PlaneClassic
}

// canonicalizeUserRoutes fills in the plane of user-catalog rows written
// before routes existed, so they still overlay onto the builtin they describe.
//
// This is NOT the "guess the route" behavior routes exist to remove. It uses
// only two sources, both recorded fact:
//
//  1. the row's own invoke_strategy, if it has one
//  2. the plane of the builtin catalog entry with the SAME id, if one exists
//
// Anything else is left with an empty plane on purpose. Such a row is an
// unpinned template that names no endpoint, and the invoke path refuses it
// and asks the user to pick rather than falling through to classic Converse,
// which is precisely the failure that motivated this work. The route it should
// have is discovered by a walk, not deduced here.
//
// Without this, every pre-existing row would stop matching its builtin the
// moment builtins gained a plane, silently dropping user price overrides,
// tiers, and enable flags.
func canonicalizeUserRoutes(rows []ModelInfo, builtin []ModelInfo) {
	planeByID := make(map[string]Plane, len(builtin))
	for _, b := range builtin {
		if b.Provider == "bedrock" && b.Plane != "" {
			planeByID[b.ID] = b.Plane
		}
	}
	for i := range rows {
		if rows[i].Provider != "bedrock" || rows[i].Plane != "" {
			continue
		}
		if rows[i].InvokeStrategy != "" {
			rows[i].Plane = planeForStrategy(rows[i].InvokeStrategy)
			continue
		}
		if p, ok := planeByID[rows[i].ID]; ok {
			rows[i].Plane = p
		}
	}
}

// markActiveRoutes flags EXACTLY ONE row per configured slot as Active.
//
// isActiveModel falls back to id-only matching when the config names no plane
// or region, which is every vault upgrading from before routes. That is
// deliberate (the model must still read as current), but applied row by row it
// marks every route of the model, so the picker shows one model three times
// and the GUI implies three endpoints are in use at once.
//
// Where several rows match, PreferRoutes picks the one the invoke path would
// actually resolve to, so what is shown as active is what is actually running.
func markActiveRoutes(cfg AIConfig, lists ...*[]ModelInfo) {
	type slot struct{ id, typ string }
	wanted := map[slot]bool{}
	if cfg.EmbeddingModel != "" {
		wanted[slot{cfg.EmbeddingModel, "embedding"}] = true
	}
	if cfg.GenerationModel != "" {
		wanted[slot{cfg.GenerationModel, "generation"}] = true
	}

	// Clear first, then elect one winner per slot across BOTH halves.
	candidates := map[slot][]ModelInfo{}
	for _, l := range lists {
		for i := range *l {
			(*l)[i].Active = false
			m := (*l)[i]
			if m.Provider != cfg.Provider {
				continue
			}
			k := slot{m.ID, m.Type}
			if wanted[k] && isActiveModel(m, cfg) {
				candidates[k] = append(candidates[k], m)
			}
		}
	}
	// Keyed by route AND type. routeKey carries no Type, so a model id
	// configured in both slots with a different best route per slot would
	// otherwise let one slot's winner mark the other slot's row too.
	winners := map[string]bool{}
	for k, group := range candidates {
		winners[routeKey(PreferRoutes(group, cfg)[0].Route())+"\x00"+k.typ] = true
	}
	for _, l := range lists {
		for i := range *l {
			if winners[routeKey((*l)[i].Route())+"\x00"+(*l)[i].Type] {
				(*l)[i].Active = true
			}
		}
	}
}

// RouteIsPinned reports whether a row names a concrete region. An unpinned
// Bedrock row is a region-agnostic template ("Claude Sonnet 5 on classic")
// rather than a callable endpoint: it resolves against whatever
// ai.bedrock.region says at the time.
func RouteIsPinned(m ModelInfo) bool { return m.Provider != "bedrock" || m.Region != "" }

// routePlane identifies a model on one plane, across every region it is
// served in. It is the grain at which an unpinned template is superseded.
type routePlane struct {
	provider, id string
	plane        Plane
}

// PinnedRoutes records which models and planes have at least one CONCRETE
// per-region row, at the two grains an unpinned template can be superseded at.
type PinnedRoutes struct {
	byPlane map[routePlane]bool
	byModel map[string]bool
}

// pinnedRoutePlanes collects, across every list given, which (provider, id,
// plane) triples and which (provider, id) pairs have a concrete per-region row.
//
// Both grains are needed because templates come in two shapes. A builtin
// carries its plane but no region ("Claude Sonnet 5 on classic"), so it is
// superseded only by concrete rows ON THAT PLANE — a mantle listing must not
// retire a classic template. A legacy user row carries neither plane nor
// region, so it names no endpoint at all and any concrete route of the same
// model supersedes it.
//
// Callers pass all halves of the catalog at once, because concrete rows
// normally arrive from discovery while the template sits among the builtins,
// and deciding per-half would retire nothing.
func pinnedRoutePlanes(lists ...[]ModelInfo) PinnedRoutes {
	p := PinnedRoutes{byPlane: map[routePlane]bool{}, byModel: map[string]bool{}}
	for _, rows := range lists {
		for _, m := range rows {
			if RouteIsPinned(m) {
				p.byPlane[routePlane{m.Provider, m.ID, m.Plane}] = true
				p.byModel[catalogKey(m.Provider, m.ID)] = true
			}
		}
	}
	return p
}

// supersedes reports whether concrete routes cover the template row m.
func (p PinnedRoutes) supersedes(m ModelInfo) bool {
	if RouteIsPinned(m) {
		return false
	}
	if m.Plane == "" {
		return p.byModel[catalogKey(m.Provider, m.ID)]
	}
	return p.byPlane[routePlane{m.Provider, m.ID, m.Plane}]
}

// dropSupersededUnpinned removes unpinned Bedrock rows whose (provider, id,
// plane) already has concrete per-region routes.
//
// Builtins are authored without a region on purpose, so the catalog works
// before any discovery walk has happened. But once discovery has produced real
// per-region routes, keeping the unpinned row would list the model twice, and
// the extra row is the strictly worse one: it is the only row that cannot say
// which endpoint it calls, which is the ambiguity routes exist to remove. So
// concrete routes win and the template retires.
//
// A template's Enabled state is PROPAGATED onto concrete siblings that have
// none before it retires, rather than the template being kept. "I disabled
// this model" is intent about the model, not about one endpoint, so it has to
// survive; but keeping the row to hold the flag would leave the model listed
// twice forever, which is what it was doing for every vendor-policy-disabled
// model (both rows disabled, so keeping one bought nothing and duplicated the
// SwiftUI row identity).
func retireSupersededTemplates(lists ...*[]ModelInfo) {
	all := make([][]ModelInfo, 0, len(lists))
	for _, l := range lists {
		all = append(all, *l)
	}
	pinned := pinnedRoutePlanes(all...)
	if len(pinned.byModel) == 0 {
		return
	}

	// Carry EVERYTHING model-level off the templates before they go.
	//
	// Enabled alone was not enough. A template is where model-level authored
	// input lands — `models add --price-in 42` writes a route-less row — so
	// dropping it without redistributing its facts silently discarded a price
	// override, notes, name, and context length the user had just set, with
	// `models add` still reporting success. The values remain on disk; they
	// simply stopped reaching the merged view once any probe created a
	// concrete sibling.
	var templates []ModelInfo
	intent := map[string]*bool{}
	for _, rows := range all {
		for _, m := range rows {
			if !pinned.supersedes(m) {
				continue
			}
			templates = append(templates, m)
			if m.Enabled != nil {
				intent[catalogKey(m.Provider, m.ID)] = m.Enabled
			}
		}
	}
	// A user PRICE OVERRIDE has to overwrite, not fill: the concrete sibling
	// usually carries a builtin or vendor price already, so fill-only-empty
	// would leave the override invisible. Same rule the catalog overlay
	// applies (hasUserPriceOverride).
	overrides := map[string]ModelInfo{}
	for _, t := range templates {
		if hasUserPriceOverride(t) {
			overrides[catalogKey(t.Provider, t.ID)] = t
		}
	}
	for _, l := range lists {
		// Fill-only-empty, so a concrete route's own authored value always
		// beats the template's.
		inheritModelFacts(*l, templates)
		for i := range *l {
			k := catalogKey((*l)[i].Provider, (*l)[i].ID)
			if (*l)[i].Enabled == nil {
				if e, ok := intent[k]; ok {
					(*l)[i].Enabled = e
				}
			}
			if t, ok := overrides[k]; ok {
				(*l)[i].PriceIn, (*l)[i].PriceOut, (*l)[i].PriceRequest = t.PriceIn, t.PriceOut, t.PriceRequest
				(*l)[i].PriceSource, (*l)[i].PriceOverride = t.PriceSource, t.PriceOverride
			}
		}
		*l = dropSupersededUnpinned(*l, pinned)
	}
}

func dropSupersededUnpinned(rows []ModelInfo, pinned PinnedRoutes) []ModelInfo {
	if len(pinned.byModel) == 0 {
		return rows
	}
	out := make([]ModelInfo, 0, len(rows))
	for _, m := range rows {
		if pinned.supersedes(m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// inheritModelFacts fills MODEL facts (the properties shared by every route of
// a given model) onto rows that lack them, sourcing from any row of the same
// (provider, id). Fill-only-empty: an authored value is never overwritten,
// the same discipline as AdoptRoutingHints.
//
// This is required once discovery emits one row per (plane, region): a
// discovered us.anthropic.claude-...@classic/us-west-2 row would otherwise
// render nameless and unpriced, because the builtin catalog declares those
// facts exactly once.
func inheritModelFacts(rows []ModelInfo, source []ModelInfo) {
	byModel := make(map[string]ModelInfo, len(source))
	for _, s := range source {
		k := catalogKey(s.Provider, s.ID)
		// Prefer the most complete donor: a named, priced row beats a bare one.
		if prev, ok := byModel[k]; ok && modelFactScore(prev) >= modelFactScore(s) {
			continue
		}
		byModel[k] = s
	}
	for i := range rows {
		src, ok := byModel[catalogKey(rows[i].Provider, rows[i].ID)]
		if !ok {
			continue
		}
		fillModelFacts(&rows[i], src)
	}
}

// modelFactScore counts how many model facts a row carries, so inheritance
// picks the richest donor rather than whichever sorted first.
func modelFactScore(m ModelInfo) int {
	n := 0
	for _, set := range []bool{
		m.Name != "", m.ContextLen != 0, m.PriceIn != 0, m.PriceOut != 0,
		m.Dimensions != 0, len(m.SupportedDimensions) > 0, len(m.Modalities) > 0,
		m.Notes != "", m.PriceSource != "",
	} {
		if set {
			n++
		}
	}
	return n
}

// fillModelFacts copies the shared-across-routes properties, never the
// per-route ones (Plane, Region, Endpoint, InvokeStrategy, Enabled, Tier, and
// every Test*/Benchmark field stay owned by the individual route row).
func fillModelFacts(dst *ModelInfo, src ModelInfo) {
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Type == "" {
		dst.Type = src.Type
	}
	if dst.ContextLen == 0 {
		dst.ContextLen = src.ContextLen
	}
	if dst.Dimensions == 0 {
		dst.Dimensions = src.Dimensions
	}
	if len(dst.SupportedDimensions) == 0 {
		dst.SupportedDimensions = src.SupportedDimensions
	}
	if len(dst.Modalities) == 0 {
		dst.Modalities = src.Modalities
	}
	if dst.RecommendedSimilarityThreshold == 0 {
		dst.RecommendedSimilarityThreshold = src.RecommendedSimilarityThreshold
	}
	if dst.PriceIn == 0 {
		dst.PriceIn = src.PriceIn
	}
	if dst.PriceOut == 0 {
		dst.PriceOut = src.PriceOut
	}
	if dst.PriceRequest == 0 {
		dst.PriceRequest = src.PriceRequest
	}
	if dst.PriceSource == "" {
		dst.PriceSource = src.PriceSource
	}
	if dst.Notes == "" {
		dst.Notes = src.Notes
	}
	if dst.ConfigHint == "" {
		dst.ConfigHint = src.ConfigHint
	}
	if !dst.Recommended && src.Recommended {
		dst.Recommended = src.Recommended
	}
}
