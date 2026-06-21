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
