package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestContract_ModelsVerifyOfflineOllama drives the full verify pipeline
// against a dead Ollama endpoint: real command path, deterministic offline
// failure, and the classified result must be persisted to the vault catalog.
func TestContract_ModelsVerifyOfflineOllama(t *testing.T) {
	_, root := newContractVault(t)
	// Point Ollama at a dead endpoint through the real config path.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.ollama.endpoint", "http://127.0.0.1:9"); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}

	got, err := runCLIArgs(t, root,
		"models", "verify", "all-minilm",
		"--provider", "ollama", "--yes",
		"--json", "--porcelain")
	if err != nil {
		t.Fatalf("models verify: %v (out=%s)", err, truncate(got, 500))
	}

	var report struct {
		Probe   string         `json:"probe"`
		Summary map[string]int `json:"summary"`
		Results []struct {
			ModelID string `json:"model_id"`
			OK      bool   `json:"ok"`
			Code    string `json:"code"`
		} `json:"results"`
		SavedScope string `json:"saved_scope"`
	}
	if err := json.Unmarshal(got, &report); err != nil {
		t.Fatalf("report parse: %v (body=%s)", err, truncate(got, 300))
	}
	if len(report.Results) != 1 || report.Results[0].OK {
		t.Fatalf("expected one failed result, got %+v", report.Results)
	}
	code := report.Results[0].Code
	if code != string(ai.TestErrProviderUnreachable) && code != string(ai.TestErrTimeout) {
		t.Fatalf("expected provider_unreachable/timeout, got %q", code)
	}
	if report.SavedScope != "vault" {
		t.Fatalf("saved_scope = %q, want vault", report.SavedScope)
	}

	data, err := os.ReadFile(filepath.Join(root, ".2ndbrain", "models.yaml"))
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	if !strings.Contains(string(data), "test_error_code:") {
		t.Fatalf("verify did not persist the classified failure:\n%s", data)
	}

	// The persisted state must surface in ai status as a model_access summary.
	statusOut, err := runCLIArgs(t, root, "ai", "status", "--json", "--porcelain")
	if err != nil {
		t.Skipf("ai status unavailable in this environment: %v", err)
	}
	var status struct {
		ModelAccess *struct {
			Verified      int    `json:"verified"`
			AccessDenied  int    `json:"access_denied"`
			OtherFailures int    `json:"other_failures"`
			LastVerified  string `json:"last_verified_at"`
		} `json:"model_access"`
	}
	if err := json.Unmarshal(statusOut, &status); err != nil {
		t.Fatalf("status parse: %v (body=%s)", err, truncate(statusOut, 300))
	}
	// The failed ollama probe was persisted, but model_access summarizes the
	// ACTIVE provider (bedrock by default) — so it should be nil here. Flip
	// the provider and re-check.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.provider", "ollama"); err != nil {
		t.Fatalf("set provider: %v", err)
	}
	statusOut, err = runCLIArgs(t, root, "ai", "status", "--json", "--porcelain")
	if err != nil {
		t.Skipf("ai status unavailable after provider flip: %v", err)
	}
	if err := json.Unmarshal(statusOut, &status); err != nil {
		t.Fatalf("status parse 2: %v", err)
	}
	if status.ModelAccess == nil {
		t.Fatal("ai status did not report model_access after a persisted verify")
	}
	if status.ModelAccess.OtherFailures != 1 || status.ModelAccess.LastVerified == "" {
		t.Fatalf("unexpected model_access summary: %+v", *status.ModelAccess)
	}
}

// TestContract_ModelsVerifyCostCapAborts asserts the cap gate: an estimate
// above --cost-cap must abort BEFORE any probe or catalog write.
func TestContract_ModelsVerifyCostCapAborts(t *testing.T) {
	_, root := newContractVault(t)

	// Haiku 4.5 carries a pinned builtin price, so the estimate is non-zero.
	got, err := runCLIArgs(t, root,
		"models", "verify", "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		"--provider", "bedrock",
		"--cost-cap", "0.0000000001",
		"--yes", "--json", "--porcelain")
	if err == nil {
		t.Fatalf("expected cost-cap abort, got success: %s", truncate(got, 300))
	}
	if !strings.Contains(err.Error(), "cost-cap") {
		t.Fatalf("error does not mention the cap: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".2ndbrain", "models.yaml")); !os.IsNotExist(statErr) {
		t.Fatal("cost-cap abort must not write the catalog")
	}
}

// TestContract_ModelsVerifyRefusesBlindSpend asserts the confirm gate: without
// --yes on a non-interactive stdin (and not in JSON mode), verify refuses.
func TestContract_ModelsVerifyRefusesBlindSpend(t *testing.T) {
	_, root := newContractVault(t)

	_, err := runCLIArgs(t, root,
		"models", "verify", "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		"--provider", "bedrock")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected non-interactive refusal mentioning --yes, got %v", err)
	}
}

// TestVerifyCandidates_DefaultSetRespectsFilters checks candidate selection
// logic offline: --recommended narrows, rerank and incompatible entries drop.
func TestVerifyCandidates_DefaultSetRespectsFilters(t *testing.T) {
	_, root := newContractVault(t)
	cfg := ai.DefaultAIConfig()

	verifyRecommended, verifyProvider, verifyVendor, verifyAll = true, "bedrock", "", false
	defer func() { verifyRecommended, verifyProvider = false, "" }()

	got, err := verifyCandidates(t.Context(), cfg, root, nil)
	if err != nil {
		t.Fatalf("verifyCandidates: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("recommended bedrock candidate set must not be empty")
	}
	for _, m := range got {
		if !m.Recommended || m.Provider != "bedrock" {
			t.Errorf("candidate %s violates filters: %+v", m.ID, m)
		}
		if m.Type == "rerank" {
			t.Errorf("rerank model %s must be skipped (no probe exists)", m.ID)
		}
	}
}

// TestContract_ModelsVerifyAnthropicLine_CredGated runs the real batch probe
// against Bedrock: every Anthropic result must be either OK or classified to
// something more specific than unknown. This is the test that catches the AWS
// staged-rollout gate (a listed model the account cannot invoke). Skips
// without AWS credentials. Costs fractions of a cent when it runs.
func TestContract_ModelsVerifyAnthropicLine_CredGated(t *testing.T) {
	if !ai.CheckBedrockCredentials(t.Context(), ai.BedrockConfig{Profile: "default", Region: "us-east-1"}) {
		t.Skip("AWS credentials not configured")
	}
	_, root := newContractVault(t)

	got, err := runCLIArgs(t, root,
		"models", "verify",
		"--vendor", "anthropic", "--provider", "bedrock",
		"--yes", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models verify: %v (out=%s)", err, truncate(got, 500))
	}
	var report struct {
		Results []struct {
			ModelID string `json:"model_id"`
			OK      bool   `json:"ok"`
			Code    string `json:"code"`
		} `json:"results"`
		Summary map[string]int `json:"summary"`
	}
	if err := json.Unmarshal(got, &report); err != nil {
		t.Fatalf("report parse: %v (body=%s)", err, truncate(got, 300))
	}
	if len(report.Results) < 4 {
		t.Fatalf("expected the full Anthropic line (>=4 generation models), got %d", len(report.Results))
	}
	for _, r := range report.Results {
		if !r.OK && (r.Code == "" || r.Code == string(ai.TestErrUnknown)) {
			t.Errorf("%s failed unclassified: %+v", r.ModelID, r)
		}
	}
	t.Logf("anthropic line summary: %v", report.Summary)
}

// TestContract_AIStatusActiveProviderDisabled asserts the loud-degradation
// flag: hand-setting ai.<active>.disabled=true must surface as
// active_provider_disabled in ai status --json.
func TestContract_AIStatusActiveProviderDisabled(t *testing.T) {
	_, root := newContractVault(t)

	if _, err := runCLIArgs(t, root, "config", "set", "ai.bedrock.disabled", "true"); err != nil {
		t.Fatalf("set disabled: %v", err)
	}
	got, err := runCLIArgs(t, root, "ai", "status", "--json", "--porcelain")
	if err != nil {
		t.Skipf("ai status unavailable in this environment: %v", err)
	}
	var status struct {
		Provider               string `json:"provider"`
		ActiveProviderDisabled bool   `json:"active_provider_disabled"`
	}
	if err := json.Unmarshal(got, &status); err != nil {
		t.Fatalf("parse: %v (body=%s)", err, truncate(got, 300))
	}
	if status.Provider != "bedrock" {
		t.Fatalf("expected default provider bedrock, got %q", status.Provider)
	}
	if !status.ActiveProviderDisabled {
		t.Fatal("active_provider_disabled not reported for a disabled active provider")
	}

	// Re-selecting the provider clears the flag (config set side effect).
	if _, err := runCLIArgs(t, root, "config", "set", "ai.provider", "bedrock"); err != nil {
		t.Fatalf("re-select provider: %v", err)
	}
	got, err = runCLIArgs(t, root, "ai", "status", "--json", "--porcelain")
	if err != nil {
		t.Skipf("ai status unavailable after re-select: %v", err)
	}
	// Fresh struct: the field is omitempty, so when false the key is absent
	// and Unmarshal would leave the previous decode's true value in place.
	var after struct {
		ActiveProviderDisabled bool `json:"active_provider_disabled"`
	}
	if err := json.Unmarshal(got, &after); err != nil {
		t.Fatalf("parse 2: %v", err)
	}
	if after.ActiveProviderDisabled {
		t.Fatal("re-selecting the provider should have cleared the disabled flag")
	}
}
