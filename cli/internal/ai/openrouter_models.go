package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type orModelsResponse struct {
	Data []orModelEntry `json:"data"`
}

type orModelEntry struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	ContextLength int        `json:"context_length"`
	Pricing       *orPricing `json:"pricing,omitempty"`
	Architecture  *orArch    `json:"architecture,omitempty"`
}

type orPricing struct {
	Prompt     string `json:"prompt"`     // per-token price as string
	Completion string `json:"completion"` // per-token price as string
}

type orArch struct {
	Modality     string `json:"modality"`      // e.g. "text->text"
	InputModality  string `json:"input_modality"`
	OutputModality string `json:"output_modality"`
}

// ListOpenRouterModels fetches available models from the OpenRouter API.
// baseURL can be overridden for testing; pass "" for the default.
func ListOpenRouterModels(ctx context.Context, apiKey, baseURL string) ([]ModelInfo, error) {
	if baseURL == "" {
		baseURL = openrouterBaseURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch openrouter models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter models: status %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp orModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, fmt.Errorf("unmarshal models response: %w", err)
	}

	var models []ModelInfo
	for _, m := range modelsResp.Data {
		mi := ModelInfo{
			ID:         m.ID,
			Name:       m.Name,
			Provider:   "openrouter",
			Type:       classifyOpenRouterModel(m),
			ContextLen: m.ContextLength,
			Local:      false,
		}
		if m.Pricing != nil {
			mi.PriceIn = parsePerMillionPrice(m.Pricing.Prompt)
			mi.PriceOut = parsePerMillionPrice(m.Pricing.Completion)
		}
		models = append(models, mi)
	}
	return models, nil
}

// classifyOpenRouterModel determines if a model is "embedding" or "generation".
func classifyOpenRouterModel(m orModelEntry) string {
	id := strings.ToLower(m.ID)
	if strings.Contains(id, "embed") {
		return "embedding"
	}
	return "generation"
}

// parsePerMillionPrice converts a per-token price string to per-million-token float.
func parsePerMillionPrice(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f * 1_000_000
}
