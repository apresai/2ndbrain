import Foundation
import SecondBrainCore
import Testing
@testable import SecondBrain

// Set 2NB_TEST exactly once, before any test spawns a `2nb` subprocess.
private let isolate2nbHomeWrites: Void = {
    setenv("2NB_TEST", "1", 1)
}()

@MainActor
private func withOpenAppStateVault(_ body: (AppState, URL) async throws -> Void) async throws {
    _ = isolate2nbHomeWrites

    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("SecondBrainAppStateTests-\(UUID().uuidString)")
    try VaultManager.initializeVault(at: root)
    let state = AppState()
    state.openVault(at: root)
    await state.databaseReady()
    try await body(state, root)
}

@Test("AppState.runCLI executes CLI with vault argument")
@MainActor
func appStateRunCLI() async throws {
    try await withOpenAppStateVault { state, root in
        let data = try await state.runCLI(["config", "get", "ai.provider"], cwd: root)
        let provider = String(decoding: data, as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        #expect(provider == "gemini")
    }
}

@Test("AppState.setActiveModel updates Gemini config")
@MainActor
func appStateSetActiveModel() async throws {
    try await withOpenAppStateVault { state, root in
        try await state.setActiveModel(type: "embedding", modelID: "gemini-embedding-001", provider: "gemini")
        let data = try await state.runCLI(["config", "get", "ai.embedding_model"], cwd: root)
        let model = String(decoding: data, as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        #expect(model == "gemini-embedding-001")
    }
}

@Test("AppState per-slot combo setters configure local embeddings with Gemini Hydrogen generation")
@MainActor
func appStatePerSlotComboSetters() async throws {
    try await withOpenAppStateVault { state, root in
        try await state.setEmbeddingSlot(provider: "llama-local", modelID: "embeddinggemma-2", dimensions: 256)
        try await state.setGenerationSlot(provider: "gemini", modelID: "gemini-4-hydrogen")

        let ep = try await state.getConfigValue("ai.embedding_provider")
        let em = try await state.getConfigValue("ai.embedding_model")
        let dims = try await state.getConfigValue("ai.dimensions")
        let gp = try await state.getConfigValue("ai.generation_provider")
        let gm = try await state.getConfigValue("ai.generation_model")

        #expect(ep == "llama-local")
        #expect(em == "embeddinggemma-2")
        #expect(dims == "256")
        #expect(gp == "gemini")
        #expect(gm == "gemini-4-hydrogen")
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
        try writeNote("auth-flow.md", "t-auth", "Auth Flow", "# Auth Flow")
        try writeNote("jwt-tokens.md", "t-jwt", "JWT Tokens", "# JWT Tokens")
        try writeNote("a.md", "t-a", "A", "See [[auth flow]] here.")
        try writeNote("b.md", "t-b", "B", "See [[jwt tokens]] here.")
        _ = try await state.runCLI(["index"], cwd: root)

        let (repaired, failed) = try await state.repairAllDrift(paths: ["a.md", "ghost.md", "b.md"])
        #expect(repaired == 2)
        #expect(failed == 1)

        let aFixed = try String(contentsOf: root.appendingPathComponent("a.md"), encoding: .utf8)
        let bFixed = try String(contentsOf: root.appendingPathComponent("b.md"), encoding: .utf8)
        #expect(aFixed.contains("[[Auth Flow]]") && !aFixed.contains("[[auth flow]]"))
        #expect(bFixed.contains("[[JWT Tokens]]") && !bFixed.contains("[[jwt tokens]]"))
    }
}

@Test("AppState vault-dependent methods fail cleanly with noVault")
@MainActor
func appStateNoVaultErrors() async {
    let state = AppState()

    var askNoVault = false
    do {
        _ = try await state.askVault(question: "hi")
    } catch CLIError.noVault {
        askNoVault = true
    } catch {}
    #expect(askNoVault)

    var searchNoVault = false
    do {
        _ = try await state.searchVault(query: "hi")
    } catch CLIError.noVault {
        searchNoVault = true
    } catch {}
    #expect(searchNoVault)

    var polishNoVault = false
    do {
        _ = try await state.polishNote(path: "notes/a.md", links: true, repairLinks: true, write: false)
    } catch CLIError.noVault {
        polishNoVault = true
    } catch {}
    #expect(polishNoVault)

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
    #expect(CLIError.nonZeroExit(1, message: "Error: gemini not ready: permission denied").errorDescription
        == "Error: gemini not ready: permission denied")

    #expect(CLIError.nonZeroExit(2, message: "   \n").errorDescription == "CLI exited with code 2")
    #expect(CLIError.nonZeroExit(3, message: "").errorDescription == "CLI exited with code 3")

    #expect(CLIError.nonZeroExit(1, message: "\nfirst\nError: real reason\n").errorDescription
        == "first\nError: real reason")
}

@Test("AppState.suggestTargetArgs appends --source; --llm/--verdict only when requested")
func suggestTargetArgConstruction() {
    let base = AppState.suggestTargetArgs(target: "ghostty", sourcePath: nil)
    #expect(base == ["suggest-target", "ghostty", "--limit", "3", "--json", "--porcelain"])

    let withSource = AppState.suggestTargetArgs(
        target: "ghostty", sourcePath: "resources/ghostty-matrix-theme.md")
    #expect(withSource == [
        "suggest-target", "ghostty", "--limit", "3", "--json", "--porcelain",
        "--source", "resources/ghostty-matrix-theme.md",
    ])

    let withLLM = AppState.suggestTargetArgs(target: "x", sourcePath: "n.md", limit: 3, llm: true)
    #expect(withLLM == [
        "suggest-target", "x", "--limit", "3", "--json", "--porcelain",
        "--source", "n.md", "--llm",
    ])

    let withVerdict = AppState.suggestTargetArgs(
        target: "x", sourcePath: "n.md", limit: 3, llm: true, verdict: true)
    #expect(withVerdict == [
        "suggest-target", "x", "--limit", "3", "--json", "--porcelain",
        "--source", "n.md", "--llm", "--verdict",
    ])
}

@Test("AppState Gemini capability CLI argument builders produce exact porcelain invocations")
func capabilityArgBuilders() {
    #expect(AppState.askArgs(question: "What is 2nb?", hasHistory: false)
        == ["ask", "--json", "--porcelain", "What is 2nb?"])
    #expect(AppState.askArgs(question: "And how?", hasHistory: true)
        == ["ask", "--json", "--porcelain", "--history", "-", "And how?"])

    #expect(AppState.searchArgs(query: "vertex ai", limit: 20)
        == ["search", "--limit", "20", "--json", "--porcelain", "vertex ai"])
    #expect(AppState.searchArgs(query: "gemini", limit: 15, bm25Only: false)
        == ["search", "--limit", "15", "--json", "--porcelain", "gemini"])
    #expect(AppState.searchArgs(query: "gemini", limit: 15, bm25Only: true)
        == ["search", "--limit", "15", "--json", "--porcelain", "--bm25-only", "gemini"])

    #expect(AppState.polishArgs(path: "notes/a.md", links: false, repairLinks: false, write: false)
        == ["polish", "notes/a.md", "--json", "--porcelain"])
    #expect(AppState.polishArgs(path: "notes/a.md", links: true, repairLinks: true, write: true)
        == ["polish", "notes/a.md", "--json", "--porcelain", "--links", "--repair-links", "--write"])
    #expect(AppState.polishArgs(
        path: "notes/a.md",
        goal: "Make concise",
        schema: true,
        links: true,
        repairLinks: true,
        backlinks: true,
        applyBacklinks: false,
        write: false
    ) == ["polish", "notes/a.md", "--json", "--porcelain", "--goal", "Make concise", "--schema", "--links", "--repair-links", "--backlinks"])
    // Blank/whitespace goal omits --goal; applyBacklinks without write NEVER emits --apply-backlinks.
    #expect(AppState.polishArgs(
        path: "notes/a.md",
        goal: "   \n ",
        schema: false,
        links: false,
        repairLinks: false,
        backlinks: false,
        applyBacklinks: true,
        write: false
    ) == ["polish", "notes/a.md", "--json", "--porcelain", "--no-schema"])
    #expect(AppState.polishArgs(
        path: "notes/a.md",
        goal: "Executive",
        schema: true,
        links: true,
        repairLinks: true,
        backlinks: true,
        applyBacklinks: true,
        write: true
    ) == ["polish", "notes/a.md", "--json", "--porcelain", "--goal", "Executive", "--schema", "--links", "--repair-links", "--backlinks", "--write", "--apply-backlinks"])

    #expect(AppState.polishUndoArgs(path: "notes/a.md")
        == ["polish", "notes/a.md", "--undo", "--json", "--porcelain"])
}

@Test("AppState.doctorSelfTestArgs pins --vault when a vault is bound")
func doctorSelfTestArgsVaultPinning() {
    #expect(AppState.doctorSelfTestArgs(vaultRoot: nil)
        == ["doctor", "--json", "--porcelain"])
    #expect(AppState.doctorSelfTestArgs(vaultRoot: URL(fileURLWithPath: "/tmp/my-vault"))
        == ["--vault", "/tmp/my-vault", "doctor", "--json", "--porcelain"])
}

@Test("AppState.credentialProbeEnvironment isolates Gemini API key vs Vertex AI ADC modes")
func credentialProbeEnvironmentModes() {
    let apiKeyEnv = AppState.credentialProbeEnvironment(
        provider: "gemini",
        apiKey: "dummy-api-key",
        project: "ignored-project",
        location: "us-central1"
    )
    #expect(apiKeyEnv["2NB_DOCTOR_PROBE_PROVIDER"] == "gemini")
    #expect(apiKeyEnv["GEMINI_API_KEY"] == "dummy-api-key")
    #expect(apiKeyEnv["2NB_DOCTOR_PROBE_GEMINI_MODE"] == "apikey")
    #expect(apiKeyEnv["GOOGLE_CLOUD_PROJECT"] == nil)
    #expect(apiKeyEnv["2NB_DOCTOR_PROBE_GCP_PROJECT"] == nil)

    let vertexEnv = AppState.credentialProbeEnvironment(
        provider: "google",
        apiKey: nil,
        project: "my-gcp-project",
        location: "us-east4"
    )
    #expect(vertexEnv["2NB_DOCTOR_PROBE_PROVIDER"] == "gemini")
    #expect(vertexEnv["GOOGLE_CLOUD_PROJECT"] == "my-gcp-project")
    #expect(vertexEnv["2NB_DOCTOR_PROBE_GEMINI_MODE"] == "vertex")
    #expect(vertexEnv["2NB_DOCTOR_PROBE_GCP_PROJECT"] == "my-gcp-project")
    #expect(vertexEnv["GOOGLE_CLOUD_LOCATION"] == "us-east4")
    #expect(vertexEnv["2NB_DOCTOR_PROBE_GCP_LOCATION"] == "us-east4")
    #expect(vertexEnv["GEMINI_API_KEY"] == nil)

    let vertexNoLocEnv = AppState.credentialProbeEnvironment(
        provider: "gemini",
        apiKey: "",
        project: "my-gcp-project",
        location: nil
    )
    #expect(vertexNoLocEnv["GOOGLE_CLOUD_PROJECT"] == "my-gcp-project")
    #expect(vertexNoLocEnv["2NB_DOCTOR_PROBE_GEMINI_MODE"] == "vertex")
    #expect(vertexNoLocEnv["2NB_DOCTOR_PROBE_GCP_PROJECT"] == "my-gcp-project")
    #expect(vertexNoLocEnv["GOOGLE_CLOUD_LOCATION"] == nil)
    #expect(vertexNoLocEnv["2NB_DOCTOR_PROBE_GCP_LOCATION"] == nil)

    let bedrockEnv = AppState.credentialProbeEnvironment(
        provider: "bedrock",
        apiKey: "bedrock-token"
    )
    #expect(bedrockEnv["2NB_DOCTOR_PROBE_PROVIDER"] == "bedrock")
    #expect(bedrockEnv["AWS_BEARER_TOKEN_BEDROCK"] == "bedrock-token")
    #expect(bedrockEnv["2NB_BEDROCK_IGNORE_PREFER_STORED"] == "1")
}

@Test("AppState.searchNotesForPicker falls back to disk on unindexed vaults (title, path, body)")
@MainActor
func appStateSearchNotesUnindexedFallback() async throws {
    try await withOpenAppStateVault { state, root in
        let notesDir = root.appendingPathComponent("notes", isDirectory: true)
        try FileManager.default.createDirectory(at: notesDir, withIntermediateDirectories: true)
        try """
        ---
        title: Offline Note
        type: note
        status: complete
        ---

        # Offline Note

        Contains unindexed-needle-phrase without running 2nb index first.
        """.write(to: notesDir.appendingPathComponent("offline-note.md"), atomically: true, encoding: .utf8)

        try """
        ---
        title: Other Note
        type: note
        status: complete
        ---

        # Other Note

        Nothing special here.
        """.write(to: notesDir.appendingPathComponent("other.md"), atomically: true, encoding: .utf8)

        // Vault has 0 rows in index.db (`2nb index` has not run).
        #expect((try? state.database?.documentCount()) == 0)

        let recent = await state.searchNotesForPicker(query: "", limit: 20)
        #expect(recent.count == 2)

        let byTitle = await state.searchNotesForPicker(query: "Offline Note", limit: 20)
        #expect(byTitle.first?.path == "notes/offline-note.md")

        let byPath = await state.searchNotesForPicker(query: "offline-note", limit: 20)
        #expect(byPath.first?.path == "notes/offline-note.md")

        let byBody = await state.searchNotesForPicker(query: "unindexed-needle-phrase", limit: 20)
        #expect(byBody.count == 1)
        #expect(byBody.first?.path == "notes/offline-note.md")
        #expect(byBody.first?.matchSource == "content")
    }
}

@Test("AppState.searchNotesForPicker finds oldest note beyond 200-note cap in indexed vault")
@MainActor
func appStateSearchNotesOver200IndexedVault() async throws {
    try await withOpenAppStateVault { state, root in
        let areasDir = root.appendingPathComponent("areas", isDirectory: true)
        let notesDir = root.appendingPathComponent("notes", isDirectory: true)
        try FileManager.default.createDirectory(at: areasDir, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: notesDir, withIntermediateDirectories: true)

        let oldestURL = areasDir.appendingPathComponent("note-225.md")
        try """
        ---
        title: Zebra Final Spec
        type: note
        status: complete
        ---

        # Zebra Final Spec

        Contains needle-225-alpha in the oldest note.
        """.write(to: oldestURL, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes(
            [.modificationDate: Date(timeIntervalSince1970: 1_577_836_800)],
            ofItemAtPath: oldestURL.path
        )

        for i in 1...210 {
            try """
            ---
            title: Filler \(i)
            type: note
            status: complete
            ---

            # Filler \(i)

            Filler text \(i).
            """.write(to: notesDir.appendingPathComponent("filler-\(i).md"), atomically: true, encoding: .utf8)
        }

        _ = try await state.runCLI(["index"], cwd: root)

        let byTitle = await state.searchNotesForPicker(query: "Zebra Final Spec", limit: 50)
        #expect(byTitle.contains(where: { $0.path == "areas/note-225.md" }))

        let byBody = await state.searchNotesForPicker(query: "needle-225-alpha", limit: 50)
        #expect(byBody.contains(where: { $0.path == "areas/note-225.md" }))
    }
}

@Test("AppState.searchNotesForPicker excludes Obsidian template folders and frontmatter placeholder notes in unindexed and indexed vaults")
@MainActor
func appStateSearchNotesExcludesTemplates() async throws {
    try await withOpenAppStateVault { state, root in
        let fm = FileManager.default
        let obsidianDir = root.appendingPathComponent(".obsidian", isDirectory: true)
        let templatesDir = root.appendingPathComponent("Templates", isDirectory: true)
        let templaterDir = root.appendingPathComponent("_templater", isDirectory: true)
        let templaterPluginDir = obsidianDir.appendingPathComponent("plugins/templater-obsidian", isDirectory: true)
        let notesDir = root.appendingPathComponent("notes", isDirectory: true)
        try fm.createDirectory(at: obsidianDir, withIntermediateDirectories: true)
        try fm.createDirectory(at: templatesDir, withIntermediateDirectories: true)
        try fm.createDirectory(at: templaterDir, withIntermediateDirectories: true)
        try fm.createDirectory(at: templaterPluginDir, withIntermediateDirectories: true)
        try fm.createDirectory(at: notesDir, withIntermediateDirectories: true)

        try "{\"templates\":true}".write(
            to: obsidianDir.appendingPathComponent("core-plugins.json"),
            atomically: true,
            encoding: .utf8
        )
        try "{\"folder\":\"Templates\"}".write(
            to: obsidianDir.appendingPathComponent("templates.json"),
            atomically: true,
            encoding: .utf8
        )
        try "{\"templates_folder\":\"_templater\"}".write(
            to: templaterPluginDir.appendingPathComponent("data.json"),
            atomically: true,
            encoding: .utf8
        )

        try """
        ---
        title: Meeting Template
        type: note
        status: draft
        ---

        # Meeting Template

        Contains unindexed-needle-phrase inside Templates folder.
        """.write(to: templatesDir.appendingPathComponent("meeting-template.md"), atomically: true, encoding: .utf8)

        try """
        ---
        title: Templater Note
        type: note
        status: draft
        ---

        # Templater Note

        Contains unindexed-needle-phrase inside Templater folder.
        """.write(to: templaterDir.appendingPathComponent("templater-note.md"), atomically: true, encoding: .utf8)

        try """
        ---
        title: "{{title}}"
        created: {{date}}
        ---

        # Placeholder Template

        Contains unindexed-needle-phrase inside placeholder template.
        """.write(to: notesDir.appendingPathComponent("placeholder-template.md"), atomically: true, encoding: .utf8)

        try """
        ---
        title: Offline Note
        type: note
        status: complete
        ---

        # Offline Note

        Contains unindexed-needle-phrase for real note matching.
        """.write(to: notesDir.appendingPathComponent("offline-note.md"), atomically: true, encoding: .utf8)

        try """
        ---
        title: Templating Guide
        type: note
        status: complete
        ---

        # Templating Guide

        Mentions {{date}} in body only and unindexed-needle-phrase.
        """.write(to: notesDir.appendingPathComponent("templating-guide.md"), atomically: true, encoding: .utf8)

        // 1. Unindexed vault (`documentCount == 0`) uses disk fallback but excludes templates.
        #expect((try? state.database?.documentCount()) == 0)
        let unindexedHits = await state.searchNotesForPicker(query: "unindexed-needle-phrase", limit: 50)
        let unindexedPaths = Set(unindexedHits.map(\.path))
        #expect(unindexedPaths == ["notes/offline-note.md", "notes/templating-guide.md"])

        let unindexedRecent = await state.searchNotesForPicker(query: "", limit: 50)
        let unindexedRecentPaths = Set(unindexedRecent.map(\.path))
        #expect(unindexedRecentPaths == ["notes/offline-note.md", "notes/templating-guide.md"])

        // 2. Indexed vault (`documentCount > 0`) must NOT fall back to disk on 0 DB hits.
        _ = try await state.runCLI(["index"], cwd: root)
        #expect(((try? state.database?.documentCount()) ?? 0) > 0)

        let indexedTemplateQuery = await state.searchNotesForPicker(query: "Meeting Template", limit: 50)
        #expect(indexedTemplateQuery.isEmpty)

        let indexedPlaceholderQuery = await state.searchNotesForPicker(query: "Placeholder Template", limit: 50)
        #expect(indexedPlaceholderQuery.isEmpty)

        let indexedOfflineHits = await state.searchNotesForPicker(query: "Offline Note", limit: 50)
        #expect(indexedOfflineHits.map(\.path) == ["notes/offline-note.md"])
    }
}

@Test("AppState.searchNotesForPicker parses CRLF note titles in unindexed fallback and resets polish undo state on vault switch")
@MainActor
func appStateSearchNotesCRLFAndPolishUndoReset() async throws {
    try await withOpenAppStateVault { state, root in
        let crlfNote = "---\r\ntitle: CRLF Title\r\ntype: note\r\nstatus: complete\r\n---\r\n\r\n# CRLF Title\r\n\r\nBody.\r\n"
        try crlfNote.write(to: root.appendingPathComponent("crlf.md"), atomically: true, encoding: .utf8)

        let hits = await state.searchNotesForPicker(query: "CRLF Title", limit: 20)
        #expect(hits.first?.path == "crlf.md")
        #expect(hits.first?.title == "CRLF Title")
        #expect(hits.first?.matchSource == "title")

        state.canUndoPolishPath = "crlf.md"
        state.canUndoPolishBacklinkPaths = ["other.md"]

        let otherVault = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("SecondBrainOther-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: otherVault, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: otherVault) }
        state.openVault(at: otherVault)
        #expect(state.canUndoPolishPath == nil)
        #expect(state.canUndoPolishBacklinkPaths.isEmpty)
    }
}
