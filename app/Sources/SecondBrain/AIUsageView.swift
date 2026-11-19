import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// Full AI usage ledger, cloud cost summary, and re-embed estimation view
/// hosted under Activity > AI Usage & Cost.
struct AIUsageView: View {
    @Environment(AppState.self) private var appState
    @State private var busyClearing = false
    @State private var statusMessage: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                headerBar

                if appState.vault == nil {
                    ThemedCard(style: .subtle, padding: 16) {
                        Text("Open an Obsidian vault to view AI usage and estimated cloud costs.")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                    }
                } else {
                    windowSummaryGrid
                    reembedEstimateCard
                    modelBreakdownCard
                    recentOperationsCard
                }

                if let statusMessage {
                    ThemedCard(style: .subtle, padding: 10) {
                        Text(statusMessage)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .padding(24)
            .frame(maxWidth: 920, alignment: .leading)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .accessibilityIdentifier("aiUsage.view")
        .task(id: appState.vault?.rootURL) {
            await appState.refreshMetrics(limit: 30)
            applyButtonCommand(appState.guiInputBridge.pendingButtonCommand)
        }
        .onChange(of: appState.guiInputBridge.pendingButtonCommand) { _, cmd in
            applyButtonCommand(cmd)
        }
    }

    // MARK: - Header

    private var headerBar: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text("AI Usage & Estimated Cloud Cost")
                    .font(.title3.weight(.bold))
                Text("Tracks all CLI and MCP model calls locally in `.2ndbrain/metrics.db`.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer(minLength: 12)

            if appState.metricsLoading {
                ProgressView()
                    .controlSize(.small)
            }

            Button {
                Task { await appState.refreshMetrics(limit: 30) }
            } label: {
                Label("Refresh", systemImage: "arrow.clockwise")
            }
            .controlSize(.small)
            .accessibilityIdentifier("aiUsage.refreshButton")
            .disabled(appState.vault == nil || appState.metricsLoading)

            Button("Reset History…") {
                confirmAndClearHistory()
            }
            .controlSize(.small)
            .accessibilityIdentifier("aiUsage.clearButton")
            .disabled(appState.vault == nil || busyClearing || appState.isIndexing)
        }
    }

    // MARK: - Time Windows

    private var windowSummaryGrid: some View {
        let usage = appState.metricsReport?.aiUsage
        let windows: [(String, AIUsageWindowSummary?)] = [
            ("Today", usage?.today),
            ("Last 7 Days", usage?.last7d),
            ("Last 30 Days", usage?.last30d),
            ("All-Time", usage?.allTime),
        ]
        return LazyVGrid(
            columns: [GridItem(.adaptive(minimum: 185, maximum: 240), spacing: 12)],
            alignment: .leading,
            spacing: 12
        ) {
            ForEach(windows, id: \.0) { title, summary in
                windowCard(title: title, summary: summary)
            }
        }
    }

    private func windowCard(title: String, summary: AIUsageWindowSummary?) -> some View {
        ThemedCard(style: .subtle, padding: 14) {
            VStack(alignment: .leading, spacing: 6) {
                Text(title)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)

                if let s = summary {
                    Text(Formatters.usd(s.cloudCostUSD, knownPricing: s.knownPricing))
                        .font(.title2.weight(.bold).monospacedDigit())
                        .foregroundStyle(s.cloudCostUSD > 0 ? .primary : .secondary)

                    Text("\(s.cloudCalls) cloud · \(s.localCalls) on-device ($0.00)")
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)

                    Text("\(Formatters.tokens(s.cloudInputTokens + s.cloudOutputTokens)) cloud · \(Formatters.tokens(s.localTokens)) local tokens")
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.tertiary)

                    if s.unpricedCalls > 0 {
                        Label("\(s.unpricedCalls) unpriced call(s)", systemImage: "questionmark.circle")
                            .font(.caption2)
                            .foregroundStyle(.orange)
                    }
                } else {
                    Text("$0.00")
                        .font(.title2.weight(.bold).monospacedDigit())
                        .foregroundStyle(.secondary)
                    Text("No recorded usage")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    // MARK: - Re-embed Estimate

    private var reembedEstimateCard: some View {
        ThemedCard(style: .prominent, padding: 16) {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    SheetSectionHeader(title: "Vault Re-embed Estimate", systemImage: "clock.arrow.2.circlepath")
                    Spacer()
                    if let est = appState.metricsReport?.reembedEstimate, est.embeddableDocs > 0 {
                        Button("Re-embed All…") {
                            appState.rebuildIndex(forceReembed: true)
                        }
                        .controlSize(.small)
                        .disabled(appState.isIndexing)
                    }
                }

                if let est = appState.metricsReport?.reembedEstimate, est.embeddableDocs > 0 {
                    let sourceText = est.speedSource == "history"
                        ? String(format: "%.1f docs/s (vault history)", locale: Locale(identifier: "en_US_POSIX"), est.docsPerSec)
                        : String(format: "%.1f docs/s (baseline)", locale: Locale(identifier: "en_US_POSIX"), est.docsPerSec)
                    let costText = est.isCloud
                        ? Formatters.usd(est.estimatedCostUSD, knownPricing: est.knownPricing)
                        : "$0.00 (on-device)"

                    LabeledContent("Estimated Time") {
                        Text("~\(Formatters.eta(est.estimatedSeconds)) · \(sourceText)")
                            .font(.callout.monospacedDigit().weight(.medium))
                    }
                    LabeledContent("Corpus Size") {
                        Text("\(est.embeddableDocs) notes · \(est.totalChunks) chunks · ~\(Formatters.tokens(est.estimatedTokens)) tokens")
                            .font(.callout.monospacedDigit())
                            .foregroundStyle(.secondary)
                    }
                    LabeledContent("Active Embedder") {
                        Text("\(ProviderDisplay.name(est.provider)) · \(est.model) · \(costText)")
                            .font(.callout.monospaced())
                            .foregroundStyle(.secondary)
                    }
                } else {
                    Text("Index your vault first so `index.db` chunk sizing is available for estimates.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    // MARK: - Per-Model Breakdown

    private var modelBreakdownCard: some View {
        let rows = appState.metricsReport?.aiUsage?.byModel ?? []
        return ThemedCard(style: .subtle, padding: 16) {
            VStack(alignment: .leading, spacing: 10) {
                SheetSectionHeader(title: "Usage by Model & Slot (All-Time)", systemImage: "cpu")

                if rows.isEmpty {
                    Text("No model invocations recorded yet.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    VStack(spacing: 8) {
                        ForEach(rows) { row in
                            HStack(alignment: .center, spacing: 10) {
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 6) {
                                        Text(row.model.isEmpty ? "(default)" : row.model)
                                            .font(.callout.monospaced().weight(.medium))
                                            .lineLimit(1)
                                            .truncationMode(.middle)
                                        Text(ProviderDisplay.name(row.provider))
                                            .font(.caption2)
                                            .foregroundStyle(.secondary)
                                        if !row.slot.isEmpty {
                                            Text(row.slot)
                                                .font(.caption2.weight(.medium))
                                                .padding(.horizontal, 6)
                                                .padding(.vertical, 1)
                                                .background(Color.primary.opacity(0.06), in: Capsule())
                                                .foregroundStyle(.secondary)
                                        }
                                    }
                                    let tokenDetail = row.outputTokens > 0
                                        ? "\(Formatters.tokens(row.inputTokens)) in / \(Formatters.tokens(row.outputTokens)) out"
                                        : "\(Formatters.tokens(row.inputTokens)) in"
                                    Text("\(row.calls) call(s) · \(tokenDetail) · \(row.priceLabel)")
                                        .font(.caption2.monospacedDigit())
                                        .foregroundStyle(.secondary)
                                }

                                Spacer()

                                Text(row.isCloud
                                     ? Formatters.usd(row.estimatedCostUSD, knownPricing: row.knownPricing)
                                     : "$0.00")
                                    .font(.callout.monospacedDigit().weight(.semibold))
                                    .foregroundStyle(row.isCloud ? .primary : .secondary)
                            }
                            if row.id != rows.last?.id {
                                Divider()
                            }
                        }
                    }
                }
            }
        }
    }

    // MARK: - Recent Operations

    private var recentOperationsCard: some View {
        let recent = appState.metricsReport?.recent ?? []
        return ThemedCard(style: .subtle, padding: 16) {
            VStack(alignment: .leading, spacing: 10) {
                SheetSectionHeader(title: "Recent Operations", systemImage: "list.bullet.rectangle")

                if recent.isEmpty {
                    Text("No recent operations logged.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    VStack(spacing: 6) {
                        ForEach(recent.prefix(15)) { op in
                            HStack(alignment: .center, spacing: 10) {
                                Image(systemName: op.ok ? "checkmark.circle.fill" : "xmark.circle.fill")
                                    .font(.caption)
                                    .foregroundStyle(op.ok ? .green : .red)

                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 6) {
                                        Text(op.operation)
                                            .font(.caption.monospaced().weight(.semibold))
                                        Text("· \(op.source)")
                                            .font(.caption2)
                                            .foregroundStyle(.secondary)
                                        Text("· \(Formatters.relativeDate(op.timestamp))")
                                            .font(.caption2)
                                            .foregroundStyle(.tertiary)
                                    }
                                    let modelSummary = [op.generationModel, op.embeddingModel]
                                        .compactMap { $0 }
                                        .filter { !$0.isEmpty }
                                        .joined(separator: " + ")
                                    let tokIn = op.inputTokens ?? 0
                                    let tokOut = op.outputTokens ?? 0
                                    Text("\(Formatters.duration(Int(op.durationMs)))\(modelSummary.isEmpty ? "" : " · \(modelSummary)")\(tokIn + tokOut > 0 ? " · \(Formatters.tokens(tokIn + tokOut)) tok" : "")")
                                        .font(.caption2.monospacedDigit())
                                        .foregroundStyle(.secondary)
                                }

                                Spacer()

                                if let cost = op.costUSD, op.knownPricing == true {
                                    Text(Formatters.usd(cost, knownPricing: true))
                                        .font(.caption.monospacedDigit().weight(.medium))
                                } else {
                                    Text("$0.00")
                                        .font(.caption.monospacedDigit())
                                        .foregroundStyle(.secondary)
                                }
                            }
                            if op.id != recent.prefix(15).last?.id {
                                Divider()
                            }
                        }
                    }
                }
            }
        }
    }

    // MARK: - Actions & GUI Automation

    private func confirmAndClearHistory() {
        #if canImport(AppKit)
        if ProcessInfo.processInfo.environment["2NB_TEST"] != "1" && LaunchOptions.current.guiControlDir == nil {
            let alert = NSAlert()
            alert.messageText = "Reset AI usage and metrics history?"
            alert.informativeText = "Clears recorded AI call counts, token totals, and historical speed samples in metrics.db for this vault."
            alert.addButton(withTitle: "Reset")
            alert.addButton(withTitle: "Cancel")
            guard alert.runModal() == .alertFirstButtonReturn else { return }
        }
        #endif
        Task {
            busyClearing = true
            defer { busyClearing = false }
            do {
                try await appState.clearMetricsHistory()
                statusMessage = "Cleared AI usage and metrics history."
            } catch {
                statusMessage = error.localizedDescription
            }
        }
    }

    private func applyButtonCommand(_ cmd: GUIButtonCommand?) {
        guard let cmd else { return }
        switch cmd.targetIdentifier {
        case "aiUsage.refreshButton":
            guard appState.vault != nil, !appState.metricsLoading else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Refresh")
            Task { await appState.refreshMetrics(limit: 30) }
        case "aiUsage.clearButton":
            guard appState.vault != nil, !busyClearing, !appState.isIndexing else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Reset History…")
            confirmAndClearHistory()
        default:
            break
        }
    }
}
