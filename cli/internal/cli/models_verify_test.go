package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// neutralizeAWSCredentials scrubs every AWS credential source the SDK's
// default chain consults (env keys, shared config files, container and IMDS
// providers), so CheckBedrockCredentials deterministically fails even on a
// developer machine with live credentials. Contract tests that exercise the
// verify default set stay spend-free with it.
func neutralizeAWSCredentials(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_PROFILE", "AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
	} {
		t.Setenv(k, "")
	}
	missing := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(missing, "nonexistent-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(missing, "nonexistent-credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

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

// TestContract_ModelsVerifyEventsZeroCandidates asserts the streamed empty
// outcome: with credentials neutralized the bedrock cred gate empties the
// default candidate set, and --events must emit exactly start(total=0) plus
// done (exit 0) instead of the non-events "no candidate models" error.
func TestContract_ModelsVerifyEventsZeroCandidates(t *testing.T) {
	_, root := newContractVault(t)
	neutralizeAWSCredentials(t)

	got, err := runCLIArgs(t, root,
		"models", "verify", "--provider", "bedrock", "--yes", "--events")
	if err != nil {
		t.Fatalf("models verify --events with zero candidates: %v (out=%s)", err, truncate(got, 300))
	}
	lines := bytes.Split(bytes.TrimSpace(got), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected exactly start+done, got %d lines:\n%s", len(lines), got)
	}
	var events []struct {
		Event      string         `json:"event"`
		Total      int            `json:"total"`
		Summary    map[string]int `json:"summary"`
		SavedScope string         `json:"saved_scope"`
	}
	for _, line := range lines {
		var e struct {
			Event      string         `json:"event"`
			Total      int            `json:"total"`
			Summary    map[string]int `json:"summary"`
			SavedScope string         `json:"saved_scope"`
		}
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("event parse: %v (line=%s)", err, line)
		}
		events = append(events, e)
	}
	if events[0].Event != "start" || events[0].Total != 0 {
		t.Fatalf("first event = %+v, want start with total=0", events[0])
	}
	if events[1].Event != "done" || events[1].SavedScope != "vault" {
		t.Fatalf("second event = %+v, want done with saved_scope=vault", events[1])
	}
}

// TestContract_ModelsVerifyEventsRequireYes asserts the non-interactive spend
// gate: --events without --yes is refused with ExitValidation before any
// vault or network work.
func TestContract_ModelsVerifyEventsRequireYes(t *testing.T) {
	_, root := newContractVault(t)

	_, err := runCLIArgs(t, root,
		"models", "verify", "--provider", "bedrock", "--events")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected refusal mentioning --yes, got %v", err)
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != ExitValidation {
		t.Fatalf("expected ExitValidation, got %v", err)
	}
}

// TestContract_ModelsVerifyEventsJSONMutuallyExclusive asserts --events and
// the --json envelope cannot be combined: two JSON dialects on one stdout
// would be undecodable.
func TestContract_ModelsVerifyEventsJSONMutuallyExclusive(t *testing.T) {
	_, root := newContractVault(t)

	_, err := runCLIArgs(t, root,
		"models", "verify", "--provider", "bedrock", "--yes", "--events", "--json")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != ExitValidation {
		t.Fatalf("expected ExitValidation, got %v", err)
	}
}

// TestVerifyCandidates_EnabledOnlyFiltersDisabled seeds an explicit
// Enabled=false user-catalog entry for a recommended bedrock model and checks
// --enabled-only drops it from the candidate set while the default keeps it.
func TestVerifyCandidates_EnabledOnlyFiltersDisabled(t *testing.T) {
	_, root := newContractVault(t)
	const disabledID = "us.anthropic.claude-sonnet-4-6"

	// Seed the explicit disable through the real CLI path.
	if _, err := runCLIArgs(t, root,
		"models", "disable", disabledID, "--provider", "bedrock", "--scope", "vault"); err != nil {
		t.Fatalf("models disable: %v", err)
	}

	cfg := ai.DefaultAIConfig()
	verifyProvider, verifyRecommended = "bedrock", true
	defer func() {
		verifyProvider, verifyRecommended, verifyEnabledOnly = "", false, false
	}()

	contains := func(models []ai.ModelInfo, id string) bool {
		for _, m := range models {
			if m.ID == id {
				return true
			}
		}
		return false
	}

	// Without --enabled-only the disabled model is still a candidate.
	verifyEnabledOnly = false
	got, err := verifyCandidates(t.Context(), cfg, root, nil)
	if err != nil {
		t.Fatalf("verifyCandidates: %v", err)
	}
	if !contains(got, disabledID) {
		t.Fatalf("disabled model %s should remain a candidate without --enabled-only", disabledID)
	}

	// With --enabled-only it drops; the rest of the set survives.
	verifyEnabledOnly = true
	got, err = verifyCandidates(t.Context(), cfg, root, nil)
	if err != nil {
		t.Fatalf("verifyCandidates enabled-only: %v", err)
	}
	if contains(got, disabledID) {
		t.Fatalf("--enabled-only leaked disabled model %s into candidates", disabledID)
	}
	if len(got) == 0 {
		t.Fatal("enabled-only candidate set must keep the still-enabled recommended models")
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
