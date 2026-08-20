import Foundation
import Testing
@testable import SecondBrain

// MARK: - StaleVerdicts

@Test("isStale: true only when the key changed after the last verification")
func staleVerdictsTable() {
    // Key changed AFTER the last verify: stale.
    #expect(StaleVerdicts.isStale(
        lastVerifiedAt: "2026-08-20T17:07:03Z",
        tokenUpdatedAt: "2026-08-20T18:00:00Z"
    ))
    // Verified after the key change: fresh.
    #expect(!StaleVerdicts.isStale(
        lastVerifiedAt: "2026-08-20T19:00:00Z",
        tokenUpdatedAt: "2026-08-20T18:00:00Z"
    ))
    // Equal timestamps: not stale.
    #expect(!StaleVerdicts.isStale(
        lastVerifiedAt: "2026-08-20T18:00:00Z",
        tokenUpdatedAt: "2026-08-20T18:00:00Z"
    ))
    // Nil / empty / unparseable on either side degrades to no banner —
    // an older CLI without the stamp must never produce a false alarm.
    #expect(!StaleVerdicts.isStale(lastVerifiedAt: nil, tokenUpdatedAt: "2026-08-20T18:00:00Z"))
    #expect(!StaleVerdicts.isStale(lastVerifiedAt: "2026-08-20T18:00:00Z", tokenUpdatedAt: nil))
    #expect(!StaleVerdicts.isStale(lastVerifiedAt: "", tokenUpdatedAt: ""))
    #expect(!StaleVerdicts.isStale(lastVerifiedAt: "not-a-date", tokenUpdatedAt: "2026-08-20T18:00:00Z"))
}

// MARK: - CredentialChipPresentation

@Test("credentials chip: unknown, not set, and identified-key states")
func credentialChipStates() throws {
    #expect(CredentialChipPresentation.chip(nil) == .init(label: "API key…", warning: false))

    let unset = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data("""
    {"path":"/tmp/bedrock.json","token_set":false,"token_source":"none"}
    """.utf8))
    #expect(CredentialChipPresentation.chip(unset) == .init(label: "API key · not set", warning: true))

    let file = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data("""
    {"path":"/tmp/bedrock.json","token_set":true,"token_suffix":"RT0=","token_source":"file"}
    """.utf8))
    #expect(CredentialChipPresentation.chip(file) == .init(label: "API key ····RT0= (file)", warning: false))

    // Suffix withheld (short token / older CLI): still identified as set.
    let short = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data("""
    {"path":"/tmp/bedrock.json","token_set":true,"token_source":"env"}
    """.utf8))
    #expect(CredentialChipPresentation.chip(short) == .init(label: "API key set (env)", warning: false))
}

// MARK: - RegionIncludeSelection

@Test("region include choices exclude the primary and keep stored customs selectable")
func regionIncludeChoices() {
    let choices = RegionIncludeSelection.choices(
        common: ["us-east-1", "us-west-2", "us-east-2"],
        primary: "us-east-1",
        stored: ["eu-north-1", "us-west-2"]
    )
    #expect(choices == ["us-west-2", "us-east-2", "eu-north-1"])
    // A stored value equal to primary never shows (it is always included).
    let dedup = RegionIncludeSelection.choices(
        common: ["us-east-1"],
        primary: "us-east-1",
        stored: ["us-east-1"]
    )
    #expect(dedup.isEmpty)
}

@Test("region include toggling adds and removes")
func regionIncludeToggle() {
    #expect(RegionIncludeSelection.toggling([], region: "us-west-2") == ["us-west-2"])
    #expect(RegionIncludeSelection.toggling(["us-west-2"], region: "us-west-2") == [])
    #expect(RegionIncludeSelection.toggling(["us-west-2"], region: "us-east-2") == ["us-west-2", "us-east-2"])
}

@Test("region summary and Validate suffix name the scope honestly")
func regionSummaryCopy() {
    #expect(RegionIncludeSelection.summary(primary: "us-east-1", included: []) == "Verification probes only us-east-1.")
    let multi = RegionIncludeSelection.summary(primary: "us-east-1", included: ["us-west-2"])
    #expect(multi.contains("us-east-1 first"))
    #expect(multi.contains("us-west-2"))
    #expect(RegionIncludeSelection.validateSuffix(regionCount: 1) == "")
    #expect(RegionIncludeSelection.validateSuffix(regionCount: 3) == " across 3 regions")
}
