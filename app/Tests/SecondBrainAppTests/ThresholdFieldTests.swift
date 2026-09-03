import Testing
@testable import SecondBrain

/// The similarity-threshold field in the model picker must start empty for
/// every model, including one that carries a catalog recommendation. It used to
/// be prefilled with that recommendation, so Save with no edit wrote the
/// built-in value into the user catalog as a calibration the user never chose.
///
/// This asserts on DetailInputs.initial, the value resetDetailInputs actually
/// applies, so it pins the RESET rather than a constant: the previous helper
/// ignored its argument and returned "", which no change to the reset could have
/// falsified. The probe-selection assertion is what makes that real, since it
/// varies with the model.
@Test("The picker's detail reset starts the threshold empty and picks the probe by model type")
@MainActor
func detailResetStartsThresholdEmpty() {
    let recommended = catalogModelFixture(recommendedSimilarityThreshold: 0.25)
    let none = catalogModelFixture(recommendedSimilarityThreshold: nil)
    let generation = catalogModelFixture(recommendedSimilarityThreshold: nil, modelType: "generation")

    #expect(ModelCatalogPickerView.DetailInputs.initial(for: recommended).thresholdText == "")
    #expect(ModelCatalogPickerView.DetailInputs.initial(for: none).thresholdText == "")

    #expect(ModelCatalogPickerView.DetailInputs.initial(for: recommended).benchmarkProbeSelection == "embed")
    #expect(ModelCatalogPickerView.DetailInputs.initial(for: generation).benchmarkProbeSelection == "generate")
}

private func catalogModelFixture(recommendedSimilarityThreshold: Double?, modelType: String = "embedding") -> CatalogModelInfo {
    CatalogModelInfo(
        modelID: "amazon.nova-2-multimodal-embeddings-v1:0",
        name: "Nova 2 Multimodal Embeddings",
        provider: "bedrock",
        modelType: modelType,
        vendor: nil,
        vendorDisplay: nil,
        family: nil,
        versionSortKey: nil,
        dimensions: 1024,
        priceIn: nil,
        priceOut: nil,
        priceRequest: nil,
        priceSource: nil,
        reachable: nil,
        credentials: nil,
        rateLimitRPS: nil,
        rateLimitTPM: nil,
        priceOverride: nil,
        contextLen: nil,
        recommendedSimilarityThreshold: recommendedSimilarityThreshold,
        local: nil,
        tier: nil,
        invokeStrategy: nil,
        plane: nil,
        region: nil,
        enabled: nil,
        active: nil,
        configHint: nil,
        notes: nil,
        testedAt: nil,
        testLatencyMs: nil,
        testError: nil,
        testErrorCode: nil,
        benchmark: nil,
        compatible: nil,
        compatibilityReason: nil,
        recommended: nil,
        supportedDimensions: nil,
        working: nil
    )
}
