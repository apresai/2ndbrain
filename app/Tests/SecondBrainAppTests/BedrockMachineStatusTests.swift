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

@Test("BedrockMachineStatus decodes the additive region/staleness/divergence fields, absent on older CLIs")
func bedrockMachineStatusAdditiveFields() throws {
    let new = """
    {"path":"/tmp/bedrock.json","region":"us-east-1","token_set":true,"token_source":"file","regions":["us-west-2","us-east-2"],"token_updated_at":"2026-08-20T18:00:00Z","env_overrides_stored":true,"stored_token_suffix":"RT0="}
    """
    let got = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data(new.utf8))
    #expect(got.regions == ["us-west-2", "us-east-2"])
    #expect(got.tokenUpdatedAt == "2026-08-20T18:00:00Z")
    #expect(got.envOverridesStored)
    #expect(got.storedTokenSuffix == "RT0=")

    // A pre-region CLI payload defaults every new field.
    let old = """
    {"path":"/tmp/bedrock.json","token_set":true,"token_source":"file"}
    """
    let legacy = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data(old.utf8))
    #expect(legacy.regions.isEmpty)
    #expect(legacy.tokenUpdatedAt == "")
    #expect(!legacy.envOverridesStored)
    #expect(legacy.storedTokenSuffix == "")
}

@Test("bedrockSetArgs builds the regions/clear-regions argv correctly")
func bedrockSetArgsBuilder() {
    #expect(AppState.bedrockSetArgs(region: "", hasToken: true, verifyRegions: nil)
        == ["config", "bedrock", "--set", "--json", "--porcelain", "--token-stdin"])
    #expect(AppState.bedrockSetArgs(region: "us-east-1", hasToken: false, verifyRegions: nil)
        == ["config", "bedrock", "--set", "--json", "--porcelain", "--region", "us-east-1"])
    #expect(AppState.bedrockSetArgs(region: "", hasToken: false, verifyRegions: ["us-west-2", "us-east-2"])
        == ["config", "bedrock", "--set", "--json", "--porcelain", "--regions", "us-west-2,us-east-2"])
    // Empty array means CLEAR, never an empty --regions value.
    #expect(AppState.bedrockSetArgs(region: "", hasToken: false, verifyRegions: [])
        == ["config", "bedrock", "--set", "--json", "--porcelain", "--clear-regions"])
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
