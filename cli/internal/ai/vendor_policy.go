package ai

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Vendor policies are the durable "enable only these vendors" statement per
// provider. Unlike per-model Enabled tri-states (one synthetic user-catalog
// entry per model, blind to models discovered later), a policy is declarative:
// every model on the policied provider whose Enabled tri-state is nil gets an
// explicit verdict at list time, so newly discovered models from non-chosen
// vendors arrive pre-disabled forever.
//
// Storage is a dedicated YAML file, never models.yaml or config.yaml: older
// CLIs round-trip those through typed structs and would silently drop an
// unknown policies key. Vault scope lives at <vault>/.2ndbrain/models-policy.yaml,
// global scope at ~/.config/2nb/models-policy.yaml (XDG-aware, mirroring
// globalCatalogPath). A vault entry for a provider fully overrides the global
// entry for that provider.

const (
	// VendorPolicyModeEnableOnly is the only policy mode in v1: models from
	// listed vendors are enabled, everything else on the provider is disabled.
	VendorPolicyModeEnableOnly = "enable_only"

	vendorPolicyFileName = "models-policy.yaml"
	vendorPolicyVersion  = 1
)

// VendorPolicy is one provider's vendor selection.
type VendorPolicy struct {
	Provider string   `yaml:"provider" json:"provider"`
	Mode     string   `yaml:"mode" json:"mode"`
	Vendors  []string `yaml:"vendors" json:"vendors"`
}

// VendorPolicyFile is the versioned on-disk document.
type VendorPolicyFile struct {
	Version  int            `yaml:"version" json:"version"`
	Policies []VendorPolicy `yaml:"policies" json:"policies"`
}

// ScopedVendorPolicy is a policy plus the scope it was loaded from, so
// callers can report provenance ("vault overrides global for bedrock").
type ScopedVendorPolicy struct {
	VendorPolicy
	Scope UserCatalogScope
}

var vendorPolicyMu sync.Mutex

// globalVendorPolicyPath returns the per-user policy file path, respecting
// $XDG_CONFIG_HOME if set (same resolution as globalCatalogPath).
func globalVendorPolicyPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "2nb", vendorPolicyFileName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "2nb", vendorPolicyFileName)
}

// vaultVendorPolicyPath returns the per-vault policy path, or "" when
// vaultRoot is empty.
func vaultVendorPolicyPath(vaultRoot string) string {
	if vaultRoot == "" {
		return ""
	}
	return filepath.Join(vaultRoot, dotDirName, vendorPolicyFileName)
}

// VendorPolicyPathForScope resolves the policy file path for a scope, so the
// CLI can tell the user exactly which file was written.
func VendorPolicyPathForScope(scope UserCatalogScope, vaultRoot string) (string, error) {
	switch scope {
	case ScopeGlobal:
		p := globalVendorPolicyPath()
		if p == "" {
			return "", fmt.Errorf("cannot resolve user home directory")
		}
		return p, nil
	case ScopeVault:
		if vaultRoot == "" {
			return "", fmt.Errorf("vault scope requires an open vault")
		}
		return vaultVendorPolicyPath(vaultRoot), nil
	default:
		return "", fmt.Errorf("unknown scope %q (use global or vault)", scope)
	}
}

// LoadVendorPolicies reads both the global and per-vault policy files and
// returns the merged view: a vault policy for a provider fully overrides the
// global policy for that provider. Missing files are not errors; a corrupt
// file is quarantined to .bak (matching user-catalog behavior) so a bad file
// never bricks the CLI, and the returned warnings say so out loud: without
// them a corrupted file would silently fail OPEN (every vendor re-enabled)
// with `models policy show` reporting no policies at all. Result is sorted
// by provider for determinism.
func LoadVendorPolicies(vaultRoot string) ([]ScopedVendorPolicy, []string) {
	vendorPolicyMu.Lock()
	defer vendorPolicyMu.Unlock()

	var warnings []string
	corrupt := func(path string, err error) {
		if err == nil {
			return
		}
		warnings = append(warnings, fmt.Sprintf(
			"vendor policy file %s was unreadable and is inactive (a malformed file is quarantined to %s.bak); re-apply it with `2nb models policy set`", path, path))
	}
	byProvider := map[string]ScopedVendorPolicy{}
	globalDoc, gerr := readVendorPolicyFile(globalVendorPolicyPath(), true)
	corrupt(globalVendorPolicyPath(), gerr)
	for _, p := range globalDoc.Policies {
		byProvider[p.Provider] = ScopedVendorPolicy{VendorPolicy: p, Scope: ScopeGlobal}
	}
	vaultDoc, verr := readVendorPolicyFile(vaultVendorPolicyPath(vaultRoot), true)
	corrupt(vaultVendorPolicyPath(vaultRoot), verr)
	for _, p := range vaultDoc.Policies {
		byProvider[p.Provider] = ScopedVendorPolicy{VendorPolicy: p, Scope: ScopeVault}
	}

	out := make([]ScopedVendorPolicy, 0, len(byProvider))
	for _, p := range byProvider {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out, warnings
}

// SaveVendorPolicy upserts the policy for p.Provider in the file at scope.
// Vendors are normalized (trimmed, lower-cased, deduped, sorted) before the
// write so the on-disk form is canonical.
func SaveVendorPolicy(scope UserCatalogScope, vaultRoot string, p VendorPolicy) error {
	if p.Provider == "" {
		return fmt.Errorf("vendor policy requires a provider")
	}
	if p.Mode != VendorPolicyModeEnableOnly {
		return fmt.Errorf("unsupported vendor policy mode %q (supported: %s)", p.Mode, VendorPolicyModeEnableOnly)
	}
	p.Vendors = NormalizeVendorSlugs(p.Vendors)
	if len(p.Vendors) == 0 {
		return fmt.Errorf("vendor policy requires at least one vendor")
	}

	vendorPolicyMu.Lock()
	defer vendorPolicyMu.Unlock()

	path, err := VendorPolicyPathForScope(scope, vaultRoot)
	if err != nil {
		return err
	}
	doc, rerr := readVendorPolicyFile(path, false)
	if rerr != nil {
		return fmt.Errorf("existing policy file is malformed and was left untouched; fix or remove it, then retry: %w", rerr)
	}
	replaced := false
	for i := range doc.Policies {
		if doc.Policies[i].Provider == p.Provider {
			doc.Policies[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Policies = append(doc.Policies, p)
	}
	sort.Slice(doc.Policies, func(i, j int) bool { return doc.Policies[i].Provider < doc.Policies[j].Provider })
	return writeVendorPolicyFile(path, doc)
}

// ClearVendorPolicy removes the policy for provider from the file at scope.
// Returns whether an entry was actually removed; a missing entry is not an
// error (nothing is written in that case).
func ClearVendorPolicy(scope UserCatalogScope, vaultRoot, provider string) (bool, error) {
	vendorPolicyMu.Lock()
	defer vendorPolicyMu.Unlock()

	path, err := VendorPolicyPathForScope(scope, vaultRoot)
	if err != nil {
		return false, err
	}
	doc, rerr := readVendorPolicyFile(path, false)
	if rerr != nil {
		return false, fmt.Errorf("existing policy file is malformed and was left untouched; fix or remove it, then retry: %w", rerr)
	}
	kept := doc.Policies[:0]
	removed := false
	for _, p := range doc.Policies {
		if p.Provider == provider {
			removed = true
			continue
		}
		kept = append(kept, p)
	}
	if !removed {
		return false, nil
	}
	doc.Policies = kept
	return true, writeVendorPolicyFile(path, doc)
}

// CheckVendorPolicyFile verifies the policy file at scope parses. Write
// flows run it BEFORE any catalog load: loads quarantine a corrupt file (the
// read path fails safe), which would otherwise remove the evidence before
// the write-path refusal in Save/ClearVendorPolicy could fire.
func CheckVendorPolicyFile(scope UserCatalogScope, vaultRoot string) error {
	vendorPolicyMu.Lock()
	defer vendorPolicyMu.Unlock()
	path, err := VendorPolicyPathForScope(scope, vaultRoot)
	if err != nil {
		return err
	}
	if _, rerr := readVendorPolicyFile(path, false); rerr != nil {
		return fmt.Errorf("existing policy file is malformed and was left untouched; fix or remove it, then retry: %w", rerr)
	}
	return nil
}

// NormalizeVendorSlugs canonicalizes a vendor slug list: trim, lower-case,
// drop empties, dedupe, sort.
func NormalizeVendorSlugs(vendors []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(vendors))
	for _, v := range vendors {
		slug := strings.ToLower(strings.TrimSpace(v))
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}

// KnownVendorSlugs returns the vendor slugs a policy may name for provider:
// every slug derivable via VendorOf from the given catalog entries on that
// provider, plus (for bedrock) the static recognized-vendor vocabulary. The
// static union matters because a policy states intent about FUTURE models: a
// user can enable-only anthropic+deepseek before any deepseek model is in the
// merged catalog, and discovered deepseek models then arrive enabled.
func KnownVendorSlugs(provider string, models []ModelInfo) map[string]bool {
	known := map[string]bool{}
	for _, m := range models {
		if m.Provider != provider {
			continue
		}
		if v := VendorOf(m.ID, provider).Vendor; v != "" {
			known[v] = true
		}
	}
	if provider == "bedrock" {
		for slug := range bedrockVendorDisplay {
			known[slug] = true
		}
	}
	return known
}

// VendorPolicyActiveGuard returns the guard applyVendorPolicy (and the CLI's
// effect preview) use to keep the active embedding/generation/rerank models
// out of a policy's blast radius: a policy states intent for the catalog, it
// must never silently hide the models currently in use. It checks the config
// directly (not just the Active mark) so discovered entries that happen to be
// active are protected too.
func VendorPolicyActiveGuard(cfg AIConfig) func(ModelInfo) bool {
	return func(m ModelInfo) bool {
		if m.Active || isActiveModel(m, cfg) {
			return true
		}
		return m.Type == "rerank" && cfg.RerankEnabled() && m.ID == cfg.ResolveRerankModel()
	}
}

// applyVendorPolicy gives every nil-Enabled model on a policied provider an
// explicit tri-state: Enabled = Ptr(vendor in policy.Vendors). Precedence:
// explicit per-model Enabled (user catalog) > policy > tier default, so an
// entry the user toggled by hand is never touched. A model activeGuard
// approves is never disabled: it stays enabled and a warning is returned
// instead. The vendor is computed via VendorOf(m.ID, m.Provider) here, NOT
// read from m.Vendor, because applyCatalogUIFields runs later in the
// BuildModelList pipeline. Mutates models in place; returns warnings.
func applyVendorPolicy(models []ModelInfo, policies []ScopedVendorPolicy, activeGuard func(ModelInfo) bool) []string {
	if len(policies) == 0 {
		return nil
	}
	type providerPolicy struct {
		vendors map[string]bool
	}
	byProvider := map[string]providerPolicy{}
	var warnings []string
	for _, p := range policies {
		if p.Mode != VendorPolicyModeEnableOnly {
			warnings = append(warnings, fmt.Sprintf("vendor policy for %s has unsupported mode %q, ignored (written by a newer 2nb?)", p.Provider, p.Mode))
			continue
		}
		vendors := map[string]bool{}
		for _, v := range p.Vendors {
			vendors[v] = true
		}
		byProvider[p.Provider] = providerPolicy{vendors: vendors}
	}

	for i := range models {
		p, ok := byProvider[models[i].Provider]
		if !ok {
			continue
		}
		if models[i].Enabled != nil {
			// Explicit per-model tri-state wins over the policy.
			continue
		}
		vendor := VendorOf(models[i].ID, models[i].Provider).Vendor
		allowed := p.vendors[vendor]
		if !allowed && activeGuard != nil && activeGuard(models[i]) {
			models[i].Enabled = Ptr(true)
			warnings = append(warnings, fmt.Sprintf(
				"vendor policy (%s): active %s model %s stays enabled although vendor %q is not in the enable-only list",
				models[i].Provider, models[i].Type, models[i].ID, vendor))
			continue
		}
		models[i].Enabled = Ptr(allowed)
	}
	return warnings
}

// ModelEnabledOverrides returns the explicit per-model Enabled tri-states
// recorded in the user catalog at ONE scope for provider, keyed by model ID.
// Unlike LoadUserCatalog this reads a single file, so the caller can tell a
// vault override from a global one. Read-only: a corrupt file is treated as
// empty without quarantining.
func ModelEnabledOverrides(scope UserCatalogScope, vaultRoot, provider string) (map[string]bool, error) {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, m := range readCatalog(path, false).Models {
		if m.Provider == provider && m.Enabled != nil {
			out[m.ID] = *m.Enabled
		}
	}
	return out, nil
}

// ClearModelEnabledOverrides nils the Enabled tri-state on every user-catalog
// entry at scope for provider (the file-level equivalent of `models
// enable-state --state default` per model), returning the cleared model IDs
// sorted. Entries themselves are kept: only the tri-state is dropped, so
// tested_at / benchmark history survives. `models policy set` runs this by
// default so a stale bulk enable/disable can't shadow the new policy.
func ClearModelEnabledOverrides(scope UserCatalogScope, vaultRoot, provider string) ([]string, error) {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return nil, err
	}
	cat, err := readCatalogForWrite(path)
	if err != nil {
		return nil, err
	}
	var cleared []string
	for i := range cat.Models {
		if cat.Models[i].Provider == provider && cat.Models[i].Enabled != nil {
			cat.Models[i].Enabled = nil
			cleared = append(cleared, cat.Models[i].ID)
		}
	}
	if len(cleared) == 0 {
		return nil, nil
	}
	sort.Strings(cleared)
	return cleared, writeCatalog(path, cat)
}

// readVendorPolicyFile reads and parses a policy file. Missing files return
// an empty document at the current version with a nil error. A file that
// exists but cannot be read or parsed returns an empty document AND a non-nil
// error so callers choose their failure mode: the read path (LoadVendorPolicies)
// quarantines to .bak and surfaces a warning, the write path (Save/Clear)
// refuses to proceed so a corrupt file can never be silently rewritten from
// empty, dropping other providers' policies.
func readVendorPolicyFile(path string, quarantineCorrupt bool) (VendorPolicyFile, error) {
	empty := VendorPolicyFile{Version: vendorPolicyVersion}
	if path == "" {
		return empty, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		slog.Warn("read vendor policy failed", "path", path, "err", err)
		return empty, fmt.Errorf("read vendor policy %s: %w", path, err)
	}
	var doc VendorPolicyFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		slog.Warn("parse vendor policy failed", "path", path, "err", err)
		if quarantineCorrupt {
			if renameErr := os.Rename(path, path+".bak"); renameErr != nil {
				slog.Warn("quarantine corrupt vendor policy failed", "path", path, "backup", path+".bak", "err", renameErr)
			} else {
				slog.Warn("quarantined corrupt vendor policy", "path", path, "backup", path+".bak")
			}
		}
		return empty, fmt.Errorf("parse vendor policy %s: %w", path, err)
	}
	if doc.Version == 0 {
		doc.Version = vendorPolicyVersion
	}
	return doc, nil
}

func writeVendorPolicyFile(path string, doc VendorPolicyFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create policy dir: %w", err)
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal policy: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write policy: %w", err)
	}
	return nil
}
