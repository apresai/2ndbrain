import Foundation
import Testing
@testable import SecondBrain

@Test("CLIAskResponse decodes ask --json output with optional rewritten_query and warnings")
func cliAskResponseDecodes() throws {
    let full = """
    {
      "mode": "rag",
      "warnings": [],
      "answer": "2nb uses Gemini 4 Hydrogen.",
      "sources": ["decisions/gemini.md"],
      "rewritten_query": "2nb Gemini 4 Hydrogen model"
    }
    """.data(using: .utf8)!
    let decoded = try JSONDecoder().decode(CLIAskResponse.self, from: full)
    #expect(decoded.mode == "rag")
    #expect(decoded.answer == "2nb uses Gemini 4 Hydrogen.")
    #expect(decoded.sources == ["decisions/gemini.md"])
    #expect(decoded.rewrittenQuery == "2nb Gemini 4 Hydrogen model")

    let degraded = """
    {
      "mode": "bm25",
      "warnings": ["Embeddings unavailable; using keyword fallback"],
      "answer": "Keyword answer",
      "sources": []
    }
    """.data(using: .utf8)!
    let degradedDecoded = try JSONDecoder().decode(CLIAskResponse.self, from: degraded)
    #expect(degradedDecoded.rewrittenQuery == nil)
    #expect(AskSearchPresentation.isDegradedBM25(
        mode: degradedDecoded.mode,
        warnings: degradedDecoded.warnings ?? []
    ))
}

@Test("CLISearchResponse and CLISearchResult decode search --json output")
func cliSearchResponseDecodes() throws {
    let json = """
    {
      "mode": "hybrid",
      "warnings": [],
      "results": [
        {
          "doc_id": "d1",
          "path": "notes/gemini.md",
          "title": "Gemini Migration",
          "type": "note",
          "chunk_id": "c1",
          "heading_path": "Architecture",
          "content": "Gemini 4 Hydrogen and gemini-embedding-2",
          "score": 0.91,
          "vector_score": 0.87,
          "status": "final"
        }
      ]
    }
    """.data(using: .utf8)!
    let decoded = try JSONDecoder().decode(CLISearchResponse.self, from: json)
    #expect(decoded.mode == "hybrid")
    #expect(decoded.results.count == 1)
    let hit = try #require(decoded.results.first)
    #expect(hit.id == "c1")
    #expect(hit.docID == "d1")
    #expect(hit.vectorScore == 0.87)
    #expect(hit.headingPath == "Architecture")
}

@Test("VaultNoteItem and StaleDocInfo decode and compute display fields")
func vaultNoteAndStaleDecodes() throws {
    let noteJSON = """
    [
      {"id":"1","path":"projects/alpha.md","title":"Alpha Plan","type":"project","status":"active","modified_at":"2026-11-10"},
      {"id":"2","path":"notes/untitled.md","title":"","type":null,"status":null,"modified_at":null}
    ]
    """.data(using: .utf8)!
    let notes = try JSONDecoder().decode([VaultNoteItem].self, from: noteJSON)
    #expect(notes[0].displayTitle == "Alpha Plan")
    #expect(notes[1].displayTitle == "untitled.md")

    let staleJSON = """
    [{"path":"old/legacy.md","title":"Legacy Note","days_stale":142}]
    """.data(using: .utf8)!
    let stale = try JSONDecoder().decode([StaleDocInfo].self, from: staleJSON)
    #expect(stale.first?.id == "old/legacy.md")
    #expect(stale.first?.daysStale == 142)
}

@Test("AskSearchPresentation formats multi-turn history and detects BM25 degradation")
func askSearchPresentationLogic() {
    let exchanges = [
        ConversationExchange(
            question: "First?",
            answer: "First answer.",
            sources: ["a.md"],
            rewrittenQuery: nil,
            mode: "rag",
            warnings: []
        ),
        ConversationExchange(
            question: "Second?",
            answer: "Second answer.",
            sources: [],
            rewrittenQuery: "Expanded second",
            mode: "rag",
            warnings: []
        ),
    ]
    let turns = AskSearchPresentation.historyTurns(from: exchanges)
    #expect(turns == [
        AIChatTurn(role: "user", content: "First?"),
        AIChatTurn(role: "assistant", content: "First answer."),
        AIChatTurn(role: "user", content: "Second?"),
        AIChatTurn(role: "assistant", content: "Second answer."),
    ])
    #expect(!AskSearchPresentation.isDegradedBM25(mode: "rag", warnings: []))
    #expect(AskSearchPresentation.isDegradedBM25(mode: "bm25", warnings: []))
    #expect(AskSearchPresentation.isDegradedBM25(mode: "hybrid", warnings: ["fallback"]))
}

@Test("NotePolishPresentation detects diffs and summarizes link actions")
func notePolishPresentationLogic() {
    let unchanged = PolishResult(
        path: "a.md", original: "same", polished: "same",
        provider: "gemini", model: "gemini-4-hydrogen", durationMs: 250,
        linksAdded: nil, linksRepaired: nil, linksSkipped: nil, warning: nil
    )
    #expect(!NotePolishPresentation.hasChanges(unchanged))

    let changed = PolishResult(
        path: "a.md", original: "old", polished: "new [[Link]]",
        provider: "gemini", model: "gemini-4-hydrogen", durationMs: 1200,
        linksAdded: ["Link"],
        linksRepaired: [RepairLinkRepair(raw: "lnk", newTarget: "Link", reason: nil)],
        linksSkipped: [RepairLinkRepair(raw: "missing", newTarget: nil, reason: "no_match")],
        warning: nil
    )
    #expect(NotePolishPresentation.hasChanges(changed))
    let summary = NotePolishPresentation.summaryLine(changed)
    #expect(summary.contains("gemini-4-hydrogen"))
    #expect(summary.contains("1 link suggested"))
    #expect(summary.contains("1 link repaired"))
    #expect(summary.contains("1 unresolved"))
}

@Test("NotePolishPresentation gates schema/backlinks changes and formats batch undo banners")
func notePolishPresentationBacklinksAndSchema() {
    let schemaOnly = PolishResult(
        path: "a.md", original: "same", polished: "same",
        provider: "gemini", model: "gemini-4-hydrogen", durationMs: 100,
        frontmatterFixes: [FixFieldChange(field: "status", from: "open", to: "complete", reason: "schema_enum")],
        schemaRepaired: true
    )
    #expect(NotePolishPresentation.hasChanges(schemaOnly, applyBacklinks: false))
    #expect(NotePolishPresentation.summaryLine(schemaOnly).contains("1 schema fix"))

    let backlinksOnly = PolishResult(
        path: "a.md", original: "same", polished: "same",
        provider: "gemini", model: "gemini-4-hydrogen", durationMs: 100,
        goal: "Deprecate Gateway",
        backlinkPreviews: [
            PolishBacklinkChange(path: "notes/svc-a.md", original: "old", polished: "new", changes: ["repaired_link"], applied: false),
        ],
        backlinksApplied: 0,
        undoBatchAvailable: false
    )
    #expect(!NotePolishPresentation.hasChanges(backlinksOnly, applyBacklinks: false))
    #expect(NotePolishPresentation.hasChanges(backlinksOnly, applyBacklinks: true))
    let previewSummary = NotePolishPresentation.summaryLine(backlinksOnly)
    #expect(previewSummary.contains("1 backlink preview"))
    #expect(previewSummary.contains("goal: Deprecate Gateway"))

    let singleUndo = PolishUndoResult(path: "a.md", reverted: true, restoredPaths: ["a.md"])
    #expect(NotePolishPresentation.undoBanner(singleUndo) == "Reverted a.md to its pre-polish backup.")

    let batchUndo = PolishUndoResult(path: "target.md", reverted: true, restoredPaths: ["target.md", "notes/svc-a.md", "notes/svc-b.md"])
    let batchMsg = NotePolishPresentation.undoBanner(batchUndo)
    #expect(batchMsg.contains("2 linked note(s)"))
    #expect(batchMsg.contains("notes/svc-a.md"))
}
