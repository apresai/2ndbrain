package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// modelSlotKeys maps each model config key to the plane/region keys that
// travel with it. `config set ai.<slot>_model <route>` writes all three
// together so a slot can never be left half-routed.
var modelSlotKeys = map[string]struct{ plane, region string }{
	"ai.generation_model": {"ai.generation_plane", "ai.generation_region"},
	"ai.embedding_model":  {"ai.embedding_plane", "ai.embedding_region"},
	"ai.rerank.model":     {"ai.rerank.plane", "ai.rerank.region"},
}

// parsePlaneValue validates a plane against the closed set. An empty value
// clears the pin, which is how a user unroutes a slot deliberately.
//
// The check is a hard error rather than a warning because plane selects which
// CLIENT is constructed: an unrecognized value has no dispatch path at all, so
// accepting it would defer a guaranteed failure to the next invocation.
func parsePlaneValue(value, slot string) (ai.Plane, error) {
	if value == "" {
		return "", nil
	}
	p := ai.Plane(value)
	if !ai.IsKnownPlane(p) {
		names := make([]string, 0, 2)
		for _, k := range ai.KnownPlanes() {
			names = append(names, string(k))
		}
		return "", fmt.Errorf("unknown plane %q (want one of: %s)", value, strings.Join(names, ", "))
	}
	// The mantle plane is generation-only in 2nb: it has no embedding or
	// rerank client, so pinning one there could never dispatch.
	if p == ai.PlaneMantle && slot != "generation" {
		return "", fmt.Errorf("the mantle plane is generation-only; %s cannot use it", slot)
	}
	return p, nil
}

// validateRegionValue rejects anything that is not a bare region label.
//
// This is a security control, not cosmetics: the region is interpolated into
// the mantle host (https://bedrock-mantle.<region>.api.aws), and the bearer
// token is sent to whatever that resolves to. The same guard runs in
// mantleBaseURL; enforcing it at write time means a bad value never reaches
// the file that a shared vault carries between machines.
func validateRegionValue(value string) error {
	if value == "" {
		return nil
	}
	if !ai.IsBareRegionLabel(value) {
		return fmt.Errorf("invalid region %q: expected a bare region label like us-west-2", value)
	}
	return nil
}

// applyModelSlotRoute handles `config set ai.<slot>_model <value>` where the
// value may be a full route reference.
//
// It returns handled=false for a key that is not a model slot, letting the
// caller fall through to the ordinary scalar path.
//
// The rules, in order:
//
//   - A route-qualified value (id@plane/region) is resolved and written to all
//     three keys.
//   - A bare id that matches exactly one route in the catalog is also written
//     to all three keys, so the common case stays a one-liner and the file
//     still ends up explicit.
//   - A bare id matching SEVERAL routes is REFUSED, printing every qualified
//     form. This is the one new refusal, and it is the point: picking one
//     silently is how a mantle-only model ended up dispatched over classic
//     Converse.
//   - A bare id matching NOTHING is accepted with its plane and region
//     cleared, preserving today's doctrine that naming an unknown model is
//     legitimate (a model can exist before 2nb's catalog knows it).
func applyModelSlotRoute(v *vault.Vault, key, value string) (bool, error) {
	slot, ok := modelSlotKeys[key]
	if !ok {
		return false, nil
	}
	_ = slot

	ref, err := ai.ParseRouteRef(value)
	if err != nil {
		return true, exitWithError(ExitValidation, "error: "+err.Error())
	}
	if ref.Provider == "" {
		ref.Provider = v.Config.AI.Provider
	}

	list, err := ai.BuildModelList(context.Background(), ai.MergedListOptions{
		Config:    v.Config.AI,
		VaultRoot: v.Root,
	})
	if err != nil {
		return true, err
	}
	rows := append(append([]ai.ModelInfo{}, list.Verified...), list.Unverified...)

	row, err := ai.ResolveRouteRef(rows, ref, v.Config.AI)
	switch {
	case err == nil:
		setModelSlot(&v.Config.AI, key, row.ID, row.Plane, row.Region)
		return true, nil
	case isNoSuchRoute(err):
		// Unknown model: keep the id, clear any stale route. `2nb models
		// discover` is how the route becomes known.
		setModelSlot(&v.Config.AI, key, ref.ID, ref.Plane, ref.Region)
		return true, nil
	default:
		return true, exitWithError(ExitValidation,
			"error: "+err.Error()+"\n(nothing was changed)")
	}
}

func isNoSuchRoute(err error) bool {
	var missing *ai.NoSuchRouteError
	return errors.As(err, &missing)
}

// setModelSlot writes a slot's model, plane, and region as one unit. Writing
// them together is what keeps "nothing is inferred at invoke time" true: a
// slot is never left naming a model without naming the endpoint it runs on.
func setModelSlot(cfg *ai.AIConfig, key, id string, plane ai.Plane, region string) {
	switch key {
	case "ai.generation_model":
		cfg.GenerationModel, cfg.GenerationPlane, cfg.GenerationRegion = id, plane, region
	case "ai.embedding_model":
		cfg.EmbeddingModel, cfg.EmbeddingPlane, cfg.EmbeddingRegion = id, plane, region
	case "ai.rerank.model":
		cfg.Rerank.Model, cfg.Rerank.Plane, cfg.Rerank.Region = id, plane, region
	}
}
