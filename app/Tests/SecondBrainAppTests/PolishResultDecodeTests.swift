import Foundation
import Testing
@testable import SecondBrain

// Pins the Swift decoder to the exact `2nb repair-links` JSON contract
// (cli.PolishResult). If the Go field names/casing drift, this fails.
@Test("PolishResult decodes the repair-links JSON contract")
func polishResultDecodesRepairContract() throws {
    let json = """
    {
      "path": "projects/source-doc.md",
      "original": "See [[auth flow]] and [[JWT Tokens]].\\n\\nAlso [[Nonexistent Topic]].\\n",
      "polished": "See [[Auth Flow]] and [[JWT Tokens]].\\n\\nAlso [[Nonexistent Topic]].\\n",
      "provider": "repair-links",
      "model": "",
      "duration_ms": 3,
      "links_repaired": [ { "raw": "auth flow", "new_target": "Auth Flow" } ],
      "links_skipped": [ { "raw": "Nonexistent Topic", "reason": "no_match" } ],
      "warning": "1 broken link(s) left unrepaired (no confident target)"
    }
    """.data(using: .utf8)!

    let res = try JSONDecoder().decode(PolishResult.self, from: json)
    #expect(res.provider == "repair-links")
    #expect(res.model == "")
    #expect(res.durationMs == 3)
    #expect(res.linksAdded == nil)            // omitted by repair-links
    #expect(res.linksRepaired?.count == 1)
    #expect(res.linksRepaired?.first?.raw == "auth flow")
    #expect(res.linksRepaired?.first?.newTarget == "Auth Flow")
    #expect(res.linksSkipped?.first?.reason == "no_match")
    #expect(res.linksSkipped?.first?.newTarget == nil)
    #expect(res.warning?.isEmpty == false)
}

@Test("PolishResult decodes a clean no-op repair (no repaired/skipped/warning)")
func polishResultDecodesNoOp() throws {
    let json = """
    { "path": "clean.md", "original": "x", "polished": "x",
      "provider": "repair-links", "model": "", "duration_ms": 1 }
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode(PolishResult.self, from: json)
    #expect(res.linksRepaired == nil)
    #expect(res.linksSkipped == nil)
    #expect(res.warning == nil)
}

@Test("PolishUndoResult decodes the polish --undo JSON contract")
func polishUndoResultDecodes() throws {
    let json = """
    {"path": "notes/foo.md", "reverted": true}
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode(PolishUndoResult.self, from: json)
    #expect(res.path == "notes/foo.md")
    #expect(res.reverted)
    #expect(res.restoredPaths == nil)
}

@Test("PolishResult decodes M2 goal, schema repairs, backlink previews, and batch undo metadata")
func polishResultDecodesM2Fields() throws {
    let json = """
    {
      "path": "target.md",
      "original": "old",
      "polished": "new",
      "provider": "gemini",
      "model": "gemini-4-hydrogen",
      "duration_ms": 420,
      "goal": "Rewrite as 3 crisp executive bullets",
      "frontmatter_fixes": [
        { "field": "status", "from": "open", "to": "complete", "reason": "schema_enum" }
      ],
      "schema_repaired": true,
      "backlink_previews": [
        {
          "path": "notes/svc-a.md",
          "original": "Uses [[Gateway Service]]",
          "polished": "Uses [[Edge Proxy]]",
          "changes": ["repaired_link", "aligned_context"],
          "applied": false
        },
        {
          "path": "notes/svc-b.md",
          "original": "Depends on [[Gateway Service]]",
          "polished": "Depends on [[Edge Proxy]]",
          "changes": ["linked_mention"],
          "applied": true
        }
      ],
      "backlinks_applied": 1,
      "undo_batch_available": true
    }
    """.data(using: .utf8)!

    let res = try JSONDecoder().decode(PolishResult.self, from: json)
    #expect(res.goal == "Rewrite as 3 crisp executive bullets")
    #expect(res.schemaRepaired == true)
    #expect(res.frontmatterFixes?.count == 1)
    #expect(res.frontmatterFixes?.first?.field == "status")
    #expect(res.backlinkPreviews?.count == 2)
    #expect(res.backlinkPreviews?[0].applied == false)
    #expect(res.backlinkPreviews?[0].changes == ["repaired_link", "aligned_context"])
    #expect(res.backlinkPreviews?[1].applied == true)
    #expect(res.backlinksApplied == 1)
    #expect(res.undoBatchAvailable == true)
}

@Test("PolishUndoResult decodes batch restored_paths")
func polishUndoResultDecodesBatch() throws {
    let json = """
    {
      "path": "target.md",
      "reverted": true,
      "restored_paths": ["target.md", "notes/svc-a.md", "notes/svc-b.md"]
    }
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode(PolishUndoResult.self, from: json)
    #expect(res.path == "target.md")
    #expect(res.reverted == true)
    #expect(res.restoredPaths == ["target.md", "notes/svc-a.md", "notes/svc-b.md"])
}

@Test("NotePolishPresentation.undoBanner reports informational noop when reverted is false")
func polishUndoBannerHandlesNoop() {
    let noop = PolishUndoResult(path: "notes/unchanged.md", reverted: false, restoredPaths: [])
    let banner = NotePolishPresentation.undoBanner(noop)
    #expect(banner.contains("notes/unchanged.md"))
    #expect(banner.contains("nothing to undo"))
    #expect(!banner.hasPrefix("Reverted "))
}
