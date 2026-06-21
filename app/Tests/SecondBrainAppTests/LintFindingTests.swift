import Testing
@testable import SecondBrain

@Test("classify recognizes a broken wikilink and extracts the target")
func classifyBrokenLink() {
    #expect(LintFinding.classify(message: "broken wikilink: [[Q3 plan]]") == .brokenLink(target: "Q3 plan"))
    #expect(LintFinding.classify(message: "broken wikilink: [[NonexistentNote]]") == .brokenLink(target: "NonexistentNote"))
}

@Test("classify recognizes a missing required field")
func classifyMissingField() {
    #expect(
        LintFinding.classify(message: "missing required field 'status' for type 'prd'")
            == .missingField(field: "status", type: "prd")
    )
}

@Test("classify recognizes an invalid enum and parses the allowed list")
func classifyInvalidEnum() {
    #expect(
        LintFinding.classify(message: "field 'status' value 'pending' not in [draft completed archived]")
            == .invalidEnum(field: "status", value: "pending", allowed: ["draft", "completed", "archived"])
    )
}

@Test("classify recognizes a parse error")
func classifyParseError() {
    #expect(LintFinding.classify(message: "parse error: yaml: line 5: mapping values are not allowed") == .parseError)
}

@Test("classify falls back to .other for unknown messages")
func classifyOther() {
    #expect(LintFinding.classify(message: "some new diagnostic we don't model yet") == .other)
}

@Test("classify does not mistake an empty broken-link target for a finding")
func classifyEmptyBrokenLink() {
    // `[[]]` has no target — not actionable as a broken-link repair.
    #expect(LintFinding.classify(message: "broken wikilink: [[]]") == .other)
}

@Test("invalid-enum with a single allowed value still parses")
func classifyInvalidEnumSingle() {
    #expect(
        LintFinding.classify(message: "field 'kind' value 'x' not in [only]")
            == .invalidEnum(field: "kind", value: "x", allowed: ["only"])
    )
}

@Test("invalid-enum with an empty allowed set is not actionable (.other)")
func classifyInvalidEnumEmptyAllowed() {
    // No valid choices to offer → no Set value… picker; fall back to .other.
    #expect(LintFinding.classify(message: "field 'kind' value 'x' not in []") == .other)
}
