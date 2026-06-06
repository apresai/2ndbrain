import Testing
@testable import SecondBrain

@Test("Primary SwiftUI AI views can be constructed")
@MainActor
func primaryViewsConstruct() {
    let model = CatalogModelInfo(
        modelID: "view.test.model",
        name: "View Test Model",
        provider: "bedrock",
        modelType: "generation",
        vendor: nil,
        vendorDisplay: nil,
        family: nil,
        versionSortKey: nil,
        dimensions: nil,
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
        recommendedSimilarityThreshold: nil,
        local: nil,
        tier: nil,
        invokeStrategy: nil,
        enabled: nil,
        active: nil,
        configHint: nil,
        notes: nil,
        testedAt: nil,
        testLatencyMs: nil,
        testError: nil,
        benchmark: nil,
        compatible: nil,
        compatibilityReason: nil
    )

    let picker = ModelCatalogPickerView(
        models: [model],
        aiStatus: nil,
        initialType: nil,
        initialModelID: nil,
        onClose: {},
        onReload: {}
    )
    let hub = AIHubView(onClose: {})

    #expect(String(describing: type(of: picker)) == "ModelCatalogPickerView")
    #expect(String(describing: type(of: hub)) == "AIHubView")
}
