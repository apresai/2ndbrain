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
    // resolve() iterates the known binary paths; the returned string must
    // be one of the three candidates regardless of whether 2nb is installed.
    let got = CLIPath.resolve()
    let validCandidates = [
        "/opt/homebrew/bin/2nb",
        "/usr/local/bin/2nb",
    ]
    #expect(validCandidates.contains(got))
}
