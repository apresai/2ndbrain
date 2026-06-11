import Testing
import Foundation
import SecondBrainCore

@Test("parseManifestVersion reads version, rejects missing/empty/garbage")
func parseManifestVersion() {
    #expect(ObsidianPlugin.parseManifestVersion(Data(#"{"id":"obsidian-2ndbrain","version":"0.8.0"}"#.utf8)) == "0.8.0")
    #expect(ObsidianPlugin.parseManifestVersion(Data(#"{"id":"x"}"#.utf8)) == nil)
    #expect(ObsidianPlugin.parseManifestVersion(Data(#"{"version":""}"#.utf8)) == nil)
    #expect(ObsidianPlugin.parseManifestVersion(Data("not json".utf8)) == nil)
    // A JSON-number version (hand-edited manifest) decodes as nil, not a crash.
    #expect(ObsidianPlugin.parseManifestVersion(Data(#"{"version":1.2}"#.utf8)) == nil)
}

@Test("installedVersion is nil for a vault without the plugin, set once a manifest exists")
func installedVersion() throws {
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-plugin-test-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: root) }

    #expect(ObsidianPlugin.installedVersion(vaultRoot: root) == nil)

    let manifest = ObsidianPlugin.manifestURL(vaultRoot: root)
    try FileManager.default.createDirectory(
        at: manifest.deletingLastPathComponent(), withIntermediateDirectories: true)
    try Data(#"{"version":"0.7.0"}"#.utf8).write(to: manifest)
    #expect(ObsidianPlugin.installedVersion(vaultRoot: root) == "0.7.0")

    // Corrupt manifest reads as not-installed rather than crashing the row.
    try Data("broken".utf8).write(to: manifest)
    #expect(ObsidianPlugin.installedVersion(vaultRoot: root) == nil)
}

@Test("manifestURL points inside .obsidian/plugins/obsidian-2ndbrain")
func manifestURLShape() {
    let url = ObsidianPlugin.manifestURL(vaultRoot: URL(fileURLWithPath: "/v"))
    #expect(url.path == "/v/.obsidian/plugins/obsidian-2ndbrain/manifest.json")
}
