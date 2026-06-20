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
