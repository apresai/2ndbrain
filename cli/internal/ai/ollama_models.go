package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ollamaTagsResponse struct {
	Models []OllamaModelEntry `json:"models"`
}

// OllamaModelEntry represents a model from Ollama's /api/tags endpoint.
type OllamaModelEntry struct {
	Name    string            `json:"name"`
	Model   string            `json:"model"`
	Size    int64             `json:"size"`
	Details OllamaModelDetail `json:"details"`
}

// OllamaModelDetail holds model metadata from Ollama.
type OllamaModelDetail struct {
	Family            string `json:"family"`
	ParameterSize     string `json:"parameter_size"`
	QuantizationLevel string `json:"quantization_level"`
}

// knownEmbeddingDims maps known embedding model prefixes to their dimensions.
var knownEmbeddingDims = map[string]int{
	"embeddinggemma":     768,
	"nomic-embed-text":   768,
	"mxbai-embed-large":  1024,
	"snowflake-arctic":   1024,
	"all-minilm":         384,
	"bge-large":          1024,
	"bge-m3":             1024,
}

// ListOllamaModelEntries fetches installed models from Ollama, preserving full metadata.
func ListOllamaModelEntries(ctx context.Context, client *http.Client, endpoint string) (map[string]OllamaModelEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("create tags request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch ollama models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read tags response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags: status %d: %s", resp.StatusCode, string(body))
	}

	var tagsResp ollamaTagsResponse
	if err := json.Unmarshal(body, &tagsResp); err != nil {
		return nil, fmt.Errorf("unmarshal tags response: %w", err)
	}

	result := make(map[string]OllamaModelEntry, len(tagsResp.Models))
	for _, m := range tagsResp.Models {
		result[m.Name] = m
	}
	return result, nil
}

// ListOllamaModels fetches installed models from Ollama's /api/tags endpoint.
func ListOllamaModels(ctx context.Context, client *http.Client, endpoint string) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("create tags request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch ollama models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read tags response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags: status %d: %s", resp.StatusCode, string(body))
	}

	var tagsResp ollamaTagsResponse
	if err := json.Unmarshal(body, &tagsResp); err != nil {
		return nil, fmt.Errorf("unmarshal tags response: %w", err)
	}

	var models []ModelInfo
	for _, m := range tagsResp.Models {
		mi := ModelInfo{
			ID:       m.Name,
			Name:     m.Name,
			Provider: "ollama",
			Type:     classifyOllamaModel(m.Name),
			Local:    true,
			PriceIn:  0,
			PriceOut: 0,
		}
		if mi.Type == "embedding" {
			mi.Dimensions = lookupEmbeddingDims(m.Name)
		}
		models = append(models, mi)
	}
	return models, nil
}

// classifyOllamaModel determines if a model is "embedding" or "generation" by name.
func classifyOllamaModel(name string) string {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "embed") {
		return "embedding"
	}
	// Check known embedding model prefixes
	for prefix := range knownEmbeddingDims {
		if strings.HasPrefix(lower, prefix) {
			return "embedding"
		}
	}
	return "generation"
}

// lookupEmbeddingDims returns known dimensions for an embedding model, or 0 if unknown.
func lookupEmbeddingDims(name string) int {
	lower := strings.ToLower(name)
	// Strip tag (e.g. ":latest" or ":308m")
	if idx := strings.Index(lower, ":"); idx > 0 {
		lower = lower[:idx]
	}
	for prefix, dims := range knownEmbeddingDims {
		if strings.HasPrefix(lower, prefix) {
			return dims
		}
	}
	return 0
}
