package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/llama"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/vault"
	"gopkg.in/yaml.v3"
)

// The functional half of `2nb doctor`: proving the current configuration
// actually works, rather than that it is internally consistent on paper.
//
// It runs in two tiers, and the split is load-bearing rather than cosmetic.
// The macOS Settings window is the one surface that works with no vault bound
// — which is exactly the state a user is in when their credentials are wrong
// and they most need this answer. So the credential and model checks must never
// require a vault, and must never create one as a side effect: vault.Open mints
// .2ndbrain/, appends to .gitignore, and creates index.db, none of which should
// happen because someone pressed a button labelled "test".
//
//	Tier 1 (no vault): are the credentials good, and do the active models answer?
//	Tier 2 (vault):    does retrieval actually run through the vector channel,
//	                   and is the rest of the wiring sound?
type SelfTestReport struct {
	OK         bool   `json:"ok"`
	VaultBound bool   `json:"vault_bound"`
	VaultPath  string `json:"vault_path,omitempty"`
	Provider   string `json:"provider"`
	// Credentials is the plain verdict on the key itself, kept distinct from
	// per-model reachability because they fail for different reasons and have
	// different fixes. A model the account is not entitled to reach is NOT a
	// credential problem, and telling a user to re-check a working key is the
	// single most wasteful thing this command could do.
	Credentials string        `json:"credentials"` // accepted | rejected | unreachable | unknown
	Checks      []DoctorCheck `json:"checks"`
}

// Credential verdict values (also the JSON `credentials` field).
const (
	credAccepted    = "accepted"
	credRejected    = "rejected"
	credUnreachable = "unreachable"
	credUnknown     = "unknown"
)

// runSelfTest executes both tiers and rolls up an OK. ctx bounds the network
// probes; each individual probe carries its own strategy-aware cap inside
// TestProbeModel (ai.ProbeDeadline: the route's transport worst case + slack).
func runSelfTest(ctx context.Context) SelfTestReport {
	root, hasSidecar := resolve2nbVaultRoot()
	cfg := readAIConfigReadOnly(root)
	if probeOverride := strings.TrimSpace(os.Getenv("2NB_DOCTOR_PROBE_PROVIDER")); probeOverride != "" {
		if probeOverride == "google" {
			probeOverride = "gemini"
		}
		cfg.Provider = probeOverride
		cfg.EmbeddingProvider = probeOverride
		cfg.GenerationProvider = probeOverride
		if probeOverride == "gemini" {
			if cfg.EmbeddingModel == "" || ai.InferProvider(cfg.EmbeddingModel) != "gemini" {
				cfg.EmbeddingModel = ai.DefaultGeminiEmbeddingModel
				cfg.Dimensions = ai.DefaultGeminiDimensions
			}
			if cfg.GenerationModel == "" || ai.InferProvider(cfg.GenerationModel) != "gemini" {
				cfg.GenerationModel = ai.DefaultGeminiGenerationModel
			}
		}
		if probeOverride == "llama-local" {
			if cfg.EmbeddingModel == "" || ai.InferProvider(cfg.EmbeddingModel) != "llama-local" {
				cfg.EmbeddingModel = llama.DefaultEmbedModelID
				cfg.Dimensions = 768
			}
			if cfg.GenerationModel == "" || ai.InferProvider(cfg.GenerationModel) != "llama-local" {
				cfg.GenerationModel = "gemma4-e2b"
			}
		}
	}
	applyDoctorGeminiProbeMode(&cfg)

	r := SelfTestReport{
		VaultBound: hasSidecar,
		VaultPath:  root,
		Provider:   cfg.Provider,
	}

	// Capture the token divergence BEFORE providers initialize: with
	// prefer_stored_token on, provider init hydrates the bearer token and
	// OVERWRITES AWS_BEARER_TOKEN_BEDROCK with the stored key, after which
	// env == stored and a later divergence read reports nothing — hiding
	// the key-source check in exactly the scenario it exists to surface.
	var keyDivergence ai.TokenDivergence
	if cfg.EffectiveGenerationProvider() == "bedrock" || cfg.EffectiveEmbeddingProvider() == "bedrock" {
		keyDivergence = ai.BedrockTokenDivergence()
	}

	// Providers must be registered against the resolved config before any
	// probe. initAIProvidersFor takes config + root rather than a *vault.Vault
	// precisely so tier 1 can run without opening one.
	initAIProvidersFor(cfg, root)

	// Each tier carries its own deadline so a slow provider in tier 1 cannot
	// starve tier 2 into reporting a timeout as an index defect.
	probeStart := time.Now()
	modelCtx, cancelModels := context.WithTimeout(ctx, doctorModelTierTimeout)
	tier1, creds, credProvider, embedRes, genRes := selfTestModelsWithResults(modelCtx, cfg, root, keyDivergence)
	cancelModels()
	if hasSidecar && root != "" && (embedRes != nil || genRes != nil) {
		var legs []metrics.UsageLeg
		if embedRes != nil && embedRes.OK {
			legs = append(legs, buildUsageLeg(root, embedRes.Provider, embedRes.ModelID, "embedding", metrics.OpProbe, 1, 20, 0))
		}
		if genRes != nil && genRes.OK {
			legs = append(legs, buildUsageLeg(root, genRes.Provider, genRes.ModelID, "generation", metrics.OpProbe, 1, 20, 16))
		}
		if len(legs) > 0 {
			op := metrics.Operation{
				Operation:  metrics.OpProbe,
				DurationMs: time.Since(probeStart).Milliseconds(),
				OK:         true,
			}
			attachUsageLegs(&op, legs...)
			recordMetricAtDotDir(filepath.Join(root, ".2ndbrain"), op)
		}
	}
	r.Credentials = creds
	if credProvider != "" {
		r.Provider = credProvider
	}
	r.Checks = append(r.Checks, tier1...)

	vaultCtx, cancelVault := context.WithTimeout(ctx, doctorVaultTierTimeout)
	defer cancelVault()
	r.Checks = append(r.Checks, selfTestVault(vaultCtx, root, hasSidecar)...)

	r.OK = true
	for _, c := range r.Checks {
		if !c.OK {
			r.OK = false
		}
	}
	return r
}

// applyDoctorGeminiProbeMode pins WHICH Gemini credential path the self-test
// judges, from the environment the macOS app sets while verifying keys before
// saving them:
//
//	2NB_DOCTOR_PROBE_GEMINI_MODE=apikey  judge GEMINI_API_KEY alone (no Vertex)
//	2NB_DOCTOR_PROBE_GEMINI_MODE=vertex  judge Vertex AI on the given project
//	                                     (2NB_DOCTOR_PROBE_GCP_PROJECT /
//	                                     2NB_DOCTOR_PROBE_GCP_LOCATION)
func applyDoctorGeminiProbeMode(cfg *ai.AIConfig) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("2NB_DOCTOR_PROBE_GEMINI_MODE"))) {
	case "apikey":
		cfg.Gemini.UseVertexAI = false
		cfg.Gemini.Project = ""
	case "vertex":
		cfg.Gemini.UseVertexAI = true
		if v := strings.TrimSpace(os.Getenv("2NB_DOCTOR_PROBE_GCP_PROJECT")); v != "" {
			cfg.Gemini.Project = v
		}
		if v := strings.TrimSpace(os.Getenv("2NB_DOCTOR_PROBE_GCP_LOCATION")); v != "" {
			cfg.Gemini.Location = v
		}
	}
}

// selfTestModels is tier 1: a real embed call and a real generation call
// against the ACTIVE models, plus the credential verdict derived from how they
// failed. No vault required.
// keyDivergence must be captured by the caller BEFORE provider init (see
// runSelfTest): reading it here would race the hydrate shim's env overwrite.
func isKeylessLocalProvider(p string) bool {
	return p == "llama-local" || p == "ollama"
}

func selfTestModels(ctx context.Context, cfg ai.AIConfig, vaultRoot string, keyDivergence ai.TokenDivergence) ([]DoctorCheck, string, string) {
	checks, creds, credProvider, _, _ := selfTestModelsWithResults(ctx, cfg, vaultRoot, keyDivergence)
	return checks, creds, credProvider
}

func selfTestModelsWithResults(ctx context.Context, cfg ai.AIConfig, vaultRoot string, keyDivergence ai.TokenDivergence) ([]DoctorCheck, string, string, *ai.TestProbeResult, *ai.TestProbeResult) {
	embedProvider := cfg.EffectiveEmbeddingProvider()
	genProvider := cfg.EffectiveGenerationProvider()

	embed := probeActiveModel(ctx, cfg, cfg.EmbeddingModel, embedProvider, "embedding", vaultRoot)
	gen := probeActiveModel(ctx, cfg, cfg.GenerationModel, genProvider, "generation", vaultRoot)

	var keyedResults []*ai.TestProbeResult
	credProvider := ""
	if embedProvider != "" && !isKeylessLocalProvider(embedProvider) {
		credProvider = embedProvider
		if embed != nil {
			keyedResults = append(keyedResults, embed)
		}
	}
	if genProvider != "" && !isKeylessLocalProvider(genProvider) {
		if credProvider == "" || genProvider == "bedrock" || genProvider == "gemini" {
			credProvider = genProvider
		}
		if gen != nil {
			keyedResults = append(keyedResults, gen)
		}
	}

	var creds, apiDetail, apiFix string
	if len(keyedResults) > 0 || credProvider != "" {
		var why string
		creds, why = deriveCredentialVerdict(keyedResults...)
		apiDetail = credentialDetail(credProvider, creds, why)
		apiFix = credentialFix(credProvider, creds)
	} else {
		credProvider = genProvider
		if credProvider == "" {
			credProvider = embedProvider
		}
		creds, _ = deriveCredentialVerdict(embed, gen)
		if creds == credAccepted {
			apiDetail = "no API key required (on-device)"
		} else {
			if creds == credUnknown {
				creds = credUnreachable
			}
			apiDetail = "local model runtime unreachable"
			apiFix = "ensure local Gemma model weights are downloaded and retry"
		}
	}

	checks := []DoctorCheck{
		{
			Name:   "api key",
			OK:     creds == credAccepted,
			Warn:   creds != credAccepted && creds != credRejected,
			Detail: apiDetail,
			Fix:    apiFix,
		},
		modelCheck("search model", cfg.EmbeddingModel, embed),
		modelCheck("answer model", cfg.GenerationModel, gen),
	}
	if embedProvider == "bedrock" || genProvider == "bedrock" {
		if check := bedrockKeySourceCheck(keyDivergence); check != nil {
			checks = append(checks, *check)
		}
	}
	return checks, creds, credProvider, embed, gen
}

// bedrockKeySourceCheck renders the env-vs-stored key divergence. Zero
// network: pure env + file/Keychain comparison, computed by the caller before
// provider init. This is where "the app works but my terminal still 403s"
// gets diagnosed — the env token outranks the saved key for every process
// that inherits this shell's environment. With prefer_stored_token on the
// saved key wins everywhere in 2nb, so the same divergence is merely
// informational. Nil when the keys don't diverge.
func bedrockKeySourceCheck(div ai.TokenDivergence) *DoctorCheck {
	if !div.Diverges {
		return nil
	}
	if div.PreferStored {
		return &DoctorCheck{
			Name: "bedrock key source",
			OK:   true,
			Detail: fmt.Sprintf("prefer_stored_token is on: 2nb uses the saved key (ends %s); AWS_BEARER_TOKEN_BEDROCK (ends %s) still serves other tools",
				suffixOrUnknown(div.StoredSuffix), suffixOrUnknown(div.EnvSuffix)),
		}
	}
	return &DoctorCheck{
		Name: "bedrock key source",
		OK:   true,
		Warn: true,
		Detail: fmt.Sprintf("AWS_BEARER_TOKEN_BEDROCK (ends %s) overrides the saved key (ends %s) in this environment",
			suffixOrUnknown(div.EnvSuffix), suffixOrUnknown(div.StoredSuffix)),
		Fix: "unset AWS_BEARER_TOKEN_BEDROCK, or run `2nb config bedrock --set --prefer-stored-token` to make the saved key win for all 2nb use",
	}
}

// probeActiveModel runs one real provider call. A nil result means the probe
// could not even be attempted (no model configured), which the caller renders
// distinctly from a probe that ran and failed.
func probeActiveModel(ctx context.Context, cfg ai.AIConfig, modelID, slotProvider, modelType, vaultRoot string) *ai.TestProbeResult {
	if modelID == "" {
		return nil
	}
	res, err := ai.TestProbeModel(ctx, cfg, modelID, slotProvider, modelType, vaultRoot)
	if err != nil {
		return &ai.TestProbeResult{
			ModelID:  modelID,
			Provider: slotProvider,
			Type:     modelType,
			Detail:   err.Error(),
			Code:     ai.ClassifyProbeError(slotProvider, err),
		}
	}
	return res
}

// modelCheck renders one model probe. On failure it leads with the classified
// cause and carries the probe's own remediation, so a mantle model the account
// is not entitled to points at AWS Sales rather than the Bedrock console.
func modelCheck(label, modelID string, res *ai.TestProbeResult) DoctorCheck {
	name := label
	if modelID != "" {
		name = fmt.Sprintf("%s (%s)", label, modelID)
	}
	switch {
	case modelID == "":
		return DoctorCheck{
			Name:   name,
			OK:     false,
			Detail: "no model configured",
			Fix:    "pick one in Settings, or run `2nb ai setup`",
		}
	case res == nil:
		return DoctorCheck{Name: name, OK: false, Detail: "probe did not run"}
	case res.OK:
		return DoctorCheck{Name: name, OK: true, Detail: modelOKDetail(res)}
	default:
		detail := trimDetail(res.Detail)
		if res.Code != "" {
			detail = fmt.Sprintf("%s (%s)", detail, res.Code)
		}
		return DoctorCheck{Name: name, OK: false, Detail: detail, Fix: res.Remediation}
	}
}

// modelOKDetail renders a passing probe WITH its proof: the latency prefix is
// stable ("responded in "), followed by what the model actually returned (the
// generation reply, or the embedding's dimensions and norm).
func modelOKDetail(res *ai.TestProbeResult) string {
	d := strings.TrimSpace(res.Detail)
	switch {
	case d == "":
		return "responded in " + res.Latency
	case res.Type == "generation":
		return fmt.Sprintf("responded in %s (replied %q)", res.Latency, d)
	default:
		return fmt.Sprintf("responded in %s (%s)", res.Latency, d)
	}
}

// deriveCredentialVerdict separates "your key is bad" from "your key is fine
// but this account cannot reach that model" — the distinction that decides
// whether the user should go hunting for a new key or file an access request.
//
// It must weigh ALL probes before deciding, because the two Bedrock planes
// report a bad key differently and a single result is not enough to judge:
// the mantle plane returns 401 with error.code=invalid_api_key (classified
// bad_credentials), while the classic plane answers a bad bearer token with a
// 403 AccessDenied. ClassifyProbeError now reads that 403's message body and
// classifies the known bad-key phrasings as bad_credentials, so the common case
// resolves on the classic plane alone. The rule below still stands for the rest:
// a 403 whose body does NOT name the key is genuinely ambiguous between "not
// entitled" and "key the service will not explain", so access_denied on its own
// does NOT prove the request authenticated. Reporting "key accepted" off such a
// 403 would send a user with a dead key looking for an entitlement problem they
// do not have.
//
// Precedence, strongest evidence first:
//
//	any success          -> accepted   (definitive: the key authenticated)
//	any bad_credentials  -> rejected   (definitive: a valid key never yields it)
//	only access_denied   -> unknown    (ambiguous by construction, see above)
//	unreachable/timeout  -> unreachable
//
// The second return value explains an inconclusive verdict for display.
func deriveCredentialVerdict(results ...*ai.TestProbeResult) (verdict, why string) {
	var sawSuccess, sawRejected, sawDenied, sawUnreachable, sawRan bool
	for _, res := range results {
		if res == nil {
			continue
		}
		sawRan = true
		if res.OK {
			sawSuccess = true
			continue
		}
		switch res.Code {
		case ai.TestErrAccessDenied:
			sawDenied = true
		case ai.TestErrBadCredentials:
			sawRejected = true
		case ai.TestErrProviderUnreachable, ai.TestErrTimeout:
			sawUnreachable = true
		}
	}
	switch {
	case sawSuccess:
		return credAccepted, ""
	case sawRejected:
		return credRejected, ""
	case sawDenied:
		return credUnknown, "every model refused with access denied, which is either a bad key or an account not entitled to these models"
	case sawUnreachable:
		return credUnreachable, ""
	case !sawRan:
		return credUnknown, "no model is configured to test with"
	}
	return credUnknown, "no model responded"
}

func credentialDetail(provider, verdict, why string) string {
	switch verdict {
	case credAccepted:
		return fmt.Sprintf("accepted by %s", provider)
	case credRejected:
		return fmt.Sprintf("rejected by %s", provider)
	case credUnreachable:
		return fmt.Sprintf("could not reach %s to check", provider)
	}
	if why != "" {
		return "could not determine — " + why
	}
	return fmt.Sprintf("could not determine (no %s model responded)", provider)
}

func credentialFix(provider, verdict string) string {
	switch verdict {
	case credRejected:
		if provider == "gemini" {
			return "set a working key in Settings > Gemini (macOS app), export GEMINI_API_KEY=..., or run gcloud auth application-default login for Vertex AI"
		}
		if provider == "bedrock" {
			return "set a working key: 2nb config bedrock --set --token-stdin"
		}
		return fmt.Sprintf("set a working key: `2nb config set-key %s`", provider)
	case credUnreachable:
		return "check your network, then re-run `2nb doctor`"
	case credUnknown:
		return "confirm the key, then request model access — `2nb models verify` probes every model so a working key shows up as a pass somewhere"
	}
	return ""
}

// selfTestVault is tier 2. It runs only when the vault already carries a
// .2ndbrain/ sidecar, so `2nb doctor` inside a plain Obsidian vault reports the
// gap instead of silently creating one.
func selfTestVault(ctx context.Context, root string, hasSidecar bool) []DoctorCheck {
	if !hasSidecar {
		detail := "no vault bound — skipped the index and retrieval checks"
		fix := "open a vault in Obsidian, or pass --vault"
		if root != "" {
			detail = fmt.Sprintf("%s is not indexed by 2nb yet — skipped the index and retrieval checks", root)
			fix = "run `2nb index` to build the index"
		}
		return []DoctorCheck{{Name: "vault", OK: true, Warn: true, Detail: detail, Fix: fix}}
	}

	v, err := openVault()
	if err != nil {
		return []DoctorCheck{{
			Name:   "vault",
			OK:     false,
			Detail: trimDetail(err.Error()),
			Fix:    "check the vault path, then re-run `2nb doctor`",
		}}
	}
	defer v.Close()

	// Re-register against the OPENED vault's config. Tier 1 registered against a
	// config read without opening anything, which is normally the same file —
	// but if that read fell back to defaults (unreadable or corrupt config.yaml)
	// the registry could otherwise be pointed at a different provider than the
	// vault checks are about to judge.
	initAIProviders(v)

	checks := []DoctorCheck{{Name: "vault", OK: true, Detail: v.Root}}

	// Config coherence, then the engine + retrieval assertion. Both are reused
	// wholesale from the commands that own them, so `doctor` can never drift
	// from `config doctor` / `mcp doctor`.
	embedder, _ := ai.DefaultRegistry.Embedder(v.Config.AI.EffectiveEmbeddingProvider())
	checks = append(checks, buildDoctorChecks(ctx, v.Root, v.Config.AI, embedder, gatherDoctorVaultState(v))...)
	checks = append(checks, buildMCPDoctorReport(ctx, v).Checks...)
	return checks
}

// resolveVaultRootReadOnly resolves the vault root WITHOUT opening it, so a
// read-only command never triggers vault.Open's side effects (creating
// .2ndbrain/, appending to .gitignore, creating index.db). Returns "" when no
// vault resolves. Shared by `doctor`'s self-test and its plugin-version read.
func resolveVaultRootReadOnly() string {
	dir, source := resolveVaultDir()
	if source == sourceCwd {
		// resolveVaultDir returns "." for the cwd case; openResolvedVault would
		// normally walk up via vault.Open. Do that walk read-only instead.
		abs, err := filepath.Abs(dir)
		if err != nil {
			return ""
		}
		dir = vault.FindVaultRoot(abs)
	}
	if dir == "" || !vault.IsVaultRoot(dir) {
		return ""
	}
	return dir
}

// resolve2nbVaultRoot adds to that the question the vault tier actually needs:
// does this root already carry a .2ndbrain/ sidecar, i.e. is it a vault 2nb has
// indexed, or merely an Obsidian folder it has never touched?
func resolve2nbVaultRoot() (root string, hasSidecar bool) {
	dir := resolveVaultRootReadOnly()
	if dir == "" {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(dir, vault.DotDirName)); err != nil {
		// An Obsidian vault 2nb has never indexed. Real, but not usable for the
		// vault-tier checks.
		return dir, false
	}
	return dir, true
}

// readAIConfigReadOnly loads a vault's AI config without vault.LoadConfig's
// self-healing writes (which regenerate config.yaml on a missing or corrupt
// file). Falls back to the shipped defaults, so tier 1 still has active model
// IDs to probe during first-run when no vault exists at all.
func readAIConfigReadOnly(root string) ai.AIConfig {
	if root == "" {
		return ai.DefaultAIConfig()
	}
	data, err := os.ReadFile(filepath.Join(root, vault.DotDirName, "config.yaml"))
	if err != nil {
		return ai.DefaultAIConfig()
	}
	var cfg vault.VaultConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil || cfg.AI.Provider == "" {
		return ai.DefaultAIConfig()
	}
	return cfg.AI
}
