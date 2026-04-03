import Foundation

public final class VaultManager: @unchecked Sendable {
    public let rootURL: URL
    public let dotDirURL: URL

    public init(rootURL: URL) {
        self.rootURL = rootURL
        self.dotDirURL = rootURL.appendingPathComponent(".2ndbrain")
    }

    public var indexDBPath: String {
        dotDirURL.appendingPathComponent("index.db").path
    }

    public var isInitialized: Bool {
        FileManager.default.fileExists(atPath: dotDirURL.path)
    }

    /// List all markdown files in the vault.
    public func listMarkdownFiles() -> [URL] {
        let fm = FileManager.default
        guard let enumerator = fm.enumerator(
            at: rootURL,
            includingPropertiesForKeys: [.isRegularFileKey],
            options: [.skipsHiddenFiles, .skipsPackageDescendants]
        ) else { return [] }

        var files: [URL] = []
        for case let url as URL in enumerator {
            if url.pathExtension.lowercased() == "md" {
                files.append(url)
            }
        }
        return files.sorted { $0.lastPathComponent < $1.lastPathComponent }
    }

    /// Get the vault-relative path for a file URL.
    public func relativePath(for url: URL) -> String {
        let root = rootURL.path
        let full = url.path
        if full.hasPrefix(root) {
            var rel = String(full.dropFirst(root.count))
            if rel.hasPrefix("/") { rel = String(rel.dropFirst()) }
            return rel
        }
        return url.lastPathComponent
    }
}
