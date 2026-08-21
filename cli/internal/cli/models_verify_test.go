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
		"AWS_BEARER_TOKEN_BEDROCK",
	} {
		t.Setenv(k, "")
	}
	missing := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(missing, "nonexistent-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(missing, "nonexistent-credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	// Bearer token now also lives in ~/.config/2nb/bedrock.json and the login
	// Keychain. Wipe the sandboxed file and skip the real Keychain so this
	// helper still means "no Bedrock creds".
	t.Setenv("2NB_BEDROCK_SKIP_KEYCHAIN", "1")
	_ = os.Remove(ai.BedrockFilePath())
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

// TestVerifyCandidatePool_DiscoverAddsUnverified pins the candidate-pool
// merge: verify used to walk only merged.Verified, so a model the user
// enabled through a vendor policy — which exists nowhere but the discovered
// half until something probes it — could never be validated.
func TestVerifyCandidatePool_DiscoverAddsUnverified(t *testing.T) {
	merged := &ai.MergedModelList{
		Verified: []ai.ModelInfo{
			{ID: "us.anthropic.claude-haiku-4-5", Provider: "bedrock", Type: "generation", Compatible: true},
		},
		Unverified: []ai.ModelInfo{
			{ID: "us.deepseek.r2-v1:0", Provider: "bedrock", Type: "generation", Compatible: true, Tier: ai.TierUnverified},
		},
	}

	without := verifyCandidatePool(merged, false)
	if len(without) != 1 || without[0].ID != "us.anthropic.claude-haiku-4-5" {
		t.Fatalf("without --discover the pool must stay the merged catalog, got %+v", without)
	}

	with := verifyCandidatePool(merged, true)
	if len(with) != 2 {
		t.Fatalf("with --discover the pool must include discoveries, got %d entries", len(with))
	}
	found := false
	for _, m := range with {
		if m.ID == "us.deepseek.r2-v1:0" {
			found = true
		}
	}
	if !found {
		t.Errorf("policy-enabled discovery missing from the --discover pool: %+v", with)
	}
	// The merged slice must not be mutated: BuildModelList's caller may still
	// read Verified afterwards.
	if len(merged.Verified) != 1 {
		t.Errorf("pool build appended into merged.Verified: %+v", merged.Verified)
	}
}

// TestVerifyCandidates_DiscoverWidensBeyondRecommended asserts --discover
// counts as a selector. Every discovered model is by definition neither
// recommended nor active, so if the default "recommended + active" narrowing
// stayed on, --discover would probe nothing new and the flag would be a
// silent no-op. Runs offline: AWS credentials are neutralized so live
// discovery contributes nothing and only the builtin catalog is exercised.
func TestVerifyCandidates_DiscoverWidensBeyondRecommended(t *testing.T) {
	_, root := newContractVault(t)
	neutralizeAWSCredentials(t)
	cfg := ai.DefaultAIConfig()

	// Mantle-plane models are builtin but deliberately NOT curated: they are
	// also invisible to ListFoundationModels, so they can only ever come from
	// the builtin catalog, never from discovery.
	const mantleID = "openai.gpt-5.5"
	ids := func(models []ai.ModelInfo) map[string]bool {
		out := map[string]bool{}
		for _, m := range models {
			out[m.ID] = true
		}
		return out
	}

	verifyProvider = "bedrock"
	defer func() { verifyProvider, verifyRecommended, verifyDiscover = "", false, false }()

	verifyRecommended, verifyDiscover = true, false
	curated, err := verifyCandidates(t.Context(), cfg, root, nil)
	if err != nil {
		t.Fatalf("verifyCandidates --recommended: %v", err)
	}
	if ids(curated)[mantleID] {
		t.Fatalf("%s is not curated; --recommended must not select it", mantleID)
	}

	verifyRecommended, verifyDiscover = false, true
	discovered, err := verifyCandidates(t.Context(), cfg, root, nil)
	if err != nil {
		t.Fatalf("verifyCandidates --discover: %v", err)
	}
	if len(discovered) <= len(curated) {
		t.Fatalf("--discover must widen the candidate set (%d) beyond the curated one (%d)", len(discovered), len(curated))
	}
	if !ids(discovered)[mantleID] {
		t.Errorf("--discover dropped the non-curated mantle builtin %s: %v", mantleID, ids(discovered))
	}
	// The mantle plane is generation-only in 2nb, and every candidate must
	// still clear the probeability filter.
	for _, m := range discovered {
		if m.Type == "rerank" || !m.Compatible {
			t.Errorf("unprobeable candidate leaked in: %+v", m)
		}
	}
}

// TestContract_ModelsVerifyEventsEnvelopeWithDiscover asserts --discover does
// not disturb the NDJSON contract the GUI decodes: the GUI's real invocation
// is `--provider bedrock --enabled-only --discover --yes --events`, and with
// no credentials that must still be exactly start+done at exit 0.
func TestContract_ModelsVerifyEventsEnvelopeWithDiscover(t *testing.T) {
	_, root := newContractVault(t)
	neutralizeAWSCredentials(t)

	got, err := runCLIArgs(t, root,
		"models", "verify", "--provider", "bedrock",
		"--enabled-only", "--discover", "--yes", "--events",
		"--cost-cap", "0")
	if err == nil {
		// With no credentials the builtin bedrock line is still a candidate
		// set (--discover drops the cred gate), so a zero cap must abort
		// before any spend rather than stream a probe.
		t.Fatalf("expected the cost cap to abort the spend, got success: %s", truncate(got, 300))
	}
	if !strings.Contains(err.Error(), "cost-cap") {
		t.Fatalf("error should be the cost-cap refusal, got %v", err)
	}

	// With the cap raised the envelope must be intact: start, zero or more
	// results, done — one decodable JSON object per line, nothing else.
	got, err = runCLIArgs(t, root,
		"models", "verify", "--provider", "bedrock", "--vendor", "nosuchvendor",
		"--discover", "--yes", "--events")
	if err != nil {
		t.Fatalf("models verify --discover --events: %v (out=%s)", err, truncate(got, 300))
	}
	lines := bytes.Split(bytes.TrimSpace(got), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected start+done for an empty vendor filter, got %d lines:\n%s", len(lines), got)
	}
	var start, done verifyEvent
	if err := json.Unmarshal(lines[0], &start); err != nil || start.Event != "start" || start.Total != 0 {
		t.Fatalf("bad start event %s (err=%v)", lines[0], err)
	}
	if err := json.Unmarshal(lines[1], &done); err != nil || done.Event != "done" {
		t.Fatalf("bad done event %s (err=%v)", lines[1], err)
	}
	if done.SavedScope != "vault" {
		t.Fatalf("done saved_scope = %q, want vault", done.SavedScope)
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

// TestContract_ModelsVerifyMultiRegion_CredGated is the end-to-end
// multi-region contract: with a second included region configured, verify's
// start event names the region set, each result carries the region it was
// probed in, and a primary-region pass persists NO region pin (self-heal
// semantics). Skips without AWS credentials; costs one probe.
func TestContract_ModelsVerifyMultiRegion_CredGated(t *testing.T) {
	if !ai.CheckBedrockCredentials(t.Context(), ai.BedrockConfig{Profile: "default", Region: "us-east-1"}) {
		t.Skip("AWS credentials not configured")
	}
	_, root := newContractVault(t)
	const model = "us.anthropic.claude-haiku-4-5-20251001-v1:0"

	if _, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--regions", "us-west-2"); err != nil {
		t.Fatalf("set regions: %v", err)
	}
	got, err := runCLIArgs(t, root,
		"models", "verify", model,
		"--yes", "--events", "--porcelain")
	if err != nil {
		t.Fatalf("verify: %v (out=%s)", err, truncate(got, 500))
	}

	var start, result verifyEvent
	sawResult := false
	for _, line := range bytes.Split(bytes.TrimSpace(got), []byte("\n")) {
		var ev verifyEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("bad NDJSON line %s: %v", line, err)
		}
		switch ev.Event {
		case "start":
			start = ev
		case "result":
			result = ev
			sawResult = true
		}
	}
	if len(start.Regions) != 2 || start.Regions[0] != "us-east-1" || start.Regions[1] != "us-west-2" {
		t.Fatalf("start regions = %v, want [us-east-1 us-west-2]", start.Regions)
	}
	if !sawResult || result.Result == nil {
		t.Fatal("no result event")
	}
	if !result.Result.OK {
		t.Skipf("account cannot invoke %s (%s); region-pin assertions need a pass", model, result.Result.Code)
	}
	if result.Result.Region != "us-east-1" {
		t.Fatalf("result region = %q, want the primary us-east-1", result.Result.Region)
	}

	// A primary-region pass must persist WITHOUT a region pin.
	for _, m := range ai.LoadUserCatalog(root) {
		if m.ID == model && m.Provider == "bedrock" {
			if m.Region != "" {
				t.Fatalf("primary pass persisted a region pin: %q", m.Region)
			}
			return
		}
	}
	t.Fatal("verified model not found in the user catalog")
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

// PR #180 review gap: the per-probe "result" event is the line a GUI progress
// bar renders, so its streamed shape must be verified, not just the empty
// stream. A dead Ollama endpoint drives a real probe to a deterministic
// offline failure through the full events pipeline.
func TestContract_ModelsVerifyEventsStreamsResult(t *testing.T) {
	_, root := newContractVault(t)
	if _, err := runCLIArgs(t, root, "config", "set", "ai.ollama.endpoint", "http://127.0.0.1:9"); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}

	got, err := runCLIArgs(t, root,
		"models", "verify", "all-minilm",
		"--provider", "ollama", "--yes", "--events")
	if err != nil {
		t.Fatalf("models verify --events: %v (out=%s)", err, truncate(got, 500))
	}
	lines := bytes.Split(bytes.TrimSpace(got), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("expected start+result+done, got %d lines:\n%s", len(lines), got)
	}
	var start struct {
		Event string  `json:"event"`
		Total int     `json:"total"`
		USD   float64 `json:"estimated_usd"`
	}
	if err := json.Unmarshal(lines[0], &start); err != nil || start.Event != "start" || start.Total != 1 {
		t.Fatalf("bad start event %s (err=%v)", lines[0], err)
	}
	var result struct {
		Event  string `json:"event"`
		N      int    `json:"n"`
		Total  int    `json:"total"`
		Result struct {
			ModelID string `json:"model_id"`
			OK      bool   `json:"ok"`
			Code    string `json:"code"`
		} `json:"result"`
	}
	if err := json.Unmarshal(lines[1], &result); err != nil {
		t.Fatalf("bad result event %s (err=%v)", lines[1], err)
	}
	if result.Event != "result" || result.N != 1 || result.Total != 1 {
		t.Fatalf("result event envelope wrong: %s", lines[1])
	}
	if result.Result.OK || result.Result.ModelID != "all-minilm" {
		t.Fatalf("expected a failed all-minilm probe, got %+v", result.Result)
	}
	if result.Result.Code != string(ai.TestErrProviderUnreachable) && result.Result.Code != string(ai.TestErrTimeout) {
		t.Fatalf("expected provider_unreachable/timeout, got %q", result.Result.Code)
	}
	var done struct {
		Event      string         `json:"event"`
		Total      int            `json:"total"`
		Summary    map[string]int `json:"summary"`
		SavedScope string         `json:"saved_scope"`
	}
	if err := json.Unmarshal(lines[2], &done); err != nil || done.Event != "done" {
		t.Fatalf("bad done event %s (err=%v)", lines[2], err)
	}
	if done.Total != 1 || done.SavedScope != "vault" {
		t.Fatalf("done envelope wrong: %s", lines[2])
	}
	total := 0
	for _, n := range done.Summary {
		total += n
	}
	if total != 1 {
		t.Fatalf("done summary should count the one probe, got %s", lines[2])
	}
}

// TestProbeSavePreservesRoutingFields pins the fix for a live-observed bug:
// `models test --save` rebuilt the entry from a merged row that had lost the
// user catalog's invoke_strategy, and SaveUserCatalogEntry replaces
// wholesale — so a hand-added mantle entry PASSED its probe and was
// simultaneously reclassified statically incompatible. Routing fields must
// survive a probe save; a CLASSIC entry's region stays under
// persistProbedRegion's control and is NOT preserved.
func TestProbeSavePreservesRoutingFields(t *testing.T) {
	_, root := newContractVault(t)
	scope := ai.ScopeVault

	if err := ai.SaveUserCatalogEntry(scope, root, ai.ModelInfo{
		ID: "xai.grok-4.6", Provider: "bedrock", Type: "generation",
		InvokeStrategy: ai.StrategyBedrockMantleResponses,
		Region:         "us-west-2",
		ContextLen:     500000,
	}); err != nil {
		t.Fatal(err)
	}

	// A probe-derived entry, as catalogEntryFromTestResult would build it
	// when the merged base lost the routing fields.
	entry := ai.ModelInfo{ID: "xai.grok-4.6", Provider: "bedrock", Type: "generation", Tier: ai.TierUserVerified}
	preserveRoutingFields(scope, root, &entry)
	if entry.InvokeStrategy != ai.StrategyBedrockMantleResponses {
		t.Errorf("invoke_strategy not preserved: %q", entry.InvokeStrategy)
	}
	if entry.Region != "us-west-2" {
		t.Errorf("mantle region pin not preserved: %q", entry.Region)
	}
	if entry.ContextLen != 500000 {
		t.Errorf("context length not preserved: %d", entry.ContextLen)
	}

	// Classic entry: region must NOT be resurrected (persistProbedRegion
	// owns it — a primary pass clears stale pins on purpose).
	if err := ai.SaveUserCatalogEntry(scope, root, ai.ModelInfo{
		ID: "us.anthropic.claude-sonnet-5", Provider: "bedrock", Type: "generation",
		Region: "us-west-2",
	}); err != nil {
		t.Fatal(err)
	}
	classic := ai.ModelInfo{ID: "us.anthropic.claude-sonnet-5", Provider: "bedrock", Type: "generation"}
	preserveRoutingFields(scope, root, &classic)
	if classic.Region != "" {
		t.Errorf("classic region must stay cleared, got %q", classic.Region)
	}
}

// TestAdoptCandidateRouting pins the persistence half of the mantle-discovery
// seam: a passing probe of a discovered row (no catalog entry anywhere) must
// save the row's routing hints so the ordinary resolvers route every future
// invoke — and must never overwrite what preserveRoutingFields already
// carried from an existing user entry, nor touch classic Region, which
// persistProbedRegion owns.
func TestAdoptCandidateRouting(t *testing.T) {
	// Discovered mantle candidate onto a bare probe-derived entry: strategy,
	// region, and context length are adopted.
	candidate := ai.ModelInfo{
		ID: "deepseek.v3.2", Provider: "bedrock", Type: "generation",
		InvokeStrategy: ai.StrategyBedrockMantleResponses,
		Region:         "us-east-1",
		ContextLen:     128000,
	}
	entry := ai.ModelInfo{ID: "deepseek.v3.2", Provider: "bedrock", Type: "generation", Tier: ai.TierUserVerified}
	adoptCandidateRouting(&entry, candidate)
	if entry.InvokeStrategy != ai.StrategyBedrockMantleResponses {
		t.Errorf("invoke_strategy not adopted: %q", entry.InvokeStrategy)
	}
	if entry.Region != "us-east-1" {
		t.Errorf("mantle region not adopted: %q", entry.Region)
	}
	if entry.ContextLen != 128000 {
		t.Errorf("context length not adopted: %d", entry.ContextLen)
	}

	// Existing values win (preserveRoutingFields ran first): nothing is
	// overwritten by the discovery hint.
	kept := ai.ModelInfo{
		ID: "deepseek.v3.2", Provider: "bedrock", Type: "generation",
		InvokeStrategy: ai.StrategyBedrockMantleResponses,
		Region:         "us-west-2",
		Endpoint:       "https://bedrock-mantle-custom.api.aws",
		ContextLen:     500000,
	}
	adoptCandidateRouting(&kept, candidate)
	if kept.Region != "us-west-2" || kept.ContextLen != 500000 || kept.Endpoint != "https://bedrock-mantle-custom.api.aws" {
		t.Errorf("existing routing overwritten: %+v", kept)
	}

	// A classic candidate (empty strategy) must never plant a Region:
	// persistProbedRegion owns classic pins, and a primary-region pass
	// clears them on purpose.
	classicCandidate := ai.ModelInfo{ID: "us.acme.model-1", Provider: "bedrock", Type: "generation", Region: "us-west-2"}
	classicEntry := ai.ModelInfo{ID: "us.acme.model-1", Provider: "bedrock", Type: "generation"}
	adoptCandidateRouting(&classicEntry, classicCandidate)
	if classicEntry.Region != "" {
		t.Errorf("classic region must stay under persistProbedRegion's control, got %q", classicEntry.Region)
	}
}

// TestLiveGrok46ClassicConverse_CredGated proves the first classic-plane xAI
// model end to end: the Converse allowlist admits it, the 1024-token probe
// budget survives always-on reasoning (a trivial answer bills ~180 output
// tokens, well under the cap), and the generator extracts the text block
// behind the reasoningContent block. Skips without AWS credentials; costs
// ~$0.002 (billed on actual tokens generated, not the cap).
func TestLiveGrok46ClassicConverse_CredGated(t *testing.T) {
	if !ai.CheckBedrockCredentials(t.Context(), ai.BedrockConfig{Profile: "default", Region: "us-east-1"}) {
		t.Skip("AWS credentials not configured")
	}
	_, root := newContractVault(t)
	got, err := runCLIArgs(t, root, "models", "test", "us.xai.grok-4.6", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models test: %v (out=%s)", err, truncate(got, 300))
	}
	var res struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(got, &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		if res.Code == "access_denied" {
			t.Skipf("account not entitled to grok-4.6 (%s)", res.Code)
		}
		t.Fatalf("probe failed: %+v (out=%s)", res, truncate(got, 300))
	}
}
