import Foundation

/// The Google Cloud regions offered in the Settings AI page, plus safety guards
/// for Vertex AI model availability.
enum GoogleRegions {
    struct Region: Identifiable, Equatable {
        let id: String // GCP region code, e.g. "us-central1"
        let label: String // human name, e.g. "Iowa"

        var display: String { "\(id) · \(label)" }
    }

    /// The primary Google Cloud regions offering Vertex AI foundation models (Gemini).
    static let common: [Region] = [
        Region(id: "us-central1", label: "Iowa (Default)"),
        Region(id: "us-east4", label: "N. Virginia"),
        Region(id: "us-west1", label: "Oregon"),
        Region(id: "europe-west1", label: "Belgium"),
        Region(id: "europe-west4", label: "Netherlands"),
        Region(id: "asia-northeast1", label: "Tokyo"),
        Region(id: "asia-southeast1", label: "Singapore"),
    ]

    static let defaultRegion = "us-central1"

    /// The risk of switching to a region.
    enum ChangeRisk: Equatable {
        /// The active embedding model can serve this region.
        case safe
        /// Known to break: the model answers only in another region.
        case breaks(String)
        /// The active embedding model is unknown, so nothing can be verified.
        case unverifiable(String)
    }

    /// What switching to `region` would do to the active embedding model.
    static func risk(forRegion region: String, embeddingModel: String?) -> ChangeRisk {
        guard let embeddingModel, !embeddingModel.isEmpty else {
            return .unverifiable("2ndbrain could not read your active embedding model, so it cannot check whether \(region) can serve it. Ensure your chosen GCP region has Vertex AI foundation model access enabled.")
        }
        // Most Gemini models (gemini-embedding-2, gemini-embedding-001, gemini-4-hydrogen, gemini-4-argon, gemini-4-carbon, etc.)
        // are globally available across standard Vertex AI regions.
        return .safe
    }

    /// True when the region code is one this picker lists.
    static func isCommon(_ code: String) -> Bool {
        common.contains { $0.id == code }
    }
}
