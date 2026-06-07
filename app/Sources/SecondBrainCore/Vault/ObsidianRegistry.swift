import Foundation
import os

/// Reads Obsidian's own vault registry — `~/Library/Application Support/obsidian/obsidian.json`
/// on macOS — the source of truth for which folders Obsidian treats as vaults
/// and which one is currently open. 2ndbrain binds to the open vault so the
/// Obsidian vault and the 2nb vault stay joined at the hip.
///
/// The file is an internal Obsidian format with no published schema, so decoding
/// is deliberately defensive: a missing `vaults` key, unknown extra fields, or
/// entries without `ts`/`open` degrade to empty/`0`/`false` rather than failing
/// `load()`. Callers fall back to a manual "Open Vault" when it can't be read.
public struct ObsidianRegistry: Sendable, Equatable {
    public struct Vault: Sendable, Equatable {
        public let id: String
        public let path: String
        /// Last-opened time as Obsidian's epoch-millis timestamp (0 if absent).
        public let ts: Int
        public let open: Bool

        public init(id: String, path: String, ts: Int, open: Bool) {
            self.id = id
            self.path = path
            self.ts = ts
            self.open = open
        }

        public var url: URL { URL(fileURLWithPath: path) }
        public var name: String { url.lastPathComponent }

        /// Whether the vault's folder still exists on disk. A registry entry can
        /// go stale if the folder was moved or deleted out from under Obsidian.
        public var exists: Bool {
            var isDir: ObjCBool = false
            return FileManager.default.fileExists(atPath: url.path, isDirectory: &isDir) && isDir.boolValue
        }
    }

    /// All known vaults, sorted most-recently-opened first.
    public let vaults: [Vault]

    public init(vaults: [Vault]) {
        self.vaults = vaults.sorted { $0.ts > $1.ts }
    }

    /// Decode from the raw bytes of `obsidian.json`.
    public init(data: Data) throws {
        let root = try JSONDecoder().decode(Root.self, from: data)
        let parsed = (root.vaults ?? [:]).map { id, entry in
            Vault(id: id, path: entry.path, ts: entry.ts ?? 0, open: entry.open ?? false)
        }
        self.init(vaults: parsed)
    }

    /// The vault Obsidian currently has open. If none is flagged `open`, falls
    /// back to the most recently opened one. `nil` when the registry lists none.
    public var openVault: Vault? {
        vaults.first(where: { $0.open }) ?? vaults.first
    }

    /// The registry entry whose path matches `url` (path-normalized), if any.
    public func vault(at url: URL) -> Vault? {
        let target = ObsidianRegistry.normalizedPath(url)
        return vaults.first { ObsidianRegistry.normalizedPath($0.url) == target }
    }

    /// Load and decode the registry from the standard macOS location. Returns
    /// `nil` if the file is absent or can't be parsed (Obsidian not installed,
    /// or an unrecognized format).
    public static func load(at path: URL = ObsidianRegistry.defaultLocation) -> ObsidianRegistry? {
        // An absent / unreadable file is the normal "Obsidian not installed"
        // case — silent. A present-but-unparseable file is worth a diagnostic so
        // a user can tell why the app didn't pick up their vault.
        guard let data = try? Data(contentsOf: path) else { return nil }
        do {
            return try ObsidianRegistry(data: data)
        } catch {
            Logger(subsystem: "dev.apresai.2ndbrain", category: "obsidian-registry")
                .error("obsidian.json present but unparseable at \(path.path, privacy: .public): \(error.localizedDescription, privacy: .public)")
            return nil
        }
    }

    /// `~/Library/Application Support/obsidian/obsidian.json`.
    public static var defaultLocation: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/obsidian/obsidian.json")
    }

    /// Symlink-resolved, standardized absolute path with any trailing slash
    /// removed, so two URLs pointing at the same folder compare equal regardless
    /// of trailing-slash OR symlink-vs-real-path differences. Without resolving
    /// symlinks, a vault opened via a symlinked path would spuriously look
    /// "different" from the same vault recorded by its real path in obsidian.json.
    public static func normalizedPath(_ url: URL) -> String {
        var p = url.resolvingSymlinksInPath().standardizedFileURL.path
        while p.count > 1, p.hasSuffix("/") { p.removeLast() }
        return p
    }

    // MARK: - Decoding

    private struct Root: Decodable {
        let vaults: [String: Entry]?
    }

    private struct Entry: Decodable {
        let path: String
        let ts: Int?
        let open: Bool?
    }
}
