import Foundation

/// The AWS regions offered in the Settings AI page, plus the guard that keeps a
/// one-click picker from quietly breaking a working vault.
///
/// Region was a free-text field nobody edited. Turning it into a dropdown makes
/// it a one-click change, which is the point — and also the hazard: several
/// models this product ships by default are pinned to a single region, so a
/// casual pick can take embeddings offline. A caption is not enough for that;
/// `constraint(for:)` exists so the UI can refuse-and-explain instead.
enum BedrockRegions {
    struct Region: Identifiable, Equatable {
        let id: String // the AWS region code, e.g. "us-east-1"
        let label: String // human name, e.g. "N. Virginia"

        var display: String { "\(id) · \(label)" }
    }

    /// The regions worth putting one click away. Bedrock runs in far more, so
    /// the picker pairs this with a free-text row rather than pretending the
    /// list is exhaustive. US-first because the default embedding model is
    /// us-east-1-only.
    static let common: [Region] = [
        Region(id: "us-east-1", label: "N. Virginia"),
        Region(id: "us-west-2", label: "Oregon"),
        Region(id: "us-east-2", label: "Ohio"),
        Region(id: "eu-west-1", label: "Ireland"),
        Region(id: "eu-central-1", label: "Frankfurt"),
        Region(id: "eu-west-2", label: "London"),
        Region(id: "ap-northeast-1", label: "Tokyo"),
        Region(id: "ap-southeast-1", label: "Singapore"),
        Region(id: "ap-southeast-2", label: "Sydney"),
        Region(id: "ca-central-1", label: "Canada Central"),
    ]

    /// Model IDs that only answer in one region, whatever `ai.bedrock.region`
    /// says. Picking a different region does not relocate them; it takes them
    /// offline. Both are 2ndbrain defaults, so this is the common case, not an
    /// exotic one.
    static let inRegionOnly: [String: String] = [
        "amazon.nova-2-multimodal-embeddings-v1:0": "us-east-1",
        "cohere.rerank-v3-5:0": "us-east-1",
    ]

    /// Models that ignore `ai.bedrock.region` entirely because they live on the
    /// Bedrock mantle plane, which pins an endpoint per model. Worth saying out
    /// loud: otherwise a user changes the region, watches Grok keep working,
    /// and reasonably concludes the setting does nothing.
    static let regionPinnedMantle: [String: String] = [
        "openai.gpt-5.5": "us-east-2",
        "xai.grok-4.3": "us-west-2",
    ]

    /// What a region change would break, or nil when it is safe.
    ///
    /// Only the embedding model is treated as blocking. An embedding model that
    /// stops answering makes every stored vector unreachable and needs a full
    /// re-embed to recover, whereas a generation model that goes quiet is an
    /// immediately obvious, immediately reversible mistake.
    static func constraint(forRegion region: String, embeddingModel: String) -> String? {
        guard let required = inRegionOnly[embeddingModel] else { return nil }
        guard region != required else { return nil }
        return "\(embeddingModel) only answers in \(required). Switching to \(region) will stop embeddings and semantic search from working until you either switch back or pick a different embedding model and re-embed the vault."
    }

    /// A note for the region row when the active generation model ignores the
    /// setting. Not a warning: nothing is breaking, the field is simply not in
    /// charge of that model.
    static func mantleNote(generationModel: String) -> String? {
        guard let pinned = regionPinnedMantle[generationModel] else { return nil }
        return "\(generationModel) runs on the Bedrock mantle plane, pinned to \(pinned). This region setting does not apply to it."
    }

    /// True when the code is one this picker lists, so the UI knows whether to
    /// select a row or fall back to the free-text field.
    static func isCommon(_ code: String) -> Bool {
        common.contains { $0.id == code }
    }
}
