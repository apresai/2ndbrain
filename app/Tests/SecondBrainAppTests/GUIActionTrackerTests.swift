import Foundation
import Testing
@testable import SecondBrain

@Test("LaunchOptions parses CLI arguments and environment overrides with CLI taking precedence")
func launchOptionsParsing() {
    let env: [String: String] = [
        "2NB_GUI_VAULT": "/tmp/env-vault",
        "2NB_GUI_LOG_DIR": "/tmp/env-logs",
        "2NB_GUI_CONTROL_DIR": "/tmp/env-ctrl",
        "2NB_GUI_SKIP_ONBOARDING": "1",
        "2NB_GUI_NO_STARTUP_SYNC": "0",
    ]

    let envOnly = LaunchOptions.parse(arguments: ["SecondBrain"], environment: env)
    #expect(envOnly.vaultPath == "/tmp/env-vault")
    #expect(envOnly.guiLogDir?.path == "/tmp/env-logs")
    #expect(envOnly.guiControlDir?.path == "/tmp/env-ctrl")
    #expect(envOnly.skipOnboarding == true)
    #expect(envOnly.noStartupSync == false)

    let cliOverride = LaunchOptions.parse(
        arguments: [
            "SecondBrain",
            "--vault", "/tmp/cli-vault",
            "--gui-log-dir=/tmp/cli-logs",
            "--gui-control-dir", "/tmp/cli-ctrl",
            "--no-startup-sync",
        ],
        environment: env
    )
    #expect(cliOverride.vaultPath == "/tmp/cli-vault")
    #expect(cliOverride.guiLogDir?.path == "/tmp/cli-logs")
    #expect(cliOverride.guiControlDir?.path == "/tmp/cli-ctrl")
    #expect(cliOverride.skipOnboarding == true)
    #expect(cliOverride.noStartupSync == true)

    let eqOverride = LaunchOptions.parse(
        arguments: ["SecondBrain", "--gui-control-dir=/tmp/eq-ctrl"],
        environment: [:]
    )
    #expect(eqOverride.guiControlDir?.path == "/tmp/eq-ctrl")
}

@Test("GUIActionTracker redacts Gemini, OpenRouter, Bedrock, and env-assigned secrets")
func secretRedaction() {
    let raw = [
        "AIza" + "SyTestSecretKey123456789012345",
        "sk-or-v1-0123456789abcdef0123456789",
        "ABSKabcdef1234567890ABCDEF==",
        "GEMINI_API_KEY=custom_secret_val",
        "GOOGLE_API_KEY: another_secret_val",
    ].joined(separator: " | ")

    let redacted = GUIActionTracker.redact(raw)
    #expect(!redacted.contains("TestSecretKey12345"))
    #expect(!redacted.contains("sk-or-v1-"))
    #expect(!redacted.contains("ABSKabcdef"))
    #expect(!redacted.contains("custom_secret_val"))
    #expect(!redacted.contains("another_secret_val"))
    #expect(redacted.contains("<REDACTED>"))
    #expect(redacted.contains("GEMINI_API_KEY=<REDACTED>"))
    #expect(redacted.contains("GOOGLE_API_KEY: <REDACTED>"))
}

@Test("GUIActionTracker redacts quoted secrets and Anthropic keys while retaining quotes")
func quotedSecretRedaction() {
    let quoted = "GEMINI_API_KEY=\"" + "AIza" + "SyA_secret_token_12345\""
    let redactedQuoted = GUIActionTracker.redact(quoted)
    #expect(redactedQuoted == "GEMINI_API_KEY=\"<REDACTED>\"")

    let singleQuoted = "AWS_SECRET_ACCESS_KEY='secret_val'"
    let redactedSingleQuoted = GUIActionTracker.redact(singleQuoted)
    #expect(redactedSingleQuoted == "AWS_SECRET_ACCESS_KEY='<REDACTED>'")

    let anthropicKey = "sk-" + "ant-api03-secret"
    let redactedAnthropic = GUIActionTracker.redact(anthropicKey)
    #expect(redactedAnthropic == "<REDACTED>")

    let mixed = "Errors: GEMINI_API_KEY=\"my_secret_token_12345\" and AWS_SECRET_ACCESS_KEY='wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'"
    let redactedMixed = GUIActionTracker.redact(mixed)
    #expect(!redactedMixed.contains("my_secret_token_12345"))
    #expect(!redactedMixed.contains("wJalrXUtnFEMI"))
    #expect(redactedMixed.contains("GEMINI_API_KEY=\"<REDACTED>\""))
    #expect(redactedMixed.contains("AWS_SECRET_ACCESS_KEY='<REDACTED>'"))
}

@Test("GUIActionTracker redacts JSON quoted keys and escaped quotes while preserving valid JSON syntax")
func jsonAndEscapedSecretRedaction() throws {
    let standardJSON = "{\"AWS_SECRET_ACCESS_KEY\": \"secret_value_12345\"}"
    let redactedStandard = GUIActionTracker.redact(standardJSON)
    #expect(!redactedStandard.contains("secret_value_12345"))
    #expect(redactedStandard == "{\"AWS_SECRET_ACCESS_KEY\": \"<REDACTED>\"}")
    let parsedStandard = try JSONSerialization.jsonObject(with: Data(redactedStandard.utf8)) as? [String: Any]
    #expect(parsedStandard?["AWS_SECRET_ACCESS_KEY"] as? String == "<REDACTED>")

    let lowercaseJSON = "{\"aws_secret_access_key\": \"secret_value_12345\"}"
    let redactedLowercase = GUIActionTracker.redact(lowercaseJSON)
    #expect(!redactedLowercase.contains("secret_value_12345"))
    #expect(redactedLowercase == "{\"aws_secret_access_key\": \"<REDACTED>\"}")
    let parsedLowercase = try JSONSerialization.jsonObject(with: Data(redactedLowercase.utf8)) as? [String: Any]
    #expect(parsedLowercase?["aws_secret_access_key"] as? String == "<REDACTED>")

    let escapedQuotes = "AWS_SECRET_ACCESS_KEY=\\\"secret_value_12345\\\""
    let redactedEscaped = GUIActionTracker.redact(escapedQuotes)
    #expect(!redactedEscaped.contains("secret_value_12345"))
    #expect(redactedEscaped == "AWS_SECRET_ACCESS_KEY=\\\"<REDACTED>\\\"")

    let embeddedJSON = "{\"error\": \"Failed with config: {\\\"AWS_SECRET_ACCESS_KEY\\\": \\\"secret_value_12345\\\"}\"}"
    let redactedEmbedded = GUIActionTracker.redact(embeddedJSON)
    #expect(!redactedEmbedded.contains("secret_value_12345"))
    #expect(redactedEmbedded == "{\"error\": \"Failed with config: {\\\"AWS_SECRET_ACCESS_KEY\\\": \\\"<REDACTED>\\\"}\"}")
    let parsedEmbedded = try JSONSerialization.jsonObject(with: Data(redactedEmbedded.utf8)) as? [String: Any]
    #expect(parsedEmbedded?["error"] != nil)
}

@Test("GUIActionTracker redacts space-separated CLI flags and equals-delimited flags")
func cliFlagSecretRedaction() {
    let cases: [(raw: String, expected: String)] = [
        ("runner --api-key \"secret-abc\"", "runner --api-key \"<REDACTED>\""),
        ("runner --api-key 'secret-abc-single'", "runner --api-key '<REDACTED>'"),
        ("runner --api-key secret-abc-unquoted", "runner --api-key <REDACTED>"),
        ("runner --api-key=secret", "runner --api-key=<REDACTED>"),
        ("runner --api-key=\"secret\"", "runner --api-key=\"<REDACTED>\""),
        ("runner -secret supersecret", "runner -secret <REDACTED>"),
        ("runner -secret=supersecret", "runner -secret=<REDACTED>"),
        ("runner --token supertoken123", "runner --token <REDACTED>"),
        ("runner --token=sk-abcdef123456", "runner --token=<REDACTED>"),
        ("runner -key secretkey", "runner -key <REDACTED>"),
        ("runner --key secretkey", "runner --key <REDACTED>"),
        ("runner --gemini-api-key secretkey", "runner --gemini-api-key <REDACTED>"),
        ("runner --auth-token \"auth123\"", "runner --auth-token \"<REDACTED>\""),
        ("runner --access-token secret-token-val", "runner --access-token <REDACTED>"),
        ("runner --password \"superpass\"", "runner --password \"<REDACTED>\""),
    ]

    for (raw, expected) in cases {
        let result = GUIActionTracker.redact(raw)
        #expect(result == expected, "Failed for [\(raw)]: got [\(result)], expected [\(expected)]")
    }
}

@Test("GUIActionTracker redacts Authorization Bearer tokens")
func bearerTokenRedaction() {
    let bearerCases: [(raw: String, expected: String)] = [
        (
            "curl -H \"Authorization: Bearer custom_jwt_token_123\" https://api.com",
            "curl -H \"Authorization: Bearer <REDACTED>\" https://api.com"
        ),
        (
            "Authorization: Bearer mytoken",
            "Authorization: Bearer <REDACTED>"
        ),
        (
            "Bearer custom_bearer_token",
            "Bearer <REDACTED>"
        ),
        (
            "Bearer \"quoted_bearer_token\"",
            "Bearer \"<REDACTED>\""
        ),
        (
            "Bearer 'single_quoted_bearer'",
            "Bearer '<REDACTED>'"
        ),
        (
            "{\"auth\": \"Bearer nested_secret_token\"}",
            "{\"auth\": \"Bearer <REDACTED>\"}"
        ),
    ]

    for (raw, expected) in bearerCases {
        let result = GUIActionTracker.redact(raw)
        #expect(result == expected, "Failed for [\(raw)]: got [\(result)], expected [\(expected)]")
    }
}

@Test("GUIActionTracker retains valid JSON syntax after redacting complex payloads")
func validJSONSyntaxRetentionAfterRedaction() throws {
    let payloads = [
        "{\"apiKey\": \"secret123\", \"name\": \"test\"}",
        "{\"config\": {\"auth\": {\"GEMINI_API_KEY\": \"" + "AIza" + "SySecretToken12345\"}}}",
        "{\"config\": {\"auth\": {\"GEMINI_API_KEY\": \"custom_random_token_val_99\"}}}",
        "{\"gemini_api_key\": \"val123\"}",
        "{\"Gcp_Secret_Token\": \"val456\"}",
        "{\"SECRET_KEY\":\"val\"}",
        "{\"SECRET_KEY\"  :  \"val\"}",
        "{\"token_count\": 512, \"keyword\": \"swift\", \"name\": \"test\"}",
        "{\"hotkey\": \"cmd+p\", \"primary_key\": \"doc_id\"}",
        "{\"access_token\": \"xyz123\", \"expires_in\": 3600, \"scope\": \"read write\"}",
    ]

    for payload in payloads {
        let redacted = GUIActionTracker.redact(payload)
        guard let data = redacted.data(using: .utf8) else {
            Issue.record("Failed to convert redacted JSON to Data: \(redacted)")
            continue
        }
        let parsed = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        #expect(parsed != nil, "Parsed JSON was nil for: \(redacted)")
    }
}

@Test("GUIActionTracker preserves normal application identifiers without over-redaction")
func preservationOfNormalIdentifiers() {
    let nonSensitive: [(raw: String, expectedSubstring: String)] = [
        ("{\"keyword\": \"machine learning in obsidian\"}", "machine learning in obsidian"),
        ("{\"tokenizer\": \"gemma-bpe\"}", "gemma-bpe"),
        ("{\"token_count\": 512}", "512"),
        ("{\"hotkey\": \"cmd+shift+p\"}", "cmd+shift+p"),
        ("{\"primary_key\": \"document_id\"}", "document_id"),
        ("turkey: thanksgiving", "thanksgiving"),
        ("monkey: banana", "banana"),
        ("hockey: nhl", "nhl"),
        ("{\"tokens\": 100}", "100"),
        ("{\"total_tokens\": 50}", "50"),
        ("{\"prompt_tokens\": 20}", "20"),
        ("{\"max_tokens\": 1024}", "1024"),
        ("embeddinggemma-2", "embeddinggemma-2"),
        ("embeddinggemma-300m", "embeddinggemma-300m"),
        ("gemma4-e2b", "gemma4-e2b"),
        ("bge-reranker-v2-m3", "bge-reranker-v2-m3"),
        ("engine.start", "engine.start"),
        ("config set ai.dimensions 256", "256"),
        ("runner --keyword \"machine learning\"", "machine learning"),
        ("runner --tokenizer \"gemma-bpe\"", "gemma-bpe"),
        ("runner --token-count 512", "512"),
        ("runner --hotkey \"cmd+shift+p\"", "cmd+shift+p"),
        ("runner --primary-key \"doc_id\"", "doc_id"),
    ]

    for (raw, expectedSubstring) in nonSensitive {
        let redacted = GUIActionTracker.redact(raw)
        #expect(redacted.contains(expectedSubstring), "Lost [\(expectedSubstring)] in [\(redacted)] from [\(raw)]")
        #expect(!redacted.contains("<REDACTED>"), "Unexpectedly redacted: [\(raw)] -> [\(redacted)]")
    }
}

@Test("GUIActionTracker preserves URL query parameters following secrets")
func urlQueryParameterPreservation() {
    let urlCases: [(raw: String, expected: String)] = [
        (
            "https://api.example.com/v1?api_key=mysecretkey888&user=alice",
            "https://api.example.com/v1?api_key=<REDACTED>&user=alice"
        ),
        (
            "https://api.example.com/v1?token=token123&user=alice&action=sync",
            "https://api.example.com/v1?token=<REDACTED>&user=alice&action=sync"
        ),
        (
            "http://localhost:8080/query?secret=xyz&next=1",
            "http://localhost:8080/query?secret=<REDACTED>&next=1"
        ),
        (
            "https://example.com/oauth?access_token=secret_val&refresh=1",
            "https://example.com/oauth?access_token=<REDACTED>&refresh=1"
        ),
    ]

    for (raw, expected) in urlCases {
        let redacted = GUIActionTracker.redact(raw)
        #expect(redacted == expected, "Failed for [\(raw)]: got [\(redacted)], expected [\(expected)]")
    }
}

@Test("GUIActionTracker enforces 300-event ring buffer, monotonic seq, and lastError capture")
@MainActor
func trackerRingBufferAndErrors() {
    let tempDir = FileManager.default.temporaryDirectory
        .appendingPathComponent("tracker-ring-\(UUID().uuidString)", isDirectory: true)
    let tracker = GUIActionTracker(overrideLogDir: tempDir, persistenceEnabled: false, appVersion: "0.23.4")

    for i in 1...305 {
        tracker.record(
            category: .navigation,
            action: "nav.step",
            phase: .succeeded,
            summary: "Step \(i)",
            metadata: ["step": String(i)]
        )
    }

    #expect(tracker.events.count == 300)
    #expect(tracker.events.first?.seq == 305)
    #expect(tracker.events.last?.seq == 6)
    #expect(tracker.status.eventCount == 305)
    #expect(tracker.status.lastActionSeq == 305)

    tracker.record(
        category: .index,
        action: "index.failed",
        phase: .failed,
        summary: "Index sync failed",
        detail: "GEMINI_API_KEY=bad_secret_123 exploded"
    )
    #expect(tracker.status.lastError == "GEMINI_API_KEY=<REDACTED> exploded")
}

@Test("GUIActionTracker writes gui-actions.jsonl + gui-state.json without creating uninitialized .2ndbrain")
@MainActor
func trackerDiskPersistenceAndWriteSurfaceGuard() throws {
    let fm = FileManager.default
    let root = fm.temporaryDirectory.appendingPathComponent("tracker-disk-\(UUID().uuidString)", isDirectory: true)
    defer { try? fm.removeItem(at: root) }

    let logDir = root.appendingPathComponent("logs", isDirectory: true)
    let uninitializedVault = root.appendingPathComponent("uninit-vault", isDirectory: true)
    try fm.createDirectory(at: uninitializedVault, withIntermediateDirectories: true)

    let tracker = GUIActionTracker(overrideLogDir: logDir, persistenceEnabled: true, appVersion: "0.23.4")
    tracker.setVaultDotDir(uninitializedVault.appendingPathComponent(".2ndbrain", isDirectory: true))
    tracker.updateStatus {
        $0.vaultPath = uninitializedVault.path
        $0.vaultName = uninitializedVault.lastPathComponent
    }
    tracker.record(
        category: .search,
        action: "search.query",
        phase: .succeeded,
        durationMs: 42,
        summary: "Search for " + "AIza" + "SyDummySecretValue1234567890",
        metadata: ["query": "test"]
    )
    tracker.flushForTesting()

    // Uninitialized vault must NEVER have .2ndbrain created by GUI telemetry.
    let uninitDot = uninitializedVault.appendingPathComponent(".2ndbrain", isDirectory: true)
    #expect(!fm.fileExists(atPath: uninitDot.path))

    let actionsText = try String(contentsOf: tracker.primaryActionsLogURL, encoding: .utf8)
    #expect(!actionsText.contains("DummySecretValue1234567890"))
    #expect(actionsText.contains("search.query"))

    let stateData = try Data(contentsOf: tracker.primaryStateFileURL)
    let decodedState = try JSONDecoder().decode(GUIStatusSnapshot.self, from: stateData)
    #expect(decodedState.vaultPath == uninitializedVault.path)
    #expect(decodedState.lastActionSeq == 1)

    // When .2ndbrain already exists, telemetry mirrors to <vault>/.2ndbrain/logs/.
    let initDot = root.appendingPathComponent("init-vault/.2ndbrain", isDirectory: true)
    try fm.createDirectory(at: initDot, withIntermediateDirectories: true)
    tracker.setVaultDotDir(initDot)
    tracker.record(
        category: .vault,
        action: "vault.open",
        phase: .succeeded,
        summary: "Opened initialized vault"
    )
    tracker.flushForTesting()

    let mirroredLog = initDot.appendingPathComponent("logs/gui-actions.jsonl")
    let mirroredState = initDot.appendingPathComponent("logs/gui-state.json")
    #expect(fm.fileExists(atPath: mirroredLog.path))
    #expect(fm.fileExists(atPath: mirroredState.path))
}

@Test("AppState.noteSettingsOpened updates settingsTab and records settings.open action")
@MainActor
func noteSettingsOpenedRecordsTelemetry() {
    let state = AppState()
    state.noteSettingsOpened(tab: .integrations)
    #expect(state.settingsTab == .integrations)
    let event = state.actionTracker.events.first
    #expect(event?.action == "settings.open")
    #expect(event?.phase == .succeeded)
    #expect(event?.metadata["tab"] == SettingsTab.integrations.rawValue)
}

@Test("GUIInputBridge tracks button click and disabled acknowledgments")
@MainActor
func guiInputBridgeButtonCommands() {
    let bridge = GUIInputBridge()

    let clickID = bridge.submitButtonCommand(target: "indexSheet.confirmButton")
    #expect(bridge.pendingButtonCommand == GUIButtonCommand(id: clickID, targetIdentifier: "indexSheet.confirmButton"))
    #expect(bridge.acknowledgedButton == nil)

    bridge.acknowledgeButtonClicked(clickID, label: "Sync Index")
    #expect(bridge.pendingButtonCommand == nil)
    #expect(bridge.acknowledgedButton == .clicked(clickID, "Sync Index"))

    let disabledID = bridge.submitButtonCommand(target: "polish.applyButton")
    #expect(bridge.pendingButtonCommand == GUIButtonCommand(id: disabledID, targetIdentifier: "polish.applyButton"))
    #expect(bridge.acknowledgedButton == nil)

    bridge.acknowledgeButtonDisabled(disabledID, label: "polish.applyButton")
    #expect(bridge.pendingButtonCommand == nil)
    #expect(bridge.acknowledgedButton == .disabled(disabledID, "polish.applyButton"))

    let searchCmdID = bridge.submitTextCommand(target: "polish.searchField", text: "quantum-canary-token", submit: false)
    #expect(bridge.pendingTextCommand == GUITextCommand(id: searchCmdID, targetIdentifier: "polish.searchField", text: "quantum-canary-token", submit: false))
    bridge.acknowledge(searchCmdID)
    #expect(bridge.acknowledgedCommandID == searchCmdID)

    let emptyGoalCmdID = bridge.submitTextCommand(target: "polish.goalField", text: "", submit: false)
    #expect(bridge.pendingTextCommand == GUITextCommand(id: emptyGoalCmdID, targetIdentifier: "polish.goalField", text: "", submit: false))
    bridge.acknowledge(emptyGoalCmdID)
    #expect(bridge.acknowledgedCommandID == emptyGoalCmdID)

    let toggleID = bridge.submitButtonCommand(target: "polish.applyBacklinksToggle")
    #expect(bridge.pendingButtonCommand?.targetIdentifier == "polish.applyBacklinksToggle")
    bridge.acknowledgeButtonClicked(toggleID, label: "Apply incoming backlinks")
    #expect(bridge.acknowledgedButton == .clicked(toggleID, "Apply incoming backlinks"))
}

@Test("GUIActionTracker keeps status.lastError concise on large multi-line failure details")
@MainActor
func trackerTruncatesHugeLastError() {
    let tempDir = FileManager.default.temporaryDirectory
        .appendingPathComponent("tracker-err-cap-\(UUID().uuidString)", isDirectory: true)
    let tracker = GUIActionTracker(overrideLogDir: tempDir, persistenceEnabled: false, appVersion: "0.23.4")

    let noisyPrefix = (1...200).map { "  embedded \($0)/417: notes/day-\($0).md" }.joined(separator: "\n")
    let trailingError = "Error: force-reembed incomplete: embedded 416/417 documents (1 failed; first error on notes/big.md: batch limit)"
    let fullDetail = noisyPrefix + "\n" + trailingError

    tracker.record(
        category: .index,
        action: "index.failed",
        phase: .failed,
        summary: "Index rebuild failed",
        detail: fullDetail
    )

    let lastError = tracker.status.lastError ?? ""
    #expect(lastError.count == GUIActionTracker.maxStatusErrorChars)
    #expect(lastError.hasPrefix("…"))
    #expect(lastError.contains("first error on notes/big.md"))
}

@Test("AppState.extractIndexFailureMessage isolates actionable single-line error from huge stderr")
func extractIndexFailureMessageIsolatesError() {
    let progressLines = (1...150).map { "  embedded \($0)/417: notes/file-\($0).md" }.joined(separator: "\n")
    let stderr = progressLines + "\n  force-reembed failed; restored previous embeddings\nError: force-reembed incomplete: embedded 416/417 documents (1 failed; first error on big.md: Error 400\n, Status: INVALID_ARGUMENT)\n"

    let extracted = AppState.extractIndexFailureMessage(stderr: stderr, exitCode: 1)
    #expect(!extracted.contains("\n"))
    #expect(extracted.contains("force-reembed incomplete"))
    #expect(extracted.contains("first error on big.md"))
    #expect(!extracted.hasPrefix("Error: "))

    let embedErrFallback = "Indexing vault...\n  embed error notes/big.md: 429 RESOURCE_EXHAUSTED\n  force-reembed failed\n"
    #expect(AppState.extractIndexFailureMessage(stderr: embedErrFallback, exitCode: 1) == "embed error notes/big.md: 429 RESOURCE_EXHAUSTED")

    #expect(AppState.extractIndexFailureMessage(stderr: "   \n", exitCode: 2) == "CLI exited with code 2")
}

@Test("GUIActionCategory.engine display label, case serialization, and filter matching")
func engineCategoryAndFilter() {
    #expect(GUIActionCategory.engine.rawValue == "engine")
    #expect(GUIActionCategory.engine.displayLabel == "Engine")

    let event = GUIActionEvent(
        id: "evt-test-1",
        seq: 1,
        timestamp: "2026-11-15T12:00:00Z",
        category: .engine,
        action: "engine.start",
        phase: .succeeded,
        durationMs: 50,
        summary: "Start local llama-server engine daemon",
        detail: nil,
        metadata: [:]
    )
    #expect(GUIActionsFilter.aiAndConfig.matches(event) == true)
    #expect(GUIActionsFilter.all.matches(event) == true)
    #expect(GUIActionsFilter.indexAndSearch.matches(event) == false)
}

@Test("GUIStatusSnapshot serializes and decodes local model and engine telemetry fields")
func statusSnapshotLocalTelemetryFields() throws {
    var snapshot = GUIStatusSnapshot.initial(appVersion: "0.23.4", timestamp: "2026-11-15T12:00:00Z")
    #expect(snapshot.localModelDownloading == false)
    #expect(snapshot.localEngineRunning == false)
    #expect(snapshot.localEngineHealthy == false)

    snapshot.localModelDownloading = true
    snapshot.localModelDownloadCurrent = "embeddinggemma-2"
    snapshot.localModelDownloadDone = 100_000
    snapshot.localModelDownloadTotal = 309_855_456
    snapshot.localModelDownloadFraction = 0.0003
    snapshot.localModelCompletedCount = 1
    snapshot.localModelTotalCount = 3
    snapshot.localEngineRunning = true
    snapshot.localEnginePid = 4321
    snapshot.localEngineHealthy = true
    snapshot.localEngineRoles = ["gen:18090", "embed:18091"]
    snapshot.localMatryoshkaDim = 512
    snapshot.localTaskPrefixPreset = "search"

    let data = try JSONEncoder().encode(snapshot)
    let decoded = try JSONDecoder().decode(GUIStatusSnapshot.self, from: data)

    #expect(decoded.localModelDownloading == true)
    #expect(decoded.localModelDownloadCurrent == "embeddinggemma-2")
    #expect(decoded.localModelDownloadDone == 100_000)
    #expect(decoded.localModelDownloadTotal == 309_855_456)
    #expect(decoded.localModelCompletedCount == 1)
    #expect(decoded.localModelTotalCount == 3)
    #expect(decoded.localEngineRunning == true)
    #expect(decoded.localEnginePid == 4321)
    #expect(decoded.localEngineHealthy == true)
    #expect(decoded.localEngineRoles == ["gen:18090", "embed:18091"])
    #expect(decoded.localMatryoshkaDim == 512)
    #expect(decoded.localTaskPrefixPreset == "search")
}

@Test("GUIActionTracker.redact and record stay linear-time on large underscore-heavy inputs without ReDoS")
@MainActor
func redactionAvoidsCatastrophicBacktrackingAndBoundsBeforeRedacting() {
    let clock = ContinuousClock()

    // 27 KB underscore-connected token without `:` or `=` (previously caused >2m ReDoS hang).
    let longIdentifier = String(repeating: "ERR_LINE_", count: 3000)
    let redactStart = clock.now
    let redactedIdentifier = GUIActionTracker.redact(longIdentifier)
    let redactElapsed = clock.now - redactStart
    #expect(redactedIdentifier == longIdentifier)
    #expect(redactElapsed < .milliseconds(250), "GUIActionTracker.redact took \(redactElapsed) on 27KB underscore input")

    // Also verify CLI-flag regex (#3) does not backtrack excessively on long hyphen/underscore tokens.
    let longFlagInput = "--" + String(repeating: "ERR_LINE-", count: 3000)
    let flagStart = clock.now
    let redactedFlag = GUIActionTracker.redact(longFlagInput)
    let flagElapsed = clock.now - flagStart
    #expect(redactedFlag == longFlagInput)
    #expect(flagElapsed < .milliseconds(250), "GUIActionTracker.redact took \(flagElapsed) on 27KB flag-like input")

    // 270 KB detail with a trailing secret: record() bounds before redacting while still redacting trailing secrets.
    let tempDir = FileManager.default.temporaryDirectory
        .appendingPathComponent("tracker-redos-\(UUID().uuidString)", isDirectory: true)
    let tracker = GUIActionTracker(overrideLogDir: tempDir, persistenceEnabled: false, appVersion: "0.23.4")

    let hugeDetail = String(repeating: "ERR_LINE_", count: 30000)
        + " api_key=super_secret_123 my_api_key=\"sk-or-v1-0123456789abcdef\""
        + " token=tok_secret_val Authorization: Bearer jwt_secret_999"
        + " AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE"

    let recordStart = clock.now
    let event = tracker.record(
        category: .cli,
        action: "cli.exec",
        phase: .failed,
        summary: "CLI failed",
        detail: hugeDetail
    )
    let recordElapsed = clock.now - recordStart
    #expect(recordElapsed < .milliseconds(250), "GUIActionTracker.record took \(recordElapsed) on 270KB detail")

    let detail = event.detail ?? ""
    #expect(detail.count <= GUIActionTracker.maxEventDetailChars)
    #expect(detail.hasPrefix("…"))
    #expect(!detail.contains("super_secret_123"))
    #expect(!detail.contains("sk-or-v1-0123456789abcdef"))
    #expect(!detail.contains("tok_secret_val"))
    #expect(!detail.contains("jwt_secret_999"))
    #expect(!detail.contains("AKIAIOSFODNN7EXAMPLE"))
    #expect(detail.contains("api_key=<REDACTED>"))
    #expect(detail.contains("my_api_key=\"<REDACTED>\""))
    #expect(detail.contains("token=<REDACTED>"))
    #expect(detail.contains("Authorization: Bearer <REDACTED>"))
    #expect(detail.contains("AWS_ACCESS_KEY_ID=<REDACTED>"))
}

@Test("GUIActionTracker.redact covers bare AWS AKIA keys and prefixed key-value pairs")
func redactsAwsKeysAndPrefixedSecrets() {
    let raw = [
        "api_key=raw_val_1",
        "my_api_key=\"" + "sk-" + "ant-api03-1234567890\"",
        "token=tok_val_2",
        "Authorization: Bearer bearer_val_3",
        "AKIAIOSFODNN7EXAMPLE",
        "myToken=camel_val_4",
        "custom_secret=sec_val_5",
    ].joined(separator: " ")

    let redacted = GUIActionTracker.redact(raw)
    #expect(!redacted.contains("raw_val_1"))
    #expect(!redacted.contains("1234567890"))
    #expect(!redacted.contains("tok_val_2"))
    #expect(!redacted.contains("bearer_val_3"))
    #expect(!redacted.contains("AKIAIOSFODNN7EXAMPLE"))
    #expect(!redacted.contains("camel_val_4"))
    #expect(!redacted.contains("sec_val_5"))
    #expect(redacted.contains("api_key=<REDACTED>"))
    #expect(redacted.contains("my_api_key=\"<REDACTED>\""))
    #expect(redacted.contains("token=<REDACTED>"))
    #expect(redacted.contains("Authorization: Bearer <REDACTED>"))
    #expect(redacted.contains("myToken=<REDACTED>"))
    #expect(redacted.contains("custom_secret=<REDACTED>"))
}

@Test("GUIActionTracker.redact handles unclosed-quote secrets in linear time without bypassing redaction")
func redactsUnclosedQuoteSecretsInLinearTime() {
    let clock = ContinuousClock()

    let cases: [(name: String, input: String, secretSnippet: String, expectedPrefix: String)] = [
        (
            "double-quote 50KB",
            "key=\"val" + String(repeating: "x", count: 50_000),
            "valxxxx",
            "key=\"<REDACTED>"
        ),
        (
            "single-quote 50KB",
            "password='unterminated" + String(repeating: "z", count: 50_000),
            "unterminatedzzzz",
            "password='<REDACTED>"
        ),
        (
            "escaped-quote 50KB",
            "key=\\\"unclosed" + String(repeating: "q", count: 50_000),
            "unclosedqqqq",
            "key=\\\"<REDACTED>"
        ),
        (
            "Bearer unclosed double-quote 50KB",
            "Authorization: Bearer \"unclosed_" + String(repeating: "b", count: 50_000),
            "unclosed_bbbb",
            "Authorization: Bearer \"<REDACTED>"
        ),
        (
            "CLI --token unclosed double-quote 50KB",
            "runner --token \"unclosed_" + String(repeating: "t", count: 50_000),
            "unclosed_tttt",
            "runner --token \"<REDACTED>"
        ),
        (
            "CLI --api-key unclosed single-quote 50KB",
            "runner --api-key 'unclosed_" + String(repeating: "k", count: 50_000),
            "unclosed_kkkk",
            "runner --api-key '<REDACTED>"
        ),
    ]

    for c in cases {
        let start = clock.now
        let redacted = GUIActionTracker.redact(c.input)
        let elapsed = clock.now - start
        #expect(elapsed < .milliseconds(250), "\(c.name) took \(elapsed)")
        #expect(!redacted.contains(c.secretSnippet), "\(c.name) leaked secret: \(redacted.prefix(120))")
        #expect(redacted == c.expectedPrefix, "\(c.name) got [\(redacted.prefix(80))] expected [\(c.expectedPrefix)]")
    }

    // Closed quotes still preserve their closing delimiter alongside an unclosed secret on a later line.
    let mixed = "my_api_key=\"sk-or-v1-closedsecret\" tokenizer=bpe\npassword='unterminated_line2"
    let redactedMixed = GUIActionTracker.redact(mixed)
    #expect(!redactedMixed.contains("closedsecret"))
    #expect(!redactedMixed.contains("unterminated_line2"))
    #expect(redactedMixed == "my_api_key=\"<REDACTED>\" tokenizer=bpe\npassword='<REDACTED>")
}

@Test("GUIActionTracker.boundedRedactedTail redacts >256-char Bearer JWT spanning the pre-limit boundary")
func boundedRedactedTailHandlesLongBearerJWT() {
    let longJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." + String(repeating: "A", count: 360)
    let payload = "PREFIX Authorization: Bearer \(longJWT) " + String(repeating: "x", count: 960)
    let result = GUIActionTracker.boundedRedactedTail(payload, limit: 1000)

    #expect(result.count <= 1000)
    #expect(!result.contains(String(repeating: "A", count: 32)))
    #expect(result.contains("Authorization: Bearer <REDACTED>"))
}

@Test("AppState.runCLI launch-failure path redacts secrets in command summary and vault error log")
@MainActor
func runCLILaunchFailureRedactsSecrets() async throws {
    let root = FileManager.default.temporaryDirectory
        .appendingPathComponent("sb-launch-redact-\(UUID().uuidString)", isDirectory: true)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }

    let state = AppState()
    state.openVault(at: root)
    state.cliExecutablePathOverride = "/nonexistent/2nb-missing-\(UUID().uuidString)"

    var threw = false
    do {
        _ = try await state.runCLI(["status", "--api-key", "sk-or-v1-launchsecret12345"], cwd: root)
    } catch {
        threw = true
    }
    #expect(threw)

    // Allow the MainActor Task recording cli.exec failure to run.
    try? await Task.sleep(for: .milliseconds(50))

    let failedEvent = state.actionTracker.events.first(where: { $0.action == "cli.exec" && $0.phase == .failed })
    #expect(failedEvent != nil)
    #expect(failedEvent?.summary.contains("sk-or-v1-launchsecret12345") == false)
    #expect(failedEvent?.summary.contains("<REDACTED>") == true)

    let errorLogURL = root.appendingPathComponent(".2ndbrain/logs/app-errors.log")
    if let logText = try? String(contentsOf: errorLogURL, encoding: .utf8) {
        #expect(!logText.contains("sk-or-v1-launchsecret12345"))
    }
}
