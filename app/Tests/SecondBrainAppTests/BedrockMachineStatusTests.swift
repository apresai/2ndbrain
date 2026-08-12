import Foundation
import Testing
@testable import SecondBrain

@Test("BedrockMachineStatus decodes the redacted CLI JSON and never requires a token field")
func bedrockMachineStatusDecode() throws {
    let full = """
    {"path":"/Users/x/.config/2nb/bedrock.json","region":"us-west-2","token_set":true,"token_source":"file"}
    """
    let got = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data(full.utf8))
    #expect(got.path.hasSuffix("bedrock.json"))
    #expect(got.region == "us-west-2")
    #expect(got.tokenSet)
    #expect(got.tokenSource == "file")

    let empty = """
    {"path":"/tmp/bedrock.json","token_set":false,"token_source":"none"}
    """
    let none = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data(empty.utf8))
    #expect(none.region == "")
    #expect(!none.tokenSet)
    #expect(none.tokenSource == "none")
    #expect(none.error == nil)

    let refused = """
    {"path":"/tmp/bedrock.json","token_set":false,"token_source":"none","error":"refusing to read /tmp/bedrock.json: mode 0644 is not private (want 0600)"}
    """
    let bad = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data(refused.utf8))
    #expect(bad.error?.contains("not private") == true)
}

@Test("ProviderStatusInfo decodes additive token_source")
func providerStatusTokenSourceDecode() throws {
    let json = """
    {"name":"bedrock","config_present":true,"disabled":false,"reachable":true,"detail":"us-east-1 token:file","token_source":"file"}
    """
    let got = try JSONDecoder().decode(ProviderStatusInfo.self, from: Data(json.utf8))
    #expect(got.tokenSource == "file")
    #expect(got.detail == "us-east-1 token:file")

    let legacy = """
    {"name":"bedrock","config_present":true,"disabled":false,"reachable":true,"detail":"us-east-1"}
    """
    let old = try JSONDecoder().decode(ProviderStatusInfo.self, from: Data(legacy.utf8))
    #expect(old.tokenSource == nil)
}
