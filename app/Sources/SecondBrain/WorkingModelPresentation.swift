import Foundation

/// Decision-line for the simple Models-tab pickers.
///
/// The CLI already ships price, last probe latency, and access codes on
/// `CatalogModelInfo`. This layer only composes them. It never treats
/// embed-only `benchmark.quality_score` as a reason to pick a generation
/// model, and it never claims a model "works" just because it is builtin-verified.
enum WorkingModelPresentation {
    static let mantleInvokeStrategy = "bedrock_mantle_responses"

    /// One picker row: `Haiku 4.5 · $1/$5 per M · 412ms · shipped default`.
    static func rowLine(_ model: CatalogModelInfo, why: String?) -> String {
        var parts: [String] = [displayName(model)]
        if let price = priceLabel(model) {
            parts.append(price)
        }
        if let latency = model.testLatencyMs, latency > 0 {
            parts.append("\(latency)ms")
        }
        if let why, !why.isEmpty {
            parts.append(why)
        }
        return parts.joined(separator: " · ")
    }

    static func displayName(_ model: CatalogModelInfo) -> String {
        let name = model.name.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? model.modelID : name
    }

    static func priceLabel(_ model: CatalogModelInfo) -> String? {
        if let req = model.priceRequest, req > 0 {
            return String(format: "$%.4f/request", req)
        }
        if let pIn = model.priceIn, pIn > 0 {
            if let pOut = model.priceOut, pOut > 0 {
                return String(format: "$%.2f/$%.2f per M", pIn, pOut)
            }
            return String(format: "$%.3f/M", pIn)
        }
        if model.local == true {
            return "free"
        }
        return nil
    }

    /// Thinking Off/Low/High is mantle-only. Anthropic Converse ignores the field.
    static func showsThinking(_ model: CatalogModelInfo) -> Bool {
        model.invokeStrategy == mantleInvokeStrategy
    }

    /// Models the simple pickers offer. Prefers the CLI `working` flag when
    /// present; falls back to recommended ∪ active so a pre-flag CLI still
    /// has something to show, with copy telling the user to Validate.
    ///
    /// Actives stay selectable even when `working == false` (failed probe).
    /// `hasWorkingFlag` means any row decoded the key — including all-false —
    /// so a missing `working:true` must not silently fall back to recommended
    /// and put unprobed Sonnet 5 in Answers.
    static func pickerModels(
        _ models: [CatalogModelInfo],
        type: String,
        activeID: String?,
        hasWorkingFlag: Bool
    ) -> [CatalogModelInfo] {
        let typed = models.filter { $0.modelType == type && $0.compatible != false }
        if hasWorkingFlag {
            return typed.filter { $0.working == true || $0.modelID == activeID }
        }
        return typed.filter { $0.recommended == true || $0.modelID == activeID || $0.active == true }
    }

    static func failedModels(_ models: [CatalogModelInfo], type: String, provider: String? = nil) -> [CatalogModelInfo] {
        models.filter {
            $0.modelType == type
                && (provider == nil || $0.provider == provider)
                && $0.testedAt?.isEmpty == false
                && !($0.testError ?? "").isEmpty
        }
    }

    /// Disclosure title for probe failures. These are invoke failures, not
    /// enable/policy failures — "couldn't be enabled" was the wrong sentence.
    static func failedDisclosureTitle(count: Int) -> String {
        count == 1 ? "1 failed validation" : "\(count) failed validation"
    }

    /// CLI supports `working` when any row decoded the key, including
    /// `working: false`. Do not use `contains { $0.working == true }` —
    /// a catalog of all-false still means "this CLI emits the flag".
    static func hasWorkingFlag(_ models: [CatalogModelInfo]) -> Bool {
        models.contains { $0.working != nil }
    }

    /// True when no catalog row carries a recorded probe (`tested_at`).
    /// Untested actives are still `working: true` on a fresh vault; a
    /// failed active is `working: false`.
    static func nothingProbed(_ models: [CatalogModelInfo]) -> Bool {
        !models.contains { ($0.testedAt?.isEmpty == false) }
    }

    /// True when every `working: true` row is one of the current actives.
    /// Fresh vaults look like this because untested actives are working;
    /// after a real Validate, non-active passing models appear.
    static func onlyActivesAreWorking(_ models: [CatalogModelInfo], activeIDs: Set<String>) -> Bool {
        let working = models.filter { $0.working == true }
        guard !working.isEmpty else { return false }
        return working.allSatisfy { activeIDs.contains($0.modelID) }
    }

    /// Show "Validate to see which models work…" when the working flag is
    /// missing (pre-flag CLI) or when only untested actives are working.
    static func shouldNudgeValidate(_ models: [CatalogModelInfo], activeIDs: Set<String>) -> Bool {
        if !hasWorkingFlag(models) { return true }
        return onlyActivesAreWorking(models, activeIDs: activeIDs) && nothingProbed(models)
    }

    /// One-line working-set summary for the Testing tab's Validate pane:
    /// "Working set: 3 answers models, 1 search model" — the models this
    /// account has proven it can invoke (the CLI `working` flag), in the
    /// pickers' Answers/Search vocabulary. A pre-flag CLI or an unvalidated
    /// account gets a validate nudge instead of a fake zero.
    static func workingSetSummary(_ models: [CatalogModelInfo], provider: String, activeIDs: Set<String>) -> String {
        guard hasWorkingFlag(models) else {
            return "Working set unknown on this CLI version — run Validate."
        }
        let ofProvider = models.filter { $0.provider == provider }
        let gen = ofProvider.filter { $0.modelType == "generation" && $0.working == true }.count
        let embed = ofProvider.filter { $0.modelType == "embedding" && $0.working == true }.count
        if gen == 0 && embed == 0 {
            return "No working models yet — run Validate."
        }
        var line = "Working set: \(gen) answers model\(gen == 1 ? "" : "s"), \(embed) search model\(embed == 1 ? "" : "s")."
        if shouldNudgeValidate(models, activeIDs: activeIDs) {
            line += " Run Validate to probe the rest."
        }
        return line
    }

    static func why(
        _ model: CatalogModelInfo,
        isShippedDefault: Bool,
        cheapestID: String?,
        fastestID: String?
    ) -> String {
        if let code = model.testErrorCode, !code.isEmpty,
           let badge = ModelAccessPresentation.guidance(
                code: code,
                provider: model.provider,
                remediation: nil
           )?.badge {
            return badge
        }
        if !(model.testError ?? "").isEmpty {
            return "failed"
        }
        if isShippedDefault {
            return "shipped default"
        }
        if model.modelID == fastestID {
            return "fastest tested"
        }
        if model.modelID == cheapestID {
            return "cheapest"
        }
        return ""
    }

    static func cheapestID(in models: [CatalogModelInfo]) -> String? {
        models.min(by: { priceRank($0) < priceRank($1) })?.modelID
    }

    static func fastestID(in models: [CatalogModelInfo]) -> String? {
        let withLatency = models.filter { ($0.testLatencyMs ?? 0) > 0 }
        return withLatency.min(by: { ($0.testLatencyMs ?? .max) < ($1.testLatencyMs ?? .max) })?.modelID
    }

    /// Lower is cheaper. Request-priced models compare as USD/request * 1e6 so
    /// they still sort against per-million token prices without pretending
    /// they are the same unit in the label.
    static func priceRank(_ model: CatalogModelInfo) -> Double {
        if let req = model.priceRequest, req > 0 { return req * 1_000_000 }
        let inn = model.priceIn ?? 0
        let out = model.priceOut ?? 0
        if inn > 0 || out > 0 { return inn + out }
        return Double.greatestFiniteMagnitude
    }
}
