import Testing
import Foundation
import SecondBrainCore

// These tests exercise the real `2nb` binary resolved by CLIPath. When the
// binary isn't installed at one of the known locations, the tests skip
// instead of failing — matching the no-mock policy from CLAUDE.md and the
// existing pattern in bedrock/openrouter Go tests.

// Set 2NB_TEST exactly once before any test shells out to `2nb`. The
// `vault create` subprocess in VaultManager.initializeVault inherits this and
// skips writing the real ~/.2ndbrain-active-vault / ~/.2ndbrain-vaults —
// otherwise running this target's tests clobbers the developer's active vault.
// A run-once global is thread-safe; a per-call setenv would race under
// swift-testing's parallel execution. (`make test-swift` also exports 2NB_TEST
// for the whole process as the primary, cross-target guard.)
private let isolate2nbHomeWrites: Void = {
    setenv("2NB_TEST", "1", 1)
}()

private func requireCLI() throws {
    _ = isolate2nbHomeWrites // force the one-time setenv before any subprocess
    let path = CLIPath.resolve()
    guard FileManager.default.isExecutableFile(atPath: path) else {
        throw TestSkip.cliMissing(path)
    }
}

private enum TestSkip: Error {
    case cliMissing(String)
}

@Test("initializeVault shells out to 2nb and produces a CLI-canonical vault")
func initializeVaultHappyPath() throws {
    try requireCLI()

    let tmp = FileManager.default.temporaryDirectory
        .appendingPathComponent("sb-vault-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: tmp) }

    try VaultManager.initializeVault(at: tmp)

    // The CLI owns the canonical layout — config.yaml, schemas.yaml, and
    // index.db all need to land. Missing schemas.yaml was the symptom that
    // originally motivated the shell-out rewrite.
    let configPath = tmp.appendingPathComponent(".2ndbrain/config.yaml").path
    let schemasPath = tmp.appendingPathComponent(".2ndbrain/schemas.yaml").path
    let dbPath = tmp.appendingPathComponent(".2ndbrain/index.db").path

    #expect(FileManager.default.fileExists(atPath: configPath))
    #expect(FileManager.default.fileExists(atPath: schemasPath))
    #expect(FileManager.default.fileExists(atPath: dbPath))

    // Regression: schemas.yaml must include prd and prfaq types (the Swift
    // hand-rolled YAML was missing these before the shell-out).
    if let body = try? String(contentsOfFile: schemasPath, encoding: .utf8) {
        #expect(body.contains("prd"))
        #expect(body.contains("prfaq"))
    }

    // Regression: config.yaml must carry an ai: block (omitted by the
    // old Swift writer).
    if let body = try? String(contentsOfFile: configPath, encoding: .utf8) {
        #expect(body.contains("ai:"))
    }
}

@Test("initializeVault throws alreadyInitialized on repeat call")
func initializeVaultRejectsExistingVault() throws {
    try requireCLI()

    let tmp = FileManager.default.temporaryDirectory
        .appendingPathComponent("sb-vault-dup-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: tmp) }

    try VaultManager.initializeVault(at: tmp)

    // Second call must fail fast with the typed error — the local
    // existence guard short-circuits before shell-out so the user sees a
    // clean message instead of a CLI "already initialized" stderr blob.
    #expect(throws: VaultError.self) {
        try VaultManager.initializeVault(at: tmp)
    }
}

@Test("VaultManager tracks its own initialization state")
func isInitializedReflectsDiskState() throws {
    try requireCLI()

    let tmp = FileManager.default.temporaryDirectory
        .appendingPathComponent("sb-vault-state-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: tmp) }

    let vm = VaultManager(rootURL: tmp)
    #expect(vm.isInitialized == false)

    try VaultManager.initializeVault(at: tmp)
    #expect(vm.isInitialized == true)
}
