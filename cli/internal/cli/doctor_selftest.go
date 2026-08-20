package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/ai"
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
// probes; each individual probe carries its own 30s cap inside TestProbeModel.
func runSelfTest(ctx context.Context) SelfTestReport {
	root, hasSidecar := resolve2nbVaultRoot()
	cfg := readAIConfigReadOnly(root)

	r := SelfTestReport{
		VaultBound: hasSidecar,
		VaultPath:  root,
		Provider:   cfg.Provider,
	}

	// Providers must be registered against the resolved config before any
	// probe. initAIProvidersFor takes config + root rather than a *vault.Vault
	// precisely so tier 1 can run without opening one.
	initAIProvidersFor(cfg, root)

	// Each tier carries its own deadline so a slow provider in tier 1 cannot
	// starve tier 2 into reporting a timeout as an index defect.
	modelCtx, cancelModels := context.WithTimeout(ctx, doctorModelTierTimeout)
	tier1, creds := selfTestModels(modelCtx, cfg, root)
	cancelModels()
	r.Credentials = creds
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

// selfTestModels is tier 1: a real embed call and a real generation call
// against the ACTIVE models, plus the credential verdict derived from how they
// failed. No vault required.
func selfTestModels(ctx context.Context, cfg ai.AIConfig, vaultRoot string) ([]DoctorCheck, string) {
	embed := probeActiveModel(ctx, cfg, cfg.EmbeddingModel, "embedding", vaultRoot)
	gen := probeActiveModel(ctx, cfg, cfg.GenerationModel, "generation", vaultRoot)

	creds, why := deriveCredentialVerdict(embed, gen)
	checks := []DoctorCheck{
		{
			Name:   "api key",
			OK:     creds == credAccepted,
			Warn:   creds != credAccepted && creds != credRejected,
			Detail: credentialDetail(cfg.Provider, creds, why),
			Fix:    credentialFix(cfg.Provider, creds),
		},
		modelCheck("search model", cfg.EmbeddingModel, embed),
		modelCheck("answer model", cfg.GenerationModel, gen),
	}
	if cfg.Provider == "bedrock" {
		if div := ai.BedrockTokenDivergence(); div.Diverges {
			// Zero-network: pure env + file/Keychain comparison. This is where
			// "the app works but my terminal still 403s" gets diagnosed — the
			// env token outranks the saved key for every process that inherits
			// this shell's environment. With prefer_stored_token on the saved
			// key wins everywhere in 2nb, so the same divergence is merely
			// informational.
			if div.PreferStored {
				checks = append(checks, DoctorCheck{
					Name: "bedrock key source",
					OK:   true,
					Detail: fmt.Sprintf("prefer_stored_token is on: 2nb uses the saved key (ends %s); AWS_BEARER_TOKEN_BEDROCK (ends %s) still serves other tools",
						suffixOrUnknown(div.StoredSuffix), suffixOrUnknown(div.EnvSuffix)),
				})
			} else {
				checks = append(checks, DoctorCheck{
					Name: "bedrock key source",
					OK:   true,
					Warn: true,
					Detail: fmt.Sprintf("AWS_BEARER_TOKEN_BEDROCK (ends %s) overrides the saved key (ends %s) in this environment",
						suffixOrUnknown(div.EnvSuffix), suffixOrUnknown(div.StoredSuffix)),
					Fix: "unset AWS_BEARER_TOKEN_BEDROCK, or run `2nb config bedrock --set --prefer-stored-token` to make the saved key win for all 2nb use",
				})
			}
		}
	}
	return checks, creds
}

// probeActiveModel runs one real provider call. A nil result means the probe
// could not even be attempted (no model configured), which the caller renders
// distinctly from a probe that ran and failed.
func probeActiveModel(ctx context.Context, cfg ai.AIConfig, modelID, modelType, vaultRoot string) *ai.TestProbeResult {
	if modelID == "" {
		return nil
	}
	res, err := ai.TestProbeModel(ctx, cfg, modelID, cfg.Provider, modelType, vaultRoot)
	if err != nil {
		return &ai.TestProbeResult{
			ModelID:  modelID,
			Provider: cfg.Provider,
			Type:     modelType,
			Detail:   err.Error(),
			Code:     ai.ClassifyProbeError(cfg.Provider, err),
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
		return DoctorCheck{Name: name, OK: true, Detail: "responded in " + res.Latency}
	default:
		detail := trimDetail(res.Detail)
		if res.Code != "" {
			detail = fmt.Sprintf("%s (%s)", detail, res.Code)
		}
		return DoctorCheck{Name: name, OK: false, Detail: detail, Fix: res.Remediation}
	}
}

// deriveCredentialVerdict separates "your key is bad" from "your key is fine
// but this account cannot reach that model" — the distinction that decides
// whether the user should go hunting for a new key or file an access request.
//
// It must weigh ALL probes before deciding, because the two Bedrock planes
// report a bad key differently and a single result is not enough to judge:
// the mantle plane returns 401 with error.code=invalid_api_key (classified
// bad_credentials), while the classic runtime plane answers a bogus bearer
// token with a plain 403 that classifies as access_denied. So access_denied
// on its own does NOT prove the request authenticated — measured live against
// a deliberately invalid token, where the embedding probe 403'd and only the
// mantle generation probe revealed the key was junk. Reporting "key accepted"
// off that 403 would send a user with a dead key looking for an entitlement
// problem they do not have.
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
		if provider == "bedrock" {
			return "set a working key: `2nb config bedrock --set --token-stdin` (or Settings in the app)"
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
	embedder, _ := ai.DefaultRegistry.Embedder(v.Config.AI.Provider)
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
