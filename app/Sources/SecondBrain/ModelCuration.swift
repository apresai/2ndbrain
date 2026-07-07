import Foundation

/// Pure partitioning logic for the AI Hub's curated-by-default catalog view.
///
/// The full catalog (especially after Discover) is a wall of unvalidated
/// vendor listings that used to render nearly identically to the shipped,
/// tested models. The default view shows only models a user can trust:
/// curated (`recommended`, computed by the CLI), shipped-verified, or ones
/// they tested themselves — with everything else behind an explicit
/// "Show all models" toggle that demotes untested discoveries into their own
/// collapsed group.
enum ModelCuration {
    /// True when a model belongs in the default (curated) view:
    /// on the ACTIVE provider, not statically incompatible, and either
    /// recommended / shipped-verified / passed a user test. The currently
    /// active models are always included so the view never hides what is
    /// actually configured. On a pre-curation CLI (`recommended` absent
    /// everywhere) this degrades to verified + tested-by-you.
    static func isCurated(_ m: CatalogModelInfo, activeProvider: String?, activeIDs: Set<String>) -> Bool {
        if activeIDs.contains(m.provider + "|" + m.modelID) { return true }
        if let provider = activeProvider, m.provider != provider { return false }
        if m.compatible == false { return false }
        let testedOK = (m.testedAt?.isEmpty == false) && (m.testError ?? "").isEmpty
        return m.recommended == true || m.tier == "verified" || testedOK
    }

    /// Splits models into (curated, rest) per `isCurated`.
    static func partition(_ models: [CatalogModelInfo], activeProvider: String?, activeIDs: Set<String>) -> (curated: [CatalogModelInfo], rest: [CatalogModelInfo]) {
        var curated: [CatalogModelInfo] = []
        var rest: [CatalogModelInfo] = []
        for m in models {
            if isCurated(m, activeProvider: activeProvider, activeIDs: activeIDs) {
                curated.append(m)
            } else {
                rest.append(m)
            }
        }
        return (curated, rest)
    }

    /// True for rows the all-models view demotes into the collapsed
    /// "Untested — discovered from provider" group: unverified tier and
    /// never tested by the user.
    static func isDemoted(_ m: CatalogModelInfo) -> Bool {
        let untested = (m.testedAt ?? "").isEmpty
        return (m.tier == "unverified" || m.tier == nil) && untested
    }

    /// The active model IDs (provider|id keys) from an AI status, used to
    /// force-include the configured models in the curated view.
    static func activeIDs(_ status: AIStatusInfo?) -> Set<String> {
        guard let status else { return [] }
        var ids: Set<String> = []
        if !status.embeddingModel.isEmpty { ids.insert(status.provider + "|" + status.embeddingModel) }
        if !status.genModel.isEmpty { ids.insert(status.provider + "|" + status.genModel) }
        if let rerank = status.rerankModel, !rerank.isEmpty { ids.insert(status.provider + "|" + rerank) }
        return ids
    }
}
