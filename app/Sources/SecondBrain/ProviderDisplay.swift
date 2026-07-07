import Foundation

/// Single source for provider display names. The CLI owns model identity
/// (vendor/family/compatible arrive over JSON); the only naming the app does
/// itself is this static provider-key to human-name mapping, shared by the
/// AI Hub cards and the Home AI card so the two can never disagree.
enum ProviderDisplay {
    static func name(_ raw: String) -> String {
        switch raw {
        case "bedrock": return "AWS Bedrock"
        case "openrouter": return "OpenRouter"
        case "ollama": return "Ollama (local)"
        case "llama-local": return "Local (llama.cpp)"
        default: return raw.capitalized
        }
    }
}
