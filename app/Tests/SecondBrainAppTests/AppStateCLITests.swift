import Foundation
import SecondBrainCore
import Testing
@testable import SecondBrain

// Set 2NB_TEST exactly once, before any test spawns a `2nb` subprocess. Those
// subprocesses (e.g. `vault create` via VaultManager.initializeVault) inherit
// this process's environment, and 2NB_TEST makes them skip writing the real
// ~/.2ndbrain-vaults recents and skip reading the developer's Obsidian registry
// — otherwise running the suite would pollute recents / bind the dev's live
// vault. A global `let` initializer runs once with thread-safe semantics; a
// per-call setenv would race under swift-testing's parallel execution (POSIX
// setenv is not thread-safe and can reallocate `environ`).
private let isolate2nbHomeWrites: Void = {
    setenv("2NB_TEST", "1", 1)
}()

@MainActor
private func withOpenAppStateVault(_ body: (AppState, URL) async throws -> Void) async throws {
    _ = isolate2nbHomeWrites // force the one-time setenv before any subprocess

    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("SecondBrainAppStateTests-\(UUID().uuidString)")
    try VaultManager.initializeVault(at: root)
    let state = AppState()
    state.openVault(at: root)
    try await body(state, root)
}

@Test("AppState.runCLI executes CLI with vault argument")
@MainActor
func appStateRunCLI() async throws {
    try await withOpenAppStateVault { state, root in
        let data = try await state.runCLI(["config", "get", "ai.provider"], cwd: root)
        let provider = String(decoding: data, as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        #expect(provider == "bedrock")
    }
}

@Test("AppState.setActiveModel updates config")
@MainActor
func appStateSetActiveModel() async throws {
    try await withOpenAppStateVault { state, root in
        try await state.setActiveModel(type: "embedding", modelID: "app.embed.model", provider: "bedrock")
        let data = try await state.runCLI(["config", "get", "ai.embedding_model"], cwd: root)
        let model = String(decoding: data, as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        #expect(model == "app.embed.model")
    }
}

@Test("AppState.setModelEnableState writes user catalog")
@MainActor
func appStateSetModelEnableState() async throws {
    try await withOpenAppStateVault { state, root in
        try await state.setModelEnableState("app.gen.model", provider: "bedrock", scope: "vault", state: "disabled")
        let yaml = try String(contentsOf: root.appendingPathComponent(".2ndbrain/models.yaml"), encoding: .utf8)
        #expect(yaml.contains("app.gen.model"))
        #expect(yaml.contains("enabled: false"))
    }
}

@Test("AppState.setModelSimilarityThreshold writes threshold without price flags")
@MainActor
func appStateSetModelSimilarityThreshold() async throws {
    try await withOpenAppStateVault { state, root in
        let model = CatalogModelInfo(
            modelID: "app.embed.threshold",
            name: "App Embed Threshold",
            provider: "bedrock",
            modelType: "embedding",
            vendor: nil,
            vendorDisplay: nil,
            family: nil,
            versionSortKey: nil,
            dimensions: 1024,
            priceIn: 1.0,
            priceOut: nil,
            priceRequest: nil,
            priceSource: "vendor",
            reachable: nil,
            credentials: nil,
            rateLimitRPS: nil,
            rateLimitTPM: nil,
            priceOverride: nil,
            contextLen: nil,
            recommendedSimilarityThreshold: nil,
            local: nil,
            tier: nil,
            invokeStrategy: nil,
            enabled: nil,
            active: nil,
            configHint: nil,
            notes: nil,
            testedAt: nil,
            testLatencyMs: nil,
            testError: nil,
        testErrorCode: nil,
            benchmark: nil,
            compatible: nil,
            compatibilityReason: nil,
            recommended: nil,
            supportedDimensions: nil
        )
        try await state.setModelSimilarityThreshold(model, threshold: 0.42, scope: "vault")
        let yaml = try String(contentsOf: root.appendingPathComponent(".2ndbrain/models.yaml"), encoding: .utf8)
        #expect(yaml.contains("app.embed.threshold"))
        #expect(yaml.contains("recommended_similarity_threshold: 0.42"))
        #expect(!yaml.contains("price_source: user"))
    }
}

@Test("AppState.repairAllDrift aggregates across files and survives a bad path")
@MainActor
func appStateRepairAllDrift() async throws {
    try await withOpenAppStateVault { state, root in
        func writeNote(_ name: String, _ id: String, _ title: String, _ body: String) throws {
            let fm = "---\nid: \(id)\ntitle: \(title)\ntype: note\nstatus: draft\ntags: []\n---\n\n"
            try (fm + body + "\n").write(to: root.appendingPathComponent(name), atomically: true, encoding: .utf8)
        }
        // Two target notes + two sources each with a case/spacing-drift link
        // (broken in 2nb's case-sensitive resolver, repairable to the title).
        try writeNote("auth-flow.md", "t-auth", "Auth Flow", "# Auth Flow")
        try writeNote("jwt-tokens.md", "t-jwt", "JWT Tokens", "# JWT Tokens")
        try writeNote("a.md", "t-a", "A", "See [[auth flow]] here.")
        try writeNote("b.md", "t-b", "B", "See [[jwt tokens]] here.")
        _ = try await state.runCLI(["index"], cwd: root)

        // A nonexistent path is counted as failed, not fatal: the real files
        // still get repaired and partial success is preserved.
        let (repaired, failed) = try await state.repairAllDrift(paths: ["a.md", "ghost.md", "b.md"])
        #expect(repaired == 2)
        #expect(failed == 1)

        let aFixed = try String(contentsOf: root.appendingPathComponent("a.md"), encoding: .utf8)
        let bFixed = try String(contentsOf: root.appendingPathComponent("b.md"), encoding: .utf8)
        #expect(aFixed.contains("[[Auth Flow]]") && !aFixed.contains("[[auth flow]]"))
        #expect(bFixed.contains("[[JWT Tokens]]") && !bFixed.contains("[[jwt tokens]]"))
    }
}

@Test("AppState provider-dependent methods fail without vault")
@MainActor
func appStateNoVaultErrors() async {
    let state = AppState()
    var testAndSaveNoVault = false
    do {
        _ = try await state.testAndSave(modelID: "m", provider: "bedrock", type: "generation", scope: "vault")
    } catch CLIError.noVault {
        testAndSaveNoVault = true
    } catch {}
    #expect(testAndSaveNoVault)

    var benchmarkNoVault = false
    do {
        try await state.benchmarkModel(modelID: "m", provider: "bedrock", type: "generation", probe: "generate") { _ in }
    } catch CLIError.noVault {
        benchmarkNoVault = true
    } catch {}
    #expect(benchmarkNoVault)

    var pluginInstallNoVault = false
    do {
        try await state.installObsidianPlugin()
    } catch CLIError.noVault {
        pluginInstallNoVault = true
    } catch {}
    #expect(pluginInstallNoVault)
}

@Test("CLIError.nonZeroExit surfaces the CLI stderr, falling back to the exit code")
func cliErrorSurfacesStderr() {
    // The real reason reaches the user instead of a bare exit code.
    #expect(CLIError.nonZeroExit(1, message: "Error: bedrock not ready: AccessDeniedException").errorDescription
        == "Error: bedrock not ready: AccessDeniedException")

    // Empty / whitespace-only stderr falls back to the exit code.
    #expect(CLIError.nonZeroExit(2, message: "   \n").errorDescription == "CLI exited with code 2")
    #expect(CLIError.nonZeroExit(3, message: "").errorDescription == "CLI exited with code 3")

    // A multi-line message is preserved, trimmed at the ends.
    #expect(CLIError.nonZeroExit(1, message: "\nfirst\nError: real reason\n").errorDescription
        == "first\nError: real reason")
}
