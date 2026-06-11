import Foundation

/// Reads the installed 2ndbrain Obsidian plugin's version out of a vault, so
/// the dashboard can offer Install/Update without shelling out. The actual
/// install is delegated to `2nb plugin install`; this type only inspects
/// `<vault>/.obsidian/plugins/obsidian-2ndbrain/manifest.json`.
public enum ObsidianPlugin {
    public static let pluginDirName = "obsidian-2ndbrain"

    public static func manifestURL(vaultRoot: URL) -> URL {
        vaultRoot
            .appendingPathComponent(".obsidian")
            .appendingPathComponent("plugins")
            .appendingPathComponent(pluginDirName)
            .appendingPathComponent("manifest.json")
    }

    /// The installed plugin's version, or nil when the plugin is not
    /// installed or its manifest is unreadable. The CLI's `plugin install`
    /// writes the manifest last, so a present manifest means a complete
    /// bundle.
    public static func installedVersion(vaultRoot: URL) -> String? {
        guard let data = try? Data(contentsOf: manifestURL(vaultRoot: vaultRoot)) else { return nil }
        return parseManifestVersion(data)
    }

    /// Extracts the version field from a plugin manifest.json payload.
    /// Pure and unit-testable; nil on malformed JSON or a missing/empty
    /// version.
    public static func parseManifestVersion(_ data: Data) -> String? {
        struct Manifest: Decodable { let version: String? }
        guard let manifest = try? JSONDecoder().decode(Manifest.self, from: data),
              let version = manifest.version, !version.isEmpty else { return nil }
        return version
    }
}
