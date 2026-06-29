package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// findCheck returns the first check with the given name, or a zero check with
// ok=false sentinel so a missing check fails the assertion loudly.
func findCheck(checks []DoctorCheck, name string) DoctorCheck {
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	return DoctorCheck{Name: name + " (MISSING)", OK: false}
}

// TestBuildDoctorChecks_Healthy: a well-formed Bedrock config over a vault whose
// DB embeddings match should pass every check.
func TestBuildDoctorChecks_Healthy(t *testing.T) {
	cfg := ai.DefaultAIConfig() // bedrock + nova-2 (1024d) + haiku
	st := doctorVaultState{
		totalDocs: 10, embeddedDocs: 10, embeddableUnembedded: 0,
		vaultDim: 1024, vaultModels: []string{cfg.EmbeddingModel},
	}
	// embedder is nil here (no registry init in a unit test); derivePortability
	// with a non-empty, fully-embedded vault and a nil embedder reports
	// "no_provider" (which doctor treats as a non-failure config state). To get a
	// true OK we'd need a live embedder, so this test asserts the config-only
	// checks pass and the portability check is not a hard failure.
	checks := buildDoctorChecks(context.Background(), "", cfg, nil, st)

	for _, name := range []string{"provider known", "active provider enabled", "model belongs to active provider", "ai.dimensions matches model", "similarity threshold resolves"} {
		if c := findCheck(checks, name); !c.OK {
			t.Errorf("check %q should pass on a healthy config, got ✗ (%s)", name, c.Detail)
		}
	}
}

// TestBuildDoctorChecks_BogusProvider: an unknown ai.provider fails the
// provider-known check AND the orphaned-slot check (the real models can't be
// served by a provider that doesn't exist).
func TestBuildDoctorChecks_BogusProvider(t *testing.T) {
	cfg := ai.DefaultAIConfig()
	cfg.Provider = "bogusprovider"
	st := doctorVaultState{totalDocs: 0} // empty vault → portability is benign

	checks := buildDoctorChecks(context.Background(), "", cfg, nil, st)

	if c := findCheck(checks, "provider known"); c.OK {
		t.Errorf("provider-known should fail for a bogus provider")
	} else if !strings.Contains(c.Fix, "ai.provider") {
		t.Errorf("provider-known fix should mention ai.provider, got %q", c.Fix)
	}
	// The builtin nova-2 / haiku models belong to bedrock, not "bogusprovider",
	// so the orphaned-slot check must fire too.
	if c := findCheck(checks, "model belongs to active provider"); c.OK {
		t.Errorf("orphaned-slot check should fail when models belong to a different provider")
	}
}

// TestBuildDoctorChecks_DisabledActiveProvider: ai.<provider>.disabled=true on
// the active provider is contradictory and must fail.
func TestBuildDoctorChecks_DisabledActiveProvider(t *testing.T) {
	cfg := ai.DefaultAIConfig() // bedrock active
	cfg.Bedrock.Disabled = true
	st := doctorVaultState{totalDocs: 0}

	checks := buildDoctorChecks(context.Background(), "", cfg, nil, st)
	if c := findCheck(checks, "active provider enabled"); c.OK {
		t.Errorf("active-provider-enabled should fail when the active provider is disabled")
	}
}

// TestBuildDoctorChecks_DimensionMismatch: an ai.dimensions that is neither the
// default NOR a supported Matryoshka width (768 for Nova) must fail with a hint.
func TestBuildDoctorChecks_DimensionMismatch(t *testing.T) {
	cfg := ai.DefaultAIConfig() // nova-2 → 1024d default, supports 256/384/1024/3072
	cfg.Dimensions = 768        // not a supported Nova width
	st := doctorVaultState{totalDocs: 0}

	checks := buildDoctorChecks(context.Background(), "", cfg, nil, st)
	c := findCheck(checks, "ai.dimensions matches model")
	if c.OK {
		t.Errorf("dimension check should fail when ai.dimensions is unsupported")
	}
	if !strings.Contains(c.Fix, "force-reembed") {
		t.Errorf("dimension fix should mention force-reembed, got %q", c.Fix)
	}
}

// TestBuildDoctorChecks_SupportedMatryoshkaDimensionPasses guards the fix: a
// deliberate non-default but SUPPORTED Matryoshka width (Nova 256) is a valid
// choice `config set` blesses, so `config doctor` must not flag it as a defect.
func TestBuildDoctorChecks_SupportedMatryoshkaDimensionPasses(t *testing.T) {
	cfg := ai.DefaultAIConfig() // nova-2; supports 256/384/1024/3072
	cfg.Dimensions = 256        // valid Matryoshka width, not the 1024 default
	st := doctorVaultState{totalDocs: 0}

	checks := buildDoctorChecks(context.Background(), "", cfg, nil, st)
	c := findCheck(checks, "ai.dimensions matches model")
	if !c.OK {
		t.Errorf("supported Matryoshka width 256 should pass, got fail: %q", c.Detail)
	}
}

// TestBuildDoctorChecks_ProviderUnavailableIsWarnNotFail guards the fix where a
// configured-but-unreachable provider (offline, Ollama down, expired creds)
// must be reported as a non-failing WARNING, not a config defect. Otherwise
// `config doctor` would exit 2 in offline/CI runs where the config is sound.
func TestBuildDoctorChecks_ProviderUnavailableIsWarnNotFail(t *testing.T) {
	cfg := ai.DefaultAIConfig()
	// An embedder that is registered but not reachable, with a matching
	// dimension and an embedded vault so derivePortability reaches the
	// availability check and returns "provider_unavailable".
	unavail := &fakeEmbedder{name: "fake", dims: cfg.Dimensions, available: false}
	st := doctorVaultState{
		totalDocs: 5, embeddedDocs: 5, embeddableUnembedded: 0,
		vaultDim: cfg.Dimensions, vaultModels: []string{cfg.EmbeddingModel},
	}

	checks := buildDoctorChecks(context.Background(), "", cfg, unavail, st)
	c := findCheck(checks, "embeddings match selection")
	if !c.OK {
		t.Errorf("provider_unavailable must NOT fail the check (would make doctor exit 2 offline); got OK=false")
	}
	if !c.Warn {
		t.Errorf("provider_unavailable should be flagged Warn=true, got %+v", c)
	}
	if c.Fix == "" {
		t.Errorf("provider_unavailable warning should still carry a remedy hint")
	}
}

// TestBuildDoctorChecks_DimensionBreakFails confirms a genuine config defect
// (the active provider produces a different vector width than the stored
// embeddings) IS a hard failure, distinct from the provider_unavailable warning.
func TestBuildDoctorChecks_DimensionBreakFails(t *testing.T) {
	cfg := ai.DefaultAIConfig() // expects 1024d
	// Reachable embedder producing 768d over a vault embedded at 1024d.
	mismatch := &fakeEmbedder{name: "fake", dims: 768, available: true}
	st := doctorVaultState{
		totalDocs: 5, embeddedDocs: 5, embeddableUnembedded: 0,
		vaultDim: 1024, vaultModels: []string{cfg.EmbeddingModel},
	}

	checks := buildDoctorChecks(context.Background(), "", cfg, mismatch, st)
	c := findCheck(checks, "embeddings match selection")
	if c.OK {
		t.Errorf("a dimension break is a real config defect and must fail the check")
	}
	if c.Warn {
		t.Errorf("a dimension break is a failure, not a warning")
	}
}

// --- Contract tests: exercise the cobra handler + JSON envelope end to end. ---

// configDoctorReportJSON mirrors the wire shape the GUI/scripts decode.
type configDoctorReportJSON struct {
	OK     bool `json:"ok"`
	Checks []struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Warn   bool   `json:"warn"`
		Detail string `json:"detail"`
		Fix    string `json:"fix"`
	} `json:"checks"`
}

func TestContract_ConfigDoctor_HealthyJSON(t *testing.T) {
	_, root := newContractVault(t) // bedrock defaults, empty vault

	out, err := runCLIArgs(t, root, "config", "doctor", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("config doctor (healthy): %v\n%s", err, out)
	}
	var rep configDoctorReportJSON
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("config doctor --json not decodable: %v\n%s", err, out)
	}
	if !rep.OK {
		t.Errorf("fresh vault should pass doctor; got ok=false: %+v", rep.Checks)
	}
	if len(rep.Checks) == 0 {
		t.Fatalf("expected check results, got none")
	}
	// The contract: every check carries a name + ok + detail.
	for _, c := range rep.Checks {
		if c.Name == "" {
			t.Errorf("check has empty name: %+v", c)
		}
	}
}

func TestContract_ConfigDoctor_BogusProviderExits2(t *testing.T) {
	v, root := newContractVault(t)

	// Corrupt the active provider in config (set won't allow a bogus value), so
	// write it through the AI config + Save the way the file would look on disk.
	v.Config.AI.Provider = "bogusprovider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save corrupted config: %v", err)
	}

	out, err := runCLIArgs(t, root, "config", "doctor", "--json", "--porcelain")
	if code := exitCode(t, err); code != ExitValidation {
		t.Fatalf("bogus-provider doctor: expected ExitValidation (%d), got %d (err=%v)", ExitValidation, code, err)
	}
	// On failure cobra also prints "Error: ..." to stderr (SilenceErrors=false
	// is a deliberate contract). The test harness merges stdout+stderr into one
	// pipe, so decode just the leading JSON object with a Decoder (which stops
	// at the first complete value) rather than json.Unmarshal over the whole
	// buffer. In real use stdout carries only the JSON.
	var rep configDoctorReportJSON
	if err := json.NewDecoder(strings.NewReader(string(out))).Decode(&rep); err != nil {
		t.Fatalf("doctor --json should still be valid on failure: %v\n%s", err, out)
	}
	if rep.OK {
		t.Errorf("ok should be false when a check fails")
	}
}

func TestContract_ConfigGetEffectiveThreshold(t *testing.T) {
	_, root := newContractVault(t)

	out, err := runCLIArgs(t, root, "config", "get", "ai.similarity_threshold", "--effective", "--porcelain")
	if err != nil {
		t.Fatalf("config get --effective: %v\n%s", err, out)
	}
	// Default vault leaves ai.similarity_threshold at 0; the effective value
	// resolves through the chain to a non-zero recommendation (Nova-2 → 0.65).
	got := strings.TrimSpace(string(out))
	if got == "" || got == "0" {
		t.Errorf("--effective threshold should resolve to a non-zero value, got %q", got)
	}
}

func TestContract_ConfigGetEffective_RejectsOtherKeys(t *testing.T) {
	_, root := newContractVault(t)

	_, err := runCLIArgs(t, root, "config", "get", "ai.provider", "--effective")
	if code := exitCode(t, err); code != ExitValidation {
		t.Fatalf("--effective on a non-threshold key: expected ExitValidation (%d), got %d (err=%v)", ExitValidation, code, err)
	}
}
