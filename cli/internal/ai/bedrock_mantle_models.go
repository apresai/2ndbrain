package ai

// Mantle-plane model enumeration. The mantle plane IS listable, despite the
// classic control plane (ListFoundationModels / GetFoundationModel) being
// blind to it: GET https://bedrock-mantle.<region>.api.aws/v1/models with
// bearer auth returns the region's catalog as an OpenAI-shaped
// {"data":[{"id":...},...]} envelope. Two facts verified live 2026-08-20:
//
//   - The listing path has NO /openai prefix. The responses call uses
//     /openai/v1/responses, the models listing uses /v1/models; the
//     /openai/v1/models form 404s.
//   - Per AWS docs (models-get-info), only the `id` field is reliable;
//     status/created/owned_by are not contractual, so the parser trusts
//     ids alone and ignores everything else.
//
// Listing proves existence, not entitlement: AWS's staged frontier rollout
// gates per account at invoke time, so a listed model can still probe
// access_denied. Only a real probe (`2nb models verify --discover`) answers
// "can THIS account use it".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// bedrockMantleRegions is the documented mantle-plane region set, verified
// live 2026-08-20 (/v1/models answered with 55/50/48 models in
// us-east-1/us-east-2/us-west-2). MAINTENANCE POINT: no API enumerates
// mantle regions, so a region AWS adds later is invisible to discovery
// until this list grows.
var bedrockMantleRegions = []string{"us-east-1", "us-east-2", "us-west-2"}

// mantleDiscoveryRegions orders the documented mantle region set
// primary-first when the resolved primary region is itself a mantle region.
// The discovery merge keeps the FIRST listing of each id and pins the row's
// Region to it, so primary-first means a model served in the user's primary
// region is pinned there rather than to whichever region happened to list
// it first.
func mantleDiscoveryRegions(cfg BedrockConfig) []string {
	primary := ResolveBedrockConfig(cfg).Region
	out := make([]string, 0, len(bedrockMantleRegions))
	for _, r := range bedrockMantleRegions {
		if r == primary {
			out = append(out, r)
		}
	}
	for _, r := range bedrockMantleRegions {
		if r != primary {
			out = append(out, r)
		}
	}
	return out
}

// parseMantleModelList extracts model ids from a /v1/models response body.
// Pure over bytes so tests exercise it on the captured live fixture. Only
// the id field is trusted (the one field AWS documents as reliable); empty
// ids are skipped and duplicates collapsed, preserving listing order.
func parseMantleModelList(body []byte) ([]string, error) {
	var doc struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal mantle model list: %w", err)
	}
	seen := make(map[string]bool, len(doc.Data))
	ids := make([]string, 0, len(doc.Data))
	for _, d := range doc.Data {
		id := strings.TrimSpace(d.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// mantleModelInfo builds the discovery row for one mantle-listed model id.
// The routing hints are the load-bearing part: discovered rows are not in
// any catalog, so without InvokeStrategy + Region on the candidate itself a
// probe would dispatch classic Converse against a plane that cannot see the
// model. The probe path reads these hints only when catalog resolution
// declares nothing (catalog wins), and a passing save persists them into
// the user catalog, after which the resolvers take over.
func mantleModelInfo(id, region string) ModelInfo {
	return ModelInfo{
		ID:             id,
		Name:           id,
		Provider:       "bedrock",
		Type:           "generation", // the mantle plane is generation-only in 2nb
		Tier:           TierUnverified,
		InvokeStrategy: StrategyBedrockMantleResponses,
		Region:         region,
		Notes:          "listed on the bedrock mantle plane (" + region + "); listing proves existence, not entitlement — verify with 2nb models verify --discover",
	}
}

// fetchMantleModelList GETs {baseURL}/v1/models with bearer auth, retrying
// HTTP 429 with the same backoff shape as doMantleRequest. Any other non-2xx
// becomes a *ProviderHTTPError via mantleHTTPError (model "": the failure is
// about the listing, not one model), so ClassifyProbeError routes 401/403
// without listing-specific rules.
func fetchMantleModelList(ctx context.Context, client *http.Client, baseURL, token string) ([]byte, error) {
	url := baseURL + "/v1/models"
	const maxRetries = 3
	for attempt := range maxRetries {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("bedrock mantle GET %s: %w", url, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, mantleMaxResponseBytes))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries-1 {
			delay := time.Duration(1<<attempt) * time.Second // 1s, 2s, 4s
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, mantleHTTPError("", url, resp.StatusCode, body)
		}
		return body, nil
	}
	// Unreachable: every iteration either returns or continues, and the final
	// attempt never takes the retry branch. Kept to satisfy the compiler.
	return nil, fmt.Errorf("bedrock mantle %s: retry loop exited unexpectedly", url)
}

// ListBedrockMantleModels enumerates one mantle region's models as
// hint-carrying unverified generation rows. Like the mantle generator it
// errors without a bearer token (the plane has no SigV4 fallback); the
// region must be a bare label so it cannot smuggle a host into the derived
// URL (same guard as mantleBaseURL).
func ListBedrockMantleModels(ctx context.Context, cfg BedrockConfig, region string) ([]ModelInfo, error) {
	token := resolveMantleBearerToken()
	if token == "" {
		return nil, errors.New(errNoMantleTokenText)
	}
	if !isBareRegionLabel(region) {
		return nil, fmt.Errorf("invalid mantle region %q: expected a bare region label like us-east-2", region)
	}
	baseURL := fmt.Sprintf("https://bedrock-mantle.%s.api.aws", region)
	// The listing is a fast GET; the shared attempt bound is only a hang cap.
	client := newProviderHTTPClient(MantleAttemptTimeout)
	body, err := fetchMantleModelList(ctx, client, baseURL, token)
	if err != nil {
		return nil, err
	}
	ids, err := parseMantleModelList(body)
	if err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		models = append(models, mantleModelInfo(id, region))
	}
	return models, nil
}

// ListBedrockMantleModelsCached is ListBedrockMantleModels behind the same
// 24h disk cache treatment as classic discovery (see discovery_cache.go):
// a fresh entry short-circuits the network, a stale entry is the fallback
// when the live listing fails.
func ListBedrockMantleModelsCached(ctx context.Context, cfg BedrockConfig, region string) ([]ModelInfo, error) {
	return listBedrockMantleModelsThroughCache(ctx, cfg, region)
}
