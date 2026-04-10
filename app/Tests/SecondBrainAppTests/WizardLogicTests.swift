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
