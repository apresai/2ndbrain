package ai

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// dotDirName mirrors vault.DotDirName. Duplicated here to keep the ai package
// free of a dependency on internal/vault (which imports ai → cycle).
const dotDirName = ".2ndbrain"

// UserCatalogScope identifies where a user catalog entry lives.
type UserCatalogScope string

const (
	// ScopeGlobal is the per-user catalog at $XDG_CONFIG_HOME/2nb/models.yaml
	// (falling back to ~/.config/2nb/models.yaml).
	ScopeGlobal UserCatalogScope = "global"
	// ScopeVault is the per-vault catalog at <vault>/.2ndbrain/models.yaml.
	ScopeVault UserCatalogScope = "vault"
)

const userCatalogFileName = "models.yaml"
const userCatalogVersion = 1

var userCatalogMu sync.Mutex

// UserCatalog is the YAML shape for both global and per-vault catalog files.
type UserCatalog struct {
	Version int         `yaml:"version"`
	Models  []ModelInfo `yaml:"models"`
}

// LoadUserCatalog reads both the global and the per-vault user catalogs and
// returns a single merged slice. The vault catalog takes precedence over the
// global catalog when both contain an entry with the same (provider, id).
//
// Missing files are not errors. A corrupt file is renamed to .bak and treated
// as empty so a malformed catalog never blocks the CLI.
func LoadUserCatalog(vaultRoot string) []ModelInfo {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	global := readCatalog(globalCatalogPath(), true).Models
	perVault := readCatalog(vaultCatalogPath(vaultRoot), true).Models

	// Fill in the plane of rows written before routes existed, so they still
	// overlay onto the builtin they describe. Both scopes are canonicalized
	// BEFORE they are merged with each other, or a legacy global row and a
	// routed vault row for the same endpoint would fail to combine.
	builtin := BuiltinCatalog()
	canonicalizeUserRoutes(global, builtin)
	canonicalizeUserRoutes(perVault, builtin)

	merged := make([]ModelInfo, 0, len(global)+len(perVault))
	merged = append(merged, global...)
	// Both sides are user files, so neither owns a fact the other must defer
	// to: the per-vault row keeps winning on every value it sets.
	merged = overlay(merged, perVault, false)
	for i := range merged {
		tagAsUserCatalog(&merged[i])
	}
	return merged
}

// SaveUserCatalogEntry writes a single entry to the catalog at `scope`. The
// file is created if it doesn't exist. An existing entry with the same ROUTE
// is replaced wholesale: fields omitted on entry are deleted from the stored
// row. Callers that persist one field must read the stored row first via
// UserCatalogEntry.
func SaveUserCatalogEntry(scope UserCatalogScope, vaultRoot string, entry ModelInfo) error {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return err
	}
	cat, err := readCatalogForWrite(path)
	if err != nil {
		return err
	}

	// Replace the row for this exact ROUTE. Matching on (provider, id) would
	// let a probe of one endpoint overwrite another endpoint's verdict, which
	// is how a passing mantle row used to be clobbered by a classic save.
	replaced := false
	want := routeKey(entry.Route())
	for i := range cat.Models {
		if routeKey(cat.Models[i].Route()) == want {
			cat.Models[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		cat.Models = append(cat.Models, entry)
	}

	return writeCatalog(path, cat)
}

// UserCatalogEntry returns the raw stored entry for one ROUTE from the single
// catalog file at scope — no builtin merge, no overlay — so a caller about to
// REPLACE an entry (SaveUserCatalogEntry is wholesale) can see exactly what it
// would erase. ok is false when the file has no such entry.
func UserCatalogEntry(scope UserCatalogScope, vaultRoot string, route RouteKey) (ModelInfo, bool) {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return ModelInfo{}, false
	}
	cat, err := readCatalogForWrite(path)
	if err != nil {
		return ModelInfo{}, false
	}
	want := routeKey(route)
	for _, m := range cat.Models {
		if routeKey(m.Route()) == want {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// UserCatalogRouteToPreserve finds the stored row whose routing a save should
// carry forward, for an entry that may not know its own route yet.
//
// It looks for an exact route match first. Failing that — the common case when
// a probe result is turned into a save and carries no plane — it falls back to
// the stored rows for that (provider, id), and returns one ONLY when there is
// exactly one. With several routes stored, picking any of them would graft one
// endpoint's routing onto another, which is the class of mistake route
// identity exists to prevent, so it returns nothing and the caller keeps
// whatever the probe actually established.
func UserCatalogRouteToPreserve(scope UserCatalogScope, vaultRoot string, route RouteKey) (ModelInfo, bool) {
	if m, ok := UserCatalogEntry(scope, vaultRoot, route); ok {
		return m, true
	}
	if route.Plane != "" && route.Region != "" {
		return ModelInfo{}, false
	}

	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return ModelInfo{}, false
	}
	cat, err := readCatalogForWrite(path)
	if err != nil {
		return ModelInfo{}, false
	}
	var found ModelInfo
	n := 0
	for _, m := range cat.Models {
		if m.Provider != route.Provider || m.ID != route.ID {
			continue
		}
		// Honor any constraint the caller DID supply.
		if route.Plane != "" && m.Plane != route.Plane {
			continue
		}
		if route.Region != "" && m.Region != route.Region {
			continue
		}
		found = m
		n++
	}
	if n != 1 {
		return ModelInfo{}, false
	}
	return found, true
}

// RemoveUserCatalogEntry removes rows from the catalog at `scope`.
//
// An empty plane AND empty region removes every route of that (provider, id):
// `models remove <id>` means "stop tracking this model". A set plane and/or
// region constrains the delete (so `id@classic/us-east-1` leaves the sibling
// routes). Returns the number of rows removed. Zero means nothing matched;
// no empty catalog file is written then.
func RemoveUserCatalogEntry(scope UserCatalogScope, vaultRoot string, route RouteKey) (int, error) {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return 0, err
	}
	cat, err := readCatalogForWrite(path)
	if err != nil {
		return 0, err
	}

	kept := cat.Models[:0]
	n := 0
	for _, m := range cat.Models {
		if catalogRowMatchesRoute(m, route) {
			n++
			continue
		}
		kept = append(kept, m)
	}
	if n == 0 {
		return 0, nil
	}
	cat.Models = kept
	return n, writeCatalog(path, cat)
}

// catalogRowMatchesRoute reports whether m is selected by route. Empty plane
// and region mean "every route of this model"; any field that is set is a
// constraint, matching RouteRef.Matches.
func catalogRowMatchesRoute(m ModelInfo, route RouteKey) bool {
	if m.Provider != route.Provider || m.ID != route.ID {
		return false
	}
	if route.Plane != "" && m.Plane != route.Plane {
		return false
	}
	if route.Region != "" && m.Region != route.Region {
		return false
	}
	return true
}

// globalCatalogPath returns the path to the per-user catalog file, respecting
// $XDG_CONFIG_HOME if set.
func globalCatalogPath() string {
	dir := globalConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, userCatalogFileName)
}

// vaultCatalogPath returns the per-vault catalog path, or "" when vaultRoot is empty.
func vaultCatalogPath(vaultRoot string) string {
	if vaultRoot == "" {
		return ""
	}
	return filepath.Join(vaultRoot, dotDirName, userCatalogFileName)
}

// CatalogPathForScope is the exported form of catalogPathForScope, used by
// CLI code that wants to tell the user exactly which file was written.
func CatalogPathForScope(scope UserCatalogScope, vaultRoot string) (string, error) {
	return catalogPathForScope(scope, vaultRoot)
}

func catalogPathForScope(scope UserCatalogScope, vaultRoot string) (string, error) {
	switch scope {
	case ScopeGlobal:
		p := globalCatalogPath()
		if p == "" {
			return "", fmt.Errorf("cannot resolve user home directory")
		}
		return p, nil
	case ScopeVault:
		if vaultRoot == "" {
			return "", fmt.Errorf("vault scope requires an open vault")
		}
		return vaultCatalogPath(vaultRoot), nil
	default:
		return "", fmt.Errorf("unknown scope %q (use global or vault)", scope)
	}
}

// readCatalog reads and parses a user catalog file. Missing files return an
// empty catalog at the current version. Corrupt files are handled based on
// `quarantineCorrupt`: true renames the bad file to .bak (so the next write
// produces a fresh one); false leaves it in place. Either way the caller
// gets an empty catalog — the CLI never bricks on a bad file.
func readCatalog(path string, quarantineCorrupt bool) UserCatalog {
	empty := UserCatalog{Version: userCatalogVersion}
	if path == "" {
		return empty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("read user catalog failed", "path", path, "err", err)
		}
		return empty
	}
	var cat UserCatalog
	if err := yaml.Unmarshal(data, &cat); err != nil {
		slog.Warn("parse user catalog failed", "path", path, "err", err)
		if quarantineCorrupt {
			if renameErr := os.Rename(path, path+".bak"); renameErr != nil {
				slog.Warn("quarantine corrupt user catalog failed", "path", path, "backup", path+".bak", "err", renameErr)
			} else {
				slog.Warn("quarantined corrupt user catalog", "path", path, "backup", path+".bak")
			}
		}
		return empty
	}
	if cat.Version == 0 {
		cat.Version = userCatalogVersion
	}
	return cat
}

func readCatalogForWrite(path string) (UserCatalog, error) {
	empty := UserCatalog{Version: userCatalogVersion}
	if path == "" {
		return empty, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		slog.Warn("read user catalog failed", "path", path, "err", err)
		return empty, fmt.Errorf("read catalog: %w", err)
	}
	var cat UserCatalog
	if err := yaml.Unmarshal(data, &cat); err != nil {
		backup := path + ".bak"
		slog.Warn("parse user catalog failed before write", "path", path, "backup", backup, "err", err)
		if renameErr := os.Rename(path, backup); renameErr != nil {
			slog.Warn("quarantine corrupt user catalog failed before write", "path", path, "backup", backup, "err", renameErr)
			return empty, fmt.Errorf("catalog %s is corrupt and could not be moved to %s: %w", path, backup, renameErr)
		}
		slog.Warn("quarantined corrupt user catalog before write", "path", path, "backup", backup)
		return empty, nil
	}
	if cat.Version == 0 {
		cat.Version = userCatalogVersion
	}
	// Fill planes on pre-route rows before any routeKey match. LoadUserCatalog
	// already does this, so a classic builtin looked like one row. The write
	// path did not: Save/Remove keyed on the file's empty plane, missed, and
	// appended a routed twin (or refused a qualified remove). Canonicalizing
	// here makes the first save upgrade the row in place.
	canonicalizeUserRoutes(cat.Models, BuiltinCatalog())
	return cat, nil
}

func writeCatalog(path string, cat UserCatalog) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create catalog dir: %w", err)
	}
	data, err := yaml.Marshal(cat)
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	return nil
}

// overlay replaces entries in base with matching entries from overlay (by
// provider+id) and appends any overlay entries that don't exist in base.
// Returns a new slice; inputs are not mutated.
//
// baseIsBuiltin says whether `base` is the BUILTIN catalog. It is the caller's
// answer to "does anything here own a fact the overlay cannot?", and it gates
// the fields the builtin owns (see mergeFields). There are exactly two overlays:
// the builtin under the user file (true), and the global user file under the
// per-vault one (false), where neither side is authoritative and the vault row
// keeps winning as it always has.
func overlay(base, top []ModelInfo, baseIsBuiltin bool) []ModelInfo {
	if len(top) == 0 {
		return base
	}
	// Keyed by ROUTE, not (provider, id): two rows for the same model on
	// different planes or in different regions are different endpoints with
	// independent entitlement, so overlaying one onto the other would merge
	// two unrelated verdicts into a single row.
	index := map[string]int{}
	for i, m := range base {
		index[routeKey(m.Route())] = i
	}
	out := make([]ModelInfo, len(base))
	copy(out, base)
	for _, m := range top {
		key := routeKey(m.Route())
		if i, ok := index[key]; ok {
			out[i] = mergeFields(out[i], m, baseIsBuiltin)
		} else {
			out = append(out, m)
			index[key] = len(out) - 1
		}
	}
	return out
}

func modelHasAnyPrice(m ModelInfo) bool {
	return m.PriceIn != 0 || m.PriceOut != 0 || m.PriceRequest != 0
}

func hasUserPriceOverride(m ModelInfo) bool {
	return m.PriceSource == "user" && (m.PriceOverride || modelHasAnyPrice(m))
}

// mergeFields copies fields from `top` onto `base`, returning the merged
// entry. Price fields are copied as a unit when `top.PriceSource` is set,
// so a user catalog entry with explicit price_in=0 (e.g. a free tier)
// correctly overrides a non-zero builtin price. Tier is monotonically
// elevated (verified beats user_verified beats unverified) so bundled
// prices can apply without demoting a user-verified entry.
//
// baseIsBuiltin marks the overlay of the user file onto the BUILTIN catalog,
// where three groups of fields belong to the builtin: the model facts (gated on
// the FactSource stamp below), ConfigHint, and Recommended. In the other
// overlay, global user file under per-vault user file, neither side owns
// anything, so the vault row wins on any value it sets, exactly as before.
func mergeFields(base, top ModelInfo, baseIsBuiltin bool) ModelInfo {
	out := base
	// Name, Dimensions and ContextLen are MODEL FACTS. Over the builtin catalog
	// a user row replaces them only when the user actually typed one, which
	// `models add --name/--dimensions/--context-length` records as FactSourceUser.
	// An unstamped copy is not authorship: every probe, promotion and benchmark
	// save used to seed its row from this very merged view, so the copy came
	// FROM the builtin and then outlived it. That is how a context_length of
	// 2048 survived the builtin's correction to 8192, kept `models list`
	// reporting the stale number, and (through inheritModelFacts) spread to
	// every per-region row discovery had just found.
	//
	// The stamp travels with the facts, never on its own, for the same reason
	// ThresholdSource does: it names who authored THESE values.
	if !baseIsBuiltin || top.FactSource == FactSourceUser {
		wrote := false
		if top.Name != "" {
			out.Name = top.Name
			wrote = true
		}
		if top.Dimensions != 0 {
			out.Dimensions = top.Dimensions
			wrote = true
		}
		if top.ContextLen != 0 {
			out.ContextLen = top.ContextLen
			wrote = true
		}
		if wrote {
			out.FactSource = top.FactSource
		}
	}
	// SupportedDimensions and Modalities stay builtin-only: no user-catalog
	// writer sets them, so there has never been anything to overlay.
	//
	// When the overlay declares a price source, treat prices as intentional
	// even if zero. Otherwise only non-zero overrides apply (protects builtin
	// prices from overlays that haven't populated them).
	if top.PriceSource == "user" {
		if hasUserPriceOverride(top) {
			out.PriceIn = top.PriceIn
			out.PriceOut = top.PriceOut
			out.PriceRequest = top.PriceRequest
			out.PriceSource = "user"
			out.PriceOverride = true
		}
	} else if top.PriceSource != "" {
		out.PriceIn = top.PriceIn
		out.PriceOut = top.PriceOut
		out.PriceRequest = top.PriceRequest
		out.PriceSource = top.PriceSource
		out.PriceOverride = top.PriceOverride
	} else {
		if top.PriceIn != 0 {
			out.PriceIn = top.PriceIn
		}
		if top.PriceOut != 0 {
			out.PriceOut = top.PriceOut
		}
		if top.PriceRequest != 0 {
			out.PriceRequest = top.PriceRequest
		}
	}
	// ConfigHint is GENERATED from (provider, type, id) by the builtin catalog.
	// No flag writes it, so a copy in a user file is only ever a snapshot of an
	// older builtin, and the builtin keeps it.
	if top.ConfigHint != "" && !baseIsBuiltin {
		out.ConfigHint = top.ConfigHint
	}
	if top.Notes != "" {
		out.Notes = top.Notes
	}
	if top.TestedAt != "" {
		out.TestedAt = top.TestedAt
		// Test result fields move as a unit with TestedAt: they describe the
		// same event. TestLatencyMs and TestError may legitimately be zero /
		// empty on a passing run, so overlay even zero values here.
		out.TestLatencyMs = top.TestLatencyMs
		out.TestError = top.TestError
		out.TestErrorCode = top.TestErrorCode
	}
	// RecommendedSimilarityThreshold: any positive overlay value wins. Zero
	// means "not set in the overlay" — preserve the builtin value. Users who
	// want to reset to the global default can set ai.similarity_threshold on
	// the vault config instead (explicit override beats catalog).
	//
	// ThresholdSource moves with the value, never on its own: it names who
	// authored THIS number, so carrying a stamp onto a different threshold
	// would credit the user with a value they never chose (and an unstamped
	// overlay must be able to clear a stamp the base carried).
	if top.RecommendedSimilarityThreshold > 0 {
		out.RecommendedSimilarityThreshold = top.RecommendedSimilarityThreshold
		out.ThresholdSource = top.ThresholdSource
	}
	if top.InvokeStrategy != "" {
		out.InvokeStrategy = top.InvokeStrategy
	}
	if top.Region != "" {
		out.Region = top.Region
	}
	if top.Endpoint != "" {
		out.Endpoint = top.Endpoint
	}
	if top.Benchmark != nil {
		b := *top.Benchmark
		out.Benchmark = &b
	}
	if top.Enabled != nil {
		e := *top.Enabled
		out.Enabled = &e
	}
	out.Tier = elevateTier(out.Tier, top.Tier)
	// Curation belongs to the builtin catalog, which is the only place a model
	// is recommended or demoted. `models verify` used to copy the merged row's
	// Recommended back into the user file, so the mirror could keep promoting a
	// model the catalog had since demoted, and no command could clear it.
	// Between the two user scopes the old add-only rule still applies.
	if top.Recommended && !baseIsBuiltin {
		out.Recommended = true
	}
	if top.Local {
		out.Local = true
	}
	return out
}

// elevateTier returns whichever tier represents more trust. Order:
// verified > user_verified > unverified > "".
func elevateTier(a, b ModelTier) ModelTier {
	if tierRank(b) > tierRank(a) {
		return b
	}
	return a
}

func tierRank(t ModelTier) int {
	switch t {
	case TierVerified:
		return 3
	case TierUserVerified:
		return 2
	case TierUnverified:
		return 1
	}
	return 0
}

func tagAsUserCatalog(m *ModelInfo) {
	if m.Tier == "" {
		m.Tier = TierUserVerified
	}
	switch {
	case m.PriceOverride:
		m.PriceSource = "user"
	case m.PriceSource == "user":
		if modelHasAnyPrice(*m) {
			m.PriceOverride = true
		} else {
			// Back-compat for buggy legacy entries written without explicit
			// price flags: zero-valued user prices should not mask vendor data.
			m.PriceSource = ""
		}
	case m.PriceSource == "" && modelHasAnyPrice(*m):
		m.PriceSource = "user"
		m.PriceOverride = true
	}
}

// UserThresholdRow finds the stored row that supplies the user's own similarity
// threshold for (provider, modelID), and says which scope holds it. It is the
// SINGLE lookup behind both the resolved value and the message that explains
// where the value came from, so the two can never disagree.
//
// It scans RAW rows per scope, vault before global, never the merged view.
// Merging is what made the two disagree: mergeFields lets any positive vault
// value overwrite the global one, so an unstamped vault MIRROR of the builtin
// (0.25 for Nova) replaced a stamped global calibration (0.30) and then failed
// IsUserThreshold, and the resolver fell through to the builtin while the user's
// real calibration sat in the global file, ignored. Provenance is a property of
// a stored row, so it has to be judged on stored rows.
//
// Per-scope precedence still matches the documented overlay: a qualifying vault
// row wins over a qualifying global one, because the vault scope is scanned
// first. Within a scope the first qualifying row wins, which is also what
// resolution does, so a model with several stored routes reports the row that is
// actually in force rather than refusing on ambiguity.
func UserThresholdRow(vaultRoot, provider, modelID string) (ModelInfo, UserCatalogScope, bool) {
	if provider == "" || modelID == "" {
		return ModelInfo{}, "", false
	}
	for _, scope := range []UserCatalogScope{ScopeVault, ScopeGlobal} {
		if scope == ScopeVault && vaultRoot == "" {
			continue
		}
		for _, m := range userCatalogRows(scope, vaultRoot) {
			if m.Type == "embedding" && m.Provider == provider && m.ID == modelID && IsUserThreshold(m) {
				return m, scope, true
			}
		}
	}
	return ModelInfo{}, "", false
}

// userCatalogRows returns one scope's stored rows verbatim: no builtin overlay,
// no cross-scope merge. Missing or unreadable files are an empty slice, matching
// every other read path, which never bricks the CLI on a bad catalog.
func userCatalogRows(scope UserCatalogScope, vaultRoot string) []ModelInfo {
	userCatalogMu.Lock()
	defer userCatalogMu.Unlock()

	path, err := catalogPathForScope(scope, vaultRoot)
	if err != nil {
		return nil
	}
	cat, err := readCatalogForWrite(path)
	if err != nil {
		return nil
	}
	return cat.Models
}
