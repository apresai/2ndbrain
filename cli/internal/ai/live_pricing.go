package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pricingCacheTTL     = 24 * time.Hour
	openRouterModelsURL = openrouterBaseURL + "/models"
	awsOfferBaseURL     = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/"
	bedrockOfferCode    = "AmazonBedrock"
	bedrockFMOfferCode  = "AmazonBedrockFoundationModels"
)

type modelPrice struct {
	PriceIn      float64
	PriceOut     float64
	PriceRequest float64
	HasIn        bool
	HasOut       bool
	HasRequest   bool
	Source       string
	inRank       int
	outRank      int
	requestRank  int
}

type bedrockPriceKind uint8

const (
	bedrockPriceUnknown bedrockPriceKind = iota
	bedrockPriceInput
	bedrockPriceOutput
	bedrockPriceRequest
)

type bedrockPriceRouting uint8

const (
	bedrockRoutingUnknown bedrockPriceRouting = iota
	bedrockRoutingRegional
	bedrockRoutingGlobal
)

type bedrockPriceTier uint8

const (
	bedrockTierUnknown bedrockPriceTier = iota
	bedrockTierStandard
	bedrockTierFlex
	bedrockTierPriority
	bedrockTierBatch
	bedrockTierReserved
)

type bedrockPriceCandidate struct {
	Kind    bedrockPriceKind
	Price   float64
	Routing bedrockPriceRouting
	Tier    bedrockPriceTier
}

type providerPricing struct {
	ready bool
	exact map[string]modelPrice
	alias map[string]modelPrice
}

type awsOfferFile struct {
	Products map[string]awsOfferProduct `json:"products"`
	Terms    struct {
		OnDemand map[string]map[string]awsOfferTerm `json:"OnDemand"`
	} `json:"terms"`
}

type awsOfferProduct struct {
	Attributes map[string]string `json:"attributes"`
}

type awsOfferTerm struct {
	PriceDimensions map[string]awsOfferDimension `json:"priceDimensions"`
}

type awsOfferDimension struct {
	Description  string            `json:"description"`
	Unit         string            `json:"unit"`
	PricePerUnit map[string]string `json:"pricePerUnit"`
}

var livePricingCacheState struct {
	mu         sync.Mutex
	openrouter *providerPricing
	bedrock    map[string]*providerPricing
}

// pricingHTTPClient has a fixed timeout so pricing fetches never hang indefinitely.
var pricingHTTPClient = &http.Client{Timeout: 15 * time.Second}

// EnrichModelPricing overlays live pricing metadata on top of catalog entries.
// User-specified prices win over live vendor data.
func EnrichModelPricing(ctx context.Context, cfg AIConfig, models []ModelInfo) []ModelInfo {
	orPricing := loadOpenRouterPricing(ctx)
	brPricing := loadBedrockPricing(ctx, cfg.Bedrock.Region)
	return enrichModels(models, orPricing, brPricing)
}

// enrichModels applies orPricing and brPricing to models without making any
// HTTP calls, making it directly testable.
func enrichModels(models []ModelInfo, orPricing, brPricing *providerPricing) []ModelInfo {
	out := make([]ModelInfo, len(models))
	copy(out, models)

	// A live no-match deliberately clears NOTHING: a stale but labeled
	// catalog price (price_source records its provenance) beats "unknown"
	// for cost previews, and the alias-matching that feeds bedrock lookups
	// is heuristic enough that clearing on no-match silently blanked real
	// prices (the pre-0.14 behavior this replaced). Delisted-model staleness
	// is the accepted trade-off.
	for i := range out {
		if hasUserPriceOverride(out[i]) {
			continue
		}
		switch out[i].Provider {
		case "openrouter":
			if price, ok := orPricing.exact[out[i].ID]; ok {
				applyModelPrice(&out[i], price)
			}
		case "bedrock":
			if price, ok := bedrockPriceForModel(brPricing, out[i].ID); ok {
				applyModelPrice(&out[i], price)
			}
		}
	}

	return out
}

func loadOpenRouterPricing(ctx context.Context) *providerPricing {
	livePricingCacheState.mu.Lock()
	if livePricingCacheState.openrouter != nil {
		p := livePricingCacheState.openrouter
		livePricingCacheState.mu.Unlock()
		return p
	}
	livePricingCacheState.mu.Unlock()

	pricing := &providerPricing{exact: map[string]modelPrice{}}
	body, err := loadCachedHTTPBody(ctx, openRouterModelsURL, "openrouter-models.json")
	if err != nil {
		slog.Debug("openrouter pricing unavailable", "err", err)
	} else {
		var resp orModelsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			slog.Debug("openrouter pricing decode failed", "err", err)
		} else {
			pricing.ready = true
			for _, m := range resp.Data {
				if m.Pricing == nil {
					continue
				}
				price := modelPrice{Source: "vendor"}
				if m.Pricing.Prompt != "" {
					price.PriceIn = parsePerMillionPrice(m.Pricing.Prompt)
					price.HasIn = true
				}
				if m.Pricing.Completion != "" {
					price.PriceOut = parsePerMillionPrice(m.Pricing.Completion)
					price.HasOut = true
				}
				if m.Pricing.Request != "" {
					price.PriceRequest = parseUnitPrice(m.Pricing.Request)
					price.HasRequest = true
				}
				pricing.exact[m.ID] = price
			}
		}
	}

	if pricing.ready {
		livePricingCacheState.mu.Lock()
		if livePricingCacheState.openrouter == nil {
			livePricingCacheState.openrouter = pricing
		} else {
			pricing = livePricingCacheState.openrouter
		}
		livePricingCacheState.mu.Unlock()
	}
	return pricing
}

func loadBedrockPricing(ctx context.Context, region string) *providerPricing {
	if region == "" {
		region = "us-east-1"
	}

	livePricingCacheState.mu.Lock()
	if livePricingCacheState.bedrock == nil {
		livePricingCacheState.bedrock = map[string]*providerPricing{}
	}
	if pricing, ok := livePricingCacheState.bedrock[region]; ok {
		livePricingCacheState.mu.Unlock()
		return pricing
	}
	livePricingCacheState.mu.Unlock()

	// Fetch the two AWS pricing offer files concurrently.
	type fetchResult struct {
		body []byte
		err  error
	}
	chA := make(chan fetchResult, 1)
	chB := make(chan fetchResult, 1)
	go func() {
		body, err := loadCachedHTTPBody(ctx, awsOfferBaseURL+bedrockOfferCode+"/current/index.json", fmt.Sprintf("bedrock-%s.json", strings.ToLower(region)))
		chA <- fetchResult{body, err}
	}()
	go func() {
		body, err := loadCachedHTTPBody(ctx, awsOfferBaseURL+bedrockFMOfferCode+"/current/index.json", fmt.Sprintf("bedrock-foundation-models-%s.json", strings.ToLower(region)))
		chB <- fetchResult{body, err}
	}()
	resA, resB := <-chA, <-chB

	pricing := &providerPricing{alias: map[string]modelPrice{}}
	if resA.err != nil {
		slog.Debug("bedrock pricing offer unavailable", "offer", bedrockOfferCode, "err", resA.err)
	}
	if resB.err != nil {
		slog.Debug("bedrock pricing offer unavailable", "offer", bedrockFMOfferCode, "err", resB.err)
	}
	if resA.err == nil && resB.err == nil {
		var genericOffer, fmOffer awsOfferFile
		if err := json.Unmarshal(resA.body, &genericOffer); err != nil {
			slog.Debug("bedrock pricing decode failed", "offer", bedrockOfferCode, "err", err)
		} else if err := json.Unmarshal(resB.body, &fmOffer); err != nil {
			slog.Debug("bedrock pricing decode failed", "offer", bedrockFMOfferCode, "err", err)
		} else {
			addBedrockOfferPricing(pricing.alias, genericOffer, region)
			addBedrockOfferPricing(pricing.alias, fmOffer, region)
			pricing.ready = true
		}
	}

	if pricing.ready {
		livePricingCacheState.mu.Lock()
		if _, ok := livePricingCacheState.bedrock[region]; !ok {
			livePricingCacheState.bedrock[region] = pricing
		} else {
			pricing = livePricingCacheState.bedrock[region]
		}
		livePricingCacheState.mu.Unlock()
	}
	return pricing
}

func addBedrockOfferPricing(dst map[string]modelPrice, offer awsOfferFile, region string) {
	for sku, product := range offer.Products {
		attrs := product.Attributes
		if attrs["regionCode"] != region {
			continue
		}
		terms := offer.Terms.OnDemand[sku]
		if len(terms) == 0 {
			continue
		}
		aliases := bedrockOfferAliases(attrs)
		if len(aliases) == 0 {
			continue
		}
		for _, term := range terms {
			for _, dim := range term.PriceDimensions {
				candidate, ok := classifyBedrockPriceDimension(attrs, dim)
				if !ok {
					continue
				}
				for _, alias := range aliases {
					key := normalizePriceAlias(alias)
					if key == "" {
						continue
					}
					current := dst[key]
					applyBedrockCandidate(&current, candidate)
					if current.HasIn || current.HasOut || current.HasRequest || current.Source != "" {
						dst[key] = current
					}
				}
			}
		}
	}
}

func bedrockOfferAliases(attrs map[string]string) []string {
	seen := map[string]bool{}
	var aliases []string
	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		aliases = append(aliases, v)
	}

	if v := attrs["model"]; v != "" {
		add(v)
	}
	if v := attrs["titanModel"]; v != "" {
		add(v)
	}
	// AmazonBedrockFoundationModels (Marketplace) offer: model attr is absent;
	// the model name lives in servicename, e.g. "Claude Sonnet 4 (Amazon Bedrock Edition)".
	if v := attrs["servicename"]; v != "" && v != "Amazon Bedrock" && v != "Amazon Bedrock Service" {
		stripped := strings.TrimSuffix(v, " (Amazon Bedrock Edition)")
		add(stripped)
		// Also emit a provider-prefixed alias so bedrockModelAliases lookups match.
		if p := attrs["provider"]; p != "" {
			add(p + " " + stripped)
		}
	}
	if v := attrs["provider"]; v != "" && attrs["model"] != "" {
		add(v + " " + attrs["model"])
	}
	for _, alias := range bedrockUsagetypeAliases(attrs["usagetype"]) {
		add(alias)
	}
	if strings.Contains(attrs["usagetype"], "NovaMultiModalEmbeddings") {
		add("Nova MultiModal Embeddings")
	}

	return aliases
}

func bedrockUsagetypeAliases(usagetype string) []string {
	if usagetype == "" || isMPUsagetype(usagetype) {
		return nil
	}
	dash := strings.Index(usagetype, "-")
	if dash < 0 || dash == len(usagetype)-1 {
		return nil
	}
	raw := usagetype[dash+1:]
	lower := strings.ToLower(raw)
	end := len(raw)
	for _, marker := range []string{
		"-input-tokens",
		"-output-tokens",
		"-input-token",
		"-output-token",
		"-cache-",
		"-customization",
		"-provisionedthroughput",
		"-rft-",
		"-nova-grounding",
	} {
		if i := strings.Index(lower, marker); i >= 0 && i < end {
			end = i
		}
	}
	if end == len(raw) {
		return nil
	}

	model := strings.Trim(raw[:end], "-_ ")
	if model == "" {
		return nil
	}

	aliases := []string{model}
	if trimmed := strings.TrimSuffix(model, "-mantle"); trimmed != model {
		aliases = append(aliases, trimmed)
	}
	return aliases
}

func classifyBedrockPriceDimension(attrs map[string]string, dim awsOfferDimension) (bedrockPriceCandidate, bool) {
	usagetype := attrs["usagetype"]
	rawLower := strings.ToLower(usagetype)
	text := normalizePricingText(strings.Join([]string{
		usagetype,
		attrs["inferenceType"],
		attrs["feature"],
		attrs["service_tier"],
		attrs["batch"],
		attrs["modality"],
		dim.Description,
		dim.Unit,
	}, " "))

	for _, unsupported := range []string{
		"cache",
		"custom model",
		"grounding",
		"image",
		"latency optimized",
		"long context",
		" lctx ",
		"provisioned",
		"rerank",
		"second",
		"video",
		"audio",
	} {
		if strings.Contains(text, unsupported) {
			return bedrockPriceCandidate{}, false
		}
	}

	price := parseUnitPrice(dim.PricePerUnit["USD"])
	if price == 0 && dim.PricePerUnit["USD"] == "" {
		return bedrockPriceCandidate{}, false
	}

	tier := bedrockTierStandard
	switch {
	case strings.Contains(text, "batch"):
		tier = bedrockTierBatch
	case strings.Contains(text, "priority"):
		tier = bedrockTierPriority
	case strings.Contains(text, "flex"):
		tier = bedrockTierFlex
	case strings.Contains(text, "reserved"):
		tier = bedrockTierReserved
	}

	routing := bedrockRoutingRegional
	switch {
	case strings.Contains(text, "global"), strings.Contains(text, "cross region"):
		routing = bedrockRoutingGlobal
	case strings.Contains(text, "regional"), strings.Contains(text, " geo "):
		routing = bedrockRoutingRegional
	}

	kind := bedrockPriceUnknown
	// Marketplace offer files use both legacy CamelCase and newer snake_case
	// token identifiers. Check the raw usagetype before the normalized text.
	if isMPUsagetype(usagetype) {
		switch {
		case strings.Contains(usagetype, "InputTokenCount"), strings.Contains(rawLower, "input_tokens"):
			kind = bedrockPriceInput
		case strings.Contains(usagetype, "OutputTokenCount"), strings.Contains(rawLower, "output_tokens"):
			kind = bedrockPriceOutput
		case strings.Contains(rawLower, "request_count"):
			kind = bedrockPriceRequest
		}
	}

	if kind == bedrockPriceUnknown {
		switch {
		case strings.Contains(text, "request count"):
			kind = bedrockPriceRequest
		case strings.Contains(text, "response token"), strings.Contains(text, "output token"):
			kind = bedrockPriceOutput
		case strings.Contains(text, "input token"):
			kind = bedrockPriceInput
		}
	}
	if kind == bedrockPriceUnknown {
		return bedrockPriceCandidate{}, false
	}

	if kind == bedrockPriceRequest {
		return bedrockPriceCandidate{Kind: kind, Price: price, Routing: routing, Tier: tier}, true
	}
	return bedrockPriceCandidate{
		Kind:    kind,
		Price:   normalizeBedrockTokenPrice(price, dim),
		Routing: routing,
		Tier:    tier,
	}, true
}

// isMPUsagetype reports whether a usagetype belongs to the Bedrock Marketplace
// (AmazonBedrockFoundationModels) offer, identified by the "MP:" infix.
func isMPUsagetype(usagetype string) bool {
	return strings.Contains(usagetype, "-MP:")
}

func normalizePricingText(s string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func bedrockCandidateRank(candidate bedrockPriceCandidate) int {
	if candidate.Tier != bedrockTierStandard {
		return 0
	}
	switch candidate.Routing {
	case bedrockRoutingGlobal:
		return 2
	case bedrockRoutingRegional, bedrockRoutingUnknown:
		return 1
	default:
		return 0
	}
}

func applyBedrockCandidate(dst *modelPrice, candidate bedrockPriceCandidate) {
	rank := bedrockCandidateRank(candidate)
	if rank == 0 {
		return
	}

	dst.Source = "vendor"
	switch candidate.Kind {
	case bedrockPriceInput:
		if !dst.HasIn || rank > dst.inRank || (rank == dst.inRank && candidate.Price < dst.PriceIn) {
			dst.PriceIn = candidate.Price
			dst.HasIn = true
			dst.inRank = rank
		}
	case bedrockPriceOutput:
		if !dst.HasOut || rank > dst.outRank || (rank == dst.outRank && candidate.Price < dst.PriceOut) {
			dst.PriceOut = candidate.Price
			dst.HasOut = true
			dst.outRank = rank
		}
	case bedrockPriceRequest:
		if !dst.HasRequest || rank > dst.requestRank || (rank == dst.requestRank && candidate.Price < dst.PriceRequest) {
			dst.PriceRequest = candidate.Price
			dst.HasRequest = true
			dst.requestRank = rank
		}
	}
}

func normalizeBedrockTokenPrice(price float64, dim awsOfferDimension) float64 {
	switch strings.ToLower(dim.Unit) {
	case "1k tokens":
		// Price is per-1K tokens; convert to per-1M.
		return price * 1000
	case "units":
		// AmazonBedrockFoundationModels Marketplace offer: price is already per-1M tokens.
		return price
	default:
		return price
	}
}

func bedrockPriceForModel(pricing *providerPricing, modelID string) (modelPrice, bool) {
	if pricing == nil {
		return modelPrice{}, false
	}
	for _, alias := range bedrockModelAliases(modelID) {
		if price, ok := pricing.alias[normalizePriceAlias(alias)]; ok {
			return price, true
		}
	}
	return modelPrice{}, false
}

func bedrockModelAliases(modelID string) []string {
	base := strings.ToLower(inferenceProfileBaseID(modelID))
	core := base
	if i := strings.Index(core, ":"); i >= 0 {
		core = core[:i]
	}
	providerless := core
	if i := strings.Index(providerless, "."); i >= 0 {
		providerless = providerless[i+1:]
	}
	providerless = trimBedrockAlias(providerless)

	seen := map[string]bool{}
	var aliases []string
	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		aliases = append(aliases, v)
	}

	add(base)
	add(core)
	add(providerless)
	add(strings.TrimSuffix(providerless, "-instruct"))
	add(strings.TrimSuffix(providerless, "-chat"))
	add(strings.TrimSuffix(providerless, "-text"))
	add(strings.TrimSuffix(providerless, "-it"))
	add(strings.TrimSuffix(providerless, "-pt"))
	if strings.HasPrefix(providerless, "nova-2-") {
		add(strings.Replace(providerless, "nova-2-", "nova-2.0-", 1))
	}
	if trimmed := trimTrailingNumericAlias(providerless); trimmed != providerless {
		add(trimmed)
	}

	switch {
	case strings.HasPrefix(base, "amazon.nova-2-multimodal-embeddings"):
		add("Nova MultiModal Embeddings")
	case strings.HasPrefix(base, "amazon.titan-embed-text-v2"):
		add("TitanEmbeddingsV2-Text-input")
		add("Titan Embeddings V2 Text")
	case strings.HasPrefix(base, "amazon.titan-embed-text-v1"), strings.HasPrefix(base, "amazon.titan-embed-g1-"):
		add("Titan Embeddings G1 Text")
	case strings.HasPrefix(base, "cohere.embed-english-v3"):
		add("Cohere Embed 3 Model - English")
		add("Cohere Embed Model 3 English")
	case strings.HasPrefix(base, "cohere.embed-multilingual-v3"):
		add("Cohere Embed Model 3 - Multilingual")
		add("Cohere Embed 3 Model Multilingual")
	case strings.HasPrefix(base, "cohere.embed-v4"):
		add("Cohere Embed 4 Model")
	case strings.Contains(base, "marengo-embed-2-7"):
		add("TwelveLabs Marengo Embed 2.7")
	case strings.Contains(base, "marengo-embed-3-0"):
		add("TwelveLabs Marengo Embed 3.0")
	}

	return aliases
}

func trimBedrockAlias(s string) string {
	parts := strings.Split(s, "-")
	if len(parts) == 0 {
		return s
	}
	last := len(parts) - 1
	if strings.HasPrefix(parts[last], "v") && isAllDigits(parts[last][1:]) && (parts[last] == "v0" || parts[last] == "v1") {
		parts = parts[:last]
		last--
	}
	if last >= 0 && len(parts[last]) == 8 && isAllDigits(parts[last]) {
		parts = parts[:last]
	}
	return strings.Join(parts, "-")
}

func trimTrailingNumericAlias(s string) string {
	parts := strings.Split(s, "-")
	if len(parts) < 2 {
		return s
	}
	last := parts[len(parts)-1]
	if len(last) == 4 && isAllDigits(last) {
		return strings.Join(parts[:len(parts)-1], "-")
	}
	return s
}

func normalizePriceAlias(s string) string {
	s = strings.ToLower(strings.TrimSuffix(s, " (amazon bedrock edition)"))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func applyModelPrice(m *ModelInfo, price modelPrice) {
	m.PriceIn = 0
	m.PriceOut = 0
	m.PriceRequest = 0
	if price.HasIn {
		m.PriceIn = price.PriceIn
	}
	if price.HasOut {
		m.PriceOut = price.PriceOut
	}
	if price.HasRequest {
		m.PriceRequest = price.PriceRequest
	}
	m.PriceSource = price.Source
}

func loadCachedHTTPBody(ctx context.Context, url, cacheName string) ([]byte, error) {
	path, err := pricingCachePath(cacheName)
	if err != nil {
		return nil, err
	}

	if data, ok := readPricingCache(path, true); ok {
		return data, nil
	}

	// A recent FAILURE is remembered as well as a success, and this is the whole
	// reason the cooldown exists. Only successes were cached before, so every
	// caller re-paid the entire network stall: the AmazonBedrock offer file is
	// 16MB against pricingHTTPClient's 15s deadline, so on any link slower than
	// roughly 1MB/s to that endpoint the fetch CANNOT finish, and `models list`
	// blocked about 30s (two offers) on every single invocation rather than
	// once. In one test binary that turned one stall into twelve and pushed the
	// package past Go's 10m timeout.
	//
	// It is a cooldown and not a permanent negative entry because a long-lived
	// process (the MCP server) has to pick pricing up again once the network
	// recovers, and because the stamp is on DISK the next CLI invocation is
	// covered too, which an in-memory one would miss entirely.
	// Two layers, because they cover different things and neither is enough
	// alone: the on-disk stamp carries a failure to the NEXT process, which is
	// what the CLI needs since every invocation is a fresh one, and the
	// in-memory map covers repeated calls inside one process, which is what a
	// long-lived server and a test binary need. BOTH are keyed by URL, never by
	// cache path: the offer URLs carry no region while their cache filenames do,
	// so a path-keyed stamp would let a second configured region re-pay the full
	// stall against a URL the first had already proved was failing.
	if until, cooling := pricingFetchCooling(url); cooling {
		if data, ok := readPricingCache(path, false); ok {
			slog.Debug("using stale pricing cache during fetch cooldown", "path", path, "until", until)
			return data, nil
		}
		return nil, fmt.Errorf("pricing fetch for %s failed recently, not retrying until %s", cacheName, until.Format(time.RFC3339))
	}

	data, err := fetchHTTPBody(ctx, url)
	if err == nil {
		if writeErr := writePricingCache(path, data); writeErr != nil {
			slog.Debug("pricing cache write failed", "path", path, "err", writeErr)
		}
		clearPricingFetchFailure(url)
		return data, nil
	}
	notePricingFetchFailure(url)

	if data, ok := readPricingCache(path, false); ok {
		slog.Debug("using stale pricing cache", "path", path, "err", err)
		return data, nil
	}
	return nil, err
}

// pricingFetchCooldown is how long a failed pricing fetch suppresses another
// attempt for the same offer file. Long enough that a command run in a loop
// does not re-stall, short enough that a recovered network is picked up without
// the user doing anything.
var pricingFetchCooldown = 10 * time.Minute

// pricingFailurePath is the stamp for one offer URL, inside the pricing cache
// directory. It is named from the URL and NOT from the cache entry's filename:
// the two Bedrock offer URLs carry no region while their cache filenames do
// (`bedrock-us-east-1.json` and `bedrock-us-west-2.json` are the same bytes), so
// a stamp named after the entry would be keyed on a dimension the URL does not
// have, and a second configured region would re-pay the full stall against a URL
// the first had already proved was failing.
//
// The empty string means the cache directory could not be resolved, in which
// case there is nowhere to keep a stamp and only the in-memory layer applies.
func pricingFailurePath(url string) string {
	dir, err := pricingCacheDir()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".failed")
}

// pricingFetchFailures is the in-memory half, keyed by URL like the stamp.
var pricingFetchFailures struct {
	mu sync.Mutex
	at map[string]time.Time
}

// pricingFetchCooling reports whether the last attempt for this offer failed
// recently enough that retrying would just repeat the stall, and when that stops
// being true. The LATER of the two deadlines wins, so neither layer can shorten
// the other's cooldown: a fresh process reads only the disk stamp, and a process
// that has already failed in memory must not have that shortened by an older
// stamp left by an earlier run.
func pricingFetchCooling(url string) (time.Time, bool) {
	var until time.Time

	pricingFetchFailures.mu.Lock()
	if at, ok := pricingFetchFailures.at[url]; ok {
		until = at.Add(pricingFetchCooldown)
	}
	pricingFetchFailures.mu.Unlock()

	if stamp := pricingFailurePath(url); stamp != "" {
		if info, err := os.Stat(stamp); err == nil {
			if disk := info.ModTime().Add(pricingFetchCooldown); disk.After(until) {
				until = disk
			}
		}
	}

	if until.IsZero() {
		return time.Time{}, false
	}
	return until, time.Now().Before(until)
}

func notePricingFetchFailure(url string) {
	// In memory FIRST, so a cache directory that cannot be written (read-only,
	// or unresolvable) still gets protection for the rest of this process.
	pricingFetchFailures.mu.Lock()
	if pricingFetchFailures.at == nil {
		pricingFetchFailures.at = map[string]time.Time{}
	}
	pricingFetchFailures.at[url] = time.Now()
	pricingFetchFailures.mu.Unlock()

	stamp := pricingFailurePath(url)
	if stamp == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(stamp), 0o755); err != nil {
		slog.Debug("pricing failure stamp not written", "path", stamp, "err", err)
		return
	}
	// The mtime carries the time; the body is only for a human who finds it.
	if err := os.WriteFile(stamp, []byte("the last fetch for this pricing offer failed\n"), 0o644); err != nil {
		slog.Debug("pricing failure stamp not written", "path", stamp, "err", err)
	}
}

func clearPricingFetchFailure(url string) {
	pricingFetchFailures.mu.Lock()
	delete(pricingFetchFailures.at, url)
	pricingFetchFailures.mu.Unlock()

	stamp := pricingFailurePath(url)
	if stamp == "" {
		return
	}
	if err := os.Remove(stamp); err != nil && !os.IsNotExist(err) {
		slog.Debug("pricing failure stamp not cleared", "path", stamp, "err", err)
	}
}

func fetchHTTPBody(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := pricingHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("GET %s: status %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func pricingCachePath(name string) (string, error) {
	dir, err := pricingCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func pricingCacheDir() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "2nb", "pricing"), nil
	}
	dir, err := os.UserCacheDir()
	if err == nil && dir != "" {
		return filepath.Join(dir, "2nb", "pricing"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "2nb", "pricing"), nil
}

func readPricingCache(path string, freshOnly bool) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if freshOnly && time.Since(info.ModTime()) > pricingCacheTTL {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func writePricingCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseUnitPrice(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
