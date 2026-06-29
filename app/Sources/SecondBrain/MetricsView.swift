import SwiftUI

// MARK: - Decoding (matches `2nb metrics --json`: {last_build, gauges, recent, aggregates})

struct VaultMetrics: Codable {
    let lastBuild: MetricOperation?
    let gauges: MetricGauges
    let recent: [MetricOperation]
    let aggregates: [String: MetricAggregate]

    enum CodingKeys: String, CodingKey {
        case lastBuild = "last_build"
        case gauges, recent, aggregates
    }
}

/// One recorded operation. Index/embed and query fields are `omitempty` in the
/// CLI, so they decode as optional here.
struct MetricOperation: Codable, Identifiable {
    let rowID: Int?
    let ts: String
    let operation: String
    let source: String
    let durationMs: Int
    let ok: Bool
    let error: String?

    let filesScanned: Int?
    let docsIndexed: Int?
    let chunksCreated: Int?
    let linksFound: Int?
    let embedded: Int?
    let embedSkipped: Int?
    let embedFailed: Int?
    let embedMs: Int?
    let totalChars: Int?
    let embeddingModel: String?
    let embeddingDims: Int?

    let resultCount: Int?
    let mode: String?
    let cliVersion: String?

    let docsPerSec: Double?
    let embeddingsPerSec: Double?
    let charsPerSec: Double?

    var id: String { "\(rowID ?? 0)-\(ts)-\(operation)-\(source)" }

    enum CodingKeys: String, CodingKey {
        case rowID = "id"
        case ts, operation, source, ok, error, mode, embedded
        case durationMs = "duration_ms"
        case filesScanned = "files_scanned"
        case docsIndexed = "docs_indexed"
        case chunksCreated = "chunks_created"
        case linksFound = "links_found"
        case embedSkipped = "embed_skipped"
        case embedFailed = "embed_failed"
        case embedMs = "embed_ms"
        case totalChars = "total_chars"
        case embeddingModel = "embedding_model"
        case embeddingDims = "embedding_dims"
        case resultCount = "result_count"
        case cliVersion = "cli_version"
        case docsPerSec = "docs_per_sec"
        case embeddingsPerSec = "embeddings_per_sec"
        case charsPerSec = "chars_per_sec"
    }
}

struct MetricGauges: Codable {
    let docCount: Int
    let embeddedCount: Int
    let embeddingCoverage: Double
    let chunkCount: Int
    let staleCount: Int
    let indexDbBytes: Int
    let walBytes: Int
    let lastIndexAt: String?
    let embeddingModel: String?
    let embeddingDims: Int?

    enum CodingKeys: String, CodingKey {
        case docCount = "doc_count"
        case embeddedCount = "embedded_count"
        case embeddingCoverage = "embedding_coverage"
        case chunkCount = "chunk_count"
        case staleCount = "stale_count"
        case indexDbBytes = "index_db_bytes"
        case walBytes = "wal_bytes"
        case lastIndexAt = "last_index_at"
        case embeddingModel = "embedding_model"
        case embeddingDims = "embedding_dims"
    }
}

struct MetricAggregate: Codable {
    let count: Int
    let avgMs: Double
    let p50Ms: Int
    let avgDocsPerSec: Double?

    enum CodingKeys: String, CodingKey {
        case count
        case avgMs = "avg_ms"
        case p50Ms = "p50_ms"
        case avgDocsPerSec = "avg_docs_per_sec"
    }
}

// MARK: - View

struct MetricsView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    var isInline: Bool = false

    // Stable display order for the per-operation aggregate table.
    private let opOrder = ["index", "reembed", "index_doc", "search", "ask"]

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Performance")
                    .font(.title2.bold())
                Spacer()
                Button {
                    Task { await appState.refreshMetrics() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain)
                .disabled(appState.metricsLoading)
                if !isInline {
                    Button("Done") { isPresented = false }
                        .keyboardShortcut(.defaultAction)
                }
            }
            .padding()

            Divider()

            content
        }
        .frame(minWidth: 560, minHeight: 480)
        .onAppear {
            Task { await appState.refreshMetrics() }
        }
    }

    @ViewBuilder
    private var content: some View {
        if appState.metricsLoading && appState.vaultMetrics == nil {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let m = appState.vaultMetrics {
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    lastBuildSection(m.lastBuild)
                    gaugesSection(m.gauges)
                    if !m.aggregates.isEmpty {
                        aggregatesSection(m.aggregates)
                    }
                    if !m.recent.isEmpty {
                        recentSection(m.recent)
                    }
                }
                .padding()
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        } else {
            VStack(spacing: 8) {
                Image(systemName: "speedometer")
                    .font(.system(size: 32))
                    .foregroundStyle(.secondary)
                Text("No metrics yet")
                    .font(.headline)
                Text("Run an index, search, or ask — index/search/ask timing is recorded automatically and shows up here.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 40)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    // MARK: Last build

    @ViewBuilder
    private func lastBuildSection(_ b: MetricOperation?) -> some View {
        sectionCard {
            if let b {
                HStack {
                    Text("Last \(b.operation == "reembed" ? "re-embed" : "index") build")
                        .font(.headline)
                    Spacer()
                    Text(relativeDate(b.ts))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if !b.ok {
                    Label(b.error ?? "failed", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
                HStack(spacing: 24) {
                    bigStat(duration(b.durationMs), "build time")
                    if let d = b.docsPerSec, d > 0 {
                        bigStat(String(format: "%.1f", d), "docs/sec")
                    }
                    if let e = b.embeddingsPerSec, e > 0 {
                        bigStat(String(format: "%.1f", e), "embeddings/sec")
                    }
                }
                .padding(.top, 2)
                statRow("Indexed", "\(b.docsIndexed ?? 0) docs, \(b.chunksCreated ?? 0) chunks, \(b.linksFound ?? 0) links")
                if (b.embedded ?? 0) > 0 || (b.embedFailed ?? 0) > 0 {
                    statRow("Embedded", "\(b.embedded ?? 0) (\(b.embedFailed ?? 0) failed, \(b.embedSkipped ?? 0) skipped)")
                }
                if let model = b.embeddingModel, !model.isEmpty {
                    statRow("Model", model)
                }
            } else {
                Text("No index build recorded yet")
                    .font(.headline)
                Text("Run `2nb index` (or Sync from the Index card) to build the vault.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    // MARK: Live gauges

    @ViewBuilder
    private func gaugesSection(_ g: MetricGauges) -> some View {
        sectionCard {
            Text("Vault gauges")
                .font(.headline)
            statRow("Documents", "\(g.docCount)  (\(g.embeddedCount) embedded, \(Int((g.embeddingCoverage * 100).rounded()))% coverage)")
            statRow("Chunks", "\(g.chunkCount)")
            statRow("Stale (90d+)", "\(g.staleCount)")
            statRow("Index DB", "\(bytes(g.indexDbBytes))  (+\(bytes(g.walBytes)) WAL)")
            if let model = g.embeddingModel, !model.isEmpty {
                statRow("Embedding", "\(model)\(g.embeddingDims.map { "  (\($0) dims)" } ?? "")")
            }
        }
    }

    // MARK: Per-operation aggregates

    @ViewBuilder
    private func aggregatesSection(_ aggs: [String: MetricAggregate]) -> some View {
        sectionCard {
            Text("Per-operation (recent window)")
                .font(.headline)
            ForEach(opOrder, id: \.self) { op in
                if let a = aggs[op] {
                    HStack {
                        Label(opLabel(op), systemImage: opIcon(op))
                            .font(.callout)
                            .frame(width: 150, alignment: .leading)
                        Text("n=\(a.count)")
                            .font(.callout.monospacedDigit())
                            .foregroundStyle(.secondary)
                            .frame(width: 60, alignment: .leading)
                        Text("avg \(durationD(a.avgMs))")
                            .font(.callout.monospacedDigit())
                            .frame(width: 100, alignment: .leading)
                        Text("p50 \(duration(a.p50Ms))")
                            .font(.callout.monospacedDigit())
                            .foregroundStyle(.secondary)
                        Spacer()
                        if let r = a.avgDocsPerSec, r > 0 {
                            Text(String(format: "%.1f docs/s", r))
                                .font(.caption.monospacedDigit())
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }

    // MARK: Recent operations

    @ViewBuilder
    private func recentSection(_ recent: [MetricOperation]) -> some View {
        sectionCard {
            Text("Recent operations")
                .font(.headline)
            ForEach(recent.prefix(30)) { op in
                HStack(spacing: 10) {
                    Image(systemName: opIcon(op.operation))
                        .foregroundStyle(op.ok ? Color.secondary : Color.orange)
                        .frame(width: 18)
                    Text(opLabel(op.operation))
                        .font(.callout)
                        .frame(width: 110, alignment: .leading)
                    Text(duration(op.durationMs))
                        .font(.callout.monospacedDigit())
                        .frame(width: 70, alignment: .leading)
                    Text(recentDetail(op))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    if op.source != "cli" {
                        Text(op.source)
                            .font(.caption2)
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(Color.secondary.opacity(0.15))
                            .clipShape(Capsule())
                    }
                    Spacer()
                    Text(relativeDate(op.ts))
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
                .padding(.vertical, 1)
            }
        }
    }

    // MARK: Helpers

    @ViewBuilder
    private func sectionCard<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            content()
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }

    private func bigStat(_ value: String, _ label: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(value).font(.title3.bold().monospacedDigit())
            Text(label).font(.caption).foregroundStyle(.secondary)
        }
    }

    private func statRow(_ label: String, _ value: String) -> some View {
        HStack(alignment: .top) {
            Text(label).font(.callout).foregroundStyle(.secondary)
            Spacer()
            Text(value).font(.callout.monospacedDigit()).multilineTextAlignment(.trailing)
        }
    }

    private func recentDetail(_ op: MetricOperation) -> String {
        switch op.operation {
        case "search", "ask":
            // MCP-sourced query rows don't carry result_count yet; only show it when present.
            if let r = op.resultCount, r > 0 { return "\(r) results" }
            return op.mode ?? ""
        default:
            return "\((op.docsIndexed ?? 0)) docs"
        }
    }

    private func opLabel(_ op: String) -> String {
        switch op {
        case "index": return "Index"
        case "reembed": return "Re-embed"
        case "index_doc": return "Index doc"
        case "search": return "Search"
        case "ask": return "Ask"
        default: return op
        }
    }

    private func opIcon(_ op: String) -> String {
        switch op {
        case "index", "reembed": return "arrow.triangle.2.circlepath"
        case "index_doc": return "doc.text"
        case "search": return "magnifyingglass"
        case "ask": return "bubble.left.and.bubble.right"
        default: return "circle"
        }
    }

    private func duration(_ ms: Int) -> String { durationD(Double(ms)) }

    private func durationD(_ ms: Double) -> String {
        if ms < 1000 { return "\(Int(ms.rounded()))ms" }
        if ms < 60000 { return String(format: "%.1fs", ms / 1000) }
        let total = Int(ms)
        return "\(total / 60000)m\(String(format: "%02d", (total % 60000) / 1000))s"
    }

    private func bytes(_ n: Int) -> String {
        let units = ["B", "KB", "MB", "GB", "TB"]
        var v = Double(n), i = 0
        while v >= 1024 && i < units.count - 1 { v /= 1024; i += 1 }
        return i == 0 ? "\(n) B" : String(format: "%.1f %@", v, units[i])
    }

    private func relativeDate(_ raw: String) -> String {
        let iso = ISO8601DateFormatter()
        if let date = iso.date(from: raw) {
            let fmt = RelativeDateTimeFormatter()
            fmt.unitsStyle = .short
            return fmt.localizedString(for: date, relativeTo: Date())
        }
        return raw
    }
}
