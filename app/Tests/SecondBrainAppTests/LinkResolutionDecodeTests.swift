import Foundation
import Testing
@testable import SecondBrain

// Pins the Swift decoders to the exact JSON contracts the LinkResolutionSheet
// drives: `2nb suggest-target`, `2nb create`, and the relink/unlink variants of
// cli.PolishResult. If the Go field names/casing drift, these fail.

@Test("SuggestTargetResult decodes the suggest-target array contract")
func suggestTargetDecodesContract() throws {
    let json = """
    [
      { "path": "resources/go-mod-why-phantom-indirect-updates.md",
        "title": "Go mod why phantom indirect updates",
        "score": 0.71,
        "snippet": "first chars of body…" },
      { "path": "resources/apresai-models.md", "title": "", "score": 6.6, "snippet": "" }
    ]
    """.data(using: .utf8)!

    let res = try JSONDecoder().decode([SuggestTargetResult].self, from: json)
    #expect(res.count == 2)
    #expect(res[0].path == "resources/go-mod-why-phantom-indirect-updates.md")
    #expect(res[0].displayTitle == "Go mod why phantom indirect updates")
    #expect(res[0].id == res[0].path)
    // A title-less note falls back to the basename for display.
    #expect(res[1].title == "")
    #expect(res[1].displayTitle == "apresai-models.md")
}

@Test("SuggestTargetResult decodes an empty array (no candidates)")
func suggestTargetDecodesEmpty() throws {
    let res = try JSONDecoder().decode([SuggestTargetResult].self, from: Data("[]".utf8))
    #expect(res.isEmpty)
}

@Test("SuggestTargetResult decodes the additive confidence field")
func suggestTargetDecodesConfidence() throws {
    let json = """
    [
      { "path": "resources/ghostty-config.md", "title": "Ghostty Config",
        "score": 3.1, "snippet": "", "confidence": "medium" },
      { "path": "resources/x.md", "title": "X", "score": 0.2, "snippet": "" }
    ]
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode([SuggestTargetResult].self, from: json)
    #expect(res[0].confidence == "medium")
    // Absent on a pre-confidence CLI (and on suggest-links) → nil, not a crash.
    #expect(res[1].confidence == nil)
}

@Test("SuggestTargetResult decodes the additive reason field from --llm")
func suggestTargetDecodesReason() throws {
    let json = """
    [
      { "path": "resources/ghostty-config.md", "title": "Ghostty Config",
        "score": 3.1, "snippet": "", "confidence": "medium",
        "reason": "Same terminal theme topic as the surrounding note" },
      { "path": "resources/x.md", "title": "X", "score": 0.2, "snippet": "" }
    ]
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode([SuggestTargetResult].self, from: json)
    #expect(res[0].reason == "Same terminal theme topic as the surrounding note")
    #expect(res[1].reason == nil)
}

@Test("LintIssue decodes the additive broken-wikilink classification fields")
func lintIssueDecodesClassification() throws {
    let json = """
    { "issues": [
        { "path": "notes/ghostty.md", "line": 12, "level": "warning",
          "message": "broken wikilink: [[ghostty]]",
          "target": "ghostty", "fix": "drift", "drift_target": "Ghostty Config" },
        { "path": "notes/plain.md", "level": "warning",
          "message": "broken wikilink: [[nope]]", "target": "nope", "fix": "missing" }
      ], "files_checked": 2, "errors": 0, "warnings": 2 }
    """.data(using: .utf8)!
    let report = try JSONDecoder().decode(LintReport.self, from: json)
    #expect(report.issues[0].target == "ghostty")
    #expect(report.issues[0].fix == "drift")
    #expect(report.issues[0].driftTarget == "Ghostty Config")
    #expect(report.issues[1].fix == "missing")
    #expect(report.issues[1].driftTarget == nil)
}

@Test("LintIssue tolerates an older CLI with no classification fields")
func lintIssueDecodesOldCLI() throws {
    let json = """
    { "issues": [
        { "path": "n.md", "level": "warning", "message": "broken wikilink: [[x]]" }
      ], "files_checked": 1, "errors": 0, "warnings": 1 }
    """.data(using: .utf8)!
    let report = try JSONDecoder().decode(LintReport.self, from: json)
    #expect(report.issues[0].target == nil)
    #expect(report.issues[0].fix == nil)
    #expect(report.issues[0].message == "broken wikilink: [[x]]")
}

@Test("CreateResult decodes the create JSON contract")
func createResultDecodesContract() throws {
    let json = """
    { "id": "db2f2300-06b0-4afc-96d9-5f4df015d299", "path": "dependency-management.md",
      "title": "Dependency Management", "type": "note" }
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode(CreateResult.self, from: json)
    #expect(res.path == "dependency-management.md")
    #expect(res.title == "Dependency Management")
    #expect(res.type == "note")
}

@Test("PolishResult decodes the unlink contract (empty new_target)")
func polishResultDecodesUnlink() throws {
    let json = """
    { "path": "resources/note.md", "original": "See [[083477d]] now.",
      "polished": "See 083477d now.", "provider": "unlink", "model": "",
      "duration_ms": 2, "links_repaired": [ { "raw": "083477d", "new_target": "" } ] }
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode(PolishResult.self, from: json)
    #expect(res.provider == "unlink")
    #expect(res.linksRepaired?.count == 1)
    #expect(res.linksRepaired?.first?.raw == "083477d")
    // Empty new_target means "no retarget", just bracket-strip.
    #expect(res.linksRepaired?.first?.newTarget == "")
    #expect(res.polished == "See 083477d now.")
}

@Test("PolishResult decodes the relink contract")
func polishResultDecodesRelink() throws {
    let json = """
    { "path": "n.md", "original": "[[go-modules]]", "polished": "[[go-mod-why]]",
      "provider": "relink", "model": "", "duration_ms": 1,
      "links_repaired": [ { "raw": "go-modules", "new_target": "go-mod-why" } ] }
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode(PolishResult.self, from: json)
    #expect(res.provider == "relink")
    #expect(res.linksRepaired?.first?.newTarget == "go-mod-why")
}

// A no-op edit (stale finding: the link no longer exists) comes back exit 0 with
// empty links_repaired + a warning. The sheet keys off empty links_repaired to
// show a problem instead of a false success banner.
@Test("PolishResult decodes a no-op edit (empty links_repaired + warning)")
func polishResultDecodesNoopEdit() throws {
    let json = """
    { "path": "n.md", "original": "x", "polished": "x", "provider": "unlink",
      "model": "", "duration_ms": 1, "links_repaired": [],
      "warning": "no [[083477d]] link found to unlink" }
    """.data(using: .utf8)!
    let res = try JSONDecoder().decode(PolishResult.self, from: json)
    #expect(res.linksRepaired?.isEmpty == true)
    #expect(res.warning?.isEmpty == false)
}
