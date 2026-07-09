import Foundation
import Testing
@testable import SecondBrain

@Test("access_denied maps to the staged-rollout guidance with a console link on bedrock")
func accessDeniedGuidance() {
    let g = ModelAccessPresentation.guidance(code: "access_denied", provider: "bedrock", region: "us-east-1")
    #expect(g?.badge == "no access")
    #expect(g?.advice.contains("staged rollout") == true)
    #expect(g?.actionLabel == "Open AWS console")
    #expect(g?.actionURL?.absoluteString == "https://us-east-1.console.aws.amazon.com/bedrock/home?region=us-east-1#/modelaccess")

    // Non-bedrock access denial gets no console link.
    let or = ModelAccessPresentation.guidance(code: "access_denied", provider: "openrouter")
    #expect(or?.actionURL == nil)
}

@Test("a mantle model's access_denied suppresses the console link and points at AWS Sales")
func mantleAccessDeniedSuppressesConsoleLink() {
    let g = ModelAccessPresentation.guidance(code: "access_denied", provider: "bedrock", region: "us-west-2", strategy: "bedrock_mantle_responses")
    #expect(g?.badge == "no access")
    // The Bedrock console Model-access page can't unblock a mantle model, so no button.
    #expect(g?.actionLabel == nil)
    #expect(g?.actionURL == nil)
    // Fallback advice is account-aware, not the classic console text.
    #expect(g?.advice.contains("AWS Sales") == true)
    #expect(g?.advice.contains("mantle") == true)

    // The CLI's own remediation still wins as advice, and the link stays suppressed.
    let withRem = ModelAccessPresentation.guidance(code: "access_denied", provider: "bedrock", remediation: "Contact AWS Sales.", region: "us-west-2", strategy: "bedrock_mantle_responses")
    #expect(withRem?.advice == "Contact AWS Sales.")
    #expect(withRem?.actionURL == nil)

    // A classic bedrock model (no mantle strategy) still gets the console link.
    let classic = ModelAccessPresentation.guidance(code: "access_denied", provider: "bedrock", region: "us-east-1", strategy: "bedrock_converse")
    #expect(classic?.actionLabel == "Open AWS console")
    #expect(classic?.actionURL != nil)
}

@Test("the CLI's own remediation text wins over the local fallback")
func remediationWins() {
    let g = ModelAccessPresentation.guidance(code: "bad_credentials", provider: "bedrock", remediation: "Refresh your SSO session.")
    #expect(g?.advice == "Refresh your SSO session.")
}

@Test("every classified code maps; unknown and nil do not")
func codeMapping() {
    for code in ["access_denied", "bad_credentials", "throttled", "not_found", "provider_unreachable", "timeout", "incompatible", "invalid_request"] {
        #expect(ModelAccessPresentation.guidance(code: code, provider: "bedrock") != nil, "\(code) should map")
    }
    #expect(ModelAccessPresentation.guidance(code: nil, provider: "bedrock") == nil)
    #expect(ModelAccessPresentation.guidance(code: "", provider: "bedrock") == nil)
    #expect(ModelAccessPresentation.guidance(code: "unknown", provider: "bedrock") == nil)
    // A future code the app doesn't know renders generically, never crashes.
    let future = ModelAccessPresentation.guidance(code: "quota_exhausted", provider: "bedrock")
    #expect(future?.badge == "quota exhausted")
}

@Test("badgeLabel classifies failures and stays quiet otherwise")
func badgeLabels() {
    #expect(ModelAccessPresentation.badgeLabel(testError: "403", testErrorCode: "access_denied", provider: "bedrock") == "no access")
    #expect(ModelAccessPresentation.badgeLabel(testError: "boom", testErrorCode: nil, provider: "bedrock") == "failed")
    #expect(ModelAccessPresentation.badgeLabel(testError: nil, testErrorCode: nil, provider: "bedrock") == nil)
    #expect(ModelAccessPresentation.badgeLabel(testError: "", testErrorCode: "access_denied", provider: "bedrock") == nil)
}

@Test("console URL degrades without a region and refuses non-region tokens")
func consoleURLFallback() {
    #expect(ModelAccessPresentation.bedrockConsoleURL(region: nil)?.absoluteString == "https://console.aws.amazon.com/bedrock/home#/modelaccess")
    // The region interpolates into the HOST; a hostile config value must
    // fall back rather than redirect off-domain.
    for hostile in ["evil.com/?", "us-east-1.evil.com", "a b", "US-EAST-1"] {
        #expect(ModelAccessPresentation.bedrockConsoleURL(region: hostile)?.absoluteString == "https://console.aws.amazon.com/bedrock/home#/modelaccess", "\(hostile) must fall back")
    }
    #expect(ModelAccessPresentation.bedrockConsoleURL(region: "eu-west-2")?.absoluteString == "https://eu-west-2.console.aws.amazon.com/bedrock/home?region=eu-west-2#/modelaccess")
}

@Test("AIProbeResult decodes code and remediation from models test JSON")
func probeResultDecodesTaxonomy() {
    let json = #"{"model_id":"openai.gpt-5.5","provider":"bedrock","type":"generation","ok":false,"detail":"mantle: 401","latency":"512ms","code":"access_denied","remediation":"Contact AWS Sales.","invoke_strategy":"bedrock_mantle_responses"}"#
    let r = try! JSONDecoder().decode(AIProbeResult.self, from: Data(json.utf8))
    #expect(r.errorCode == "access_denied")
    #expect(r.remediation?.contains("AWS Sales") == true)
    #expect(r.invokeStrategy == "bedrock_mantle_responses")
    // Pre-taxonomy CLI: fields absent, decode still succeeds.
    let legacy = try! JSONDecoder().decode(AIProbeResult.self, from: Data(#"{"model_id":"m","provider":"bedrock","type":"generation","ok":true,"latency":"100ms"}"#.utf8))
    #expect(legacy.errorCode == nil)
    #expect(legacy.invokeStrategy == nil)
}
