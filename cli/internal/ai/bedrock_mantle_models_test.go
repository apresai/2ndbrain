package ai

// Offline tests exercise the pure listing helpers on a fixture captured from
// the REAL /v1/models endpoint (us-east-1, 2026-08-20) — per the no-mocks
// policy the endpoint itself is never faked. The two live tests are
// cred-gated on a resolvable Bedrock bearer token.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readMantleFixture loads the captured live listing (55 models as captured;
// the body contains no secrets — the bearer token travels only in the
// request header, which is not part of the response).
func readMantleFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "mantle_models_us-east-1.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func TestParseMantleModelList_Fixture(t *testing.T) {
	ids, err := parseMantleModelList(readMantleFixture(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ids) < 48 {
		t.Fatalf("fixture should yield 48+ ids, got %d", len(ids))
	}
	// Dated variant and its alias are DISTINCT ids (exact-id dedupe only);
	// deepseek.v3.2 is the live-probe target; claude-fable-5 pins that the
	// parser keeps vendors 2nb's catalog has never carried.
	for _, want := range []string{
		"deepseek.v3.2",
		"openai.gpt-5.5",
		"openai.gpt-5.5-2026-04-23",
		"anthropic.claude-fable-5",
	} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fixture ids missing %q", want)
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Error("parser must never emit an empty id")
		}
		if seen[id] {
			t.Errorf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestParseMantleModelList_EdgeCases(t *testing.T) {
	if _, err := parseMantleModelList([]byte("<html>gateway error</html>")); err == nil {
		t.Error("malformed body must error")
	}
	if ids, err := parseMantleModelList([]byte(`{"data":[],"object":"list"}`)); err != nil || len(ids) != 0 {
		t.Errorf("empty data should parse to zero ids, got %v / %v", ids, err)
	}
	if ids, err := parseMantleModelList([]byte(`{"object":"list"}`)); err != nil || len(ids) != 0 {
		t.Errorf("missing data key should parse to zero ids, got %v / %v", ids, err)
	}
	// Only `id` is documented reliable: entries without one are skipped,
	// unknown fields are ignored, duplicates collapse, order is preserved.
	body := `{"data":[
	  {"id":"deepseek.v3.2","status":"available","future_field":{"x":1}},
	  {"status":"available"},
	  {"id":"  "},
	  {"id":"deepseek.v3.2"},
	  {"id":"qwen.qwen3-max"}
	]}`
	ids, err := parseMantleModelList([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ids) != 2 || ids[0] != "deepseek.v3.2" || ids[1] != "qwen.qwen3-max" {
		t.Errorf("ids = %v, want [deepseek.v3.2 qwen.qwen3-max]", ids)
	}
}

// TestMantleModelInfoPins holds the discovery row's routing hints: without
// InvokeStrategy + Region on the candidate, a probe of a mantle-discovered
// model dispatches classic Converse against a plane that cannot see it.
func TestMantleModelInfoPins(t *testing.T) {
	m := mantleModelInfo("deepseek.v3.2", "us-east-1")
	if m.Provider != "bedrock" {
		t.Errorf("provider = %q", m.Provider)
	}
	if m.Type != "generation" {
		t.Errorf("type = %q; the mantle plane is generation-only in 2nb", m.Type)
	}
	if m.Tier != TierUnverified {
		t.Errorf("tier = %q; listing proves existence, not a harness or entitlement", m.Tier)
	}
	if m.InvokeStrategy != StrategyBedrockMantleResponses {
		t.Errorf("invoke strategy = %q, want %q", m.InvokeStrategy, StrategyBedrockMantleResponses)
	}
	if m.Region != "us-east-1" {
		t.Errorf("region = %q, want the listing region", m.Region)
	}
	if m.Notes == "" {
		t.Error("notes should explain the listing-vs-entitlement distinction")
	}
}

func TestMantleDiscoveryRegionsOrdering(t *testing.T) {
	setupHome(t)
	for _, tt := range []struct {
		name    string
		primary string
		want    []string
	}{
		{"primary is first documented region", "us-east-1", []string{"us-east-1", "us-east-2", "us-west-2"}},
		{"primary reorders to front", "us-west-2", []string{"us-west-2", "us-east-1", "us-east-2"}},
		{"non-mantle primary keeps documented order", "eu-west-1", []string{"us-east-1", "us-east-2", "us-west-2"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := mantleDiscoveryRegions(BedrockConfig{Region: tt.primary})
			if len(got) != len(tt.want) {
				t.Fatalf("regions = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("regions = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestMantleBaseURL_RegionOverridePin pins the routing carrier the hinted
// probe rides: TestProbeModelInfo copies a discovery row's Region into
// cfg.Bedrock.RegionOverride, ResolveBedrockConfig maps the override onto
// Region, and mantleBaseURL's config-region fallback derives the host from
// it — so the probe reaches the listing region with no mantle-client change.
// A refactor that stops mapping RegionOverride before the fallback breaks
// every hinted probe silently; this test makes it loud.
func TestMantleBaseURL_RegionOverridePin(t *testing.T) {
	setupHome(t)
	cfg := ResolveBedrockConfig(BedrockConfig{Region: "us-east-1", RegionOverride: "us-west-2"})
	got, err := mantleBaseURL(cfg, "deepseek.v3.2", "")
	if err != nil {
		t.Fatalf("mantleBaseURL: %v", err)
	}
	if got != "https://bedrock-mantle.us-west-2.api.aws" {
		t.Errorf("base URL = %q, want the override region's host", got)
	}
}

// TestVendorDisplayCoversMantleListing walks the captured listing's vendor
// prefixes through the curated display map, so a future fixture re-capture
// that introduces a new mantle vendor fails here instead of silently
// Title-casing in the GUI (and drifting from VendorSelection.bedrockLabels
// in SimpleModelsView.swift, which mirrors bedrockVendorDisplay).
func TestVendorDisplayCoversMantleListing(t *testing.T) {
	ids, err := parseMantleModelList(readMantleFixture(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, id := range ids {
		vendor := strings.SplitN(id, ".", 2)[0]
		if _, ok := bedrockVendorDisplay[vendor]; !ok {
			t.Errorf("vendor %q (from %s) missing from bedrockVendorDisplay — add it there AND to VendorSelection.bedrockLabels", vendor, id)
		}
	}
}

func TestDedupeDiscoveredBedrock(t *testing.T) {
	classic := ModelInfo{ID: "openai.gpt-oss-120b", Provider: "bedrock", Type: "generation"}
	mantle := mantleModelInfo("openai.gpt-oss-120b", "us-east-1")
	other := ModelInfo{ID: "deepseek.v3.2", Provider: "bedrock", Type: "generation", InvokeStrategy: StrategyBedrockMantleResponses}
	openrouter := ModelInfo{ID: "openai.gpt-oss-120b", Provider: "openrouter", Type: "generation"}

	assertClassicWins := func(t *testing.T, out []ModelInfo) {
		t.Helper()
		if len(out) != 3 {
			t.Fatalf("got %d rows, want 3: %+v", len(out), out)
		}
		found := 0
		for _, m := range out {
			if m.Provider == "bedrock" && m.ID == "openai.gpt-oss-120b" {
				found++
				if m.InvokeStrategy != "" {
					t.Errorf("cross-plane collision kept strategy %q, want classic (empty)", m.InvokeStrategy)
				}
			}
		}
		if found != 1 {
			t.Errorf("bedrock rows for the colliding id = %d, want exactly 1", found)
		}
	}

	// The goroutines append in nondeterministic order: classic-wins must
	// hold in BOTH arrival orders.
	t.Run("classic first", func(t *testing.T) {
		assertClassicWins(t, dedupeDiscoveredBedrock([]ModelInfo{classic, other, mantle, openrouter}))
	})
	t.Run("mantle first", func(t *testing.T) {
		assertClassicWins(t, dedupeDiscoveredBedrock([]ModelInfo{mantle, other, openrouter, classic}))
	})

	// Distinct ids and non-bedrock providers pass through untouched.
	out := dedupeDiscoveredBedrock([]ModelInfo{other, openrouter})
	if len(out) != 2 {
		t.Errorf("distinct rows must survive, got %+v", out)
	}
}

func TestEffectiveInvokeStrategyPrecedence(t *testing.T) {
	setupHome(t)

	// Hint fills in when no catalog declares anything.
	hinted := mantleModelInfo("deepseek.v3.2", "us-east-1")
	if got := effectiveInvokeStrategy("bedrock", hinted, ""); got != StrategyBedrockMantleResponses {
		t.Errorf("hint should fill empty catalog resolution, got %q", got)
	}

	// Catalog beats hint: a user entry declaring a different strategy wins
	// over whatever the candidate carries.
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: "acme.cataloged", Provider: "bedrock", Type: "generation",
		InvokeStrategy: StrategyBedrockConverse,
	}); err != nil {
		t.Fatal(err)
	}
	candidate := ModelInfo{ID: "acme.cataloged", Provider: "bedrock", Type: "generation", InvokeStrategy: StrategyBedrockMantleResponses}
	if got := effectiveInvokeStrategy("bedrock", candidate, ""); got != StrategyBedrockConverse {
		t.Errorf("catalog must beat the hint, got %q", got)
	}

	// A builtin mantle entry also wins over a (nonsense) classic hint.
	builtin := ModelInfo{ID: "openai.gpt-5.5", Provider: "bedrock", Type: "generation", InvokeStrategy: StrategyBedrockConverse}
	if got := effectiveInvokeStrategy("bedrock", builtin, ""); got != StrategyBedrockMantleResponses {
		t.Errorf("builtin catalog must beat the hint, got %q", got)
	}

	// A profile-prefixed id never takes a mantle hint: cross-region profiles
	// exist only on the classic plane.
	profile := ModelInfo{ID: "us.acme.model-1", Provider: "bedrock", Type: "generation", InvokeStrategy: StrategyBedrockMantleResponses}
	if got := effectiveInvokeStrategy("bedrock", profile, ""); got != "" {
		t.Errorf("profile-prefixed id must not inherit the mantle hint, got %q", got)
	}

	// Dual-plane case: the classic us.xai.grok-4.6 builtin declares Converse,
	// and the bare mantle-listed xai.grok-4.6 base-matches it — but the
	// /v1/models listing is authoritative that the EXACT bare id lives on the
	// mantle plane, so the hint must beat that base-match inference (Converse
	// on the bare id would 404 and persist a spurious FAIL).
	dualPlane := mantleModelInfo("xai.grok-4.6", "us-west-2")
	if got := effectiveInvokeStrategy("bedrock", dualPlane, ""); got != StrategyBedrockMantleResponses {
		t.Errorf("mantle hint must beat the classic builtin's base-match inference, got %q", got)
	}
	// Without a hint, the same bare id keeps the base-match inheritance
	// (hint-less probes are exactly the pre-hint behavior).
	bare := ModelInfo{ID: "xai.grok-4.6", Provider: "bedrock", Type: "generation"}
	if got := effectiveInvokeStrategy("bedrock", bare, ""); got != StrategyBedrockConverse {
		t.Errorf("hint-less bare id should keep base-match inheritance, got %q", got)
	}

	// A non-mantle hint on a profile id passes through (the guard is
	// mantle-specific, mirroring ResolveInvokeStrategy's base-match rule).
	profileClassic := ModelInfo{ID: "us.acme.model-1", Provider: "bedrock", Type: "generation", InvokeStrategy: StrategyBedrockConverse}
	if got := effectiveInvokeStrategy("bedrock", profileClassic, ""); got != StrategyBedrockConverse {
		t.Errorf("non-mantle hint should pass through, got %q", got)
	}
}

// TestLiveMantleModelList_CredGated hits the real us-east-1 /v1/models
// endpoint: the listing is free (no tokens billed), so this proves the path,
// the auth header, and the parser against today's live payload.
func TestLiveMantleModelList_CredGated(t *testing.T) {
	if resolveMantleBearerToken() == "" {
		t.Skipf("set %s (or store a key) to run the live mantle listing", bedrockBearerTokenEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	models, err := ListBedrockMantleModels(ctx, BedrockConfig{}, "us-east-1")
	if err != nil {
		t.Fatalf("live mantle listing failed: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("live listing returned zero models")
	}
	for _, m := range models {
		if !strings.Contains(m.ID, ".") {
			t.Errorf("id %q does not parse as vendor.model", m.ID)
		}
		if m.InvokeStrategy != StrategyBedrockMantleResponses || m.Region != "us-east-1" {
			t.Errorf("row %q missing routing hints: strategy=%q region=%q", m.ID, m.InvokeStrategy, m.Region)
		}
	}
	t.Logf("live us-east-1 mantle listing: %d models", len(models))
}

// TestLiveMantleDiscoveredProbe_CredGated probes discovered-only mantle
// models through the HINTED path: HOME is isolated so no user catalog knows
// the ids, exactly the state of a freshly discovered mantle model.
//
// Two targets, because the live plane split them (2026-08-20):
//
//   - deepseek.v3.2 routes correctly via the hint but REJECTS the Responses
//     dialect (400 "does not support the '/openai/v1/responses' API"; it
//     answers only on the plane's unprefixed /v1/chat/completions route,
//     which 2nb has no client for yet). That classified invalid_request IS
//     the honest recorded outcome for such models; the error text itself
//     proves the request reached the mantle plane (a classic mis-route
//     yields a Converse/SDK error, a wrong-region mantle route a 404).
//   - xai.grok-4.6 (bare mantle id, us-west-2) DOES speak Responses and is
//     in no catalog (the builtin is the classic us.xai.grok-4.6 profile), so
//     it proves the full hinted PASS path end to end — including the
//     dual-plane precedence, since the hint must beat the classic builtin's
//     base-match inference to dispatch mantle at all.
//
// Non-defect account states skip: not entitled, throttled.
func TestLiveMantleDiscoveredProbe_CredGated(t *testing.T) {
	setupHome(t) // isolate catalogs; the bearer token must come from the env
	if os.Getenv(bedrockBearerTokenEnv) == "" {
		t.Skipf("set %s to run the live hinted mantle probe", bedrockBearerTokenEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	m := mantleModelInfo("deepseek.v3.2", "us-east-1")
	result, err := TestProbeModelInfo(ctx, AIConfig{}, m, "")
	if err != nil {
		t.Fatalf("TestProbeModelInfo returned hard error: %v", err)
	}
	if result.Strategy != StrategyBedrockMantleResponses {
		t.Errorf("deepseek result strategy = %q, want the hinted mantle strategy", result.Strategy)
	}
	if result.Region != "us-east-1" {
		t.Errorf("deepseek result region = %q, want the listing region", result.Region)
	}
	switch {
	case result.OK:
		// AWS added Responses support for it — even better.
		t.Logf("hinted deepseek.v3.2 probe passed in %s: %s", result.Latency, result.Detail)
	case result.Code == TestErrAccessDenied || result.Code == TestErrThrottled:
		t.Skipf("account state prevents the deepseek assertion (%s): %s", result.Code, result.Detail)
	case result.Code == TestErrInvalidRequest && strings.Contains(result.Detail, "does not support the '/openai/v1/responses' API"):
		t.Logf("deepseek.v3.2 routed to the mantle plane via the hint but rejects the Responses dialect (recorded honestly as %s): %s", result.Code, result.Detail)
	default:
		t.Fatalf("hinted deepseek probe failed unexpectedly [%s]: %s", result.Code, result.Detail)
	}

	g := mantleModelInfo("xai.grok-4.6", "us-west-2")
	gres, err := TestProbeModelInfo(ctx, AIConfig{}, g, "")
	if err != nil {
		t.Fatalf("TestProbeModelInfo returned hard error: %v", err)
	}
	if gres.Strategy != StrategyBedrockMantleResponses {
		t.Errorf("grok result strategy = %q, want the hinted mantle strategy (dual-plane precedence)", gres.Strategy)
	}
	if gres.Region != "us-west-2" {
		t.Errorf("grok result region = %q, want the listing region", gres.Region)
	}
	if !gres.OK {
		switch gres.Code {
		case TestErrAccessDenied, TestErrThrottled:
			t.Skipf("account state prevents the grok assertion (%s): %s", gres.Code, gres.Detail)
		}
		t.Fatalf("hinted grok-4.6 probe failed [%s]: %s", gres.Code, gres.Detail)
	}
	t.Logf("hinted xai.grok-4.6 probe passed in %s: %s", gres.Latency, gres.Detail)
}
