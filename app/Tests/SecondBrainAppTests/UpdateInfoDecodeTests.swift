import Foundation
import Testing
@testable import SecondBrain

// Pins the Swift UpdateInfo decoder to the exact JSON the Go `2nb update --json`
// (cli.UpdateStatus) emits. If the Go field names/casing drift, these fail.

@Test("UpdateInfo decodes an update-available payload")
func updateInfoDecodesAvailable() throws {
    let json = """
    { "current": "0.10.0", "latest": "v0.10.1", "update_available": true, "checked": true }
    """.data(using: .utf8)!

    let info = try JSONDecoder().decode(UpdateInfo.self, from: json)
    #expect(info.current == "0.10.0")
    #expect(info.latest == "v0.10.1")
    #expect(info.updateAvailable)
    #expect(info.checked)
    #expect(info.detail == nil)
}

@Test("UpdateInfo decodes an up-to-date payload")
func updateInfoDecodesUpToDate() throws {
    let json = """
    { "current": "0.10.1", "latest": "v0.10.1", "update_available": false, "checked": true }
    """.data(using: .utf8)!

    let info = try JSONDecoder().decode(UpdateInfo.self, from: json)
    #expect(!info.updateAvailable)
    #expect(info.checked)
}

@Test("UpdateInfo decodes an offline payload (no latest, with detail)")
func updateInfoDecodesOffline() throws {
    let json = """
    { "current": "0.10.1", "update_available": false, "checked": false, "detail": "couldn't check for updates (offline?): ..." }
    """.data(using: .utf8)!

    let info = try JSONDecoder().decode(UpdateInfo.self, from: json)
    #expect(info.latest == nil)
    #expect(!info.checked)
    #expect(info.detail?.isEmpty == false)
}
