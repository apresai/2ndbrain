import Testing
import Foundation
import SecondBrainCore

@Test("args prepends --vault to every command")
func argsPrependsVault() {
    let vault = URL(fileURLWithPath: "/tmp/sb-test-vault")
    let result = CLIPath.args(["search", "query"], vault: vault)
    #expect(result == ["--vault", "/tmp/sb-test-vault", "search", "query"])
}

@Test("args preserves arg order after the --vault pair")
func argsPreservesOrder() {
    let vault = URL(fileURLWithPath: "/v")
    let result = CLIPath.args(["a", "b", "c", "--json"], vault: vault)
    #expect(result == ["--vault", "/v", "a", "b", "c", "--json"])
}

@Test("args handles empty argument list")
func argsHandlesEmpty() {
    let vault = URL(fileURLWithPath: "/v")
    let result = CLIPath.args([], vault: vault)
    #expect(result == ["--vault", "/v"])
}

@Test("resolve returns an existing path or the fallback")
func resolveReturnsKnownCandidate() {
    // resolve() prefers the bundled binary, then iterates the known Homebrew
    // paths. The test runner is not the SecondBrain.app bundle, so bundledPath
    // is nil and the returned string must be one of the Homebrew candidates
    // regardless of whether 2nb is installed.
    let got = CLIPath.resolve()
    let validCandidates = [
        "/opt/homebrew/bin/2nb",
        "/usr/local/bin/2nb",
    ]
    #expect(validCandidates.contains(got))
}

@Test("bundledPath is nil outside an app bundle, so resolve() falls back to PATH")
func bundledPathNilInTests() {
    // The xctest runner has no Contents/Resources/2nb, so bundledPath is nil.
    // This guards the precedence in resolve(): a non-bundled run (dev/tests)
    // must never claim a bundled binary and must fall through to Homebrew.
    #expect(CLIPath.bundledPath == nil)
    #expect(CLIPath.resolve() != Bundle.main.bundlePath + "/Contents/Resources/2nb")
}

@Test("augmentedPATH prepends the Homebrew/Go bin dirs that launchd omits")
func augmentedPATHPrependsMissing() {
    let got = CLIPath.augmentedPATH(current: "/usr/bin:/bin", home: "/Users/x")
    #expect(got == "/opt/homebrew/bin:/usr/local/bin:/Users/x/go/bin:/usr/bin:/bin")
}

@Test("augmentedPATH never duplicates a dir already on PATH, keeping its place")
func augmentedPATHDedupes() {
    let got = CLIPath.augmentedPATH(current: "/opt/homebrew/bin:/usr/bin", home: "/Users/x")
    // /opt/homebrew/bin is already present, so it keeps its position and only
    // the genuinely-missing dirs are prepended.
    #expect(got == "/usr/local/bin:/Users/x/go/bin:/opt/homebrew/bin:/usr/bin")
}

@Test("augmentedPATH builds a PATH from scratch when the inherited one is empty")
func augmentedPATHEmpty() {
    let got = CLIPath.augmentedPATH(current: "", home: "/Users/x")
    #expect(got == "/opt/homebrew/bin:/usr/local/bin:/Users/x/go/bin")
}

/// True if `path` carries a `com.apple.quarantine` xattr (probe via getxattr's
/// length query, which returns >= 0 when present and -1 when absent).
private func hasQuarantine(_ path: String) -> Bool {
    path.withCString { cPath in
        getxattr(cPath, "com.apple.quarantine", nil, 0, 0, 0) >= 0
    }
}

@Test("clearQuarantine removes the com.apple.quarantine xattr from a file")
func clearQuarantineRemovesXattr() throws {
    let tmp = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-quarantine-\(UUID().uuidString)")
    try Data("x".utf8).write(to: tmp)
    defer { try? FileManager.default.removeItem(at: tmp) }

    // Apply a realistic quarantine xattr, then confirm it's there.
    let value = "0081;00000000;Test;\(UUID().uuidString)"
    tmp.path.withCString { cPath in
        value.withCString { cVal in
            _ = setxattr(cPath, "com.apple.quarantine", cVal, strlen(cVal), 0, 0)
        }
    }
    #expect(hasQuarantine(tmp.path) == true)

    // Stripping it succeeds and the xattr is gone — this is what the app does to
    // its bundled 2nb at launch so Gatekeeper can't block the quarantined helper.
    #expect(CLIPath.clearQuarantine(at: tmp.path) == true)
    #expect(hasQuarantine(tmp.path) == false)
}

@Test("clearQuarantine is a successful no-op when the xattr is absent")
func clearQuarantineNoXattrIsSuccess() throws {
    let tmp = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-noquarantine-\(UUID().uuidString)")
    try Data("x".utf8).write(to: tmp)
    defer { try? FileManager.default.removeItem(at: tmp) }

    // No quarantine to begin with: removexattr returns ENOATTR, which we treat
    // as success (the goal — "not quarantined" — already holds).
    #expect(hasQuarantine(tmp.path) == false)
    #expect(CLIPath.clearQuarantine(at: tmp.path) == true)
}
