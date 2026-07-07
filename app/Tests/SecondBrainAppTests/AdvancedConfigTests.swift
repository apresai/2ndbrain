import Foundation
import Testing
@testable import SecondBrain

@Test("thresholdWriteValue maps the override checkbox to the CLI contract")
func thresholdWriteValueMapping() {
    // Unchecked writes "0" = automatic (resolution chain), regardless of text.
    #expect(AdvancedConfig.thresholdWriteValue(overrideOn: false, text: "0.35") == "0")
    #expect(AdvancedConfig.thresholdWriteValue(overrideOn: false, text: "") == "0")
    // Checked passes a valid 0..1 value through verbatim.
    #expect(AdvancedConfig.thresholdWriteValue(overrideOn: true, text: "0.25") == "0.25")
    // Checked with junk, zero, or out-of-range is rejected (nil), so an
    // accidental near-zero override can't be written silently.
    #expect(AdvancedConfig.thresholdWriteValue(overrideOn: true, text: "abc") == nil)
    #expect(AdvancedConfig.thresholdWriteValue(overrideOn: true, text: "0") == nil)
    #expect(AdvancedConfig.thresholdWriteValue(overrideOn: true, text: "1.5") == nil)
}

@Test("flatten renders config show JSON as sorted dotted key paths")
func flattenConfigShow() {
    let json = #"{"vault_name":"v","config":{"ai":{"provider":"bedrock","dimensions":1024,"rerank":{"enabled":false},"bm25_weight":1.5},"version":"1"},"tags":["a","b"],"none":null}"#
    let rows = AdvancedConfig.flatten(Data(json.utf8))
    let dict = Dictionary(uniqueKeysWithValues: rows.map { ($0.key, $0.value) })
    #expect(dict["config.ai.provider"] == "bedrock")
    #expect(dict["config.ai.dimensions"] == "1024")
    #expect(dict["config.ai.rerank.enabled"] == "0")   // NSNumber bool renders 0/1
    #expect(dict["config.ai.bm25_weight"] == "1.5")
    #expect(dict["tags[0]"] == "a")
    #expect(dict["none"] == "null")
    // Sorted for a stable read.
    #expect(rows.map(\.key) == rows.map(\.key).sorted())
}

@Test("flatten tolerates non-JSON input")
func flattenGarbage() {
    #expect(AdvancedConfig.flatten(Data("not json".utf8)).isEmpty)
}

@Test("CalibrationInfo decodes the models calibrate payload")
func calibrationDecode() {
    let json = #"{"provider":"bedrock","model":"amazon.nova-2-multimodal-embeddings-v1:0","dimensions":1024,"doc_count":150,"sample_count":500,"min":0.01,"p50":0.12,"p90":0.2,"p95":0.22,"p99":0.3,"max":0.4,"recommended_threshold":0.23,"active_threshold":0.25,"active_source":"model_recommendation","saved_to":"vault"}"#
    let info = try! JSONDecoder().decode(CalibrationInfo.self, from: Data(json.utf8))
    #expect(info.recommendedThreshold == 0.23)
    #expect(info.p95 == 0.22)
    #expect(info.savedTo == "vault")
}

@Test("calibrationMessage claims a save only when the CLI actually saved")
func calibrationMessageHonesty() {
    let saved = try! JSONDecoder().decode(CalibrationInfo.self, from: Data(#"{"provider":"ollama","model":"nomic-embed-text","recommended_threshold":0.43,"p95":0.42,"saved_to":"vault"}"#.utf8))
    #expect(AdvancedConfig.calibrationMessage(saved).contains("saved to the vault catalog"))

    // Asymmetric refusal: exit 0, saved_to omitted, --porcelain suppresses
    // the CLI warning. The message must NOT claim a save.
    let refused = try! JSONDecoder().decode(CalibrationInfo.self, from: Data(#"{"provider":"bedrock","model":"amazon.nova-2-multimodal-embeddings-v1:0","recommended_threshold":0.23,"p95":0.22}"#.utf8))
    let text = AdvancedConfig.calibrationMessage(refused)
    #expect(text.contains("did NOT save"))
    #expect(!text.contains("saved to"))
}

@Test("EmbedProbeInfo decodes the ai embed-probe payload")
func embedProbeDecode() {
    let json = #"{"provider":"bedrock","model":"amazon.nova-2-multimodal-embeddings-v1:0","sample_size":64,"levels":[{"concurrency":4,"duration_ms":9000,"texts_per_sec":7.1,"errors":0},{"concurrency":8,"duration_ms":6000,"texts_per_sec":10.6,"errors":2}],"recommended":4}"#
    let info = try! JSONDecoder().decode(EmbedProbeInfo.self, from: Data(json.utf8))
    #expect(info.recommended == 4)
    #expect(info.levels.count == 2)
    #expect(info.levels[1].errors == 2)
}
