import Testing
@testable import SecondBrain

/// The similarity-threshold field in the model picker must start empty for
/// every model, including one that carries a catalog recommendation. It used to
/// be prefilled with that recommendation, so Save with no edit wrote the
/// built-in value into the user catalog as a calibration the user never chose.
@Test("The picker's threshold field starts empty even for a model with a recommendation")
@MainActor
func thresholdFieldStartsEmpty() {
    let recommended = catalogModelFixture(recommendedSimilarityThreshold: 0.25)
    let none = catalogModelFixture(recommendedSimilarityThreshold: nil)

    #expect(ModelCatalogPickerView.initialThresholdText(for: recommended) == "")
    #expect(ModelCatalogPickerView.initialThresholdText(for: none) == "")
}

private func catalogModelFixture(recommendedSimilarityThreshold: Double?) -> CatalogModelInfo {
    CatalogModelInfo(
        modelID: "amazon.nova-2-multimodal-embeddings-v1:0",
        name: "Nova 2 Multimodal Embeddings",
        provider: "bedrock",
        modelType: "embedding",
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
