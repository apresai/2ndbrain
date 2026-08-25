# AI Providers Reference

The canonical reference for 2nb's AI provider layer: the default Bedrock setup, authentication and token precedence, the Bedrock mantle plane, the opt-in providers, embedding request shapes, live pricing, invoke strategies, the classic-plane compatibility gate, the retrieval-quality probe, the cost estimator, and the builtin model catalog. CLAUDE.md points here; this file is the single home for these facts.

## Defaults

The default provider is **AWS Bedrock** (via your AWS credentials): generation is Claude Haiku 4.5 (`us.anthropic.claude-haiku-4-5-20251001-v1:0`), embeddings are Amazon Nova-2 (`amazon.nova-2-multimodal-embeddings-v1:0`, 1024 dims). Defaults live in `DefaultAIConfig()` (`cli/internal/ai/config.go`).

## Bedrock authentication

Bedrock auth uses a Bedrock **API key (bearer token)** or the AWS SDK credential chain (SigV4 from env or `~/.aws`).

Token precedence, highest first:

1. `AWS_BEARER_TOKEN_BEDROCK` (environment variable)
2. `~/.config/2nb/bedrock.json` (XDG-aware; written by dashboard Settings or `2nb config bedrock --set`)
3. macOS Keychain (`2nb config set-key bedrock`)
4. SigV4 (the AWS SDK credential chain)

**`prefer_stored_token: true` in bedrock.json inverts that for 2nb only** (set via `2nb config bedrock --set --prefer-stored-token`, or the Settings AI checkbox): the stored key (file, then Keychain) wins over the env var, which keeps serving every other tool in the shell. This works via `ResolveBedrockToken` plus an in-process env overwrite in the hydrate shim, so the classic SDK path and the mantle plane both follow it. With no stored key the env var still applies (the flag never bricks a working setup). The internal `2NB_BEDROCK_IGNORE_PREFER_STORED=1` escape hatch restores env-first for one process: the app's verify-before-accept sets it so a CANDIDATE key probed via the env var is actually the key being tested.

`ensureBedrockBearerToken` (`cli/internal/ai/bedrock.go`) exports a stored token into the env var the AWS SDK reads. The SDK **prefers a bearer token over SigV4**, so a stored key overrides `~/.aws` for Bedrock. File `region`, when set, overlays vault `ai.bedrock.region` via `ResolveBedrockConfig`. The token is never written into vault `config.yaml`. A world-readable `bedrock.json` is refused. This mechanism is how the macOS app reaches Bedrock without your shell's credentials.

## Bedrock mantle plane (partner-hosted frontier models)

Two builtin, non-curated generation entries, `openai.gpt-5.5` (region us-east-2) and `xai.grok-4.3` (region us-west-2), run on AWS's newer **mantle** plane. (Grok **4.6**, unlike 4.3, is dual-plane: its builtin entry is the CLASSIC `us.xai.grok-4.6` Converse profile in the builtin catalog below, and the mantle id `xai.grok-4.6` remains reachable via a user-catalog entry with the mantle strategy.) The mantle plane is an OpenAI-Responses REST API at `https://bedrock-mantle.<region>.api.aws` rather than the classic Converse API. Mantle models are **bearer-token only** (SigV4 does not work; set a key via Settings or `2nb config bedrock --set`), region-pinned per model (`ModelInfo.Region`), invisible to `ListFoundationModels`, and per-account entitlement varies.

### Enumerating the plane

**The plane IS enumerable** (corrected 2026-08-20; it was believed unenumerable): `GET https://bedrock-mantle.<region>.api.aws/v1/models` (bearer auth) lists each region's catalog. The listing path has **no `/openai` prefix**: the responses call uses `/openai/v1/responses`, the models listing does not, and the `/openai/v1/models` form 404s. Only the `id` field is reliable per AWS docs. Roughly 50 models per region across the documented mantle regions us-east-1/us-east-2/us-west-2 (`bedrockMantleRegions` in `cli/internal/ai/bedrock_mantle_models.go`, the maintenance point for new regions).

### Discovery and routing hints

`--discover` (on `models list`, `models verify`, and `models cost-preview`) merges the listing in as unverified generation rows **carrying their routing** (`invoke_strategy: bedrock_mantle_responses` plus a listing-region pin), so a probe of a discovered row dispatches over the mantle plane in the right region even though no catalog knows the id. The probe path takes the candidate's hints only when catalog resolution declares nothing: AUTHORED routing wins, but the absence of routing does not. A merged row whose routing fields are empty adopts the discovered row's hints in-memory at list time (`mergeDiscovered` plus `ai.AdoptRoutingHints`), so a row whose routing was stripped by a pre-0.19.0 save clobber self-heals on the next `verify --discover` instead of shadowing its own cure.

A passing `models verify --discover` persists that routing into the user catalog, after which the ordinary resolvers route every invoke. Until then, bare `models test <mantle-discovered-id>` still resolves classic and 404s (documented limitation; use `models verify --discover <id>`). Mantle listings ride the same 24h discovery cache, namespaced as `bedrock-mantle-<region>-<profile>.json`.

### Entitlement and dialect caveats

**Listing proves existence, not entitlement**: the staged rollout gates at invoke time, so listed models can still probe `access_denied`.

**Nor does listing prove the Responses dialect**: measured live 2026-08-20, part of the listed catalog (e.g. `deepseek.v3.2`) rejects `/openai/v1/responses` with a 400 naming the gap and answers only on the plane's unprefixed `/v1/chat/completions` route, which 2nb has no client for yet. Those models record an honest classified `invalid_request` on probe (a mantle chat-completions strategy is a tracked follow-up decision); frontier Responses models (`xai.grok-4.6` mantle, the openai line) pass.

On the probe result the effective strategy also drives the dual-plane precedence: an exact catalog entry beats the discovery hint, and the hint beats the profile-stripped base-match inference (so the classic `us.xai.grok-4.6` builtin cannot drag the mantle-listed bare `xai.grok-4.6` onto Converse).

### Error classification: the two 401s

The plane returns **HTTP 401 for two different causes**, so the status alone must never drive classification. The response body's `error.code` disambiguates them: `invalid_api_key` means a bad bearer token; `access_denied` means a valid token whose account is not entitled to the model (AWS's staged frontier rollout). `mantleHTTPError` lifts that code onto `ProviderHTTPError.Code`, which `ClassifyProbeError` consults **before** the HTTP status (`classifyProviderErrorCode`), so a gated model reports `access_denied` rather than sending a user with working credentials off to re-run `aws configure`.

### Remediation

The remediation is **mantle-aware**: because mantle models are invisible to the Bedrock control plane, `RemediationFor` (keyed on the resolved `InvokeStrategy`, threaded through `TestProbeModel`) points a gated mantle model at **AWS Sales** and disclaims a 2nb/credential fault ("your other models still work"), NOT the Bedrock console's "Model access" page, which does not govern mantle models. The probe result carries the model's `invoke_strategy` (additive JSON) so the macOS AI Hub suppresses its "Open AWS console" button for mantle `access_denied` (the page can't unblock the model).

### Client

The client is `cli/internal/ai/bedrock_mantle.go` (`BedrockMantleGenerator`, strategy `bedrock_mantle_responses`); `NewBedrockGeneration` dispatches to it by strategy, and a smoke probe sends `reasoning.effort: none` so default-on reasoning does not starve the answer. The resolved endpoint host is constrained to `https` plus `*.api.aws` so a poisoned vault-scoped `models.yaml` cannot exfiltrate the bearer token. Generation-only for now (no mantle embeddings or rerank).

## Provider readiness: what "not ready" reports, and what gets cached

`Available(ctx) bool` answers only yes or no, and it is implemented on ~12 types across three interfaces, so it was never widened. Instead the three Bedrock providers (embedder, generator, reranker) implement the optional `ai.AvailabilityReporter`:

```go
type AvailabilityReporter interface {
	AvailableDetail(ctx context.Context) (bool, TestErrorCode)
}
```

`Available()` is a thin wrapper over it, so no call site changed. The shared `bedrockAvailableProbe` (a free control-plane `ListFoundationModels`, 5s) now returns its error instead of swallowing it at `slog.Debug`, and `ClassifyProbeError` turns that into the same `TestErrorCode` vocabulary `models test` and the macOS app already use.

**Why it matters:** `2nb index --force-reembed` refuses to run against an unready provider, and the message it printed was `is not ready (check credentials)` for **every** cause. A five-second network blip therefore accused the user's credentials. `embedderNotReadyError` (`cli/internal/cli/readiness.go`) now names the cause and reuses `ai.RemediationFor`, so a timeout reads `is not ready (timeout). The probe timed out. …` and only a genuine auth failure mentions credentials. A provider that does not implement the interface keeps the original wording.

**Ask once, carry the answer.** Callers use `ai.Availability(ctx, provider)`, which does the optional-interface assertion in one place and returns `(ready, code)`. Testing `Available()` and then re-asking for the reason can get a different answer than the first ask, and because a transient failure is deliberately short-lived in the cache, that second ask can find the entry lapsed and pay a fresh probe, on exactly the path this reporting exists to explain.

Inside the CLI the resolved verdict travels as `providerReadiness` (`cli/internal/cli/readiness.go`) rather than being re-derived at each surface that reports it. `ai status` resolves the embedder, generator, and reranker once each, concurrently (the pattern `vault status` already used), and hands the embedder's verdict to both `derivePortability` and the per-provider `reason` field. This is a correctness and consistency change more than a latency one: for a healthy provider or a *definitive* failure the cache already collapsed the repeats, so the live-probe count is unchanged at one per registered role. The difference shows on a *transient* failure, whose 2s TTL a repeat can miss, paying a fresh 5s probe; and on every path, carrying one verdict means the surfaces cannot disagree.

**One renderer, every surface.** `ai.NotReadyMessage(kind, provider, code)` (`cli/internal/ai/not_ready.go`) backs the CLI paths (`ask`, `chat`, `polish`, `index --force-reembed`, `ai embed-probe`, `eval`, `eval answers`, `suggest-links`, `suggest-target`), the MCP twins (`kb_ask`, `kb_polish`, `kb_suggest_links`), the `VectorCompat` degradation banner, and `mcp doctor`. The split is deliberate: **`internal/cli` owns the carrier** (`providerReadiness`, which holds a resolved verdict and travels between surfaces), **`internal/ai` owns the words**.

`kind` names the role ("embedding" or "generation") so the message says which thing the user was trying to do. `derivePortability`'s `provider_unavailable` LABEL is unchanged (`config_doctor` and the macOS `VaultStatusView` both switch on it); only its hint text now names the cause instead of listing every provider's likeliest problem and leaving the user to guess.

**One `access_denied` override.** The readiness probe is a control-plane listing, so a denial means the IAM principal cannot call `bedrock:ListFoundationModels`. `RemediationFor` answers for `models test`, which invokes a model, and so reads `access_denied` as model entitlement and points at the Bedrock console's "Model access" page. That page cannot grant an API permission, so `readinessRemediation` substitutes text naming the permission. Every other code means the same thing on both paths.

**Caching rule** (`failureTTL`, `cli/internal/ai/availability.go`): a *definitive* failure (`bad_credentials`, `access_denied`, `not_found`, `incompatible`, `invalid_request`, `unknown`) is held for the full 30s TTL, because it will still be false a second from now. A *transient* one (`timeout`, `provider_unreachable`, `throttled`) is held for only **2 seconds**. Not zero: an MCP server holds one provider instance for its whole life and consults `Available()` on every `kb_search`/`kb_ask`, so with no entry at all a black-holed network adds the probe's full 5s timeout to every tool call, and a throttled control plane receives a fresh burst of requests per call, which is the cache being disabled in the exact regime it exists to protect. Two seconds bounds the storm while still seeing a recovered provider almost immediately. `unknown` is definitive deliberately: it preserves the amortization the cache exists for, and a genuine transient landing there is a missing rule in `ClassifyProbeError`, which is where it should be fixed.

Missing credentials is the case that ties the two together. With no credentials the AWS SDK falls through to the EC2 instance metadata service, which is unreachable off EC2 and returns an error *wrapping* `context.DeadlineExceeded`. `ClassifyProbeError` therefore checks for credential-resolution failures **before** the deadline branch; otherwise every credential-free caller would classify as `timeout`, hold it for only the 2s transient TTL, and pay the full 5s probe again every couple of seconds. Setting `AWS_EC2_METADATA_DISABLED=true` (as CI does) turns that path into a 3ms `bad_credentials`.

## Opt-in providers: Ollama and OpenRouter

**Ollama (local) and OpenRouter are opt-in**: both ship `disabled: true` in a fresh vault's `config.yaml`, so selection UIs show only Bedrock until the user enables them. `2nb ai setup` (a Bedrock-first wizard that detects AWS creds, confirms region, verifies models, and reminds you to enable Bedrock model access in the AWS console), the macOS AI Hub, or activating the provider with `2nb config set ai.provider <name>` clears the `disabled` flag. `Disabled` only hides a provider's models from dropdowns; an explicitly-chosen active provider still runs.

## llama-local (bundled Gemma, fully offline)

llama-local is a fourth, opt-in provider (`disabled: true` on a fresh vault): a bundled llama.cpp `llama-server` runs Gemma weights locally with no API key. The GGUF weights are downloaded on demand (never bundled), sha256-verified into `~/Library/Caches/2nb/models/` from ungated Hugging Face repos: EmbeddingGemma-300m (embed), Gemma 4 E2B/E4B (generation), bge-reranker-v2-m3 (rerank). Fetch them via `2nb ai engine pull <id>`, the `2nb ai setup` llama-local branch, or the macOS AI Hub's "Download local models" button. `EnsureModel` fails closed on an empty or mismatched sha256, and a 60s idle watchdog bounds a stalled transfer. Model pins and licenses (Gemma Terms of Use, not Apache) live in `cli/internal/llama/models.go`; attribution is in `THIRD_PARTY_NOTICES.md`.

**llama-local is NOT user-ready and is hidden in the macOS GUI.** Only model weights are provisioned; the `llama-server` engine binary is neither bundled in the app nor downloaded, so `LocateEngine` finds nothing and embeddings/generation fail with "engine not running". A CLI user can still run it by putting `llama-server` on PATH (`brew install llama.cpp`) then `2nb ai engine install`. Every GUI surface is gated behind `AIHubView.localEngineFeatureEnabled` (currently `false`); do not surface llama-local in the GUI until the engine ships. Full architecture, build status, and the path forward: [llama-local-engine.md](llama-local-engine.md).

## Bedrock embedding (Marengo)

Beyond builtin models, the Bedrock embedder supports TwelveLabs Marengo embed via Bedrock InvokeModel. Marengo 2.7 takes `{"inputType":"text","inputText":"..."}`; Marengo 3.0 wraps the text: `{"inputType":"text","text":{"inputText":"..."}}`. Both return `{"data":{"embedding":[...]}}`. Add via `2nb models add <id> --provider bedrock --type embedding --price-request <USD>`.

## Live pricing

`models list`, `ai status`, and `index` fetch pricing from OpenRouter `/models` and AWS pricing offer files, with a 24h disk cache at `$XDG_CACHE_HOME/2nb/pricing` (macOS: `~/Library/Caches/2nb/pricing`). Fetches carry a 15s timeout; an air-gapped machine falls back to the stale cache, then to builtin metadata.

## Invoke strategies

Catalog entries carry an `InvokeStrategy` naming the API dialect. Strategies (in `cli/internal/ai/invoke_strategy.go`):

- `bedrock_converse`
- `bedrock_invoke_anthropic`, `bedrock_invoke_nova`, `bedrock_invoke_nova_embed`, `bedrock_invoke_titan_embed`, `bedrock_invoke_cohere_embed`, `bedrock_invoke_marengo_2_7`, `bedrock_invoke_marengo_3_0`
- `bedrock_mantle_responses` (OpenAI-Responses dialect on the per-model-region bedrock-mantle plane; generation-only)
- `anthropic_messages`
- `openai_chat`, `openai_embeddings`
- `openrouter_chat`, `openrouter_embeddings`
- `ollama_generate`, `ollama_embeddings`

Empty means "use provider default". The user catalog overrides the builtin catalog. Adding a model variant no longer requires dispatcher code changes: a catalog entry with the right strategy is enough.

## Classic-plane compatibility gate (default-allow)

The classic-plane generation compatibility gate is **default-allow** (`bedrockModelSupported` in `cli/internal/ai/bedrock_support.go`, called from discovery's `catalogCompatibility` and from `BedrockPreflightModel` before every probe): only five conceptual deny categories are checked statically (image-generation, video-generation, rerank-as-generation, video-understanding (pegasus), and Palmyra Vision), and everything else on the classic Converse plane is admitted, including a vendor family 2nb has never invoked. Before this, an unrecognized vendor needed a code change to even become visible (Grok 4.6 needed one, PR #213); now it surfaces through `models list --discover` immediately as an unverified, probeable row, and `models test`/`models verify` is the real compatibility check: it measures entitlement and dialect fit directly and persists a classified failure (e.g. `incompatible`) if a model turns out to need a non-Converse wire format.

The embedding and rerank gates are unaffected and stay explicit per-family allowlists, since each vendor's `InvokeModel` body shape is genuinely different (no analogous uniform dialect to default-allow into). `internal/ai/discovery_cache.go`'s `DiscoveryCacheVersion` was bumped to 2 alongside this change so a cache written under the old, narrower gate cannot keep serving a stale catalog for its 24h TTL.

## Retrieval-quality probe

Scores stored embeddings by checking whether each resolved wikilink's target appears in the source's top-K semantic neighbors. Returns MRR@K and Recall@K (K=10) over the usable-pair set. Requires at least 10 resolved wikilink pairs (configurable via `MinLinksForRetrievalProbe`); below that it returns `ErrTooFewLinks` so callers skip silently. Zero API cost.

## Cost estimator

Per-probe token assumptions:

| Probe | Input tokens | Output tokens | Requests | Constant |
|---|---|---|---|---|
| `test` | 20 | 1024 | 1 | `probeGenMaxTokens` (the probe's output budget) |
| `bench_embed` | 10 | 0 | 1 | |
| `bench_gen` | 20 | 1024 | 1 | `BenchGenMaxTokens` |
| `bench_rag` | 2500 | 4096 | 1 | `RAGGenMaxTokens` (the same budget `ask`/RAG generation spends) |
| `retrieval` | 0 | 0 | 0 | zero-API |

The specs quote the shared constants in `cost_estimate.go` so estimates cannot drift from what a probe may bill. Every generation budget follows the project rule: **budgets bound runaway cost, they must never fail a working model**. Classic always-reasoning models bill roughly 180 reasoning tokens before any answer text, with no off switch; `TestGenerationBudgetsPinned` holds the floors. `KnownPricing` distinguishes known-free (`Local=true`, or an explicit $0 with `PriceSource` set) from unknown.

## Builtin catalog

Curated Bedrock entries and their release invariants:

- **Anthropic line**: Haiku 4.5 (the tested default), Sonnet 4.6, Sonnet 5, Opus 4.6, and Opus 4.8. Sonnet 5 and Opus 4.8 ship **unpinned** (live offer-file pricing) with a staged-rollout note.
- **Deliberately non-curated**: Opus 4.7 and Fable 5. Discovery still surfaces them.
- **`us.xai.grok-4.6`** is a builtin CLASSIC-plane entry: Converse via the cross-region profile, 500K context, geo pricing pinned. The first xAI model on the classic plane (2026-08-19), per-account gated like the Anthropic frontier. The probe budget covers its always-on reasoning, which bills roughly 180 output tokens for a trivial answer.
- **Mantle builtins**: `openai.gpt-5.5` (us-east-2) and `xai.grok-4.3` (us-west-2), both non-curated; see the mantle plane section above.
- **Catalog freshness** is guarded by cred-gated tests: `TestBuiltinBedrockAnthropicModelsStillListed` and `TestLivePricing_ResolvesUnpinnedBuiltinAnthropic`. Run them before releases.
