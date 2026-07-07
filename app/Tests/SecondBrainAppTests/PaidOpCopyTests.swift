import Foundation
import Testing
@testable import SecondBrain

private func decodePreview(_ json: String) -> CostPreviewResponse {
    try! JSONDecoder().decode(CostPreviewResponse.self, from: Data(json.utf8))
}

@Test("PaidOpCopy.message shows the estimate for known pricing")
func paidOpCopyKnownPricing() {
    let preview = decodePreview(#"{"estimates":[{"model_id":"m","provider":"bedrock","requests":1,"input_tokens":20,"output_tokens":32,"probe":"test","usd":0.00018,"known_pricing":true}],"total_usd":0.00018}"#)
    let text = PaidOpCopy.message(preview, operation: "Test m")
    #expect(text.contains("$0.000180"))
    #expect(text.contains("real API calls"))
    #expect(!text.contains("unknown"))
}

@Test("PaidOpCopy.message leads with the unknown when nothing is priced")
func paidOpCopyAllUnknownPricing() {
    let preview = decodePreview(#"{"estimates":[{"model_id":"m","provider":"bedrock","requests":1,"input_tokens":20,"output_tokens":32,"probe":"test","usd":0,"known_pricing":false}],"total_usd":0}"#)
    let text = PaidOpCopy.message(preview, operation: "Test m")
    #expect(text.contains("Pricing is unknown"))
    // No misleading zero-dollar figure next to "unknown".
    #expect(!text.contains("$0.000000"))
}

@Test("PaidOpCopy.message flags a partial unknown alongside the estimate")
func paidOpCopyPartialUnknownPricing() {
    let preview = decodePreview(#"{"estimates":[{"model_id":"m","provider":"bedrock","requests":1,"input_tokens":20,"output_tokens":32,"probe":"test","usd":0.0002,"known_pricing":true},{"model_id":"n","provider":"bedrock","requests":1,"input_tokens":20,"output_tokens":32,"probe":"test","usd":0,"known_pricing":false}],"total_usd":0.0002}"#)
    let text = PaidOpCopy.message(preview, operation: "Test batch")
    #expect(text.contains("$0.000200"))
    #expect(text.contains("unknown for 1 model"))
}

@Test("PaidOpCopy.message degrades when the estimator is unavailable")
func paidOpCopyNoPreview() {
    let text = PaidOpCopy.message(nil, operation: "Benchmark m")
    #expect(text.contains("estimate isn't available"))
    #expect(text.contains("real API calls"))
}
