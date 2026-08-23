import Foundation
import Testing
@testable import SecondBrain

// Fixtures captured from the built CLI (`2nb models discover --json
// --porcelain`, v0.19.1 + PR #225/#228) against a temp vault with seeded
// discovery caches, mirroring cli/internal/cli/models_discover_test.go's
// seedDiscoverCaches. Trimmed to the load-bearing rows; key shapes are
// verbatim.

/// First run: pool listed, baseline seeds silently (first_run, empty diff).
private let firstRunEnvelope = #"""
{
  "sources": [
    {"source": "classic", "region": "us-east-1", "path": "/x/cache/2nb/discovery/bedrock-us-east-1-default.json", "exists": true, "fetched_at": "2026-08-23T01:36:18Z", "age_seconds": 5, "stale": false},
    {"source": "mantle", "region": "us-east-1", "path": "/x/cache/2nb/discovery/bedrock-mantle-us-east-1-default.json", "exists": true, "fetched_at": "2026-08-23T01:36:18Z", "stale": false}
  ],
  "models": [
    {"id": "fake.classic-model-v1", "name": "", "provider": "bedrock", "type": "generation", "vendor": "fake", "vendor_display": "Fake", "price_input_per_million": 0, "price_output_per_million": 0, "local": false, "tier": "unverified", "compatible": true, "working": false},
    {"id": "fake.mantle-model-v1", "name": "", "provider": "bedrock", "type": "generation", "vendor": "fake", "vendor_display": "Fake", "price_input_per_million": 0, "price_output_per_million": 0, "local": false, "tier": "unverified", "invoke_strategy": "bedrock_mantle_responses", "region": "us-east-1", "compatible": true, "working": false}
  ],
  "new": [],
  "gone": [],
  "first_run": true,
  "warnings": [
    "ollama discovery failed: fetch ollama models: Get \"http://127.0.0.1:9/api/tags\": dial tcp 127.0.0.1:9: connect: connection refused"
  ]
}
"""#

/// A later run where one mantle model is newly listed.
private let newArrivalEnvelope = #"""
{
  "sources": [
    {"source": "classic", "region": "us-east-1", "exists": true, "fetched_at": "2026-08-23T01:36:18Z", "age_seconds": 5, "stale": false}
  ],
  "models": [
    {"id": "fake.mantle-model-v1", "name": "", "provider": "bedrock", "type": "generation", "tier": "unverified", "invoke_strategy": "bedrock_mantle_responses", "region": "us-east-1", "compatible": true, "working": false},
    {"id": "fake.mantle-model-v2", "name": "", "provider": "bedrock", "type": "generation", "tier": "unverified", "invoke_strategy": "bedrock_mantle_responses", "region": "us-east-1", "compatible": true, "working": false}
  ],
  "new": [
    {"id": "fake.mantle-model-v2", "name": "", "provider": "bedrock", "type": "generation", "tier": "unverified", "invoke_strategy": "bedrock_mantle_responses", "region": "us-east-1", "compatible": true, "working": false}
  ],
  "gone": [],
  "warnings": []
}
"""#

/// A run after a listed model was delisted: GONE reports its baseline key.
private let goneEnvelope = #"""
{
  "sources": [
    {"source": "classic", "region": "us-east-1", "exists": true, "stale": false}
  ],
  "models": [
    {"id": "fake.mantle-model-v1", "name": "", "provider": "bedrock", "type": "generation", "tier": "unverified", "invoke_strategy": "bedrock_mantle_responses", "region": "us-east-1", "compatible": true, "working": false}
  ],
  "new": [],
  "gone": ["bedrock|fake.mantle-model-v2"]
}
"""#

/// `--add x --validate --yes`: added ids plus per-probe results (the verify
/// TestProbeResult shape; this capture failed on a dummy bearer token).
private let addValidateEnvelope = #"""
{
  "sources": [
    {"source": "classic", "region": "us-east-1", "exists": true, "stale": false}
  ],
  "models": [
    {"id": "fake.mantle-model-v2", "name": "", "provider": "bedrock", "type": "generation", "tier": "unverified", "invoke_strategy": "bedrock_mantle_responses", "region": "us-east-1", "compatible": true, "working": false}
  ],
  "new": [],
  "gone": [],
  "added": ["fake.mantle-model-v2"],
  "results": [
    {
      "model_id": "fake.mantle-model-v2",
      "provider": "bedrock",
      "type": "generation",
      "ok": false,
      "detail": "bedrock https://bedrock-mantle.us-east-1.api.aws/openai/v1/responses: status 401",
      "latency": "156ms",
      "code": "bad_credentials",
      "remediation": "The Bedrock API key was rejected by the mantle plane (invalid or expired bearer token).",
      "invoke_strategy": "bedrock_mantle_responses",
      "region": "us-east-1"
    }
  ]
}
"""#

/// What a PRE-discover CLI emits for the same argv: cobra parents with a
/// RunE swallow unknown subcommands, so `models discover --json` runs as
/// `models list --json` — exit 0, top-level ARRAY (measured live on 0.19.1).
private let preDiscoverCLIPayload = #"""
[
  {"id": "amazon.nova-2-multimodal-embeddings-v1:0", "name": "Amazon Nova Embeddings v2", "provider": "bedrock", "type": "embedding"},
  {"id": "us.anthropic.claude-haiku-4-5-20251001-v1:0", "name": "Claude Haiku 4.5", "provider": "bedrock", "type": "generation"}
]
"""#

private func decodeReport(_ json: String) throws -> DiscoverReportInfo {
    try JSONDecoder().decode(DiscoverReportInfo.self, from: Data(json.utf8))
}

// MARK: - Envelope decoding

@Test("first-run envelope decodes: sources, pool, silent seed")
func decodeFirstRunEnvelope() throws {
    let report = try decodeReport(firstRunEnvelope)
    #expect(report.firstRun == true)
    #expect(report.new.isEmpty)
    #expect(report.gone.isEmpty)
    #expect(report.models.map(\.modelID) == ["fake.classic-model-v1", "fake.mantle-model-v1"])
    #expect(report.sources.count == 2)
    let classic = try #require(report.sources.first)
    #expect(classic.source == "classic")
    #expect(classic.region == "us-east-1")
    #expect(classic.exists)
    #expect(!classic.stale)
    #expect(classic.ageSeconds == 5)
    // Go omitempty drops age_seconds at 0: the mantle row decodes without it.
    #expect(report.sources[1].ageSeconds == nil)
    #expect(report.warnings?.count == 1)
}

@Test("NEW envelope carries the arrival with its routing hints intact")
func decodeNewArrivalEnvelope() throws {
    let report = try decodeReport(newArrivalEnvelope)
    #expect(report.firstRun == nil)
    #expect(report.new.map(\.modelID) == ["fake.mantle-model-v2"])
    let arrival = try #require(report.new.first)
    #expect(arrival.invokeStrategy == "bedrock_mantle_responses")
    #expect(arrival.region == "us-east-1")
}

@Test("GONE envelope carries the baseline key of the delisted model")
func decodeGoneEnvelope() throws {
    let report = try decodeReport(goneEnvelope)
    #expect(report.gone == ["bedrock|fake.mantle-model-v2"])
    #expect(report.new.isEmpty)
}

@Test("add+validate envelope carries added ids and classified probe results")
func decodeAddValidateEnvelope() throws {
    let report = try decodeReport(addValidateEnvelope)
    #expect(report.added == ["fake.mantle-model-v2"])
    let result = try #require(report.results?.first)
    #expect(result.modelID == "fake.mantle-model-v2")
    #expect(!result.ok)
    #expect(result.code == "bad_credentials")
    #expect(result.latency == "156ms")
}

// MARK: - Capability probe (shape-based, never exit-code-based)

@Test("classify accepts every discover envelope variant as supported")
func classifySupportedVariants() {
    for fixture in [firstRunEnvelope, newArrivalEnvelope, goneEnvelope, addValidateEnvelope] {
        guard case .supported = DiscoverCLIProbe.classify(Data(fixture.utf8)) else {
            Issue.record("envelope misclassified: \(fixture.prefix(60))")
            return
        }
    }
}

@Test("classify flags the pre-discover CLI's models-list array as unsupported")
func classifyPreDiscoverArray() {
    guard case .unsupported = DiscoverCLIProbe.classify(Data(preDiscoverCLIPayload.utf8)) else {
        Issue.record("a top-level array is the old CLI's models list, not an envelope")
        return
    }
}

@Test("classify reports garbage as undecodable, deciding nothing about support")
func classifyGarbage() {
    guard case .undecodable = DiscoverCLIProbe.classify(Data("not json".utf8)) else {
        Issue.record("garbage must not settle the capability either way")
        return
    }
    // An object that is NOT the envelope (missing `sources`) is also
    // undecodable: only the real envelope proves support.
    guard case .undecodable = DiscoverCLIProbe.classify(Data(#"{"error": "boom"}"#.utf8)) else {
        Issue.record("a non-envelope object must not classify as supported or unsupported")
        return
    }
}

@Test("indicatesUnsupported matches unknown-flag exits, never cost or validation refusals")
func indicatesUnsupportedShapes() {
    #expect(DiscoverCLIProbe.indicatesUnsupported(stderr: "Error: unknown flag: --refresh"))
    #expect(DiscoverCLIProbe.indicatesUnsupported(stderr: "Error: unknown command \"discover\" for \"2nb models\""))
    #expect(!DiscoverCLIProbe.indicatesUnsupported(stderr: "estimated probe cost $1.20 exceeds --cost-cap $0.50"))
    #expect(!DiscoverCLIProbe.indicatesUnsupported(stderr: "x is not in the discovered pool; run `2nb models discover` to list ids"))
}

// MARK: - Presentation (mirrors the CLI's own rendering)

@Test("source lines render like the terminal: age, stale, not-cached")
func sourceLineRendering() {
    let fresh = DiscoverSourceAgeInfo(source: "classic", region: "us-east-1", exists: true, fetchedAt: "2026-08-23T01:00:00Z", ageSeconds: 3 * 3600, stale: false)
    #expect(DiscoverPresentation.sourceLine(fresh) == "classic us-east-1: 3h ago")

    let justNow = DiscoverSourceAgeInfo(source: "mantle", region: "us-east-1", exists: true, fetchedAt: nil, ageSeconds: 5, stale: false)
    #expect(DiscoverPresentation.sourceLine(justNow) == "mantle us-east-1: just now")

    let stale = DiscoverSourceAgeInfo(source: "mantle", region: "us-west-2", exists: true, fetchedAt: nil, ageSeconds: 26 * 3600, stale: true)
    #expect(DiscoverPresentation.sourceLine(stale) == "mantle us-west-2: stale (26h)")

    let missing = DiscoverSourceAgeInfo(source: "classic", region: "us-east-1", exists: false, fetchedAt: nil, ageSeconds: nil, stale: false)
    #expect(DiscoverPresentation.sourceLine(missing) == "classic us-east-1: not cached (live walk on next use)")
}

@Test("compactAge matches the CLI's buckets")
func compactAgeBuckets() {
    #expect(DiscoverPresentation.compactAge(seconds: 30) == "<1m")
    #expect(DiscoverPresentation.compactAge(seconds: 45 * 60) == "45m")
    #expect(DiscoverPresentation.compactAge(seconds: 26 * 3600) == "26h")
    #expect(DiscoverPresentation.compactAge(seconds: 72 * 3600) == "3d")
}

@Test("route labels mirror discoverRouteLabel: mantle+region, classic, provider")
func routeLabels() throws {
    let report = try decodeReport(newArrivalEnvelope)
    let mantle = try #require(report.new.first)
    #expect(DiscoverPresentation.routeLabel(mantle) == "mantle us-east-1")

    let classic = try #require(try decodeReport(firstRunEnvelope).models.first)
    #expect(DiscoverPresentation.routeLabel(classic) == "classic")

    let ollama = try JSONDecoder().decode(CatalogModelInfo.self, from: Data(#"{"id": "llama3", "name": "Llama", "provider": "ollama", "type": "generation"}"#.utf8))
    #expect(DiscoverPresentation.routeLabel(ollama) == "ollama")
}

@Test("validate outcome lines render PASS with latency and FAIL with the classified code")
func validateOutcomeRendering() throws {
    let fail = try #require(try decodeReport(addValidateEnvelope).results?.first)
    let pass = try JSONDecoder().decode(VerifyProbeResult.self, from: Data(#"{"model_id": "openai.gpt-5.5", "provider": "bedrock", "type": "generation", "ok": true, "latency": "1.2s"}"#.utf8))
    #expect(DiscoverPresentation.validateOutcomeLines([pass, fail]) == [
        "openai.gpt-5.5: PASS (1.2s)",
        "fake.mantle-model-v2: FAIL (bad_credentials)",
    ])
}

@Test("gone keys display as the model id")
func goneDisplayTrimsProvider() {
    #expect(DiscoverPresentation.goneDisplay("bedrock|fake.mantle-model-v2") == "fake.mantle-model-v2")
    #expect(DiscoverPresentation.goneDisplay("no-separator") == "no-separator")
}

// MARK: - Argv builder

@Test("modelsDiscoverArgs builds the documented argv shapes")
func discoverArgs() {
    #expect(AppState.modelsDiscoverArgs(refresh: false, add: [], validate: false, costCap: nil)
        == ["models", "discover", "--json", "--porcelain"])
    #expect(AppState.modelsDiscoverArgs(refresh: true, add: [], validate: false, costCap: nil)
        == ["models", "discover", "--json", "--porcelain", "--refresh"])
    #expect(AppState.modelsDiscoverArgs(refresh: false, add: ["xai.grok-4.6"], validate: false, costCap: nil)
        == ["models", "discover", "--json", "--porcelain", "--add", "xai.grok-4.6"])
    // Add + Validate: --yes always rides with --validate (the GUI already
    // cost-confirmed), and the cap is the confirmed estimate's.
    #expect(AppState.modelsDiscoverArgs(refresh: false, add: ["xai.grok-4.6"], validate: true, costCap: 0.0301)
        == ["models", "discover", "--json", "--porcelain", "--add", "xai.grok-4.6", "--validate", "--yes", "--cost-cap", "0.0301"])
}
