import Foundation
import SecondBrainCore
import SwiftUI
import Testing
@testable import SecondBrain

private func makeSearchResult(
    id: String,
    path: String,
    title: String,
    score: Double
) -> CLISearchResult {
    CLISearchResult(
        docID: id,
        path: path,
        title: title,
        docType: "note",
        chunkID: "\(id)-c0",
        headingPath: nil,
        content: "Sample excerpt for \(title)",
        score: score,
        vectorScore: score,
        status: nil
    )
}

private func makeAIStatus(
    embedAvailable: Bool,
    genAvailable: Bool,
    documentCount: Int,
    embeddingCount: Int,
    portabilityStatus: String? = nil
) throws -> AIStatusInfo {
    var payload: [String: Any] = [
        "provider": "gemini",
        "embedding_model": "gemini-embedding-2",
        "generation_model": "gemini-4-hydrogen",
        "dimensions": 768,
        "embed_available": embedAvailable,
        "gen_available": genAvailable,
        "embedding_count": embeddingCount,
        "document_count": documentCount,
    ]
    if let portabilityStatus {
        payload["portability_status"] = portabilityStatus
    }
    let data = try JSONSerialization.data(withJSONObject: payload)
    return try JSONDecoder().decode(AIStatusInfo.self, from: data)
}

@Test("KeyCombo parses modifiers, builds keycaps, and rejects bare letter keys")
func keyComboParsing() {
    let defaultCombo = KeyCombo("ctrl+shift+space")
    #expect(defaultCombo != nil)
    #expect(defaultCombo?.caps == ["⌃", "⇧", "Space"])
    #expect(defaultCombo?.display == "⌃⇧Space")
    #expect(defaultCombo?.spec == "ctrl+shift+space")
    #expect(defaultCombo?.menuEquivalent?.0 == " ")
    #expect(defaultCombo?.menuEquivalent?.1 == [.control, .shift])

    let optSpace = KeyCombo("opt+space")
    #expect(optSpace?.display == "⌥Space")
    #expect(optSpace?.spec == "opt+space")

    let cmdShiftK = KeyCombo("cmd+shift+k")
    #expect(cmdShiftK?.caps == ["⇧", "⌘", "K"])
    #expect(cmdShiftK?.menuEquivalent?.0 == "k")

    #expect(KeyCombo("a") == nil)
    #expect(KeyCombo("space") == nil)
    #expect(KeyCombo("shift+a") == nil)
    #expect(KeyCombo("f5") != nil)
}

@Test("SetupChecklist surfaces missing steps and disappears completely once setup is satisfied")
func setupChecklistDisappearingLogic() throws {
    let unready = try makeAIStatus(
        embedAvailable: false,
        genAvailable: false,
        documentCount: 0,
        embeddingCount: 0
    )
    let allMissing = SetupChecklist.missingItems(
        hasVault: false,
        aiStatus: unready,
        configuredClients: 0,
        clientsDismissed: false
    )
    #expect(allMissing.map(\.kind) == [.vault, .gemini, .clients])

    let unindexed = try makeAIStatus(
        embedAvailable: true,
        genAvailable: true,
        documentCount: 12,
        embeddingCount: 0
    )
    let needsIndex = SetupChecklist.missingItems(
        hasVault: true,
        aiStatus: unindexed,
        configuredClients: 0,
        clientsDismissed: true
    )
    #expect(needsIndex.map(\.kind) == [.index])

    let ready = try makeAIStatus(
        embedAvailable: true,
        genAvailable: true,
        documentCount: 12,
        embeddingCount: 12,
        portabilityStatus: "ok"
    )
    let complete = SetupChecklist.missingItems(
        hasVault: true,
        aiStatus: ready,
        configuredClients: 1,
        clientsDismissed: false
    )
    #expect(complete.isEmpty)

    let completeDismissed = SetupChecklist.missingItems(
        hasVault: true,
        aiStatus: ready,
        configuredClients: 0,
        clientsDismissed: true
    )
    #expect(completeDismissed.isEmpty)
}

@Test("PartitionedSearchResults folds hits below 50% of top score while keeping at least one strong hit")
func partitionedSearchResultsThresholding() {
    let empty = PartitionedSearchResults([])
    #expect(empty.strong.isEmpty)
    #expect(empty.weaker.isEmpty)

    let hits = [
        makeSearchResult(id: "1", path: "a.md", title: "Top Note", score: 0.90),
        makeSearchResult(id: "2", path: "b.md", title: "Strong Note", score: 0.50),
        makeSearchResult(id: "3", path: "c.md", title: "Boundary Note", score: 0.45),
        makeSearchResult(id: "4", path: "d.md", title: "Weak Note", score: 0.20),
    ]
    let partitioned = PartitionedSearchResults(hits)
    #expect(partitioned.strong.map(\.docID) == ["1", "2", "3"])
    #expect(partitioned.weaker.map(\.docID) == ["4"])

    let zeroTop = [
        makeSearchResult(id: "z1", path: "z1.md", title: "Zero One", score: 0),
        makeSearchResult(id: "z2", path: "z2.md", title: "Zero Two", score: 0),
    ]
    let partitionedZeros = PartitionedSearchResults(zeroTop)
    #expect(partitionedZeros.strong.map(\.docID) == ["z1", "z2"])
    #expect(partitionedZeros.weaker.isEmpty)
}

@Test("NoteCitation builds wikilinks safely and strips frontmatter without Yams.load")
func noteCitationAndPreviewSafety() {
    #expect(NoteCitation.wikilink(path: "projects/gemini.md", title: "Gemini Migration") == "[[Gemini Migration]]")
    #expect(NoteCitation.wikilink(path: "daily/2026-11-14.md", title: "  ") == "[[2026-11-14]]")

    let templateNote = """
    ---
    title: Daily {{date}}
    tags: [daily, {{type}}]
    ---

    # Morning Plan
    Review [[Gemini 4]] rollout.
    """
    let preview = NoteCitation.previewBody(fromMarkdown: templateNote)
    #expect(preview.hasPrefix("# Morning Plan"))
    #expect(!preview.contains("title: Daily {{date}}"))

    let truncated = NoteCitation.previewBody(fromMarkdown: String(repeating: "a", count: 100), maxChars: 20)
    #expect(truncated.hasPrefix(String(repeating: "a", count: 20)))
    #expect(truncated.hasSuffix("…"))
}

@Test("OnboardingState gates progression until a vault is bound and transitions cleanly across 3 steps")
@MainActor
func onboardingStateTransitions() {
    let state = OnboardingState(step: .vault, hasBoundVault: false)
    #expect(state.step == .vault)
    #expect(!state.advance())
    #expect(state.step == .vault)

    state.go(to: .gemini)
    #expect(state.step == .vault)

    state.hasBoundVault = true
    #expect(state.advance())
    #expect(state.step == .gemini)
    #expect(state.advance())
    #expect(state.step == .shortcutAndClients)
    #expect(!state.advance())

    #expect(state.back())
    #expect(state.step == .gemini)
    #expect(state.back())
    #expect(state.step == .vault)
    #expect(!state.back())
}

@Test("AppState manages pauseAutoSync and indexFraction cleanly under 2NB_TEST=1")
@MainActor
func appStatePauseAndIndexFraction() {
    let appState = AppState()
    #expect(!appState.pauseAutoSync)
    #expect(appState.indexFraction == nil)

    appState.setAutoSyncPaused(true)
    #expect(appState.pauseAutoSync)
    appState.setAutoSyncPaused(false)
    #expect(!appState.pauseAutoSync)

    appState.isIndexing = true
    #expect(appState.indexFraction == 0.06)

    appState.embeddingProgress = EmbeddingProgress(current: 5, total: 10)
    #expect(appState.indexFraction == 0.5)

    appState.embeddingProgress = EmbeddingProgress(current: 10, total: 10)
    #expect(appState.indexFraction == 0.98)

    appState.isIndexing = false
    #expect(appState.indexFraction == nil)
}

@Test("Workspace theme, onboarding, menu bar, and HUD components construct cleanly")
@MainActor
func workspaceViewsConstructCleanly() {
    let appState = AppState()
    let card = ThemedCard { Text("Card") }
    let caps = Keycaps(caps: ["⌃", "⇧", "Space"])
    let preview = NotePreviewPane(
        vaultRoot: nil,
        result: nil,
        onOpenObsidian: { _ in },
        onRevealFinder: { _ in },
        onCopyCitation: { _ in }
    )
    let onboarding = OnboardingView().environment(appState)
    let menuStatus = MenuStatusView().environment(appState)
    let hud = QuickSearchPanelView().environment(appState)

    #expect(String(describing: type(of: card)).hasPrefix("ThemedCard"))
    #expect(String(describing: type(of: caps)) == "Keycaps")
    #expect(String(describing: type(of: preview)) == "NotePreviewPane")
    #expect(String(describing: type(of: onboarding)).contains("OnboardingView"))
    #expect(String(describing: type(of: menuStatus)).contains("MenuStatusView"))
    #expect(String(describing: type(of: hud)).contains("QuickSearchPanelView"))
}

@Test("AppState.setKeyStdinData trims whitespace and appends a trailing newline for CLI stdin")
@MainActor
func setKeyStdinDataAppendsNewline() {
    let dummyKey = "AIza" + "SyExample"
    #expect(AppState.setKeyStdinData("  \(dummyKey)  ") == Data("\(dummyKey)\n".utf8))
}

@Test("HomeAI.portabilityBanner surfaces dimension breaks, model mismatches, and upgrade hints")
func homeAIPortabilityBanner() throws {
    let dimBreakJSON = """
    {"provider":"gemini","embedding_model":"gemini-embedding-2","generation_model":"gemini-4-hydrogen","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":417,"document_count":417,"vault_embedding_dim":1024,"portability_status":"dimension_break"}
    """
    let dimBreak = try JSONDecoder().decode(AIStatusInfo.self, from: Data(dimBreakJSON.utf8))
    let dimBanner = try #require(HomeAI.portabilityBanner(dimBreak))
    #expect(dimBanner.buttonTitle == "Re-embed All")
    #expect(dimBanner.forceReembed == true)
    #expect(dimBanner.message.contains("1024d"))
    #expect(dimBanner.message.contains("768d"))

    let mixed = try makeAIStatus(
        embedAvailable: true,
        genAvailable: true,
        documentCount: 10,
        embeddingCount: 10,
        portabilityStatus: "mixed"
    )
    let mixedBanner = try #require(HomeAI.portabilityBanner(mixed))
    #expect(mixedBanner.buttonTitle == "Re-embed All")
    #expect(mixedBanner.forceReembed == true)

    let modelMismatch = try makeAIStatus(
        embedAvailable: true,
        genAvailable: true,
        documentCount: 10,
        embeddingCount: 10,
        portabilityStatus: "model_mismatch"
    )
    let mismatchBanner = try #require(HomeAI.portabilityBanner(modelMismatch))
    #expect(mismatchBanner.buttonTitle == "Re-embed All")
    #expect(mismatchBanner.forceReembed == true)

    let upgradeReembed = try makeAIStatus(
        embedAvailable: true,
        genAvailable: true,
        documentCount: 10,
        embeddingCount: 10,
        portabilityStatus: "upgrade_reembed_recommended"
    )
    let reembedBanner = try #require(HomeAI.portabilityBanner(upgradeReembed))
    #expect(reembedBanner.buttonTitle == "Re-embed")
    #expect(reembedBanner.forceReembed == true)

    let upgradeReindex = try makeAIStatus(
        embedAvailable: true,
        genAvailable: true,
        documentCount: 10,
        embeddingCount: 10,
        portabilityStatus: "upgrade_reindex_recommended"
    )
    let reindexBanner = try #require(HomeAI.portabilityBanner(upgradeReindex))
    #expect(reindexBanner.buttonTitle == "Reindex")
    #expect(reindexBanner.forceReembed == false)

    let ok = try makeAIStatus(
        embedAvailable: true,
        genAvailable: true,
        documentCount: 10,
        embeddingCount: 10,
        portabilityStatus: "ok"
    )
    #expect(HomeAI.portabilityBanner(ok) == nil)
}

@Test("SetupChecklist surfaces Re-embed All action when portabilityStatus is dimension_break")
func setupChecklistIncludesDimensionBreak() throws {
    let dimBreak = try makeAIStatus(
        embedAvailable: true,
        genAvailable: true,
        documentCount: 417,
        embeddingCount: 417,
        portabilityStatus: "dimension_break"
    )
    let items = SetupChecklist.missingItems(
        hasVault: true,
        aiStatus: dimBreak,
        configuredClients: 1,
        clientsDismissed: false
    )
    #expect(items.count == 1)
    #expect(items.first?.kind == .index)
    #expect(items.first?.actionTitle == "Re-embed All")
}
