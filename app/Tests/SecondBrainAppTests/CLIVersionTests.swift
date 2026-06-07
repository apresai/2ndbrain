import Foundation
import Testing
@testable import SecondBrain

@Test("CLIVersion.parse extracts a version triple from the common forms")
func cliVersionParse() {
    #expect(CLIVersion.parse("2nb version 0.5.8").map { "\($0.0).\($0.1).\($0.2)" } == "0.5.8")
    #expect(CLIVersion.parse("0.5.8").map { "\($0.0).\($0.1).\($0.2)" } == "0.5.8")
    #expect(CLIVersion.parse("v0.5.8").map { "\($0.0).\($0.1).\($0.2)" } == "0.5.8")
    #expect(CLIVersion.parse("2nb version 1.10.2 (abc123)").map { "\($0.0).\($0.1).\($0.2)" } == "1.10.2")
    // No parseable triple → nil.
    #expect(CLIVersion.parse("not a version") == nil)
    #expect(CLIVersion.parse("0.5") == nil)
    #expect(CLIVersion.parse(nil) == nil)
}

@Test("CLIVersion.isOlder warns only when the CLI is strictly behind the app")
func cliVersionIsOlder() {
    // The bug that motivated this: app 0.5.8 against CLI 0.5.4.
    #expect(CLIVersion.isOlder(cli: "0.5.4", thanApp: "0.5.8"))
    #expect(CLIVersion.isOlder(cli: "2nb version 0.5.4", thanApp: "0.5.8"))

    // Matched or newer CLI → no warning.
    #expect(!CLIVersion.isOlder(cli: "0.5.8", thanApp: "0.5.8"))
    #expect(!CLIVersion.isOlder(cli: "0.6.0", thanApp: "0.5.8"))
    #expect(!CLIVersion.isOlder(cli: "1.0.0", thanApp: "0.5.8"))

    // Ordering must respect each component, not lexical compare (0.5.10 > 0.5.8).
    #expect(!CLIVersion.isOlder(cli: "0.5.10", thanApp: "0.5.8"))
    #expect(CLIVersion.isOlder(cli: "0.4.99", thanApp: "0.5.0"))
    // Older across each component independently (major / minor / build).
    #expect(CLIVersion.isOlder(cli: "0.9.9", thanApp: "1.0.0"))   // major behind
    #expect(CLIVersion.isOlder(cli: "0.5.9", thanApp: "0.6.0"))   // minor behind
    #expect(CLIVersion.isOlder(cli: "0.5.7", thanApp: "0.5.8"))   // build behind

    // Unparseable on either side → never cry wolf.
    #expect(!CLIVersion.isOlder(cli: nil, thanApp: "0.5.8"))
    #expect(!CLIVersion.isOlder(cli: "garbage", thanApp: "0.5.8"))
    #expect(!CLIVersion.isOlder(cli: "0.5.4", thanApp: "unknown"))
}
