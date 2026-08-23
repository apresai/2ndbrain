import Foundation

/// Types and pure logic for `2nb models discover` (discovery as a verb):
/// per-source cache ages, the discovered pool, and the machine-local NEW/GONE
/// diff. The Testing tab's Discover card and the Models tab's nudge banner
/// both render from these; every view decision that can be computed without
/// SwiftUI lives here so it is unit-testable.

/// One row of the envelope's `sources` array (Go `ai.DiscoverySourceAge`):
/// the freshness of one discovery cache file ("classic us-east-1", "mantle
/// us-west-2", ...).
struct DiscoverSourceAgeInfo: Codable, Equatable, Identifiable {
    var id: String { source + "|" + region }
    let source: String
    let region: String
    let exists: Bool
    /// RFC3339 mtime of the cache file; absent when no file exists.
    let fetchedAt: String?
    /// Age in whole seconds; absent (Go omitempty) when zero or no file.
    let ageSeconds: Int64?
    /// Older than the 24h TTL: the next cached read-through walks live.
    let stale: Bool

    enum CodingKeys: String, CodingKey {
        case source, region, exists, stale
        case fetchedAt = "fetched_at"
        case ageSeconds = "age_seconds"
    }
}

/// The `2nb models discover --json` envelope (Go `discoverReport`):
/// `{sources, models, new, gone, first_run?, refreshed?, added?, results?,
/// warnings?}`. The collection fields are always `[]`, never null (pinned by
/// the CLI contract tests); `results` reuses the verify probe shape
/// (`ai.TestProbeResult`), which `VerifyProbeResult` already decodes.
struct DiscoverReportInfo: Codable {
    let sources: [DiscoverSourceAgeInfo]
    let models: [CatalogModelInfo]
    let new: [CatalogModelInfo]
    let gone: [String]
    let firstRun: Bool?
    let refreshed: Bool?
    let added: [String]?
    let results: [VerifyProbeResult]?
    let warnings: [String]?

    enum CodingKeys: String, CodingKey {
        case sources, models, new, gone, added, results, warnings
        case firstRun = "first_run"
        case refreshed = "refreshed"
    }
}

/// Outcome of one `models discover` invocation through AppState.
enum DiscoverRunOutcome {
    case report(DiscoverReportInfo)
    /// The resolved CLI predates the verb. See `DiscoverCLIProbe`.
    case unsupported
}

/// Capability gate for `models discover`. Exit status proves NOTHING here:
/// cobra parents with a RunE swallow unknown subcommands, so a pre-discover
/// CLI runs `2nb models discover --json` as `models list --json` with a stray
/// positional arg: exit 0, top-level JSON ARRAY (measured live on 0.19.1).
/// The envelope, an OBJECT carrying `sources`, is the real signal.
enum DiscoverCLIProbe {
    enum Outcome {
        case supported(DiscoverReportInfo)
        /// The payload is the `models list` array a pre-discover CLI emits.
        case unsupported
        /// Neither shape: report the failure, decide nothing about support.
        case undecodable
    }

    static func classify(_ data: Data) -> Outcome {
        if let report = try? JSONDecoder().decode(DiscoverReportInfo.self, from: data) {
            return .supported(report)
        }
        if (try? JSONSerialization.jsonObject(with: data)) is [Any] {
            return .unsupported
        }
        return .undecodable
    }

    /// A non-zero exit whose stderr names an unknown flag also proves an
    /// older CLI: `models list` rejects `--refresh`, so the refresh form
    /// fails loudly where the plain form silently lists. Never matches a
    /// cost-cap or validation refusal, which must stay transient errors.
    static func indicatesUnsupported(stderr: String) -> Bool {
        let lower = stderr.lowercased()
        return lower.contains("unknown flag") || lower.contains("unknown command")
            || lower.contains("unknown shorthand flag")
    }
}

/// Session-scoped accumulation of the server-side NEW/GONE diff. The CLI's
/// diff is one-shot (every run advances the machine-local seen baseline, so
/// NEW and GONE report once and then clear), and the GUI runs discover from
/// more than one surface (the Models tab's nudge probe, the Discover card's
/// load, Refresh). Without accumulation, whichever surface ran first would
/// eat the diff before the other rendered it, and a reload between the run
/// and the user's glance would silently clear the banner. Keys are the CLI's
/// own "provider|modelID" baseline vocabulary.
enum DiscoverDiffSession {
    struct State: Equatable {
        var newKeys: Set<String> = []
        var goneKeys: Set<String> = []
    }

    static func poolKeys(_ report: DiscoverReportInfo) -> Set<String> {
        Set(report.models.map { DiscoveryNudge.modelKey(provider: $0.provider, modelID: $0.modelID) })
    }

    /// Folds one report into the session state. A key stays session-NEW only
    /// while it is still in the discovered pool (a graduated or delisted key
    /// drops, the delisted one landing in GONE via the same report); GONE
    /// accumulates until the model returns to the pool.
    static func merged(_ state: State, report: DiscoverReportInfo) -> State {
        let pool = poolKeys(report)
        var next = state
        next.newKeys = state.newKeys.intersection(pool)
            .union(report.new.map { DiscoveryNudge.modelKey(provider: $0.provider, modelID: $0.modelID) })
        next.goneKeys = state.goneKeys.union(report.gone).subtracting(pool)
        return next
    }

    /// Drops just-added ids from session-NEW immediately: the --add run's
    /// envelope still lists them in the pool (the CLI computes the listing
    /// before persisting the add), so the pool-intersection rule alone would
    /// keep them badged one run too long.
    static func afterAdd(_ state: State, addedIDs: [String]) -> State {
        let ids = Set(addedIDs)
        var next = state
        next.newKeys = state.newKeys.filter { !ids.contains(modelID(of: $0)) }
        return next
    }

    /// Clears every session-NEW key for one provider: the user dismissed the
    /// banner or completed a Validate, i.e. has seen this discovery state.
    static func clearingNew(_ state: State, provider: String) -> State {
        var next = state
        next.newKeys = state.newKeys.filter { !$0.hasPrefix(provider + "|") }
        return next
    }

    /// Pool rows the Discover card lists as NEW, ordered by id.
    static func newRows(report: DiscoverReportInfo, state: State) -> [CatalogModelInfo] {
        report.models
            .filter { state.newKeys.contains(DiscoveryNudge.modelKey(provider: $0.provider, modelID: $0.modelID)) }
            .sorted { $0.modelID < $1.modelID }
    }

    /// GONE keys for informational display, sorted.
    static func goneRows(_ state: State) -> [String] {
        state.goneKeys.sorted()
    }

    static func modelID(of key: String) -> String {
        guard let sep = key.firstIndex(of: "|") else { return key }
        return String(key[key.index(after: sep)...])
    }
}

/// Display helpers for the Discover card, mirroring the CLI's own rendering
/// (`formatSourceAge` / `compactAge` / `discoverRouteLabel` in
/// models_discover.go) so the card reads like the terminal.
enum DiscoverPresentation {
    /// "classic us-east-1: 3h ago" / "mantle us-west-2: stale (26h)".
    static func sourceLine(_ s: DiscoverSourceAgeInfo) -> String {
        "\(s.source) \(s.region): \(ageText(s))"
    }

    static func ageText(_ s: DiscoverSourceAgeInfo) -> String {
        guard s.exists else { return "not cached (live walk on next use)" }
        let seconds = s.ageSeconds ?? 0
        if s.stale { return "stale (\(compactAge(seconds: seconds)))" }
        if seconds < 60 { return "just now" }
        return compactAge(seconds: seconds) + " ago"
    }

    static func compactAge(seconds: Int64) -> String {
        switch seconds {
        case ..<60: return "<1m"
        case ..<3600: return "\(seconds / 60)m"
        case ..<(48 * 3600): return "\(seconds / 3600)h"
        default: return "\(seconds / (24 * 3600))d"
        }
    }

    /// The plane a discovered row would invoke over: "mantle <region>" for
    /// hint-carrying mantle listings, "classic" for control-plane Bedrock
    /// rows, the provider's own name otherwise.
    static func routeLabel(_ m: CatalogModelInfo) -> String {
        if m.invokeStrategy == "bedrock_mantle_responses" {
            let region = m.region ?? ""
            return region.isEmpty ? "mantle" : "mantle " + region
        }
        return m.provider == "bedrock" ? "classic" : m.provider
    }

    /// "provider|id" GONE key rendered as the id (the provider is obvious in
    /// context; the raw key stays available in the CLI).
    static func goneDisplay(_ key: String) -> String {
        DiscoverDiffSession.modelID(of: key)
    }

    /// One line summarizing a `--validate` run's probe results:
    /// "fake.mantle-model-v2: PASS (156ms)" / "...: FAIL (bad_credentials)".
    static func validateOutcomeLines(_ results: [VerifyProbeResult]) -> [String] {
        results.map { r in
            if r.ok {
                let latency = (r.latency?.isEmpty == false) ? " (\(r.latency!))" : ""
                return "\(r.modelID): PASS\(latency)"
            }
            let code = (r.code?.isEmpty == false) ? r.code! : "failed"
            return "\(r.modelID): FAIL (\(code))"
        }
    }
}
