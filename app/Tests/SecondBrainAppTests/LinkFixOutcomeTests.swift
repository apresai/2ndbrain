import Foundation
import Testing
@testable import SecondBrain

// Pins LinkFixOutcome.classify against the real `2nb repair-links`/`relink`/
// `unlink` JSON (the Go cli.PolishResult wire shape, links_skipped reasons
// included), so the sheet's dismiss/stay-open decision can't drift from what
// the CLI actually reports.

private func decodeResult(_ json: String) throws -> PolishResult {
    try JSONDecoder().decode(PolishResult.self, from: Data(json.utf8))
}

@Test("a repaired link classifies as success")
func classifyRepairedIsSuccess() throws {
    let res = try decodeResult("""
    {
      "path": "projects/source-doc.md",
      "original": "See [[auth flow]].",
      "polished": "See [[Auth Flow]].",
      "provider": "repair-links",
      "model": "",
      "duration_ms": 3,
      "links_repaired": [ { "raw": "auth flow", "new_target": "Auth Flow" } ]
    }
    """)
    #expect(LinkFixOutcome.classify(result: res, target: "auth flow", verb: "repair") == .success)
}

@Test("a repair alongside unrelated skips is still success")
func classifyRepairedWithSkipsIsSuccess() throws {
    let res = try decodeResult("""
    {
      "path": "a.md", "original": "x", "polished": "y",
      "provider": "repair-links", "model": "", "duration_ms": 4,
      "links_repaired": [ { "raw": "auth flow", "new_target": "Auth Flow" } ],
      "links_skipped": [ { "raw": "Other Topic", "reason": "no_match" } ]
    }
    """)
    #expect(LinkFixOutcome.classify(result: res, target: "auth flow", verb: "repair") == .success)
}

@Test("a no_match skip is actionable guidance, not a stale finding")
func classifyNoMatchIsActionable() throws {
    let res = try decodeResult("""
    {
      "path": "a.md", "original": "x", "polished": "x",
      "provider": "repair-links", "model": "", "duration_ms": 2,
      "links_skipped": [ { "raw": "Nonexistent Topic", "reason": "no_match" } ],
      "warning": "1 broken link(s) left unrepaired (no confident target)"
    }
    """)
    #expect(
        LinkFixOutcome.classify(result: res, target: "Nonexistent Topic", verb: "repair")
            == .actionable("No existing note matches [[Nonexistent Topic]]. Pick a suggestion below, create it, or unlink.")
    )
}

@Test("an ambiguous skip is actionable guidance")
func classifyAmbiguousIsActionable() throws {
    let res = try decodeResult("""
    {
      "path": "a.md", "original": "x", "polished": "x",
      "provider": "repair-links", "model": "", "duration_ms": 2,
      "links_skipped": [ { "raw": "setup", "reason": "ambiguous" } ]
    }
    """)
    #expect(
        LinkFixOutcome.classify(result: res, target: "setup", verb: "repair")
            == .actionable("[[setup]] matches more than one note. Pick the right one below.")
    )
}

@Test("no repair and no skip entry classifies as stale")
func classifyBareResultIsStale() throws {
    // relink/unlink report a matched-nothing run as a bare result: nothing
    // repaired, nothing skipped. Same shape when repair-links finds no such
    // link. The note changed since the lint report was produced.
    let res = try decodeResult("""
    {
      "path": "a.md", "original": "x", "polished": "x",
      "provider": "relink", "model": "", "duration_ms": 1
    }
    """)
    #expect(
        LinkFixOutcome.classify(result: res, target: "gone", verb: "repoint")
            == .stale("No [[gone]] link found to repoint. The note changed since the last check.")
    )
}

@Test("a skip entry for a different target does not mask staleness")
func classifySkipForOtherTargetIsStale() throws {
    let res = try decodeResult("""
    {
      "path": "a.md", "original": "x", "polished": "x",
      "provider": "repair-links", "model": "", "duration_ms": 1,
      "links_skipped": [ { "raw": "other link", "reason": "no_match" } ]
    }
    """)
    #expect(
        LinkFixOutcome.classify(result: res, target: "gone", verb: "unlink")
            == .stale("No [[gone]] link found to unlink. The note changed since the last check.")
    )
}

@Test("an unrecognized skip reason stays actionable, never stale")
func classifyUnknownReasonIsActionable() throws {
    // The link was found (it has a skip entry), so dismissing as stale would
    // be wrong even if a future CLI grows a new reason code.
    let res = try decodeResult("""
    {
      "path": "a.md", "original": "x", "polished": "x",
      "provider": "repair-links", "model": "", "duration_ms": 1,
      "links_skipped": [ { "raw": "weird", "reason": "some_future_reason" } ]
    }
    """)
    #expect(
        LinkFixOutcome.classify(result: res, target: "weird", verb: "repair")
            == .actionable("[[weird]] could not be fixed automatically. Pick an option below.")
    )
}
