import SwiftUI
import SecondBrainCore

struct AITestView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    @State private var running: Bool = false
    @State private var results: [AIProbeResult] = []
    @State private var currentProbe: String? = nil
    @State private var autoRunOnAppear: Bool = true

    var body: some View {
        VStack(spacing: 0) {
            header

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    configSection
                    Divider()
                    resultsSection
                }
                .padding(20)
            }

            footer
        }
        .frame(width: 540, height: 500)
        .task {
            await appState.refreshAIStatus()
            if autoRunOnAppear {
                autoRunOnAppear = false
                await runTests()
            }
        }
    }

    // MARK: - Header / Footer

    private var header: some View {
        HStack {
            Image(systemName: "sparkles.rectangle.stack")
                .font(.title2)
                .foregroundStyle(Color.accentColor)
            Text("Test AI Connection")
                .font(.title2)
                .fontWeight(.semibold)
            Spacer()
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 14)
        .background(Color(nsColor: .windowBackgroundColor))
        .overlay(alignment: .bottom) { Divider() }
    }

    private var footer: some View {
        HStack {
            if hasFailures {
                Button("Open AI Setup...") {
                    isPresented = false
                    appState.showAISetupWizard = true
                }
            }
            Spacer()
            Button("Run Tests") {
                Task { await runTests() }
            }
            .disabled(running || appState.aiStatus == nil)
            Button("Close") {
                isPresented = false
            }
            .keyboardShortcut(.cancelAction)
        }
        .padding(16)
        .background(Color(nsColor: .windowBackgroundColor))
        .overlay(alignment: .top) { Divider() }
    }

    // MARK: - Sections

    private var configSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Configuration", systemImage: "gearshape")

            if let status = appState.aiStatus {
                LabeledContent("Provider", value: status.provider.capitalized)
                LabeledContent("Embedding Model", value: status.embeddingModel.isEmpty ? "—" : status.embeddingModel)
                LabeledContent("Generation Model", value: status.genModel.isEmpty ? "—" : status.genModel)
            } else {
                Text("No AI configured")
                    .foregroundStyle(.secondary)
                Button("Run AI Setup...") {
                    isPresented = false
                    appState.showAISetupWizard = true
                }
                .padding(.top, 4)
            }
        }
    }

    private var resultsSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Probe Results", systemImage: "checkmark.seal")

            if running, let probing = currentProbe {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text(probing)
                        .foregroundStyle(.secondary)
                }
                .padding(.top, 4)
            }

            if results.isEmpty && !running {
                Text("No tests run yet. Click Run Tests to verify both models respond.")
                    .foregroundStyle(.secondary)
                    .font(.callout)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 4)
            }

            ForEach(results, id: \.modelID) { result in
                resultRow(result)
            }
        }
    }

    private func resultRow(_ result: AIProbeResult) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: result.ok ? "checkmark.circle.fill" : "xmark.circle.fill")
                .foregroundStyle(result.ok ? Color.green : Color.red)
                .font(.title3)

            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(result.modelType.capitalized)
                        .font(.callout)
                        .fontWeight(.medium)
                    Text(result.modelID)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    if !result.latency.isEmpty {
                        Text(result.latency)
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                            .monospacedDigit()
                    }
                }
                if let detail = result.detail, !detail.isEmpty {
                    Text(detail)
                        .font(.caption)
                        .foregroundStyle(result.ok ? AnyShapeStyle(HierarchicalShapeStyle.secondary) : AnyShapeStyle(Color.red))
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .padding(10)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    // MARK: - Helpers

    private var hasFailures: Bool {
        !results.isEmpty && results.contains { !$0.ok }
    }

    private func runTests() async {
        guard let status = appState.aiStatus else { return }
        running = true
        results = []

        if !status.embeddingModel.isEmpty {
            currentProbe = "Testing embedding model..."
            do {
                let r = try await appState.testModel(
                    provider: status.provider,
                    modelID: status.embeddingModel,
                    modelType: "embedding"
                )
                results.append(r)
            } catch {
                results.append(AIProbeResult(
                    modelID: status.embeddingModel,
                    provider: status.provider,
                    modelType: "embedding",
                    ok: false,
                    detail: error.localizedDescription,
                    latency: ""
                ))
            }
        }

        if !status.genModel.isEmpty {
            currentProbe = "Testing generation model..."
            do {
                let r = try await appState.testModel(
                    provider: status.provider,
                    modelID: status.genModel,
                    modelType: "generation"
                )
                results.append(r)
            } catch {
                results.append(AIProbeResult(
                    modelID: status.genModel,
                    provider: status.provider,
                    modelType: "generation",
                    ok: false,
                    detail: error.localizedDescription,
                    latency: ""
                ))
            }
        }

        currentProbe = nil
        running = false
    }
}
