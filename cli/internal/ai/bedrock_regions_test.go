package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBedrockFileRegionsRoundTrip(t *testing.T) {
	setupBedrockHome(t)

	if err := WriteBedrockFile(BedrockFile{Region: "us-east-1", Regions: []string{"us-west-2", " us-east-2 ", ""}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadBedrockFile()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Regions) != 2 || got.Regions[0] != "us-west-2" || got.Regions[1] != "us-east-2" {
		t.Fatalf("regions should be trimmed with empties dropped: %+v", got.Regions)
	}

	// A token merge must not disturb the stored regions.
	tok := "ABSK-new-token-value"
	if err := UpdateBedrockFile("", &tok, false); err != nil {
		t.Fatal(err)
	}
	got, err = ReadBedrockFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Regions) != 2 {
		t.Fatalf("token update dropped regions: %+v", got.Regions)
	}
}

func TestUpdateBedrockRegions(t *testing.T) {
	setupBedrockHome(t)

	if err := UpdateBedrockRegions([]string{"us-west-2", "eu-central-1"}, false); err != nil {
		t.Fatalf("valid regions refused: %v", err)
	}
	got, err := ReadBedrockFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Regions) != 2 {
		t.Fatalf("regions = %+v", got.Regions)
	}

	// A hostile or mistyped label is refused before anything is written.
	if err := UpdateBedrockRegions([]string{"us-west-2", "evil.example.com/path"}, false); err == nil {
		t.Fatal("expected refusal of a non-bare region label")
	}
	got, _ = ReadBedrockFile()
	if len(got.Regions) != 2 {
		t.Fatalf("failed update must not modify the file: %+v", got.Regions)
	}

	if err := UpdateBedrockRegions(nil, true); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = ReadBedrockFile()
	if len(got.Regions) != 0 {
		t.Fatalf("clear left regions: %+v", got.Regions)
	}
}

func TestResolveBedrockRegions(t *testing.T) {
	setupBedrockHome(t)

	// No file at all: exactly the primary.
	regions := ResolveBedrockRegions(BedrockConfig{Region: "us-east-1"})
	if len(regions) != 1 || regions[0] != "us-east-1" {
		t.Fatalf("no-file regions = %v", regions)
	}

	// Primary first, file extras appended, primary deduped out of the extras.
	if err := WriteBedrockFile(BedrockFile{Region: "us-east-1", Regions: []string{"us-west-2", "us-east-1", "us-east-2"}}); err != nil {
		t.Fatal(err)
	}
	regions = ResolveBedrockRegions(BedrockConfig{Region: "eu-west-1"})
	want := []string{"us-east-1", "us-west-2", "us-east-2"}
	if len(regions) != len(want) {
		t.Fatalf("regions = %v, want %v", regions, want)
	}
	for i := range want {
		if regions[i] != want[i] {
			t.Fatalf("regions = %v, want %v (file region overlays the vault primary)", regions, want)
		}
	}
}

func TestResolveBedrockConfigRegionOverride(t *testing.T) {
	setupBedrockHome(t)
	if err := WriteBedrockFile(BedrockFile{Region: "us-east-1"}); err != nil {
		t.Fatal(err)
	}

	// The file region normally clobbers the caller's region…
	got := ResolveBedrockConfig(BedrockConfig{Region: "us-west-2"})
	if got.Region != "us-east-1" {
		t.Fatalf("file should win without an override: %q", got.Region)
	}
	// …which is exactly why RegionOverride must outrank it.
	got = ResolveBedrockConfig(BedrockConfig{Region: "us-west-2", RegionOverride: "us-east-2"})
	if got.Region != "us-east-2" {
		t.Fatalf("override should win over the file: %q", got.Region)
	}
}

func TestEffectiveBedrockRegionPrecedence(t *testing.T) {
	setupBedrockHome(t)

	// Override wins over everything, including a builtin catalog pin.
	if r := EffectiveBedrockRegion(BedrockConfig{Region: "us-east-1", RegionOverride: "eu-west-1"}, "openai.gpt-5.5", ""); r != "eu-west-1" {
		t.Fatalf("override should win: %q", r)
	}
	// Catalog pin wins over the configured region (openai.gpt-5.5 is builtin
	// region-pinned to us-east-2).
	if r := EffectiveBedrockRegion(BedrockConfig{Region: "us-east-1"}, "openai.gpt-5.5", ""); r != "us-east-2" {
		t.Fatalf("catalog pin should win: %q", r)
	}
	// No pin, no override: the configured region.
	if r := EffectiveBedrockRegion(BedrockConfig{Region: "ap-southeast-2"}, "us.anthropic.claude-haiku-4-5-20251001-v1:0", ""); r != "ap-southeast-2" {
		t.Fatalf("configured region should apply: %q", r)
	}
}

// TestBedrockConfigRegionOverrideNeverSerialized guards the in-memory-only
// contract: a RegionOverride must never leak into a persisted config.yaml or
// a JSON payload, where a stale value would silently reroute every call.
func TestBedrockConfigRegionOverrideNeverSerialized(t *testing.T) {
	cfg := BedrockConfig{Region: "us-east-1", RegionOverride: "us-west-2"}
	y, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(y)), "override") || strings.Contains(string(y), "us-west-2") {
		t.Fatalf("RegionOverride leaked into YAML: %s", y)
	}
	j, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(j)), "override") || strings.Contains(string(j), "us-west-2") {
		t.Fatalf("RegionOverride leaked into JSON: %s", j)
	}
}

func TestBedrockFileTokenUpdatedAt(t *testing.T) {
	setupBedrockHome(t)

	// A region-only write must not stamp a token change.
	if err := UpdateBedrockFile("us-east-1", nil, false); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadBedrockFile()
	if got.TokenUpdatedAt != "" {
		t.Fatalf("region-only write stamped token_updated_at: %q", got.TokenUpdatedAt)
	}

	tok := "ABSK-first-token-value"
	if err := UpdateBedrockFile("", &tok, false); err != nil {
		t.Fatal(err)
	}
	got, _ = ReadBedrockFile()
	first := got.TokenUpdatedAt
	if first == "" {
		t.Fatal("token write did not stamp token_updated_at")
	}

	// Another region-only write keeps the stamp.
	if err := UpdateBedrockFile("us-west-2", nil, false); err != nil {
		t.Fatal(err)
	}
	got, _ = ReadBedrockFile()
	if got.TokenUpdatedAt != first {
		t.Fatalf("region write changed the stamp: %q -> %q", first, got.TokenUpdatedAt)
	}
}

func TestBedrockTokenDivergence(t *testing.T) {
	env := func(v string) func(string) string {
		return func(string) string { return v }
	}
	file := func(v string) func() string {
		return func() string { return v }
	}

	longA := "ABSK-token-value-alpha-1234"
	longB := "ABSK-token-value-bravo-5678"

	// Env and file agree: no divergence.
	d := bedrockTokenDivergence(env(longA), file(longA), nil, false)
	if d.Diverges {
		t.Fatal("identical tokens must not diverge")
	}
	// Env set, file different: divergence with both suffixes.
	d = bedrockTokenDivergence(env(longA), file(longB), nil, false)
	if !d.Diverges || d.EnvSuffix != "1234" || d.StoredSuffix != "5678" {
		t.Fatalf("divergence = %+v", d)
	}
	// Env only: not divergence (nothing stored to be shadowed).
	d = bedrockTokenDivergence(env(longA), file(""), nil, false)
	if d.Diverges || !d.EnvSet || d.StoredSet {
		t.Fatalf("env-only = %+v", d)
	}
	// File only: not divergence.
	d = bedrockTokenDivergence(env(""), file(longB), nil, false)
	if d.Diverges || d.EnvSet || !d.StoredSet {
		t.Fatalf("file-only = %+v", d)
	}
	// Keychain acts as the stored fallback when the file is empty.
	kc := func(string) (string, error) { return longB, nil }
	d = bedrockTokenDivergence(env(longA), file(""), kc, false)
	if !d.Diverges || d.StoredSuffix != "5678" {
		t.Fatalf("keychain-stored = %+v", d)
	}
}

func TestRegionAttempts(t *testing.T) {
	cases := []struct {
		name    string
		regions []string
		pinned  string
		want    []string // nil = no fan-out (plain single probe)
	}{
		{"single region, no pin", []string{"us-east-1"}, "", nil},
		{"single region, pin equals it", []string{"us-east-1"}, "us-east-1", nil},
		{"single region, differing pin fans out primary-first", []string{"us-east-1"}, "us-west-2", []string{"us-east-1", "us-west-2"}},
		{"multi region, no pin", []string{"us-east-1", "us-west-2"}, "", []string{"us-east-1", "us-west-2"}},
		{"multi region, pin already included", []string{"us-east-1", "us-west-2"}, "us-west-2", []string{"us-east-1", "us-west-2"}},
		{"multi region, new pin appended last", []string{"us-east-1", "us-west-2"}, "eu-west-1", []string{"us-east-1", "us-west-2", "eu-west-1"}},
	}
	for _, tc := range cases {
		got := regionAttempts(tc.regions, tc.pinned)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

func TestResolveBedrockTokenPreferStored(t *testing.T) {
	setupBedrockHome(t)
	const envTok = "ABSK-environment-key-alpha"
	const fileTok = "ABSK-stored-file-key-bravo"

	// Default precedence: env wins.
	if err := WriteBedrockFile(BedrockFile{Token: fileTok}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(bedrockBearerTokenEnv, envTok)
	if tok, src := ResolveBedrockToken(); tok != envTok || src != BedrockTokenEnv {
		t.Fatalf("default: got %q from %s, want env", TokenSuffix(tok), src)
	}

	// prefer_stored_token inverts it: the file wins over a set env var.
	if err := UpdateBedrockPreferStored(true); err != nil {
		t.Fatal(err)
	}
	if tok, src := ResolveBedrockToken(); tok != fileTok || src != BedrockTokenFile {
		t.Fatalf("prefer: got %q from %s, want file", TokenSuffix(tok), src)
	}

	// The escape hatch restores env-first for a candidate-key probe.
	t.Setenv(bedrockIgnorePreferStoredEnv, "1")
	if tok, src := ResolveBedrockToken(); tok != envTok || src != BedrockTokenEnv {
		t.Fatalf("escape hatch: got %q from %s, want env", TokenSuffix(tok), src)
	}
	t.Setenv(bedrockIgnorePreferStoredEnv, "")

	// Prefer with NO stored key falls back to the env var (never bricks).
	empty := ""
	if err := UpdateBedrockFile("", &empty, false); err != nil {
		t.Fatal(err)
	}
	if tok, src := ResolveBedrockToken(); tok != envTok || src != BedrockTokenEnv {
		t.Fatalf("prefer without stored: got %q from %s, want env fallback", TokenSuffix(tok), src)
	}
}

func TestEnsureBedrockBearerTokenPreferOverwrites(t *testing.T) {
	env := map[string]string{bedrockBearerTokenEnv: "ABSK-old-environment-token"}
	getenv := func(k string) string { return env[k] }
	setenv := func(k, v string) error { env[k] = v; return nil }
	fileTok := func() string { return "ABSK-stored-file-key-bravo" }

	// Default: an already-set env var is left alone.
	ensureBedrockBearerTokenPrefer(getenv, setenv, fileTok, nil, false)
	if env[bedrockBearerTokenEnv] != "ABSK-old-environment-token" {
		t.Fatalf("default overwrote the env var: %q", env[bedrockBearerTokenEnv])
	}
	// Prefer: the stored key overwrites it in-process, so the classic SDK
	// path AND the mantle plane (which reads the env var after this shim)
	// both follow the saved key.
	ensureBedrockBearerTokenPrefer(getenv, setenv, fileTok, nil, true)
	if env[bedrockBearerTokenEnv] != "ABSK-stored-file-key-bravo" {
		t.Fatalf("prefer did not overwrite: %q", env[bedrockBearerTokenEnv])
	}
	// Prefer with no stored key: the env var stands.
	env[bedrockBearerTokenEnv] = "ABSK-old-environment-token"
	ensureBedrockBearerTokenPrefer(getenv, setenv, func() string { return "" }, nil, true)
	if env[bedrockBearerTokenEnv] != "ABSK-old-environment-token" {
		t.Fatalf("prefer without stored clobbered the env var: %q", env[bedrockBearerTokenEnv])
	}
}

func TestRegionRetryable(t *testing.T) {
	retry := []TestErrorCode{TestErrNotFound, TestErrInvalidRequest, TestErrAccessDenied}
	stop := []TestErrorCode{TestErrBadCredentials, TestErrThrottled, TestErrTimeout, TestErrProviderUnreachable, TestErrIncompatible, TestErrUnknown, ""}
	for _, c := range retry {
		if !regionRetryable(c) {
			t.Errorf("%q should be region-retryable", c)
		}
	}
	for _, c := range stop {
		if regionRetryable(c) {
			t.Errorf("%q must not be region-retryable", c)
		}
	}
}

func TestInvalidRequestRemediationMentionsRegion(t *testing.T) {
	msg := RemediationFor(TestErrInvalidRequest, "bedrock", "")
	for _, want := range []string{"region", "inference-profile"} {
		if !strings.Contains(msg, want) {
			t.Errorf("bedrock invalid_request remediation missing %q: %s", want, msg)
		}
	}
	if generic := RemediationFor(TestErrInvalidRequest, "openrouter", ""); strings.Contains(generic, "region") {
		t.Errorf("non-bedrock remediation should not mention region: %s", generic)
	}
}

// TestCrossPlanePinsNeverBleed pins two live-observed bugs from grok-4.6's
// dual-plane debut: a mantle user entry for the bare id must not leak its
// invoke strategy, region, or endpoint onto the CLASSIC us./global. profile
// forms via the profile-stripped base match — profiles exist only on the
// classic plane, and the leak dispatched Converse traffic to a mantle 404
// and pinned it to the mantle region. Endpoint was never observed bleeding
// (its one caller sits behind the strategy gate) but shares the matching
// rule, so it is pinned here alongside the other two.
func TestCrossPlanePinsNeverBleed(t *testing.T) {
	root := t.TempDir()
	if err := SaveUserCatalogEntry(ScopeVault, root, ModelInfo{
		ID: "xai.grok-4.6", Provider: "bedrock", Type: "generation",
		InvokeStrategy: StrategyBedrockMantleResponses,
		Region:         "us-west-2",
		Endpoint:       "https://bedrock-mantle.us-west-2.api.aws",
	}); err != nil {
		t.Fatal(err)
	}

	// Exact id: mantle strategy, region, and endpoint apply.
	if s := ResolveInvokeStrategy("bedrock", "xai.grok-4.6", root); s != StrategyBedrockMantleResponses {
		t.Errorf("exact mantle strategy lost: %q", s)
	}
	if r := ResolveModelRegion("bedrock", "xai.grok-4.6", root); r != "us-west-2" {
		t.Errorf("exact mantle region lost: %q", r)
	}
	if ep := ResolveModelEndpoint("bedrock", "xai.grok-4.6", root); ep != "https://bedrock-mantle.us-west-2.api.aws" {
		t.Errorf("exact mantle endpoint lost: %q", ep)
	}

	// Profile forms: NOTHING bleeds across the plane boundary.
	for _, id := range []string{"us.xai.grok-4.6", "global.xai.grok-4.6"} {
		if s := ResolveInvokeStrategy("bedrock", id, root); s == StrategyBedrockMantleResponses {
			t.Errorf("%s inherited the mantle strategy via base match", id)
		}
		if r := ResolveModelRegion("bedrock", id, root); r != "" {
			t.Errorf("%s inherited region %q via base match", id, r)
		}
		if ep := ResolveModelEndpoint("bedrock", id, root); ep != "" {
			t.Errorf("%s inherited endpoint %q via base match", id, ep)
		}
	}
}
