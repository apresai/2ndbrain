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

@Test("UpdateInfo decodes the app/plugin ProductState fields")
func updateInfoDecodesProductStates() throws {
    // The real `2nb update --json` shape once app/plugin parity was added: the
    // CLI is current, the app + plugin are behind.
    let json = """
    {
      "current": "0.10.4", "latest": "v0.10.4", "update_available": false, "checked": true,
      "app":    { "name": "app",    "status": "outdated", "installed": true,  "version": "0.10.3", "update_available": true, "fix": "brew upgrade --cask apresai/tap/secondbrain" },
      "plugin": { "name": "plugin", "status": "outdated", "installed": true,  "version": "0.10.3", "update_available": true, "fix": "2nb plugin install" }
    }
    """.data(using: .utf8)!

    let info = try JSONDecoder().decode(UpdateInfo.self, from: json)
    #expect(!info.updateAvailable) // CLI itself is current
    #expect(info.app?.version == "0.10.3")
    #expect(info.app?.updateAvailable == true)
    #expect(info.plugin?.status == "outdated")
    #expect(info.plugin?.fix == "2nb plugin install")
}

@Test("UpdateInfo tolerates a pre-doctor payload with no app/plugin fields")
func updateInfoDecodesWithoutProductStates() throws {
    // An older CLI emits no app/plugin keys; they must decode to nil so the view
    // falls back to local comparison rather than failing to decode.
    let json = """
    { "current": "0.10.1", "latest": "v0.10.1", "update_available": false, "checked": true }
    """.data(using: .utf8)!

    let info = try JSONDecoder().decode(UpdateInfo.self, from: json)
    #expect(info.app == nil)
    #expect(info.plugin == nil)
}
