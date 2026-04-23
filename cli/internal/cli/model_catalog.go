package cli

import (
	"context"

	"github.com/apresai/2ndbrain/internal/ai"
)

func loadVerifiedModelCatalog(ctx context.Context, cfg ai.AIConfig, vaultRoot string) ([]ai.ModelInfo, error) {
	merged, err := ai.BuildModelList(ctx, ai.MergedListOptions{
		Config:    cfg,
		VaultRoot: vaultRoot,
	})
	if err != nil {
		return nil, err
	}
	return merged.Verified, nil
}

func lookupModelInfo(models []ai.ModelInfo, provider, modelID string) (ai.ModelInfo, bool) {
	for _, m := range models {
		if m.Provider == provider && m.ID == modelID {
			return m, true
		}
	}
	return ai.ModelInfo{
		ID:       modelID,
		Provider: provider,
	}, false
}
