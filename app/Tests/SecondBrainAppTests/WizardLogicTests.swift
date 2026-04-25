import Testing
import Foundation

// Tests for AI setup wizard logic — easy mode defaults and validation rules.
// These match the Go CLI defaults in ai_setup.go easyModeDefaults().

struct EasyModeDefaults {
    let embedModel: String
    let genModel: String
    let dims: Int
}

func easyModeDefaults(for provider: String) -> EasyModeDefaults? {
    switch provider {
    case "bedrock":
        return EasyModeDefaults(
            embedModel: "amazon.nova-2-multimodal-embeddings-v1:0",
            genModel: "amazon.nova-micro-v1:0",
            dims: 1024
        )
    case "openrouter":
        return EasyModeDefaults(
            embedModel: "nvidia/llama-nemotron-embed-vl-1b-v2:free",
            genModel: "google/gemma-4-31b-it:free",
            dims: 1024
        )
    case "ollama":
        return EasyModeDefaults(
            embedModel: "nomic-embed-text",
            genModel: "qwen2.5:0.5b",
            dims: 768
        )
    default:
        return nil
    }
}

// MARK: - Easy Mode Defaults (must match Go CLI)

@Test(arguments: ["bedrock", "openrouter", "ollama"])
func easyModeDefaultsExist(provider: String) {
    let defaults = easyModeDefaults(for: provider)
    #expect(defaults != nil, "Easy mode defaults missing for \(provider)")
    #expect(!defaults!.embedModel.isEmpty)
    #expect(!defaults!.genModel.isEmpty)
    #expect(defaults!.dims > 0)
}

@Test("Bedrock easy mode matches Go CLI defaults")
func bedrockDefaults() {
    let d = easyModeDefaults(for: "bedrock")!
    #expect(d.embedModel == "amazon.nova-2-multimodal-embeddings-v1:0")
    #expect(d.genModel == "amazon.nova-micro-v1:0")
    #expect(d.dims == 1024)
}

@Test("OpenRouter easy mode matches Go CLI defaults")
func openrouterDefaults() {
    let d = easyModeDefaults(for: "openrouter")!
    #expect(d.embedModel == "nvidia/llama-nemotron-embed-vl-1b-v2:free")
    #expect(d.genModel == "google/gemma-4-31b-it:free")
    #expect(d.dims == 1024)
}

@Test("Ollama easy mode matches Go CLI defaults")
func ollamaDefaults() {
    let d = easyModeDefaults(for: "ollama")!
    #expect(d.embedModel == "nomic-embed-text")
    #expect(d.genModel == "qwen2.5:0.5b")
    #expect(d.dims == 768)
}

@Test("Unknown provider returns nil")
func unknownProvider() {
    #expect(easyModeDefaults(for: "unknown") == nil)
}

// MARK: - Credentials Validation Logic

func credentialsNextDisabled(provider: String, openrouterKey: String, ollamaStatus: String) -> Bool {
    switch provider {
    case "openrouter": return openrouterKey.trimmingCharacters(in: .whitespaces).isEmpty
    case "ollama": return ollamaStatus != "ready"
    default: return false // bedrock allows empty (uses default profile)
    }
}

@Test("OpenRouter requires non-empty API key")
func openrouterKeyRequired() {
    #expect(credentialsNextDisabled(provider: "openrouter", openrouterKey: "", ollamaStatus: "") == true)
    #expect(credentialsNextDisabled(provider: "openrouter", openrouterKey: "   ", ollamaStatus: "") == true)
    #expect(credentialsNextDisabled(provider: "openrouter", openrouterKey: "sk-or-123", ollamaStatus: "") == false)
}

@Test("Ollama requires ready status")
func ollamaRequiresReady() {
    #expect(credentialsNextDisabled(provider: "ollama", openrouterKey: "", ollamaStatus: "not-installed") == true)
    #expect(credentialsNextDisabled(provider: "ollama", openrouterKey: "", ollamaStatus: "not-running") == true)
    #expect(credentialsNextDisabled(provider: "ollama", openrouterKey: "", ollamaStatus: "checking") == true)
    #expect(credentialsNextDisabled(provider: "ollama", openrouterKey: "", ollamaStatus: "ready") == false)
}

@Test("Bedrock never blocks on credentials")
func bedrockAlwaysAllowed() {
    #expect(credentialsNextDisabled(provider: "bedrock", openrouterKey: "", ollamaStatus: "") == false)
}

// MARK: - Model Picker Logic

struct PickerLogicModel {
    let id: String
    let local: Bool
    let priceIn: Double?
    let priceOut: Double?
    let priceRequest: Double?
    let priceSource: String?
    let enabled: Bool?
    let benchmarkMs: Int64?
    let testMs: Int64?
}

func pickerEnableLabel(_ enabled: Bool?) -> String {
    guard let enabled else { return "Default" }
    return enabled ? "Enabled" : "Disabled"
}

func pickerPriceRank(_ model: PickerLogicModel) -> Double {
    let hasTokenPrice = (model.priceIn ?? 0) > 0 || (model.priceOut ?? 0) > 0
    let hasRequestPrice = (model.priceRequest ?? 0) > 0
    if model.local || (model.priceSource != nil && !hasTokenPrice && !hasRequestPrice) {
        return 0
    }
    if hasRequestPrice {
        return 1 + (model.priceRequest ?? 0)
    }
    if hasTokenPrice {
        return 10_000 + (model.priceIn ?? 0) + (model.priceOut ?? 0)
    }
    return Double.greatestFiniteMagnitude
}

func pickerFastestRank(_ model: PickerLogicModel) -> Int64 {
    model.benchmarkMs ?? model.testMs ?? Int64.max
}

func pickerActiveKinds(provider: String, embeddingModel: String, generationModel: String, modelProvider: String, modelID: String) -> [String] {
    guard provider == modelProvider else { return [] }
    var kinds: [String] = []
    if embeddingModel == modelID { kinds.append("embedding") }
    if generationModel == modelID { kinds.append("generation") }
    return kinds
}

@Test("Picker enable labels cover tri-state")
func pickerEnableLabels() {
    #expect(pickerEnableLabel(nil) == "Default")
    #expect(pickerEnableLabel(true) == "Enabled")
    #expect(pickerEnableLabel(false) == "Disabled")
}

@Test("Picker cheapest rank orders free, request, token, unknown")
func pickerCheapestRank() {
    let free = PickerLogicModel(id: "free", local: true, priceIn: nil, priceOut: nil, priceRequest: nil, priceSource: nil, enabled: nil, benchmarkMs: nil, testMs: nil)
    let request = PickerLogicModel(id: "request", local: false, priceIn: nil, priceOut: nil, priceRequest: 0.01, priceSource: "vendor", enabled: nil, benchmarkMs: nil, testMs: nil)
    let token = PickerLogicModel(id: "token", local: false, priceIn: 0.8, priceOut: 4.0, priceRequest: nil, priceSource: "vendor", enabled: nil, benchmarkMs: nil, testMs: nil)
    let unknown = PickerLogicModel(id: "unknown", local: false, priceIn: nil, priceOut: nil, priceRequest: nil, priceSource: nil, enabled: nil, benchmarkMs: nil, testMs: nil)
    let sorted = [unknown, token, request, free].sorted { pickerPriceRank($0) < pickerPriceRank($1) }.map(\.id)
    #expect(sorted == ["free", "request", "token", "unknown"])
}

@Test("Picker active kind derives from shared provider")
func pickerActiveKindsSharedProvider() {
    #expect(pickerActiveKinds(provider: "bedrock", embeddingModel: "embed", generationModel: "gen", modelProvider: "bedrock", modelID: "embed") == ["embedding"])
    #expect(pickerActiveKinds(provider: "bedrock", embeddingModel: "embed", generationModel: "gen", modelProvider: "openrouter", modelID: "gen").isEmpty)
}
