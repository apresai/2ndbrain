import Foundation
import Observation
import os

enum GUIActionCategory: String, Codable, CaseIterable, Sendable {
    case app
    case navigation
    case vault
    case index
    case search
    case ask
    case settings
    case lint
    case polish
    case mcp
    case git
    case cli
    case engine

    var displayLabel: String {
        switch self {
        case .app: return "App"
        case .navigation: return "Nav"
        case .vault: return "Vault"
        case .index: return "Index"
        case .search: return "Search"
        case .ask: return "Ask"
        case .settings: return "Settings"
        case .lint: return "Lint"
        case .polish: return "Polish"
        case .mcp: return "MCP"
        case .git: return "Git"
        case .cli: return "CLI"
        case .engine: return "Engine"
        }
    }
}

enum GUIActionPhase: String, Codable, Sendable {
    case started
    case progress
    case succeeded
    case failed
}

struct GUIActionEvent: Codable, Identifiable, Equatable, Sendable {
    let id: String
    let seq: Int
    let timestamp: String
    let category: GUIActionCategory
    let action: String
    let phase: GUIActionPhase
    let durationMs: Int?
    let summary: String
    let detail: String?
    let metadata: [String: String]
}

struct GUIStatusSnapshot: Codable, Equatable, Sendable {
    var updatedAt: String
    var pid: Int32
    var appVersion: String
    var cliVersion: String?
    var vaultPath: String?
    var vaultName: String?
    var fileCount: Int

    var activeTab: String
    var activeSection: String?
    var askSearchMode: String?
    var hudVisible: Bool
    var onboardingVisible: Bool
    var indexSheetVisible: Bool

    var isIndexing: Bool
    var indexPhase: String?
    var indexForceReembed: Bool
    var indexFraction: Double?
    var filesIndexed: Int
    var docsIndexed: Int
    var chunksCreated: Int
    var embeddingCurrent: Int
    var embeddingTotal: Int
    var lastIndexError: String?
    var lastIndexingProblemPath: String?
    var lastIndexingProblemMessage: String?

    var aiProvider: String?
    var embedProvider: String?
    var genProvider: String?
    var genModel: String?
    var embedModel: String?
    var dimensions: Int?
    var embedAvailable: Bool
    var genAvailable: Bool
    var documentCount: Int
    var embeddingCount: Int
    var portabilityStatus: String?
    var semanticWarnings: [String]

    var lastSearchQuery: String?
    var lastSearchPhase: String?
    var lastSearchMode: String?
    var lastSearchHitCount: Int?
    var lastSearchTopPath: String?
    var lastAskQuestion: String?
    var lastAskStatus: String?
    var lastAskSourcesCount: Int?

    // MARK: - Local Model Provisioning & Tuning Telemetry
    var localModelDownloading: Bool
    var localModelDownloadCurrent: String?
    var localModelDownloadDone: Int64?
    var localModelDownloadTotal: Int64?
    var localModelDownloadFraction: Double?
    var localModelCompletedCount: Int?
    var localModelTotalCount: Int?
    var localModelDownloadError: String?

    // MARK: - Local Engine Daemon Telemetry
    var localEngineRunning: Bool
    var localEnginePid: Int32?
    var localEngineHealthy: Bool
    var localEngineRoles: [String]?
    var localEngineError: String?

    // MARK: - Local Tuning Telemetry
    var localMatryoshkaDim: Int?
    var localTaskPrefixPreset: String?

    // MARK: - Model Probe & Doctor Telemetry
    var lastGenProbeModel: String?
    var lastGenProbeProvider: String?
    var lastGenProbeOK: Bool?
    var lastGenProbeDetail: String?
    var lastGenProbeLatency: String?
    var lastEmbedProbeModel: String?
    var lastEmbedProbeProvider: String?
    var lastEmbedProbeOK: Bool?
    var lastEmbedProbeDetail: String?
    var lastEmbedProbeLatency: String?
    var lastModelsTestOK: Bool?
    var lastDoctorOK: Bool?
    var lastDoctorCredentials: String?
    var lastDoctorSummary: String?

    // MARK: - Re-embed ETA & AI Usage Telemetry
    var indexEstimatedRemainingSeconds: Double?
    var indexDocsPerSec: Double?
    var aiUsage30dCostUSD: Double?
    var aiUsage30dCloudCalls: Int?
    var aiUsage30dLocalCalls: Int?

    var lastError: String?
    var eventCount: Int
    var lastActionSeq: Int
    var lastActionSummary: String?

    static func initial(appVersion: String, timestamp: String) -> GUIStatusSnapshot {
        GUIStatusSnapshot(
            updatedAt: timestamp,
            pid: ProcessInfo.processInfo.processIdentifier,
            appVersion: appVersion,
            cliVersion: nil,
            vaultPath: nil,
            vaultName: nil,
            fileCount: 0,
            activeTab: DashboardTab.home.rawValue,
            activeSection: nil,
            askSearchMode: nil,
            hudVisible: false,
            onboardingVisible: false,
            indexSheetVisible: false,
            isIndexing: false,
            indexPhase: nil,
            indexForceReembed: false,
            indexFraction: nil,
            filesIndexed: 0,
            docsIndexed: 0,
            chunksCreated: 0,
            embeddingCurrent: 0,
            embeddingTotal: 0,
            lastIndexError: nil,
            lastIndexingProblemPath: nil,
            lastIndexingProblemMessage: nil,
            aiProvider: nil,
            embedProvider: nil,
            genProvider: nil,
            genModel: nil,
            embedModel: nil,
            dimensions: nil,
            embedAvailable: false,
            genAvailable: false,
            documentCount: 0,
            embeddingCount: 0,
            portabilityStatus: nil,
            semanticWarnings: [],
            lastSearchQuery: nil,
            lastSearchPhase: nil,
            lastSearchMode: nil,
            lastSearchHitCount: nil,
            lastSearchTopPath: nil,
            lastAskQuestion: nil,
            lastAskStatus: nil,
            lastAskSourcesCount: nil,
            localModelDownloading: false,
            localModelDownloadCurrent: nil,
            localModelDownloadDone: nil,
            localModelDownloadTotal: nil,
            localModelDownloadFraction: nil,
            localModelCompletedCount: nil,
            localModelTotalCount: nil,
            localModelDownloadError: nil,
            localEngineRunning: false,
            localEnginePid: nil,
            localEngineHealthy: false,
            localEngineRoles: nil,
            localEngineError: nil,
            localMatryoshkaDim: nil,
            localTaskPrefixPreset: nil,
            lastGenProbeModel: nil,
            lastGenProbeProvider: nil,
            lastGenProbeOK: nil,
            lastGenProbeDetail: nil,
            lastGenProbeLatency: nil,
            lastEmbedProbeModel: nil,
            lastEmbedProbeProvider: nil,
            lastEmbedProbeOK: nil,
            lastEmbedProbeDetail: nil,
            lastEmbedProbeLatency: nil,
            lastModelsTestOK: nil,
            lastDoctorOK: nil,
            lastDoctorCredentials: nil,
            lastDoctorSummary: nil,
            indexEstimatedRemainingSeconds: nil,
            indexDocsPerSec: nil,
            aiUsage30dCostUSD: nil,
            aiUsage30dCloudCalls: nil,
            aiUsage30dLocalCalls: nil,
            lastError: nil,
            eventCount: 0,
            lastActionSeq: 0,
            lastActionSummary: nil
        )
    }
}

/// Serialized file writer for `gui-actions.jsonl` and atomic `gui-state.json`.
/// Isolated from `@MainActor` state so disk I/O never blocks SwiftUI frames.
private final class GUIActionDiskWriter: @unchecked Sendable {
    private let queue = DispatchQueue(label: "dev.apresai.2ndbrain.gui-tracker")
    private let primaryLogDir: URL
    private let maxLogBytes: UInt64 = 5 * 1024 * 1024
    private var rotatedDirs: Set<URL> = []

    init(primaryLogDir: URL) {
        self.primaryLogDir = primaryLogDir.standardizedFileURL
    }

    func enqueueWrite(eventLine: Data?, stateData: Data, vaultDotDir: URL?) {
        queue.async { [weak self] in
            self?.writeSync(eventLine: eventLine, stateData: stateData, vaultDotDir: vaultDotDir)
        }
    }

    func enqueueStateWrite(stateData: Data, vaultDotDir: URL?) {
        queue.async { [weak self] in
            self?.writeSync(eventLine: nil, stateData: stateData, vaultDotDir: vaultDotDir)
        }
    }

    func flush() {
        queue.sync {}
    }

    private func writeSync(eventLine: Data?, stateData: Data, vaultDotDir: URL?) {
        var targetDirs: [URL] = [primaryLogDir]
        if let vaultDotDir, isExistingDirectory(vaultDotDir) {
            let vaultLogs = vaultDotDir.appendingPathComponent("logs", isDirectory: true).standardizedFileURL
            if vaultLogs != primaryLogDir {
                targetDirs.append(vaultLogs)
            }
        }

        for dir in targetDirs {
            try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            if let eventLine {
                let actionsURL = dir.appendingPathComponent("gui-actions.jsonl")
                rotateIfNeeded(at: actionsURL, in: dir)
                appendLine(eventLine, to: actionsURL)
            }
            let stateURL = dir.appendingPathComponent("gui-state.json")
            try? stateData.write(to: stateURL, options: .atomic)
        }
    }

    private func isExistingDirectory(_ url: URL) -> Bool {
        var isDir: ObjCBool = false
        return FileManager.default.fileExists(atPath: url.path, isDirectory: &isDir) && isDir.boolValue
    }

    private func rotateIfNeeded(at url: URL, in dir: URL) {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: url.path),
              let size = attrs[.size] as? UInt64,
              size > maxLogBytes else {
            return
        }
        let prevURL = dir.appendingPathComponent("gui-actions.prev.jsonl")
        try? FileManager.default.removeItem(at: prevURL)
        do {
            try FileManager.default.moveItem(at: url, to: prevURL)
            rotatedDirs.insert(dir)
        } catch {
            // Best-effort rotation.
        }
    }

    private func appendLine(_ data: Data, to fileURL: URL) {
        if let handle = try? FileHandle(forWritingTo: fileURL) {
            defer { try? handle.close() }
            do {
                try handle.seekToEnd()
                try handle.write(contentsOf: data)
            } catch {
                // Best-effort diagnostics write.
            }
        } else {
            try? data.write(to: fileURL, options: .atomic)
        }
    }
}

/// Tracks all GUI user actions and live application status in memory, on OSLog,
/// and on disk (`gui-actions.jsonl` + `gui-state.json`) with secret redaction.
@Observable @MainActor
final class GUIActionTracker {
    private static let logger = Logger(subsystem: "dev.apresai.2ndbrain", category: "gui-action")
    private static let maxInMemoryEvents = 300
    nonisolated static let maxStatusErrorChars = 1000
    nonisolated static let maxEventDetailChars = 8000

    private(set) var events: [GUIActionEvent] = []
    private(set) var status: GUIStatusSnapshot

    let primaryLogDir: URL
    let persistenceEnabled: Bool

    @ObservationIgnored private var vaultDotDir: URL?
    @ObservationIgnored private var nextSeq: Int = 1
    @ObservationIgnored private let writer: GUIActionDiskWriter?

    init(
        overrideLogDir: URL? = nil,
        persistenceEnabled: Bool? = nil,
        appVersion: String = SecondBrain.appVersion
    ) {
        let defaultLogDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/SecondBrain", isDirectory: true)
        let resolvedDir = (overrideLogDir ?? defaultLogDir).standardizedFileURL
        self.primaryLogDir = resolvedDir

        let shouldPersist = persistenceEnabled
            ?? (overrideLogDir != nil || ProcessInfo.processInfo.environment["2NB_TEST"] != "1")
        self.persistenceEnabled = shouldPersist
        self.writer = shouldPersist ? GUIActionDiskWriter(primaryLogDir: resolvedDir) : nil
        self.status = GUIStatusSnapshot.initial(appVersion: appVersion, timestamp: Self.isoTimestamp())
    }

    var primaryActionsLogURL: URL {
        primaryLogDir.appendingPathComponent("gui-actions.jsonl")
    }

    var primaryStateFileURL: URL {
        primaryLogDir.appendingPathComponent("gui-state.json")
    }

    /// Prefers the per-vault `.2ndbrain/logs/gui-actions.jsonl` file when an initialized
    /// vault is bound and no explicit override directory was supplied.
    var activeActionsLogURL: URL {
        if LaunchOptions.current.guiLogDir == nil,
           let vaultDotDir,
           FileManager.default.fileExists(atPath: vaultDotDir.path) {
            return vaultDotDir.appendingPathComponent("logs/gui-actions.jsonl")
        }
        return primaryActionsLogURL
    }

    func setVaultDotDir(_ dotDir: URL?) {
        vaultDotDir = dotDir?.standardizedFileURL
    }

    @discardableResult
    func record(
        category: GUIActionCategory,
        action: String,
        phase: GUIActionPhase,
        durationMs: Int? = nil,
        summary: String,
        detail: String? = nil,
        metadata: [String: String] = [:]
    ) -> GUIActionEvent {
        let seq = nextSeq
        nextSeq += 1
        let now = Self.isoTimestamp()

        let safeSummary = Self.boundedRedactedTail(summary, limit: Self.maxEventDetailChars)
        let safeDetail = detail.map { Self.boundedRedactedTail($0, limit: Self.maxEventDetailChars) }
        var safeMetadata: [String: String] = [:]
        safeMetadata.reserveCapacity(metadata.count)
        for (key, value) in metadata {
            safeMetadata[key] = Self.boundedRedactedTail(value, limit: Self.maxEventDetailChars)
        }

        let event = GUIActionEvent(
            id: UUID().uuidString,
            seq: seq,
            timestamp: now,
            category: category,
            action: action,
            phase: phase,
            durationMs: durationMs,
            summary: safeSummary,
            detail: safeDetail,
            metadata: safeMetadata
        )

        events.insert(event, at: 0)
        if events.count > Self.maxInMemoryEvents {
            events.removeLast(events.count - Self.maxInMemoryEvents)
        }

        var nextStatus = status
        nextStatus.updatedAt = now
        nextStatus.eventCount = seq
        nextStatus.lastActionSeq = seq
        nextStatus.lastActionSummary = "\(action) (\(phase.rawValue)): \(safeSummary)"
        if phase == .failed {
            nextStatus.lastError = Self.boundedTail(safeDetail ?? safeSummary, limit: Self.maxStatusErrorChars)
        }
        status = nextStatus

        Self.logger.info("[#\(seq, privacy: .public)] [\(category.rawValue, privacy: .public)] \(action, privacy: .public) (\(phase.rawValue, privacy: .public)) — \(safeSummary, privacy: .public)")

        if let writer {
            let lineData = Self.encodeJSONLine(event)
            let stateData = Self.encodePrettyState(nextStatus)
            if let stateData {
                writer.enqueueWrite(eventLine: lineData, stateData: stateData, vaultDotDir: vaultDotDir)
            }
        }
        return event
    }

    func updateStatus(_ mutate: (inout GUIStatusSnapshot) -> Void) {
        var candidate = status
        mutate(&candidate)
        if candidate == status {
            return
        }
        candidate.updatedAt = Self.isoTimestamp()
        status = candidate

        if let writer, let stateData = Self.encodePrettyState(candidate) {
            writer.enqueueStateWrite(stateData: stateData, vaultDotDir: vaultDotDir)
        }
    }

    func clearSessionEvents() {
        events.removeAll()
        updateStatus { snapshot in
            snapshot.lastError = nil
            snapshot.lastIndexError = nil
        }
    }

    func flushForTesting() {
        writer?.flush()
    }

    /// Redacts sensitive API tokens/keys before storing or logging any GUI action payload.
    nonisolated static func redact(_ raw: String) -> String {
        var output = raw

        // 1. Well-known secret token prefixes
        output = output.replacing(/AIza[0-9A-Za-z_-]{20,}/, with: "<REDACTED>")
        output = output.replacing(/sk-(?:or-v1-|proj-|ant-)?[0-9A-Za-z_-]{8,}/, with: "<REDACTED>")
        output = output.replacing(/ABSK[0-9A-Za-z+\/_=-]{16,}/, with: "<REDACTED>")
        output = output.replacing(/AKIA[0-9A-Z]{16,}/, with: "<REDACTED>")

        // 2. Authorization Bearer tokens (closed or unclosed quotes)
        output = output.replacing(
            /(?i)(\bBearer\s+)(?:(\\")([^"\n\\]+)(\\")?|"([^"\n\\]+)(")?|'([^'\n]+)(')?|([^\s,"'\n\\}&\];]+))/
        ) { match in
            let prefix = match.output.1
            if match.output.2 != nil {
                let close = match.output.4 != nil ? "\\\"" : ""
                return "\(prefix)\\\"<REDACTED>\(close)"
            } else if match.output.5 != nil {
                let close = match.output.6 != nil ? "\"" : ""
                return "\(prefix)\"<REDACTED>\(close)"
            } else if match.output.7 != nil {
                let close = match.output.8 != nil ? "'" : ""
                return "\(prefix)'<REDACTED>\(close)"
            } else {
                return "\(prefix)<REDACTED>"
            }
        }

        // 3. Command-line flags (space-delimited or equals-delimited, closed or unclosed quotes)
        output = output.replacing(
            /(?i)(^|[\s"'\\])(--?(?:(?:[a-zA-Z0-9_-]+[-_])?(?:api[-_]?key|secret[-_]?key|access[-_]?key|auth[-_]?key|private[-_]?key|client[-_]?secret|app[-_]?secret|auth[-_]?token|access[-_]?token|bearer[-_]?token|refresh[-_]?token|password|passwd)|token|secret|key))(\s*=\s*|\s+)(?:(\\")([^"\n\\]+)(\\")?|"([^"\n\\]+)(")?|'([^'\n]+)(')?|([^\s,"'\n\\}&\];]+))/
        ) { match in
            let lead = match.output.1
            let flag = match.output.2
            let sep = match.output.3
            if match.output.4 != nil {
                let close = match.output.6 != nil ? "\\\"" : ""
                return "\(lead)\(flag)\(sep)\\\"<REDACTED>\(close)"
            } else if match.output.7 != nil {
                let close = match.output.8 != nil ? "\"" : ""
                return "\(lead)\(flag)\(sep)\"<REDACTED>\(close)"
            } else if match.output.9 != nil {
                let close = match.output.10 != nil ? "'" : ""
                return "\(lead)\(flag)\(sep)'<REDACTED>\(close)"
            } else {
                return "\(lead)\(flag)\(sep)<REDACTED>"
            }
        }

        // 4. Key-value pairs in JSON, environment variables, URLs, and configs (closed or unclosed quotes).
        // Match only the fixed sensitive suffix before `[:=]` (no unanchored `[a-zA-Z0-9_]*?` prefix)
        // so long underscore-connected identifiers without `[:=]` run in strict O(N) time.
        output = output.replacing(
            /(?i)((?:(?:api|secret|access|auth|private)[-_]?key|(?:client|app|api)[-_]?secret|_secret|\bsecret|token|password|passwd|\bkey)\\?["']?\s*[:=]\s*)(?:(\\")([^"\n\\]+)(\\")?|"([^"\n\\]+)(")?|'([^'\n]+)(')?|([^\s,"'\n\\}&\];]+))/
        ) { match in
            let keyPrefix = match.output.1
            if match.output.2 != nil {
                let close = match.output.4 != nil ? "\\\"" : ""
                return "\(keyPrefix)\\\"<REDACTED>\(close)"
            } else if match.output.5 != nil {
                let close = match.output.6 != nil ? "\"" : ""
                return "\(keyPrefix)\"<REDACTED>\(close)"
            } else if match.output.7 != nil {
                let close = match.output.8 != nil ? "'" : ""
                return "\(keyPrefix)'<REDACTED>\(close)"
            } else {
                return "\(keyPrefix)<REDACTED>"
            }
        }

        return output
    }

    /// Bounds `text` before running regex redaction so oversized CLI stderr streams
    /// cannot stall `@MainActor`, keeping a small leading margin to avoid splitting
    /// a secret across the truncation boundary.
    nonisolated static func boundedRedactedTail(_ text: String, limit: Int) -> String {
        let preLimit = limit > 1 ? limit + 4096 : limit
        let preBounded = boundedTail(text, limit: preLimit)
        let redacted = redact(preBounded)
        return boundedTail(redacted, limit: limit)
    }

    /// Bounds diagnostic strings to their trailing `limit` characters so large
    /// stderr streams stay concise in `gui-state.json` and `gui-actions.jsonl`.
    nonisolated static func boundedTail(_ text: String, limit: Int) -> String {
        guard limit > 1, text.count > limit else { return text }
        return "…" + String(text.suffix(limit - 1))
    }

    private static func isoTimestamp(_ date: Date = Date()) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.string(from: date)
    }

    private static func encodeJSONLine(_ event: GUIActionEvent) -> Data? {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        guard var data = try? encoder.encode(event) else { return nil }
        data.append(0x0A)
        return data
    }

    private static func encodePrettyState(_ snapshot: GUIStatusSnapshot) -> Data? {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        return try? encoder.encode(snapshot)
    }
}
