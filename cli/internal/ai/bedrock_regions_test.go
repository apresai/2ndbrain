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
	d := bedrockTokenDivergence(env(longA), file(longA), nil)
	if d.Diverges {
		t.Fatal("identical tokens must not diverge")
	}
	// Env set, file different: divergence with both suffixes.
	d = bedrockTokenDivergence(env(longA), file(longB), nil)
	if !d.Diverges || d.EnvSuffix != "1234" || d.StoredSuffix != "5678" {
		t.Fatalf("divergence = %+v", d)
	}
	// Env only: not divergence (nothing stored to be shadowed).
	d = bedrockTokenDivergence(env(longA), file(""), nil)
	if d.Diverges || !d.EnvSet || d.StoredSet {
		t.Fatalf("env-only = %+v", d)
	}
	// File only: not divergence.
	d = bedrockTokenDivergence(env(""), file(longB), nil)
	if d.Diverges || d.EnvSet || !d.StoredSet {
		t.Fatalf("file-only = %+v", d)
	}
	// Keychain acts as the stored fallback when the file is empty.
	kc := func(string) (string, error) { return longB, nil }
	d = bedrockTokenDivergence(env(longA), file(""), kc)
	if !d.Diverges || d.StoredSuffix != "5678" {
		t.Fatalf("keychain-stored = %+v", d)
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
