import Foundation
import SecondBrainCore
import Testing
@testable import SecondBrain

@Test("FixVaultReport decodes exact Go JSON payload")
func decodeFixVaultReport() throws {
    let json = """
    {
      "files_scanned": 42,
      "notes_changed": 2,
      "total_fixes": 5,
      "schema_updated": true,
      "schema_statuses_added": ["active", "reference"],
      "schema_issues_resolved": 2,
      "links_updated": 1,
      "links_unlinked": 1,
      "formatting_fixed": 1,
      "written": false,
      "undo_available": true,
      "notes": [
        {
          "path": "Notes/broken.md",
          "link_actions": [
            { "action": "format", "from": "Guides\\\\CLI.md", "to": "Guides/CLI", "count": 1, "reason": "link_syntax" },
            { "action": "relink", "from": "claude-code", "to": "Claude Code CLI", "count": 1, "reason": "obvious_match" }
          ],
          "frontmatter_fixes": [
            { "field": "created", "from": "\\"2026-04-20T10:00:00Z\\"", "to": "2026-04-20T10:00:00Z", "reason": "unquote_date" }
          ]
        }
      ],
      "ambiguous": [
        { "path": "Notes/amb.md", "target": "dup", "candidates": ["one/dup.md", "two/dup.md"] }
      ]
    }
    """
    let decoded = try JSONDecoder().decode(FixVaultReport.self, from: Data(json.utf8))
    #expect(decoded.filesScanned == 42)
    #expect(decoded.notesChanged == 2)
    #expect(decoded.totalFixes == 5)
    #expect(decoded.schemaUpdated == true)
    #expect(decoded.schemaStatusesAdded == ["active", "reference"])
    #expect(decoded.schemaIssuesResolved == 2)
    #expect(decoded.linksUpdated == 1)
    #expect(decoded.linksUnlinked == 1)
    #expect(decoded.formattingFixed == 1)
    #expect(decoded.written == false)
    #expect(decoded.undoAvailable == true)
    #expect(decoded.notes.count == 1)
    #expect(decoded.notes[0].linkActions.count == 2)
    #expect(decoded.notes[0].frontmatterFixes.count == 1)
    #expect(decoded.ambiguous.count == 1)
    #expect(decoded.ambiguous[0].candidates == ["one/dup.md", "two/dup.md"])
}

@Test("FixUndoResult decodes exact Go JSON payload")
func decodeFixUndoResult() throws {
    let json = """
    {
      "reverted": true,
      "files_restored": 3,
      "schema_restored": true,
      "paths": ["a.md", "b.md", "c.md"]
    }
    """
    let decoded = try JSONDecoder().decode(FixUndoResult.self, from: Data(json.utf8))
    #expect(decoded.reverted == true)
    #expect(decoded.filesRestored == 3)
    #expect(decoded.schemaRestored == true)
    #expect(decoded.paths == ["a.md", "b.md", "c.md"])
}

@Test("AppState fixVault and undo CLI arg builders")
func appStateFixVaultArgBuilders() {
    #expect(AppState.fixVaultArgs(write: false) == ["fix", "--json", "--porcelain"])
    #expect(AppState.fixVaultArgs(write: true) == ["fix", "--write", "--json", "--porcelain"])
    #expect(AppState.undoFixVaultArgs(force: false) == ["fix", "--undo", "--json", "--porcelain"])
    #expect(AppState.undoFixVaultArgs(force: true) == ["fix", "--undo", "--force", "--json", "--porcelain"])
}

@Test("FixVaultPresentation gates Fix Vault and Undo buttons accurately")
func presentationEnablementAndTitles() {
    let empty = FixVaultReport(
        filesScanned: 10,
        notesChanged: 0,
        totalFixes: 0,
        schemaUpdated: false,
        schemaStatusesAdded: [],
        schemaIssuesResolved: 0,
        linksUpdated: 0,
        linksUnlinked: 0,
        formattingFixed: 0,
        written: false,
        undoAvailable: false,
        notes: [],
        ambiguous: []
    )
    #expect(!FixVaultPresentation.canFixVault(nil))
    #expect(!FixVaultPresentation.canFixVault(empty))
    #expect(!FixVaultPresentation.canUndo(empty))
    #expect(FixVaultPresentation.primaryButtonTitle(empty, isFixing: false) == "Fix Vault")
    #expect(FixVaultPresentation.primaryButtonTitle(empty, isFixing: true) == "Fixing Vault…")

    let actionable = FixVaultReport(
        filesScanned: 10,
        notesChanged: 2,
        totalFixes: 4,
        schemaUpdated: true,
        schemaStatusesAdded: ["active"],
        schemaIssuesResolved: 1,
        linksUpdated: 1,
        linksUnlinked: 1,
        formattingFixed: 1,
        written: false,
        undoAvailable: true,
        notes: [],
        ambiguous: [FixAmbiguousLink(path: "a.md", target: "dup", candidates: ["1/dup.md", "2/dup.md"])]
    )
    #expect(FixVaultPresentation.canFixVault(actionable))
    #expect(FixVaultPresentation.canUndo(actionable))
    #expect(FixVaultPresentation.primaryButtonTitle(actionable, isFixing: false) == "Fix Vault (4)")
    #expect(
        FixVaultPresentation.summaryLine(fix: actionable, lint: nil)
            == "1 link updated · 1 dead link unwrapped · 1 formatting fix · 1 schema update · 1 needs your choice"
    )
}

@Test("FixVaultPresentation banners and manual issue filtering")
func presentationBannersAndManualFilter() {
    let applied = FixVaultReport(
        filesScanned: 5,
        notesChanged: 1,
        totalFixes: 3,
        schemaUpdated: true,
        schemaStatusesAdded: ["active"],
        schemaIssuesResolved: 1,
        linksUpdated: 1,
        linksUnlinked: 1,
        formattingFixed: 0,
        written: true,
        undoAvailable: true,
        notes: [
            FixNoteChange(
                path: "Notes/broken.md",
                linkActions: [
                    FixLinkAction(action: "relink", from: "claude-code", to: "Claude Code", count: 1, reason: "obvious_match"),
                    FixLinkAction(action: "unlink", from: "dead", to: nil, count: 1, reason: "dead_link"),
                ],
                frontmatterFixes: [
                    FixFieldChange(field: "title", from: nil, to: "Broken", reason: "missing_required"),
                ]
            ),
        ],
        ambiguous: [
            FixAmbiguousLink(path: "Notes/broken.md", target: "dup", candidates: ["one/dup.md", "two/dup.md"]),
        ]
    )

    let banner = FixVaultPresentation.appliedBanner(applied)
    #expect(banner.tone == .success)
    #expect(banner.message == "Fixed 3 issues across 1 note and updated schema. Undo anytime.")

    let undoRes = FixUndoResult(reverted: true, filesRestored: 1, schemaRestored: true, paths: ["Notes/broken.md"])
    let uBanner = FixVaultPresentation.undoBanner(undoRes)
    #expect(uBanner.tone == .info)
    #expect(uBanner.message == "Undid last fix: restored 1 file and schema.")

    let lint = LintReport(
        issues: [
            LintIssue(path: "Notes/broken.md", line: 5, level: "warning", message: "broken wikilink: [[claude-code]]", target: "claude-code", fix: "missing", driftTarget: nil),
            LintIssue(path: "Notes/broken.md", line: 5, level: "warning", message: "broken wikilink: [[dead]]", target: "dead", fix: "missing", driftTarget: nil),
            LintIssue(path: "Notes/broken.md", line: 6, level: "warning", message: "broken wikilink: [[dup]]", target: "dup", fix: "ambiguous", driftTarget: nil),
            LintIssue(path: "Notes/broken.md", line: 1, level: "error", message: "field 'status' value 'active' not in [draft complete]", target: nil, fix: nil, driftTarget: nil),
            LintIssue(path: "Notes/broken.md", line: 1, level: "error", message: "missing required field 'title' for type 'note'", target: nil, fix: nil, driftTarget: nil),
        ],
        filesChecked: 1,
        errors: 2,
        warnings: 3
    )
    let remaining = FixVaultPresentation.manualIssues(lint: lint, fix: applied)
    #expect(remaining.count == 1)
    #expect(remaining[0].target == "dup")
}

@Test("FixVaultReport decodes all 8 M1 AI-repair fields and preserves legacy defaults")
func decodeM1AIFieldsAndLegacyFallback() throws {
    let fullJSON = """
    {
      "files_scanned": 12,
      "notes_changed": 2,
      "total_fixes": 3,
      "schema_updated": false,
      "schema_statuses_added": [],
      "schema_issues_resolved": 0,
      "links_updated": 1,
      "links_unlinked": 0,
      "formatting_fixed": 1,
      "written": true,
      "undo_available": true,
      "notes": [
        {
          "path": "notes/repaired.md",
          "link_actions": [
            { "action": "relink", "from": "old-ref", "to": "New Ref", "count": 1, "reason": "ai_suggest" },
            { "action": "relink", "from": "dup", "to": "areas/dup.md", "count": 1, "reason": "ai_disambiguate" }
          ],
          "frontmatter_fixes": [
            { "field": "frontmatter", "to": "repaired YAML", "reason": "parse_error" }
          ]
        }
      ],
      "ambiguous": [],
      "ai_fixable_count": 2,
      "ai_fixable_issues": [
        { "path": "notes/bad-yaml.md", "kind": "parse_error", "message": "parse error: yaml: line 2" },
        { "path": "notes/missing.md", "kind": "broken_link", "message": "broken wikilink: [[Lost Note]]", "target": "Lost Note" }
      ],
      "ai_applied": true,
      "ai_fixes_applied": 2,
      "ai_provider": "gemini",
      "ai_model": "gemini-4-hydrogen",
      "ai_skipped_reason": null,
      "warnings": ["1 link left as manual choice"]
    }
    """
    let full = try JSONDecoder().decode(FixVaultReport.self, from: Data(fullJSON.utf8))
    #expect(full.aiFixableCount == 2)
    #expect(full.aiFixableIssues.count == 2)
    #expect(full.aiFixableIssues[0].kind == "parse_error")
    #expect(full.aiFixableIssues[1].target == "Lost Note")
    #expect(full.aiApplied == true)
    #expect(full.aiFixesApplied == 2)
    #expect(full.aiProvider == "gemini")
    #expect(full.aiModel == "gemini-4-hydrogen")
    #expect(full.aiSkippedReason == nil)
    #expect(full.warnings == ["1 link left as manual choice"])

    // Legacy payload without M1 AI fields
    let legacyJSON = """
    { "files_scanned": 3, "total_fixes": 0 }
    """
    let legacy = try JSONDecoder().decode(FixVaultReport.self, from: Data(legacyJSON.utf8))
    #expect(legacy.aiFixableCount == 0)
    #expect(legacy.aiFixableIssues.isEmpty)
    #expect(legacy.aiApplied == false)
    #expect(legacy.aiFixesApplied == 0)
    #expect(legacy.aiProvider == nil)
    #expect(legacy.aiModel == nil)
    #expect(legacy.aiSkippedReason == nil)
    #expect(legacy.warnings.isEmpty)
}

@Test("FixVaultPresentation enables Fix Vault for AI-only issues, surfaces skips, and keeps ambiguous links manual")
func presentationAIFixableAndManualAmbiguous() {
    let aiOnly = FixVaultReport(
        filesScanned: 5,
        notesChanged: 0,
        totalFixes: 0,
        schemaUpdated: false,
        schemaStatusesAdded: [],
        schemaIssuesResolved: 0,
        linksUpdated: 0,
        linksUnlinked: 0,
        formattingFixed: 0,
        written: false,
        undoAvailable: false,
        notes: [],
        ambiguous: [
            FixAmbiguousLink(path: "notes/amb.md", target: "spec", candidates: ["a/spec.md", "b/spec.md"]),
        ],
        aiFixableCount: 3,
        aiFixableIssues: [
            AIFixableIssue(path: "notes/bad.md", kind: "parse_error", message: "parse error: bad yaml"),
            AIFixableIssue(path: "notes/enum.md", kind: "schema_enum", message: "field 'status' value 'open' not in [draft complete]"),
            AIFixableIssue(path: "notes/amb.md", kind: "broken_link", message: "ambiguous wikilink: [[spec]]", target: "spec"),
        ]
    )
    #expect(FixVaultPresentation.canFixVault(aiOnly))
    #expect(FixVaultPresentation.primaryButtonTitle(aiOnly, isFixing: false) == "Fix Vault (3)")
    #expect(FixVaultPresentation.summaryLine(fix: aiOnly, lint: nil) == "3 AI-fixable · 1 needs your choice")

    let lint = LintReport(
        issues: [
            LintIssue(path: "notes/bad.md", line: 1, level: "error", message: "parse error: bad yaml", target: nil, fix: nil, driftTarget: nil, kind: "parse_error"),
            LintIssue(path: "notes/enum.md", line: 1, level: "error", message: "field 'status' value 'open' not in [draft complete]", target: nil, fix: nil, driftTarget: nil, kind: "schema_enum"),
            LintIssue(path: "notes/amb.md", line: 5, level: "warning", message: "broken wikilink: [[spec]]", target: "spec", fix: "ambiguous", driftTarget: nil, kind: "broken_link"),
        ],
        filesChecked: 3,
        errors: 2,
        warnings: 1
    )
    let remaining = FixVaultPresentation.manualIssues(lint: lint, fix: aiOnly)
    #expect(remaining.count == 1)
    #expect(remaining.first?.path == "notes/amb.md")
    #expect(remaining.first?.target == "spec")

    // Skip/failure banner
    let skipped = FixVaultReport(
        filesScanned: 5,
        notesChanged: 0,
        totalFixes: 0,
        schemaUpdated: false,
        schemaStatusesAdded: [],
        schemaIssuesResolved: 0,
        linksUpdated: 0,
        linksUnlinked: 0,
        formattingFixed: 0,
        written: false,
        undoAvailable: false,
        notes: [],
        ambiguous: [],
        aiFixableCount: 1,
        aiSkippedReason: "gemini not ready"
    )
    let skipBanner = FixVaultPresentation.appliedBanner(skipped)
    #expect(skipBanner.tone == .error)
    #expect(skipBanner.message.contains("gemini not ready"))

    // Partial deterministic success + AI skip
    let partial = FixVaultReport(
        filesScanned: 5,
        notesChanged: 1,
        totalFixes: 2,
        schemaUpdated: false,
        schemaStatusesAdded: [],
        schemaIssuesResolved: 0,
        linksUpdated: 1,
        linksUnlinked: 0,
        formattingFixed: 1,
        written: true,
        undoAvailable: true,
        notes: [],
        ambiguous: [],
        aiSkippedReason: "gemini offline"
    )
    let partialBanner = FixVaultPresentation.appliedBanner(partial)
    #expect(partialBanner.tone == .info)
    #expect(partialBanner.message.contains("Fixed 2 issues across 1 note"))
    #expect(partialBanner.message.contains("gemini offline"))
}

@Test("AppState runLint, applyVaultFix, and undoVaultFix work end-to-end on a real tmp vault")
@MainActor
func appStateFixVaultIntegration() async throws {
    setenv("2NB_TEST", "1", 1)
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("FixVaultAppState-\(UUID().uuidString)", isDirectory: true)
    try VaultManager.initializeVault(at: root)
    defer { try? FileManager.default.removeItem(at: root) }

    try """
    ---
    title: Target
    type: note
    status: complete
    ---

    # Target
    """.write(to: root.appendingPathComponent("Target.md"), atomically: true, encoding: .utf8)

    let brokenURL = root.appendingPathComponent("broken.md")
    let originalBroken = """
    ---
    title: Broken
    type: note
    status: complete
    created: "2026-04-20T10:00:00Z"
    ---

    # Broken

    See [[target]].
    """
    try originalBroken.write(to: brokenURL, atomically: true, encoding: .utf8)

    let state = AppState()
    state.openVault(at: root)
    _ = try await state.runCLI(["index"], cwd: root)

    await state.runLint()
    #expect(state.lintError == nil)
    #expect(state.fixPreviewError == nil)
    #expect(FixVaultPresentation.canFixVault(state.fixReport))

    let applied = try await state.applyVaultFix()
    #expect(applied.written)
    #expect(applied.totalFixes > 0)

    let fixedText = try String(contentsOf: brokenURL, encoding: .utf8)
    #expect(fixedText.contains("[[Target]]"))
    #expect(!fixedText.contains("\"2026-04-20T10:00:00Z\""))

    let undone = try await state.undoVaultFix()
    #expect(undone.reverted)
    #expect(undone.filesRestored >= 1)
    let restoredText = try String(contentsOf: brokenURL, encoding: .utf8)
    #expect(restoredText == originalBroken)
}

@Test("FixVaultPresentation synthesizes manual issue for exact-basename ambiguous link omitted from lint.issues")
@MainActor
func exactBasenameAmbiguousLinkSynthesizedInManualIssues() async throws {
    setenv("2NB_TEST", "1", 1)
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("FixVaultAmbiguous-\(UUID().uuidString)", isDirectory: true)
    try VaultManager.initializeVault(at: root)
    defer { try? FileManager.default.removeItem(at: root) }

    let fm = FileManager.default
    try fm.createDirectory(at: root.appendingPathComponent("one"), withIntermediateDirectories: true)
    try fm.createDirectory(at: root.appendingPathComponent("two"), withIntermediateDirectories: true)
    try fm.createDirectory(at: root.appendingPathComponent("notes"), withIntermediateDirectories: true)

    try "---\ntitle: Dup One\ntype: note\nstatus: complete\n---\n\n# Dup One\n".write(
        to: root.appendingPathComponent("one/dup.md"),
        atomically: true,
        encoding: .utf8
    )
    try "---\ntitle: Dup Two\ntype: note\nstatus: complete\n---\n\n# Dup Two\n".write(
        to: root.appendingPathComponent("two/dup.md"),
        atomically: true,
        encoding: .utf8
    )
    try "---\ntitle: Ref\ntype: note\nstatus: complete\n---\n\n# Ref\n\nSee [[dup]].\n".write(
        to: root.appendingPathComponent("notes/ref.md"),
        atomically: true,
        encoding: .utf8
    )

    let state = AppState()
    state.openVault(at: root)
    _ = try await state.runCLI(["index"], cwd: root)
    await state.runLint()

    let lint = try #require(state.lintReport)
    let fix = try #require(state.fixReport)
    #expect(fix.ambiguous.contains(where: { $0.path == "notes/ref.md" && $0.target == "dup" }))

    let manual = FixVaultPresentation.manualIssues(lint: lint, fix: fix)
    #expect(manual.count == 1)
    let ambIssue = try #require(manual.first)
    #expect(ambIssue.path == "notes/ref.md")
    #expect(ambIssue.target == "dup")
    #expect(ambIssue.fix == "ambiguous")
    #expect(ambIssue.candidates == ["one/dup.md", "two/dup.md"])
    #expect(LintFinding.classify(issue: ambIssue) == .brokenLink(target: "dup"))
}

@Test("AppState.validateAppliedFixReport fails on !written with warnings and runLint surfaces corrupt schemas.yaml stderr")
@MainActor
func fixFailureWarningsAndCorruptSchemaStderr() async throws {
    let warningOnly = FixVaultReport(
        filesScanned: 2,
        notesChanged: 0,
        totalFixes: 0,
        schemaUpdated: false,
        schemaStatusesAdded: [],
        schemaIssuesResolved: 0,
        linksUpdated: 0,
        linksUnlinked: 0,
        formattingFixed: 0,
        written: false,
        undoAvailable: false,
        notes: [],
        ambiguous: [],
        aiFixableCount: 1,
        aiApplied: true,
        aiSkippedReason: nil,
        warnings: ["notes/bad.md: AI YAML repair failed: HTTP 402"]
    )
    var threwWarning = false
    do {
        try AppState.validateAppliedFixReport(warningOnly)
    } catch let CLIError.nonZeroExit(_, message) {
        threwWarning = message.contains("AI YAML repair failed: HTTP 402")
    }
    #expect(threwWarning)

    let cleanNoOp = FixVaultReport(
        filesScanned: 2,
        notesChanged: 0,
        totalFixes: 0,
        schemaUpdated: false,
        schemaStatusesAdded: [],
        schemaIssuesResolved: 0,
        linksUpdated: 0,
        linksUnlinked: 0,
        formattingFixed: 0,
        written: false,
        undoAvailable: false,
        notes: [],
        ambiguous: []
    )
    try AppState.validateAppliedFixReport(cleanNoOp)

    // Real CLI runLint against corrupt .2ndbrain/schemas.yaml surfaces CLI stderr.
    setenv("2NB_TEST", "1", 1)
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("CorruptSchemaLint-\(UUID().uuidString)", isDirectory: true)
    try VaultManager.initializeVault(at: root)
    defer { try? FileManager.default.removeItem(at: root) }

    try "types:\n  note:\n    fields: [unclosed\n".write(
        to: root.appendingPathComponent(".2ndbrain/schemas.yaml"),
        atomically: true,
        encoding: .utf8
    )

    let state = AppState()
    state.openVault(at: root)
    await state.runLint()

    #expect(state.lintReport == nil)
    let errMsg = try #require(state.lintError)
    #expect(!errMsg.contains("couldn’t be read because it isn’t in the correct format"))
    #expect(errMsg.localizedCaseInsensitiveContains("schema") || errMsg.localizedCaseInsensitiveContains("yaml"))
}

@Test("AppState.runLint bounds and redacts huge CLI stderr with secrets in lintError, fixPreviewError, and secondbrain.log")
@MainActor
func runLintBoundsAndRedactsHugeStderrAndSecrets() async throws {
    setenv("2NB_TEST", "1", 1)
    let fm = FileManager.default
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("HugeStderrLint-\(UUID().uuidString)", isDirectory: true)
    try VaultManager.initializeVault(at: root)
    defer { try? fm.removeItem(at: root) }

    // Write a real executable wrapper script in /tmp that emits 270KB + trailing secrets on stderr
    // and exits non-zero for `lint` and `fix`.
    let wrapperURL = root.appendingPathComponent("fake-2nb-stderr.sh")
    let script = """
    #!/bin/sh
    python3 -c 'import sys; sys.stderr.write("ERR_LINE_" * 30000 + " api_key=super_secret_lint_123 my_api_key=\\"sk-or-v1-abcdef0123456789\\" myToken=tok_lint_456 custom_secret=sec_lint_789 Authorization: Bearer bearer_lint_999 AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE key=\\"unterminated_secret_xyz\\n")'
    exit 1
    """
    try script.write(to: wrapperURL, atomically: true, encoding: .utf8)
    try fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: wrapperURL.path)

    let state = AppState()
    state.cliExecutablePathOverride = wrapperURL.path
    state.openVault(at: root)

    let clock = ContinuousClock()
    let start = clock.now
    await state.runLint()
    let elapsed = clock.now - start

    #expect(elapsed < .seconds(3), "AppState.runLint() took \(elapsed) on 270KB stderr")
    #expect(state.lintReport == nil)
    #expect(state.fixReport == nil)

    let lintErr = try #require(state.lintError)
    let fixErr = try #require(state.fixPreviewError)
    let forbiddenSecrets = [
        "super_secret_lint_123",
        "sk-or-v1-abcdef0123456789",
        "tok_lint_456",
        "sec_lint_789",
        "bearer_lint_999",
        "AKIAIOSFODNN7EXAMPLE",
        "unterminated_secret_xyz",
    ]

    for (label, value) in [("lintError", lintErr), ("fixPreviewError", fixErr)] {
        #expect(value.count <= GUIActionTracker.maxStatusErrorChars, "\(label) exceeded cap: \(value.count)")
        #expect(value.hasPrefix("…"), "\(label) missing truncation ellipsis")
        #expect(value.contains("<REDACTED>"), "\(label) did not redact trailing secrets")
        for secret in forbiddenSecrets {
            #expect(!value.contains(secret), "\(label) leaked secret \(secret)")
        }
    }

    // Also verify ErrorLogger's secondbrain.log is bounded & redacted (poll briefly for queue.async).
    let logFile = root.appendingPathComponent(".2ndbrain/logs/secondbrain.log")
    var logText = ""
    for _ in 0..<40 {
        if let text = try? String(contentsOf: logFile, encoding: .utf8),
           text.contains("Lint validation failed") {
            logText = text
            break
        }
        try await Task.sleep(for: .milliseconds(25))
    }
    if logText.isEmpty, let text = try? String(contentsOf: logFile, encoding: .utf8) {
        logText = text
    }
    #expect(!logText.isEmpty, "secondbrain.log should have recorded failure entries")
    #expect(logText.count < 15_000, "secondbrain.log should be bounded, got \(logText.count) chars")
    #expect(logText.contains("<REDACTED>"))
    for secret in forbiddenSecrets {
        #expect(!logText.contains(secret), "secondbrain.log leaked secret \(secret)")
    }
}
